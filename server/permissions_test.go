package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// ---------------------------------------------------------------------------
// adminOnly / requirePermission: direct unit tests, no HTTP mux needed.
// ---------------------------------------------------------------------------

func withCtxIdentity(r *http.Request, id identity) *http.Request {
	return r.WithContext(withIdentity(r.Context(), id))
}

func TestAdminOnlyAllowsAdmin(t *testing.T) {
	called := false
	h := adminOnly(func(w http.ResponseWriter, r *http.Request) { called = true })
	rec, req := newRecorderAndRequest("GET", "/x")
	h(rec, withCtxIdentity(req, identity{username: "admin", isAdmin: true}))
	if !called || rec.Code != http.StatusOK {
		t.Fatalf("admin should reach the handler: called=%v code=%d", called, rec.Code)
	}
}

func TestAdminOnlyDeniesEmployee(t *testing.T) {
	called := false
	h := adminOnly(func(w http.ResponseWriter, r *http.Request) { called = true })
	rec, req := newRecorderAndRequest("GET", "/x")
	h(rec, withCtxIdentity(req, identity{username: "bob", isAdmin: false}))
	if called {
		t.Fatal("employee must not reach an admin-only handler")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestRequirePermissionAllowsAdminRegardlessOfGrants(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)

	called := false
	h := requirePermission(db, "clients", func(w http.ResponseWriter, r *http.Request) { called = true })
	rec, req := newRecorderAndRequest("GET", "/x")
	h(rec, withCtxIdentity(req, identity{username: "admin", isAdmin: true}))
	if !called || rec.Code != http.StatusOK {
		t.Fatalf("admin should bypass the permission check: called=%v code=%d", called, rec.Code)
	}
}

func TestRequirePermissionAllowsGrantedEmployee(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	if _, _, err := createUser(db, "bob", "password123", []string{"clients"}, ""); err != nil {
		t.Fatalf("createUser: %v", err)
	}

	called := false
	h := requirePermission(db, "clients", func(w http.ResponseWriter, r *http.Request) { called = true })
	rec, req := newRecorderAndRequest("GET", "/x")
	h(rec, withCtxIdentity(req, identity{username: "bob", isAdmin: false}))
	if !called || rec.Code != http.StatusOK {
		t.Fatalf("granted employee should reach the handler: called=%v code=%d", called, rec.Code)
	}
}

func TestRequirePermissionDeniesUngrantedEmployee(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	if _, _, err := createUser(db, "bob", "password123", []string{"quotes"}, ""); err != nil {
		t.Fatalf("createUser: %v", err)
	}

	called := false
	h := requirePermission(db, "clients", func(w http.ResponseWriter, r *http.Request) { called = true })
	rec, req := newRecorderAndRequest("GET", "/x")
	h(rec, withCtxIdentity(req, identity{username: "bob", isAdmin: false}))
	if called {
		t.Fatal("an employee without the required key must not reach the handler")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestRequirePermissionPanicsOnUnknownKey(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("requirePermission should panic on a key outside validPermissionKeys -- a registration-time bug, not a runtime condition")
		}
	}()
	requirePermission(nil, "not-a-real-tab", func(w http.ResponseWriter, r *http.Request) {})
}

// requireAnyPermission, the OR counterpart to requirePermission: direct unit tests mirroring the AND wrapper's.

func TestRequireAnyPermissionAllowsAdminRegardlessOfGrants(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)

	called := false
	h := requireAnyPermission(db, []string{"filament", "electricity"}, func(w http.ResponseWriter, r *http.Request) { called = true })
	rec, req := newRecorderAndRequest("GET", "/x")
	h(rec, withCtxIdentity(req, identity{username: "admin", isAdmin: true}))
	if !called || rec.Code != http.StatusOK {
		t.Fatalf("admin should bypass the permission check: called=%v code=%d", called, rec.Code)
	}
}

func TestRequireAnyPermissionAllowsHolderOfAnyListedKey(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	if _, _, err := createUser(db, "bob", "password123", []string{"printers"}, ""); err != nil {
		t.Fatalf("createUser: %v", err)
	}

	called := false
	h := requireAnyPermission(db, []string{"filament", "electricity", "printers"}, func(w http.ResponseWriter, r *http.Request) { called = true })
	rec, req := newRecorderAndRequest("GET", "/x")
	h(rec, withCtxIdentity(req, identity{username: "bob", isAdmin: false}))
	if !called || rec.Code != http.StatusOK {
		t.Fatalf("holder of one of the listed keys should reach the handler: called=%v code=%d", called, rec.Code)
	}
}

func TestRequireAnyPermissionDeniesHolderOfNoneOfTheKeys(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	if _, _, err := createUser(db, "bob", "password123", []string{"items"}, ""); err != nil {
		t.Fatalf("createUser: %v", err)
	}

	called := false
	h := requireAnyPermission(db, []string{"filament", "electricity", "printers"}, func(w http.ResponseWriter, r *http.Request) { called = true })
	rec, req := newRecorderAndRequest("GET", "/x")
	h(rec, withCtxIdentity(req, identity{username: "bob", isAdmin: false}))
	if called {
		t.Fatal("an employee holding none of the listed keys must not reach the handler")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestRequireAnyPermissionPanicsOnUnknownKey(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("requireAnyPermission should panic on a key outside validPermissionKeys -- a registration-time bug, not a runtime condition")
		}
	}()
	requireAnyPermission(nil, []string{"filament", "not-a-real-tab"}, func(w http.ResponseWriter, r *http.Request) {})
}

func newRecorderAndRequest(method, path string) (*httptest.ResponseRecorder, *http.Request) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, nil)
	return rec, req
}

// Full route-table permission matrix: each key grants its family and nothing else (default-deny on unmapped routes), through the real registerRoutes table.

// permissionRoute is one representative route from a permission key's
// family: enough to prove the wrapper on that route resolves the given key,
// without needing to exercise every single registered route.
type permissionRoute struct {
	key    string
	method string
	path   string
	body   any
}

var permissionMatrixRoutes = []permissionRoute{
	{"quote", "POST", "/api/quote/compute", map[string]any{}},
	{"quotes", "GET", "/api/quotes", nil},
	{"requests", "GET", "/api/requests", nil},
	{"invoices", "GET", "/api/invoices", nil},
	{"items", "GET", "/api/items", nil},
	{"clients", "GET", "/api/clients", nil},
	{"dashboard", "GET", "/api/dashboard", nil},
	{"dashboard", "GET", "/api/dashboard/top-items", nil},
}

// One employee per mapped key (holding ONLY it): the matching route must not 403 and every other family's route must 403,
// proving each call site's requirePermission key is the one enforced.
func TestPermissionMatrixGrantsOnlyItsOwnFamily(t *testing.T) {
	handler, auth, _ := newMultiUserTestMux(t, false)

	for _, holder := range permissionMatrixRoutes {
		username := "u_" + holder.key
		if _, _, err := createUser(auth.db, username, "password123", []string{holder.key}, ""); err != nil {
			t.Fatalf("createUser(%s): %v", username, err)
		}
	}

	for _, holder := range permissionMatrixRoutes {
		username := "u_" + holder.key
		cookie := employeeSessionCookie(t, handler, username)
		for _, route := range permissionMatrixRoutes {
			rec := doJSON(t, handler, route.method, route.path, cookie, route.body)
			isOwn := route.key == holder.key
			if isOwn && rec.Code == http.StatusForbidden {
				t.Errorf("holder of %q: %s %s = 403, want granted (not 403); body = %s", holder.key, route.method, route.path, rec.Body.String())
			}
			if !isOwn && rec.Code != http.StatusForbidden {
				t.Errorf("holder of %q (not %q): %s %s = %d, want 403", holder.key, route.key, route.method, route.path, rec.Code)
			}
		}
	}
}

// TestPermissionMatrixDefaultDenyAdminOnlyRoutes pins default-deny: an
// employee holding every single grantable key still cannot reach a route
// that's hard admin-only or that simply isn't in the mapping table at all.
func TestPermissionMatrixDefaultDenyAdminOnlyRoutes(t *testing.T) {
	handler, auth, _ := newMultiUserTestMux(t, false)
	allKeys := make([]string, 0, len(validPermissionKeys))
	for k := range validPermissionKeys {
		allKeys = append(allKeys, k)
	}
	if _, _, err := createUser(auth.db, "superuser", "password123", allKeys, ""); err != nil {
		t.Fatalf("createUser: %v", err)
	}
	cookie := employeeSessionCookie(t, handler, "superuser")

	adminOnlyRoutes := []struct {
		method string
		path   string
	}{
		{"GET", "/api/export"},
		{"GET", "/api/logs/errors"},
		// GET /api/settings is absent: it is the default-deny exception for filament/electricity/printers holders (see TestSettingsGetScopedRead).
		{"GET", "/api/users"},
		{"POST", "/api/integrations/test"},
		{"POST", "/api/smtp/test"},
	}
	for _, route := range adminOnlyRoutes {
		rec := doJSON(t, handler, route.method, route.path, cookie, nil)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s with every grantable key held: status = %d, want 403 (default-deny); body = %s", route.method, route.path, rec.Code, rec.Body.String())
		}
	}
}

// TestPermissionMatrixUnauthenticatedIs401 pins that every one of these
// same routes still requires a session at all -- the permission wrapper
// runs behind, not instead of, the session check.
func TestPermissionMatrixUnauthenticatedIs401(t *testing.T) {
	handler, _, _ := newMultiUserTestMux(t, false)
	for _, route := range permissionMatrixRoutes {
		rec := doJSON(t, handler, route.method, route.path, nil, route.body)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s with no session: status = %d, want 401", route.method, route.path, rec.Code)
		}
	}
}

// GET /api/settings scoped-read carve-out: admin or a filament/electricity/printers holder may read the document (their tab's editor needs it); everyone else 403s.

func TestSettingsGetScopedRead(t *testing.T) {
	handler, auth, _ := newMultiUserTestMux(t, false)

	for _, key := range []string{"filament", "electricity", "printers"} {
		username := "settings_" + key
		if _, _, err := createUser(auth.db, username, "password123", []string{key}, ""); err != nil {
			t.Fatalf("createUser(%s): %v", username, err)
		}
		cookie := employeeSessionCookie(t, handler, username)
		rec := doJSON(t, handler, "GET", "/api/settings", cookie, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("%s holder: GET /api/settings = %d, want 200; body = %s", key, rec.Code, rec.Body.String())
		}
	}

	if _, _, err := createUser(auth.db, "items_only", "password123", []string{"items"}, ""); err != nil {
		t.Fatalf("createUser: %v", err)
	}
	itemsCookie := employeeSessionCookie(t, handler, "items_only")
	if rec := doJSON(t, handler, "GET", "/api/settings", itemsCookie, nil); rec.Code != http.StatusForbidden {
		t.Errorf("items-only holder: GET /api/settings = %d, want 403; body = %s", rec.Code, rec.Body.String())
	}
}

// Proxy routes: Spoolman ("filament") / Moonraker ("quote"), via the demo fallback handlers so no network is touched while requirePermission wrapping is still exercised.

func TestProxyRoutesPermissionGating(t *testing.T) {
	handler, auth, _ := newMultiUserTestMux(t, true) // demoMode: true registers the fake Spoolman/Moonraker handlers
	if _, _, err := createUser(auth.db, "filament_only", "password123", []string{"filament"}, ""); err != nil {
		t.Fatalf("createUser: %v", err)
	}
	if _, _, err := createUser(auth.db, "quote_only", "password123", []string{"quote"}, ""); err != nil {
		t.Fatalf("createUser: %v", err)
	}

	filamentCookie := employeeSessionCookie(t, handler, "filament_only")
	quoteCookie := employeeSessionCookie(t, handler, "quote_only")

	if rec := doJSON(t, handler, "GET", "/api/spoolman/api/v1/spool", filamentCookie, nil); rec.Code == http.StatusForbidden {
		t.Errorf("filament holder should reach the Spoolman proxy: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, handler, "GET", "/api/spoolman/api/v1/spool", quoteCookie, nil); rec.Code != http.StatusForbidden {
		t.Errorf("quote holder should NOT reach the Spoolman proxy: status = %d, want 403", rec.Code)
	}

	if rec := doJSON(t, handler, "GET", "/api/moonraker/server/history/list", quoteCookie, nil); rec.Code == http.StatusForbidden {
		t.Errorf("quote holder should reach the Moonraker proxy: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, handler, "GET", "/api/moonraker/server/history/list", filamentCookie, nil); rec.Code != http.StatusForbidden {
		t.Errorf("filament holder should NOT reach the Moonraker proxy: status = %d, want 403", rec.Code)
	}
}
