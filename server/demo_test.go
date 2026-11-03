package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// deriveIntegrations/deriveMoonraker force the integration on in demo mode even with the *_URL unset.

func TestDeriveIntegrationsForcesSpoolmanOnInDemoMode(t *testing.T) {
	t.Setenv("SPOOLMAN_URL", "")
	if got := deriveIntegrations(true, &Config{}); !got.Spoolman {
		t.Errorf("deriveIntegrations(true).Spoolman = false, want true")
	}
	if got := deriveIntegrations(false, &Config{}); got.Spoolman {
		t.Errorf("deriveIntegrations(false).Spoolman = true, want false (demoMode off must not force it on)")
	}
}

func TestDeriveMoonrakerForcesOnInDemoMode(t *testing.T) {
	t.Setenv("MOONRAKER_URL", "")
	if got := deriveMoonraker(true, &Config{}); !got {
		t.Errorf("deriveMoonraker(true) = false, want true")
	}
	if got := deriveMoonraker(false, &Config{}); got {
		t.Errorf("deriveMoonraker(false) = true, want false (demoMode off must not force it on)")
	}
}

// TestHandleConfigSurfacesDemoMode pins that DemoMode rides
// the same configExtras mechanism authEnabled/moonraker/fluiddUrl already
// use.
func TestHandleConfigSurfacesDemoMode(t *testing.T) {
	cfg := loadTestConfig(t, t.TempDir())
	store := NewConfigStore(cfg)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	handleConfig(store, configExtras{demoMode: true}, newAuthState("", nil))(rr, req)

	var body struct {
		DemoMode bool `json:"demoMode"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.DemoMode {
		t.Errorf("demoMode = false, want true")
	}
}

// GET /api/demo is a tiny unauthenticated pre-login signal (handleDemo + authExemptPaths).

// GET /api/demo bypasses the session check even with auth enabled and no cookie, and its body reflects authState.demoMode;
// the login screen needs it before any session exists.
func TestHandleDemoReachableWithoutAuthAndReflectsFlag(t *testing.T) {
	for _, demoMode := range []bool{true, false} {
		auth := newAuthState("secret", nil) // auth enabled
		auth.demoMode = demoMode
		mux := demoTestMux(t, t.TempDir(), auth)
		wrapped := auth.middleware(mux)

		req := httptest.NewRequest("GET", "/api/demo", nil)
		req.Host = "localhost"
		// Deliberately no session cookie: this must not 401.
		rec := serveReq(wrapped, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("demoMode=%v: status = %d, want 200 (no session required); body: %s", demoMode, rec.Code, rec.Body.String())
		}
		var body struct {
			DemoMode bool `json:"demoMode"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("demoMode=%v: decode response: %v", demoMode, err)
		}
		if body.DemoMode != demoMode {
			t.Errorf("demoMode=%v: response demoMode = %v, want %v", demoMode, body.DemoMode, demoMode)
		}
	}
}

// With demo mode on, publicUrl mirrors authState.publicURL (the "explore the customer-facing quote page" link on the login screen).
func TestHandleDemoSurfacesPublicURLWhenSetAndDemoOn(t *testing.T) {
	auth := newAuthState("secret", nil)
	auth.demoMode = true
	auth.publicURL = "https://demo-cust.example.com"
	mux := demoTestMux(t, t.TempDir(), auth)
	wrapped := auth.middleware(mux)

	req := httptest.NewRequest("GET", "/api/demo", nil)
	req.Host = "localhost"
	rec := serveReq(wrapped, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		DemoMode  bool   `json:"demoMode"`
		PublicURL string `json:"publicUrl"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.DemoMode || body.PublicURL != "https://demo-cust.example.com" {
		t.Errorf("body = %+v, want demoMode:true and the configured publicUrl", body)
	}
}

// With publicURL unset the response drops the publicUrl key entirely, not an empty string; LoginScreen's presence check relies on it.
func TestHandleDemoOmitsPublicURLWhenUnset(t *testing.T) {
	auth := newAuthState("secret", nil)
	auth.demoMode = true // publicURL left at its zero value ""
	mux := demoTestMux(t, t.TempDir(), auth)
	wrapped := auth.middleware(mux)

	req := httptest.NewRequest("GET", "/api/demo", nil)
	req.Host = "localhost"
	rec := serveReq(wrapped, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "publicUrl") {
		t.Errorf("body = %s, want no publicUrl key at all (omitempty)", rec.Body.String())
	}
}

// deriveDemoPublicURL must not read DEMO_PUBLIC_URL with demoMode off: a malformed value must not fail boot (log.Fatal), and /api/demo must not surface it.
func TestDeriveDemoPublicURLIgnoredWhenDemoModeOff(t *testing.T) {
	t.Setenv("DEMO_PUBLIC_URL", "not a valid url at all")
	if got := deriveDemoPublicURL(false); got != "" {
		t.Errorf("deriveDemoPublicURL(false) = %q, want \"\" (must not even validate/read the env var with demo mode off)", got)
	}
}

// With demoMode on it returns a valid DEMO_PUBLIC_URL, or "" when unset; invalid values log.Fatal and aren't exercised here.
func TestDeriveDemoPublicURLValidatesWhenDemoModeOn(t *testing.T) {
	t.Setenv("DEMO_PUBLIC_URL", "https://demo-cust.example.com")
	if got := deriveDemoPublicURL(true); got != "https://demo-cust.example.com" {
		t.Errorf("deriveDemoPublicURL(true) = %q, want the env value unchanged", got)
	}

	t.Setenv("DEMO_PUBLIC_URL", "")
	if got := deriveDemoPublicURL(true); got != "" {
		t.Errorf("deriveDemoPublicURL(true) with unset env = %q, want \"\"", got)
	}
}

// demoMode also rides GET /api/public/config so the public quote page can show its "nothing is saved" banner.
func TestHandlePublicConfigSurfacesDemoMode(t *testing.T) {
	for _, demoMode := range []bool{true, false} {
		dir := t.TempDir()
		db := openTestDB(t, dir)
		cfg := newPublicTestConfig(t, dir)
		store := NewConfigStore(cfg)
		mux, _ := buildPublicMux(store, db, dir, false, demoMode)

		rec := serveReq(mux, httptest.NewRequest("GET", "/api/public/config", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("demoMode=%v: status = %d, want 200; body: %s", demoMode, rec.Code, rec.Body.String())
		}
		var body struct {
			DemoMode bool `json:"demoMode"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("demoMode=%v: decode response: %v", demoMode, err)
		}
		if body.DemoMode != demoMode {
			t.Errorf("demoMode=%v: response demoMode = %v, want %v", demoMode, body.DemoMode, demoMode)
		}
	}
}

// Admin write guard (demoWriteGuard): an unsafe request in demoMode gets 403 with the read-only body; /api/login and GETs still work.

// demoTestMux builds the full admin route table (mirroring newTestMux) with
// demoMode threaded into both registerRoutes and auth, so these tests
// exercise the same wiring main.go uses in production.
func demoTestMux(t *testing.T, dir string, auth *authState) *http.ServeMux {
	t.Helper()
	db := openTestDB(t, dir)
	cfg := loadTestConfig(t, dir)
	mux := routesMux(db, NewConfigStore(cfg), auth, dir, true)
	return mux
}

// demoWriteGuard runs before csrfGuard: a POST that csrfGuard would 415 gets the friendly read-only 403 instead.
func TestDemoWriteGuardBlocksWriteBeforeCSRFCheck(t *testing.T) {
	auth := newAuthState("secret", nil)
	auth.demoMode = true
	mux := demoTestMux(t, t.TempDir(), auth)
	wrapped := auth.middleware(mux)

	req := httptest.NewRequest("POST", "/api/clients", strings.NewReader(`{}`))
	req.Host = "localhost"
	req.Header.Set("Content-Type", "text/plain") // would 415 from csrfGuard if reached
	rec := serveReq(wrapped, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body: %s", rec.Code, rec.Body.String())
	}
	var body authErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Error != demoReadOnlyMessage {
		t.Errorf("error = %q, want %q", body.Error, demoReadOnlyMessage)
	}
}

// POST /api/integrations/test makes an outbound request, so demoWriteGuard must reject it with no bespoke exemption:
// a public demo must never probe the network on an operator's behalf.
func TestIntegrationsTestBlockedInDemoMode(t *testing.T) {
	auth := newAuthState("secret", nil)
	auth.demoMode = true
	mux := demoTestMux(t, t.TempDir(), auth)
	wrapped := auth.middleware(mux)

	req := httptest.NewRequest("POST", "/api/integrations/test",
		strings.NewReader(`{"kind":"spoolman","url":"http://spoolman.local"}`))
	req.Host = "localhost"
	req.Header.Set("Content-Type", "application/json")
	rec := serveReq(wrapped, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body: %s", rec.Code, rec.Body.String())
	}
	var body authErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Error != demoReadOnlyMessage {
		t.Errorf("error = %q, want %q", body.Error, demoReadOnlyMessage)
	}
}

// TestDemoWriteGuardExemptsLoginAndLogoutButNotOtherWrites confirms
// /api/login and /api/logout stay writable in demo mode (a visitor still
// needs to log in/out) while every other unsafe /api/* request is rejected.
func TestDemoWriteGuardExemptsLoginAndLogoutButNotOtherWrites(t *testing.T) {
	auth := newAuthState("secret", nil)
	auth.demoMode = true
	mux := demoTestMux(t, t.TempDir(), auth)
	wrapped := auth.middleware(mux)

	loginReq := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"password":"secret"}`))
	loginReq.Host = "localhost"
	loginReq.Header.Set("Content-Type", "application/json")
	rec := serveReq(wrapped, loginReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var loginResp struct {
		Ok bool `json:"ok"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &loginResp); err != nil || !loginResp.Ok {
		t.Fatalf("login body = %s, want ok:true", rec.Body.String())
	}

	// Logout must not be blocked by the demo guard: with no session it falls to the session check (401 is fine) but never demoWriteGuard's 403.
	logoutReq := httptest.NewRequest("POST", "/api/logout", nil)
	logoutReq.Host = "localhost"
	rec = serveReq(wrapped, logoutReq)
	if rec.Code == http.StatusForbidden {
		t.Fatalf("logout was rejected with 403 (read-only demo); it must stay writable, body: %s", rec.Body.String())
	}

	// A non-exempt write still gets the read-only 403.
	writeReq := httptest.NewRequest("POST", "/api/clients", strings.NewReader(`{}`))
	writeReq.Host = "localhost"
	writeReq.Header.Set("Content-Type", "application/json")
	rec = serveReq(wrapped, writeReq)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("write status = %d, want 403; body: %s", rec.Code, rec.Body.String())
	}
}

// POST /api/quote/compute is a POST only for its body; it reads no store and writes nothing, and is the editor's only pricing path,
// so blocking it left DEMO_MODE=1 unable to price anything.
func TestDemoWriteGuardExemptsQuoteCompute(t *testing.T) {
	auth := newAuthState("", nil) // auth opted out, so a 200 can only come from the handler
	auth.demoMode = true
	mux := demoTestMux(t, t.TempDir(), auth)
	wrapped := auth.middleware(mux)

	body := `{"inputs":{"parts":[{"weightG":100,"timeHours":2,"quantity":1,"pricePerKg":20}]},"options":{}}`
	req := httptest.NewRequest("POST", "/api/quote/compute", strings.NewReader(body))
	req.Host = "localhost"
	req.Header.Set("Content-Type", "application/json")
	rec := serveReq(wrapped, req)

	if rec.Code == http.StatusForbidden {
		t.Fatalf("quote compute was rejected with 403 (read-only demo); pricing is a read, body: %s", rec.Body.String())
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
}

// Reads pass through regardless of demoMode; auth is disabled so a 200 can only mean the guard let the request through.
func TestDemoWriteGuardLeavesGETsUntouched(t *testing.T) {
	auth := newAuthState("", nil) // auth disabled
	auth.demoMode = true
	mux := demoTestMux(t, t.TempDir(), auth)
	wrapped := auth.middleware(mux)

	req := httptest.NewRequest("GET", "/api/clients", nil)
	req.Host = "localhost"
	rec := serveReq(wrapped, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
}

// With demoMode off (the zero value) the guard adds nothing observable to a normal deployment.
func TestDemoWriteGuardIsNoopWhenDemoModeOff(t *testing.T) {
	auth := newAuthState("", nil) // demoMode zero value: false
	dir := t.TempDir()
	db := openTestDB(t, dir)
	cfg := loadTestConfig(t, dir)
	mux := routesMux(db, NewConfigStore(cfg), auth, dir, false)
	wrapped := auth.middleware(mux)

	req := httptest.NewRequest("POST", "/api/clients", strings.NewReader(`{"name":"Ada"}`))
	req.Host = "localhost"
	req.Header.Set("Content-Type", "application/json")
	rec := serveReq(wrapped, req)
	if rec.Code == http.StatusForbidden {
		t.Fatalf("write was rejected 403 with demoMode off; the guard must be a no-op, body: %s", rec.Body.String())
	}
}

// The public listener simulates the quote submit in demo mode: 201, no quote_requests row, no gcode file.

func TestPublicQuoteRequestDemoModeSimulatesNoInsert(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	cfg := newPublicTestConfig(t, dir)
	want := expectedPublicPriceCents(t, cfg, "PLA")
	store := NewConfigStore(cfg)
	mux, _ := buildPublicMux(store, db, dir, false, true)

	content := []byte("G1 X10 Y10\nG1 X20 Y20\n; a fake gcode file\n")
	rec := postPublicQuoteRequest(t, mux, publicQuoteRequestFields("PLA"), "model.gcode", content)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body = %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		ID         string `json:"id"`
		PriceCents int    `json:"priceCents"`
		Message    string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.ID == "" || resp.PriceCents != want || resp.Message != "Estimate, subject to review" {
		t.Fatalf("response = %+v, want a synthesized id, the server-computed priceCents %d, and the normal message", resp, want)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM quote_requests`).Scan(&count); err != nil {
		t.Fatalf("count quote_requests rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("quote_requests row count = %d, want 0 (demo mode must persist nothing)", count)
	}

	entries, err := os.ReadDir(filepath.Join(dir, "uploads"))
	if err == nil && len(entries) != 0 {
		t.Fatalf("uploads dir has %d entries, want 0 (demo mode must not write the gcode file)", len(entries))
	}
}

// TestPublicQuoteRequestDemoModeStillValidates confirms demoMode runs the
// same validation a real submission gets: an invalid field still 422s, it's
// only a passing submission that gets simulated rather than stored.
func TestPublicQuoteRequestDemoModeStillValidates(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	cfg := newPublicTestConfig(t, dir)
	store := NewConfigStore(cfg)
	mux, _ := buildPublicMux(store, db, dir, false, true)

	fields := publicQuoteRequestFields("PLA")
	fields["email"] = "not-an-email"
	rec := postPublicQuoteRequest(t, mux, fields, "", nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body: %s", rec.Code, rec.Body.String())
	}
}

// Fake Spoolman/Moonraker handlers return the expected shapes under demoMode.

func TestDemoFakeSpoolmanHandlerShape(t *testing.T) {
	t.Setenv("SPOOLMAN_URL", "")
	auth := newAuthState("", nil)
	mux := demoTestMux(t, t.TempDir(), auth)

	rec := serveReq(mux, httptest.NewRequest("GET", "/api/spoolman/api/v1/spool", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	var spools []struct {
		ID              int     `json:"id"`
		RemainingWeight float64 `json:"remaining_weight"`
		Filament        struct {
			Name     string  `json:"name"`
			Material string  `json:"material"`
			Density  float64 `json:"density"`
		} `json:"filament"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &spools); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(spools) < 3 || len(spools) > 5 {
		t.Fatalf("got %d spools, want 3-5 (spec: 3-5 spools)", len(spools))
	}
	materials := map[string]bool{}
	for _, s := range spools {
		if s.ID == 0 {
			t.Errorf("spool missing id: %+v", s)
		}
		if s.Filament.Material == "" || s.Filament.Density <= 0 {
			t.Errorf("spool missing filament shape: %+v", s)
		}
		materials[s.Filament.Material] = true
	}
	if len(materials) < 2 {
		t.Errorf("materials = %v, want varied materials (spec: PLA/PETG/ABS mix)", materials)
	}
}

func TestDemoFakeMoonrakerHandlersShape(t *testing.T) {
	t.Setenv("MOONRAKER_URL", "")
	auth := newAuthState("", nil)
	mux := demoTestMux(t, t.TempDir(), auth)

	rec := serveReq(mux, httptest.NewRequest("GET", "/api/moonraker/server/history/list", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("history status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var historyResp struct {
		Result struct {
			Count int `json:"count"`
			Jobs  []struct {
				JobID    string `json:"job_id"`
				Filename string `json:"filename"`
				Status   string `json:"status"`
			} `json:"jobs"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &historyResp); err != nil {
		t.Fatalf("unmarshal history: %v", err)
	}
	if historyResp.Result.Count == 0 || len(historyResp.Result.Jobs) != historyResp.Result.Count {
		t.Fatalf("history = %+v, want count matching len(jobs) and > 0", historyResp)
	}
	for _, j := range historyResp.Result.Jobs {
		if j.JobID == "" || j.Filename == "" {
			t.Errorf("job missing job_id/filename: %+v", j)
		}
		if j.Status != "completed" {
			t.Errorf("job %q status = %q, want completed", j.JobID, j.Status)
		}
	}

	rec = serveReq(mux, httptest.NewRequest("GET", "/api/moonraker/server/files/metadata?filename=benchy_0.2mm_PLA.gcode", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("metadata status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var metaResp struct {
		Result struct {
			Filename            string  `json:"filename"`
			FilamentWeightTotal float64 `json:"filament_weight_total"`
			EstimatedTime       float64 `json:"estimated_time"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &metaResp); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if metaResp.Result.Filename != "benchy_0.2mm_PLA.gcode" {
		t.Errorf("metadata filename = %q, want the echoed requested filename", metaResp.Result.Filename)
	}
	if metaResp.Result.FilamentWeightTotal <= 0 || metaResp.Result.EstimatedTime <= 0 {
		t.Errorf("metadata = %+v, want positive filament_weight_total/estimated_time", metaResp.Result)
	}

	// A malformed filename still 400s, same as guardMoonrakerMetadataFilename
	// enforces for the real proxy.
	rec = serveReq(mux, httptest.NewRequest("GET", "/api/moonraker/server/files/metadata?filename=../etc/passwd", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad filename status = %d, want 400", rec.Code)
	}
}

// TestDemoModePrefersRealSpoolmanWhenURLSet confirms a real, configured
// SPOOLMAN_URL always wins over the fake handler, even with demoMode on.
func TestDemoModePrefersRealSpoolmanWhenURLSet(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"id":999,"filament":{"density":1.24}}]`))
	}))
	defer backend.Close()
	t.Setenv("SPOOLMAN_URL", backend.URL)

	auth := newAuthState("", nil)
	mux := demoTestMux(t, t.TempDir(), auth)

	rec := serveReq(mux, httptest.NewRequest("GET", "/api/spoolman/api/v1/spool", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"id":999`) {
		t.Fatalf("body = %s, want the real backend's response, not the fake demo data", rec.Body.String())
	}
}

func TestResolveAdminPasswordAllowsShortPasswordInDemoMode(t *testing.T) {
	t.Setenv("DEMO_MODE", "1")
	t.Setenv("ADMIN_PASSWORD", "demo")
	pw, err := resolveAdminPassword(t.TempDir())
	if err != nil {
		t.Fatalf("demo mode should accept the short well-known demo password, got: %v", err)
	}
	if pw != "demo" {
		t.Fatalf("password = %q, want %q", pw, "demo")
	}
}

func TestResolveAdminPasswordRejectsShortPasswordWithoutDemoMode(t *testing.T) {
	t.Setenv("DEMO_MODE", "")
	t.Setenv("ADMIN_PASSWORD", "demo")
	if _, err := resolveAdminPassword(t.TempDir()); err == nil {
		t.Fatal("a 4-character ADMIN_PASSWORD must be rejected when DEMO_MODE is off")
	}
}
