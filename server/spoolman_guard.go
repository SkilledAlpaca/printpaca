package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The Spoolman proxy is the only gate in front of Spoolman, which has no authentication of its own. Reads
// need the "filament" grant (requirePermission, in registerProxies). Writes are admin-only and go through
// this guard, which allows exactly three write shapes, each with a field allowlist and a size cap:
//
//	POST  /api/v1/spool        create a spool
//	PATCH /api/v1/spool/{id}   update a spool; {id} is digits only
//
// Every other method and path is rejected before any upstream call.

// spoolmanAllMethods is the union of methods any Spoolman route accepts. The per-path map below narrows it.
var spoolmanAllMethods = map[string]bool{
	http.MethodGet: true, http.MethodHead: true, http.MethodPost: true, http.MethodPatch: true,
}

// spoolmanWriteMaxBytes caps a write body. Real bodies are a few hundred bytes.
const spoolmanWriteMaxBytes = 16 * 1024

// spoolIDPath matches the single-spool path. The id is digits only with no leading zero, so no encoded or
// traversal-shaped segment can match.
var spoolIDPath = regexp.MustCompile(`^/api/v1/spool/[1-9][0-9]{0,11}$`)

// spoolmanRouteMethods returns the methods allowed on escapedTrimmed (the path with the mount prefix removed),
// or nil when the path is not on the allowlist. A per-path map: POST is not allowed on /api/v1/vendor.
func spoolmanRouteMethods(escapedTrimmed string) map[string]bool {
	switch {
	case escapedTrimmed == "/api/v1/spool":
		return map[string]bool{http.MethodGet: true, http.MethodHead: true, http.MethodPost: true}
	case spoolIDPath.MatchString(escapedTrimmed):
		return map[string]bool{http.MethodGet: true, http.MethodHead: true, http.MethodPatch: true}
	case escapedTrimmed == "/api/v1/filament", escapedTrimmed == "/api/v1/vendor":
		return map[string]bool{http.MethodGet: true, http.MethodHead: true}
	}
	return nil
}

func isSpoolmanWrite(method string) bool {
	return method != http.MethodGet && method != http.MethodHead
}

// guardSpoolman enforces the route table, the admin-only write rule, and the body allowlist before inner (the
// reverse proxy) sees the request. Order: traversal check, path allowlist (403), method (405), then for
// writes the admin role (403), demo block (403), JSON content type (415), size cap (413), and fields (400).
// The body forwarded upstream is re-encoded from the validated fields, so nothing else can ride along.
func guardSpoolman(stripPrefix string, demoMode bool, inner http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		escaped := r.URL.EscapedPath()
		if hasSuspiciousPath(escaped) {
			http.NotFound(w, r)
			return
		}
		trimmed := strings.TrimPrefix(escaped, stripPrefix)
		methods := spoolmanRouteMethods(trimmed)
		if methods == nil {
			http.Error(w, "path not allowed", http.StatusForbidden)
			return
		}
		if !methods[r.Method] {
			w.Header().Set("Allow", allowHeader(methods))
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !isSpoolmanWrite(r.Method) {
			inner.ServeHTTP(w, r)
			return
		}

		if !identityFor(r).isAdmin {
			writeAuthError(w, http.StatusForbidden, "admin access required")
			return
		}
		if demoMode {
			writeAuthError(w, http.StatusForbidden, "read-only demo")
			return
		}
		if !hasJSONContentType(r.Header.Get("Content-Type")) {
			writeAuthError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, spoolmanWriteMaxBytes))
		if err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				writeAPIError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("body exceeds %d bytes", spoolmanWriteMaxBytes))
				return
			}
			writeAPIError(w, http.StatusBadRequest, "could not read body")
			return
		}
		clean, err := sanitizeSpoolWrite(r.Method, raw)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(clean))
		r.ContentLength = int64(len(clean))
		r.TransferEncoding = nil
		r.Header.Set("Content-Length", strconv.Itoa(len(clean)))
		r.Header.Set("Content-Type", "application/json")
		r.URL.RawQuery = "" // no query parameters on writes
		inner.ServeHTTP(w, r)
	})
}

func allowHeader(methods map[string]bool) string {
	var out []string
	for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPatch} {
		if methods[m] {
			out = append(out, m)
		}
	}
	return strings.Join(out, ", ")
}

type spoolFieldKind int

const (
	fieldID     spoolFieldKind = iota // positive integer
	fieldWeight                       // finite number >= 0
	fieldString
	fieldBool
)

type spoolField struct {
	kind   spoolFieldKind
	maxLen int // runes, for strings
}

// Field allowlists per method (spec item 11). Anything else is a 400.
var (
	spoolCreateFields = map[string]spoolField{
		"filament_id":      {kind: fieldID},
		"initial_weight":   {kind: fieldWeight},
		"remaining_weight": {kind: fieldWeight},
		"price":            {kind: fieldWeight},
		"location":         {kind: fieldString, maxLen: 128},
		"comment":          {kind: fieldString, maxLen: 1024},
	}
	spoolUpdateFields = map[string]spoolField{
		"remaining_weight": {kind: fieldWeight},
		"price":            {kind: fieldWeight},
		"location":         {kind: fieldString, maxLen: 128},
		"archived":         {kind: fieldBool},
		"comment":          {kind: fieldString, maxLen: 1024},
	}
)

const spoolMaxNumber = 1e7

// sanitizeSpoolWrite validates raw against the allowlist for method and returns a freshly encoded body that
// holds only the validated fields. Unknown or duplicate-shadowed keys, nulls, wrong types, out-of-range
// numbers, and trailing data are errors.
func sanitizeSpoolWrite(method string, raw []byte) ([]byte, error) {
	fields := spoolUpdateFields
	if method == http.MethodPost {
		fields = spoolCreateFields
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var in map[string]any
	if err := dec.Decode(&in); err != nil || in == nil {
		return nil, errors.New("body must be a JSON object")
	}
	if dec.More() {
		return nil, errors.New("body must be a single JSON object")
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("body must be a single JSON object")
	}

	out := make(map[string]any, len(in))
	for key, val := range in {
		spec, ok := fields[key]
		if !ok {
			return nil, fmt.Errorf("%s: field not allowed", key)
		}
		switch spec.kind {
		case fieldID:
			n, ok := val.(json.Number)
			id, err := strconv.ParseInt(n.String(), 10, 64)
			if !ok || err != nil || id < 1 || id > 1<<53 {
				return nil, fmt.Errorf("%s: must be a positive integer", key)
			}
			out[key] = id
		case fieldWeight:
			n, ok := val.(json.Number)
			f, err := n.Float64()
			if !ok || err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f > spoolMaxNumber {
				return nil, fmt.Errorf("%s: must be a number from 0 to %.0f", key, spoolMaxNumber)
			}
			out[key] = n
		case fieldString:
			s, ok := val.(string)
			if !ok || !utf8.ValidString(s) || utf8.RuneCountInString(s) > spec.maxLen {
				return nil, fmt.Errorf("%s: must be text of at most %d characters", key, spec.maxLen)
			}
			out[key] = s
		case fieldBool:
			b, ok := val.(bool)
			if !ok {
				return nil, fmt.Errorf("%s: must be true or false", key)
			}
			out[key] = b
		}
	}
	if method == http.MethodPost {
		if _, ok := out["filament_id"]; !ok {
			return nil, errors.New("filament_id: required")
		}
	} else if len(out) == 0 {
		return nil, errors.New("no fields to update")
	}
	return json.Marshal(out)
}
