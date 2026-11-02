package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"slices"
	"strings"
	"time"
)

// proxyTransport bounds dial and response waits so a dead Spoolman can't tie up goroutines.
var proxyTransport = &http.Transport{
	Proxy: http.ProxyFromEnvironment,
	DialContext: (&net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext,
	ResponseHeaderTimeout: 10 * time.Second,
	ForceAttemptHTTP2:     true,
	MaxIdleConns:          100,
	IdleConnTimeout:       90 * time.Second,
	TLSHandshakeTimeout:   10 * time.Second,
	ExpectContinueTimeout: 1 * time.Second,
}

// hasSuspiciousPath reports whether an escaped path has percent-encoded dot/slash sequences or
// dot-segments. Check r.URL.EscapedPath(), never the decoded Path.
func hasSuspiciousPath(escaped string) bool {
	lower := strings.ToLower(escaped)
	if strings.Contains(lower, "%2e") || strings.Contains(lower, "%2f") {
		return true
	}
	return path.Clean(escaped) != escaped
}

// stripHeaders drops the browser's Cookie/Authorization so they never reach the upstream.
func stripHeaders(out *http.Request) {
	out.Header.Del("Cookie")
	out.Header.Del("Authorization")
}

// scrubResponse strips upstream Set-Cookie and CORS headers, which would blur this app's origin boundary.
// Every proxied route is an exact-match JSON endpoint, so it also pins the response to application/json and
// adds nosniff plus a sandbox CSP: a compromised or spoofed upstream can't serve HTML or script that the
// browser would run on the admin origin.
func scrubResponse(resp *http.Response) error {
	resp.Header.Del("Set-Cookie")
	resp.Header.Set("Content-Type", "application/json")
	resp.Header.Set("X-Content-Type-Options", "nosniff")
	resp.Header.Set("Content-Security-Policy", "sandbox")
	for h := range resp.Header {
		if strings.HasPrefix(strings.ToLower(h), "access-control-") {
			resp.Header.Del(h)
		}
	}
	return nil
}

// newProxyHandler builds a reverse proxy for target mounted at stripPrefix+"/". Requests are rejected
// before any network call if the method isn't allowed or pathAllowed (given the trimmed escaped path)
// fails. The validated escaped path is joined onto target's base path and written to both Path and RawPath.
func newProxyHandler(target *url.URL, stripPrefix string, allowedMethods map[string]bool, pathAllowed func(escapedTrimmed string) bool, mutate func(*http.Request)) http.Handler {
	proxy := &httputil.ReverseProxy{
		Transport: proxyTransport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)

			escapedIn := pr.In.URL.EscapedPath()
			trimmed := strings.TrimPrefix(escapedIn, stripPrefix)
			if !strings.HasPrefix(trimmed, "/") {
				trimmed = "/" + trimmed
			}
			joined := strings.TrimSuffix(target.EscapedPath(), "/") + trimmed
			if joined == "" {
				joined = "/"
			}
			if decoded, err := url.PathUnescape(joined); err == nil {
				pr.Out.URL.Path = decoded
			} else {
				pr.Out.URL.Path = joined
			}
			pr.Out.URL.RawPath = joined
			pr.Out.Host = target.Host

			stripHeaders(pr.Out)
			if mutate != nil {
				mutate(pr.Out)
			}
		},
		ModifyResponse: scrubResponse,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			// %q prevents log injection: r.URL.Path is attacker-controlled (see accesslog.go).
			log.Printf("proxy %q: %v", r.URL.Path, err)
			http.Error(w, "upstream unreachable", http.StatusBadGateway)
		},
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowedMethods[r.Method] {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		escaped := r.URL.EscapedPath()
		if hasSuspiciousPath(escaped) {
			http.NotFound(w, r)
			return
		}

		if pathAllowed != nil {
			trimmed := strings.TrimPrefix(escaped, stripPrefix)
			if !pathAllowed(trimmed) {
				http.Error(w, "path not allowed", http.StatusForbidden)
				return
			}
		}

		proxy.ServeHTTP(w, r)
	})
}

// validProxyURL checks scheme is http/https and host is non-empty. Split out so tests avoid log.Fatalf.
func validProxyURL(envVar, raw string) (*url.URL, error) {
	target, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s invalid: %w", envVar, err)
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return nil, fmt.Errorf("%s invalid: scheme must be http or https, got %q", envVar, target.Scheme)
	}
	if target.Host == "" {
		return nil, fmt.Errorf("%s invalid: missing host", envVar)
	}
	return target, nil
}

// mustValidProxyURL fails fast at startup: a scheme-less value (e.g. "nas.lan:7912") would otherwise
// register a proxy that 502s while /api/config advertises the integration as enabled.
func mustValidProxyURL(envVar, raw string) *url.URL {
	target, err := validProxyURL(envVar, raw)
	if err != nil {
		log.Fatal(err)
	}
	return target
}

// mustValidLinkURL validates like mustValidProxyURL but registers no proxy: FLUIDD_URL is only a link.
func mustValidLinkURL(envVar, raw string) string {
	if _, err := validProxyURL(envVar, raw); err != nil {
		log.Fatal(err)
	}
	return raw
}

// Method-qualified patterns avoid the ServeMux panic from a bare path overlapping the SPA's "GET /"
// catch-all, and give automatic 405s with an Allow header.
func registerMethodRoutes(mux *http.ServeMux, pattern string, allowedMethods map[string]bool, handler http.Handler) {
	// The bare mount point is registered too. Without it ServeMux answers "/api/spoolman" with a redirect to
	// "/api/spoolman/", and behind the main router's "/admin" prefix that Location loses "/admin".
	bare := strings.TrimSuffix(pattern, "/")
	for method := range allowedMethods {
		mux.Handle(method+" "+pattern, handler)
		mux.Handle(method+" "+bare, handler)
	}
}

// moonrakerAllowedPaths is an exact-match allowlist. Moonraker also exposes printer control and file
// download; this app only reads job history and slicer metadata.
var moonrakerAllowedPaths = map[string]bool{
	"/server/history/list":   true,
	"/server/files/metadata": true,
}

// moonrakerPathAllowed matches exactly; "/server/history/list/extra" is rejected.
func moonrakerPathAllowed(escapedTrimmed string) bool {
	return moonrakerAllowedPaths[escapedTrimmed]
}

// moonrakerMetadataPath is the trimmed path guarded by guardMoonrakerMetadataFilename.
const moonrakerMetadataPath = "/server/files/metadata"

// filenameTraversalUnsafe reports whether filename (empty, rooted, or containing "..") is unsafe to
// forward. Safety must not rest on Moonraker treating it as an opaque key today.
func filenameTraversalUnsafe(filename string) bool {
	if filename == "" || strings.HasPrefix(filename, "/") {
		return true
	}
	return slices.Contains(strings.Split(filename, "/"), "..")
}

// guardMoonrakerMetadataFilename rejects with 400, before any upstream call, a metadata request
// whose filename fails filenameTraversalUnsafe. It checks every "filename" value and requires exactly
// one: ReverseProxy forwards duplicates verbatim and an upstream may use the last occurrence.
func guardMoonrakerMetadataFilename(stripPrefix string, inner http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trimmed := strings.TrimPrefix(r.URL.EscapedPath(), stripPrefix)
		if trimmed == moonrakerMetadataPath {
			values := r.URL.Query()["filename"]
			if len(values) != 1 || filenameTraversalUnsafe(values[0]) {
				http.Error(w, "invalid filename", http.StatusBadRequest)
				return
			}
		}
		inner.ServeHTTP(w, r)
	})
}

// spoolmanAllowedPaths is an exact-match allowlist (the UI calls only /api/v1/spool once the prefix
// is stripped), so Spoolman's other endpoints and any other service SPOOLMAN_URL points at are unreachable.
var spoolmanAllowedPaths = map[string]bool{
	"/api/v1/spool": true,
}

// spoolmanPathAllowed matches exactly, like moonrakerPathAllowed.
func spoolmanPathAllowed(escapedTrimmed string) bool {
	return spoolmanAllowedPaths[escapedTrimmed]
}

// resolveIntegrationURL applies env-wins-over-config for the three integration URLs and returns the
// raw URL plus a source label ("env", "config", "unset") shown in Settings. Resolved once at boot, so
// a Settings change takes effect after restart.
func resolveIntegrationURL(envVal, configVal string) (raw, source string) {
	if envVal != "" {
		return envVal, "env"
	}
	if configVal != "" {
		return configVal, "config"
	}
	return "", "unset"
}

// registerProxies wires the opt-in Spoolman and Moonraker reverse proxies, so the browser needs no
// CORS or token. URLs resolve once from the boot-time cfg (env wins). When a URL is unset and demoMode
// is on, a fake handler (demo.go) takes the mount point; a real URL always wins.
// Routes are gated on "filament" (Spoolman) and "quote" (Moonraker) via requirePermission.
func registerProxies(mux *http.ServeMux, demoMode bool, cfg *Config, db *sql.DB) {
	spoolmanRaw, _ := resolveIntegrationURL(os.Getenv("SPOOLMAN_URL"), cfg.IntegrationsUrls.SpoolmanURL)
	if spoolmanRaw != "" {
		target := mustValidProxyURL("SPOOLMAN_URL", spoolmanRaw)
		// GET/HEAD only: a simple-request POST needs no preflight, so any open page could otherwise mutate
		// inventory. spoolmanPathAllowed also blocks other GET endpoints as an SSRF pivot.
		spoolmanMethods := map[string]bool{http.MethodGet: true, http.MethodHead: true}
		handler := requirePermission(db, "filament", newProxyHandler(target, "/api/spoolman", spoolmanMethods, spoolmanPathAllowed, nil).ServeHTTP)
		registerMethodRoutes(mux, "/api/spoolman/", spoolmanMethods, handler)
	} else if demoMode {
		registerDemoSpoolman(mux, db)
	}

	moonrakerRaw, _ := resolveIntegrationURL(os.Getenv("MOONRAKER_URL"), cfg.IntegrationsUrls.MoonrakerURL)
	if moonrakerRaw != "" {
		target := mustValidProxyURL("MOONRAKER_URL", moonrakerRaw)
		// GET/HEAD only, plus moonrakerPathAllowed, since some Moonraker GETs (gcode download) are too broad.
		moonrakerMethods := map[string]bool{http.MethodGet: true, http.MethodHead: true}
		proxied := newProxyHandler(target, "/api/moonraker", moonrakerMethods, moonrakerPathAllowed, nil)
		handler := requirePermission(db, "quote", guardMoonrakerMetadataFilename("/api/moonraker", proxied).ServeHTTP)
		registerMethodRoutes(mux, "/api/moonraker/", moonrakerMethods, handler)
	} else if demoMode {
		registerDemoMoonraker(mux, db)
	}
}

// integrationsTestMaxBytes bounds the POST body: one kind, one URL.
const integrationsTestMaxBytes = 4 * 1024

// integrationsTestTimeout bounds the whole probe so the waiting operator fails fast.
const integrationsTestTimeout = 4 * time.Second

// integrationsTestBodyCap bounds the probe body; only the status is kept, the rest is drained for keep-alive.
const integrationsTestBodyCap = 4096

// integrationsTestClient reuses proxyTransport with a tighter per-call deadline. CheckRedirect stops
// at the first response so the result describes the target, not a redirect destination.
var integrationsTestClient = &http.Client{
	Transport: proxyTransport,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// integrationsTestPath returns the allowlisted GET path for kind, or "" if unknown. Using the real
// proxy path means a pass proves the proxied request would also succeed.
func integrationsTestPath(kind string) string {
	switch kind {
	case "spoolman":
		return "/api/v1/spool"
	case "moonraker":
		return "/server/history/list"
	}
	return ""
}

// probeIntegration GETs kind's allowlisted path and returns ok (2xx) plus a detail string: "HTTP <code>
// from <path>" or the raw dial/timeout error. Showing errors verbatim is safe: only an authenticated
// admin can trigger it, and demoWriteGuard blocks it in demo mode.
func probeIntegration(ctx context.Context, target *url.URL, kind string) (ok bool, detail string) {
	probePath := integrationsTestPath(kind)
	probeURL := *target
	probeURL.Path = strings.TrimSuffix(target.Path, "/") + probePath
	probeURL.RawPath = ""

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL.String(), nil)
	if err != nil {
		return false, err.Error()
	}
	resp, err := integrationsTestClient.Do(req)
	if err != nil {
		return false, err.Error()
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, integrationsTestBodyCap)) //nolint:errcheck // draining only, for keep-alive reuse

	ok = resp.StatusCode >= 200 && resp.StatusCode < 300
	return ok, fmt.Sprintf("HTTP %d from %s", resp.StatusCode, probePath)
}

// integrationsTestRequest is POST /api/integrations/test's body.
type integrationsTestRequest struct {
	Kind string `json:"kind"`
	URL  string `json:"url"`
}

// integrationsTestResponse is POST /api/integrations/test's success body.
type integrationsTestResponse struct {
	Ok     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// handleIntegrationsTest implements POST /api/integrations/test: validates kind and url (validProxyURL
// rules, 4xx on failure), probes live, and reports the outcome. Demo mode rejects it so a public demo
// can never trigger an outbound request.
func handleIntegrationsTest(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, integrationsTestMaxBytes)
	var in integrationsTestRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	if integrationsTestPath(in.Kind) == "" {
		writeAPIError(w, http.StatusBadRequest, `kind: must be "spoolman" or "moonraker"`)
		return
	}
	target, err := validProxyURL("url", in.URL)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), integrationsTestTimeout)
	defer cancel()
	ok, detail := probeIntegration(ctx, target, in.Kind)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(integrationsTestResponse{Ok: ok, Detail: detail})
}
