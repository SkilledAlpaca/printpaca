package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type settingsGetResponse struct {
	Config    *Config `json:"config"`
	Builtin   *Config `json:"builtin"`
	LoadError string  `json:"loadError,omitempty"`
}

type settingsErrResponse struct {
	Errors []string `json:"errors"`
}

func newSettingsMux(dir string, store *ConfigStore) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/settings", handleSettingsGet(dir, store))
	// A disabled authState with no session middleware: identityFor falls back to the admin identity, so tests here exercise handleSettingsPut's unrestricted admin path.
	// Scoped non-admin tests build their own mux/authState (see TestSettingsScoped*).
	mux.HandleFunc("PUT /api/settings", handleSettingsPut(dir, store, newAuthState("", nil)))
	return mux
}

func putSettings(t *testing.T, mux *http.ServeMux, cfg *Config) *httptest.ResponseRecorder {
	t.Helper()
	payload := struct {
		Config *Config `json:"config"`
	}{cfg}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal PUT body: %v", err)
	}
	rec := serveReq(mux, httptest.NewRequest("PUT", "/api/settings", bytes.NewReader(data)))
	return rec
}

// cloneConfig deep-copies cfg via a JSON round trip, since Config's json
// tags cover every field the settings API exchanges with a client.
func cloneConfig(t *testing.T, cfg *Config) *Config {
	t.Helper()
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	var out Config
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	return &out
}

func TestSettingsPutRoundTrip(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"default unchanged", func(c *Config) {}},
		{"scalar tweak", func(c *Config) {
			c.Pricing.MarginPct = 45
		}},
		{"modified builtin preset field", func(c *Config) {
			hobby := c.Presets["hobby"]
			hobby.LaborRate = 99
			c.Presets["hobby"] = hobby
		}},
		{"user-added preset", func(c *Config) {
			c.Presets["custom"] = Preset{
				FailureRatePct: 2,
				PrepMinutes:    1,
				LaborRate:      20,
				MonthlyFixed:   10,
				MonthlyHours:   40,
			}
		}},
		{"replaced materials array", func(c *Config) {
			c.Materials = []Material{
				{ID: "pla", Name: "PLA Custom", Density: 1.24, PricePerKg: 99, WastePct: 5},
				{ID: "petg", Name: "PETG", Density: 1.27, PricePerKg: 22, WastePct: 0},
			}
		}},
		{"tou energy config", func(c *Config) {
			c.Energy = Energy{
				Mode:     "tou",
				FlatRate: c.Energy.FlatRate,
				Tou:      Tou{PeakRate: 0.5, OffPeakRate: 0.2, DefaultOffPeakFrac: 0.6},
				Schedule: c.Energy.Schedule,
			}
		}},
		{"schedule energy config", func(c *Config) {
			c.Energy.Mode = "schedule"
			c.Energy.Schedule = EnergySchedule{
				DefaultStartTime: "21:30",
				Holidays:         []string{"2026-12-25"},
				Seasons: []TouSeason{{
					Name: "All year", Months: []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12},
					Weekday: TouDay{BaseName: "Off-peak", BaseRate: 0.2, Windows: []TouWindow{{Name: "Peak", Start: "16:00", End: "21:00", Rate: 0.4}}},
					Weekend: TouDay{BaseName: "Off-peak", BaseRate: 0.2, Windows: []TouWindow{}},
				}},
			}
		}},
		{"invoicing section", func(c *Config) {
			c.Invoicing = Invoicing{NumberPrefix: "ACME", SalesTaxPct: 8.25, DueDays: 15, PaymentMethods: []string{"cash", "check"}}
		}},
		{"material public flag", func(c *Config) {
			c.Materials[0].Public = true
		}},
		{"publicQuotes section", func(c *Config) {
			c.PublicQuotes = PublicQuotes{Printer: c.Printers[0].ID, Preset: "hobby", IntroText: "Upload your G-code for a quick estimate."}
		}},
		{"smtp section", func(c *Config) {
			c.Smtp = Smtp{Host: "smtp.example.com", Port: 465, Username: "bot", From: "quotes@example.com", To: "owner@example.com"}
		}},
		{"ui disabledTabs section", func(c *Config) {
			c.UI = UI{DisabledTabs: []string{"filament", "guide"}}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			running := loadTestConfig(t, dir)
			desired := cloneConfig(t, running)
			tc.mutate(desired)

			mux := newSettingsMux(dir, NewConfigStore(running))
			rec := putSettings(t, mux, desired)
			if rec.Code != http.StatusOK {
				t.Fatalf("PUT status = %d, body = %s", rec.Code, rec.Body.String())
			}

			loaded := loadTestConfig(t, dir)
			if !reflect.DeepEqual(loaded, desired) {
				t.Fatalf("round trip mismatch:\n got  %+v\n want %+v", loaded, desired)
			}
		})
	}
}

func TestSettingsPutSparseness(t *testing.T) {
	dir := t.TempDir()
	running := loadTestConfig(t, dir)
	desired := cloneConfig(t, running)
	desired.Pricing.MarginPct = 45

	mux := newSettingsMux(dir, NewConfigStore(running))
	rec := putSettings(t, mux, desired)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body = %s", rec.Code, rec.Body.String())
	}

	data, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatalf("read config.yaml: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "printers:") {
		t.Errorf("sparse file should not mention untouched printers key:\n%s", content)
	}
	if !strings.Contains(content, "marginPct: 45") {
		t.Errorf("sparse file missing changed key:\n%s", content)
	}
}

func TestSettingsPutValidationRejected(t *testing.T) {
	dir := t.TempDir()
	running := loadTestConfig(t, dir)
	desired := cloneConfig(t, running)
	desired.Pricing.MarginPct = 150

	mux := newSettingsMux(dir, NewConfigStore(running))
	rec := putSettings(t, mux, desired)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422, body = %s", rec.Code, rec.Body.String())
	}
	var errResp settingsErrResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	// The bound in this message is maxMarginPct (pricing.go), the single
	// ceiling config validation and ComputePricing now share -- not a
	// config-only [0,100) that accepted values the engine then rejected.
	want := "pricing.marginPct: 150 out of range [0,99.9]"
	if !containsStr(errResp.Errors, want) {
		t.Fatalf("errors = %v, want to contain %q", errResp.Errors, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.yaml")); !os.IsNotExist(err) {
		t.Fatalf("config.yaml should not be written on validation failure (stat err = %v)", err)
	}
}

// Error responses must stay no-store through the real admin chain (noStoreAPIMiddleware), not via a handler-local Cache-Control.Set that a direct handler-call test would never notice removed.
func TestSettingsErrorResponsesAreNoStoreViaMiddleware(t *testing.T) {
	dir := t.TempDir()
	running := loadTestConfig(t, dir)
	desired := cloneConfig(t, running)
	desired.Pricing.MarginPct = 150 // out of range: settings.go's validate() rejects this

	payload := struct {
		Config *Config `json:"config"`
	}{desired}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal PUT body: %v", err)
	}

	mux := newSettingsMux(dir, NewConfigStore(running))
	handler := adminListenerHandler(mux, newAuthState("", nil), false) // auth disabled: focus on the header

	req := httptest.NewRequest("PUT", "/api/settings", bytes.NewReader(data))
	req.Host = "localhost"
	req.Header.Set("Content-Type", "application/json")
	rec := serveReq(handler, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422, body = %s", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
}

// A body past settingsMaxBytes is rejected with 413 before validation or writing, like every other write endpoint.
func TestSettingsPutOversizedBodyIs413(t *testing.T) {
	dir := t.TempDir()
	running := loadTestConfig(t, dir)
	mux := newSettingsMux(dir, NewConfigStore(running))

	// Padding lands in quote.businessName, a known field: an unknown one could let DisallowUnknownFields beat MaxBytesReader, as in TestClientsPostOversizedBodyIs413.
	var buf bytes.Buffer
	buf.WriteString(`{"config":{"quote":{"businessName":"`)
	buf.WriteString(strings.Repeat("x", settingsMaxBytes+1))
	buf.WriteString(`"}}}`)

	rec := serveReq(mux, httptest.NewRequest("PUT", "/api/settings", bytes.NewReader(buf.Bytes())))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413, body = %s", rec.Code, rec.Body.String())
	}
	if _, statErr := os.Stat(filepath.Join(dir, "config.yaml")); !os.IsNotExist(statErr) {
		t.Fatalf("config.yaml should not be written on an oversized PUT (stat err = %v)", statErr)
	}
}

func TestSettingsPutDeletionGuard(t *testing.T) {
	dir := t.TempDir()
	running := loadTestConfig(t, dir)
	desired := cloneConfig(t, running)
	delete(desired.Presets, "hobby")

	mux := newSettingsMux(dir, NewConfigStore(running))
	rec := putSettings(t, mux, desired)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422, body = %s", rec.Code, rec.Body.String())
	}
	var errResp settingsErrResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	want := "presets.hobby: built-in entries cannot be removed"
	if !containsStr(errResp.Errors, want) {
		t.Fatalf("errors = %v, want to contain %q", errResp.Errors, want)
	}
}

func TestSettingsPutKeepsBackup(t *testing.T) {
	dir := t.TempDir()
	running := loadTestConfig(t, dir)
	mux := newSettingsMux(dir, NewConfigStore(running))

	first := cloneConfig(t, running)
	first.Pricing.MarginPct = 40
	rec := putSettings(t, mux, first)
	if rec.Code != http.StatusOK {
		t.Fatalf("first PUT status = %d, body = %s", rec.Code, rec.Body.String())
	}
	firstContent, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatalf("read config.yaml after first PUT: %v", err)
	}

	second := cloneConfig(t, running)
	second.Pricing.MarginPct = 55
	rec = putSettings(t, mux, second)
	if rec.Code != http.StatusOK {
		t.Fatalf("second PUT status = %d, body = %s", rec.Code, rec.Body.String())
	}

	bak, err := os.ReadFile(filepath.Join(dir, "config.yaml.bak"))
	if err != nil {
		t.Fatalf("read config.yaml.bak: %v", err)
	}
	if string(bak) != string(firstContent) {
		t.Fatalf("config.yaml.bak = %q, want first PUT content %q", bak, firstContent)
	}
}

// A PUT's effect is visible through the store immediately: no restart and no reload of config.yaml. Uses handleConfig (as main.go wires GET /api/config) against the store the PUT mutates, not pendingConfig(dir).
func TestSettingsPutHotApplies(t *testing.T) {
	dir := t.TempDir()
	running := loadTestConfig(t, dir)
	store := NewConfigStore(running)
	mux := newSettingsMux(dir, store)
	mux.HandleFunc("GET /api/config", handleConfig(store, Integrations{}, configExtras{}, newAuthState("", nil)))

	getMarginPct := func() float64 {
		rec := serveReq(mux, httptest.NewRequest("GET", "/api/config", nil))
		var body struct {
			Pricing struct {
				MarginPct float64 `json:"marginPct"`
			} `json:"pricing"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode /api/config body: %v", err)
		}
		return body.Pricing.MarginPct
	}

	if got := getMarginPct(); got != running.Pricing.MarginPct {
		t.Fatalf("marginPct before any PUT = %v, want %v", got, running.Pricing.MarginPct)
	}

	changed := cloneConfig(t, running)
	changed.Pricing.MarginPct = 41
	rec := putSettings(t, mux, changed)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body = %s", rec.Code, rec.Body.String())
	}

	if got := getMarginPct(); got != 41 {
		t.Errorf("marginPct after PUT (no restart) = %v, want 41", got)
	}
}

// TestSettingsPutConcurrentGet races GET /api/settings against a PUT under
// the race detector (go test -race): store.Get()/store.Set() must never
// hand a torn or partially-built Config to a concurrent request.
func TestSettingsPutConcurrentGet(t *testing.T) {
	dir := t.TempDir()
	running := loadTestConfig(t, dir)
	store := NewConfigStore(running)
	mux := newSettingsMux(dir, store)

	changed := cloneConfig(t, running)
	changed.Pricing.MarginPct = 41

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		rec := putSettings(t, mux, changed)
		if rec.Code != http.StatusOK {
			t.Errorf("PUT status = %d, body = %s", rec.Code, rec.Body.String())
		}
	}()
	go func() {
		defer wg.Done()
		for range 50 {
			rec := serveReq(mux, httptest.NewRequest("GET", "/api/settings", nil))
			if rec.Code != http.StatusOK {
				t.Errorf("GET status = %d, body = %s", rec.Code, rec.Body.String())
			}
			var getResp settingsGetResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &getResp); err != nil {
				t.Errorf("decode GET body: %v", err)
			}
		}
	}()
	wg.Wait()
}

func TestSettingsGetLoadError(t *testing.T) {
	dir := t.TempDir()
	running := loadTestConfig(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("not: [valid"), 0o644); err != nil {
		t.Fatalf("write garbage config.yaml: %v", err)
	}

	mux := newSettingsMux(dir, NewConfigStore(running))
	rec := serveReq(mux, httptest.NewRequest("GET", "/api/settings", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	var getResp settingsGetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("decode GET body: %v", err)
	}
	if getResp.LoadError == "" {
		t.Errorf("loadError missing for a hand-written garbage config.yaml")
	}
	if !reflect.DeepEqual(getResp.Config, running) {
		t.Errorf("config on load error = %+v, want the running config %+v", getResp.Config, running)
	}
}

func TestSettingsPutUnknownFieldRejected(t *testing.T) {
	dir := t.TempDir()
	running := loadTestConfig(t, dir)
	mux := newSettingsMux(dir, NewConfigStore(running))

	rec := httptest.NewRecorder()
	body := `{"config": {}, "bogus": true}`
	mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/settings", strings.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
}

// handleSettingsPut must call normalizeConfig so a PUT omitting ui (or "disabledTabs":null) leaves DisabledTabs [] and not nil; otherwise yaml renders `disabledTabs: null` and pruneEqual
// keeps it as a spurious override of a default it doesn't differ from, in a file advertising "only values that differ from the built-in defaults".
func TestSettingsPutOmittingUIWritesNoSpuriousLine(t *testing.T) {
	dir := t.TempDir()
	running := loadTestConfig(t, dir)
	desired := cloneConfig(t, running)
	desired.UI.DisabledTabs = nil  // simulates a PUT body omitting "ui" (or sending disabledTabs: null)
	desired.Pricing.MarginPct = 46 // an unrelated change, so the PUT writes *something* to config.yaml

	mux := newSettingsMux(dir, NewConfigStore(running))
	rec := putSettings(t, mux, desired)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body = %s", rec.Code, rec.Body.String())
	}

	data, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatalf("read config.yaml: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "disabledTabs") {
		t.Errorf("config.yaml should not mention disabledTabs when ui was omitted/null:\n%s", content)
	}

	loaded := loadTestConfig(t, dir)
	if len(loaded.UI.DisabledTabs) != 0 {
		t.Errorf("loaded UI.DisabledTabs = %v, want empty", loaded.UI.DisabledTabs)
	}
}

// writeConfigFile failures unrelated to storage (EACCES, EROFS, a read error during the .bak step) must not return 507 Insufficient Storage, which monitoring reads as "out of disk" and sends an operator to check free space.
// Only genuine ENOSPC/EDQUOT maps to 507; everything else is 500.
func TestWriteConfigFileErrorStatus(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"permission denied", fmt.Errorf("reading existing config.yaml: %w", syscall.EACCES), http.StatusInternalServerError},
		{"read-only filesystem", fmt.Errorf("writing config.yaml: %w", syscall.EROFS), http.StatusInternalServerError},
		{"generic error", errors.New("boom"), http.StatusInternalServerError},
		{"out of space", fmt.Errorf("writing config.yaml: %w", syscall.ENOSPC), http.StatusInsufficientStorage},
		{"quota exceeded", fmt.Errorf("writing config.yaml: %w", syscall.EDQUOT), http.StatusInsufficientStorage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := writeConfigFileErrorStatus(tc.err); got != tc.want {
				t.Errorf("writeConfigFileErrorStatus(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}

// TestSettingsPutWriteFailureStatus exercises the same mapping through the
// real handler: a PUT that fails to write config.yaml because configDir
// itself is a file (ENOTDIR, not ENOSPC/EDQUOT) must come back 500, not 507.
func TestSettingsPutWriteFailureStatus(t *testing.T) {
	dir := t.TempDir()
	running := loadTestConfig(t, dir)
	// Replace configDir with a path that can never be a directory: any write
	// under it fails with ENOTDIR, a cause writeConfigFileErrorStatus must
	// not classify as insufficient storage.
	blocked := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(blocked, []byte("x"), 0o644); err != nil {
		t.Fatalf("create blocking file: %v", err)
	}

	desired := cloneConfig(t, running)
	desired.Pricing.MarginPct = 47

	mux := newSettingsMux(blocked, NewConfigStore(running))
	rec := putSettings(t, mux, desired)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body = %s", rec.Code, rec.Body.String())
	}
}

func containsStr(list []string, want string) bool {
	return slices.Contains(list, want)
}

// ---------------------------------------------------------------------------
// Scoped settings PUT: employees never get blanket config PUT.
// ---------------------------------------------------------------------------

// newScopedSettingsFixture builds a real database (so userPermissions can
// look up an employee's grants) plus a settings-only mux wired to it.
func newScopedSettingsFixture(t *testing.T) (mux *http.ServeMux, dir string, db *sql.DB, store *ConfigStore) {
	t.Helper()
	dir = t.TempDir()
	db = openTestDB(t, dir)
	cfg := loadTestConfig(t, dir)
	store = NewConfigStore(cfg)
	auth := newAuthState("adminpw", nil)
	auth.db = db
	mux = http.NewServeMux()
	mux.HandleFunc("PUT /api/settings", handleSettingsPut(dir, store, auth))
	return mux, dir, db, store
}

// putSettingsAs PUTs cfg as id by attaching id to the request context directly (the key auth.middleware sets after a real session check; identityFor reads it either way).
func putSettingsAs(t *testing.T, mux *http.ServeMux, cfg *Config, id identity) *httptest.ResponseRecorder {
	t.Helper()
	payload := struct {
		Config *Config `json:"config"`
	}{cfg}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal PUT body: %v", err)
	}
	req := httptest.NewRequest("PUT", "/api/settings", bytes.NewReader(data))
	req = req.WithContext(withIdentity(req.Context(), id))
	rec := serveReq(mux, req)
	return rec
}

func TestSettingsScopedPutAllowsMatchingPermission(t *testing.T) {
	mux, dir, db, store := newScopedSettingsFixture(t)
	if _, _, err := createUser(db, "flo", "password123", []string{"filament"}, ""); err != nil {
		t.Fatalf("createUser: %v", err)
	}
	running := loadTestConfig(t, dir)
	store.Set(running)
	desired := cloneConfig(t, running)
	desired.Materials[0].PricePerKg = 999

	rec := putSettingsAs(t, mux, desired, identity{username: "flo"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (materials is granted by filament); body = %s", rec.Code, rec.Body.String())
	}
}

func TestSettingsScopedPutDeniesUngrantedKey(t *testing.T) {
	mux, dir, db, store := newScopedSettingsFixture(t)
	if _, _, err := createUser(db, "flo", "password123", []string{"filament"}, ""); err != nil {
		t.Fatalf("createUser: %v", err)
	}
	running := loadTestConfig(t, dir)
	store.Set(running)
	desired := cloneConfig(t, running)
	desired.Energy.FlatRate = 0.99 // "electricity" tab, not granted

	rec := putSettingsAs(t, mux, desired, identity{username: "flo"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", rec.Code, rec.Body.String())
	}
	var body authErrorBody
	json.Unmarshal(rec.Body.Bytes(), &body)
	if !strings.Contains(body.Error, "energy") {
		t.Errorf("error = %q, want it to name the offending key %q", body.Error, "energy")
	}
}

// Default-deny: a key with no entry in settingsScopedKeyPermission (smtp) is never allowed via the scoped path, however many tabs are granted.
func TestSettingsScopedPutDeniesUnmappedKeyEvenWithEveryPermission(t *testing.T) {
	mux, dir, db, store := newScopedSettingsFixture(t)
	if _, _, err := createUser(db, "flo", "password123", []string{
		"quote", "quotes", "requests", "invoices", "items", "clients", "dashboard", "filament", "electricity", "printers",
	}, ""); err != nil {
		t.Fatalf("createUser: %v", err)
	}
	running := loadTestConfig(t, dir)
	store.Set(running)
	desired := cloneConfig(t, running)
	desired.Smtp.Host = "smtp.example.com"
	desired.Smtp.From = "a@example.com"
	desired.Smtp.To = "b@example.com"

	rec := putSettingsAs(t, mux, desired, identity{username: "flo"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (smtp is unmapped -- default-deny); body = %s", rec.Code, rec.Body.String())
	}
}

// The scoped-permission decision is judged against the config as it stands NOW, not the employee's earlier snapshot. staleCfg is what "flo" fetched before admin's PUT: flo changes only materials (permitted)
// but carries the old smtp section. Once admin commits a different smtp.host, flo's PUT must be rejected for touching smtp (no permission) rather than silently reverting admin's change.
func TestSettingsScopedPutRejectsStaleSubmissionAfterAdminChange(t *testing.T) {
	mux, dir, db, store := newScopedSettingsFixture(t)
	if _, _, err := createUser(db, "flo", "password123", []string{"filament"}, ""); err != nil {
		t.Fatalf("createUser: %v", err)
	}
	running := loadTestConfig(t, dir)
	store.Set(running)

	// flo's stale snapshot, captured before admin's change below.
	staleCfg := cloneConfig(t, running)

	adminDesired := cloneConfig(t, running)
	adminDesired.Smtp.Host = "changed-by-admin.example.com"
	adminDesired.Smtp.From = "a@example.com"
	adminDesired.Smtp.To = "b@example.com"
	rec := putSettingsAs(t, mux, adminDesired, identity{username: "admin", isAdmin: true})
	if rec.Code != http.StatusOK {
		t.Fatalf("admin PUT: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	floDesired := staleCfg
	floDesired.Materials[0].PricePerKg = 999 // the only change flo intends; permitted by "filament"

	rec = putSettingsAs(t, mux, floDesired, identity{username: "flo"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("flo PUT: status = %d, want 403 (stale smtp must be caught against the post-admin-change config); body = %s", rec.Code, rec.Body.String())
	}
	var body authErrorBody
	json.Unmarshal(rec.Body.Bytes(), &body)
	if !strings.Contains(body.Error, "smtp") {
		t.Errorf("error = %q, want it to name the offending key %q", body.Error, "smtp")
	}

	// admin's change must have survived: flo's rejected PUT must not have
	// reverted it.
	reloaded := loadTestConfig(t, dir)
	if reloaded.Smtp.Host != "changed-by-admin.example.com" {
		t.Errorf("smtp.host = %q, want admin's change to have survived flo's rejected PUT", reloaded.Smtp.Host)
	}
}

// Proves structurally that current := store.Get() runs only while settingsWriteMu is held, via settingsPutCurrentReadHook (fires inside the handler at the read): the hook's TryLock must fail,
// then admin's PUT is launched concurrently and must not complete until flo's is unblocked. Both then complete with the store left at admin's write.
func TestSettingsPutScopedCheckReadsCurrentUnderLock(t *testing.T) {
	mux, dir, db, store := newScopedSettingsFixture(t)
	if _, _, err := createUser(db, "flo", "password123", []string{"filament"}, ""); err != nil {
		t.Fatalf("createUser: %v", err)
	}
	running := loadTestConfig(t, dir)
	store.Set(running)

	floDesired := cloneConfig(t, running)
	floDesired.Materials[0].PricePerKg = 111

	adminDesired := cloneConfig(t, running)
	adminDesired.Smtp.Host = "admin-set.example.com"
	adminDesired.Smtp.From = "a@example.com"
	adminDesired.Smtp.To = "b@example.com"

	floInHook := make(chan struct{})
	releaseFlo := make(chan struct{})
	var hookRan, tryLockFailed bool
	var once sync.Once

	// settingsPutCurrentReadHook fires on EVERY handleSettingsPut call, admin's included; once.Do ensures only flo's first call pauses and asserts, and admin's later call passes through.
	settingsPutCurrentReadHook = func() {
		once.Do(func() {
			hookRan = true
			tryLockFailed = !settingsWriteMu.TryLock() // must already be held by this same goroutine's Lock() above
			if !tryLockFailed {
				settingsWriteMu.Unlock() // wrongly acquired; release it so the test doesn't hang
			}
			close(floInHook)
			<-releaseFlo
		})
	}
	t.Cleanup(func() { settingsPutCurrentReadHook = nil })

	floDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		floDone <- putSettingsAs(t, mux, floDesired, identity{username: "flo"})
	}()

	<-floInHook // flo is now parked mid-handler, holding settingsWriteMu

	adminDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		adminDone <- putSettingsAs(t, mux, adminDesired, identity{username: "admin", isAdmin: true})
	}()

	// admin's PUT needs settingsWriteMu too, so it cannot possibly finish
	// while flo is parked holding it; a short grace period distinguishes
	// "genuinely blocked" from "raced ahead and finished already".
	select {
	case <-adminDone:
		t.Fatal("admin's PUT completed while flo's was still parked inside the lock -- the mutex did not serialize them")
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseFlo)

	floRec := <-floDone
	if floRec.Code != http.StatusOK {
		t.Fatalf("flo PUT: status = %d, want 200; body = %s", floRec.Code, floRec.Body.String())
	}
	adminRec := <-adminDone
	if adminRec.Code != http.StatusOK {
		t.Fatalf("admin PUT: status = %d, want 200; body = %s", adminRec.Code, adminRec.Body.String())
	}

	if !hookRan {
		t.Fatal("settingsPutCurrentReadHook never ran -- test did not exercise the read it's meant to pin")
	}
	if tryLockFailed {
		// This is the expected, fixed outcome: current was read while
		// already holding settingsWriteMu.
	} else {
		t.Error("settingsWriteMu.TryLock() succeeded inside the hook: current was read without the lock already held")
	}

	// Admin's PUT is an unrestricted blanket replace built from its pre-flo snapshot, so it reverts flo's materials change by design; what matters is that admin's smtp change landed, proving it ran rather than deadlocked.
	reloaded := loadTestConfig(t, dir)
	if reloaded.Smtp.Host != "admin-set.example.com" {
		t.Errorf("smtp.host = %q, want admin's change (applied after flo's, once unblocked)", reloaded.Smtp.Host)
	}
}

// TestSettingsScopedPutPrintersFilamentDiameterOnlyNeedsFilament pins the
// split of the "printers" config key across two tabs: filamentDiameterMm
// alone is covered by "filament", not "printers".
func TestSettingsScopedPutPrintersFilamentDiameterOnlyNeedsFilament(t *testing.T) {
	mux, dir, db, store := newScopedSettingsFixture(t)
	if _, _, err := createUser(db, "flo", "password123", []string{"filament"}, ""); err != nil {
		t.Fatalf("createUser: %v", err)
	}
	running := loadTestConfig(t, dir)
	store.Set(running)
	desired := cloneConfig(t, running)
	desired.Printers[0].FilamentDiameterMm = 2.85

	rec := putSettingsAs(t, mux, desired, identity{username: "flo"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (filamentDiameterMm-only change is covered by filament); body = %s", rec.Code, rec.Body.String())
	}
}

// TestSettingsScopedPutPrintersOtherFieldNeedsPrintersPermission pins the
// other half of that same split: changing any other printer field requires
// "printers", and "filament" alone is not enough.
func TestSettingsScopedPutPrintersOtherFieldNeedsPrintersPermission(t *testing.T) {
	mux, dir, db, store := newScopedSettingsFixture(t)
	if _, _, err := createUser(db, "flo", "password123", []string{"filament"}, ""); err != nil {
		t.Fatalf("createUser: %v", err)
	}
	if _, _, err := createUser(db, "gus", "password123", []string{"printers"}, ""); err != nil {
		t.Fatalf("createUser: %v", err)
	}
	running := loadTestConfig(t, dir)
	store.Set(running)
	desired := cloneConfig(t, running)
	desired.Printers[0].Watts = 300

	rec := putSettingsAs(t, mux, desired, identity{username: "flo"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("flo (filament only): status = %d, want 403 (watts is not filamentDiameterMm); body = %s", rec.Code, rec.Body.String())
	}

	rec = putSettingsAs(t, mux, desired, identity{username: "gus"})
	if rec.Code != http.StatusOK {
		t.Fatalf("gus (printers): status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
}

// TestSettingsPutAdminBypassesScopedCheck pins that the admin identity
// keeps today's unrestricted PUT regardless of what changed -- the scoped
// check in handleSettingsPut only ever runs for a non-admin identity.
func TestSettingsPutAdminBypassesScopedCheck(t *testing.T) {
	mux, dir, _, store := newScopedSettingsFixture(t)
	running := loadTestConfig(t, dir)
	store.Set(running)
	desired := cloneConfig(t, running)
	desired.Smtp.Host = "smtp.example.com"
	desired.Smtp.From = "a@example.com"
	desired.Smtp.To = "b@example.com"

	rec := putSettingsAs(t, mux, desired, identity{username: "admin", isAdmin: true})
	if rec.Code != http.StatusOK {
		t.Fatalf("admin status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
}

// Turning users.multiUser on via a live Settings save is a 422 unless ADMIN_PASSWORD is set, the same rule as the startup gate for a hand-edited config.yaml, folded into the validate() 422 response.
func TestSettingsPutMultiUserRequiresAdminPassword(t *testing.T) {
	dir := t.TempDir()
	running := loadTestConfig(t, dir)
	store := NewConfigStore(running)
	disabledAuth := newAuthState("", nil) // ADMIN_PASSWORD unset/disabled
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/settings", handleSettingsPut(dir, store, disabledAuth))

	desired := cloneConfig(t, running)
	desired.Users.MultiUser = true

	rec := putSettings(t, mux, desired)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", rec.Code, rec.Body.String())
	}
	var errResp settingsErrResponse
	json.Unmarshal(rec.Body.Bytes(), &errResp)
	if !containsSubstr(errResp.Errors, "ADMIN_PASSWORD") {
		t.Fatalf("errors = %v, want one mentioning ADMIN_PASSWORD", errResp.Errors)
	}
}
