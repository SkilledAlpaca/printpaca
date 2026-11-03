package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// spoolmanRecorder is a stub Spoolman that records every request it receives.
type spoolmanRecorder struct {
	mu   sync.Mutex
	reqs []recordedReq
	srv  *httptest.Server
}

type recordedReq struct {
	Method, Path, Query, ContentType string
	ContentLength                    int64
	Body                             string
}

func newSpoolmanRecorder(t *testing.T) *spoolmanRecorder {
	t.Helper()
	rec := &spoolmanRecorder{}
	rec.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rec.mu.Lock()
		rec.reqs = append(rec.reqs, recordedReq{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Content-Type"), r.ContentLength, string(b)})
		rec.mu.Unlock()
		w.Write([]byte(`{"id":7}`))
	}))
	t.Cleanup(rec.srv.Close)
	return rec
}

func (r *spoolmanRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.reqs)
}

func (r *spoolmanRecorder) last() recordedReq {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reqs[len(r.reqs)-1]
}

func spoolWrite(method, path, body string) *http.Request {
	req := httptest.NewRequest(method, "/api/spoolman"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// Method and path matrix: exactly the spec's shapes pass; everything else is rejected before the upstream.
func TestSpoolmanMethodPathMatrix(t *testing.T) {
	cases := []struct {
		method, path string
		body         string
		want         int
	}{
		{"GET", "/api/v1/spool", "", 200},
		{"HEAD", "/api/v1/spool", "", 200},
		{"GET", "/api/v1/spool/12", "", 200},
		{"GET", "/api/v1/filament", "", 200},
		{"GET", "/api/v1/vendor", "", 200},
		{"POST", "/api/v1/spool", `{"filament_id":3}`, 200},
		{"PATCH", "/api/v1/spool/12", `{"archived":true}`, 200},
		// Wrong method on a known path: 405.
		{"POST", "/api/v1/vendor", `{"name":"x"}`, 405},
		{"POST", "/api/v1/filament", `{"name":"x"}`, 405},
		{"PATCH", "/api/v1/spool", `{"archived":true}`, 405},
		{"POST", "/api/v1/spool/12", `{"filament_id":3}`, 405},
		{"PATCH", "/api/v1/vendor", `{}`, 405},
		{"PATCH", "/api/v1/filament", `{}`, 405},
		{"PUT", "/api/v1/spool/12", `{}`, 405},
		{"DELETE", "/api/v1/spool/12", "", 405},
		{"PUT", "/api/v1/spool", `{}`, 405},
		{"DELETE", "/api/v1/spool", "", 405},
		// Unknown or malformed paths: 403.
		{"GET", "/api/v1/spool/abc", "", 403},
		{"GET", "/api/v1/spool/1a", "", 403},
		{"GET", "/api/v1/spool/-1", "", 403},
		{"GET", "/api/v1/spool/01", "", 403},
		{"GET", "/api/v1/spool/1/", "", 404}, // path.Clean differs: rejected as suspicious
		{"GET", "/api/v1/spool/1/use", "", 403},
		{"GET", "/api/v1/spool/1234567890123", "", 403},
		{"PATCH", "/api/v1/spool/1/use", `{"use_weight":5}`, 403},
		{"PUT", "/api/v1/spool/1/use", `{"use_weight":5}`, 405},
		{"GET", "/api/v1/filament/1", "", 403},
		{"POST", "/api/v1/filament/1", `{}`, 403},
		{"GET", "/api/v1/vendor/1", "", 403},
		{"GET", "/api/v1/setting/", "", 404},
		{"GET", "/api/v1/setting", "", 403},
		{"GET", "/api/v1/export/spools", "", 403},
		{"GET", "/api/v1/backup", "", 403},
		{"POST", "/api/v1/backup", `{}`, 403},
		{"GET", "/api/v1/health", "", 403},
		{"GET", "/", "", 404},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			up := newSpoolmanRecorder(t)
			mux := proxyMux(t, up.srv.URL)
			rec := serveReq(mux, spoolWrite(tc.method, tc.path, tc.body))
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tc.want, rec.Body.String())
			}
			if tc.want != 200 && up.count() != 0 {
				t.Errorf("upstream was reached on a rejected request")
			}
		})
	}
}

func TestSpoolmanWriteRejectsEncodedIDs(t *testing.T) {
	up := newSpoolmanRecorder(t)
	mux := proxyMux(t, up.srv.URL)
	for _, p := range []string{"/api/v1/spool/1%2f..%2fbackup", "/api/v1/spool/%31", "/api/v1/spool/1%00", "/api/v1/spool/../backup"} {
		rec := serveReq(mux, spoolWrite("PATCH", p, `{"archived":true}`))
		if rec.Code == 200 {
			t.Errorf("%s reached the upstream", p)
		}
	}
	if up.count() != 0 {
		t.Errorf("upstream was reached %d times", up.count())
	}
}

func TestSpoolmanWriteRoleChecks(t *testing.T) {
	dir := t.TempDir()
	up := newSpoolmanRecorder(t)
	t.Setenv("SPOOLMAN_URL", up.srv.URL)
	db, _, store := openTestEnv(t, dir)
	mux := routesMux(db, store, newAuthState("", nil), dir, false)
	if _, _, err := createUser(db, "bob", "password123", []string{"filament"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := createUser(db, "eve", "password123", []string{"quote"}, ""); err != nil {
		t.Fatal(err)
	}
	as := func(name string, admin bool, req *http.Request) *httptest.ResponseRecorder {
		return serveReq(mux, withCtxIdentity(req, identity{username: name, isAdmin: admin}))
	}

	// filament grant: read yes, write no (the grant stays read-only).
	if rec := as("bob", false, spoolWrite("GET", "/api/v1/spool", "")); rec.Code != 200 {
		t.Errorf("granted employee read = %d, want 200", rec.Code)
	}
	if rec := as("bob", false, spoolWrite("POST", "/api/v1/spool", `{"filament_id":3}`)); rec.Code != http.StatusForbidden {
		t.Errorf("granted employee POST = %d, want 403", rec.Code)
	}
	if rec := as("bob", false, spoolWrite("PATCH", "/api/v1/spool/3", `{"archived":true}`)); rec.Code != http.StatusForbidden {
		t.Errorf("granted employee PATCH = %d, want 403", rec.Code)
	}
	// no filament grant: nothing.
	if rec := as("eve", false, spoolWrite("GET", "/api/v1/spool", "")); rec.Code != http.StatusForbidden {
		t.Errorf("ungranted employee read = %d, want 403", rec.Code)
	}
	if rec := as("eve", false, spoolWrite("POST", "/api/v1/spool", `{"filament_id":3}`)); rec.Code != http.StatusForbidden {
		t.Errorf("ungranted employee POST = %d, want 403", rec.Code)
	}
	if got := up.count(); got != 1 {
		t.Fatalf("upstream saw %d requests, want only the one permitted read", got)
	}
	// admin: writes pass.
	if rec := as("admin", true, spoolWrite("POST", "/api/v1/spool", `{"filament_id":3}`)); rec.Code != 200 {
		t.Errorf("admin POST = %d, want 200", rec.Code)
	}
	if rec := as("admin", true, spoolWrite("PATCH", "/api/v1/spool/3", `{"archived":true}`)); rec.Code != 200 {
		t.Errorf("admin PATCH = %d, want 200", rec.Code)
	}
}

func TestSpoolmanWriteBodyAllowlist(t *testing.T) {
	cases := []struct {
		name, method, path, body string
		want                     int
	}{
		{"create ok all fields", "POST", "/api/v1/spool", `{"filament_id":3,"initial_weight":1000,"remaining_weight":900.5,"price":24.99,"location":"Shelf 2","comment":"hi"}`, 200},
		{"create requires filament_id", "POST", "/api/v1/spool", `{"price":3}`, 400},
		{"create rejects archived", "POST", "/api/v1/spool", `{"filament_id":3,"archived":true}`, 400},
		{"create rejects used_weight", "POST", "/api/v1/spool", `{"filament_id":3,"used_weight":5}`, 400},
		{"create rejects extra", "POST", "/api/v1/spool", `{"filament_id":3,"extra":{"a":1}}`, 400},
		{"create rejects registered", "POST", "/api/v1/spool", `{"filament_id":3,"registered":"2020-01-01"}`, 400},
		{"filament_id zero", "POST", "/api/v1/spool", `{"filament_id":0}`, 400},
		{"filament_id fraction", "POST", "/api/v1/spool", `{"filament_id":1.5}`, 400},
		{"filament_id string", "POST", "/api/v1/spool", `{"filament_id":"3"}`, 400},
		{"update ok", "PATCH", "/api/v1/spool/5", `{"remaining_weight":120,"price":19,"location":"","archived":false,"comment":"c"}`, 200},
		{"update rejects filament_id", "PATCH", "/api/v1/spool/5", `{"filament_id":9}`, 400},
		{"update rejects initial_weight", "PATCH", "/api/v1/spool/5", `{"initial_weight":9}`, 400},
		{"update rejects used_weight", "PATCH", "/api/v1/spool/5", `{"used_weight":9}`, 400},
		{"update rejects id", "PATCH", "/api/v1/spool/5", `{"id":9}`, 400},
		{"update empty object", "PATCH", "/api/v1/spool/5", `{}`, 400},
		{"update negative weight", "PATCH", "/api/v1/spool/5", `{"remaining_weight":-1}`, 400},
		{"update huge weight", "PATCH", "/api/v1/spool/5", `{"remaining_weight":1e12}`, 400},
		{"update null", "PATCH", "/api/v1/spool/5", `{"location":null}`, 400},
		{"update string weight", "PATCH", "/api/v1/spool/5", `{"remaining_weight":"5"}`, 400},
		{"update archived string", "PATCH", "/api/v1/spool/5", `{"archived":"yes"}`, 400},
		{"update long location", "PATCH", "/api/v1/spool/5", `{"location":"` + strings.Repeat("x", 129) + `"}`, 400},
		{"not an object", "PATCH", "/api/v1/spool/5", `[1]`, 400},
		{"not json", "PATCH", "/api/v1/spool/5", `archived=true`, 400},
		{"empty body", "PATCH", "/api/v1/spool/5", ``, 400},
		{"trailing data", "PATCH", "/api/v1/spool/5", `{"archived":true} {"archived":false}`, 400},
		{"trailing garbage", "PATCH", "/api/v1/spool/5", `{"archived":true}x`, 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			up := newSpoolmanRecorder(t)
			mux := proxyMux(t, up.srv.URL)
			rec := serveReq(mux, spoolWrite(tc.method, tc.path, tc.body))
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tc.want, rec.Body.String())
			}
			if tc.want != 200 && up.count() != 0 {
				t.Errorf("upstream was reached on a rejected body")
			}
		})
	}
}

// The body forwarded upstream is rebuilt from validated fields only: a duplicate key cannot smuggle a value past
// validation, and the query string is dropped.
func TestSpoolmanWriteForwardsOnlyValidatedFields(t *testing.T) {
	up := newSpoolmanRecorder(t)
	mux := proxyMux(t, up.srv.URL)
	rec := serveReq(mux, spoolWrite("PATCH", "/api/v1/spool/5?use_weight=1000", `{"location":"a","location":"b","price":3}`))
	if rec.Code != 200 {
		t.Fatalf("status = %d; %s", rec.Code, rec.Body.String())
	}
	got := up.last()
	if got.Method != "PATCH" || got.Path != "/api/v1/spool/5" || got.Query != "" {
		t.Errorf("forwarded %+v", got)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(got.Body), &sent); err != nil {
		t.Fatalf("upstream body: %v", err)
	}
	if len(sent) != 2 || sent["location"] != "b" || sent["price"] != float64(3) {
		t.Errorf("upstream body = %s", got.Body)
	}
	if got.ContentType != "application/json" || got.ContentLength != int64(len(got.Body)) {
		t.Errorf("content-type %q length %d for body %q", got.ContentType, got.ContentLength, got.Body)
	}
}

func TestSpoolmanWriteSizeCap(t *testing.T) {
	up := newSpoolmanRecorder(t)
	mux := proxyMux(t, up.srv.URL)
	big := `{"comment":"` + strings.Repeat("a", spoolmanWriteMaxBytes) + `"}`
	if rec := serveReq(mux, spoolWrite("PATCH", "/api/v1/spool/5", big)); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversize = %d, want 413", rec.Code)
	}
	// Just under the cap but still over the field cap: rejected by the field rule, not the size rule.
	under := `{"comment":"` + strings.Repeat("a", 2000) + `"}`
	if rec := serveReq(mux, spoolWrite("PATCH", "/api/v1/spool/5", under)); rec.Code != http.StatusBadRequest {
		t.Errorf("long comment = %d, want 400", rec.Code)
	}
	if up.count() != 0 {
		t.Errorf("upstream reached %d times", up.count())
	}
}

func TestSpoolmanWriteRequiresJSONContentType(t *testing.T) {
	up := newSpoolmanRecorder(t)
	mux := proxyMux(t, up.srv.URL)
	for _, ct := range []string{"", "text/plain", "application/x-www-form-urlencoded", "multipart/form-data"} {
		req := httptest.NewRequest("PATCH", "/api/spoolman/api/v1/spool/5", strings.NewReader(`{"archived":true}`))
		if ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		if rec := serveReq(mux, req); rec.Code != http.StatusUnsupportedMediaType {
			t.Errorf("content-type %q = %d, want 415", ct, rec.Code)
		}
	}
	if up.count() != 0 {
		t.Errorf("upstream reached %d times", up.count())
	}
}

// Through the auth middleware (CSRF guard runs ahead of the proxy): a cross-origin write and a text/plain
// "simple request" write are refused before they reach the guard or Spoolman.
func TestSpoolmanWriteCSRF(t *testing.T) {
	dir := t.TempDir()
	up := newSpoolmanRecorder(t)
	t.Setenv("SPOOLMAN_URL", up.srv.URL)
	db, _, store := openTestEnv(t, dir)
	auth := newAuthState("", nil) // auth disabled: csrfGuard still applies
	h := auth.middleware(routesMux(db, store, auth, dir, false))

	cross := spoolWrite("PATCH", "/api/v1/spool/5", `{"archived":true}`)
	cross.Host = "localhost"
	cross.Header.Set("Origin", "https://evil.example")
	if rec := serveReq(h, cross); rec.Code != http.StatusForbidden {
		t.Errorf("cross-origin PATCH = %d, want 403", rec.Code)
	}
	plain := spoolWrite("POST", "/api/v1/spool", `{"filament_id":3}`)
	plain.Host = "localhost"
	plain.Header.Set("Content-Type", "text/plain")
	if rec := serveReq(h, plain); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("text/plain POST = %d, want 415", rec.Code)
	}
	same := spoolWrite("PATCH", "/api/v1/spool/5", `{"archived":true}`)
	same.Host = "localhost"
	same.Header.Set("Origin", "http://localhost")
	if rec := serveReq(h, same); rec.Code != 200 {
		t.Errorf("same-origin PATCH = %d, want 200; %s", rec.Code, rec.Body.String())
	}
	if up.count() != 1 {
		t.Errorf("upstream reached %d times, want 1 (the same-origin write)", up.count())
	}
}

// Demo mode: the middleware blocks writes (demoWriteGuard), and the guard refuses them even without it.
func TestSpoolmanWritesBlockedInDemoMode(t *testing.T) {
	up := newSpoolmanRecorder(t)
	h := guardSpoolman("/api/spoolman", true, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("inner reached in demo mode")
	}))
	_ = up
	rec := serveReq(h, spoolWrite("PATCH", "/api/spoolman/api/v1/spool/5", `{"archived":true}`))
	if rec.Code != http.StatusForbidden {
		t.Errorf("demo PATCH = %d, want 403", rec.Code)
	}

	dir := t.TempDir()
	auth := newAuthState("secret", nil)
	auth.demoMode = true
	mux := demoTestMux(t, dir, auth)
	req := spoolWrite("POST", "/api/v1/spool", `{"filament_id":3}`)
	req.Host = "localhost"
	if rec := serveReq(auth.middleware(mux), req); rec.Code != http.StatusForbidden {
		t.Errorf("demo POST via middleware = %d, want 403", rec.Code)
	}
}

func TestSpoolmanUnitSanitizeKeepsNumbersExact(t *testing.T) {
	out, err := sanitizeSpoolWrite("POST", []byte(`{"filament_id":12,"price":19.99}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"filament_id":12,"price":19.99}` {
		t.Errorf("out = %s", out)
	}
}
