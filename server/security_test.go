package main

import (
	"bytes"
	"crypto/tls"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Every response through the admin chain, including a 401 from the session check, carries the conservative header set.
// adminListenerHandler is the real production chain: a hand-maintained copy once omitted accessLogMiddleware, hiding the accessLogStatusRecorder.Unwrap defect.
func TestSecurityHeadersPresentOnAdminListener(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	auth := newAuthState("secret", nil) // enabled, no session presented below
	handler := adminListenerHandler(mux, auth, false)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/clients", nil)
	req.Host = "localhost"
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (no session presented)", rec.Code)
	}
	assertSecurityHeaders(t, rec.Header())
}

// TestSecurityHeadersPresentOnPublicListener pins the same header set on
// the second listener: publicListenerHandler wraps buildPublicMux the same way
// newPublicServer does in production.
func TestSecurityHeadersPresentOnPublicListener(t *testing.T) {
	dir := t.TempDir()
	cfg := newPublicTestConfig(t, dir)
	db := openTestDB(t, dir)
	publicMux, _ := buildPublicMux(NewConfigStore(cfg), db, dir, false, false)
	handler := publicListenerHandler(publicMux, false)

	rec := serveReq(handler, httptest.NewRequest("GET", "/api/public/config", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	assertSecurityHeaders(t, rec.Header())
}

func assertSecurityHeaders(t *testing.T, h http.Header) {
	t.Helper()
	if got := h.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := h.Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want DENY", got)
	}
	if got := h.Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy = %q, want no-referrer", got)
	}
	csp := h.Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("Content-Security-Policy = %q, want default-src 'self' and frame-ancestors 'none'", csp)
	}
}

// Every endpoint carrying client PII, financial data, or a full export gets Cache-Control: no-store though none of these handlers set it themselves (unlike /api/config, /api/settings and auth).
func TestNoStoreOnPIIAndExportEndpoints(t *testing.T) {
	dir := t.TempDir()
	mux := newTestMux(t, dir)
	handler := adminListenerHandler(mux, newAuthState("", nil), false) // auth disabled: focus on the header, not 401s

	for _, path := range []string{"/api/clients", "/api/invoices", "/api/quotes", "/api/requests", "/api/export", "/api/dashboard"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", path, nil)
		req.Host = "localhost"
		handler.ServeHTTP(rec, req)
		if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
			t.Errorf("%s: Cache-Control = %q, want no-store (status %d)", path, cc, rec.Code)
		}
	}
}

// TestNoStoreExcludesProxiedPaths confirms noStoreAPIMiddleware doesn't
// touch /api/spoolman/ or /api/moonraker/, which pass through an upstream
// service's own response headers (see the middleware's doc).
func TestNoStoreExcludesProxiedPaths(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=60")
		w.Write([]byte("[]"))
	}))
	defer backend.Close()

	t.Setenv("SPOOLMAN_URL", backend.URL)
	mux := newTestMux(t, t.TempDir())
	handler := adminListenerHandler(mux, newAuthState("", nil), false)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/spoolman/api/v1/spool", nil)
	req.Host = "localhost"
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=60" {
		t.Errorf("Cache-Control = %q, want the upstream's own value untouched", cc)
	}
}

// Admin-listener filter: a 401 is logged, a normal 200 is not. adminListenerHandler already includes accessLogMiddleware, so don't wrap it again.
func TestAccessLogAdminFilterOnlyLogsAuthFailuresAndServerErrors(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	auth := newAuthState("secret", nil)
	handler := adminListenerHandler(mux, auth, false)

	var buf bytes.Buffer
	restore := redirectLog(t, &buf)
	defer restore()

	// A 401 (no session): must be logged.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/clients", nil)
	req.Host = "localhost"
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if !strings.Contains(buf.String(), `access admin "GET" "/api/clients" 401`) {
		t.Errorf("log output = %q, want an admin 401 access line", buf.String())
	}

	// A 200 to a route open without a session (the SPA shell, "/"): must
	// NOT be logged -- the admin filter only cares about auth failures and
	// server errors, not ordinary successful traffic.
	buf.Reset()
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/", nil)
	req.Host = "localhost"
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if strings.Contains(buf.String(), "access admin") {
		t.Errorf("log output = %q, want no access line for a 200", buf.String())
	}
}

// TestAccessLogPublicAlwaysLogs pins the public-listener behavior: a
// plain 200 is logged too, unlike the admin filter above -- the public
// listener has no other record of who hit it.
func TestAccessLogPublicAlwaysLogs(t *testing.T) {
	dir := t.TempDir()
	cfg := newPublicTestConfig(t, dir)
	db := openTestDB(t, dir)
	publicMux, _ := buildPublicMux(NewConfigStore(cfg), db, dir, false, false)
	handler := publicListenerHandler(publicMux, false)

	var buf bytes.Buffer
	restore := redirectLog(t, &buf)
	defer restore()

	rec := serveReq(handler, httptest.NewRequest("GET", "/api/public/config", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(buf.String(), `access public "GET" "/api/public/config" 200`) {
		t.Errorf("log output = %q, want a public 200 access line", buf.String())
	}
}

// redirectLog points the standard logger at buf and restores the previous output on the returned func, for the narrow window a test asserts on captured output.
func redirectLog(t *testing.T, buf *bytes.Buffer) func() {
	t.Helper()
	prev := log.Writer()
	log.SetOutput(buf)
	return func() { log.SetOutput(prev) }
}

func TestHSTSOnlyWhenKnownHTTPSOnDNSHost(t *testing.T) {
	h := securityHeadersTLSMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}), true)
	cases := []struct {
		name  string
		host  string
		proto string
		tls   bool
		want  bool
	}{
		{"plain http", "shop.example.com", "", false, false},
		{"forwarded https", "shop.example.com", "https", false, true},
		{"forwarded http", "shop.example.com", "http", false, false},
		{"direct tls", "shop.example.com", "", true, true},
		{"https on ipv4 host", "192.168.1.5:8080", "https", false, false},
		{"https on ipv6 host", "[::1]:8080", "https", false, false},
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", "/", nil)
		req.Host = c.host
		if c.proto != "" {
			req.Header.Set("X-Forwarded-Proto", c.proto)
		}
		if c.tls {
			req.TLS = &tls.ConnectionState{}
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if got := rec.Header().Get("Strict-Transport-Security") != ""; got != c.want {
			t.Errorf("%s: HSTS present = %v, want %v", c.name, got, c.want)
		}
	}
	// Without a trusted proxy a spoofed X-Forwarded-Proto earns nothing.
	untrusted := securityHeadersTLSMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}), false)
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "shop.example.com"
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	untrusted.ServeHTTP(rec, req)
	if rec.Header().Get("Strict-Transport-Security") != "" {
		t.Error("HSTS sent on a spoofed X-Forwarded-Proto without a trusted proxy")
	}
}
