package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigDefaultsOnly(t *testing.T) {
	cfg, err := LoadConfig(t.TempDir()) // no config.yaml present
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Pricing.MarginPct != 30 {
		t.Errorf("marginPct = %v, want 30", cfg.Pricing.MarginPct)
	}
	if len(cfg.Materials) != 5 || cfg.Materials[0].ID != "pla" {
		t.Errorf("materials not loaded from defaults: %+v", cfg.Materials)
	}
	if _, ok := cfg.FeeSchedules["etsy-us"]; !ok {
		t.Error("etsy-us fee schedule missing")
	}
}

func TestLoadConfigMergesUserFile(t *testing.T) {
	dir := t.TempDir()
	user := `
pricing:
  marginPct: 40
materials:
  - { id: pla-plus, name: "PLA+", density: 1.24, pricePerKg: 17.50 }
defaults:
  material: pla-plus
feeSchedules:
  direct:
    fixed: []
    percent: []
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(user), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := loadTestConfig(t, dir)
	if cfg.Pricing.MarginPct != 40 {
		t.Errorf("user marginPct not applied: %v", cfg.Pricing.MarginPct)
	}
	if cfg.Pricing.MarkupMultiplier != 2.5 {
		t.Errorf("untouched default lost: %v", cfg.Pricing.MarkupMultiplier)
	}
	if len(cfg.Materials) != 1 || cfg.Materials[0].ID != "pla-plus" {
		t.Errorf("arrays should replace, got %+v", cfg.Materials)
	}
	if _, ok := cfg.FeeSchedules["etsy-us"]; !ok {
		t.Error("maps should merge by key; etsy-us lost")
	}
	if _, ok := cfg.FeeSchedules["direct"]; !ok {
		t.Error("user fee schedule missing")
	}
}

func TestLoadConfigRejectsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("pricing:\n  margin: 40\n"), 0o644)
	_, err := LoadConfig(dir)
	if err == nil || !strings.Contains(err.Error(), "margin") {
		t.Fatalf("want unknown-field error mentioning the field, got %v", err)
	}
}

func TestLoadConfigValidatesReferences(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("defaults:\n  material: carbonfiber\n"), 0o644)
	_, err := LoadConfig(dir)
	if err == nil || !strings.Contains(err.Error(), "carbonfiber") {
		t.Fatalf("want dangling-reference error, got %v", err)
	}
}

func TestValidateCatchesBadValues(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.yaml"),
		[]byte("pricing:\n  marginPct: 150\n"), 0o644)
	_, err := LoadConfig(dir)
	if err == nil || !strings.Contains(err.Error(), "pricing.marginPct") {
		t.Fatalf("want path-level validation error, got %v", err)
	}
}

// Keeps config validation and ComputePricing on ONE marginPct ceiling. A value that passes validation but exceeds
// ComputePricing's 99.9 cap would start clean and then make every quote unpriceable (Valid false, public previews 422).
// No value passing config validation may produce an above-max marginPct warning, and the rejection must name the field and bound.
func TestMarginPctCeilingIsAlignedAcrossLayers(t *testing.T) {
	loadWithMargin := func(t *testing.T, margin string) (*Config, error) {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "config.yaml"),
			[]byte("pricing:\n  marginPct: "+margin+"\n"), 0o644); err != nil {
			t.Fatalf("write config: %v", err)
		}
		return LoadConfig(dir)
	}

	// The dead zone [99.9, 100) is rejected at config load -- the single
	// place a marginPct is entered -- naming both the field and the bound.
	if _, err := loadWithMargin(t, "99.95"); err == nil {
		t.Fatal("marginPct 99.95 loaded clean; want a config validation error")
	} else {
		for _, want := range []string{"pricing.marginPct", "99.9"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not mention %q", err.Error(), want)
			}
		}
	}

	// Anything config accepts must actually price. The boundary value is the
	// interesting one: 99.9 is inclusive on both sides of the alignment.
	for _, margin := range []string{"0", "30", "99.9"} {
		cfg, err := loadWithMargin(t, margin)
		if err != nil {
			t.Fatalf("marginPct %s: config rejected a value it should accept: %v", margin, err)
		}
		var material Material
		for _, m := range cfg.Materials {
			if m.Name == "PLA" {
				material = m
			}
		}
		if material.Name == "" {
			material = cfg.Materials[0]
		}
		qi, opt := buildPublicQuoteInputs(cfg, material, 1, 50, 3600)
		bd := ComputeQuote(qi, opt)
		if bd.Pricing == nil {
			t.Fatalf("marginPct %s: no pricing result", margin)
		}
		if hasWarning(bd.Pricing.Warnings, "marginPct", "above-max") {
			t.Errorf("marginPct %s: config accepted a value ComputePricing then rejects as above-max -- the two ceilings have diverged again", margin)
		}
		if !bd.Pricing.Valid {
			t.Errorf("marginPct %s: pricing valid = false, want true; warnings = %+v", margin, bd.Pricing.Warnings)
		}
	}
}

// TestPresetPartialOverridePreservesSiblings pins the entry-level deep merge:
// overriding one field of a preset must not silently zero its siblings.
func TestPresetPartialOverridePreservesSiblings(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.yaml"),
		[]byte("presets:\n  hobby:\n    laborRate: 99\n"), 0o644)
	cfg := loadTestConfig(t, dir)
	hobby, ok := cfg.Presets["hobby"]
	if !ok {
		t.Fatal("hobby preset missing")
	}
	if hobby.LaborRate != 99 {
		t.Errorf("laborRate override not applied: %v", hobby.LaborRate)
	}
	if hobby.FailureRatePct != 3 {
		t.Errorf("sibling failureRatePct zeroed: %v, want 3 (default)", hobby.FailureRatePct)
	}
	if hobby.PrepMinutes != 0 {
		t.Errorf("sibling prepMinutes changed: %v, want 0 (default)", hobby.PrepMinutes)
	}
	if hobby.MonthlyHours != 50 {
		t.Errorf("sibling monthlyHours zeroed: %v, want 50 (default)", hobby.MonthlyHours)
	}
}

// TestFeeSchedulePartialOverridePreservesSourceAndFixed pins the same
// deep-merge behavior for feeSchedules: overriding percent must not drop the
// entry's source or fixed fees.
func TestFeeSchedulePartialOverridePreservesSourceAndFixed(t *testing.T) {
	dir := t.TempDir()
	user := `
feeSchedules:
  etsy-us:
    percent:
      - { pct: 10, base: revenue, label: custom }
`
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(user), 0o644)
	cfg := loadTestConfig(t, dir)
	fs, ok := cfg.FeeSchedules["etsy-us"]
	if !ok {
		t.Fatal("etsy-us fee schedule missing")
	}
	if fs.Source == "" {
		t.Error("source lost on partial override")
	}
	if len(fs.Fixed) != 2 {
		t.Errorf("fixed fees lost on partial override: %+v", fs.Fixed)
	}
	if len(fs.Percent) != 1 || fs.Percent[0].Label != "custom" {
		t.Errorf("percent override not applied: %+v", fs.Percent)
	}
}

func TestLoadConfigToleratesEmptyOrCommentsOnlyFile(t *testing.T) {
	cases := map[string]string{
		"empty":         "",
		"comments-only": "# just a comment\n\n   \n# another\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0o644)
			cfg := loadTestConfig(t, dir)
			if cfg.Pricing.MarginPct != 30 {
				t.Errorf("defaults not applied: marginPct = %v, want 30", cfg.Pricing.MarginPct)
			}
		})
	}
}

func TestLoadConfigRejectsMultiDocumentYAML(t *testing.T) {
	dir := t.TempDir()
	user := "pricing:\n  marginPct: 40\n---\npricing:\n  marginPct: 50\n"
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(user), 0o644)
	_, err := LoadConfig(dir)
	if err == nil || !strings.Contains(err.Error(), "multiple YAML documents") {
		t.Fatalf("want multi-document error, got %v", err)
	}
}

func TestValidateRejectsNonRevenueBase(t *testing.T) {
	dir := t.TempDir()
	user := `
feeSchedules:
  etsy-us:
    percent:
      - { pct: 5, base: profit, label: x }
`
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(user), 0o644)
	_, err := LoadConfig(dir)
	if err == nil || !strings.Contains(err.Error(), "profit") || !strings.Contains(err.Error(), "revenue") {
		t.Fatalf("want path-level base error mentioning profit/revenue, got %v", err)
	}
}

func TestValidateRejectsOutOfRangeWastePct(t *testing.T) {
	dir := t.TempDir()
	user := `
materials:
  - { id: pla, name: PLA, density: 1.24, pricePerKg: 20.00, wastePct: 150 }
`
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(user), 0o644)
	_, err := LoadConfig(dir)
	if err == nil || !strings.Contains(err.Error(), "wastePct") {
		t.Fatalf("want wastePct range error, got %v", err)
	}
}

func TestOmittedWastePctDefaultsToZero(t *testing.T) {
	dir := t.TempDir()
	// defaults.material is already "pla" via embedded defaults, so a
	// wholesale materials override with a matching id needs no other change.
	user := `
materials:
  - { id: pla, name: PLA, density: 1.24, pricePerKg: 20.00 }
`
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(user), 0o644)
	cfg := loadTestConfig(t, dir)
	if cfg.Materials[0].WastePct != 0 {
		t.Errorf("omitted wastePct = %v, want 0", cfg.Materials[0].WastePct)
	}
}

// Materials/printers/feeSchedules/presets each reject past 200 entries (maxConfigCollectionSize) even when every entry is valid,
// so an unbounded PUT can't be written to config.yaml and re-loaded on every startup.
func TestValidateRejectsOversizedCollections(t *testing.T) {
	cfg, err := LoadConfig(t.TempDir()) // defaults: a valid starting point
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	base := cfg.Materials[0]
	for i := range maxConfigCollectionSize + 1 {
		m := base
		m.ID = fmt.Sprintf("extra-%d", i)
		cfg.Materials = append(cfg.Materials, m)
	}

	problems := validate(cfg)
	want := fmt.Sprintf("materials: %d entries exceeds max %d", len(cfg.Materials), maxConfigCollectionSize)
	if !containsSubstr(problems, want) {
		t.Fatalf("problems = %v, want to contain %q", problems, want)
	}
}

func TestValidateRejectsOutOfRangePaymentMethodsCount(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("invoicing:\n  paymentMethods: []\n"), 0o644)
	_, err := LoadConfig(dir)
	if err == nil || !strings.Contains(err.Error(), "invoicing.paymentMethods") {
		t.Fatalf("want invoicing.paymentMethods count error, got %v", err)
	}
}

func TestValidateRejectsBlankPaymentMethodEntry(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("invoicing:\n  paymentMethods: [\"cash\", \"   \"]\n"), 0o644)
	_, err := LoadConfig(dir)
	if err == nil || !strings.Contains(err.Error(), "invoicing.paymentMethods[1]") {
		t.Fatalf("want invoicing.paymentMethods[1] error, got %v", err)
	}
}

func TestValidateRejectsOverlongPaymentMethodEntry(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("x", 51)
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("invoicing:\n  paymentMethods: [\""+long+"\"]\n"), 0o644)
	_, err := LoadConfig(dir)
	if err == nil || !strings.Contains(err.Error(), "invoicing.paymentMethods[0]") {
		t.Fatalf("want invoicing.paymentMethods[0] error, got %v", err)
	}
}

func TestValidateTrimsPaymentMethodEntries(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("invoicing:\n  paymentMethods: [\"  cash \", \"check\"]\n"), 0o644)
	cfg := loadTestConfig(t, dir)
	if cfg.Invoicing.PaymentMethods[0] != "cash" {
		t.Errorf("paymentMethods[0] = %q, want trimmed %q", cfg.Invoicing.PaymentMethods[0], "cash")
	}
}

// These pin the caps on the two branding fields that land unbounded in every sent email body (mail_templates.go).
func TestValidateRejectsOverlongPaymentInstructions(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("x", 2001)
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("quote:\n  paymentInstructions: \""+long+"\"\n"), 0o644)
	_, err := LoadConfig(dir)
	if err == nil || !strings.Contains(err.Error(), "quote.paymentInstructions") {
		t.Fatalf("want quote.paymentInstructions error, got %v", err)
	}
}

func TestValidateRejectsOverlongTermsText(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("x", 2001)
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("quote:\n  termsText: \""+long+"\"\n"), 0o644)
	_, err := LoadConfig(dir)
	if err == nil || !strings.Contains(err.Error(), "quote.termsText") {
		t.Fatalf("want quote.termsText error, got %v", err)
	}
}

// TestLoadConfigDefaultsHavePublicQuotesAndSmtp pins the config defaults:
// publicQuotes resolves to real printer/preset entries and smtp
// defaults to a disabled (empty host) configuration.
func TestLoadConfigDefaultsHavePublicQuotesAndSmtp(t *testing.T) {
	cfg := loadTestConfig(t, t.TempDir())
	if cfg.PublicQuotes.Printer == "" {
		t.Error("publicQuotes.printer default is empty")
	}
	found := false
	for _, pr := range cfg.Printers {
		if pr.ID == cfg.PublicQuotes.Printer {
			found = true
		}
	}
	if !found {
		t.Errorf("publicQuotes.printer %q does not name a printer", cfg.PublicQuotes.Printer)
	}
	if _, ok := cfg.Presets[cfg.PublicQuotes.Preset]; !ok {
		t.Errorf("publicQuotes.preset %q does not name a preset", cfg.PublicQuotes.Preset)
	}
	if cfg.Smtp.Host != "" {
		t.Errorf("smtp.host default = %q, want empty", cfg.Smtp.Host)
	}
	if cfg.Smtp.Port != 587 {
		t.Errorf("smtp.port default = %v, want 587", cfg.Smtp.Port)
	}
}

// TestMaterialPublicFlagRoundTrips pins that the material.public flag (phase
// 8a) survives a config.yaml round trip.
func TestMaterialPublicFlagRoundTrips(t *testing.T) {
	dir := t.TempDir()
	user := `
materials:
  - { id: pla, name: PLA, density: 1.24, pricePerKg: 20.00, public: true }
`
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(user), 0o644)
	cfg := loadTestConfig(t, dir)
	if len(cfg.Materials) != 1 || !cfg.Materials[0].Public {
		t.Errorf("materials[0].public = %+v, want true", cfg.Materials)
	}
}

func TestValidateRejectsUnknownPublicQuotesPrinter(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("publicQuotes:\n  printer: nope\n"), 0o644)
	_, err := LoadConfig(dir)
	if err == nil || !strings.Contains(err.Error(), "publicQuotes.printer") {
		t.Fatalf("want publicQuotes.printer error, got %v", err)
	}
}

func TestValidateRejectsUnknownPublicQuotesPreset(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("publicQuotes:\n  preset: nope\n"), 0o644)
	_, err := LoadConfig(dir)
	if err == nil || !strings.Contains(err.Error(), "publicQuotes.preset") {
		t.Fatalf("want publicQuotes.preset error, got %v", err)
	}
}

// The top-level customCss key survives a config.yaml round trip verbatim, like introText.
func TestCustomCssRoundTrips(t *testing.T) {
	dir := t.TempDir()
	css := ":root { --accent: #2f6fed; }\n.cta-heading { color: var(--accent); }"
	user := "customCss: " + fmt.Sprintf("%q", css) + "\n"
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(user), 0o644)
	cfg := loadTestConfig(t, dir)
	if cfg.CustomCss != css {
		t.Errorf("customCss = %q, want %q", cfg.CustomCss, css)
	}
}

// One byte over the 32 KiB customCssMaxBytes cap must be rejected at load, not re-served on every public/print page.
func TestValidateRejectsOverlongCustomCss(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("x", customCssMaxBytes+1)
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("customCss: \""+long+"\"\n"), 0o644)
	_, err := LoadConfig(dir)
	if err == nil || !strings.Contains(err.Error(), "customCss") {
		t.Fatalf("want customCss error, got %v", err)
	}
}

func TestValidateRejectsSmtpFromWithoutAt(t *testing.T) {
	dir := t.TempDir()
	user := "smtp:\n  host: smtp.example.com\n  from: no-at-sign\n  to: owner@example.com\n"
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(user), 0o644)
	_, err := LoadConfig(dir)
	if err == nil || !strings.Contains(err.Error(), "smtp.from") {
		t.Fatalf("want smtp.from error, got %v", err)
	}
}

func TestValidateSmtpSkippedWhenHostEmpty(t *testing.T) {
	dir := t.TempDir()
	// No host set: from/to/port are never validated even though they'd
	// otherwise fail (port 0, no @ in either field).
	user := "smtp:\n  port: 0\n  from: nope\n  to: nope\n"
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(user), 0o644)
	if _, err := LoadConfig(dir); err != nil {
		t.Fatalf("unexpected error with smtp.host empty: %v", err)
	}
}

// TestLoadConfigDefaultDisabledTabsIsEmpty pins the UI-toggles default:
// no tab is hidden until the owner opts in via Settings.
func TestLoadConfigDefaultDisabledTabsIsEmpty(t *testing.T) {
	cfg := loadTestConfig(t, t.TempDir())
	if len(cfg.UI.DisabledTabs) != 0 {
		t.Errorf("ui.disabledTabs default = %+v, want empty", cfg.UI.DisabledTabs)
	}
	data, err := json.Marshal(cfg.UI.DisabledTabs)
	if err != nil {
		t.Fatalf("marshal disabledTabs: %v", err)
	}
	if string(data) != "[]" {
		t.Errorf("ui.disabledTabs JSON = %s, want []", data)
	}
}

func TestValidateAcceptsKnownDisabledTabs(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("ui:\n  disabledTabs: [filament, guide]\n"), 0o644)
	cfg := loadTestConfig(t, dir)
	if len(cfg.UI.DisabledTabs) != 2 {
		t.Errorf("ui.disabledTabs = %+v, want 2 entries", cfg.UI.DisabledTabs)
	}
}

func TestValidateRejectsQuoteAsDisabledTab(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("ui:\n  disabledTabs: [quote]\n"), 0o644)
	_, err := LoadConfig(dir)
	if err == nil || !strings.Contains(err.Error(), "ui.disabledTabs[0]") {
		t.Fatalf("want ui.disabledTabs[0] error rejecting \"quote\", got %v", err)
	}
}

func TestValidateRejectsSettingsAsDisabledTab(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("ui:\n  disabledTabs: [settings]\n"), 0o644)
	_, err := LoadConfig(dir)
	if err == nil || !strings.Contains(err.Error(), "ui.disabledTabs[0]") {
		t.Fatalf("want ui.disabledTabs[0] error rejecting \"settings\", got %v", err)
	}
}

func TestValidateRejectsUnknownDisabledTab(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("ui:\n  disabledTabs: [nope]\n"), 0o644)
	_, err := LoadConfig(dir)
	if err == nil || !strings.Contains(err.Error(), "\"nope\" is not a valid tab id") {
		t.Fatalf("want unknown-id error, got %v", err)
	}
}

func TestValidateRejectsDuplicateDisabledTab(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("ui:\n  disabledTabs: [filament, filament]\n"), 0o644)
	_, err := LoadConfig(dir)
	if err == nil || !strings.Contains(err.Error(), "ui.disabledTabs[1]: duplicate id \"filament\"") {
		t.Fatalf("want duplicate-id error, got %v", err)
	}
}

func TestLoadConfigNormalizesEmptyFeeScheduleArrays(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("feeSchedules:\n  direct: {}\n"), 0o644)
	cfg := loadTestConfig(t, dir)
	fs, ok := cfg.FeeSchedules["direct"]
	if !ok {
		t.Fatal("direct fee schedule missing")
	}
	if fs.Fixed == nil || len(fs.Fixed) != 0 {
		t.Errorf("fixed should be an empty (non-nil) slice, got %#v", fs.Fixed)
	}
	if fs.Percent == nil || len(fs.Percent) != 0 {
		t.Errorf("percent should be an empty (non-nil) slice, got %#v", fs.Percent)
	}
}

// integrationsUrls: the Settings-editable fallback for SPOOLMAN_URL/MOONRAKER_URL/FLUIDD_URL. Empty means unset; a non-empty
// value gets the env-var rules from mustValidProxyURL, but as a validation error rather than log.Fatal.

func TestLoadConfigDefaultIntegrationsUrlsAreEmpty(t *testing.T) {
	cfg := loadTestConfig(t, t.TempDir())
	if cfg.IntegrationsUrls != (IntegrationsUrls{}) {
		t.Errorf("integrationsUrls default = %+v, want all empty", cfg.IntegrationsUrls)
	}
}

func TestLoadConfigMergesIntegrationsUrls(t *testing.T) {
	dir := t.TempDir()
	user := "integrationsUrls:\n  spoolmanUrl: http://spoolman.local:7912\n  moonrakerUrl: http://printer.local:7125\n  fluiddUrl: http://printer.local\n"
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(user), 0o644)
	cfg := loadTestConfig(t, dir)
	want := IntegrationsUrls{
		SpoolmanURL:  "http://spoolman.local:7912",
		MoonrakerURL: "http://printer.local:7125",
		FluiddURL:    "http://printer.local",
	}
	if cfg.IntegrationsUrls != want {
		t.Errorf("integrationsUrls = %+v, want %+v", cfg.IntegrationsUrls, want)
	}
}

func TestValidateRejectsBadIntegrationsUrls(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{"bad spoolmanUrl", "integrationsUrls:\n  spoolmanUrl: nas.lan:7912\n", "integrationsUrls.spoolmanUrl"},
		{"bad moonrakerUrl scheme", "integrationsUrls:\n  moonrakerUrl: ftp://printer.local\n", "integrationsUrls.moonrakerUrl"},
		{"bad fluiddUrl missing host", "integrationsUrls:\n  fluiddUrl: http://\n", "integrationsUrls.fluiddUrl"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(tc.yaml), 0o644)
			_, err := LoadConfig(dir)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

// TestValidateAcceptsEmptyIntegrationsUrls confirms an empty string (the
// "unset" value) never fails validation, unlike a genuinely malformed URL.
func TestValidateAcceptsEmptyIntegrationsUrls(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("integrationsUrls:\n  spoolmanUrl: \"\"\n"), 0o644)
	if _, err := LoadConfig(dir); err != nil {
		t.Fatalf("unexpected error with an empty integrationsUrls value: %v", err)
	}
}

// --- site ------------------------------------------------------------------

func TestLoadConfigSiteDefaults(t *testing.T) {
	cfg := loadTestConfig(t, t.TempDir())
	if want := (Site{ShopOnMainPort: true, StaffLink: true}); cfg.Site != want {
		t.Errorf("site default = %+v, want %+v", cfg.Site, want)
	}
}

// A user file may turn the booleans off: false must override the true defaults, not be lost in the merge.
func TestLoadConfigMergesSite(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("site:\n  shopOnMainPort: false\n  staffLink: false\n  adminUrl: https://admin.example.com/admin/\n"), 0o644)
	cfg := loadTestConfig(t, dir)
	want := Site{ShopOnMainPort: false, StaffLink: false, AdminURL: "https://admin.example.com/admin/"}
	if cfg.Site != want {
		t.Errorf("site = %+v, want %+v", cfg.Site, want)
	}
}

func TestValidateRejectsBadSiteAdminUrl(t *testing.T) {
	for _, bad := range []string{"admin.example.com/admin/", "ftp://admin.example.com", "https://", "/admin/"} {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("site:\n  adminUrl: \""+bad+"\"\n"), 0o644)
		_, err := LoadConfig(dir)
		if err == nil || !strings.Contains(err.Error(), "site.adminUrl") {
			t.Errorf("adminUrl %q: want error naming site.adminUrl, got %v", bad, err)
		}
	}
}

func TestLoadConfigRejectsUnknownSiteKey(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("site:\n  shopOnMainPrt: false\n"), 0o644)
	if _, err := LoadConfig(dir); err == nil {
		t.Fatal("a misspelled site key must fail strict decoding")
	}
}

// --- Hardware catalog ------------------------------------------------------

// Hardware has no "at least one" floor: the embedded defaults ship an empty catalog, served as a non-nil, non-null empty slice.
func TestLoadConfigDefaultsHaveNoHardware(t *testing.T) {
	cfg := loadTestConfig(t, t.TempDir())
	if cfg.Hardware == nil {
		t.Fatal("hardware = nil, want a non-nil empty slice (normalizeHardware)")
	}
	if len(cfg.Hardware) != 0 {
		t.Errorf("hardware = %+v, want empty by default", cfg.Hardware)
	}
	data, err := json.Marshal(cfg.Hardware)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != "[]" {
		t.Errorf("hardware serializes as %s, want [] (never null)", data)
	}
}

// TestLoadConfigMergesUserHardware confirms hardware follows the same
// list-REPLACE-wholesale rule as materials/printers, and that
// lostOnFailure/unitCost round-trip.
func TestLoadConfigMergesUserHardware(t *testing.T) {
	dir := t.TempDir()
	user := `
hardware:
  - { name: "6x2mm magnet", unitCost: 0.15, lostOnFailure: true }
  - { name: "earring hook", unitCost: 0.08 }
`
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(user), 0o644)
	cfg := loadTestConfig(t, dir)
	if len(cfg.Hardware) != 2 {
		t.Fatalf("hardware = %+v, want 2 entries (arrays replace wholesale)", cfg.Hardware)
	}
	if cfg.Hardware[0].Name != "6x2mm magnet" || cfg.Hardware[0].UnitCost != 0.15 || !cfg.Hardware[0].LostOnFailure {
		t.Errorf("hardware[0] = %+v, want {6x2mm magnet, 0.15, true}", cfg.Hardware[0])
	}
	// lostOnFailure omitted -> defaults false.
	if cfg.Hardware[1].Name != "earring hook" || cfg.Hardware[1].LostOnFailure {
		t.Errorf("hardware[1] = %+v, want lostOnFailure defaulting to false", cfg.Hardware[1])
	}
}

// TestValidateRejectsMissingOrDuplicateHardwareName mirrors the
// materials/printers id-uniqueness check, but on name (hardware has no id
// field of its own).
func TestValidateRejectsMissingOrDuplicateHardwareName(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{"missing name", "hardware:\n  - { name: \"\", unitCost: 1 }\n", "hardware[0]"},
		{"duplicate name", "hardware:\n  - { name: magnet, unitCost: 1 }\n  - { name: magnet, unitCost: 2 }\n", "hardware[1]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(tc.yaml), 0o644)
			_, err := LoadConfig(dir)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

// TestValidateRejectsNegativeHardwareUnitCost mirrors the other list
// entries' non-negative-cost checks.
func TestValidateRejectsNegativeHardwareUnitCost(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("hardware:\n  - { name: magnet, unitCost: -0.01 }\n"), 0o644)
	_, err := LoadConfig(dir)
	if err == nil || !strings.Contains(err.Error(), "hardware[0]") || !strings.Contains(err.Error(), "unitCost") {
		t.Fatalf("want hardware[0] unitCost error, got %v", err)
	}
}

// Mirrors TestValidateRejectsOversizedCollections for hardware, which has no "at least one" floor to complicate the fixture.
func TestValidateRejectsOversizedHardwareCollection(t *testing.T) {
	cfg := loadTestConfig(t, t.TempDir())
	for i := range maxConfigCollectionSize + 1 {
		cfg.Hardware = append(cfg.Hardware, Hardware{Name: fmt.Sprintf("extra-%d", i), UnitCost: 1})
	}
	problems := validate(cfg)
	want := fmt.Sprintf("hardware: %d entries exceeds max %d", len(cfg.Hardware), maxConfigCollectionSize)
	if !containsSubstr(problems, want) {
		t.Fatalf("problems = %v, want to contain %q", problems, want)
	}
}

// multiUser true is a problem only when auth is NOT enabled; this needs an env-derived fact validate() lacks.
func TestUsersMultiUserRequiresAuthProblem(t *testing.T) {
	cases := []struct {
		name        string
		multiUser   bool
		authEnabled bool
		wantProblem bool
	}{
		{"off, auth disabled", false, false, false},
		{"off, auth enabled", false, true, false},
		{"on, auth enabled", true, true, false},
		{"on, auth disabled", true, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := usersMultiUserRequiresAuthProblem(tc.multiUser, tc.authEnabled)
			if (got != "") != tc.wantProblem {
				t.Errorf("usersMultiUserRequiresAuthProblem(%v, %v) = %q, wantProblem = %v", tc.multiUser, tc.authEnabled, got, tc.wantProblem)
			}
		})
	}
}

// A config with no users section starts in single-user mode.
func TestDefaultConfigHasMultiUserOff(t *testing.T) {
	cfg := loadTestConfig(t, t.TempDir())
	if cfg.Users.MultiUser {
		t.Error("users.multiUser should default to false")
	}
}

// loadWithPricingYAML loads a config.yaml whose only override is the given `pricing:` block body.
func loadWithPricingYAML(t *testing.T, body string) (*Config, error) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("pricing:\n"+body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return LoadConfig(dir)
}

// pricingWarnsFor builds real PricingInputs from cfg via the public-quote input builder and reports whether ComputePricing
// produced the named warning, so the alignment assertion asks the ENGINE rather than a second copy of the bound.
func pricingWarnsFor(t *testing.T, cfg *Config, field, code string) (bool, *PricingResult) {
	t.Helper()
	material := cfg.Materials[0]
	for _, m := range cfg.Materials {
		if m.Name == "PLA" {
			material = m
		}
	}
	qi, opt := buildPublicQuoteInputs(cfg, material, 1, 50, 3600)
	bd := ComputeQuote(qi, opt)
	if bd.Pricing == nil {
		t.Fatal("no pricing result")
	}
	return hasWarning(bd.Pricing.Warnings, field, code), bd.Pricing
}

// markupMultiplier twin of the marginPct test: it once had no ceiling at any layer, so 1e6 priced a $14.51 part at $14.5 M and
// 1e15 at quantity 1000 wrapped revenue negative on a Valid quote. No value passing validation may produce an above-max
// markupMultiplier warning, and the rejection must name the field and bound.
func TestMarkupMultiplierCeilingIsAlignedAcrossLayers(t *testing.T) {
	// Above the ceiling is rejected at config load, naming field and bound.
	if _, err := loadWithPricingYAML(t, "  strategy: markup\n  markupMultiplier: 1000.1\n"); err == nil {
		t.Fatal("markupMultiplier 1000.1 loaded clean; want a config validation error")
	} else {
		for _, want := range []string{"pricing.markupMultiplier", "1000"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not mention %q", err.Error(), want)
			}
		}
	}

	// Anything config accepts must actually price. maxMarkupMultiplier is
	// inclusive on both sides of the alignment, so it is the interesting one.
	for _, mult := range []string{"0", "2.5", "1000"} {
		cfg, err := loadWithPricingYAML(t, "  strategy: markup\n  markupMultiplier: "+mult+"\n")
		if err != nil {
			t.Fatalf("markupMultiplier %s: config rejected a value it should accept: %v", mult, err)
		}
		warned, p := pricingWarnsFor(t, cfg, "markupMultiplier", "above-max")
		if warned {
			t.Errorf("markupMultiplier %s: config accepted a value ComputePricing then rejects as above-max -- the two ceilings have diverged", mult)
		}
		if !p.Valid {
			t.Errorf("markupMultiplier %s: pricing valid = false, want true; warnings = %+v", mult, p.Warnings)
		}
		if p.RecommendedPrice < 0 || p.Revenue < 0 {
			t.Errorf("markupMultiplier %s: recommendedPrice/revenue = %d/%d, neither may be negative", mult, p.RecommendedPrice, p.Revenue)
		}
	}
}

// Pins pricing.taxRatePct's [0,100] range at config load against ComputePricing's rule. Unchecked, a negative rate
// saved from Settings shaved every price (the tax term is price-inclusive) while ComputePricing rejected the same value.
func TestTaxRatePctBoundsAreAlignedAcrossLayers(t *testing.T) {
	for _, bad := range []string{"-5", "-0.1", "100.1"} {
		if _, err := loadWithPricingYAML(t, "  taxRatePct: "+bad+"\n"); err == nil {
			t.Errorf("taxRatePct %s loaded clean; want a config validation error", bad)
		} else if !strings.Contains(err.Error(), "pricing.taxRatePct") {
			t.Errorf("taxRatePct %s: error %q does not name the field", bad, err.Error())
		}
	}

	for _, ok := range []string{"0", "20", "100"} {
		cfg, err := loadWithPricingYAML(t, "  taxRatePct: "+ok+"\n")
		if err != nil {
			t.Fatalf("taxRatePct %s: config rejected a value it should accept: %v", ok, err)
		}
		for _, code := range []string{"below-min", "above-max"} {
			if warned, _ := pricingWarnsFor(t, cfg, "taxRatePct", code); warned {
				t.Errorf("taxRatePct %s: config accepted a value ComputePricing rejects as %s", ok, code)
			}
		}
	}
}

func TestValidatePayPalMeAndCurrencyCode(t *testing.T) {
	load := func(yaml string) error {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(yaml), 0o644)
		_, err := LoadConfig(dir)
		return err
	}
	for _, tc := range []struct {
		name, yaml, wantErr string
	}{
		{"valid handle and code", "invoicing:\n  paypalMe: yourshop\ncurrency:\n  code: USD\n", ""},
		{"empty is allowed", "invoicing:\n  paypalMe: \"\"\ncurrency:\n  code: \"\"\n", ""},
		{"handle is a URL", "invoicing:\n  paypalMe: \"https://paypal.me/yourshop\"\n", "invoicing.paypalMe"},
		{"handle too long", "invoicing:\n  paypalMe: " + strings.Repeat("a", 21) + "\n", "invoicing.paypalMe"},
		{"handle has a slash", "invoicing:\n  paypalMe: \"your/shop\"\n", "invoicing.paypalMe"},
		{"code lowercase", "currency:\n  code: usd\n", "currency.code"},
		{"code too long", "currency:\n  code: USDX\n", "currency.code"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := load(tc.yaml)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error mentioning %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func loadWithConfigYAML(t *testing.T, yaml string) (*Config, error) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	return LoadConfig(dir)
}

// timezone defaults to UTC, accepts IANA names (resolved from the embedded tzdata), and rejects anything else,
// including "Local", which would follow the host's TZ.
func TestTimezoneConfig(t *testing.T) {
	cfg, err := LoadConfig(t.TempDir())
	if err != nil || cfg.Timezone != "UTC" || cfg.Location().String() != "UTC" {
		t.Fatalf("default timezone = %q (%v), want UTC", cfg.Timezone, err)
	}
	cfg, err = loadWithConfigYAML(t, "timezone: America/Los_Angeles\n")
	if err != nil {
		t.Fatalf("America/Los_Angeles rejected: %v", err)
	}
	if got := cfg.Location().String(); got != "America/Los_Angeles" {
		t.Errorf("Location = %q", got)
	}
	cfg, err = loadWithConfigYAML(t, "timezone: \"\"\n")
	if err != nil || cfg.Timezone != "UTC" {
		t.Errorf("empty timezone = %q (%v), want normalized to UTC", cfg.Timezone, err)
	}
	for _, bad := range []string{"Mars/Phobos", "Local", "not a zone"} {
		if _, err := loadWithConfigYAML(t, fmt.Sprintf("timezone: %q\n", bad)); err == nil || !strings.Contains(err.Error(), "timezone") {
			t.Errorf("timezone %q: err = %v, want a timezone validation error", bad, err)
		}
	}
}

func TestDefaultShippingChargeConfig(t *testing.T) {
	cfg, err := LoadConfig(t.TempDir())
	if err != nil || cfg.Pricing.DefaultShippingCharge != 0 {
		t.Fatalf("default charge = %v (%v), want 0", cfg.Pricing.DefaultShippingCharge, err)
	}
	cfg, err = loadWithPricingYAML(t, "  defaultShippingCharge: 6.5\n")
	if err != nil || cfg.Pricing.DefaultShippingCharge != 6.5 {
		t.Fatalf("charge = %v (%v), want 6.5", cfg.Pricing.DefaultShippingCharge, err)
	}
	if _, err := loadWithPricingYAML(t, "  defaultShippingCharge: -1\n"); err == nil || !strings.Contains(err.Error(), "pricing.defaultShippingCharge") {
		t.Errorf("negative charge: err = %v, want a pricing.defaultShippingCharge error", err)
	}
}

// failureRatePct shares one ceiling (maxFailureRatePct) between the config defaults, every preset, and
// ComputeQuote; a value above it is rejected at load and the message names the field.
func TestValidateRejectsFailureRateAboveCeiling(t *testing.T) {
	for name, user := range map[string]string{
		"defaults": "defaults:\n  failureRatePct: 90.5\n",
		"preset":   "presets:\n  hobby: { failureRatePct: 95 }\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(user), 0o644)
			_, err := LoadConfig(dir)
			if err == nil || !strings.Contains(err.Error(), "failureRatePct") {
				t.Fatalf("want failureRatePct range error, got %v", err)
			}
		})
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("defaults:\n  failureRatePct: 90\n"), 0o644)
	if _, err := LoadConfig(dir); err != nil {
		t.Fatalf("failureRatePct 90 (inclusive ceiling) rejected: %v", err)
	}
}

// A failure rate outside 0-90 in defaults or a preset fails the load with the normal validation error.
func TestLoadConfigRejectsOutOfRangeFailureRates(t *testing.T) {
	dir := t.TempDir()
	user := "defaults:\n  failureRatePct: 95\npresets:\n  hobby:\n    failureRatePct: -5\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(user), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(dir)
	if err == nil {
		t.Fatal("LoadConfig accepted out-of-range failureRatePct")
	}
	for _, want := range []string{"defaults.failureRatePct", "presets.hobby.failureRatePct"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not name %q: %v", want, err)
		}
	}
}
