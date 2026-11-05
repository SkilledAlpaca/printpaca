package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An employee's reads of the effective config omit the SMTP block and the integration URLs; an admin's
// reads carry everything.

const redactionYAML = `smtp:
  host: smtp.internal.example
  port: 2525
  username: mailer
  from: shop@example.com
  to: owner@example.com
integrationsUrls:
  spoolmanUrl: http://spoolman.internal:7912
  moonrakerUrl: http://printer.internal:7125
  fluiddUrl: http://printer.internal
`

func newRedactionFixture(t *testing.T) (mux *http.ServeMux, dir string, store *ConfigStore) {
	t.Helper()
	t.Setenv("SPOOLMAN_URL", "")
	t.Setenv("MOONRAKER_URL", "")
	t.Setenv("FLUIDD_URL", "")
	dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(redactionYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	db := openTestDB(t, dir)
	store = NewConfigStore(loadTestConfig(t, dir))
	auth := newAuthState("adminpw", nil)
	auth.db = db
	for name, perms := range map[string][]string{"quinn": {"quote"}, "flo": {"filament"}} {
		if _, _, err := createUser(db, name, "password123", perms, ""); err != nil {
			t.Fatalf("createUser %s: %v", name, err)
		}
	}
	mux = http.NewServeMux()
	mux.HandleFunc("GET /api/config", handleConfig(store, configExtras{}, auth))
	mux.HandleFunc("GET /api/settings", handleSettingsGet(dir, store))
	mux.HandleFunc("PUT /api/settings", handleSettingsPut(dir, store, auth))
	return mux, dir, store
}

func getAs(mux *http.ServeMux, path string, id identity) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	req = req.WithContext(withIdentity(req.Context(), id))
	return serveReq(mux, req)
}

type redactionBody struct {
	Smtp             Smtp             `json:"smtp"`
	IntegrationsUrls IntegrationsUrls `json:"integrationsUrls"`
	Integrations     Integrations     `json:"integrations"`
	Moonraker        bool             `json:"moonraker"`
	FluiddURL        string           `json:"fluiddUrl"`
	SmtpConfigured   bool             `json:"smtpConfigured"`
	Materials        []Material       `json:"materials"`
	Pricing          Pricing          `json:"pricing"`
}

func TestConfigReadAdminSeesEverything(t *testing.T) {
	mux, _, _ := newRedactionFixture(t)
	rec := getAs(mux, "/api/config", identity{username: "admin", isAdmin: true})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var b redactionBody
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	if b.Smtp.Host != "smtp.internal.example" || b.Smtp.Username != "mailer" || b.Smtp.To != "owner@example.com" {
		t.Errorf("admin smtp = %+v, want the full block", b.Smtp)
	}
	if b.IntegrationsUrls.SpoolmanURL == "" || b.IntegrationsUrls.MoonrakerURL == "" || b.IntegrationsUrls.FluiddURL == "" {
		t.Errorf("admin integrationsUrls = %+v, want all three", b.IntegrationsUrls)
	}
	if b.FluiddURL != "http://printer.internal" || !b.SmtpConfigured {
		t.Errorf("admin fluiddUrl = %q, smtpConfigured = %v", b.FluiddURL, b.SmtpConfigured)
	}
}

func TestConfigReadEmployeeOmitsSmtpAndIntegrationURLs(t *testing.T) {
	mux, _, _ := newRedactionFixture(t)
	for _, name := range []string{"quinn", "flo"} { // quotes grant, filament grant
		t.Run(name, func(t *testing.T) {
			rec := getAs(mux, "/api/config", identity{username: name})
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
			}
			raw := rec.Body.String()
			for _, secret := range []string{"smtp.internal.example", "mailer", "owner@example.com", "spoolman.internal", "printer.internal", "7125"} {
				if strings.Contains(raw, secret) {
					t.Errorf("employee body contains %q", secret)
				}
			}
			var b redactionBody
			if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
				t.Fatal(err)
			}
			if b.Smtp != (Smtp{}) || b.IntegrationsUrls != (IntegrationsUrls{}) || b.FluiddURL != "" {
				t.Errorf("employee still sees smtp %+v, integrationsUrls %+v, fluiddUrl %q", b.Smtp, b.IntegrationsUrls, b.FluiddURL)
			}
			// What their screens need stays: the derived flags and the pricing data.
			if !b.Integrations.Spoolman || !b.Moonraker || !b.SmtpConfigured {
				t.Errorf("flags lost: spoolman=%v moonraker=%v smtpConfigured=%v", b.Integrations.Spoolman, b.Moonraker, b.SmtpConfigured)
			}
			if len(b.Materials) == 0 || b.Pricing.MarginPct == 0 {
				t.Errorf("pricing data missing: %d materials, margin %v", len(b.Materials), b.Pricing.MarginPct)
			}
		})
	}
}

func TestSettingsReadEmployeeOmitsSmtpAndIntegrationURLs(t *testing.T) {
	mux, _, _ := newRedactionFixture(t)
	rec := getAs(mux, "/api/settings", identity{username: "flo"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	for _, secret := range []string{"smtp.internal.example", "mailer", "spoolman.internal", "printer.internal"} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Errorf("employee settings body contains %q", secret)
		}
	}
	admin := getAs(mux, "/api/settings", identity{username: "admin", isAdmin: true})
	if !strings.Contains(admin.Body.String(), "smtp.internal.example") || !strings.Contains(admin.Body.String(), "spoolman.internal") {
		t.Error("admin settings body lost the SMTP host or Spoolman URL")
	}
}

// An employee's editor sends the config it loaded, with the omitted blocks blank. That save must still work and
// must leave the stored SMTP block and integration URLs alone; a real change to either stays refused.
func TestSettingsPutEmployeeKeepsOmittedBlocks(t *testing.T) {
	mux, dir, store := newRedactionFixture(t)
	loaded := getAs(mux, "/api/settings", identity{username: "flo"})
	var resp struct {
		Config *Config `json:"config"`
	}
	if err := json.NewDecoder(bytes.NewReader(loaded.Body.Bytes())).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	resp.Config.Materials[0].PricePerKg = 123
	rec := putSettingsAs(t, mux, resp.Config, identity{username: "flo"})
	if rec.Code != http.StatusOK {
		t.Fatalf("employee save status = %d: %s", rec.Code, rec.Body.String())
	}
	got := loadTestConfig(t, dir)
	if got.Materials[0].PricePerKg != 123 {
		t.Errorf("material price = %v, want 123", got.Materials[0].PricePerKg)
	}
	if got.Smtp.Host != "smtp.internal.example" || got.IntegrationsUrls.SpoolmanURL != "http://spoolman.internal:7912" {
		t.Errorf("stored blocks changed: smtp %+v, urls %+v", got.Smtp, got.IntegrationsUrls)
	}
	if store.Get().Smtp.Host != "smtp.internal.example" {
		t.Error("live config lost the SMTP host")
	}

	changed := cloneConfig(t, got)
	changed.Smtp.Host = "evil.example"
	if rec := putSettingsAs(t, mux, changed, identity{username: "flo"}); rec.Code != http.StatusForbidden {
		t.Errorf("employee changing smtp: status = %d, want 403", rec.Code)
	}
	changed = cloneConfig(t, got)
	changed.IntegrationsUrls.SpoolmanURL = "http://evil.example"
	if rec := putSettingsAs(t, mux, changed, identity{username: "flo"}); rec.Code != http.StatusForbidden {
		t.Errorf("employee changing integrationsUrls: status = %d, want 403", rec.Code)
	}
}
