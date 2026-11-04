package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
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
// download; this app only reads job history, slicer metadata, and the printer's hostname and state.
var moonrakerAllowedPaths = map[string]bool{
	"/server/history/list":   true,
	"/server/files/metadata": true,
	"/printer/info":          true,
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

// resolveIntegrationURL applies env-wins-over-config for the three integration URLs and returns the
// raw URL plus a source label ("env", "config", "unset") shown in Settings. Callers resolve it per
// request from the live ConfigStore, so a Settings save applies without a restart.
func resolveIntegrationURL(envVal, configVal string) (raw, source string) {
	if envVal != "" {
		return envVal, "env"
	}
	if configVal != "" {
		return configVal, "config"
	}
	return "", "unset"
}

// liveProxy caches the reverse proxy built for the last resolved URL, so a Settings change swaps the target on
// the next request while an unchanged URL reuses one handler.
type liveProxy struct {
	mu    sync.Mutex
	raw   string
	inner http.Handler
}

// handlerFor returns the proxy for raw, rebuilding it when raw changed. A raw value that fails validProxyURL
// returns the error; Settings validates saved values, so this only trips on a bad environment value.
func (l *liveProxy) handlerFor(envVar, raw string, build func(*url.URL) http.Handler) (http.Handler, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inner != nil && l.raw == raw {
		return l.inner, nil
	}
	target, err := validProxyURL(envVar, raw)
	if err != nil {
		return nil, err
	}
	l.raw, l.inner = raw, build(target)
	return l.inner, nil
}

// registerProxies wires the opt-in Spoolman and Moonraker reverse proxies, so the browser needs no
// CORS or token. The URL resolves on every request from the env var (wins) or the live config, so a
// Settings save applies at once. With no URL and demoMode on, a fake handler (demo.go) answers; a real URL
// always wins. Without either, the mount point is a plain 404.
// Spoolman is gated on "filament" for reads and admin for writes (spoolman_guard.go); Moonraker on "quote".
func registerProxies(mux *http.ServeMux, demoMode bool, store *ConfigStore, db *sql.DB) {
	// An invalid environment value still fails fast at boot, as before.
	for _, name := range []string{"SPOOLMAN_URL", "MOONRAKER_URL"} {
		if raw := os.Getenv(name); raw != "" {
			mustValidProxyURL(name, raw)
		}
	}

	demoSpoolman := http.NewServeMux()
	registerDemoSpoolman(demoSpoolman, db)
	spoolmanLive := &liveProxy{}
	spoolman := requirePermission(db, "filament", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := resolveIntegrationURL(os.Getenv("SPOOLMAN_URL"), store.Get().IntegrationsUrls.SpoolmanURL)
		if raw == "" {
			if demoMode {
				demoSpoolman.ServeHTTP(w, r)
				return
			}
			http.NotFound(w, r)
			return
		}
		inner, err := spoolmanLive.handlerFor("SPOOLMAN_URL", raw, func(target *url.URL) http.Handler {
			return newProxyHandler(target, "/api/spoolman", spoolmanAllMethods, nil, nil)
		})
		if err != nil {
			log.Printf("spoolman proxy: %v", err)
			http.Error(w, "integration URL invalid", http.StatusBadGateway)
			return
		}
		guardSpoolman("/api/spoolman", demoMode, inner).ServeHTTP(w, r)
	})
	registerMethodRoutes(mux, "/api/spoolman/", spoolmanAllMethods, spoolman)

	demoMoonraker := http.NewServeMux()
	registerDemoMoonraker(demoMoonraker, db)
	moonrakerMethods := map[string]bool{http.MethodGet: true, http.MethodHead: true}
	moonrakerLive := &liveProxy{}
	moonraker := requirePermission(db, "quote", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := resolveIntegrationURL(os.Getenv("MOONRAKER_URL"), store.Get().IntegrationsUrls.MoonrakerURL)
		if raw == "" {
			if demoMode {
				demoMoonraker.ServeHTTP(w, r)
				return
			}
			http.NotFound(w, r)
			return
		}
		inner, err := moonrakerLive.handlerFor("MOONRAKER_URL", raw, func(target *url.URL) http.Handler {
			// GET/HEAD only, plus moonrakerPathAllowed, since some Moonraker GETs (gcode download) are too broad.
			return guardMoonrakerMetadataFilename("/api/moonraker",
				newProxyHandler(target, "/api/moonraker", moonrakerMethods, moonrakerPathAllowed, nil))
		})
		if err != nil {
			log.Printf("moonraker proxy: %v", err)
			http.Error(w, "integration URL invalid", http.StatusBadGateway)
			return
		}
		inner.ServeHTTP(w, r)
	})
	registerMethodRoutes(mux, "/api/moonraker/", moonrakerMethods, moonraker)
}

// integrationsTestMaxBytes bounds the POST body: one kind, one URL.
const integrationsTestMaxBytes = 4 * 1024

// integrationsTestTimeout bounds the whole probe so the waiting operator fails fast.
const integrationsTestTimeout = 4 * time.Second

// integrationsTestBodyCap bounds the probe body that is parsed for counts and the hostname.
const integrationsTestBodyCap = 2 << 20

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
		return "/server/history/list?limit=20&order=desc"
	}
	return ""
}

// probeGet GETs target+path and returns the status, the response header, and up to integrationsTestBodyCap
// bytes of body. The rest is drained for keep-alive reuse.
func probeGet(ctx context.Context, target *url.URL, path string) (status int, header http.Header, body []byte, err error) {
	probeURL := *target
	pathOnly, query, _ := strings.Cut(path, "?")
	probeURL.Path = strings.TrimSuffix(target.Path, "/") + pathOnly
	probeURL.RawPath = ""
	probeURL.RawQuery = query

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL.String(), nil)
	if err != nil {
		return 0, nil, nil, err
	}
	resp, err := integrationsTestClient.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	body, err = io.ReadAll(io.LimitReader(resp.Body, integrationsTestBodyCap))
	if err != nil {
		return 0, nil, nil, err
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, integrationsTestBodyCap)) //nolint:errcheck // draining only
	return resp.StatusCode, resp.Header, body, nil
}

// failureHint turns a transport error into plain words about what to check. The raw error is never shown:
// it can carry resolver or socket detail that helps nobody at the Settings page.
func failureHint(name string, err error) string {
	var dnsErr *net.DNSError
	var netErr net.Error
	switch {
	case errors.As(err, &dnsErr):
		return fmt.Sprintf("Couldn't find %s at that address. Check the host name in the URL.", name)
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()):
		return fmt.Sprintf("%s didn't answer in time. Check the URL and port, and that printpaca can reach that network.", name)
	case errors.Is(err, syscall.ECONNREFUSED):
		return fmt.Sprintf("%s refused the connection. Check the port in the URL and that %s is running.", name, name)
	}
	return fmt.Sprintf("Couldn't connect to %s. Check the URL, the port, and the network between printpaca and it.", name)
}

// statusHint explains a non-2xx answer in plain words.
func statusHint(name string, status int) string {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return fmt.Sprintf("%s answered but refused access (HTTP %d). printpaca sends no credentials, so allow it through any proxy or login in front of %s.", name, status, name)
	case status == http.StatusNotFound:
		return fmt.Sprintf("Something answered, but it isn't %s (HTTP 404). Check the URL and port, and any path prefix.", name)
	case status >= 300 && status < 400:
		return fmt.Sprintf("The URL redirects (HTTP %d) instead of reaching %s. Use the final address, including http or https.", status, name)
	}
	return fmt.Sprintf("%s answered with an error (HTTP %d). Check that it is running and that the URL is its API address.", name, status)
}

// cleanHostname trims and bounds a hostname reported by an upstream, dropping control characters. The value
// is untrusted: it ends up in a prompt and, if accepted, a printer name.
func cleanHostname(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if utf8.RuneCountInString(s) > 64 {
		s = string([]rune(s)[:64])
	}
	return s
}

// pluralize returns "1 job" or "14 jobs".
func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", singular)
	}
	return fmt.Sprintf("%d %s", n, plural)
}

// probeIntegration checks kind at target and returns the outcome. detail is a sentence for the operator
// that says what was found or what to check; hostname is set for a Moonraker that reported one. Showing
// detail is safe: only an authenticated admin can trigger it, and demoWriteGuard blocks it in demo mode.
func probeIntegration(ctx context.Context, target *url.URL, kind string) (res integrationsTestResponse) {
	switch kind {
	case "spoolman":
		return probeSpoolman(ctx, target)
	case "moonraker":
		return probeMoonraker(ctx, target)
	}
	return integrationsTestResponse{Detail: "Unknown integration."}
}

func probeSpoolman(ctx context.Context, target *url.URL) integrationsTestResponse {
	status, header, body, err := probeGet(ctx, target, integrationsTestPath("spoolman"))
	if err != nil {
		return integrationsTestResponse{Detail: failureHint("Spoolman", err)}
	}
	if status < 200 || status >= 300 {
		return integrationsTestResponse{Detail: statusHint("Spoolman", status)}
	}
	// Spoolman's list excludes archived spools by default, so the count is "active spools".
	count := -1
	if n, err := strconv.Atoi(header.Get("X-Total-Count")); err == nil && n >= 0 {
		count = n
	}
	var spools []json.RawMessage
	if err := json.Unmarshal(body, &spools); err != nil {
		return integrationsTestResponse{Detail: "Something answered, but not like Spoolman: the spool list isn't JSON. Check the URL and port."}
	}
	if count < 0 {
		count = len(spools)
	}
	return integrationsTestResponse{Ok: true, Count: &count, Detail: fmt.Sprintf("Connected. %s.", pluralize(count, "active spool", "active spools"))}
}

func probeMoonraker(ctx context.Context, target *url.URL) integrationsTestResponse {
	status, _, body, err := probeGet(ctx, target, integrationsTestPath("moonraker"))
	if err != nil {
		return integrationsTestResponse{Detail: failureHint("Moonraker", err)}
	}
	if status < 200 || status >= 300 {
		return integrationsTestResponse{Detail: statusHint("Moonraker", status)}
	}
	var history struct {
		Result struct {
			Jobs []json.RawMessage `json:"jobs"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &history); err != nil {
		return integrationsTestResponse{Detail: "Something answered, but not like Moonraker: the job history isn't JSON. Check the URL and port."}
	}
	count := len(history.Result.Jobs)
	jobs := pluralize(count, "recent job", "recent jobs")

	// The printer info is best-effort: Moonraker answers history even while Klipper is down.
	res := integrationsTestResponse{Ok: true, Count: &count}
	infoStatus, _, infoBody, infoErr := probeGet(ctx, target, "/printer/info")
	var info struct {
		Result struct {
			Hostname string `json:"hostname"`
			State    string `json:"state"`
		} `json:"result"`
	}
	if infoErr == nil && infoStatus >= 200 && infoStatus < 300 && json.Unmarshal(infoBody, &info) == nil {
		res.Hostname = cleanHostname(info.Result.Hostname)
		state := cleanHostname(info.Result.State)
		switch {
		case res.Hostname != "" && state != "":
			res.Detail = fmt.Sprintf("Connected to %s (Klipper %s). %s.", res.Hostname, state, jobs)
		case res.Hostname != "":
			res.Detail = fmt.Sprintf("Connected to %s. %s.", res.Hostname, jobs)
		}
	}
	if res.Detail == "" {
		res.Detail = fmt.Sprintf("Connected to Moonraker, but Klipper isn't reporting its state. %s.", jobs)
	}
	return res
}

// integrationsTestRequest is POST /api/integrations/test's body.
type integrationsTestRequest struct {
	Kind string `json:"kind"`
	URL  string `json:"url"`
}

// integrationsTestResponse is POST /api/integrations/test's success body. Count is the number of active
// spools or recent jobs found; Hostname is the Moonraker host name, when it reported one.
type integrationsTestResponse struct {
	Ok       bool   `json:"ok"`
	Detail   string `json:"detail"`
	Count    *int   `json:"count,omitempty"`
	Hostname string `json:"hostname,omitempty"`
}

// handleIntegrationsTest implements POST /api/integrations/test: validates kind and url (validProxyURL
// rules, 4xx on failure), probes live, and reports the outcome. An empty url tests the effective URL (the
// environment variable, else the saved setting), which is how the Settings card tests a URL it can't show
// because an environment variable sets it. Demo mode rejects it so a public demo can never trigger an
// outbound request.
func handleIntegrationsTest(store *ConfigStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		integrationsTest(store, w, r)
	}
}

func integrationsTest(store *ConfigStore, w http.ResponseWriter, r *http.Request) {
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
	rawURL := in.URL
	if rawURL == "" {
		cfg := store.Get().IntegrationsUrls
		if in.Kind == "spoolman" {
			rawURL, _ = resolveIntegrationURL(os.Getenv("SPOOLMAN_URL"), cfg.SpoolmanURL)
		} else {
			rawURL, _ = resolveIntegrationURL(os.Getenv("MOONRAKER_URL"), cfg.MoonrakerURL)
		}
		if rawURL == "" {
			writeAPIError(w, http.StatusBadRequest, "url: no URL is set for this integration")
			return
		}
	}
	target, err := validProxyURL("url", rawURL)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), integrationsTestTimeout)
	defer cancel()
	res := probeIntegration(ctx, target, in.Kind)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(res)
}
