package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// stubChains returns admin and public handlers that echo which chain ran and the path it saw.
func stubChains() (admin, public http.Handler) {
	echo := func(name string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, name+" "+r.URL.Path)
		})
	}
	return echo("admin"), echo("public")
}

// stubRouter is newMainRouter over stubChains plus an echoing readiness stub and an identity wrapper.
func stubRouter() http.Handler {
	admin, public := stubChains()
	health := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "health "+r.URL.Path) })
	return newMainRouter(admin, public, health, func(h http.Handler) http.Handler { return h })
}

func TestMainRouterDispatch(t *testing.T) {
	h := stubRouter()

	cases := []struct {
		path     string
		wantCode int
		wantBody string // substring
		wantLoc  string
	}{
		{"/admin", http.StatusMovedPermanently, "", "/admin/"},
		{"/admin/", http.StatusOK, "admin /", ""},
		{"/admin/api/me", http.StatusOK, "admin /api/me", ""},
		{"/admin/assets/x.js", http.StatusOK, "admin /assets/x.js", ""},
		{"/healthz", http.StatusOK, "health /healthz", ""},
		{"/api/quotes", http.StatusNotFound, apiNotFoundMessage, ""},
		{"/api/", http.StatusNotFound, apiNotFoundMessage, ""},
		{"/api", http.StatusNotFound, apiNotFoundMessage, ""},
		{"/api/config", http.StatusNotFound, apiNotFoundMessage, ""},
		{"/api/public/config", http.StatusOK, "public /api/public/config", ""},
		{"/", http.StatusOK, "public /", ""},
		{"/order", http.StatusOK, "public /order", ""},
		{"/item-images/a.png", http.StatusOK, "public /item-images/a.png", ""},
		{"/custom.css", http.StatusOK, "public /custom.css", ""},
		{"/assets/app.js", http.StatusOK, "public /assets/app.js", ""},
		// Not under /admin/: no prefix confusion.
		{"/administrator", http.StatusOK, "public /administrator", ""},
		{"/Admin/", http.StatusOK, "public /Admin/", ""},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			rec := serveReq(h, httptest.NewRequest("GET", tc.path, nil))
			if rec.Code != tc.wantCode {
				t.Fatalf("GET %s = %d, want %d; body: %s", tc.path, rec.Code, tc.wantCode, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Errorf("GET %s body = %q, want it to contain %q", tc.path, rec.Body.String(), tc.wantBody)
			}
			if got := rec.Header().Get("Location"); got != tc.wantLoc {
				t.Errorf("GET %s Location = %q, want %q", tc.path, got, tc.wantLoc)
			}
		})
	}
}

// Traversal out of /admin/ must not reach the admin chain with an unstripped or mangled path.
func TestMainRouterCleansTraversal(t *testing.T) {
	h := stubRouter()
	for _, path := range []string{"/admin/../api/quotes", "/admin//api/me", "/admin/./api/me"} {
		rec := serveReq(h, httptest.NewRequest("GET", path, nil))
		if strings.Contains(rec.Body.String(), "admin /") && !strings.Contains(rec.Body.String(), "admin /api/") {
			t.Errorf("GET %s reached the admin chain with %q", path, rec.Body.String())
		}
		if rec.Code == http.StatusOK && strings.HasPrefix(rec.Body.String(), "admin //") {
			t.Errorf("GET %s reached the admin chain with a doubled slash: %q", path, rec.Body.String())
		}
	}
	// The classic escape resolves to /api/quotes, which is the /api 404, never an admin handler.
	rec := serveReq(h, httptest.NewRequest("GET", "/admin/../api/quotes", nil))
	if rec.Code == http.StatusOK {
		t.Errorf("GET /admin/../api/quotes = 200 %q, want a redirect or the /api 404", rec.Body.String())
	}
}

func TestMainRouterAPIPathIsJSONNotRedirect(t *testing.T) {
	rec := serveReq(stubRouter(), httptest.NewRequest("POST", "/api/quotes", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST /api/quotes = %d, want 404", rec.Code)
	}
	if rec.Header().Get("Location") != "" {
		t.Errorf("/api must not redirect, got Location %q", rec.Header().Get("Location"))
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if !strings.Contains(rec.Body.String(), apiNotFoundMessage) {
		t.Errorf("body %q should carry %q", rec.Body.String(), apiNotFoundMessage)
	}
}

// mainTestHandler wires the production main-port handler (buildMainHandler) over a fresh database.
func mainTestHandler(t *testing.T, password string, allowedHosts []string, mutate func(*authState)) (http.Handler, *authState, *sql.DB) {
	t.Helper()
	dir := t.TempDir()
	db := openTestDB(t, dir)
	cfg := newPublicTestConfig(t, dir)
	store := NewConfigStore(cfg)
	auth := newAuthState(password, allowedHosts)
	if mutate != nil {
		mutate(auth)
	}
	adminMux := routesMux(db, store, auth, dir, false)
	return buildMainHandler(adminMux, auth, store, db, dir, newPublicShared(), false), auth, db
}

func mainReq(method, path, host string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	req.Host = host
	return req
}

func TestMainHandlerRoutes(t *testing.T) {
	h, _, _ := mainTestHandler(t, "", nil, nil) // auth off: /admin/api/me reaches the handler directly

	rec := serveReq(h, mainReq("GET", "/admin", "127.0.0.1:8080"))
	if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/admin/" {
		t.Errorf("/admin = %d %q, want 301 /admin/", rec.Code, rec.Header().Get("Location"))
	}

	rec = serveReq(h, mainReq("GET", "/admin/", "127.0.0.1:8080"))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), adminBundleMarker) {
		t.Errorf("/admin/ = %d %q, want the admin index", rec.Code, rec.Body.String())
	}

	rec = serveReq(h, mainReq("GET", "/admin/api/me", "127.0.0.1:8080"))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "isAdmin") {
		t.Errorf("/admin/api/me = %d %q, want the admin handler's JSON", rec.Code, rec.Body.String())
	}

	rec = serveReq(h, mainReq("GET", "/api/quotes", "127.0.0.1:8080"))
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), apiNotFoundMessage) {
		t.Errorf("/api/quotes = %d %q, want the /api 404", rec.Code, rec.Body.String())
	}

	rec = serveReq(h, mainReq("GET", "/api/public/config", "127.0.0.1:8080"))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"materials"`) {
		t.Errorf("/api/public/config = %d %q, want the public config", rec.Code, rec.Body.String())
	}

	// The placeholder dist has no public.html (503); a real build serves it (200). Never the admin bundle.
	for _, path := range []string{"/", "/order"} {
		rec = serveReq(h, mainReq("GET", path, "127.0.0.1:8080"))
		if rec.Code != http.StatusOK && rec.Code != http.StatusServiceUnavailable {
			t.Errorf("GET %s = %d, want 200 or 503", path, rec.Code)
		}
		if strings.Contains(rec.Body.String(), adminBundleMarker) {
			t.Errorf("GET %s served the admin bundle: %q", path, rec.Body.String())
		}
	}

	// The admin bundle is not reachable at the root through the public branch.
	rec = serveReq(h, mainReq("GET", "/index.html", "127.0.0.1:8080"))
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /index.html = %d, want 404 (admin index only under /admin/)", rec.Code)
	}

	// /healthz keeps the real readiness probe.
	rec = serveReq(h, mainReq("GET", "/healthz", "127.0.0.1:8080"))
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Errorf("/healthz = %d %q, want 200 ok", rec.Code, rec.Body.String())
	}
}

// The Host allowlist covers public paths and admin paths alike.
func TestMainHandlerHostAllowlistCoversPublicAndAdmin(t *testing.T) {
	h, _, _ := mainTestHandler(t, "secret", []string{"shop.example.com"}, nil)

	for _, path := range []string{"/", "/order", "/api/public/config", "/admin/", "/admin/api/me", "/custom.css"} {
		rec := serveReq(h, mainReq("GET", path, "evil.example.org"))
		if rec.Code != http.StatusForbidden || rec.Body.String() != allowedHostsErrorBody {
			t.Errorf("GET %s with a disallowed Host = %d %q, want the allowlist 403", path, rec.Code, rec.Body.String())
		}
	}
	rec := serveReq(h, mainReq("GET", "/api/public/config", "shop.example.com"))
	if rec.Code != http.StatusOK {
		t.Errorf("allowlisted Host on a public path = %d, want 200", rec.Code)
	}
	rec = serveReq(h, mainReq("GET", "/admin/", "shop.example.com"))
	if rec.Code != http.StatusOK {
		t.Errorf("allowlisted Host on /admin/ = %d, want 200", rec.Code)
	}
}

// Auth, CSRF and the tunnel guard apply to every /admin/* request, and the SPA shell stays open for login.
func TestMainHandlerAdminBranchStaysGuarded(t *testing.T) {
	h, _, _ := mainTestHandler(t, "secret", nil, nil)
	const host = "127.0.0.1:8080"

	// No session: every admin API route is a 401, including ones that exist only under /admin/api/.
	for _, path := range []string{"/admin/api/me", "/admin/api/quotes", "/admin/api/clients", "/admin/api/settings", "/admin/api/export", "/admin/api/users"} {
		rec := serveReq(h, mainReq("GET", path, host))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s without a session = %d, want 401", path, rec.Code)
		}
	}
	// The shell is open so the login page can load.
	if rec := serveReq(h, mainReq("GET", "/admin/", host)); rec.Code != http.StatusOK {
		t.Errorf("GET /admin/ = %d, want 200", rec.Code)
	}

	// CSRF: a non-JSON body and a foreign Origin are rejected before the session check.
	req := httptest.NewRequest("POST", "/admin/api/quotes", strings.NewReader("x"))
	req.Host = host
	req.Header.Set("Content-Type", "text/plain")
	if rec := serveReq(h, req); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("POST text/plain to /admin/api/quotes = %d, want 415", rec.Code)
	}
	req = httptest.NewRequest("POST", "/admin/api/login", strings.NewReader(`{"password":"secret"}`))
	req.Host = host
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example.org")
	if rec := serveReq(h, req); rec.Code != http.StatusForbidden {
		t.Errorf("cross-origin POST to /admin/api/login = %d, want 403", rec.Code)
	}

	// A real login works through the prefix and its cookie authenticates /admin/api/me.
	req = httptest.NewRequest("POST", "/admin/api/login", strings.NewReader(`{"password":"secret"}`))
	req.Host = host
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://"+host)
	rec := serveReq(h, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login through /admin/api/login = %d (%s)", rec.Code, rec.Body.String())
	}
	req = mainReq("GET", "/admin/api/me", host)
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(c)
	}
	if rec := serveReq(h, req); rec.Code != http.StatusOK {
		t.Errorf("GET /admin/api/me with the session cookie = %d, want 200", rec.Code)
	}
}

// Cloudflare headers on the public branch never trigger the admin tunnel guard.
func TestMainHandlerPublicBranchIgnoresTunnelGuard(t *testing.T) {
	h, _, _ := mainTestHandler(t, "", nil, nil) // auth off: the admin branch would 403 these headers
	req := mainReq("GET", "/api/public/config", "127.0.0.1:8080")
	req.Header.Set("CF-Connecting-IP", "203.0.113.7")
	if rec := serveReq(h, req); rec.Code != http.StatusOK {
		t.Errorf("tunneled public request = %d, want 200", rec.Code)
	}
	req = mainReq("GET", "/admin/api/me", "127.0.0.1:8080")
	req.Header.Set("CF-Connecting-IP", "203.0.113.7")
	if rec := serveReq(h, req); rec.Code != http.StatusForbidden {
		t.Errorf("tunneled /admin/ with auth off = %d, want 403", rec.Code)
	}
}

// A submission through one listener's mux spends the per-IP budget of the other.
func TestPublicSharedStateSpansListeners(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	store := NewConfigStore(newPublicTestConfig(t, dir))
	shared := newPublicShared()
	mainSide := buildPublicMuxShared(store, db, dir, shared, false, false, mainStaffURL)
	publicSide := buildPublicMuxShared(store, db, dir, shared, false, false, mainStaffURL)

	post := func(mux http.Handler) int {
		req := httptest.NewRequest("POST", "/api/public/item-order", strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		return serveReq(mux, req).Code
	}
	for i := range publicRateLimit {
		if code := post(mainSide); code == http.StatusTooManyRequests {
			t.Fatalf("submission %d on the main side was rate limited early", i+1)
		}
	}
	if code := post(publicSide); code != http.StatusTooManyRequests {
		t.Errorf("submission on the other listener = %d, want 429 (shared per-IP budget)", code)
	}

	// An independent state does not share the budget.
	other := buildPublicMuxShared(store, db, dir, newPublicShared(), false, false, mainStaffURL)
	if code := post(other); code == http.StatusTooManyRequests {
		t.Errorf("a separate publicShared must start with a fresh budget, got 429")
	}
}

func TestNewMainServerTimeouts(t *testing.T) {
	srv := newMainServer(":0", http.NotFoundHandler())
	if srv.ReadTimeout != 15*time.Second {
		t.Errorf("ReadTimeout = %v, want 15s", srv.ReadTimeout)
	}
	if srv.ReadHeaderTimeout != 15*time.Second {
		t.Errorf("ReadHeaderTimeout = %v, want 15s", srv.ReadHeaderTimeout)
	}
}

// A G-code upload dribbled over a connection slower than the main server's real ReadTimeout (15s) still
// lands on the main port: the handler's own deadlines replace ReadTimeout and WriteTimeout, as on PUBLIC_PORT.
// The server below takes its timeouts from newMainServer, so a change to the production values is tested.
// The upload takes about 17 seconds on purpose, so the test is skipped in -short mode.
func TestMainPortGcodeUploadSucceeds(t *testing.T) {
	if testing.Short() {
		t.Skip("dribbles an upload for longer than the 15s main-server ReadTimeout")
	}
	t.Parallel()
	h, _, _ := mainTestHandler(t, "secret", nil, nil)
	real := newMainServer(":0", h)
	srv := httptest.NewUnstartedServer(h)
	srv.Config.ReadHeaderTimeout = real.ReadHeaderTimeout
	srv.Config.ReadTimeout = real.ReadTimeout
	srv.Config.WriteTimeout = real.WriteTimeout
	srv.Config.IdleTimeout = real.IdleTimeout
	srv.Start()
	defer srv.Close()

	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	for k, v := range publicQuoteRequestFields("PLA") {
		mw.WriteField(k, v)
	}
	fw, _ := mw.CreateFormFile("gcode", "model.gcode")
	fw.Write([]byte("G1 X1 Y1\nG1 X2 Y2\nG1 X3 Y3\n"))
	mw.Close()

	const chunks = 10
	delay := (real.ReadTimeout + 2*time.Second) / chunks
	slow := &slowBodyReader{data: body.Bytes(), chunkSize: body.Len()/chunks + 1, delay: delay}
	req, err := http.NewRequest("POST", srv.URL+"/api/public/quote-request", slow)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	start := time.Now()
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("upload on the main port: %v", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if elapsed := time.Since(start); elapsed <= real.ReadTimeout {
		t.Fatalf("upload took %v, which does not exceed ReadTimeout %v; the test proves nothing", elapsed, real.ReadTimeout)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload on the main port = %d, want 201; body: %s", resp.StatusCode, respBody)
	}
}

// staffUrl is computed per listener from the live config.
func TestPublicConfigStaffURLPerListener(t *testing.T) {
	cases := []struct {
		name       string
		site       Site
		mainWant   string
		publicWant string
	}{
		{"defaults", Site{ShopOnMainPort: true, StaffLink: true}, "/admin/", ""},
		{"staff link off", Site{ShopOnMainPort: true, StaffLink: false}, "", ""},
		{"admin url set", Site{ShopOnMainPort: true, StaffLink: true, AdminURL: "https://admin.example.com/admin/"}, "/admin/", "https://admin.example.com/admin/"},
		{"admin url set, staff link off", Site{ShopOnMainPort: true, StaffLink: false, AdminURL: "https://admin.example.com/admin/"}, "", "https://admin.example.com/admin/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			db := openTestDB(t, dir)
			cfg := newPublicTestConfig(t, dir)
			cfg.Site = tc.site
			store := NewConfigStore(cfg)
			mainMux := buildPublicMuxShared(store, db, dir, newPublicShared(), false, false, mainStaffURL)
			publicMux, _ := buildPublicMux(store, db, dir, false, false)

			for name, c := range map[string]struct {
				mux  http.Handler
				want string
			}{"main": {mainMux, tc.mainWant}, "PUBLIC_PORT": {publicMux, tc.publicWant}} {
				rec := serveReq(c.mux, httptest.NewRequest("GET", "/api/public/config", nil))
				var got struct {
					StaffUrl *string `json:"staffUrl"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.StaffUrl == nil {
					t.Fatalf("%s: staffUrl missing from %s (%v)", name, rec.Body.String(), err)
				}
				if *got.StaffUrl != c.want {
					t.Errorf("%s: staffUrl = %q, want %q", name, *got.StaffUrl, c.want)
				}
			}
		})
	}
}

// site.shopOnMainPort off: / redirects to /admin/, the other public routes 404, admin keeps working, and a
// Settings change applies on the next request.
func TestMainHandlerShopOnMainPortOff(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	cfg := newPublicTestConfig(t, dir)
	cfg.Site.ShopOnMainPort = false
	store := NewConfigStore(cfg)
	auth := newAuthState("", nil)
	adminMux := routesMux(db, store, auth, dir, false)
	h := buildMainHandler(adminMux, auth, store, db, dir, newPublicShared(), false)
	const host = "127.0.0.1:8080"

	rec := serveReq(h, mainReq("GET", "/", host))
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/admin/" {
		t.Errorf("GET / = %d %q, want 302 /admin/", rec.Code, rec.Header().Get("Location"))
	}
	for _, path := range []string{"/order", "/api/public/config", "/custom.css", "/item-images/x.png", "/assets/x.js"} {
		if rec := serveReq(h, mainReq("GET", path, host)); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, rec.Code)
		}
	}
	if rec := serveReq(h, mainReq("GET", "/admin/api/me", host)); rec.Code != http.StatusOK {
		t.Errorf("GET /admin/api/me = %d, want 200", rec.Code)
	}
	if rec := serveReq(h, mainReq("GET", "/admin/custom.css", host)); rec.Code != http.StatusOK {
		t.Errorf("GET /admin/custom.css = %d, want 200 (admin keeps its own copy)", rec.Code)
	}
	if rec := serveReq(h, mainReq("GET", "/healthz", host)); rec.Code != http.StatusOK {
		t.Errorf("GET /healthz = %d, want 200", rec.Code)
	}

	// Hot-swap, as handleSettingsPut does.
	next := *cfg
	next.Site.ShopOnMainPort = true
	store.Set(&next)
	if rec := serveReq(h, mainReq("GET", "/api/public/config", host)); rec.Code != http.StatusOK {
		t.Errorf("after enabling, GET /api/public/config = %d, want 200", rec.Code)
	}
}
