package main

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gopkg.in/yaml.v3"
)

//go:embed defaults.yaml
var defaultsYAML []byte

// ConfigStore holds the effective Config behind a concurrency-safe accessor, so settings changes apply
// without a restart. The installed Config is immutable: handleSettingsPut builds a fresh one via LoadConfig.
type ConfigStore struct {
	ptr atomic.Pointer[Config]
}

// NewConfigStore wraps cfg (typically LoadConfig's result) for concurrent use.
func NewConfigStore(cfg *Config) *ConfigStore {
	s := &ConfigStore{}
	s.Set(cfg)
	return s
}

// Get returns the current effective Config. Take one snapshot per request and reuse it, so a request sees
// one consistent Config even if a PUT swaps in a new one.
func (s *ConfigStore) Get() *Config {
	return s.ptr.Load()
}

// Set installs cfg as the new effective Config. The caller must not mutate cfg afterward.
func (s *ConfigStore) Set(cfg *Config) {
	s.ptr.Store(cfg)
}

type Currency struct {
	Symbol   string `yaml:"symbol" json:"symbol"`
	Decimals int    `yaml:"decimals" json:"decimals"`
	// Code is the ISO 4217 currency code (e.g. "USD"). Optional; used only for the PayPal.me payment link.
	Code string `yaml:"code" json:"code"`
}

type Pricing struct {
	Strategy            string  `yaml:"strategy" json:"strategy"`
	MarginPct           float64 `yaml:"marginPct" json:"marginPct"`
	MarkupMultiplier    float64 `yaml:"markupMultiplier" json:"markupMultiplier"`
	TaxRatePct          float64 `yaml:"taxRatePct" json:"taxRatePct"`
	DefaultShippingCost float64 `yaml:"defaultShippingCost" json:"defaultShippingCost"`
	// DefaultShippingCharge is what the public quote page bills the visitor for shipping, and the shipping
	// charge a new admin quote starts with. It is separate from DefaultShippingCost, the expense you pay.
	DefaultShippingCharge float64 `yaml:"defaultShippingCharge" json:"defaultShippingCharge"`
	DefaultFeeSchedule    string  `yaml:"defaultFeeSchedule" json:"defaultFeeSchedule"`
	LaborPerUnit          bool    `yaml:"laborPerUnit" json:"laborPerUnit"`
}

type FeeFixed struct {
	Amount float64 `yaml:"amount" json:"amount"`
	Label  string  `yaml:"label" json:"label"`
}

type FeePercent struct {
	Pct   float64 `yaml:"pct" json:"pct"`
	Base  string  `yaml:"base" json:"base"`
	Label string  `yaml:"label" json:"label"`
}

type FeeSchedule struct {
	Source  string       `yaml:"source,omitempty" json:"source,omitempty"`
	Fixed   []FeeFixed   `yaml:"fixed" json:"fixed"`
	Percent []FeePercent `yaml:"percent" json:"percent"`
}

// decodeNodeStrict re-serializes a YAML node and decodes it with KnownFields enforcement: Node.Decode has no
// strict mode, so this keeps unknown-field rejection in FeeScheduleMap and PresetMap's UnmarshalYAML.
func decodeNodeStrict(node *yaml.Node, out any) error {
	data, err := yaml.Marshal(node)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// unmarshalMergeMap deep-merges by key for FeeScheduleMap and PresetMap: a mounted entry overlays only the
// fields it sets onto the existing one. label names the field in error messages.
func unmarshalMergeMap[T any](m *map[string]T, label string, value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: %s must be a mapping", value.Line, label)
	}
	if *m == nil {
		*m = map[string]T{}
	}
	for i := 0; i+1 < len(value.Content); i += 2 {
		var key string
		if err := value.Content[i].Decode(&key); err != nil {
			return err
		}
		base := (*m)[key]
		if err := decodeNodeStrict(value.Content[i+1], &base); err != nil {
			return fmt.Errorf("%s.%s: %w", label, key, err)
		}
		(*m)[key] = base
	}
	return nil
}

// FeeScheduleMap deep-merges by key; see unmarshalMergeMap.
type FeeScheduleMap map[string]FeeSchedule

func (m *FeeScheduleMap) UnmarshalYAML(value *yaml.Node) error {
	return unmarshalMergeMap((*map[string]FeeSchedule)(m), "feeSchedules", value)
}

// PresetMap deep-merges by key; see unmarshalMergeMap.
type PresetMap map[string]Preset

func (m *PresetMap) UnmarshalYAML(value *yaml.Node) error {
	return unmarshalMergeMap((*map[string]Preset)(m), "presets", value)
}

type Material struct {
	ID         string  `yaml:"id" json:"id"`
	Name       string  `yaml:"name" json:"name"`
	Density    float64 `yaml:"density" json:"density"`
	PricePerKg float64 `yaml:"pricePerKg" json:"pricePerKg"`
	WastePct   float64 `yaml:"wastePct" json:"wastePct"`
	// Public opts this material into the public quote page. Default false: nothing is exposed until opted in.
	Public bool `yaml:"public" json:"public"`
}

type Printer struct {
	ID                 string  `yaml:"id" json:"id"`
	Name               string  `yaml:"name" json:"name"`
	Watts              float64 `yaml:"watts" json:"watts"`
	PurchaseCost       float64 `yaml:"purchaseCost" json:"purchaseCost"`
	LifespanHours      float64 `yaml:"lifespanHours" json:"lifespanHours"`
	FilamentDiameterMm float64 `yaml:"filamentDiameterMm" json:"filamentDiameterMm"`
}

// Hardware is a reusable catalog entry for a physical add-on (magnet, insert, ...) attached to a quote as a
// per-unit cost line. It has no id: name is its only identity, required and unique (see validate()).
// An empty catalog is normal, so there is no at-least-one floor.
type Hardware struct {
	Name     string  `yaml:"name" json:"name"`
	UnitCost float64 `yaml:"unitCost" json:"unitCost"`
	// LostOnFailure marks a piece consumed during printing (e.g. a magnet placed mid-print), so a failed
	// print wastes it and its cost joins the failure-rate buffer. Default false: later-attached hardware
	// isn't wasted.
	LostOnFailure bool `yaml:"lostOnFailure" json:"lostOnFailure"`
}

type Tou struct {
	PeakRate           float64 `yaml:"peakRate" json:"peakRate"`
	OffPeakRate        float64 `yaml:"offPeakRate" json:"offPeakRate"`
	DefaultOffPeakFrac float64 `yaml:"defaultOffPeakFrac" json:"defaultOffPeakFrac"`
}

type Energy struct {
	Mode     string  `yaml:"mode" json:"mode"`
	FlatRate float64 `yaml:"flatRate" json:"flatRate"`
	Tou      Tou     `yaml:"tou" json:"tou"`
	// Schedule is the time-of-use tariff used when Mode is "schedule" (energy_schedule.go).
	Schedule EnergySchedule `yaml:"schedule" json:"schedule"`
}

type Preset struct {
	FailureRatePct float64 `yaml:"failureRatePct" json:"failureRatePct"`
	PrepMinutes    float64 `yaml:"prepMinutes" json:"prepMinutes"`
	LaborRate      float64 `yaml:"laborRate" json:"laborRate"`
	MonthlyFixed   float64 `yaml:"monthlyFixed" json:"monthlyFixed"`
	MonthlyHours   float64 `yaml:"monthlyHours" json:"monthlyHours"`
}

type Defaults struct {
	Preset         string  `yaml:"preset" json:"preset"`
	Printer        string  `yaml:"printer" json:"printer"`
	Material       string  `yaml:"material" json:"material"`
	FailureRatePct float64 `yaml:"failureRatePct" json:"failureRatePct"`
	Quantity       int     `yaml:"quantity" json:"quantity"`
}

type QuoteBranding struct {
	BusinessName        string `yaml:"businessName" json:"businessName"`
	LogoURL             string `yaml:"logoUrl" json:"logoUrl"`
	Address             string `yaml:"address" json:"address"`
	Email               string `yaml:"email" json:"email"`
	Phone               string `yaml:"phone" json:"phone"`
	TaxID               string `yaml:"taxId" json:"taxId"`
	PaymentInstructions string `yaml:"paymentInstructions" json:"paymentInstructions"`
	TermsText           string `yaml:"termsText" json:"termsText"`
	ValidDays           int    `yaml:"validDays" json:"validDays"`
	// ReferencePrefix is the prefix of the short order reference customers see ("HL-1001"). Blank derives it
	// from the business name's initials (reference.go).
	ReferencePrefix string `yaml:"referencePrefix" json:"referencePrefix"`
}

// Invoicing holds invoice numbering, default terms, and the payment method list.
// salesTaxPct is a float in YAML/JSON; the API boundary converts it once to basis points and all stored or
// computed values are basis points. Each payment stores method as free text, so editing the list is safe.
type Invoicing struct {
	NumberPrefix   string   `yaml:"numberPrefix" json:"numberPrefix"`
	SalesTaxPct    float64  `yaml:"salesTaxPct" json:"salesTaxPct"`
	DueDays        int      `yaml:"dueDays" json:"dueDays"`
	PaymentMethods []string `yaml:"paymentMethods" json:"paymentMethods"`
	// PayPalMe is a bare PayPal.me handle (no URL). When set, invoices show a pay link and QR code.
	PayPalMe string `yaml:"paypalMe" json:"paypalMe"`
}

// PublicQuotes configures the public quote page: the owner printer and preset every visitor estimate uses,
// plus optional intro text. The visitor never chooses either; validate() checks they exist.
type PublicQuotes struct {
	Printer   string `yaml:"printer" json:"printer"`
	Preset    string `yaml:"preset" json:"preset"`
	IntroText string `yaml:"introText" json:"introText"`
}

// Site controls what the main port (PORT) serves at its root and what the shop footer links to. The admin
// app always lives under /admin/. A change applies without a restart, like the rest of the config.
type Site struct {
	// ShopOnMainPort serves the shop (/ and /order) and /api/public/* on the main port. When
	// false, / redirects to /admin/ and the public routes 404 there. PUBLIC_PORT is unaffected.
	ShopOnMainPort bool `yaml:"shopOnMainPort" json:"shopOnMainPort"`
	// StaffLink shows the Staff sign-in link in the footer of the shop on the main port.
	StaffLink bool `yaml:"staffLink" json:"staffLink"`
	// AdminURL is the absolute http(s) URL of the admin app, the Staff sign-in target on the PUBLIC_PORT
	// shop footer. Empty hides the link there.
	AdminURL string `yaml:"adminUrl" json:"adminUrl"`
}

// Smtp configures outgoing email; this struct stores and validates config only. The password comes solely
// from the SMTP_PASSWORD env var, keeping secrets out of config.yaml. RequireTLS makes a server without
// STARTTLS fail the send instead of falling back to plaintext; default false.
type Smtp struct {
	Host       string `yaml:"host" json:"host"`
	Port       int    `yaml:"port" json:"port"`
	Username   string `yaml:"username" json:"username"`
	From       string `yaml:"from" json:"from"`
	To         string `yaml:"to" json:"to"`
	RequireTLS bool   `yaml:"requireTLS" json:"requireTLS"`
}

// UI holds the Settings "Features" section: which nav tabs are hidden. Decluttering, never access control.
type UI struct {
	DisabledTabs []string `yaml:"disabledTabs" json:"disabledTabs"`
}

// IntegrationsUrls is the Settings alternative to the SPOOLMAN_URL/MOONRAKER_URL/FLUIDD_URL env vars (the env
// var wins when set; empty means unset). Unlike other sections a change needs a restart: proxy targets are
// resolved once at boot. The store is still hot-swapped; the web UI tells the operator to restart.
type IntegrationsUrls struct {
	SpoolmanURL  string `yaml:"spoolmanUrl" json:"spoolmanUrl"`
	MoonrakerURL string `yaml:"moonrakerUrl" json:"moonrakerUrl"`
	FluiddURL    string `yaml:"fluiddUrl" json:"fluiddUrl"`
}

// validTabIDs are the only entries validate() accepts in ui.disabledTabs: the ten nav-visible tabs.
// "quote" and "settings" are absent because App.svelte always renders them, so listing either is an error.
var validTabIDs = map[string]bool{
	"quotes":      true,
	"requests":    true,
	"invoices":    true,
	"items":       true,
	"clients":     true,
	"dashboard":   true,
	"filament":    true,
	"electricity": true,
	"printers":    true,
	"guide":       true,
}

// Users holds the Settings "Users" section: whether the multi-employee permission system is active.
// Accounts, hashes, and grants live in the users table, never here (Settings round-trips this file).
// Default false. Enabling needs ADMIN_PASSWORD (usersMultiUserRequiresAuthProblem; validate() can't see env).
type Users struct {
	MultiUser bool `yaml:"multiUser" json:"multiUser"`
	// AccessAdminEmail is the Cloudflare Access email that signs in as admin in multi-user mode;
	// normalizeConfig lowercases it. Ignored in single-user mode, where every Access-verified request is the
	// admin.
	AccessAdminEmail string `yaml:"accessAdminEmail" json:"accessAdminEmail"`
}

type Config struct {
	// Timezone is an IANA time zone name ("UTC", "America/Los_Angeles"). Dashboard month and year windows,
	// overdue checks, and auto-dated payments use it; stored timestamps stay UTC.
	Timezone     string         `yaml:"timezone" json:"timezone"`
	Currency     Currency       `yaml:"currency" json:"currency"`
	Pricing      Pricing        `yaml:"pricing" json:"pricing"`
	FeeSchedules FeeScheduleMap `yaml:"feeSchedules" json:"feeSchedules"`
	Materials    []Material     `yaml:"materials" json:"materials"` // arrays replace wholesale
	Printers     []Printer      `yaml:"printers" json:"printers"`
	Hardware     []Hardware     `yaml:"hardware" json:"hardware"` // arrays replace wholesale; empty is normal
	Energy       Energy         `yaml:"energy" json:"energy"`
	Presets      PresetMap      `yaml:"presets" json:"presets"`
	Defaults     Defaults       `yaml:"defaults" json:"defaults"`
	Quote        QuoteBranding  `yaml:"quote" json:"quote"`
	Invoicing    Invoicing      `yaml:"invoicing" json:"invoicing"`
	PublicQuotes PublicQuotes   `yaml:"publicQuotes" json:"publicQuotes"`
	Users        Users          `yaml:"users" json:"users"`
	Site         Site           `yaml:"site" json:"site"`
	// CustomCss is operator-authored CSS served verbatim, unsanitized: same trust as every free-text field
	// here, so only the byte cap applies. GET /custom.css styles the public page; the admin listener links
	// it with media="print" only, for printed documents.
	CustomCss        string           `yaml:"customCss" json:"customCss"`
	Smtp             Smtp             `yaml:"smtp" json:"smtp"`
	UI               UI               `yaml:"ui" json:"ui"`
	IntegrationsUrls IntegrationsUrls `yaml:"integrationsUrls" json:"integrationsUrls"`
}

// timezoneCache memoizes loaded locations: time.LoadLocation reads the zoneinfo database on every call.
var timezoneCache sync.Map // string -> *time.Location

// loadTimezone resolves an IANA zone name. "Local" is rejected (it would follow the server's TZ, which the
// scratch image doesn't have), and the empty name is UTC.
func loadTimezone(name string) (*time.Location, error) {
	if name == "" {
		return time.UTC, nil
	}
	if name == "Local" {
		return nil, errors.New("Local is not allowed")
	}
	if loc, ok := timezoneCache.Load(name); ok {
		return loc.(*time.Location), nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, err
	}
	timezoneCache.Store(name, loc)
	return loc, nil
}

// Location returns the configured time zone, or UTC if the name is unusable (validate rejects those).
func (c *Config) Location() *time.Location {
	loc, err := loadTimezone(c.Timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// now returns the current time in the configured zone.
func (c *Config) now() time.Time {
	return time.Now().In(c.Location())
}

func decodeStrict(data []byte, cfg *Config) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		if errors.Is(err, io.EOF) {
			// Empty, comments-only, or blank-lines-only document: no overrides.
			return nil
		}
		return err
	}
	// A well-formed single document must EOF on the next read; anything else is a second "---" document.
	var extra yaml.Node
	if err := dec.Decode(&extra); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	return errors.New("multiple YAML documents not supported")
}

// LoadConfig reads the embedded defaults, overlays config.yaml from configDir, normalizes, and validates.
func LoadConfig(configDir string) (*Config, error) {
	cfg := &Config{}
	if err := decodeStrict(defaultsYAML, cfg); err != nil {
		return nil, fmt.Errorf("embedded defaults are invalid (bug): %w", err)
	}
	userPath := filepath.Join(configDir, "config.yaml")
	if data, err := os.ReadFile(userPath); err == nil {
		if err := decodeStrict(data, cfg); err != nil {
			return nil, fmt.Errorf("%s: %w", userPath, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("reading %s: %w", userPath, err)
	}
	normalizeConfig(cfg)
	if problems := validate(cfg); len(problems) > 0 {
		return nil, fmt.Errorf("invalid configuration:\n  %s", strings.Join(problems, "\n  "))
	}
	return cfg, nil
}

// normalizeConfig guarantees a Config's slices are never nil before validate() or serialization.
// Every path that builds or accepts a whole Config must call it: a nil DisabledTabs would make pruneEqual
// keep a spurious "ui:\n  disabledTabs: null" override in config.yaml.
func normalizeConfig(c *Config) {
	c.Timezone = strings.TrimSpace(c.Timezone)
	if c.Timezone == "" {
		c.Timezone = "UTC"
	}
	normalizeFeeSchedules(c)
	normalizeUI(c)
	normalizeHardware(c)
	normalizeEnergySchedule(&c.Energy.Schedule)
	c.Users.AccessAdminEmail = foldEmail(c.Users.AccessAdminEmail)
}

// normalizeFeeSchedules makes Fixed/Percent non-nil so /api/config never serves "fixed":null.
func normalizeFeeSchedules(c *Config) {
	for key, fs := range c.FeeSchedules {
		changed := false
		if fs.Fixed == nil {
			fs.Fixed = []FeeFixed{}
			changed = true
		}
		if fs.Percent == nil {
			fs.Percent = []FeePercent{}
			changed = true
		}
		if changed {
			c.FeeSchedules[key] = fs
		}
	}
}

// normalizeUI makes ui.disabledTabs non-nil, so `disabledTabs: null` never reaches /api/config as JSON null.
func normalizeUI(c *Config) {
	if c.UI.DisabledTabs == nil {
		c.UI.DisabledTabs = []string{}
	}
}

// normalizeHardware makes c.Hardware non-nil: an unset `hardware:` key must serialize as [] on /api/config.
func normalizeHardware(c *Config) {
	if c.Hardware == nil {
		c.Hardware = []Hardware{}
	}
}

// maxConfigCollectionSize bounds materials, printers, feeSchedules, and presets, which validate() otherwise
// leaves unbounded: endless valid entries would be re-parsed on every start (a self-inflicted DoS).
// 200 is far beyond a real catalog.
const maxConfigCollectionSize = 200

// publicIntroTextMaxBytes bounds publicQuotes.introText in bytes (len()), not characters, so the web counter
// (PublicQuotesSettings.svelte, keep in sync) and the 422 agree for multi-byte scripts and emoji.
const publicIntroTextMaxBytes = 500

// customCssMaxBytes bounds the top-level customCss field (32 KiB); mirrored by CUSTOM_CSS_MAX_BYTES in
// settingsRules.ts. The content is unsanitized, so this is the one bound on what config.yaml holds.
const customCssMaxBytes = 32 << 10

// quotePaymentInstructionsMaxBytes and quoteTermsTextMaxBytes bound quote.paymentInstructions and
// quote.termsText in bytes. Both land in every invoice and quote email body (mail_templates.go), so the cap
// is well above real text but short of what an SMTP relay might reject. Mirrored by QuoteSettings.svelte.
const quotePaymentInstructionsMaxBytes = 2000
const quoteTermsTextMaxBytes = 2000

// paymentMethodMaxBytes bounds each invoicing.paymentMethods entry in bytes after trimming.
const paymentMethodMaxBytes = 50

func validate(c *Config) []string {
	var p []string
	if len(c.Materials) > maxConfigCollectionSize {
		p = append(p, fmt.Sprintf("materials: %d entries exceeds max %d", len(c.Materials), maxConfigCollectionSize))
	}
	if len(c.Printers) > maxConfigCollectionSize {
		p = append(p, fmt.Sprintf("printers: %d entries exceeds max %d", len(c.Printers), maxConfigCollectionSize))
	}
	if len(c.FeeSchedules) > maxConfigCollectionSize {
		p = append(p, fmt.Sprintf("feeSchedules: %d entries exceeds max %d", len(c.FeeSchedules), maxConfigCollectionSize))
	}
	if len(c.Presets) > maxConfigCollectionSize {
		p = append(p, fmt.Sprintf("presets: %d entries exceeds max %d", len(c.Presets), maxConfigCollectionSize))
	}
	if len(c.Hardware) > maxConfigCollectionSize {
		p = append(p, fmt.Sprintf("hardware: %d entries exceeds max %d", len(c.Hardware), maxConfigCollectionSize))
	}
	if c.Currency.Decimals < 0 || c.Currency.Decimals > 4 {
		p = append(p, "currency.decimals: must be 0-4")
	}
	if c.Currency.Code != "" && !currencyCodeRe.MatchString(c.Currency.Code) {
		p = append(p, "currency.code: must be 3 uppercase letters (ISO 4217) or empty")
	}
	if c.Pricing.Strategy != "margin" && c.Pricing.Strategy != "markup" {
		p = append(p, "pricing.strategy: must be margin or markup")
	}
	// The ceiling is maxMarginPct (pricing.go): this check and ComputePricing's marginPct rule must accept
	// the same set, or a config can validate clean and then make every quote unpriceable (config_test.go
	// pins it).
	if c.Pricing.MarginPct < 0 || c.Pricing.MarginPct > maxMarginPct {
		p = append(p, fmt.Sprintf("pricing.marginPct: %v out of range [0,%v]", c.Pricing.MarginPct, maxMarginPct))
	}
	// Same one-ceiling rule as marginPct: maxMarkupMultiplier (pricing.go) is ComputePricing's bound.
	// Unbounded, a configured 1e15 wrapped revenue negative on a quote marked valid.
	if c.Pricing.MarkupMultiplier < 0 || c.Pricing.MarkupMultiplier > maxMarkupMultiplier {
		p = append(p, fmt.Sprintf("pricing.markupMultiplier: %v out of range [0,%v]", c.Pricing.MarkupMultiplier, maxMarkupMultiplier))
	}
	// taxRatePct needs a range check: the tax term is price-inclusive, so a negative rate silently shaves the
	// tax line on every quote. maxPercentAmount mirrors ComputePricing's ceiling.
	if c.Pricing.TaxRatePct < 0 || c.Pricing.TaxRatePct > maxPercentAmount {
		p = append(p, fmt.Sprintf("pricing.taxRatePct: %v out of range [0,%v]", c.Pricing.TaxRatePct, float64(maxPercentAmount)))
	}
	if c.Pricing.DefaultShippingCharge < 0 || c.Pricing.DefaultShippingCharge > maxMoneyAmount {
		p = append(p, fmt.Sprintf("pricing.defaultShippingCharge: %v out of range [0,%v]", c.Pricing.DefaultShippingCharge, float64(maxMoneyAmount)))
	}
	if _, err := loadTimezone(c.Timezone); err != nil {
		p = append(p, fmt.Sprintf("timezone: %q is not an IANA time zone name such as UTC or America/New_York", c.Timezone))
	}
	// failureRatePct shares maxFailureRatePct (pricing.go) with ComputeQuote: the buffer is base x p / (1 - p),
	// so a rate above the ceiling would pass config validation and then blank every quote that uses it.
	if c.Defaults.FailureRatePct < 0 || c.Defaults.FailureRatePct > maxFailureRatePct {
		p = append(p, fmt.Sprintf("defaults.failureRatePct: %v out of range [0,%v]", c.Defaults.FailureRatePct, float64(maxFailureRatePct)))
	}
	for _, key := range slices.Sorted(maps.Keys(c.Presets)) {
		if r := c.Presets[key].FailureRatePct; r < 0 || r > maxFailureRatePct {
			p = append(p, fmt.Sprintf("presets.%s.failureRatePct: %v out of range [0,%v]", key, r, float64(maxFailureRatePct)))
		}
	}
	if c.Pricing.DefaultFeeSchedule != "none" {
		if _, ok := c.FeeSchedules[c.Pricing.DefaultFeeSchedule]; !ok {
			p = append(p, fmt.Sprintf("pricing.defaultFeeSchedule: %q not in feeSchedules", c.Pricing.DefaultFeeSchedule))
		}
	}
	for key, fs := range c.FeeSchedules {
		for i, pf := range fs.Percent {
			if pf.Base != "revenue" {
				p = append(p, fmt.Sprintf("feeSchedules.%s.percent[%d].base: must be \"revenue\", got %q", key, i, pf.Base))
			}
		}
	}
	if len(c.Materials) == 0 {
		p = append(p, "materials: at least one material required")
	}
	seen := map[string]bool{}
	for i, m := range c.Materials {
		if m.ID == "" || seen[m.ID] {
			p = append(p, fmt.Sprintf("materials[%d]: missing or duplicate id %q", i, m.ID))
		}
		seen[m.ID] = true
		if m.Density <= 0 {
			p = append(p, fmt.Sprintf("materials[%d] (%s): density must be > 0", i, m.ID))
		}
		if m.WastePct < 0 || m.WastePct > 100 {
			p = append(p, fmt.Sprintf("materials[%d] (%s): wastePct must be 0-100", i, m.ID))
		}
	}
	if len(c.Printers) == 0 {
		p = append(p, "printers: at least one printer required")
	}
	seenP := map[string]bool{}
	for i, pr := range c.Printers {
		if pr.ID == "" || seenP[pr.ID] {
			p = append(p, fmt.Sprintf("printers[%d]: missing or duplicate id %q", i, pr.ID))
		}
		seenP[pr.ID] = true
		if pr.FilamentDiameterMm <= 0 {
			p = append(p, fmt.Sprintf("printers[%d] (%s): filamentDiameterMm must be > 0", i, pr.ID))
		}
	}
	// hardware has no at-least-one floor (an empty catalog is normal); name, the only identity, must be
	// unique.
	seenHw := map[string]bool{}
	for i, hw := range c.Hardware {
		if hw.Name == "" || seenHw[hw.Name] {
			p = append(p, fmt.Sprintf("hardware[%d]: missing or duplicate name %q", i, hw.Name))
		}
		seenHw[hw.Name] = true
		if hw.UnitCost < 0 {
			p = append(p, fmt.Sprintf("hardware[%d] (%s): unitCost must be >= 0", i, hw.Name))
		}
	}
	if c.Energy.Mode != "flat" && c.Energy.Mode != "tou" && c.Energy.Mode != "schedule" {
		p = append(p, "energy.mode: must be flat, tou, or schedule")
	}
	p = append(p, validateEnergySchedule(c.Energy.Schedule, c.Energy.Mode == "schedule")...)
	if !seen[c.Defaults.Material] {
		p = append(p, fmt.Sprintf("defaults.material: %q not in materials", c.Defaults.Material))
	}
	if !seenP[c.Defaults.Printer] {
		p = append(p, fmt.Sprintf("defaults.printer: %q not in printers", c.Defaults.Printer))
	}
	if _, ok := c.Presets[c.Defaults.Preset]; !ok {
		p = append(p, fmt.Sprintf("defaults.preset: %q not in presets", c.Defaults.Preset))
	}
	if c.Defaults.Quantity < 1 {
		p = append(p, "defaults.quantity: must be ≥ 1")
	}
	if c.Quote.ReferencePrefix != "" && !isAlnumPrefix(c.Quote.ReferencePrefix) {
		p = append(p, "quote.referencePrefix: must be 1-10 letters/digits, or blank to use your business initials")
	}
	if !isAlnumPrefix(c.Invoicing.NumberPrefix) {
		p = append(p, "invoicing.numberPrefix: must be 1-10 letters/digits")
	}
	if c.Invoicing.SalesTaxPct < 0 || c.Invoicing.SalesTaxPct > 30 {
		p = append(p, fmt.Sprintf("invoicing.salesTaxPct: %v out of range [0,30]", c.Invoicing.SalesTaxPct))
	}
	if c.Invoicing.DueDays < 0 || c.Invoicing.DueDays > 365 {
		p = append(p, fmt.Sprintf("invoicing.dueDays: %v out of range [0,365]", c.Invoicing.DueDays))
	}
	p = append(p, validatePaymentMethods(c.Invoicing.PaymentMethods)...)
	if c.Invoicing.PayPalMe != "" && !paypalHandleRe.MatchString(c.Invoicing.PayPalMe) {
		p = append(p, "invoicing.paypalMe: must be 1-20 letters/digits (the PayPal.me handle, not the URL) or empty")
	}
	if !seenP[c.PublicQuotes.Printer] {
		p = append(p, fmt.Sprintf("publicQuotes.printer: %q not in printers", c.PublicQuotes.Printer))
	}
	if _, ok := c.Presets[c.PublicQuotes.Preset]; !ok {
		p = append(p, fmt.Sprintf("publicQuotes.preset: %q not in presets", c.PublicQuotes.Preset))
	}
	if len(c.PublicQuotes.IntroText) > publicIntroTextMaxBytes {
		p = append(p, fmt.Sprintf("publicQuotes.introText: max %d bytes", publicIntroTextMaxBytes))
	}
	if len(c.CustomCss) > customCssMaxBytes {
		p = append(p, fmt.Sprintf("customCss: max %d bytes", customCssMaxBytes))
	}
	// Both fields land in every invoice and quote email body (mail_templates.go): cap well above real text
	// but short of what an SMTP relay might reject.
	if len(c.Quote.PaymentInstructions) > quotePaymentInstructionsMaxBytes {
		p = append(p, fmt.Sprintf("quote.paymentInstructions: max %d bytes", quotePaymentInstructionsMaxBytes))
	}
	if len(c.Quote.TermsText) > quoteTermsTextMaxBytes {
		p = append(p, fmt.Sprintf("quote.termsText: max %d bytes", quoteTermsTextMaxBytes))
	}
	if c.Smtp.Host != "" {
		if c.Smtp.Port < 1 || c.Smtp.Port > 65535 {
			p = append(p, fmt.Sprintf("smtp.port: %v out of range [1,65535]", c.Smtp.Port))
		}
		if !strings.Contains(c.Smtp.From, "@") {
			p = append(p, "smtp.from: must contain @")
		}
		if !strings.Contains(c.Smtp.To, "@") {
			p = append(p, "smtp.to: must contain @")
		}
	}
	if _, problem := normalizeEmail("users.accessAdminEmail", c.Users.AccessAdminEmail); problem != "" {
		p = append(p, problem)
	}
	p = append(p, validateDisabledTabs(c.UI.DisabledTabs)...)
	p = append(p, validateIntegrationsUrls(c.IntegrationsUrls)...)
	p = append(p, validateSite(c.Site)...)
	return p
}

// validateIntegrationsUrls applies mustValidProxyURL's startup rules (http/https scheme, non-empty host) via
// the non-fatal validProxyURL, so a bad Settings value is an ordinary 4xx, not a crash. Empty means unset.
func validateIntegrationsUrls(u IntegrationsUrls) []string {
	var p []string
	fields := []struct {
		path string
		val  string
	}{
		{"integrationsUrls.spoolmanUrl", u.SpoolmanURL},
		{"integrationsUrls.moonrakerUrl", u.MoonrakerURL},
		{"integrationsUrls.fluiddUrl", u.FluiddURL},
	}
	for _, f := range fields {
		if f.val == "" {
			continue
		}
		if _, err := validProxyURL(f.path, f.val); err != nil {
			// validProxyURL's error already leads with f.path (the envVar argument); errorSection (web
			// settings.ts) needs only that leading dotted token.
			p = append(p, err.Error())
		}
	}
	return p
}

// validateSite applies the integration URLs' rules (http/https scheme, non-empty host) to site.adminUrl.
// Empty means unset.
func validateSite(site Site) []string {
	if site.AdminURL == "" {
		return nil
	}
	if _, err := validProxyURL("site.adminUrl", site.AdminURL); err != nil {
		return []string{err.Error()}
	}
	return nil
}

// validateDisabledTabs enforces the validTabIDs vocabulary and rejects duplicates; "quote", "settings" fail.
func validateDisabledTabs(ids []string) []string {
	var p []string
	seen := map[string]bool{}
	for i, id := range ids {
		if !validTabIDs[id] {
			p = append(p, fmt.Sprintf("ui.disabledTabs[%d]: %q is not a valid tab id", i, id))
			continue
		}
		if seen[id] {
			p = append(p, fmt.Sprintf("ui.disabledTabs[%d]: duplicate id %q", i, id))
		}
		seen[id] = true
	}
	return p
}

// validatePaymentMethods trims entries in place and returns one message per problem: 1-20 entries, each
// nonempty and at most paymentMethodMaxBytes.
func validatePaymentMethods(methods []string) []string {
	var p []string
	for i, m := range methods {
		methods[i] = strings.TrimSpace(m)
	}
	if n := len(methods); n < 1 || n > 20 {
		p = append(p, fmt.Sprintf("invoicing.paymentMethods: %d entries out of range [1,20]", n))
	}
	for i, m := range methods {
		if m == "" {
			p = append(p, fmt.Sprintf("invoicing.paymentMethods[%d]: required", i))
		} else if len(m) > paymentMethodMaxBytes {
			p = append(p, fmt.Sprintf("invoicing.paymentMethods[%d]: max %d bytes", i, paymentMethodMaxBytes))
		}
	}
	return p
}

// usersMultiUserRequiresAuthProblem enforces that auth off (no ADMIN_PASSWORD) means single-user mode.
// It lives outside validate() (a pure function of Config) because ADMIN_PASSWORD is env-derived.
// Both places a Config is accepted call it: main() at startup and handleSettingsPut (folded into the 422).
func usersMultiUserRequiresAuthProblem(multiUser, authEnabled bool) string {
	if multiUser && !authEnabled {
		return "users.multiUser: requires authentication to be enabled -- enable authentication (unset ADMIN_PASSWORD to have one generated, or set it explicitly) before turning on multi-user mode"
	}
	return ""
}

// isAlnumPrefix reports whether s is 1-10 ASCII letters or digits, the shape of invoicing.numberPrefix.
func isAlnumPrefix(s string) bool {
	if len(s) < 1 || len(s) > 10 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}
