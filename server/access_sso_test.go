package main

// Tests for Cloudflare Access as the login: a verified request is authenticated by its token's email claim,
// and CF_ACCESS_REQUIRE_PASSWORD=1 restores the two-lock behavior. Fixtures live in cfaccess_test.go.

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const ssoHost = "admin.example.com"

// ssoEnv is a full route table behind auth.middleware with Access mode on.
type ssoEnv struct {
	t     *testing.T
	f     *cfFixture
	h     http.Handler
	auth  *authState
	store *ConfigStore
	db    *sql.DB
}

// newSSOEnv builds the production route table (registerRoutes) with the
// Access verifier attached. multi turns users.multiUser on; adminEmail sets
// users.accessAdminEmail; requirePassword is CF_ACCESS_REQUIRE_PASSWORD=1.
func newSSOEnv(t *testing.T, multi bool, adminEmail string, requirePassword bool) *ssoEnv {
	t.Helper()
	f := newCFFixture(t)
	f.load()
	dir := t.TempDir()
	db := openTestDB(t, dir)
	cfg := loadTestConfig(t, dir)
	cfg.Users.MultiUser = multi
	cfg.Users.AccessAdminEmail = strings.ToLower(adminEmail) // normalizeConfig's job in production
	store := NewConfigStore(cfg)
	auth := newAuthState("adminpw", nil)
	f.cf.requirePassword = requirePassword
	auth.cfAccess = f.cf
	mux := routesMux(db, store, auth, dir, false)
	// main() registers GET /api/config and PUT /api/settings outside registerRoutes.
	mux.HandleFunc("GET /api/config", handleConfig(store, configExtras{authEnabled: true}, auth))
	mux.HandleFunc("PUT /api/settings", handleSettingsPut(dir, store, auth))
	return &ssoEnv{t: t, f: f, h: auth.middleware(mux), auth: auth, store: store, db: db}
}

// request builds a request from an Access-verified caller whose token carries email ("" omits the claim). sameOrigin
// adds the Origin a browser would send on a write; hdr adds or overrides headers.
func (e *ssoEnv) request(method, path, body, email string, sameOrigin bool, hdr map[string]string) *http.Request {
	e.t.Helper()
	tok := e.f.token(func(o *tokOpt) {
		if email != "" {
			o.claims["email"] = email
		}
	})
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = ssoHost
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
		if sameOrigin {
			req.Header.Set("Origin", "https://"+ssoHost)
		}
	}
	req.Header.Set("Cf-Access-Jwt-Assertion", tok)
	req.Header.Set("CF-Connecting-IP", "203.0.113.9")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return req
}

// do sends a same-origin request through the middleware as an Access-verified caller, optionally with a session cookie.
func (e *ssoEnv) do(method, path, body, email string, cookie *http.Cookie) *httptest.ResponseRecorder {
	e.t.Helper()
	req := e.request(method, path, body, email, true, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	return serveReq(e.h, req)
}

type meBody struct {
	Username    string   `json:"username"`
	IsAdmin     bool     `json:"isAdmin"`
	Permissions []string `json:"permissions"`
	SignedInVia string   `json:"signedInVia"`
	AccessEmail string   `json:"accessEmail"`
}

func (e *ssoEnv) me(email string, cookie *http.Cookie) (int, meBody) {
	e.t.Helper()
	rec := e.do("GET", "/api/me", "", email, cookie)
	var b meBody
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
			e.t.Fatalf("decode /api/me: %v (%s)", err, rec.Body.String())
		}
	}
	return rec.Code, b
}

// passwordCookie signs in with the env admin password through the verified
// request path and returns the session cookie.
func (e *ssoEnv) passwordCookie() *http.Cookie {
	e.t.Helper()
	rec := e.do("POST", "/api/login", `{"password":"adminpw"}`, "nobody@example.com", nil)
	if rec.Code != http.StatusOK {
		e.t.Fatalf("password login = %d (%s)", rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == authCookieName {
			return c
		}
	}
	e.t.Fatal("no session cookie")
	return nil
}

func TestAccessSSOSingleUserNeedsNoCookie(t *testing.T) {
	e := newSSOEnv(t, false, "", false)
	code, me := e.me("Owner@Example.com", nil)
	if code != http.StatusOK {
		t.Fatalf("verified request with no cookie = %d, want 200", code)
	}
	if !me.IsAdmin || me.Username != "admin" || me.SignedInVia != "access" || me.AccessEmail != "owner@example.com" {
		t.Fatalf("/api/me = %+v, want admin via access as owner@example.com", me)
	}
	// Any other /api route works too, but a token with no email claim (a service token) names nobody and is
	// answered with 401 (so the web app offers the password sign-in), even in single-user mode.
	if rec := e.do("GET", "/api/clients", "", "owner@example.com", nil); rec.Code != http.StatusOK {
		t.Fatalf("GET /api/clients with an email token = %d, want 200", rec.Code)
	}
	rec0 := e.do("GET", "/api/clients", "", "", nil)
	if rec0.Code != http.StatusUnauthorized {
		t.Fatalf("GET /api/clients with an email-less token = %d (%s), want 401", rec0.Code, rec0.Body.String())
	}
	// The "user" extra on /api/config carries the same fields.
	rec := e.do("GET", "/api/config", "", "owner@example.com", nil)
	var cfg struct {
		User meBody `json:"user"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &cfg); err != nil || cfg.User.SignedInVia != "access" {
		t.Fatalf("/api/config user = %+v (err %v), want signedInVia access", cfg.User, err)
	}
}

func TestAccessSSORequirePasswordKeepsTwoLocks(t *testing.T) {
	e := newSSOEnv(t, false, "", true)
	if code, _ := e.me("owner@example.com", nil); code != http.StatusUnauthorized {
		t.Fatalf("verified request without a session = %d, want 401 under CF_ACCESS_REQUIRE_PASSWORD=1", code)
	}
	cookie := e.passwordCookie()
	code, me := e.me("owner@example.com", cookie)
	if code != http.StatusOK || me.SignedInVia != "password" || me.AccessEmail != "" {
		t.Fatalf("with a session: %d %+v, want 200 via password", code, me)
	}
}

func TestAccessSSOMultiUser(t *testing.T) {
	e := newSSOEnv(t, true, "Boss@Example.com", false)
	if _, _, err := createUser(e.db, "flo", "password123", []string{"clients"}, "flo@example.com"); err != nil {
		t.Fatal(err)
	}
	u, _, err := createUser(e.db, "gus", "password123", []string{"clients"}, "gus@example.com")
	if err != nil {
		t.Fatal(err)
	}

	// Admin email.
	code, me := e.me("boss@example.com", nil)
	if code != http.StatusOK || !me.IsAdmin || me.SignedInVia != "access" {
		t.Fatalf("admin email: %d %+v, want admin via access", code, me)
	}

	// Employee match gets that employee's permissions.
	code, me = e.me("FLO@example.com", nil)
	if code != http.StatusOK || me.IsAdmin || me.Username != "flo" || me.SignedInVia != "access" || me.AccessEmail != "flo@example.com" {
		t.Fatalf("employee email: %d %+v", code, me)
	}
	if len(me.Permissions) != 1 || me.Permissions[0] != "clients" {
		t.Fatalf("employee permissions = %v, want [clients]", me.Permissions)
	}
	if rec := e.do("GET", "/api/clients", "", "flo@example.com", nil); rec.Code != http.StatusOK {
		t.Fatalf("granted route = %d, want 200", rec.Code)
	}
	if rec := e.do("GET", "/api/quotes", "", "flo@example.com", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("ungranted route = %d, want 403", rec.Code)
	}
	if rec := e.do("GET", "/api/users", "", "flo@example.com", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("admin-only route = %d, want 403", rec.Code)
	}

	// A disabled user falls back to the login flow.
	disabled := true
	if _, _, err := updateUserFields(e.db, u.ID, nil, &disabled, nil); err != nil {
		t.Fatal(err)
	}
	if code, _ := e.me("gus@example.com", nil); code != http.StatusUnauthorized {
		t.Fatalf("disabled user = %d, want 401", code)
	}

	// An unmatched email and a missing email claim both fall back to 401.
	if code, _ := e.me("stranger@example.com", nil); code != http.StatusUnauthorized {
		t.Fatalf("unmatched email = %d, want 401", code)
	}
	if code, _ := e.me("", nil); code != http.StatusUnauthorized {
		t.Fatalf("missing email claim = %d, want 401", code)
	}

	// The fallback login still works for the unmatched person.
	cookie := e.passwordCookie()
	code, me = e.me("stranger@example.com", cookie)
	if code != http.StatusOK || !me.IsAdmin || me.SignedInVia != "password" {
		t.Fatalf("after password login: %d %+v, want admin via password", code, me)
	}
}

func TestAccessSSOSessionCookieOverridesToken(t *testing.T) {
	e := newSSOEnv(t, true, "", false)
	if _, _, err := createUser(e.db, "flo", "password123", []string{"clients"}, "flo@example.com"); err != nil {
		t.Fatal(err)
	}
	cookie := e.passwordCookie() // env admin session
	code, me := e.me("flo@example.com", cookie)
	if code != http.StatusOK || !me.IsAdmin || me.SignedInVia != "password" {
		t.Fatalf("session + employee token: %d %+v, want the session's admin identity", code, me)
	}
}

func TestAccessSSOAuditUsesMappedIdentity(t *testing.T) {
	createdBy := func(e *ssoEnv, email string) string {
		t.Helper()
		rec := e.do("POST", "/api/clients", `{"name":"Acme"}`, email, nil)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create client = %d (%s)", rec.Code, rec.Body.String())
		}
		var c Client
		if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		return c.CreatedBy
	}

	single := newSSOEnv(t, false, "", false)
	if got := createdBy(single, "owner@example.com"); got != "admin" {
		t.Fatalf("single-user createdBy = %q, want admin", got)
	}

	multi := newSSOEnv(t, true, "", false)
	if _, _, err := createUser(multi.db, "flo", "password123", []string{"clients"}, "flo@example.com"); err != nil {
		t.Fatal(err)
	}
	if got := createdBy(multi, "flo@example.com"); got != "flo" {
		t.Fatalf("multi-user createdBy = %q, want flo", got)
	}
}

func TestAccessSSOAuthDisabledStillMarksAccess(t *testing.T) {
	f := newCFFixture(t)
	f.load()
	dir := t.TempDir()
	db := openTestDB(t, dir)
	cfg, _ := LoadConfig(dir)
	auth := newAuthState("", nil)
	auth.cfAccess = f.cf
	mux := routesMux(db, NewConfigStore(cfg), auth, dir, false)
	req := cfReq("GET", "/api/me", ssoHost, "", map[string]string{
		"Cf-Access-Jwt-Assertion": f.token(func(o *tokOpt) { o.claims["email"] = "owner@example.com" }),
		"CF-Connecting-IP":        "203.0.113.9",
	})
	rec := serveReq(auth.middleware(mux), req)
	var me meBody
	json.Unmarshal(rec.Body.Bytes(), &me)
	if rec.Code != http.StatusOK || me.SignedInVia != "access" || !me.IsAdmin {
		t.Fatalf("auth disabled + token: %d %+v", rec.Code, me)
	}
	// A LAN request with auth disabled reports "none".
	rec = httptest.NewRecorder()
	lan := httptest.NewRequest("GET", "/api/me", nil)
	lan.Host = "localhost"
	auth.middleware(mux).ServeHTTP(rec, lan)
	me = meBody{}
	json.Unmarshal(rec.Body.Bytes(), &me)
	if me.SignedInVia != "none" {
		t.Fatalf("LAN auth disabled signedInVia = %q, want none", me.SignedInVia)
	}
}

func TestAccessSignedInViaPasswordOnLAN(t *testing.T) {
	e := newSSOEnv(t, false, "", false)
	cookie := e.passwordCookie()
	req := httptest.NewRequest("GET", "/api/me", nil)
	req.Host = "localhost"
	req.AddCookie(cookie)
	rec := serveReq(e.h, req)
	var me meBody
	json.Unmarshal(rec.Body.Bytes(), &me)
	if me.SignedInVia != "password" || me.AccessEmail != "" {
		t.Fatalf("LAN session /api/me = %+v, want password", me)
	}
}

// ---------------------------------------------------------------------------
// users.email: API, uniqueness
// ---------------------------------------------------------------------------

func TestUsersAPIEmail(t *testing.T) {
	handler, _, _ := newMultiUserTestMux(t, false)
	admin := adminSessionCookie(t, handler)

	rec := doJSON(t, handler, "POST", "/api/users", admin, map[string]any{"username": "flo", "password": "password123", "permissions": []string{"clients"}, "email": " Flo@Example.com "})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d (%s)", rec.Code, rec.Body.String())
	}
	var flo User
	json.Unmarshal(rec.Body.Bytes(), &flo)
	if flo.Email != "flo@example.com" {
		t.Fatalf("created email = %q, want lowercased and trimmed", flo.Email)
	}

	// Case-insensitive duplicate on create: 409.
	rec = doJSON(t, handler, "POST", "/api/users", admin, map[string]any{"username": "gus", "password": "password123", "email": "FLO@example.com"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate email on create = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
	// Invalid shapes: 422.
	for _, bad := range []string{"no-at-sign", "a b@example.com", "a@", "@b.com", strings.Repeat("a", 250) + "@example.com"} {
		rec = doJSON(t, handler, "POST", "/api/users", admin, map[string]any{"username": "hal", "password": "password123", "email": bad})
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("email %q on create = %d, want 422", bad[:min(len(bad), 20)], rec.Code)
		}
	}
	// No email is fine, twice (empty emails don't collide).
	for _, name := range []string{"ida", "jon"} {
		rec = doJSON(t, handler, "POST", "/api/users", admin, map[string]any{"username": name, "password": "password123"})
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s without email = %d (%s)", name, rec.Code, rec.Body.String())
		}
	}

	// PUT sets, rejects a duplicate, and clears.
	rec = doJSON(t, handler, "PUT", "/api/users/"+flo.ID, admin, map[string]any{"email": "flo2@example.com"})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"email":"flo2@example.com"`) {
		t.Fatalf("PUT email = %d (%s)", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, handler, "GET", "/api/users", admin, nil)
	if !strings.Contains(rec.Body.String(), `"email":"flo2@example.com"`) {
		t.Fatalf("list should return email: %s", rec.Body.String())
	}
	var ida User
	rec = doJSON(t, handler, "GET", "/api/users", admin, nil)
	var list struct {
		Users []User `json:"users"`
	}
	json.Unmarshal(rec.Body.Bytes(), &list)
	for _, u := range list.Users {
		if u.Username == "ida" {
			ida = u
		}
	}
	rec = doJSON(t, handler, "PUT", "/api/users/"+ida.ID, admin, map[string]any{"email": "Flo2@example.com"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("PUT duplicate email = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, handler, "PUT", "/api/users/"+flo.ID, admin, map[string]any{"email": ""})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"email":""`) {
		t.Fatalf("PUT clear email = %d (%s)", rec.Code, rec.Body.String())
	}
	// The cleared address is free again.
	rec = doJSON(t, handler, "PUT", "/api/users/"+ida.ID, admin, map[string]any{"email": "flo2@example.com"})
	if rec.Code != http.StatusOK {
		t.Fatalf("reuse cleared email = %d (%s)", rec.Code, rec.Body.String())
	}
	// Strict decoding stays on.
	rec = doJSON(t, handler, "PUT", "/api/users/"+ida.ID, admin, map[string]any{"emial": "x@example.com"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field = %d, want 400", rec.Code)
	}
	// An omitted email leaves it alone.
	rec = doJSON(t, handler, "PUT", "/api/users/"+ida.ID, admin, map[string]any{"disabled": false})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"email":"flo2@example.com"`) {
		t.Fatalf("PUT without email = %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestUserEmailUniqueIndexCaseInsensitive(t *testing.T) {
	db := openTestDB(t, t.TempDir())
	if _, _, err := createUser(db, "flo", "password123", nil, "flo@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := createUser(db, "gus", "password123", nil, "FLO@example.com"); err != errEmailTaken {
		t.Fatalf("duplicate email err = %v, want errEmailTaken", err)
	}
	// The index itself rejects a case-variant even when the pre-check is
	// bypassed.
	_, err := db.Exec(`INSERT INTO users (id, username, password_hash, created_at, updated_at, email) VALUES ('x','zed','h','t','t','Flo@Example.com')`)
	if err == nil {
		t.Fatal("unique index should reject a case-variant duplicate email")
	}
	if _, ok, _ := getUserByEmail(db, "FLO@EXAMPLE.COM"); !ok {
		t.Fatal("getUserByEmail should match case-insensitively")
	}
	if _, ok, _ := getUserByEmail(db, ""); ok {
		t.Fatal("getUserByEmail must not match an empty email")
	}
}

// ---------------------------------------------------------------------------
// users.accessAdminEmail: config schema, strict decode, validation
// ---------------------------------------------------------------------------

func TestConfigAccessAdminEmail(t *testing.T) {
	dir := t.TempDir()
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("users:\n  multiUser: true\n  accessAdminEmail: \"  Boss@Example.com \"\n")
	cfg := loadTestConfig(t, dir)
	if cfg.Users.AccessAdminEmail != "boss@example.com" {
		t.Fatalf("accessAdminEmail = %q, want lowercased and trimmed", cfg.Users.AccessAdminEmail)
	}
	write("users:\n  accessAdminEmail: not-an-email\n")
	if _, err := LoadConfig(dir); err == nil || !strings.Contains(err.Error(), "users.accessAdminEmail") {
		t.Fatalf("bad email err = %v, want one naming users.accessAdminEmail", err)
	}
	write("users:\n  accessAdminMail: x@example.com\n")
	if _, err := LoadConfig(dir); err == nil {
		t.Fatal("an unknown users key must fail strict YAML decoding")
	}
	write("users:\n  accessAdminEmail: " + strings.Repeat("a", 250) + "@example.com\n")
	if _, err := LoadConfig(dir); err == nil {
		t.Fatal("an over-long email must be rejected")
	}
}

func TestSettingsPutAccessAdminEmail(t *testing.T) {
	dir := t.TempDir()
	running, err := LoadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	store := NewConfigStore(running)
	mux := newSettingsMux(dir, store)

	desired := cloneConfig(t, running)
	desired.Users.AccessAdminEmail = "Boss@Example.com"
	if rec := putSettings(t, mux, desired); rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d (%s)", rec.Code, rec.Body.String())
	}
	if got := store.Get().Users.AccessAdminEmail; got != "boss@example.com" {
		t.Fatalf("stored accessAdminEmail = %q", got)
	}
	reloaded, err := LoadConfig(dir)
	if err != nil || reloaded.Users.AccessAdminEmail != "boss@example.com" {
		t.Fatalf("reload = %+v, %v; want the saved email", reloaded.Users, err)
	}

	desired.Users.AccessAdminEmail = "nope"
	if rec := putSettings(t, mux, desired); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad email PUT = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}

	rec := serveReq(mux, httptest.NewRequest("PUT", "/api/settings", strings.NewReader(`{"config":{"users":{"bogus":1}}}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown users key over JSON = %d, want 400", rec.Code)
	}
}

// Review hardening: employee-token limits, token-only CSRF, email folding, adminEmail collisions, /api/config redaction.

// doRaw sends a token-authenticated request with exactly the given extra headers (no implicit Origin), so the token-only CSRF cases control them.
func (e *ssoEnv) doRaw(method, path, body, email string, hdr map[string]string) *httptest.ResponseRecorder {
	e.t.Helper()
	return serveReq(e.h, e.request(method, path, body, email, false, hdr))
}

func settingsBody(t *testing.T, cfg *Config) string {
	t.Helper()
	data, err := json.Marshal(struct {
		Config *Config `json:"config"`
	}{cfg})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestAccessSSOEmployeeTokenCannotManageUsersOrAdminEmail(t *testing.T) {
	e := newSSOEnv(t, true, "boss@example.com", false)
	u, _, err := createUser(e.db, "flo", "password123", []string{"clients"}, "flo@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if rec := e.do("PUT", "/api/users/"+u.ID, `{"permissions":["clients","quotes"]}`, "flo@example.com", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("employee PUT /api/users/{id} = %d, want 403", rec.Code)
	}
	if rec := e.do("POST", "/api/users", `{"username":"gus","password":"password123"}`, "flo@example.com", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("employee POST /api/users = %d, want 403", rec.Code)
	}
	desired := cloneConfig(t, e.store.Get())
	desired.Users.AccessAdminEmail = "flo@example.org"
	if rec := e.do("PUT", "/api/settings", settingsBody(t, desired), "flo@example.com", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("employee PUT /api/settings changing accessAdminEmail = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
	if got := e.store.Get().Users.AccessAdminEmail; got != "boss@example.com" {
		t.Fatalf("accessAdminEmail = %q, want unchanged", got)
	}
}

func TestAccessSSODeletedUserEmailFallsBackTo401(t *testing.T) {
	e := newSSOEnv(t, true, "", false)
	u, _, err := createUser(e.db, "flo", "password123", []string{"clients"}, "flo@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := e.me("flo@example.com", nil); code != http.StatusOK {
		t.Fatalf("before delete = %d, want 200", code)
	}
	if _, err := e.db.Exec(`DELETE FROM users WHERE id = ?`, u.ID); err != nil {
		t.Fatal(err)
	}
	if code, _ := e.me("flo@example.com", nil); code != http.StatusUnauthorized {
		t.Fatalf("after delete = %d, want 401", code)
	}
}

func TestAccessSSOAdminEmailEmployeeCollision(t *testing.T) {
	e := newSSOEnv(t, true, "boss@example.com", false)
	if _, _, err := createUser(e.db, "flo", "password123", nil, "flo@example.com"); err != nil {
		t.Fatal(err)
	}
	admin := e.passwordCookie()

	// Settings: accessAdminEmail equal to an employee's email is rejected.
	desired := cloneConfig(t, e.store.Get())
	desired.Users.AccessAdminEmail = "Flo@Example.com"
	if rec := e.do("PUT", "/api/settings", settingsBody(t, desired), "nobody@example.com", admin); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("settings PUT with employee email as admin email = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
	// Users: a user email equal to the admin email is rejected on create and update.
	if rec := e.do("POST", "/api/users", `{"username":"gus","password":"password123","email":"BOSS@example.com"}`, "nobody@example.com", admin); rec.Code != http.StatusConflict {
		t.Fatalf("create with admin email = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
	u, _, err := createUser(e.db, "hal", "password123", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if rec := e.do("PUT", "/api/users/"+u.ID, `{"email":"boss@example.com"}`, "nobody@example.com", admin); rec.Code != http.StatusConflict {
		t.Fatalf("update to admin email = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
}

func TestAccessSSOTokenOnlyWritesNeedSameOrigin(t *testing.T) {
	e := newSSOEnv(t, false, "", false)
	body := `{"name":"Ada"}`
	cases := []struct {
		name string
		hdr  map[string]string
		want int
	}{
		{"cross-origin", map[string]string{"Origin": "https://evil.example.net"}, http.StatusForbidden},
		{"no origin, no fetch metadata", nil, http.StatusForbidden},
		{"cross-site fetch metadata", map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"same-origin fetch metadata", map[string]string{"Sec-Fetch-Site": "same-origin"}, http.StatusCreated},
		{"same origin header", map[string]string{"Origin": "https://" + ssoHost}, http.StatusCreated},
	}
	for _, c := range cases {
		rec := e.doRaw("POST", "/api/clients", body, "owner@example.com", c.hdr)
		if rec.Code != c.want {
			t.Errorf("%s: POST /api/clients = %d, want %d (%s)", c.name, rec.Code, c.want, rec.Body.String())
		}
	}
	// Safe methods are unaffected.
	if rec := e.doRaw("GET", "/api/clients", "", "owner@example.com", nil); rec.Code != http.StatusOK {
		t.Errorf("GET with no Origin = %d, want 200", rec.Code)
	}
	// A password session is not subject to the token-only rule.
	cookie := e.passwordCookie()
	req := httptest.NewRequest("POST", "/api/clients", strings.NewReader(body))
	req.Host = ssoHost
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cf-Access-Jwt-Assertion", e.f.token(nil))
	req.Header.Set("CF-Connecting-IP", "203.0.113.9")
	req.AddCookie(cookie)
	rec := serveReq(e.h, req)
	if rec.Code != http.StatusCreated {
		t.Errorf("session-authenticated write with no Origin = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
}

func TestAccessSSOSingleUserEmployeeEmailFallsBackTo401(t *testing.T) {
	e := newSSOEnv(t, false, "", false)
	u, _, err := createUser(e.db, "flo", "password123", []string{"clients"}, "flo@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if code, me := e.me("flo@example.com", nil); code != http.StatusUnauthorized {
		t.Fatalf("enabled employee, multi-user off = %d %+v, want 401", code, me)
	}
	disabled := true
	if _, _, err := updateUserFields(e.db, u.ID, nil, &disabled, nil); err != nil {
		t.Fatal(err)
	}
	if code, _ := e.me("Flo@Example.com", nil); code != http.StatusUnauthorized {
		t.Fatalf("disabled employee, multi-user off = %d, want 401", code)
	}
	if code, me := e.me("owner@example.com", nil); code != http.StatusOK || !me.IsAdmin {
		t.Fatalf("non-employee, multi-user off = %d %+v, want admin", code, me)
	}
	// A lookup failure fails closed.
	e.db.Close()
	if code, _ := e.me("owner@example.com", nil); code != http.StatusUnauthorized {
		t.Fatalf("lookup error = %d, want 401", code)
	}
}

func TestAccessSSOEmailFoldingIsASCIIOnly(t *testing.T) {
	// Kelvin sign U+212A lowercases to "k" under Unicode folding.
	const kelvin = "Kelly@example.com"
	if foldEmail(kelvin) == "kelly@example.com" {
		t.Fatal("foldEmail must not fold the Kelvin sign onto ASCII k")
	}
	if got := foldEmail("  KeLLy@Example.COM "); got != "kelly@example.com" {
		t.Fatalf("foldEmail = %q", got)
	}
	for _, multi := range []bool{true, false} {
		e := newSSOEnv(t, multi, "", false)
		if _, _, err := createUser(e.db, "kelly", "password123", []string{"clients"}, "kelly@example.com"); err != nil {
			t.Fatal(err)
		}
		if code, me := e.me(kelvin, nil); code != http.StatusUnauthorized {
			t.Errorf("multi=%v: Kelvin-sign token email = %d %+v, want 401", multi, code, me)
		}
	}

	// Stored emails with non-ASCII, control characters, or whitespace are rejected.
	handler, _, _ := newMultiUserTestMux(t, false)
	admin := adminSessionCookie(t, handler)
	for _, bad := range []string{kelvin, "jörg@example.com", "a\x00b@example.com", "a\tb@example.com", "a b@example.com"} {
		rec := doJSON(t, handler, "POST", "/api/users", admin, map[string]any{"username": "hal", "password": "password123", "email": bad})
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("email %q on create = %d, want 422", bad, rec.Code)
		}
	}
	dir := t.TempDir()
	running, err := LoadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	mux := newSettingsMux(dir, NewConfigStore(running))
	desired := cloneConfig(t, running)
	desired.Users.AccessAdminEmail = kelvin
	if rec := putSettings(t, mux, desired); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("non-ASCII accessAdminEmail = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
}

func TestAccessSSOConfigRedactsAdminEmailForEmployees(t *testing.T) {
	e := newSSOEnv(t, true, "boss@example.com", false)
	if _, _, err := createUser(e.db, "flo", "password123", []string{"clients"}, "flo@example.com"); err != nil {
		t.Fatal(err)
	}
	get := func(email string) string {
		t.Helper()
		rec := e.do("GET", "/api/config", "", email, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/config as %s = %d", email, rec.Code)
		}
		var cfg struct {
			Users struct {
				AccessAdminEmail string `json:"accessAdminEmail"`
			} `json:"users"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &cfg); err != nil {
			t.Fatal(err)
		}
		return cfg.Users.AccessAdminEmail
	}
	if got := get("flo@example.com"); got != "" {
		t.Errorf("employee sees accessAdminEmail %q, want empty", got)
	}
	if got := get("boss@example.com"); got != "boss@example.com" {
		t.Errorf("admin sees accessAdminEmail %q, want the value", got)
	}
	if got := e.store.Get().Users.AccessAdminEmail; got != "boss@example.com" {
		t.Errorf("redaction mutated the shared config: %q", got)
	}
}

// With ADMIN_PASSWORD="" (auth off), an Access-verified write still needs
// same-origin proof: the Access cookie carries it, not a session.
func TestAccessSSOAuthDisabledTokenWritesNeedSameOrigin(t *testing.T) {
	f := newCFFixture(t)
	f.load()
	dir := t.TempDir()
	db := openTestDB(t, dir)
	cfg := loadTestConfig(t, dir)
	auth := newAuthState("", nil)
	auth.cfAccess = f.cf
	mux := routesMux(db, NewConfigStore(cfg), auth, dir, false)
	h := auth.middleware(mux)
	for _, c := range []struct {
		name string
		hdr  map[string]string
		want int
	}{
		{"no origin, no fetch metadata", nil, http.StatusForbidden},
		{"same-origin fetch metadata", map[string]string{"Sec-Fetch-Site": "same-origin"}, http.StatusCreated},
	} {
		req := httptest.NewRequest("POST", "/api/clients", strings.NewReader(`{"name":"Ada"}`))
		req.Host = ssoHost
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Cf-Access-Jwt-Assertion", f.token(nil))
		req.Header.Set("CF-Connecting-IP", "203.0.113.9")
		for k, v := range c.hdr {
			req.Header.Set(k, v)
		}
		rec := serveReq(h, req)
		if rec.Code != c.want {
			t.Errorf("%s: POST /api/clients = %d, want %d (%s)", c.name, rec.Code, c.want, rec.Body.String())
		}
	}
}
