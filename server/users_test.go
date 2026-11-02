package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// newMultiUserTestMux builds the full registerRoutes table on a fresh database with users.multiUser on, wrapped in auth.middleware so csrfGuard/demoWriteGuard/the session check run as in production.
// Returns the handler, the authState (for session/permission manipulation), and the db (for createUser).
func newMultiUserTestMux(t *testing.T, demoMode bool) (http.Handler, *authState, *ConfigStore) {
	t.Helper()
	dir := t.TempDir()
	db := openTestDB(t, dir)
	cfg := loadTestConfig(t, dir)
	cfg.Users.MultiUser = true
	store := NewConfigStore(cfg)
	auth := newAuthState("adminpw", nil)
	auth.demoMode = demoMode
	mux := http.NewServeMux()
	// Mirrors main()'s registration of these two routes (outside registerRoutes, see static.go): GET allows admin or a filament/electricity/printers grant (requireAnyPermission); PUT has handleSettingsPut's own admin/non-admin split.
	mux.HandleFunc("GET /api/settings", requireAnyPermission(db, []string{"filament", "electricity", "printers"}, handleSettingsGet(dir, store)))
	mux.HandleFunc("PUT /api/settings", handleSettingsPut(dir, store, auth))
	registerRoutes(mux, db, store, auth, dir, demoMode)
	return auth.middleware(mux), auth, store
}

// adminSessionCookie logs into handler as the env admin and returns the
// resulting session cookie.
func adminSessionCookie(t *testing.T, handler http.Handler) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/login", bytes.NewReader([]byte(`{"password":"adminpw"}`)))
	req.Host = "localhost"
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin login: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatalf("admin login: no cookie set")
	}
	return cookies[0]
}

// employeeSessionCookie logs into handler as an existing employee (created
// via createUser with password "password123") and returns the resulting
// session cookie.
func employeeSessionCookie(t *testing.T, handler http.Handler, username string) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	body, _ := json.Marshal(map[string]string{"username": username, "password": "password123"})
	req := httptest.NewRequest("POST", "/api/login", bytes.NewReader(body))
	req.Host = "localhost"
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("employee login for %q: status = %d, body = %s", username, rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatalf("employee login for %q: no cookie set", username)
	}
	return cookies[0]
}

func doJSON(t *testing.T, handler http.Handler, method, path string, cookie *http.Cookie, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Host = "localhost"
	if method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := serveReq(handler, req)
	return rec
}

// ---------------------------------------------------------------------------
// CRUD + validation
// ---------------------------------------------------------------------------

func TestCreateUserBcryptRoundTrip(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)

	u, taken, err := createUser(db, "alice", "correct-horse", []string{"quotes", "clients"}, "")
	if err != nil || taken {
		t.Fatalf("createUser: taken=%v err=%v", taken, err)
	}
	if u.Username != "alice" || u.Disabled {
		t.Fatalf("unexpected user: %+v", u)
	}

	row, ok, err := getUserByUsername(db, "alice")
	if err != nil || !ok {
		t.Fatalf("getUserByUsername: ok=%v err=%v", ok, err)
	}
	if bcrypt.CompareHashAndPassword([]byte(row.PasswordHash), []byte("correct-horse")) != nil {
		t.Error("stored hash should verify against the original password")
	}
	if bcrypt.CompareHashAndPassword([]byte(row.PasswordHash), []byte("wrong-password")) == nil {
		t.Error("stored hash should NOT verify against a wrong password")
	}
	// Never a plaintext-shaped hash: bcrypt's own output always starts with
	// "$2" -- a cheap sanity check that we didn't accidentally store the
	// plaintext password instead of a hash.
	if len(row.PasswordHash) < 4 || row.PasswordHash[:2] != "$2" {
		t.Errorf("password_hash does not look like a bcrypt hash: %q", row.PasswordHash)
	}
}

func TestGetUserByUsernameCaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)

	if _, _, err := createUser(db, "alice", "password123", nil, ""); err != nil {
		t.Fatalf("createUser: %v", err)
	}
	if _, ok, err := getUserByUsername(db, "ALICE"); err != nil || !ok {
		t.Fatalf("case-insensitive lookup failed: ok=%v err=%v", ok, err)
	}

	_, taken, err := createUser(db, "Alice", "password456", nil, "")
	if err != nil {
		t.Fatalf("createUser (dup, different case): %v", err)
	}
	if !taken {
		t.Error("creating a username differing only in case should report taken=true")
	}
}

func TestValidPermissionKeysRejectsUnknown(t *testing.T) {
	_, problems := normalizePermissions([]string{"quotes", "not-a-tab"})
	if !containsStr(problems, `permissions: "not-a-tab" is not a valid tab key`) {
		t.Errorf("problems = %v, want unknown-key message", problems)
	}
}

func TestNormalizePermissionsDedupesAndSorts(t *testing.T) {
	perms, problems := normalizePermissions([]string{"printers", "clients", "clients", " quotes "})
	if len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	want := []string{"clients", "printers", "quotes"}
	if len(perms) != len(want) {
		t.Fatalf("perms = %v, want %v", perms, want)
	}
	for i := range want {
		if perms[i] != want[i] {
			t.Fatalf("perms = %v, want %v", perms, want)
		}
	}
}

// ---------------------------------------------------------------------------
// Mode gating: the users API answers 409 while multiUser is off.
// ---------------------------------------------------------------------------

func TestUsersAPI409WhenMultiUserOff(t *testing.T) {
	handler, _, store := newMultiUserTestMux(t, false)
	cfg := store.Get()
	cfg.Users.MultiUser = false
	store.Set(cfg)

	admin := adminSessionCookie(t, handler)

	cases := []struct {
		method string
		path   string
		body   any
	}{
		{"GET", "/api/users", nil},
		{"POST", "/api/users", map[string]any{"username": "bob", "password": "password123", "permissions": []string{}}},
		{"PUT", "/api/users/whatever", map[string]any{"disabled": true}},
		{"POST", "/api/users/whatever/password", map[string]any{"new": "password456"}},
		{"DELETE", "/api/users/whatever", nil},
	}
	for _, tc := range cases {
		rec := doJSON(t, handler, tc.method, tc.path, admin, tc.body)
		if rec.Code != http.StatusConflict {
			t.Errorf("%s %s: status = %d, want 409; body = %s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
}

// ---------------------------------------------------------------------------
// Admin-only: an employee (any permissions) can never reach /api/users.
// ---------------------------------------------------------------------------

func TestUsersAPIAdminOnly(t *testing.T) {
	handler, auth, _ := newMultiUserTestMux(t, false)
	if _, _, err := createUser(auth.db, "carol", "password123", []string{
		"quote", "quotes", "requests", "invoices", "items", "clients", "dashboard", "filament", "electricity", "printers",
	}, ""); err != nil {
		t.Fatalf("createUser: %v", err)
	}
	emp := employeeSessionCookie(t, handler, "carol")

	rec := doJSON(t, handler, "GET", "/api/users", emp, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("employee GET /api/users: status = %d, want 403 even with every permission granted (default-deny); body = %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Session kill on disable / admin password reset / delete.
// ---------------------------------------------------------------------------

func TestDisablingUserKillsSessions(t *testing.T) {
	handler, auth, _ := newMultiUserTestMux(t, false)
	u, _, err := createUser(auth.db, "dave", "password123", []string{"quotes"}, "")
	if err != nil {
		t.Fatalf("createUser: %v", err)
	}
	admin := adminSessionCookie(t, handler)
	emp := employeeSessionCookie(t, handler, "dave")

	// Confirm the session works before disabling.
	if rec := doJSON(t, handler, "GET", "/api/me", emp, nil); rec.Code != http.StatusOK {
		t.Fatalf("GET /api/me before disable: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec := doJSON(t, handler, "PUT", "/api/users/"+u.ID, admin, map[string]any{"disabled": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("disable user: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, handler, "GET", "/api/me", emp, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/me after disable: status = %d, want 401 (session should be killed)", rec.Code)
	}
}

func TestAdminPasswordResetKillsSessions(t *testing.T) {
	handler, auth, _ := newMultiUserTestMux(t, false)
	u, _, err := createUser(auth.db, "erin", "password123", []string{"quotes"}, "")
	if err != nil {
		t.Fatalf("createUser: %v", err)
	}
	admin := adminSessionCookie(t, handler)
	emp := employeeSessionCookie(t, handler, "erin")

	rec := doJSON(t, handler, "POST", "/api/users/"+u.ID+"/password", admin, map[string]any{"new": "new-password1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("admin reset: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, handler, "GET", "/api/me", emp, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/me after admin reset: status = %d, want 401 (old session should be killed)", rec.Code)
	}

	// The new password actually works; the old one no longer does.
	loginRec := doJSON(t, handler, "POST", "/api/login", nil, map[string]string{"username": "erin", "password": "new-password1"})
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login with reset password: status = %d, body = %s", loginRec.Code, loginRec.Body.String())
	}
	oldLoginRec := doJSON(t, handler, "POST", "/api/login", nil, map[string]string{"username": "erin", "password": "password123"})
	if oldLoginRec.Code != http.StatusUnauthorized {
		t.Errorf("login with old password: status = %d, want 401", oldLoginRec.Code)
	}
}

func TestDeleteUserKillsSessions(t *testing.T) {
	handler, auth, _ := newMultiUserTestMux(t, false)
	u, _, err := createUser(auth.db, "frank", "password123", []string{"quotes"}, "")
	if err != nil {
		t.Fatalf("createUser: %v", err)
	}
	admin := adminSessionCookie(t, handler)
	emp := employeeSessionCookie(t, handler, "frank")

	rec := doJSON(t, handler, "DELETE", "/api/users/"+u.ID, admin, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete user: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, handler, "GET", "/api/me", emp, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/me after delete: status = %d, want 401", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Reserved username: "admin" always means the env admin.
// ---------------------------------------------------------------------------

func TestCreateUserRejectsReservedAdminUsername(t *testing.T) {
	handler, _, _ := newMultiUserTestMux(t, false)
	admin := adminSessionCookie(t, handler)

	rec := doJSON(t, handler, "POST", "/api/users", admin, map[string]any{
		"username": "admin", "password": "password123", "permissions": []string{},
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", rec.Code, rec.Body.String())
	}
	var body apiErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if !containsStr(body.Errors, "username: reserved") {
		t.Fatalf("errors = %v, want %q", body.Errors, "username: reserved")
	}
}

// An employee literally named "admin" would collide with the env-admin because killSessionsForUsername matches case-insensitively with no isAdmin discrimination, so disabling, resetting, or deleting them would kill the env-admin's session.
// "admin" cannot be created, so this pins the invariant for every creatable username: none of the three actions touches the env-admin's session.
func TestEnvAdminSessionSurvivesEmployeeLifecycleActions(t *testing.T) {
	handler, auth, _ := newMultiUserTestMux(t, false)
	admin := adminSessionCookie(t, handler)

	assertAdminSessionAlive := func(t *testing.T, step string) {
		t.Helper()
		rec := doJSON(t, handler, "GET", "/api/me", admin, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/me as admin after %s: status = %d, want 200 (env-admin session must survive); body = %s", step, rec.Code, rec.Body.String())
		}
		var me meResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil {
			t.Fatalf("decode /api/me: %v", err)
		}
		if !me.IsAdmin || me.Username != "admin" {
			t.Fatalf("after %s: /api/me = %+v, want the env admin", step, me)
		}
	}

	// Disable.
	uDisable, _, err := createUser(auth.db, "hana", "password123", []string{"quotes"}, "")
	if err != nil {
		t.Fatalf("createUser hana: %v", err)
	}
	rec := doJSON(t, handler, "PUT", "/api/users/"+uDisable.ID, admin, map[string]any{"disabled": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("disable hana: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	assertAdminSessionAlive(t, "disabling an employee")

	// Password reset.
	uReset, _, err := createUser(auth.db, "ivan", "password123", []string{"quotes"}, "")
	if err != nil {
		t.Fatalf("createUser ivan: %v", err)
	}
	rec = doJSON(t, handler, "POST", "/api/users/"+uReset.ID+"/password", admin, map[string]any{"new": "new-password1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("reset ivan's password: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	assertAdminSessionAlive(t, "resetting an employee's password")

	// Delete.
	uDelete, _, err := createUser(auth.db, "jill", "password123", []string{"quotes"}, "")
	if err != nil {
		t.Fatalf("createUser jill: %v", err)
	}
	rec = doJSON(t, handler, "DELETE", "/api/users/"+uDelete.ID, admin, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete jill: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	assertAdminSessionAlive(t, "deleting an employee")
}

// ---------------------------------------------------------------------------
// Self-service password change.
// ---------------------------------------------------------------------------

func TestSelfPasswordChangeKillsOtherSessionsButNotCurrent(t *testing.T) {
	handler, auth, _ := newMultiUserTestMux(t, false)
	if _, _, err := createUser(auth.db, "grace", "password123", []string{"quotes"}, ""); err != nil {
		t.Fatalf("createUser: %v", err)
	}
	sessionA := employeeSessionCookie(t, handler, "grace")
	sessionB := employeeSessionCookie(t, handler, "grace")

	rec := doJSON(t, handler, "POST", "/api/me/password", sessionA, map[string]any{"current": "password123", "new": "new-password1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("self password change: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	if rec := doJSON(t, handler, "GET", "/api/me", sessionA, nil); rec.Code != http.StatusOK {
		t.Errorf("session A (the one that changed the password) should survive: status = %d", rec.Code)
	}
	if rec := doJSON(t, handler, "GET", "/api/me", sessionB, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("session B (a different session for the same user) should be killed: status = %d, want 401", rec.Code)
	}
}

func TestSelfPasswordChangeWrongCurrentPasswordRejected(t *testing.T) {
	handler, auth, _ := newMultiUserTestMux(t, false)
	if _, _, err := createUser(auth.db, "heidi", "password123", []string{"quotes"}, ""); err != nil {
		t.Fatalf("createUser: %v", err)
	}
	emp := employeeSessionCookie(t, handler, "heidi")

	rec := doJSON(t, handler, "POST", "/api/me/password", emp, map[string]any{"current": "wrong", "new": "new-password1"})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", rec.Code, rec.Body.String())
	}
}

func TestSelfPasswordChangeAdminGets400(t *testing.T) {
	handler, _, _ := newMultiUserTestMux(t, false)
	admin := adminSessionCookie(t, handler)

	rec := doJSON(t, handler, "POST", "/api/me/password", admin, map[string]any{"current": "adminpw", "new": "new-password1"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (env admin's password is env-managed); body = %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// GET /api/me shape.
// ---------------------------------------------------------------------------

func TestGetMeAdminAndEmployee(t *testing.T) {
	handler, auth, _ := newMultiUserTestMux(t, false)
	if _, _, err := createUser(auth.db, "ivan", "password123", []string{"quotes", "clients"}, ""); err != nil {
		t.Fatalf("createUser: %v", err)
	}
	admin := adminSessionCookie(t, handler)
	emp := employeeSessionCookie(t, handler, "ivan")

	rec := doJSON(t, handler, "GET", "/api/me", admin, nil)
	var adminResp meResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &adminResp); err != nil {
		t.Fatalf("decode admin /api/me: %v", err)
	}
	if !adminResp.IsAdmin || adminResp.Username != "admin" {
		t.Errorf("admin /api/me = %+v, want isAdmin=true username=admin", adminResp)
	}

	rec = doJSON(t, handler, "GET", "/api/me", emp, nil)
	var empResp meResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &empResp); err != nil {
		t.Fatalf("decode employee /api/me: %v", err)
	}
	if empResp.IsAdmin || empResp.Username != "ivan" {
		t.Errorf("employee /api/me = %+v, want isAdmin=false username=ivan", empResp)
	}
	if !containsStr(empResp.Permissions, "quotes") || !containsStr(empResp.Permissions, "clients") {
		t.Errorf("employee /api/me permissions = %v, want to contain quotes and clients", empResp.Permissions)
	}
}

// ---------------------------------------------------------------------------
// Demo mode blocks every /api/users write, same as every other write.
// ---------------------------------------------------------------------------

func TestDemoModeBlocksUserWrites(t *testing.T) {
	handler, _, _ := newMultiUserTestMux(t, true)
	admin := adminSessionCookie(t, handler)

	rec := doJSON(t, handler, "POST", "/api/users", admin, map[string]any{"username": "mallory", "password": "password123", "permissions": []string{}})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (demo read-only); body = %s", rec.Code, rec.Body.String())
	}
	var body authErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error != demoReadOnlyMessage {
		t.Errorf("error = %q, want the demo read-only message", body.Error)
	}
}
