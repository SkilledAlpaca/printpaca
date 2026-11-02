package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// proxyMux builds the FULL route table via registerRoutes, as main.go does; a proxy-only mux would pass even if the real table let /api/spoolman fall through to the SPA.
func proxyMux(t *testing.T, spoolman string) *http.ServeMux {
	t.Helper()
	t.Setenv("SPOOLMAN_URL", spoolman)
	return newTestMux(t, t.TempDir())
}

func TestSpoolmanProxyStripsPrefix(t *testing.T) {
	var gotPath string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte(`[{"id":1}]`))
	}))
	defer backend.Close()

	mux := proxyMux(t, backend.URL)
	rec := serveReq(mux, httptest.NewRequest("GET", "/api/spoolman/api/v1/spool", nil))
	if rec.Code != 200 || gotPath != "/api/v1/spool" {
		t.Fatalf("got %d path %q", rec.Code, gotPath)
	}
}

func TestSpoolmanUnsetIs404(t *testing.T) {
	mux := proxyMux(t, "")
	rec := serveReq(mux, httptest.NewRequest("GET", "/api/spoolman/api/v1/spool", nil))
	if rec.Code != 404 {
		t.Fatalf("want 404, got %d", rec.Code)
	}
}

// spoolmanAllowedPaths must constrain the proxy: the UI only calls /api/v1/spool, so anything else is rejected with 403 before any upstream call, as for Moonraker's allowlist.
func TestSpoolmanProxyRejectsNonAllowlistedPath(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("backend must not be reached")
	}))
	defer backend.Close()

	mux := proxyMux(t, backend.URL)
	for _, path := range []string{"/api/v1/filament", "/api/v1/spool/1", "/api/v1/vendor"} {
		rec := serveReq(mux, httptest.NewRequest("GET", "/api/spoolman"+path, nil))
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403", path, rec.Code)
		}
	}
}

func TestSpoolmanProxyRejectsWriteMethods(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("backend must not be reached")
	}))
	defer backend.Close()

	mux := proxyMux(t, backend.URL)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		rec := serveReq(mux, httptest.NewRequest(method, "/api/spoolman/api/v1/spool", strings.NewReader("{}")))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: want 405, got %d", method, rec.Code)
		}
	}
}

// Unit-tests the traversal guard against escaped forms: percent-encoded slash/dot sequences and literal dot-segments.
func TestHasSuspiciousPath(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"clean path", "/api/v1/spool", false},
		{"clean nested path", "/api/v1/spool/1", false},
		{"encoded slash traversal", "/api/v1/..%2f..%2fetc/passwd", true},
		{"encoded dot traversal", "/api/v1/%2e%2e/%2e%2e/etc/passwd", true},
		{"literal dot-segment", "/api/v1/../etc/passwd", true},
		{"uppercase encoded slash", "/api/v1/..%2F..%2Fetc/passwd", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasSuspiciousPath(tc.in); got != tc.want {
				t.Errorf("hasSuspiciousPath(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// Regression from a live probe: a percent-encoded traversal sequence decoded r.URL.Path to a clean path that passed the prefix guard, while the outgoing request kept "../.." in the wire path. The backend must never be contacted.
func TestSpoolmanProxyRejectsEncodedTraversal(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("backend must not be reached")
	}))
	defer backend.Close()
	mux := proxyMux(t, backend.URL)

	for _, raw := range []string{
		"/api/spoolman/..%2f..%2fetc/passwd",
		"/api/spoolman/%2e%2e/%2e%2e/etc/passwd",
	} {
		rec := serveReq(mux, httptest.NewRequest("GET", raw, nil))
		if rec.Code == http.StatusOK {
			t.Fatalf("%s: traversal reached the backend (200)", raw)
		}
	}
}

// TestPlainDotDotStillHandled confirms an un-encoded ".." never reaches the
// proxy handler either: Go's ServeMux redirects requests whose decoded path
// isn't already clean, and our own guard is a backstop if it ever didn't.
func TestPlainDotDotStillHandled(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("backend must not be reached")
	}))
	defer backend.Close()
	mux := proxyMux(t, backend.URL)

	rec := serveReq(mux, httptest.NewRequest("GET", "/api/spoolman/api/v1/../../spool", nil))
	if rec.Code != http.StatusTemporaryRedirect && rec.Code != http.StatusMovedPermanently &&
		rec.Code != http.StatusNotFound && rec.Code != http.StatusForbidden {
		t.Fatalf("want redirect or rejection, got %d", rec.Code)
	}
}

// TestProxyResponseHeadersScrubbed ensures upstream Set-Cookie and
// Access-Control-* headers never reach the browser: letting either through
// would blur this app's origin with the proxied service's.
func TestProxyResponseHeadersScrubbed(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Set-Cookie", "session=upstream; Path=/")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Write([]byte(`[]`))
	}))
	defer backend.Close()

	mux := proxyMux(t, backend.URL)
	rec := serveReq(mux, httptest.NewRequest("GET", "/api/spoolman/api/v1/spool", nil))
	if rec.Code != 200 {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if rec.Header().Get("Set-Cookie") != "" {
		t.Errorf("Set-Cookie leaked: %q", rec.Header().Get("Set-Cookie"))
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("Access-Control-Allow-Origin leaked: %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
	if rec.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Errorf("Access-Control-Allow-Credentials leaked: %q", rec.Header().Get("Access-Control-Allow-Credentials"))
	}
}

// TestSpoolmanProxyStripsClientAuthAndCookie confirms the browser's own
// Cookie/Authorization headers (if any, e.g. from some unrelated site on
// the same host) never reach Spoolman, which has no business seeing them.
func TestSpoolmanProxyStripsClientAuthAndCookie(t *testing.T) {
	var gotAuth, gotCookie string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCookie = r.Header.Get("Cookie")
		w.Write([]byte(`[]`))
	}))
	defer backend.Close()

	mux := proxyMux(t, backend.URL)
	req := httptest.NewRequest("GET", "/api/spoolman/api/v1/spool", nil)
	req.Header.Set("Authorization", "Bearer client-side-token")
	req.Header.Set("Cookie", "foo=bar")
	rec := serveReq(mux, req)
	if rec.Code != 200 {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if gotAuth != "" {
		t.Errorf("Authorization leaked to Spoolman: %q", gotAuth)
	}
	if gotCookie != "" {
		t.Errorf("Cookie leaked to Spoolman: %q", gotCookie)
	}
}

// TestProxyBasePathJoined confirms a SPOOLMAN_URL with its own path prefix
// (e.g. behind a reverse proxy at http://host/spoolman) isn't silently
// discarded; the inbound path must be joined onto it.
func TestProxyBasePathJoined(t *testing.T) {
	var gotPath string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte(`[]`))
	}))
	defer backend.Close()

	mux := proxyMux(t, backend.URL+"/spoolman")
	rec := serveReq(mux, httptest.NewRequest("GET", "/api/spoolman/api/v1/spool", nil))
	if rec.Code != 200 {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if gotPath != "/spoolman/api/v1/spool" {
		t.Fatalf("base path not joined: got %q", gotPath)
	}
}

// TestSpoolmanProxyUpstreamDownIsBadGateway confirms an unreachable Spoolman
// instance fails closed (502) rather than hanging or leaking an internal
// error to the client.
func TestSpoolmanProxyUpstreamDownIsBadGateway(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := backend.URL
	backend.Close() // nothing listens on addr anymore; connections refuse

	mux := proxyMux(t, addr)
	rec := serveReq(mux, httptest.NewRequest("GET", "/api/spoolman/api/v1/spool", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("want 502, got %d", rec.Code)
	}
}

// The path logged by ErrorHandler must use %q, like accesslog.go's fields, since it is attacker-controlled on any exposed listener. ErrorHandler sees the request after Rewrite put it on the upstream path ("/api/v1/spool").
// The allowlist only permits an exact match, so this only confirms the log line quotes whatever it logs.
func TestProxyErrorHandlerEscapesPath(t *testing.T) {
	var buf bytes.Buffer
	restore := redirectLog(t, &buf)
	defer restore()

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := backend.URL
	backend.Close() // nothing listens on addr anymore; connections refuse

	mux := proxyMux(t, addr)
	rec := serveReq(mux, httptest.NewRequest("GET", "/api/spoolman/api/v1/spool", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("want 502, got %d", rec.Code)
	}
	if !strings.Contains(buf.String(), `"/api/v1/spool"`) {
		t.Errorf("log output = %q, want the request path logged quoted via %%q", buf.String())
	}
}

// Moonraker: the same proxy machinery as Spoolman plus an exact-match path allowlist, since it also exposes printer control and arbitrary file download.

// moonrakerMux mirrors proxyMux: builds the full route table via
// registerRoutes with MOONRAKER_URL set, not just registerProxies in
// isolation.
func moonrakerMux(t *testing.T, moonraker string) *http.ServeMux {
	t.Helper()
	t.Setenv("MOONRAKER_URL", moonraker)
	return newTestMux(t, t.TempDir())
}

func TestMoonrakerUnsetIs404(t *testing.T) {
	mux := moonrakerMux(t, "")
	rec := serveReq(mux, httptest.NewRequest("GET", "/api/moonraker/server/history/list", nil))
	if rec.Code != 404 {
		t.Fatalf("want 404, got %d", rec.Code)
	}
}

// Both allowlisted paths reach the backend with the prefix stripped; the metadata path needs a safe filename query or guardMoonrakerMetadataFilename 400s it first.
func TestMoonrakerProxyAllowedPathsPassThrough(t *testing.T) {
	var gotPath string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte(`{"result":{}}`))
	}))
	defer backend.Close()

	mux := moonrakerMux(t, backend.URL)
	for _, path := range []string{"/server/history/list", "/server/files/metadata?filename=job.gcode"} {
		wantPath, _, _ := strings.Cut(path, "?")
		rec := serveReq(mux, httptest.NewRequest("GET", "/api/moonraker"+path, nil))
		if rec.Code != 200 || gotPath != wantPath {
			t.Errorf("%s: got %d path %q", path, rec.Code, gotPath)
		}
	}
}

// TestMoonrakerProxyRejectsControlPath confirms a printer-control endpoint
// (not on the allowlist) is rejected with 403 and the backend is never
// contacted -- the whole point of the allowlist.
func TestMoonrakerProxyRejectsControlPath(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("backend must not be reached")
	}))
	defer backend.Close()

	mux := moonrakerMux(t, backend.URL)
	rec := serveReq(mux, httptest.NewRequest("GET", "/api/moonraker/printer/gcode/script", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rec.Code)
	}
}

// TestMoonrakerProxyIsExactMatchNotPrefix confirms the allowlist is exact
// match only: a path that merely starts with an allowed entry is still
// rejected, since pathAllowed does no prefix matching.
func TestMoonrakerProxyIsExactMatchNotPrefix(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("backend must not be reached")
	}))
	defer backend.Close()

	mux := moonrakerMux(t, backend.URL)
	for _, path := range []string{
		"/server/history/list/extra",
		"/server/files/metadata/extra",
	} {
		rec := serveReq(mux, httptest.NewRequest("GET", "/api/moonraker"+path, nil))
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: want 403, got %d", path, rec.Code)
		}
	}
}

// TestMoonrakerProxyRejectsWriteMethods mirrors
// TestSpoolmanProxyRejectsWriteMethods: even on an allowlisted path, only
// GET/HEAD pass.
func TestMoonrakerProxyRejectsWriteMethods(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("backend must not be reached")
	}))
	defer backend.Close()

	mux := moonrakerMux(t, backend.URL)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		rec := serveReq(mux, httptest.NewRequest(method, "/api/moonraker/server/history/list", strings.NewReader("{}")))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: want 405, got %d", method, rec.Code)
		}
	}
}

// The traversal guard runs before the path allowlist, so an encoded traversal never reaches either the allowlist or the backend.
func TestMoonrakerProxyRejectsEncodedTraversal(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("backend must not be reached")
	}))
	defer backend.Close()
	mux := moonrakerMux(t, backend.URL)

	for _, raw := range []string{
		"/api/moonraker/server/history/..%2f..%2flist",
		"/api/moonraker/%2e%2e/%2e%2e/server/history/list",
	} {
		rec := serveReq(mux, httptest.NewRequest("GET", raw, nil))
		if rec.Code == http.StatusOK {
			t.Fatalf("%s: traversal reached the backend (200)", raw)
		}
	}
}

// guardMoonrakerMetadataFilename rejects an unsafe filename query on /server/files/metadata with 400 before the backend is contacted, and passes an allowed one.
func TestMoonrakerMetadataFilenameValidation(t *testing.T) {
	cases := []struct {
		name     string
		filename string
		hasQuery bool
		wantCode int
	}{
		{"allowed filename", "job.gcode", true, http.StatusOK},
		{"nested allowed filename", "folder/job.gcode", true, http.StatusOK},
		{"dot-dot segment", "../job.gcode", true, http.StatusBadRequest},
		{"dot-dot segment nested", "folder/../../job.gcode", true, http.StatusBadRequest},
		{"leading slash", "/etc/passwd", true, http.StatusBadRequest},
		{"empty value", "", true, http.StatusBadRequest},
		{"missing query", "", false, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.wantCode != http.StatusOK {
					t.Error("backend must not be reached")
				}
				w.Write([]byte(`{"result":{}}`))
			}))
			defer backend.Close()

			mux := moonrakerMux(t, backend.URL)
			path := "/api/moonraker/server/files/metadata"
			if tc.hasQuery {
				path += "?filename=" + tc.filename
			}
			rec := serveReq(mux, httptest.NewRequest("GET", path, nil))
			if rec.Code != tc.wantCode {
				t.Errorf("got %d, want %d", rec.Code, tc.wantCode)
			}
		})
	}
}

// The guard must inspect every "filename" query value, not just the first: ReverseProxy forwards RawQuery verbatim, so a second malicious value could reach an upstream that resolves duplicates to the last.
func TestMoonrakerMetadataFilenameRejectsDuplicateQueryValues(t *testing.T) {
	cases := []struct {
		name     string
		rawQuery string
	}{
		{"two benign values", "filename=job.gcode&filename=other.gcode"},
		{"second value is traversal", "filename=job.gcode&filename=../../../etc/passwd"},
		{"first value is traversal", "filename=../../../etc/passwd&filename=job.gcode"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("backend must not be reached")
			}))
			defer backend.Close()

			mux := moonrakerMux(t, backend.URL)
			rec := serveReq(mux, httptest.NewRequest("GET", "/api/moonraker/server/files/metadata?"+tc.rawQuery, nil))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("got %d, want %d", rec.Code, http.StatusBadRequest)
			}
		})
	}
}

// validProxyURL: the pure validator behind mustValidProxyURL and mustValidLinkURL (FLUIDD_URL reuses it), covering startup validation without exercising log.Fatal.

func TestValidProxyURLAcceptsHTTPAndHTTPS(t *testing.T) {
	for _, raw := range []string{"http://host.local", "https://host.local:9000", "http://host.local/base/path"} {
		if _, err := validProxyURL("TEST_URL", raw); err != nil {
			t.Errorf("validProxyURL(%q) = %v, want nil error", raw, err)
		}
	}
}

// resolveIntegrationURL: env wins over config for SPOOLMAN_URL/MOONRAKER_URL/FLUIDD_URL and the Settings-page fallback (IntegrationsUrls). The tests below pin the pure function, then the same precedence through proxy registration.

func TestResolveIntegrationURLPrecedence(t *testing.T) {
	cases := []struct {
		name       string
		env        string
		config     string
		wantRaw    string
		wantSource string
	}{
		{"neither set", "", "", "", "unset"},
		{"config only", "", "http://cfg.local", "http://cfg.local", "config"},
		{"env only", "http://env.local", "", "http://env.local", "env"},
		{"env wins over config", "http://env.local", "http://cfg.local", "http://env.local", "env"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, source := resolveIntegrationURL(tc.env, tc.config)
			if raw != tc.wantRaw || source != tc.wantSource {
				t.Errorf("resolveIntegrationURL(%q, %q) = (%q, %q), want (%q, %q)",
					tc.env, tc.config, raw, source, tc.wantRaw, tc.wantSource)
			}
		})
	}
}

// TestRegisterProxiesUsesConfigWhenEnvUnset proves (not just describes) that
// an unset SPOOLMAN_URL falls back to config.yaml's integrationsUrls.spoolmanUrl:
// a request through the real proxy route reaches the config-sourced backend.
func TestRegisterProxiesUsesConfigWhenEnvUnset(t *testing.T) {
	var gotPath string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte(`[]`))
	}))
	defer backend.Close()

	dir := t.TempDir()
	cfgYAML := "integrationsUrls:\n  spoolmanUrl: " + backend.URL + "\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(cfgYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPOOLMAN_URL", "")
	mux := newTestMux(t, dir)

	rec := serveReq(mux, httptest.NewRequest("GET", "/api/spoolman/api/v1/spool", nil))
	if rec.Code != 200 || gotPath != "/api/v1/spool" {
		t.Fatalf("got %d path %q, want 200 /api/v1/spool via config-sourced SPOOLMAN_URL", rec.Code, gotPath)
	}
}

// SPOOLMAN_URL wins when both it and integrationsUrls.spoolmanUrl are set: the config URL (an address nothing listens on) must never be contacted.
func TestRegisterProxiesEnvWinsOverConfig(t *testing.T) {
	var hit bool
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.Write([]byte(`[]`))
	}))
	defer backend.Close()

	dir := t.TempDir()
	cfgYAML := "integrationsUrls:\n  spoolmanUrl: http://must-not-be-contacted.invalid\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(cfgYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPOOLMAN_URL", backend.URL)
	mux := newTestMux(t, dir)

	rec := serveReq(mux, httptest.NewRequest("GET", "/api/spoolman/api/v1/spool", nil))
	if rec.Code != 200 || !hit {
		t.Fatalf("env-sourced SPOOLMAN_URL was not used: status=%d hit=%v", rec.Code, hit)
	}
}

// POST /api/integrations/test validates kind/url, then live-probes. Demo-mode blocking is covered in demo_test.go since it depends on the auth middleware chain.

func TestIntegrationsTestValidSpoolman(t *testing.T) {
	var gotPath string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[]`))
	}))
	defer backend.Close()

	mux := newTestMux(t, t.TempDir())
	body := `{"kind":"spoolman","url":"` + backend.URL + `"}`
	rec := serveReq(mux, httptest.NewRequest("POST", "/api/integrations/test", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Ok     bool   `json:"ok"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Ok {
		t.Errorf("ok = false, want true; detail = %q", resp.Detail)
	}
	if !strings.Contains(resp.Detail, "200") {
		t.Errorf("detail = %q, want it to mention HTTP 200", resp.Detail)
	}
	if gotPath != "/api/v1/spool" {
		t.Errorf("probed path = %q, want /api/v1/spool (the same allowlisted endpoint the real proxy uses)", gotPath)
	}
}

func TestIntegrationsTestValidMoonraker(t *testing.T) {
	var gotPath string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"result":{}}`))
	}))
	defer backend.Close()

	mux := newTestMux(t, t.TempDir())
	body := `{"kind":"moonraker","url":"` + backend.URL + `"}`
	rec := serveReq(mux, httptest.NewRequest("POST", "/api/integrations/test", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if gotPath != "/server/history/list" {
		t.Errorf("probed path = %q, want /server/history/list", gotPath)
	}
}

func TestIntegrationsTestUnreachableIsOkFalse(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := backend.URL
	backend.Close() // nothing listens on addr anymore; connections refuse

	mux := newTestMux(t, t.TempDir())
	body := `{"kind":"spoolman","url":"` + addr + `"}`
	rec := serveReq(mux, httptest.NewRequest("POST", "/api/integrations/test", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the probe failure is reported in the body, not the HTTP status); body: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Ok     bool   `json:"ok"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Ok {
		t.Errorf("ok = true, want false for an unreachable target")
	}
	if resp.Detail == "" {
		t.Error("detail is empty, want an operator-facing error message")
	}
}

func TestIntegrationsTestRejectsInvalidKind(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/integrations/test",
		strings.NewReader(`{"kind":"fluidd","url":"http://printer.local"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (fluidd is link-only, not a valid test kind)", rec.Code)
	}
}

func TestIntegrationsTestRejectsInvalidURL(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/integrations/test",
		strings.NewReader(`{"kind":"spoolman","url":"nas.lan:7912"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestValidProxyURLRejectsInvalid(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"missing scheme and host", "nas.lan:7912"},
		{"unsupported scheme", "ftp://host.local"},
		{"scheme with no host", "http://"},
		{"unparsable", "http://[::1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := validProxyURL("FLUIDD_URL", tc.raw); err == nil {
				t.Errorf("validProxyURL(%q) = nil error, want a validation error", tc.raw)
			}
		})
	}
}

func TestProxyForcesJSONAndHardeningHeaders(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<script>alert(1)</script>`))
	}))
	defer backend.Close()

	mux := proxyMux(t, backend.URL)
	rec := serveReq(mux, httptest.NewRequest("GET", "/api/spoolman/api/v1/spool", nil))
	h := rec.Header()
	if h.Get("Content-Type") != "application/json" || h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Content-Security-Policy") != "sandbox" {
		t.Fatalf("headers = %v, want forced JSON, nosniff and CSP sandbox", h)
	}
}
