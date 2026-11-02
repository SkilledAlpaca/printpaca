package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Service worker paths
// ---------------------------------------------------------------------------

// The PWA worker lives at /admin/sw.js; the public site registers none, so /sw.js is a plain 404 on both listeners.
func TestRootServiceWorkerIsNotFound(t *testing.T) {
	h, _, _ := mainTestHandler(t, "secret", nil, nil)
	for name, handler := range map[string]http.Handler{
		"main port": h,
		"PUBLIC_PORT": func() http.Handler {
			dir := t.TempDir()
			db := openTestDB(t, dir)
			mux, _ := buildPublicMux(NewConfigStore(newPublicTestConfig(t, dir)), db, dir, false, false)
			return publicListenerHandler(mux, false)
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			rec := serveReq(handler, mainReq("GET", "/sw.js", "127.0.0.1:8080"))
			if rec.Code != http.StatusNotFound {
				t.Fatalf("GET /sw.js = %d, want 404", rec.Code)
			}
		})
	}
}

func TestMainRouterServiceWorkerPaths(t *testing.T) {
	h := stubRouter()
	if got := serveReq(h, httptest.NewRequest("GET", "/sw.js", nil)).Body.String(); got != "public /sw.js" {
		t.Errorf("GET /sw.js reached %q, want the public chain", got)
	}
	if got := serveReq(h, httptest.NewRequest("GET", "/admin/sw.js", nil)).Body.String(); got != "admin /sw.js" {
		t.Errorf("GET /admin/sw.js reached %q, want the admin chain with the prefix stripped", got)
	}
}

// ---------------------------------------------------------------------------
// /healthz outside the admin chain
// ---------------------------------------------------------------------------

func TestMainHandlerHealthzIgnoresTunnelGuardAndAuth(t *testing.T) {
	for _, tc := range []struct {
		name     string
		password string
	}{{"auth off", ""}, {"auth on", "secret"}} {
		t.Run(tc.name, func(t *testing.T) {
			h, _, _ := mainTestHandler(t, tc.password, nil, nil)
			req := mainReq("GET", "/healthz", "127.0.0.1:8080")
			req.Header.Set("CF-Connecting-IP", "203.0.113.7")
			rec := serveReq(h, req)
			if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
				t.Errorf("tunneled /healthz = %d %q, want 200 ok", rec.Code, rec.Body.String())
			}
			if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Errorf("/healthz lacks the public chain's security headers")
			}
		})
	}
}

func TestMainHandlerHealthzRespectsHostAllowlist(t *testing.T) {
	h, _, _ := mainTestHandler(t, "secret", []string{"shop.example.com"}, nil)
	if rec := serveReq(h, mainReq("GET", "/healthz", "evil.example.org")); rec.Code != http.StatusForbidden {
		t.Errorf("/healthz with a disallowed Host = %d, want 403", rec.Code)
	}
	if rec := serveReq(h, mainReq("GET", "/healthz", "shop.example.com")); rec.Code != http.StatusOK {
		t.Errorf("/healthz with an allowed Host = %d, want 200", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Access configured, through the production main handler
// ---------------------------------------------------------------------------

func TestMainHandlerWithAccessConfigured(t *testing.T) {
	f := newCFFixture(t)
	f.load()
	h, _, _ := mainTestHandler(t, "secret", nil, func(a *authState) { a.cfAccess = f.cf })
	const host = "127.0.0.1:8080"
	tunneled := func(method, path string) *http.Request {
		req := mainReq(method, path, host)
		req.Header.Set("CF-Connecting-IP", "203.0.113.7")
		return req
	}

	rec := serveReq(h, tunneled("GET", "/admin/"))
	if rec.Code != http.StatusForbidden {
		t.Errorf("tunneled /admin/ without a token = %d, want 403", rec.Code)
	}
	// Public paths and the readiness probe need no token. "/" and "/order" are 503 only while the embedded
	// dist is the tracked placeholder without public.html; a real build serves 200.
	for _, tc := range []struct {
		path string
		want []int
	}{
		{"/", []int{http.StatusOK, http.StatusServiceUnavailable}},
		{"/order", []int{http.StatusOK, http.StatusServiceUnavailable}},
		{"/api/public/config", []int{http.StatusOK}},
		{"/healthz", []int{http.StatusOK}},
	} {
		rec := serveReq(h, tunneled("GET", tc.path))
		ok := false
		for _, w := range tc.want {
			ok = ok || rec.Code == w
		}
		if !ok {
			t.Errorf("tunneled %s without a token = %d, want one of %v", tc.path, rec.Code, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Router-level handlers sit inside the public chain
// ---------------------------------------------------------------------------

func TestMainHandlerRouterResponsesUsePublicChain(t *testing.T) {
	h, _, _ := mainTestHandler(t, "secret", []string{"shop.example.com"}, nil)

	// Allowed Host: the router's own answers carry the security headers, and /api answers are no-store.
	for _, tc := range []struct {
		path string
		code int
	}{
		{"/admin", http.StatusMovedPermanently},
		{"/api/quotes", http.StatusNotFound},
		{"/api", http.StatusNotFound},
		{"//order", http.StatusMovedPermanently},
		{"/admin/../order", http.StatusMovedPermanently},
	} {
		rec := serveReq(h, mainReq("GET", tc.path, "shop.example.com"))
		if rec.Code != tc.code {
			t.Errorf("GET %s = %d, want %d", tc.path, rec.Code, tc.code)
		}
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("Content-Security-Policy") == "" {
			t.Errorf("GET %s lacks security headers: %v", tc.path, rec.Header())
		}
		if strings.HasPrefix(tc.path, "/api/") && rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("GET %s Cache-Control = %q, want no-store", tc.path, rec.Header().Get("Cache-Control"))
		}
	}

	// Disallowed Host: the allowlist applies to them too, so they reveal nothing.
	for _, path := range []string{"/admin", "/api/quotes", "/api", "//order", "/admin/../order"} {
		rec := serveReq(h, mainReq("GET", path, "evil.example.org"))
		if rec.Code != http.StatusForbidden || rec.Header().Get("Location") != "" {
			t.Errorf("GET %s with a disallowed Host = %d (Location %q), want 403", path, rec.Code, rec.Header().Get("Location"))
		}
	}
}

func TestMainHandlerRouterResponsesAreLogged(t *testing.T) {
	h, _, _ := mainTestHandler(t, "secret", nil, nil)
	var buf bytes.Buffer
	defer redirectLog(t, &buf)()

	serveReq(h, mainReq("GET", "/api/quotes", "127.0.0.1:8080"))
	serveReq(h, mainReq("GET", "/admin", "127.0.0.1:8080"))
	logged := buf.String()
	for _, want := range []string{`access public "GET" "/api/quotes" 404`, `access public "GET" "/admin" 301`} {
		if !strings.Contains(logged, want) {
			t.Errorf("access log lacks %q:\n%s", want, logged)
		}
	}
}

func TestMainHandlerAdminRedirectKeepsQuery(t *testing.T) {
	h, _, _ := mainTestHandler(t, "secret", nil, nil)
	rec := serveReq(h, mainReq("GET", "/admin?next=%2Finvoices&x=1", "127.0.0.1:8080"))
	if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/admin/?next=%2Finvoices&x=1" {
		t.Errorf("/admin?... = %d %q, want 301 /admin/?next=%%2Finvoices&x=1", rec.Code, rec.Header().Get("Location"))
	}
	// The mux's cleaning redirect keeps the query as well.
	rec = serveReq(h, mainReq("GET", "//order?a=b", "127.0.0.1:8080"))
	if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/order?a=b" {
		t.Errorf("//order?a=b = %d %q, want 301 /order?a=b", rec.Code, rec.Header().Get("Location"))
	}
}

func TestMainHandlerShopOffPublicAPIIsJSON404(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	cfg := newPublicTestConfig(t, dir)
	cfg.Site.ShopOnMainPort = false
	store := NewConfigStore(cfg)
	auth := newAuthState("", nil)
	h := buildMainHandler(routesMux(db, store, auth, dir, false), auth, store, db, dir, newPublicShared(), false)

	for _, path := range []string{"/api/public/config", "/api/public/items"} {
		rec := serveReq(h, mainReq("GET", path, "127.0.0.1:8080"))
		if rec.Code != http.StatusNotFound || rec.Header().Get("Content-Type") != "application/json" {
			t.Errorf("GET %s = %d %q, want a JSON 404", path, rec.Code, rec.Header().Get("Content-Type"))
		}
		var body apiErrorBody
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Errors) == 0 {
			t.Errorf("GET %s body = %q, want the writeAPIError shape", path, rec.Body.String())
		}
	}
}

// ---------------------------------------------------------------------------
// Access log records the original path
// ---------------------------------------------------------------------------

func TestMainHandlerAccessLogKeepsAdminPrefix(t *testing.T) {
	h, _, _ := mainTestHandler(t, "secret", nil, nil)
	var buf bytes.Buffer
	defer redirectLog(t, &buf)()

	serveReq(h, mainReq("GET", "/admin/api/me", "127.0.0.1:8080")) // 401, which the admin filter logs
	logged := buf.String()
	if !strings.Contains(logged, `access admin "GET" "/admin/api/me" 401`) {
		t.Errorf("access log lacks the unstripped path:\n%s", logged)
	}
	if strings.Contains(logged, `"/api/me"`) {
		t.Errorf("access log recorded the stripped path:\n%s", logged)
	}
}

// ---------------------------------------------------------------------------
// Bare proxy mount points do not redirect out of /admin
// ---------------------------------------------------------------------------

func TestMainHandlerBareProxyMountPointsDoNotRedirect(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "[]") }))
	defer backend.Close()
	t.Setenv("SPOOLMAN_URL", backend.URL)
	t.Setenv("MOONRAKER_URL", backend.URL)
	t.Setenv("DEMO_MODE", "")

	h, _, _ := mainTestHandler(t, "", nil, nil)
	for _, path := range []string{"/admin/api/spoolman", "/admin/api/moonraker", "/admin/api/spoolman/", "/admin/api/moonraker/"} {
		rec := serveReq(h, mainReq("GET", path, "127.0.0.1:8080"))
		if loc := rec.Header().Get("Location"); loc != "" || rec.Code/100 == 3 {
			t.Errorf("GET %s = %d with Location %q, want no redirect", path, rec.Code, loc)
		}
	}
}

// ---------------------------------------------------------------------------
// Settings PUT without a site object
// ---------------------------------------------------------------------------

func TestSettingsPutWithoutSiteIsRejected(t *testing.T) {
	dir := t.TempDir()
	cfg := loadTestConfig(t, dir)
	cfg.Site = Site{ShopOnMainPort: false, StaffLink: false, AdminURL: "https://admin.example.com/admin/"}
	store := NewConfigStore(cfg)
	mux := newSettingsMux(dir, store)

	data, err := json.Marshal(struct {
		Config *Config `json:"config"`
	}{cfg})
	if err != nil {
		t.Fatal(err)
	}
	var generic map[string]map[string]json.RawMessage
	if err := json.Unmarshal(data, &generic); err != nil {
		t.Fatal(err)
	}
	delete(generic["config"], "site")
	data, _ = json.Marshal(generic)
	if bytes.Contains(data, []byte(`"site"`)) {
		t.Fatalf("test body still has a site key: %s", data)
	}

	rec := serveReq(mux, httptest.NewRequest("PUT", "/api/settings", bytes.NewReader(data)))
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "site: required") {
		t.Fatalf("PUT without site = %d (%s), want 422 site: required", rec.Code, rec.Body.String())
	}
	if got := store.Get().Site; got != cfg.Site {
		t.Errorf("site after rejected PUT = %+v, want it unchanged: %+v", got, cfg.Site)
	}

	// An explicit site object replaces the settings.
	next := cloneConfig(t, store.Get())
	next.Site = Site{ShopOnMainPort: true, StaffLink: true}
	if rec := putSettings(t, mux, next); rec.Code != http.StatusOK {
		t.Fatalf("PUT with site = %d (%s)", rec.Code, rec.Body.String())
	}
	if got := store.Get().Site; got != next.Site {
		t.Errorf("site after explicit PUT = %+v, want %+v", got, next.Site)
	}
}

// ---------------------------------------------------------------------------
// Log lines
// ---------------------------------------------------------------------------

func TestHostRejectionLogNamesTheBranch(t *testing.T) {
	h, _, _ := mainTestHandler(t, "secret", []string{"shop.example.com"}, nil)
	var buf bytes.Buffer
	defer redirectLog(t, &buf)()

	serveReq(h, mainReq("GET", "/order", "evil-public.example.org"))
	serveReq(h, mainReq("GET", "/admin/api/me", "evil-admin.example.org"))
	logged := buf.String()
	if !strings.Contains(logged, `main port: rejected request for disallowed Host "evil-public.example.org"`) {
		t.Errorf("public rejection should name the main port:\n%s", logged)
	}
	if !strings.Contains(logged, `admin listener: rejected request for disallowed Host "evil-admin.example.org"`) {
		t.Errorf("admin rejection should name the admin listener:\n%s", logged)
	}
}

func TestTunnelPasswordOnlyLogMentionsTrustProxy(t *testing.T) {
	for _, tc := range []struct {
		name       string
		trustProxy bool
		wantSuffix bool
	}{{"unset", false, true}, {"set", true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			h, _, _ := mainTestHandler(t, "secret", nil, func(a *authState) { a.trustProxy = tc.trustProxy })
			var buf bytes.Buffer
			defer redirectLog(t, &buf)()
			req := mainReq("GET", "/admin/", "127.0.0.1:8080")
			req.Header.Set("CF-Connecting-IP", "203.0.113.7")
			serveReq(h, req)
			logged := buf.String()
			if !strings.Contains(logged, cloudflarePasswordOnlyLogMessage) {
				t.Fatalf("log lacks the password-only line:\n%s", logged)
			}
			const suffix = "; ADMIN_TRUST_PROXY is unset, so all tunneled clients share one rate-limit bucket"
			if got := strings.Contains(logged, cloudflarePasswordOnlyLogMessage+suffix); got != tc.wantSuffix {
				t.Errorf("suffix present = %v, want %v:\n%s", got, tc.wantSuffix, logged)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Path tricks through the production main handler
// ---------------------------------------------------------------------------

// With a password set and no session, no path spelling reaches an admin API. Each row pins the status the
// production handler gives, so a routing change shows up here.
func TestMainHandlerPathTricksNeverReachAdminAPI(t *testing.T) {
	h, _, _ := mainTestHandler(t, "secret", nil, nil)
	const host = "127.0.0.1:8080"
	cases := []struct {
		method, path string
		want         int
		wantLoc      string // set for redirects
	}{
		{"GET", "/admin/api/me", 401, ""},
		{"GET", "//admin/api/me", 301, "/admin/api/me"},
		{"GET", "/admin//api/me", 301, "/admin/api/me"},
		{"GET", "/admin/../api/me", 301, "/api/me"},
		{"GET", "/admin/%2e%2e/api/me", 404, ""},
		{"GET", "/admin/.%2e/api/me", 404, ""},
		{"GET", "/%61dmin/api/me", 404, ""},
		{"GET", "/admin%2fapi/me", 404, ""},
		{"GET", "/admin/%2fapi/me", 404, ""},
		{"GET", "/admin/api%2fme", 401, ""},
		{"GET", "/ADMIN/api/me", 404, ""},
		{"GET", "/api/me", 404, ""},
		{"GET", "/api/publicX", 404, ""},
		{"GET", "/api/public/../me", 301, "/api/me"},
		{"HEAD", "/admin/api/me", 401, ""},
		{"OPTIONS", "/admin/api/me", 401, ""},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := mainReq(tc.method, "/", host)
			u, err := url.ParseRequestURI(tc.path)
			if err != nil {
				t.Fatal(err)
			}
			req.URL = u
			rec := serveReq(h, req)
			if rec.Code != tc.want {
				t.Fatalf("%s %s = %d, want %d; body: %s", tc.method, tc.path, rec.Code, tc.want, rec.Body.String())
			}
			if loc := rec.Header().Get("Location"); loc != tc.wantLoc {
				t.Errorf("Location = %q, want %q", loc, tc.wantLoc)
			}
			if rec.Code/100 == 2 || strings.Contains(rec.Body.String(), "isAdmin") {
				t.Errorf("%s %s reached an admin API: %d %s", tc.method, tc.path, rec.Code, rec.Body.String())
			}
		})
	}
}
