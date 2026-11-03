package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// /healthz
// ---------------------------------------------------------------------------

func TestHandleHealthzOK(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)

	rec := httptest.NewRecorder()
	handleHealthz(db, dir)(rec, httptest.NewRequest("GET", "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	// The body is a bare "ok" with no version, so an anonymous caller can't fingerprint the build.
	if got, want := rec.Body.String(), "ok"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

// Close the *sql.DB under the handler to force probeDBReadable to fail; permission bits don't bind root in CI.
func TestHandleHealthzDBFailure(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	rec := httptest.NewRecorder()
	handleHealthz(db, dir)(rec, httptest.NewRequest("GET", "/healthz", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body: %s", rec.Code, rec.Body.String())
	}
}

// A regular file where a directory is expected makes os.CreateTemp fail with ENOTDIR regardless of the user (no permission bits).
func TestHandleHealthzConfigDirNotWritable(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)

	blocked := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(blocked, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed blocker file: %v", err)
	}

	rec := httptest.NewRecorder()
	handleHealthz(db, blocked)(rec, httptest.NewRequest("GET", "/healthz", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body: %s", rec.Code, rec.Body.String())
	}
}

// SELECT 1 never touches a b-tree page, so it would report ok on an unreadable database. Dropping app_state under a
// still-open *sql.DB makes probeDBReadable's "SELECT COUNT(*) FROM app_state" fail without real page corruption.
func TestHandleHealthzCorruptTableDetected(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	if _, err := db.Exec("DROP TABLE app_state"); err != nil {
		t.Fatalf("drop app_state: %v", err)
	}

	rec := httptest.NewRecorder()
	handleHealthz(db, dir)(rec, httptest.NewRequest("GET", "/healthz", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (probeDBReadable must actually read app_state, not just SELECT 1); body: %s", rec.Code, rec.Body.String())
	}
}

// /healthz has no session check, so a failed probe's body must never leak the sqlite driver's error text or an absolute CONFIG_DIR path.
// This pins the response body only; the real reason goes to the log.
func TestHandleHealthzFailureBodyIsRedacted(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	rec := httptest.NewRecorder()
	handleHealthz(db, dir)(rec, httptest.NewRequest("GET", "/healthz", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body: %s", rec.Code, rec.Body.String())
	}
	if got, want := rec.Body.String(), "unhealthy\n"; got != want {
		t.Errorf("body = %q, want %q (the real error must reach the log only, never the response body)", got, want)
	}
}

// Within one TTL window get must call probe exactly once and replay its result; once the injected fake clock crosses the TTL the next call probes again.
func TestHealthzCacheReplaysResultWithinTTL(t *testing.T) {
	now := time.Now()
	cache := &healthzCache{now: func() time.Time { return now }, ttl: 5 * time.Second}

	calls := 0
	probe := func() healthzResult {
		calls++
		return healthzResult{ok: true}
	}

	cache.get(probe)
	cache.get(probe)
	cache.get(probe)
	if calls != 1 {
		t.Fatalf("probe called %d times within the TTL window, want 1 (subsequent calls must replay the cached result)", calls)
	}

	now = now.Add(6 * time.Second) // past the 5s ttl
	cache.get(probe)
	if calls != 2 {
		t.Fatalf("probe called %d times after the TTL elapsed, want 2 (the cache should have expired and re-probed)", calls)
	}
}

// Through newHealthzHandler: a DB failure after a successful request must not flip to 503 until the TTL elapses, and must flip once it does.
func TestHandleHealthzUsesCachedResultAcrossRequests(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)

	now := time.Now()
	cache := &healthzCache{now: func() time.Time { return now }, ttl: 5 * time.Second}
	handler := newHealthzHandler(db, dir, cache)

	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	// Break the DB after the first (real) probe already cached success.
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	rec = httptest.NewRecorder()
	handler(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("second request (within TTL) status = %d, want 200 (cached result should be replayed, not re-probed)", rec.Code)
	}

	now = now.Add(6 * time.Second) // past the 5s ttl
	rec = httptest.NewRecorder()
	handler(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("third request (after TTL) status = %d, want 503 (cache should have expired and re-probed the now-broken db)", rec.Code)
	}
}

// A probe that only created and removed a file would report a full filesystem (ENOSPC) as healthy, so probeConfigDirWritable writes bytes.
// writeAndSyncProbe is the write+fsync step; test it directly against a file that can still be read back.
func TestWriteAndSyncProbeWritesBytes(t *testing.T) {
	dir := t.TempDir()
	f, err := os.CreateTemp(dir, "probe-*")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer f.Close()

	if err := writeAndSyncProbe(f); err != nil {
		t.Fatalf("writeAndSyncProbe: %v", err)
	}

	got, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(probeWritePayload) {
		t.Errorf("file content = %q, want %q (probeWritePayload)", got, probeWritePayload)
	}
	if len(got) == 0 {
		t.Error("probe file has 0 bytes: a real write must have happened, not just an empty CreateTemp")
	}
}

// newPublicServer must return a real, externally shutdownable *http.Server so main can Shutdown it before db.Close().
// This drives a real listener and HTTP round trip, started the way main starts it.
func TestNewPublicServerGracefulShutdown(t *testing.T) {
	dir := t.TempDir()
	cfg := newPublicTestConfig(t, dir)
	db := openTestDB(t, dir)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()

	srv := newPublicServer("0", NewConfigStore(cfg), db, dir, newPublicShared(), false, false)
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	client := &http.Client{Timeout: 3 * time.Second}
	if err := waitForHTTPOK(client, "http://"+addr+"/api/public/config"); err != nil {
		t.Fatalf("public listener never answered: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	select {
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("Serve returned %v, want http.ErrServerClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve never returned after Shutdown")
	}

	// The listener is closed: a fresh connection attempt must fail.
	if _, err := client.Get("http://" + addr + "/api/public/config"); err == nil {
		t.Error("request after Shutdown unexpectedly succeeded")
	}
}

// waitForHTTPOK polls url until it answers 200 or the timeout elapses.
func waitForHTTPOK(client *http.Client, url string) error {
	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			lastErr = fmt.Errorf("status %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(10 * time.Millisecond)
	}
	return lastErr
}

// Pins the /api/config contract: every EffectiveConfig top-level key present, and sampled nested camelCase fields under exactly those names.
func TestHandleConfigGoldenBody(t *testing.T) {
	cfg, err := LoadConfig(t.TempDir()) // defaults only
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	store := NewConfigStore(cfg)
	t.Setenv("SPOOLMAN_URL", "")
	t.Setenv("MOONRAKER_URL", "http://moonraker.local:7125")
	t.Setenv("FLUIDD_URL", "http://fluidd.local:9000")

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	handleConfig(store, configExtras{authEnabled: false}, newAuthState("", nil))(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if cc := rr.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}

	wantTopLevel := []string{
		"currency", "pricing", "feeSchedules", "materials", "printers",
		"energy", "presets", "defaults", "quote", "ui", "integrations", "version",
		"authEnabled", "moonraker", "fluiddUrl",
	}
	for _, key := range wantTopLevel {
		if _, ok := body[key]; !ok {
			t.Errorf("response missing top-level key %q", key)
		}
	}

	// Spot-check the nested camelCase fields the pre-flight scan flagged.
	var decoded struct {
		Pricing struct {
			DefaultShippingCost float64 `json:"defaultShippingCost"`
			LaborPerUnit        bool    `json:"laborPerUnit"`
		} `json:"pricing"`
		Materials []struct {
			PricePerKg float64 `json:"pricePerKg"`
		} `json:"materials"`
		Printers []struct {
			FilamentDiameterMm float64 `json:"filamentDiameterMm"`
		} `json:"printers"`
		Energy struct {
			Tou struct {
				OffPeakRate float64 `json:"offPeakRate"`
			} `json:"tou"`
		} `json:"energy"`
		Integrations struct {
			Spoolman bool `json:"spoolman"`
		} `json:"integrations"`
		Version     string `json:"version"`
		AuthEnabled bool   `json:"authEnabled"`
		Moonraker   bool   `json:"moonraker"`
		FluiddURL   string `json:"fluiddUrl"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode into typed struct: %v", err)
	}
	if decoded.Pricing.DefaultShippingCost != 0 {
		t.Errorf("pricing.defaultShippingCost = %v, want 0", decoded.Pricing.DefaultShippingCost)
	}
	if decoded.Pricing.LaborPerUnit != false {
		t.Errorf("pricing.laborPerUnit = %v, want false", decoded.Pricing.LaborPerUnit)
	}
	if len(decoded.Materials) == 0 || decoded.Materials[0].PricePerKg != 20 {
		t.Errorf("materials[0].pricePerKg = %+v, want 20", decoded.Materials)
	}
	if len(decoded.Printers) == 0 || decoded.Printers[0].FilamentDiameterMm != 1.75 {
		t.Errorf("printers[0].filamentDiameterMm = %+v, want 1.75", decoded.Printers)
	}
	if decoded.Energy.Tou.OffPeakRate != 0.10 {
		t.Errorf("energy.tou.offPeakRate = %v, want 0.10", decoded.Energy.Tou.OffPeakRate)
	}
	if decoded.Integrations.Spoolman {
		t.Errorf("integrations = %+v, want false", decoded.Integrations)
	}
	if decoded.AuthEnabled {
		t.Errorf("authEnabled = %v, want false (no ADMIN_PASSWORD passed to handleConfig)", decoded.AuthEnabled)
	}
	if !decoded.Moonraker {
		t.Errorf("moonraker = %v, want true (value passed to handleConfig)", decoded.Moonraker)
	}
	if decoded.FluiddURL != "http://fluidd.local:9000" {
		t.Errorf("fluiddUrl = %q, want %q", decoded.FluiddURL, "http://fluidd.local:9000")
	}
	// go test doesn't apply the Dockerfile's -ldflags -X main.version=...
	// override, so the package-level default ("dev") is what a test binary
	// always sees; that's the value this pins.
	if decoded.Version != "dev" {
		t.Errorf("version = %q, want %q (package default)", decoded.Version, "dev")
	}
}

// TestHandleConfigIntegrationsReflectEnv pins integrations derivation from
// SPOOLMAN_URL.
func TestHandleConfigIntegrationsReflectEnv(t *testing.T) {
	cases := []struct {
		name         string
		spoolmanURL  string
		wantSpoolman bool
	}{
		{"none set", "", false},
		{"spoolman set", "http://spoolman.local", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SPOOLMAN_URL", tc.spoolmanURL)

			got := deriveIntegrations(false, &Config{})
			if got.Spoolman != tc.wantSpoolman {
				t.Fatalf("deriveIntegrations() = %+v, want {Spoolman:%v}",
					got, tc.wantSpoolman)
			}

			// And confirm it round-trips through the handler's JSON body.
			cfg := loadTestConfig(t, t.TempDir())
			store := NewConfigStore(cfg)
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
			handleConfig(store, configExtras{}, newAuthState("", nil))(rr, req)

			var body struct {
				Integrations Integrations `json:"integrations"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body.Integrations != got {
				t.Errorf("response integrations = %+v, want %+v", body.Integrations, got)
			}
		})
	}
}

// TestHandleConfigMoonrakerFluiddReflectEnv pins moonraker/fluiddUrl
// derivation from MOONRAKER_URL/FLUIDD_URL, mirroring
// TestHandleConfigIntegrationsReflectEnv above.
func TestHandleConfigMoonrakerFluiddReflectEnv(t *testing.T) {
	cases := []struct {
		name          string
		moonrakerURL  string
		fluiddURL     string
		wantMoonraker bool
		wantFluidd    string
	}{
		{"none set", "", "", false, ""},
		{"both set", "http://moonraker.local", "http://fluidd.local", true, "http://fluidd.local"},
		{"moonraker only", "http://moonraker.local", "", true, ""},
		{"fluidd only", "", "http://fluidd.local", false, "http://fluidd.local"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MOONRAKER_URL", tc.moonrakerURL)
			t.Setenv("FLUIDD_URL", tc.fluiddURL)

			gotMoonraker := deriveMoonraker(false, &Config{})
			if gotMoonraker != tc.wantMoonraker {
				t.Fatalf("deriveMoonraker() = %v, want %v", gotMoonraker, tc.wantMoonraker)
			}
			gotFluidd := deriveFluiddURL(&Config{})
			if gotFluidd != tc.wantFluidd {
				t.Fatalf("deriveFluiddURL() = %q, want %q", gotFluidd, tc.wantFluidd)
			}

			// And confirm both round-trip through the handler's JSON body.
			cfg := loadTestConfig(t, t.TempDir())
			store := NewConfigStore(cfg)
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
			handleConfig(store, configExtras{}, newAuthState("", nil))(rr, req)

			var body struct {
				Moonraker bool   `json:"moonraker"`
				FluiddURL string `json:"fluiddUrl"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body.Moonraker != tc.wantMoonraker {
				t.Errorf("response moonraker = %v, want %v", body.Moonraker, tc.wantMoonraker)
			}
			if body.FluiddURL != tc.wantFluidd {
				t.Errorf("response fluiddUrl = %q, want %q", body.FluiddURL, tc.wantFluidd)
			}
		})
	}
}

// Pins deriveIntegrationSources' env/config/unset labeling through /api/config for each integration; IntegrationsUrls is what makes "config" reachable.
func TestHandleConfigIntegrationSourcesReflectEnvAndConfig(t *testing.T) {
	t.Setenv("SPOOLMAN_URL", "http://spoolman-env.local")
	t.Setenv("MOONRAKER_URL", "")
	t.Setenv("FLUIDD_URL", "")

	cfg := &Config{IntegrationsUrls: IntegrationsUrls{
		SpoolmanURL:  "http://spoolman-config.local", // must lose to the env var above
		MoonrakerURL: "http://moonraker-config.local",
		// FluiddURL left empty: exercises "unset".
	}}

	store := NewConfigStore(cfg)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	handleConfig(store, configExtras{}, newAuthState("", nil))(rr, req)

	var body struct {
		IntegrationSources IntegrationSources `json:"integrationSources"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	want := IntegrationSources{Spoolman: "env", Moonraker: "config", Fluidd: "unset"}
	if body.IntegrationSources != want {
		t.Errorf("integrationSources = %+v, want %+v", body.IntegrationSources, want)
	}
}

// ---------------------------------------------------------------------------
// demoModeStartupGate
// ---------------------------------------------------------------------------

// A database already holding a real (non-seeded) client row must make demoModeStartupGate refuse, or DEMO_MODE=1 would expose that data behind the demo's published password.
func TestDemoModeStartupGateRefusesRealData(t *testing.T) {
	dir := t.TempDir()
	db, _, store := openTestEnv(t, dir)

	now := "2020-01-01T00:00:00Z"
	if _, err := db.Exec(`
		INSERT INTO clients (id, name, email, phone, billing_address, shipping_address, notes, created_at, updated_at)
		VALUES (?, 'Real Client', '', '', '', '', '', ?, ?)`,
		newID(), now, now,
	); err != nil {
		t.Fatalf("insert real client: %v", err)
	}

	err := demoModeStartupGate(db, store, dir)
	if err == nil {
		t.Fatal("demoModeStartupGate: want a refusal error against a non-empty, unmarked database, got nil")
	}
	if !strings.Contains(err.Error(), "DEMO_MODE=1 refused to start") {
		t.Fatalf("demoModeStartupGate error = %q, want it to explain the DEMO_MODE=1 refusal", err.Error())
	}

	// The refusal must not itself have mutated anything: still no marker,
	// still exactly the one real client.
	if marked, err := demoSeedMarkerPresent(db); err != nil {
		t.Fatalf("demoSeedMarkerPresent: %v", err)
	} else if marked {
		t.Fatal("demoModeStartupGate must not write the seed marker on refusal")
	}
	var nClients int
	if err := db.QueryRow(`SELECT COUNT(*) FROM clients`).Scan(&nClients); err != nil {
		t.Fatalf("count clients: %v", err)
	}
	if nClients != 1 {
		t.Fatalf("clients = %d, want 1 (unchanged)", nClients)
	}
}

// A fresh database seeds and records the marker; a second startup against it is accepted (not re-seeded, not refused).
func TestDemoModeStartupGateSeedsFreshDatabase(t *testing.T) {
	dir := t.TempDir()
	db, _, store := openTestEnv(t, dir)

	if err := demoModeStartupGate(db, store, dir); err != nil {
		t.Fatalf("demoModeStartupGate (first startup, fresh db): %v", err)
	}

	marked, err := demoSeedMarkerPresent(db)
	if err != nil {
		t.Fatalf("demoSeedMarkerPresent: %v", err)
	}
	if !marked {
		t.Fatal("expected the seed marker to be present after seeding a fresh database")
	}
	var nClients int
	if err := db.QueryRow(`SELECT COUNT(*) FROM clients`).Scan(&nClients); err != nil {
		t.Fatalf("count clients: %v", err)
	}
	if nClients == 0 {
		t.Fatal("expected seedDemoData to have populated clients")
	}

	// Second startup against the seeded database: accepted (marker present) and not reseeded (seedDemoData's idempotence is covered in demoseed_test.go).
	if err := demoModeStartupGate(db, store, dir); err != nil {
		t.Fatalf("demoModeStartupGate (second startup, seeded db): %v", err)
	}
	var nClientsAfter int
	if err := db.QueryRow(`SELECT COUNT(*) FROM clients`).Scan(&nClientsAfter); err != nil {
		t.Fatalf("count clients after second startup: %v", err)
	}
	if nClientsAfter != nClients {
		t.Fatalf("clients after second startup = %d, want %d (unchanged, not reseeded)", nClientsAfter, nClients)
	}
}
