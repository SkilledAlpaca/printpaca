package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

// This file seeds ~6 months of backdated, internally consistent sample data into an empty database when
// DEMO_MODE=1 (main() runs seedDemoData after openDB; failure is logged, not fatal). Rows are INSERTed
// directly because the handlers stamp time.Now(); computeQuoteOutputs and computeTotals are reused.

// ---------------------------------------------------------------------------
// Time helpers
// ---------------------------------------------------------------------------

// demoNow is resolved once per seed run so every backdated timestamp is computed against the same instant.
var demoNow = time.Now().UTC()

// daysAgoAt returns a UTC timestamp daysAgo days before demoNow at the given hour:minute, so seeded
// records land at plausible business hours.
func daysAgoAt(daysAgo, hour, minute int) time.Time {
	d := time.Date(demoNow.Year(), demoNow.Month(), demoNow.Day(), hour, minute, 0, 0, time.UTC)
	return d.AddDate(0, 0, -daysAgo)
}

// currentMonthDaysAgo returns n, clamped so the date lands on or after the first of demoNow's month.
// Payments that must count toward "this month" use it: on the 1st it returns 0 instead of counting back
// into the previous month.
func currentMonthDaysAgo(n int) int {
	return min(n, demoNow.Day()-1)
}

// paidInMonthDaysAgo returns how many days before demoNow the given day (1-28) of the calendar month
// monthsBack months before demoNow's month falls. Anchoring payments to calendar months, not "N days ago",
// keeps the Dashboard revenue chart populated for the same months on any day the seed runs.
func paidInMonthDaysAgo(monthsBack, day int) int {
	target := time.Date(demoNow.Year(), demoNow.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -monthsBack, day-1)
	today := time.Date(demoNow.Year(), demoNow.Month(), demoNow.Day(), 0, 0, 0, 0, time.UTC)
	return int(today.Sub(target).Hours() / 24)
}

func rfc3339(t time.Time) string { return t.Format(time.RFC3339) }
func ymd(t time.Time) string     { return t.Format("2006-01-02") }

// ---------------------------------------------------------------------------
// Gate: demoDBIsEmpty / seedDemoData
// ---------------------------------------------------------------------------

// demoDBIsEmpty reports whether this looks like a fresh demo container: none of the tables a user or the
// seed populates (clients, quotes, invoices, items, quote_requests, users) holds a row. A database with
// a row in any of them is seeded or real, and must not be touched.
func demoDBIsEmpty(db *sql.DB) (bool, error) {
	var n int
	if err := db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM clients) + (SELECT COUNT(*) FROM quotes) + (SELECT COUNT(*) FROM invoices) +
		(SELECT COUNT(*) FROM items) + (SELECT COUNT(*) FROM quote_requests) + (SELECT COUNT(*) FROM users)`).Scan(&n); err != nil {
		return false, err
	}
	return n == 0, nil
}

// demoSeedMarkerKey is the app_state key seedDemoData writes in its seeding transaction. main.go's
// demoModeStartupGate uses it to fail closed: DEMO_MODE waives the password floor and tunnel guard, wrong
// to waive in front of real data, and a bare "non-empty" check can't tell the two apart.
const demoSeedMarkerKey = "demo_seeded"

// demoSeedMarkerPresent reports whether app_state carries the seed marker; see demoSeedMarkerKey for why
// demoModeStartupGate consults it.
func demoSeedMarkerPresent(db *sql.DB) (bool, error) {
	var v string
	err := db.QueryRow(`SELECT value FROM app_state WHERE key = ?`, demoSeedMarkerKey).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// seedDemoData seeds all tables plus the config extras (seedDemoConfig); it is idempotent, in one
// transaction. seedDemoConfig deliberately runs before Commit: on failure the Rollback lets the next boot
// retry, whereas after commit later boots would skip the seed and hide the hardware picker.
func seedDemoData(db *sql.DB, store *ConfigStore, configDir string) error {
	empty, err := demoDBIsEmpty(db)
	if err != nil {
		return fmt.Errorf("demo seed: check empty: %w", err)
	}
	if !empty {
		return nil
	}

	cfg := store.Get()
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("demo seed: begin: %w", err)
	}
	defer tx.Rollback()

	if err := seedDemoUsers(tx); err != nil {
		return fmt.Errorf("demo seed: users: %w", err)
	}

	clients, err := seedDemoClients(tx)
	if err != nil {
		return fmt.Errorf("demo seed: clients: %w", err)
	}

	quotes, err := seedDemoQuotes(tx, cfg, clients)
	if err != nil {
		return fmt.Errorf("demo seed: quotes: %w", err)
	}

	nInvoices, nPayments, err := seedDemoInvoices(tx, cfg, clients, quotes)
	if err != nil {
		return fmt.Errorf("demo seed: invoices: %w", err)
	}

	nRequests, err := seedDemoRequests(tx)
	if err != nil {
		return fmt.Errorf("demo seed: requests: %w", err)
	}

	items, err := seedDemoItems(tx, configDir)
	if err != nil {
		return fmt.Errorf("demo seed: items: %w", err)
	}

	if err := seedDemoItemOrder(tx, items); err != nil {
		return fmt.Errorf("demo seed: item order: %w", err)
	}

	if err := seedDemoItemSales(tx, items); err != nil {
		return fmt.Errorf("demo seed: item sales: %w", err)
	}

	// Seed config.yaml too (see seedDemoConfig): a hardware catalog, two public materials, and an intro
	// text, since those features hide on fresh-demo defaults. It runs before the marker and commit so a
	// failure rolls the database back (deferred Rollback) and the next boot can retry.
	if err := seedDemoConfig(configDir); err != nil {
		return fmt.Errorf("demo seed: config: %w", err)
	}

	// Write the seed marker inside this transaction: if anything above failed, it rolls back with the rest,
	// so a database that never got its data is never marked seeded.
	if _, err := tx.Exec(`INSERT INTO app_state (key, value) VALUES (?, ?)`, demoSeedMarkerKey, rfc3339(demoNow)); err != nil {
		return fmt.Errorf("demo seed: marker: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("demo seed: commit: %w", err)
	}

	// main() ran LoadConfig before this, so the store still holds the pre-seed config.yaml. Reload and swap
	// it in (as settings.go's PUT handler does) so the demo serves the new hardware, materials, and intro
	// text on this first boot.
	reloaded, err := LoadConfig(configDir)
	if err != nil {
		return fmt.Errorf("demo seed: reload config: %w", err)
	}
	store.Set(reloaded)

	log.Printf("demo seed: %d employees, %d clients, %d quotes, %d invoices, %d payments, %d requests, %d items",
		len(demoEmployeeSpecs), len(clients), len(quotes), nInvoices, nPayments, nRequests, len(items))
	return nil
}

// demoHardwareCatalog is the demo's small hardware add-on catalog, enough to make the hardware picker and
// Settings Hardware card visible (both hide when hardware is empty). Hardware has no built-in default
// list to derive from, so a literal list carries no drift risk; the values are invented.
var demoHardwareCatalog = []Hardware{
	{Name: "6x2 neodymium magnet", UnitCost: 0.11, LostOnFailure: true},
	{Name: "Earring hook (pair)", UnitCost: 0.08, LostOnFailure: false},
	{Name: "M3 heat-set insert", UnitCost: 0.06, LostOnFailure: false},
}

// demoIntroText seeds publicQuotes.introText for the demo's public page; defaults.yaml's introText is "",
// so the intro-text feature would otherwise show nothing.
const demoIntroText = "Upload your model for an instant estimate, or order from the gallery below — we print and ship within 5 business days."

// demoSeedMaterials returns defaultConfig()'s materials verbatim except "pla" and "petg" are Public, so
// the public page has materials to offer. Built from defaultConfig(), not a literal: config.yaml's
// materials array replaces the built-in list wholesale, so it must be complete.
func demoSeedMaterials() ([]Material, error) {
	defaults, err := defaultConfig()
	if err != nil {
		return nil, fmt.Errorf("load built-in defaults: %w", err)
	}
	materials := make([]Material, len(defaults.Materials))
	copy(materials, defaults.Materials)
	for i := range materials {
		if materials[i].ID == "pla" || materials[i].ID == "petg" {
			materials[i].Public = true
		}
	}
	return materials, nil
}

// demoSeedConfigPublicQuotes is a deliberate subset of PublicQuotes: publicQuotes merges per YAML key
// present in the file, so marshaling the full struct would emit printer: ""/preset: "" and blank
// defaults.yaml's non-empty defaults. Declaring only the seeded fields keeps it a key-level patch.
type demoSeedConfigPublicQuotes struct {
	IntroText string `yaml:"introText"`
}

// demoCustomCss seeds the top-level customCss key with a comment-only value, so the Custom CSS card is
// discoverable without changing how pages render.
const demoCustomCss = "/* Brand the public page and printed documents here: rules load after\n   the app's own styles. Example: :root { --accent: #7c4dcc; } */"

// demoSeedConfigDoc is the config.yaml document seedDemoConfig writes, marshaled from the real
// Material/Hardware structs so a field addition can't drift out of it.
type demoSeedConfigDoc struct {
	Materials    []Material                 `yaml:"materials"`
	Hardware     []Hardware                 `yaml:"hardware"`
	PublicQuotes demoSeedConfigPublicQuotes `yaml:"publicQuotes"`
	CustomCss    string                     `yaml:"customCss"`
	// Users seeds users.multiUser: true, reusing the single-field struct. DEMO_MODE always resolves a
	// non-empty ADMIN_PASSWORD, so usersMultiUserRequiresAuthProblem never rejects it. seedDemoUsers gives
	// multi-user mode something to show: two employees with distinct grants.
	Users Users `yaml:"users"`
}

// demoSeedConfigHeader is prepended to demoSeedConfigDoc's marshaled YAML.
const demoSeedConfigHeader = `# printpaca demo seed data
# Written once by the demo's first-boot seed (server/demoseed.go).
#
# materials: mirrors the built-in defaults (server/defaults.yaml) exactly,
# except "pla" and "petg" are marked public: true, so the public customer
# page's custom-estimate/upload flow has something to offer -- materials
# default to public: false (opt-in), and the demo would otherwise expose
# none of them, hiding that flow entirely.
#
# hardware: a small catalog so the hardware picker (QuoteTab's Costs card)
# and the Settings Hardware card have something to show -- both hide when
# this list is empty.
#
# publicQuotes.introText: shown on the public customer page.
#
# customCss: a comment-only placeholder so the Settings page's Custom CSS
# card has something to show; it styles the public page on screen and
# printed quotes/invoices, both unaffected by this placeholder's content.
#
# users.multiUser: true so the demo shows off employee accounts (two example
# employees, "sam" and "riley", password "demo", each with distinct grants --
# see seedDemoUsers, server/demoseed.go) and per-record attribution ("Created
# by X / Updated by Y") instead of the single-admin default.
#
# DEMO_MODE blocks writes, so without this file the hardware catalog, the
# public materials, and the intro text would all be invisible in the
# published demo. Edit or delete this file freely: the seed never
# overwrites an existing config.yaml, on this or any later boot.
`

// seedDemoConfig writes config.yaml only if none exists, so an operator's file is never clobbered. It
// mirrors writeConfigFile's atomic write but can't call it: that replaces unconditionally and needs
// settingsWriteMu, while this runs once, single-threaded, before the server listens.
func seedDemoConfig(configDir string) error {
	path := filepath.Join(configDir, "config.yaml")
	if _, err := os.Stat(path); err == nil {
		log.Print("demo seed: config.yaml already exists, leaving it untouched (no demo hardware/materials/intro text added)")
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", path, err)
	}

	materials, err := demoSeedMaterials()
	if err != nil {
		return fmt.Errorf("build demo materials: %w", err)
	}
	body, err := yaml.Marshal(demoSeedConfigDoc{
		Materials:    materials,
		Hardware:     demoHardwareCatalog,
		PublicQuotes: demoSeedConfigPublicQuotes{IntroText: demoIntroText},
		CustomCss:    demoCustomCss,
		Users:        Users{MultiUser: true},
	})
	if err != nil {
		return fmt.Errorf("marshal demo config: %w", err)
	}
	content := append([]byte(demoSeedConfigHeader), body...)

	tmp, err := os.CreateTemp(configDir, "config.yaml.*.tmp")
	if err != nil {
		return fmt.Errorf("create temp config.yaml: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename below succeeds

	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp config.yaml: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp config.yaml: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp config.yaml: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename config.yaml into place: %w", err)
	}
	log.Print("demo seed: wrote config.yaml with a demo hardware catalog, two public materials (pla, petg), a public intro text, and users.multiUser: true")
	return nil
}

// ---------------------------------------------------------------------------
// Employee accounts: two employees with distinct grants so the feature is visible.
// ---------------------------------------------------------------------------

// demoEmployeeSpec is one seeded employee: username (password is always demoEmployeePassword) and granted
// tab keys.
type demoEmployeeSpec struct {
	username    string
	permissions []string
}

// demoEmployeeSpecs are the two example employees, with non-overlapping grants so the Users card and nav
// filtering are both visible: sam runs sales (quotes/invoices/clients/dashboard), riley production
// (items/requests/filament).
var demoEmployeeSpecs = []demoEmployeeSpec{
	{username: "sam", permissions: []string{"quotes", "invoices", "clients", "dashboard"}},
	{username: "riley", permissions: []string{"items", "requests", "filament"}},
}

// demoEmployeePassword is short on purpose, like the demo's published ADMIN_PASSWORD: both accounts exist
// only in DEMO_MODE, where demoWriteGuard rejects every write, so it protects nothing beyond a read-only
// sandbox.
const demoEmployeePassword = "demo"

// seedDemoUsers inserts demoEmployeeSpecs directly, matching createUser's columns: createUser runs
// outside any transaction, but the seed must roll back cleanly in tx. Timestamps are backdated (89 days,
// the oldest client's age) so the accounts predate the shop's history.
func seedDemoUsers(tx *sql.Tx) error {
	createdAt := rfc3339(daysAgoAt(200, 8, 0))
	for _, spec := range demoEmployeeSpecs {
		hash, err := bcrypt.GenerateFromPassword([]byte(demoEmployeePassword), bcryptCost)
		if err != nil {
			return fmt.Errorf("demo user %q: hash password: %w", spec.username, err)
		}
		permsJSON, err := encodePermissions(spec.permissions)
		if err != nil {
			return fmt.Errorf("demo user %q: encode permissions: %w", spec.username, err)
		}
		if _, err := tx.Exec(`
			INSERT INTO users (id, username, password_hash, permissions, disabled, created_at, updated_at)
			VALUES (?, ?, ?, ?, 0, ?, ?)`,
			newID(), spec.username, string(hash), permsJSON, createdAt, createdAt,
		); err != nil {
			return fmt.Errorf("demo user %q: insert: %w", spec.username, err)
		}
	}
	return nil
}

// demoAttributionUsernames is who seeded records' created_by/updated_by are drawn from (the env admin and
// both employees), cycled so attribution displays show varied authors.
var demoAttributionUsernames = []string{"admin", "sam", "riley"}

// demoAttributionAt returns demoAttributionUsernames[i % 3], keyed on a record's position in its slice
// (not a global counter), so re-running the seed attributes the same record to the same username.
func demoAttributionAt(i int) string {
	return demoAttributionUsernames[i%len(demoAttributionUsernames)]
}

// ---------------------------------------------------------------------------
// Clients
// ---------------------------------------------------------------------------

// demoClientRow is the subset of a seeded client later steps need to link a quote or invoice to it (and
// rebuild an invoice's frozen client snapshot) without re-querying.
type demoClientRow struct {
	id, name, email, billing, shipping string
}

// demoClientSpec is one hand-built fake client; daysAgo backdates created_at/updated_at across the
// ~200-day window.
type demoClientSpec struct {
	name, email, phone, billing, shipping, notes string
	daysAgo                                      int
}

// demoClientSpecs are obviously-fake but plausible small-shop customers; two (O'Rourke, Nakamura) have no
// email. Each client's daysAgo is at least that of the oldest record referencing them, so no client
// postdates its records; the older-month invoices in seedDemoInvoices reach about 190 days back.
var demoClientSpecs = []demoClientSpec{
	{"Alex Merriweather", "alex.merriweather@example.com", "555-201-4487",
		"214 Birchwood Ln, Ashcombe, OH 44011", "", "Repeat customer, prefers PLA.", 200},
	{"Jordan Blackwood Studio", "orders@jordanblackwoodstudio.example.com", "555-330-7712",
		"48 Foundry St Suite 3, Milltown, PA 19003", "48 Foundry St Suite 3, Milltown, PA 19003",
		"Small design studio; batch orders.", 195},
	{"Priya Anand", "priya.anand@example.com", "555-118-2290",
		"9 Larkspur Ct, Rosedale, TX 75001", "", "", 190},
	{"Sam O'Rourke", "", "555-402-6631",
		"1120 Cedar Ave Apt 4B, Northfield, MN 55057", "", "No email on file; prefers phone contact.", 180},
	{"Taylor Whitfield Designs", "hello@taylorwhitfielddesigns.example.net", "555-275-9081",
		"77 Harbor View Rd, Portsound, ME 04101", "77 Harbor View Rd, Portsound, ME 04101", "", 80},
	{"Morgan Alvarez", "morgan.alvarez@example.com", "",
		"5502 Prairie Rose Dr, Deer Park, IL 60010", "", "", 42},
	{"Casey Nakamura", "", "555-559-1043",
		"310 Willowmere Blvd, Fairhaven, NC 27401", "", "Walk-in regular; no email.", 52},
	{"Riley Fontaine", "riley.fontaine@example.org", "555-664-2298",
		"88 Copperfield Rd, Elmsworth, WA 98101", "88 Copperfield Rd, Elmsworth, WA 98101", "Corporate swag orders.", 28},
}

// seedDemoClients inserts demoClientSpecs, matching clients.go handlePost's INSERT columns. created_by
// and updated_by both come from demoAttributionAt(i): each client is single-touch, unlike quotes and
// invoices where a later status change can move updated_by.
func seedDemoClients(tx *sql.Tx) ([]demoClientRow, error) {
	rows := make([]demoClientRow, 0, len(demoClientSpecs))
	for i, s := range demoClientSpecs {
		id := newID()
		createdAt := rfc3339(daysAgoAt(s.daysAgo, 9, 15))
		by := demoAttributionAt(i)
		if _, err := tx.Exec(`
			INSERT INTO clients (id, name, email, phone, billing_address, shipping_address, notes, created_at, updated_at, created_by, updated_by)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, s.name, s.email, s.phone, s.billing, s.shipping, s.notes, createdAt, createdAt, by, by,
		); err != nil {
			return nil, err
		}
		rows = append(rows, demoClientRow{id: id, name: s.name, email: s.email, billing: s.billing, shipping: s.shipping})
	}
	return rows, nil
}

// Quotes: formState shapes. The engine inputs are built from pricing.go's own OrderInputs types, so the
// seed can't drift from what the server decodes. formState is the editor snapshot the web client restores;
// these structs mirror web/src/lib/state.svelte.ts's PartFormState, OrderForm, and FormState (same JSON
// keys, no per-part id).

// demoPartForm mirrors PartFormState.
type demoPartForm struct {
	Label             string   `json:"label"`
	WeightG           float64  `json:"weightG"`
	TimeHours         float64  `json:"timeHours"`
	TimeMinutes       float64  `json:"timeMinutes"`
	PricePerKg        float64  `json:"pricePerKg"`
	WastePct          float64  `json:"wastePct"`
	PrinterID         string   `json:"printerId"`
	MaterialID        string   `json:"materialId"`
	Watts             float64  `json:"watts"`
	MachineCost       float64  `json:"machineCost"`
	MachineLifeHours  float64  `json:"machineLifeHours"`
	FlatRate          float64  `json:"flatRate"`
	OffPeakFrac       float64  `json:"offPeakFrac"`
	FailureRatePct    float64  `json:"failureRatePct"`
	PrepMinutes       float64  `json:"prepMinutes"`
	LaborRate         float64  `json:"laborRate"`
	MonthlyFixed      float64  `json:"monthlyFixed"`
	MonthlyHours      float64  `json:"monthlyHours"`
	Quantity          float64  `json:"quantity"`
	Hardware          []any    `json:"hardware"`
	PresetName        string   `json:"presetName"`
	PresetModified    bool     `json:"presetModified"`
	SalePriceOverride *float64 `json:"salePriceOverride"`
	SpoolOverride     any      `json:"spoolOverride"`
}

// demoOrderForm mirrors OrderForm.
type demoOrderForm struct {
	SellingEnabled   bool    `json:"sellingEnabled"`
	Strategy         string  `json:"strategy"`
	MarginPct        float64 `json:"marginPct"`
	MarkupMultiplier float64 `json:"markupMultiplier"`
	TaxRatePct       float64 `json:"taxRatePct"`
	ShippingCharge   float64 `json:"shippingCharge"`
	ShippingCost     float64 `json:"shippingCost"`
	FeeScheduleName  string  `json:"feeScheduleName"`
}

// demoFormState mirrors FormState.
type demoFormState struct {
	Version int            `json:"version"`
	Parts   []demoPartForm `json:"parts"`
	Order   demoOrderForm  `json:"order"`
}

// demoStatusLogEntry mirrors web/src/lib/quoteStatus.ts's StatusLogEntry
// ({status, at, sentTo?}).
type demoStatusLogEntry struct {
	Status string `json:"status"`
	At     string `json:"at"`
	SentTo string `json:"sentTo,omitempty"`
}

// ---------------------------------------------------------------------------
// Quotes: templates and instances
// ---------------------------------------------------------------------------

// demoStatusStep is one entry in a seeded quote's status history: status at daysAgo before demoNow, with
// sentTo set only on an emailed "sent" step (quoteStatus.ts's applyTransition convention).
type demoStatusStep struct {
	status  string
	daysAgo int
	sentTo  string
}

// demoQuoteInstance is one fully specified seeded quote: a template's fields plus per-instance overrides
// (client, dates, status history).
type demoQuoteInstance struct {
	label, project, reference    string
	materialID                   string
	weightG                      float64
	timeHours, timeMinutes       float64
	presetName                   string
	quantity                     float64
	sellingEnabled               bool
	strategy                     string // "margin" | "markup"
	marginPct, markupMultiplier  float64
	taxRatePct                   float64
	shippingCharge, shippingCost float64
	feeScheduleName              string // "" or "none" = no fee schedule
	clientIdx                    int    // -1 = walk-in
	steps                        []demoStatusStep
}

// The six hand-built quote templates (rather than random generation, so every record is valid), each
// instantiated 2-4 times below with a different client, date, and status history.

func demoTplCableClips() demoQuoteInstance {
	return demoQuoteInstance{
		label: "Cable clip 8-pack", project: "Cable management",
		materialID: "pla", weightG: 42, timeHours: 0, timeMinutes: 45,
		presetName: "hobby", quantity: 8, sellingEnabled: false,
		strategy: "margin", marginPct: 30, markupMultiplier: 2.5,
		feeScheduleName: "none",
	}
}

func demoTplBracketSet() demoQuoteInstance {
	return demoQuoteInstance{
		label: "Mounting bracket set", project: "Shelf install",
		materialID: "petg", weightG: 210, timeHours: 3, timeMinutes: 10,
		presetName: "side-hustle", quantity: 4, sellingEnabled: true,
		strategy: "margin", marginPct: 30, markupMultiplier: 2.5,
		feeScheduleName: "none",
	}
}

func demoTplHelmetShell() demoQuoteInstance {
	return demoQuoteInstance{
		label: "Cosplay helmet shell", project: "Con costume",
		materialID: "abs", weightG: 850, timeHours: 14, timeMinutes: 0,
		presetName: "small-shop", quantity: 1, sellingEnabled: true,
		strategy: "markup", marginPct: 30, markupMultiplier: 2.5,
		shippingCharge: 12, shippingCost: 8, feeScheduleName: "none",
	}
}

func demoTplPhoneStandBatch() demoQuoteInstance {
	return demoQuoteInstance{
		label: "Phone stand - Etsy batch", project: "Etsy listing #4",
		materialID: "pla", weightG: 65, timeHours: 1, timeMinutes: 20,
		presetName: "side-hustle", quantity: 12, sellingEnabled: true,
		strategy: "margin", marginPct: 35, markupMultiplier: 2.5,
		feeScheduleName: "etsy-us",
	}
}

func demoTplPhoneCasePrototype() demoQuoteInstance {
	return demoQuoteInstance{
		label: "Flexible phone case prototype", project: "R&D",
		materialID: "tpu", weightG: 38, timeHours: 1, timeMinutes: 5,
		presetName: "hobby", quantity: 3, sellingEnabled: true,
		strategy: "margin", marginPct: 25, markupMultiplier: 2.5,
		feeScheduleName: "none",
	}
}

func demoTplResinMinis() demoQuoteInstance {
	return demoQuoteInstance{
		label: "Resin miniature commission (x25)", project: "Tabletop commission",
		materialID: "resin", weightG: 18, timeHours: 2, timeMinutes: 30,
		presetName: "small-shop", quantity: 25, sellingEnabled: true,
		strategy: "markup", marginPct: 30, markupMultiplier: 3.0,
		shippingCharge: 6, shippingCost: 4, feeScheduleName: "etsy-us",
	}
}

// demoQuoteInstances lists the 20 seeded quotes: the six templates instantiated across the last ~90 days.
// Quotes ending "accepted" are the candidates seedDemoInvoices converts, as the real convert-to-invoice
// flow (convertQuote.ts) does; invoicedAs is then set on the quote.
func demoQuoteInstances(clients []demoClientRow) []demoQuoteInstance {
	sentTo := func(clientIdx int) string {
		if clientIdx < 0 || clients[clientIdx].email == "" {
			return ""
		}
		return clients[clientIdx].email
	}
	mk := func(base demoQuoteInstance, clientIdx int, steps []demoStatusStep) demoQuoteInstance {
		base.clientIdx = clientIdx
		base.steps = steps
		return base
	}

	return []demoQuoteInstance{
		mk(demoTplCableClips(), -1, []demoStatusStep{
			{"draft", 85, ""},
		}),
		mk(demoTplBracketSet(), 0, []demoStatusStep{
			{"draft", 80, ""}, {"sent", 77, sentTo(0)},
		}),
		mk(demoTplHelmetShell(), 1, []demoStatusStep{
			{"draft", 74, ""}, {"sent", 70, sentTo(1)}, {"accepted", 65, ""},
		}),
		mk(demoTplPhoneStandBatch(), -1, []demoStatusStep{
			{"draft", 68, ""}, {"sent", 66, ""}, {"declined", 60, ""},
		}),
		mk(demoTplPhoneCasePrototype(), 2, []demoStatusStep{
			{"draft", 55, ""}, {"sent", 50, sentTo(2)}, {"accepted", 45, ""},
		}),
		mk(demoTplResinMinis(), 3, []demoStatusStep{
			{"draft", 50, ""}, {"sent", 45, sentTo(3)},
		}),
		mk(demoTplCableClips(), 4, []demoStatusStep{
			{"draft", 44, ""},
		}),
		mk(demoTplBracketSet(), -1, []demoStatusStep{
			{"draft", 40, ""}, {"sent", 38, ""}, {"declined", 35, ""},
		}),
		mk(demoTplHelmetShell(), 5, []demoStatusStep{
			{"draft", 33, ""}, {"sent", 30, sentTo(5)}, {"accepted", 28, ""},
		}),
		mk(demoTplPhoneStandBatch(), 6, []demoStatusStep{
			{"draft", 29, ""}, {"sent", 27, ""},
		}),
		mk(demoTplPhoneCasePrototype(), -1, []demoStatusStep{
			{"draft", 25, ""},
		}),
		mk(demoTplResinMinis(), 7, []demoStatusStep{
			{"draft", 22, ""}, {"sent", 20, sentTo(7)}, {"accepted", 16, ""},
		}),
		mk(demoTplCableClips(), 0, []demoStatusStep{
			{"draft", 18, ""}, {"sent", 16, ""},
		}),
		mk(demoTplBracketSet(), 2, []demoStatusStep{
			{"draft", 15, ""},
		}),
		mk(demoTplHelmetShell(), -1, []demoStatusStep{
			{"draft", 12, ""}, {"sent", 10, ""}, {"declined", 8, ""},
		}),
		mk(demoTplPhoneStandBatch(), 4, []demoStatusStep{
			{"draft", 10, ""},
		}),
		mk(demoTplPhoneCasePrototype(), 6, []demoStatusStep{
			{"draft", 8, ""}, {"sent", 6, sentTo(6)},
		}),
		mk(demoTplResinMinis(), -1, []demoStatusStep{
			{"draft", 6, ""},
		}),
		mk(demoTplBracketSet(), 1, []demoStatusStep{
			{"draft", 4, ""}, {"sent", 3, sentTo(1)},
		}),
		mk(demoTplCableClips(), 3, []demoStatusStep{
			{"draft", 2, ""},
		}),
	}
}

// seededQuote is what seedDemoInvoices needs to convert an accepted quote into an invoice line, mirroring
// convertQuote.ts's invoiceLineFromQuote.
type seededQuote struct {
	id, label, status string
	clientID          *string
	clientName        string
	quantity          float64
	priceCents        int64
	hasPrice          bool
	shippingCharge    float64
}

// demoPartSpec is one part of a seeded order: the catalog entries it uses and its per-part numbers.
type demoPartSpec struct {
	label            string
	materialID       string
	weightG          float64
	timeHours, timeM float64
	preset           string
	quantity         float64
	override         *float64 // per-part sale price override; sent only when the order sells
}

// demoOrderSpec is the order-level selling block. feeScheduleName "" or "none" means no fee schedule.
type demoOrderSpec struct {
	sellingEnabled              bool
	strategy                    string // "margin" | "markup"
	marginPct, markupMultiplier float64
	taxRatePct                  float64
	shippingCharge              float64
	shippingCost                float64
	feeScheduleName             string
}

// buildDemoOrder builds a seeded quote's {parts, pricing} inputs bag and its formState snapshot from the
// same values, the way the web editor derives the first from the second (toOrderInputs). Every seeded
// quote, single-part or not, goes through here.
func buildDemoOrder(cfg *Config, materials map[string]Material, printer Printer, parts []demoPartSpec, order demoOrderSpec) (rawInputs, rawFormState json.RawMessage, err error) {
	feeName := order.feeScheduleName
	if feeName == "" {
		feeName = "none"
	}
	in := OrderInputs{Parts: make([]OrderPartInputs, 0, len(parts))}
	form := demoFormState{Version: 1, Parts: make([]demoPartForm, 0, len(parts))}
	for _, sp := range parts {
		mat, ok := materials[sp.materialID]
		if !ok {
			return nil, nil, fmt.Errorf("unknown material %q", sp.materialID)
		}
		preset, ok := cfg.Presets[sp.preset]
		if !ok {
			return nil, nil, fmt.Errorf("unknown preset %q", sp.preset)
		}
		part := OrderPartInputs{
			Label: sp.label,
			QuoteInputs: QuoteInputs{
				WeightG: F(sp.weightG), PricePerKg: F(mat.PricePerKg), WastePct: F(mat.WastePct),
				TimeHours: F(sp.timeHours), TimeMinutes: F(sp.timeM), Watts: F(printer.Watts),
				Energy:           EnergyInputs{Mode: "flat", FlatRate: OptF{Value: cfg.Energy.FlatRate, Present: true}},
				FailureRatePct:   F(preset.FailureRatePct),
				PrepMinutes:      F(preset.PrepMinutes),
				LaborRate:        F(preset.LaborRate),
				MachineCost:      F(printer.PurchaseCost),
				MachineLifeHours: F(printer.LifespanHours),
				MonthlyFixed:     F(preset.MonthlyFixed),
				MonthlyHours:     F(preset.MonthlyHours),
				Quantity:         F(sp.quantity),
			},
		}
		if order.sellingEnabled && sp.override != nil {
			part.SalePriceOverride = OptF{Value: *sp.override, Present: true}
		}
		in.Parts = append(in.Parts, part)
		form.Parts = append(form.Parts, demoPartForm{
			Label: sp.label, WeightG: sp.weightG, TimeHours: sp.timeHours, TimeMinutes: sp.timeM,
			PricePerKg: mat.PricePerKg, WastePct: mat.WastePct,
			PrinterID: printer.ID, MaterialID: mat.ID,
			Watts: printer.Watts, MachineCost: printer.PurchaseCost, MachineLifeHours: printer.LifespanHours,
			FlatRate: cfg.Energy.FlatRate, OffPeakFrac: cfg.Energy.Tou.DefaultOffPeakFrac,
			FailureRatePct: preset.FailureRatePct, PrepMinutes: preset.PrepMinutes, LaborRate: preset.LaborRate,
			MonthlyFixed: preset.MonthlyFixed, MonthlyHours: preset.MonthlyHours,
			Quantity: sp.quantity, Hardware: []any{},
			PresetName: sp.preset, PresetModified: false,
			SalePriceOverride: sp.override, SpoolOverride: nil,
		})
	}
	form.Order = demoOrderForm{
		SellingEnabled: order.sellingEnabled, Strategy: order.strategy,
		MarginPct: order.marginPct, MarkupMultiplier: order.markupMultiplier,
		TaxRatePct: order.taxRatePct, ShippingCharge: order.shippingCharge, ShippingCost: order.shippingCost,
		FeeScheduleName: feeName,
	}
	if order.sellingEnabled {
		pricing := &OrderPricingInputs{
			Strategy:         order.strategy,
			MarginPct:        OptF{Value: order.marginPct, Present: true},
			MarkupMultiplier: OptF{Value: order.markupMultiplier, Present: true},
			TaxRatePct:       F(order.taxRatePct), ShippingCharge: F(order.shippingCharge), ShippingCost: F(order.shippingCost),
		}
		if feeName != "none" {
			sched, ok := cfg.FeeSchedules[feeName]
			if !ok {
				return nil, nil, fmt.Errorf("unknown fee schedule %q", feeName)
			}
			fs := &PricingFeeSchedule{Fixed: []PricingFeeFixed{}, Percent: []PricingFeePercent{}}
			if sched.Source != "" {
				fs.Source = &sched.Source
			}
			for _, f := range sched.Fixed {
				fs.Fixed = append(fs.Fixed, PricingFeeFixed{Amount: F(f.Amount), Label: f.Label})
			}
			for _, p := range sched.Percent {
				fs.Percent = append(fs.Percent, PricingFeePercent{Pct: F(p.Pct), Base: p.Base, Label: p.Label})
			}
			pricing.FeeSchedule = fs
			pricing.FeeScheduleName = &feeName
		}
		in.Pricing = pricing
	}
	if rawInputs, err = json.Marshal(in); err != nil {
		return nil, nil, fmt.Errorf("marshal inputs: %w", err)
	}
	if rawFormState, err = json.Marshal(form); err != nil {
		return nil, nil, fmt.Errorf("marshal formState: %w", err)
	}
	return rawInputs, rawFormState, nil
}

// seedDemoQuotes inserts every demoQuoteInstance, matching quotestore.go handlePost's columns and
// stored-inputs wrapper. quotes.inputs is {inputs, options, formState}: inputs is the {parts, pricing} bag
// and formState the editor snapshot, both built by buildDemoOrder from the same values. A
// template is a one-part order.
func seedDemoQuotes(tx *sql.Tx, cfg *Config, clients []demoClientRow) ([]seededQuote, error) {
	materials := map[string]Material{}
	for _, m := range cfg.Materials {
		materials[m.ID] = m
	}
	var printer Printer
	for _, p := range cfg.Printers {
		if p.ID == cfg.Defaults.Printer {
			printer = p
			break
		}
	}

	instances := demoQuoteInstances(clients)
	seeded := make([]seededQuote, 0, len(instances))

	for i, inst := range instances {
		rawInputs, rawFormState, err := buildDemoOrder(cfg, materials, printer,
			[]demoPartSpec{{
				label: "Part 1", materialID: inst.materialID, weightG: inst.weightG,
				timeHours: inst.timeHours, timeM: inst.timeMinutes, preset: inst.presetName, quantity: inst.quantity,
			}},
			demoOrderSpec{
				sellingEnabled: inst.sellingEnabled, strategy: inst.strategy,
				marginPct: inst.marginPct, markupMultiplier: inst.markupMultiplier,
				taxRatePct: inst.taxRatePct, shippingCharge: inst.shippingCharge, shippingCost: inst.shippingCost,
				feeScheduleName: inst.feeScheduleName,
			})
		if err != nil {
			return nil, fmt.Errorf("demo quote %q: %w", inst.label, err)
		}
		rawOptions, err := json.Marshal(EngineOptions{LaborPerUnit: cfg.Pricing.LaborPerUnit})
		if err != nil {
			return nil, fmt.Errorf("demo quote %q: marshal options: %w", inst.label, err)
		}

		var clientID *string
		var clientName string
		if inst.clientIdx >= 0 {
			c := clients[inst.clientIdx]
			clientID = &c.id
			clientName = c.name
		}

		out, err := computeQuoteOutputs(rawInputs, rawOptions)
		if err != nil {
			return nil, fmt.Errorf("demo quote %q: compute: %w", inst.label, err)
		}

		statusLog := make([]demoStatusLogEntry, 0, len(inst.steps))
		for _, st := range inst.steps {
			statusLog = append(statusLog, demoStatusLogEntry{Status: st.status, At: rfc3339(daysAgoAt(st.daysAgo, 10, 0)), SentTo: st.sentTo})
		}
		rawStatusLog, err := json.Marshal(statusLog)
		if err != nil {
			return nil, fmt.Errorf("demo quote %q: marshal statusLog: %w", inst.label, err)
		}
		last := inst.steps[len(inst.steps)-1]
		savedAt := rfc3339(daysAgoAt(last.daysAgo, 10, 0))

		stored, err := json.Marshal(storedQuoteInputs{
			Inputs: rawInputs, Options: normalizeJSONObject(rawOptions), FormState: rawFormState,
		})
		if err != nil {
			return nil, fmt.Errorf("demo quote %q: marshal stored inputs: %w", inst.label, err)
		}

		// createdBy is the rotation's author for this quote's index; updatedBy moves to the next seat when the
		// quote has more than one status-log step (someone else touched it), as the real write path does. A
		// single-step draft has createdBy == updatedBy.
		createdBy := demoAttributionAt(i)
		updatedBy := createdBy
		if len(inst.steps) > 1 {
			updatedBy = demoAttributionAt(i + 1)
		}

		id := newID()
		_, err = tx.Exec(`
			INSERT INTO quotes (id, saved_at, label, client_id, client_name, project, reference, status, status_log, invoiced_as, inputs, quantity, per_unit_total_cents, batch_total_cents, recommended_price_cents, resolved_price_cents, net_profit_cents, achieved_margin_pct, valid, created_by, updated_by, part_count)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, savedAt, inst.label, nullable(clientID), clientName, inst.project, inst.reference,
			last.status, string(rawStatusLog), sql.Null[string]{}, string(stored),
			out.Quantity, out.PerUnitTotalCents, out.BatchTotalCents,
			nullable(out.RecommendedPriceCents), nullable(out.ResolvedPriceCents), nullable(out.NetProfitCents),
			nullable(out.AchievedMarginPct), boolToInt(out.Valid), createdBy, updatedBy, out.PartCount)
		if err != nil {
			return nil, fmt.Errorf("demo quote %q: insert: %w", inst.label, err)
		}

		sq := seededQuote{
			id: id, label: inst.label, status: last.status,
			clientID: clientID, clientName: clientName, quantity: out.Quantity,
			shippingCharge: inst.shippingCharge,
		}
		switch {
		case out.ResolvedPriceCents != nil:
			sq.priceCents, sq.hasPrice = *out.ResolvedPriceCents, true
		case out.RecommendedPriceCents != nil:
			sq.priceCents, sq.hasPrice = *out.RecommendedPriceCents, true
		}
		seeded = append(seeded, sq)
	}

	multi, err := seedDemoMultipartQuote(tx, cfg, materials, printer)
	if err != nil {
		return nil, err
	}
	seeded = append(seeded, multi)
	return seeded, nil
}

// seedDemoMultipartQuote inserts the one multi-part demo quote: a three-part walk-in order (bracket, lid,
// spacers with a hand-set price) stored as quotestore.go stores a 2+ part order (part_count 3, quantity
// 1, prices = order item revenue).
func seedDemoMultipartQuote(tx *sql.Tx, cfg *Config, materials map[string]Material, printer Printer) (seededQuote, error) {
	const label = "Bracket, lid and spacer order"
	spacerPrice := 0.75
	specs := []demoPartSpec{
		{"Bracket body", "petg", 210, 3, 10, "side-hustle", 4, nil},
		{"Bracket lid", "pla", 60, 1, 20, "side-hustle", 4, nil},
		{"Spacer pack", "pla", 15, 0, 25, "side-hustle", 20, &spacerPrice},
	}
	order := demoOrderSpec{
		sellingEnabled: true, strategy: "margin", marginPct: 35, markupMultiplier: 2.5,
		taxRatePct: 7, shippingCharge: 8, shippingCost: 5, feeScheduleName: "none",
	}
	rawInputs, rawFormState, err := buildDemoOrder(cfg, materials, printer, specs, order)
	if err != nil {
		return seededQuote{}, fmt.Errorf("demo multi-part quote: %w", err)
	}
	rawOptions, err := json.Marshal(EngineOptions{LaborPerUnit: cfg.Pricing.LaborPerUnit})
	if err != nil {
		return seededQuote{}, fmt.Errorf("demo multi-part quote: marshal options: %w", err)
	}
	out, err := computeQuoteOutputs(rawInputs, rawOptions)
	if err != nil {
		return seededQuote{}, fmt.Errorf("demo multi-part quote: compute: %w", err)
	}
	if out.PartCount != len(specs) || !out.Valid || out.ResolvedPriceCents == nil {
		return seededQuote{}, fmt.Errorf("demo multi-part quote: computed partCount=%d valid=%v, want %d parts and a valid price", out.PartCount, out.Valid, len(specs))
	}

	rawStatusLog, err := json.Marshal([]demoStatusLogEntry{{Status: "draft", At: rfc3339(daysAgoAt(1, 10, 0))}})
	if err != nil {
		return seededQuote{}, fmt.Errorf("demo multi-part quote: marshal statusLog: %w", err)
	}
	stored, err := json.Marshal(storedQuoteInputs{
		Inputs: rawInputs, Options: normalizeJSONObject(rawOptions), FormState: rawFormState,
	})
	if err != nil {
		return seededQuote{}, fmt.Errorf("demo multi-part quote: marshal stored inputs: %w", err)
	}
	by := demoAttributionAt(0)
	id := newID()
	_, err = tx.Exec(`
		INSERT INTO quotes (id, saved_at, label, client_id, client_name, project, reference, status, status_log, invoiced_as, inputs, quantity, per_unit_total_cents, batch_total_cents, recommended_price_cents, resolved_price_cents, net_profit_cents, achieved_margin_pct, valid, created_by, updated_by, part_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, rfc3339(daysAgoAt(1, 10, 0)), label, sql.Null[string]{}, "", "Shelf install", "",
		"draft", string(rawStatusLog), sql.Null[string]{}, string(stored),
		out.Quantity, out.PerUnitTotalCents, out.BatchTotalCents,
		nullable(out.RecommendedPriceCents), nullable(out.ResolvedPriceCents), nullable(out.NetProfitCents),
		nullable(out.AchievedMarginPct), boolToInt(out.Valid), by, by, out.PartCount)
	if err != nil {
		return seededQuote{}, fmt.Errorf("demo multi-part quote: insert: %w", err)
	}
	return seededQuote{
		id: id, label: label, status: "draft", quantity: out.Quantity,
		shippingCharge: order.shippingCharge, priceCents: *out.ResolvedPriceCents, hasPrice: true,
	}, nil
}

// ---------------------------------------------------------------------------
// Invoices and payments
// ---------------------------------------------------------------------------

// demoPayment is one seeded payment slot, paid daysAgo before demoNow. The amount is derived:
// seedDemoInvoices splits the invoice total evenly across a plan's payments (remainder last), so they sum
// exactly. reference is "" on about half on purpose, so the demo shows both states.
type demoPayment struct {
	daysAgo   int
	method    string
	note      string
	reference string
}

// seedDemoInvoices inserts 19 invoices: 4 converted from accepted seeded quotes (as convertQuote.ts does,
// with a "Shipping" line when charged) and 15 standalone, spanning draft/sent/paid/canceled with one
// overdue sent. Paid ones get 1-2 payments summing to the total.
func seedDemoInvoices(tx *sql.Tx, cfg *Config, clients []demoClientRow, quotes []seededQuote) (nInvoices, nPayments int, err error) {
	taxBp := roundHalfUp(cfg.Invoicing.SalesTaxPct * 100)
	dueDays := cfg.Invoicing.DueDays
	if dueDays <= 0 {
		dueDays = 30
	}
	prefix := cfg.Invoicing.NumberPrefix

	type invoicePlan struct {
		// Either quoteIdx >= 0 (converted from a seeded quote) or client/walkinName + lines are set directly
		// (standalone).
		quoteIdx      int
		clientIdx     int // -1 with walkinName set = freeform walk-in customer
		walkinName    string
		status        string
		issueDaysAgo  int
		discountCents int
		lines         []invoiceLineInput
		payments      []demoPayment
	}

	plans := []invoicePlan{
		// Converted from accepted quotes (indices 2, 4, 8, 11 in demoQuoteInstances).
		{quoteIdx: 2, status: "paid", issueDaysAgo: 64, payments: []demoPayment{
			{daysAgo: 55, method: "bank transfer", note: "deposit", reference: "Bank transfer"},
			{daysAgo: 40, method: "cash", note: "balance on pickup"},
		}},
		{quoteIdx: 4, status: "sent", issueDaysAgo: 45}, // overdue: issued 45d ago, 30d terms, unpaid
		{quoteIdx: 8, status: "draft", issueDaysAgo: 26},
		{quoteIdx: 11, status: "paid", issueDaysAgo: 16, payments: []demoPayment{
			{daysAgo: currentMonthDaysAgo(10), method: "PayPal", reference: "PayPal 8823"},
		}},
		// Standalone invoices, not linked to any quote.
		{quoteIdx: -1, clientIdx: 3, status: "paid", issueDaysAgo: 70,
			lines:    []invoiceLineInput{{Description: "PLA prototype set", Quantity: 3, UnitPriceCents: 1800}},
			payments: []demoPayment{{daysAgo: 60, method: "cash", reference: "Cash"}}},
		{quoteIdx: -1, clientIdx: 4, status: "sent", issueDaysAgo: 20,
			lines: []invoiceLineInput{{Description: "PETG enclosure batch", Quantity: 2, UnitPriceCents: 3250}}},
		{quoteIdx: -1, clientIdx: 5, status: "canceled", issueDaysAgo: 35,
			lines: []invoiceLineInput{{Description: "ABS tool holder", Quantity: 1, UnitPriceCents: 4500}}},
		{quoteIdx: -1, clientIdx: -1, walkinName: "Jamie Ostrowski", status: "draft", issueDaysAgo: 5,
			lines: []invoiceLineInput{{Description: "Custom nameplate", Quantity: 5, UnitPriceCents: 900}}},
		{quoteIdx: -1, clientIdx: 6, status: "paid", issueDaysAgo: 48,
			lines:    []invoiceLineInput{{Description: "Resin minis (10)", Quantity: 10, UnitPriceCents: 650}},
			payments: []demoPayment{{daysAgo: 40, method: "other", note: "Venmo"}}},
		{quoteIdx: -1, clientIdx: 7, status: "draft", issueDaysAgo: 9,
			lines: []invoiceLineInput{{Description: "TPU gasket set", Quantity: 6, UnitPriceCents: 425}}},
		{quoteIdx: -1, clientIdx: 0, status: "paid", issueDaysAgo: 27, discountCents: 1000,
			lines:    []invoiceLineInput{{Description: "PLA architectural model", Quantity: 1, UnitPriceCents: 12000}},
			payments: []demoPayment{{daysAgo: currentMonthDaysAgo(3), method: "bank transfer"}}},
		{quoteIdx: -1, clientIdx: -1, walkinName: "Devon Marchetti", status: "sent", issueDaysAgo: 8,
			lines: []invoiceLineInput{{Description: "PETG parts run", Quantity: 8, UnitPriceCents: 1100}}},
		// Older paid invoices anchored to calendar months, so the revenue chart shows a growing trend over
		// six months on any day the seed runs.
		{quoteIdx: -1, clientIdx: 1, status: "paid", issueDaysAgo: paidInMonthDaysAgo(5, 14) + 9,
			lines:    []invoiceLineInput{{Description: "PLA display stands", Quantity: 6, UnitPriceCents: 2200}},
			payments: []demoPayment{{daysAgo: paidInMonthDaysAgo(5, 14), method: "bank transfer", reference: "Bank transfer"}}},
		{quoteIdx: -1, clientIdx: 2, status: "paid", issueDaysAgo: paidInMonthDaysAgo(4, 9) + 5,
			lines:    []invoiceLineInput{{Description: "Resin figurine set", Quantity: 4, UnitPriceCents: 3400}},
			payments: []demoPayment{{daysAgo: paidInMonthDaysAgo(4, 9), method: "PayPal", reference: "PayPal 7710"}}},
		{quoteIdx: -1, clientIdx: 3, status: "paid", issueDaysAgo: paidInMonthDaysAgo(4, 24) + 7,
			lines:    []invoiceLineInput{{Description: "ABS bracket kit", Quantity: 6, UnitPriceCents: 1450}},
			payments: []demoPayment{{daysAgo: paidInMonthDaysAgo(4, 24), method: "cash"}}},
		{quoteIdx: -1, clientIdx: 0, status: "paid", issueDaysAgo: paidInMonthDaysAgo(3, 12) + 8,
			lines:    []invoiceLineInput{{Description: "PETG drone frame batch", Quantity: 5, UnitPriceCents: 4200}},
			payments: []demoPayment{{daysAgo: paidInMonthDaysAgo(3, 12), method: "bank transfer"}}},
		{quoteIdx: -1, clientIdx: 1, status: "paid", issueDaysAgo: paidInMonthDaysAgo(3, 27) + 10,
			lines:    []invoiceLineInput{{Description: "PLA signage letters", Quantity: 18, UnitPriceCents: 1350}},
			payments: []demoPayment{{daysAgo: paidInMonthDaysAgo(3, 27), method: "bank transfer", reference: "Bank transfer"}}},
		{quoteIdx: -1, clientIdx: 2, status: "paid", issueDaysAgo: paidInMonthDaysAgo(2, 8) + 4,
			lines:    []invoiceLineInput{{Description: "Resin miniatures (12)", Quantity: 12, UnitPriceCents: 1500}},
			payments: []demoPayment{{daysAgo: paidInMonthDaysAgo(2, 8), method: "PayPal"}}},
		{quoteIdx: -1, clientIdx: 4, status: "paid", issueDaysAgo: paidInMonthDaysAgo(1, 11) + 6,
			lines:    []invoiceLineInput{{Description: "PLA retail display pieces", Quantity: 16, UnitPriceCents: 3600}},
			payments: []demoPayment{{daysAgo: paidInMonthDaysAgo(1, 11), method: "bank transfer", reference: "Bank transfer"}}},
	}

	// nextInvoiceNumber hands out increasing number_seq in loop order and the admin list sorts
	// newest-numbered first, so plans must be issued oldest-first (non-increasing issueDaysAgo), or the demo
	// would show numbering the app never produces. Sorted here so the literal stays grouped by origin.
	sort.SliceStable(plans, func(i, j int) bool { return plans[i].issueDaysAgo > plans[j].issueDaysAgo })

	for i, p := range plans {
		by := demoAttributionAt(i)
		var snap clientSnapshot
		var quoteID string
		var lines []invoiceLineInput
		var convertedQuote *seededQuote

		if p.quoteIdx >= 0 {
			q := quotes[p.quoteIdx]
			if !q.hasPrice {
				return nInvoices, nPayments, fmt.Errorf("demo invoice: seeded quote %q has no price to convert", q.label)
			}
			convertedQuote = &q
			quoteID = q.id
			lines = append(lines, invoiceLineInput{
				Description: q.label, Quantity: q.quantity, UnitPriceCents: int(q.priceCents), QuoteID: q.id,
			})
			if q.shippingCharge > 0 {
				lines = append(lines, invoiceLineInput{
					Description: quoteConversionShippingDescription, Quantity: 1,
					UnitPriceCents: int(toCents(q.shippingCharge)), QuoteID: q.id, Kind: invoiceLineKindShipping,
				})
			}
			if q.clientID != nil {
				snap = clientSnapshot{ClientID: *q.clientID, ClientName: q.clientName}
				for _, c := range clients {
					if c.id == *q.clientID {
						snap.ClientEmail = c.email
						snap.ClientBillingAddress = c.billing
						snap.ClientShippingAddress = c.shipping
					}
				}
			} else {
				snap = clientSnapshot{ClientName: q.clientName}
			}
		} else {
			lines = p.lines
			if p.clientIdx >= 0 {
				c := clients[p.clientIdx]
				snap = clientSnapshot{
					ClientID: c.id, ClientName: c.name, ClientEmail: c.email,
					ClientBillingAddress: c.billing, ClientShippingAddress: c.shipping,
				}
			} else {
				snap = clientSnapshot{ClientName: p.walkinName}
			}
		}

		issue := daysAgoAt(p.issueDaysAgo, 11, 0)
		issueDate := ymd(issue)
		dueDate := ymd(issue.AddDate(0, 0, dueDays))
		createdAt := rfc3339(issue)
		updatedAt := createdAt

		// computeTotals applies its shipping-kind exemption uniformly, so converted and standalone plans share
		// one totals call.
		totals := computeTotals(lines, p.discountCents, taxBp)

		id := newID()
		year := issue.Year()
		number, seq, err := nextInvoiceNumber(tx, prefix, year)
		if err != nil {
			return nInvoices, nPayments, fmt.Errorf("demo invoice: number: %w", err)
		}

		_, err = tx.Exec(`
			INSERT INTO invoices (
				id, number, number_year, number_seq, status,
				client_id, client_name, client_email, client_billing_address, client_shipping_address,
				issue_date, due_date, tax_bp, discount_cents, notes,
				subtotal_cents, tax_cents, total_cents, created_at, updated_at, created_by, updated_by
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, number, year, seq, p.status,
			snap.ClientID, snap.ClientName, snap.ClientEmail, snap.ClientBillingAddress, snap.ClientShippingAddress,
			issueDate, dueDate, taxBp, p.discountCents, "",
			totals.SubtotalCents, totals.TaxCents, totals.TotalCents, createdAt, updatedAt, by, by,
		)
		if err != nil {
			return nInvoices, nPayments, fmt.Errorf("demo invoice: insert: %w", err)
		}
		if err := insertInvoiceLines(tx, id, lines); err != nil {
			return nInvoices, nPayments, fmt.Errorf("demo invoice: lines: %w", err)
		}
		nInvoices++

		if convertedQuote != nil {
			if _, err := tx.Exec(`UPDATE quotes SET invoiced_as = ?, status = 'accepted' WHERE id = ?`, id, quoteID); err != nil {
				return nInvoices, nPayments, fmt.Errorf("demo invoice: link quote: %w", err)
			}
		}

		if len(p.payments) > 0 {
			// Split the total across the planned payments: all but the last get an equal share and the last takes
			// the remainder, so the sum is exactly totals.TotalCents.
			share := totals.TotalCents / len(p.payments)
			remaining := totals.TotalCents
			for i, pay := range p.payments {
				amount := share
				if i == len(p.payments)-1 {
					amount = remaining
				}
				remaining -= amount
				paidDate := ymd(daysAgoAt(pay.daysAgo, 15, 0))
				paidCreatedAt := rfc3339(daysAgoAt(pay.daysAgo, 15, 0))
				// by is this invoice's rotation seat: a payment has no separate updated_by (never edited), so it is
				// attributed to whoever recorded the invoice.
				if err := insertPayment(tx, id, paidDate, amount, pay.method, pay.note, pay.reference, paidCreatedAt, by); err != nil {
					return nInvoices, nPayments, fmt.Errorf("demo invoice: payment: %w", err)
				}
				nPayments++
			}
		}
	}

	return nInvoices, nPayments, nil
}

// ---------------------------------------------------------------------------
// Quote requests (public quote page submissions)
// ---------------------------------------------------------------------------

// demoRequestSpec is one seeded public quote-request submission.
type demoRequestSpec struct {
	name, email, note, material string
	quantity                    int
	weightG                     float64
	timeSeconds                 int
	priceCents                  int
	status                      string
	daysAgo                     int
}

var demoRequestSpecs = []demoRequestSpec{
	{"Harper Quinlan", "harper.quinlan@example.com", "Need this by next weekend if possible!",
		"pla", 2, 54, 2600, 1899, "new", 3},
	{"Nate Ferreira", "nate.ferreira@example.net", "",
		"petg", 1, 180, 9800, 3899, "new", 1},
	{"Ola Svensson", "ola.svensson@example.org", "Can you do this in matte black?",
		"abs", 3, 310, 14400, 6299, "viewed", 6},
	{"Priya Anand", "priya.anand@example.com", "For a small business gift order",
		"resin", 10, 20, 7200, 8999, "viewed", 9},
	{"Deja Whitcombe", "deja.whitcombe@example.com", "Approved, please proceed",
		"pla", 5, 95, 3200, 4499, "finalized", 12},
}

// seedDemoRequests inserts demoRequestSpecs, matching public.go's INSERT INTO quote_requests columns
// minus the G-code upload fields (no seeded request has a file), plus finalized_at for the one finalized
// request. price_cents is the row's own authoritative price.
func seedDemoRequests(tx *sql.Tx) (int, error) {
	n := 0
	for i, s := range demoRequestSpecs {
		created := daysAgoAt(s.daysAgo, 13, 30)
		finalizedAt := ""
		if s.status == "finalized" {
			finalizedAt = rfc3339(created.Add(2 * time.Hour))
		}
		// updated_by stays "" for a still-"new" request (nobody on staff has touched it, as in requests.go);
		// "viewed" and "finalized" reflect a staff action and get a rotation seat.
		updatedBy := ""
		if s.status != "new" {
			updatedBy = demoAttributionAt(i)
		}
		_, err := tx.Exec(`
			INSERT INTO quote_requests (id, status, name, email, note, material, quantity, weight_g, time_seconds, price_cents, gcode_filename, gcode_path, gcode_bytes, created_at, finalized_at, updated_by, tax_inclusive)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', '', 0, ?, ?, ?, 0)`,
			newID(), s.status, s.name, s.email, s.note, s.material, s.quantity, s.weightG, s.timeSeconds, s.priceCents,
			rfc3339(created), finalizedAt, updatedBy,
		)
		if err != nil {
			return n, fmt.Errorf("demo request %q: insert: %w", s.name, err)
		}
		n++
	}
	return n, nil
}

// ---------------------------------------------------------------------------
// Items (public gallery) and one item order
// ---------------------------------------------------------------------------

// demoItemRow is the subset of a seeded item seedDemoItemOrder needs to pick the right item (public,
// in_stock) and build a quote_requests snapshot without re-querying.
type demoItemRow struct {
	id, name    string
	priceCents  int
	public      bool
	stockMode   string
	optionName  string
	optionDelta int
	options     []ItemOption // every option, for seedDemoItemSales
}

// demoItemSpec is one hand-built fake gallery item; daysAgo backdates created_at/updated_at as in
// seedDemoClients. imageBG/imageShape drive drawDemoItemImage: items get a generated placeholder photo,
// unlike other seeded records (see demoItemSpecs).
type demoItemSpec struct {
	name, description string
	priceCents        int
	optionLabel       string
	options           []ItemOption
	public            bool
	stockMode         string
	stockQty          int
	allowBackorder    bool
	sortOrder         int
	daysAgo           int
	imageBG           color.RGBA
	imageShape        demoShape
}

// demoItemSpecs seeds four items: a public in_stock item with a two-option "Color" list (one with a
// positive delta) and allowBackorder, a public ready_to_print item, a non-public item proving the
// public filter, and a public ready_to_print item with a two-option "Size" list. Items alone get a placeholder photo: a gallery of gray tiles undersells the feature.
var demoItemSpecs = []demoItemSpec{
	{
		name:        "Cable Clip 8-Pack",
		description: "A set of 8 snap-in cable clips for desk or wall mounting.",
		priceCents:  900,
		optionLabel: "Color",
		options: []ItemOption{
			{Name: "Black", DeltaCents: 0},
			{Name: "White", DeltaCents: 50},
		},
		public:         true,
		stockMode:      stockModeInStock,
		stockQty:       24,
		allowBackorder: true,
		sortOrder:      0,
		daysAgo:        40,
		imageBG:        color.RGBA{R: 0x1f, G: 0x6f, B: 0xeb, A: 0xff}, // blue
		imageShape:     demoShapeCircle,
	},
	{
		name:        "Phone Stand",
		description: "An adjustable-angle desk stand, printed to order.",
		priceCents:  1499,
		public:      true,
		stockMode:   stockModeReadyToPrint,
		sortOrder:   1,
		daysAgo:     35,
		imageBG:     color.RGBA{R: 0x1a, G: 0x9c, B: 0x5b, A: 0xff}, // green
		imageShape:  demoShapeRoundedSquare,
	},
	{
		name:        "Custom Cosplay Commission",
		description: "Draft listing for bespoke cosplay commission work, not yet published.",
		priceCents:  12000,
		public:      false,
		stockMode:   stockModeReadyToPrint,
		sortOrder:   2,
		daysAgo:     20,
		imageBG:     color.RGBA{R: 0xc2, G: 0x41, B: 0x0c, A: 0xff}, // orange
		imageShape:  demoShapeTriangle,
	},
	{
		name:        "Desk Organizer Tray",
		description: "A stackable tray for pens, cables, and small parts, printed to order.",
		priceCents:  2200,
		optionLabel: "Size",
		options: []ItemOption{
			{Name: "Small", DeltaCents: 0},
			{Name: "Large", DeltaCents: 400},
		},
		public:     true,
		stockMode:  stockModeReadyToPrint,
		sortOrder:  3,
		daysAgo:    88,
		imageBG:    color.RGBA{R: 0x6d, G: 0x28, B: 0xd9, A: 0xff}, // purple
		imageShape: demoShapeRoundedSquare,
	},
}

// ---------------------------------------------------------------------------
// Item placeholder image generation
// ---------------------------------------------------------------------------

// demoItemImageSize is the edge length in pixels of every placeholder image: clear as a product photo in
// the thumbnail and gallery card, yet negligible in seed bytes.
const demoItemImageSize = 512

// demoShape names the flat white silhouette drawDemoItemImage paints centered on a spec's background color.
type demoShape int

const (
	demoShapeCircle demoShape = iota
	demoShapeRoundedSquare
	demoShapeTriangle
)

// drawDemoItemImage renders a square PNG: bg fills the canvas and shape is painted flat white, centered.
// Standard library only, no randomness or clock, so a (bg, shape) pair always yields the same bytes;
// seedDemoItems relies on that for the content-addressed filename (sha256).
func drawDemoItemImage(bg color.RGBA, shape demoShape) []byte {
	img := image.NewRGBA(image.Rect(0, 0, demoItemImageSize, demoItemImageSize))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: bg}, image.Point{}, draw.Src)

	white := color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	const margin = demoItemImageSize / 5 // leaves a visible border of the background color
	switch shape {
	case demoShapeCircle:
		drawDemoFilledCircle(img, white, margin)
	case demoShapeRoundedSquare:
		drawDemoFilledRoundedSquare(img, white, margin)
	case demoShapeTriangle:
		drawDemoFilledTriangle(img, white, margin)
	}

	var buf bytes.Buffer
	// png.Encode embeds no timestamp or run-varying metadata, so the same pixels give the same bytes.
	if err := png.Encode(&buf, img); err != nil {
		// img is in memory, so an encode failure is a programming error: panic rather than plumb an error
		// through every seedDemoItems caller.
		panic(fmt.Sprintf("demo item image: encode: %v", err))
	}
	return buf.Bytes()
}

// drawDemoFilledCircle fills every pixel inside the circle inscribed within margin of img's edges.
func drawDemoFilledCircle(img *image.RGBA, c color.RGBA, margin int) {
	size := img.Bounds().Dx()
	cx, cy := size/2, size/2
	radius := size/2 - margin
	for y := range size {
		for x := range size {
			dx, dy := x-cx, y-cy
			if dx*dx+dy*dy <= radius*radius {
				img.SetRGBA(x, y, c)
			}
		}
	}
}

// drawDemoFilledRoundedSquare fills a square inset by margin with rounded corners, treating each corner
// as a quarter-circle.
func drawDemoFilledRoundedSquare(img *image.RGBA, c color.RGBA, margin int) {
	size := img.Bounds().Dx()
	left, top := margin, margin
	right, bottom := size-margin, size-margin
	corner := (right - left) / 6
	for y := top; y < bottom; y++ {
		for x := left; x < right; x++ {
			var inCorner bool
			var ccx, ccy int
			switch {
			case x < left+corner && y < top+corner:
				inCorner, ccx, ccy = true, left+corner, top+corner
			case x >= right-corner && y < top+corner:
				inCorner, ccx, ccy = true, right-corner, top+corner
			case x < left+corner && y >= bottom-corner:
				inCorner, ccx, ccy = true, left+corner, bottom-corner
			case x >= right-corner && y >= bottom-corner:
				inCorner, ccx, ccy = true, right-corner, bottom-corner
			}
			if inCorner {
				dx, dy := x-ccx, y-ccy
				if dx*dx+dy*dy > corner*corner {
					continue
				}
			}
			img.SetRGBA(x, y, c)
		}
	}
}

// drawDemoFilledTriangle fills an upward-pointing triangle inset by margin, using the
// same-sign-of-all-three-cross-products point-in-triangle test.
func drawDemoFilledTriangle(img *image.RGBA, c color.RGBA, margin int) {
	size := img.Bounds().Dx()
	apexX, apexY := size/2, margin
	leftX, leftY := margin, size-margin
	rightX, rightY := size-margin, size-margin

	edgeSign := func(px, py, ax, ay, bx, by int) int {
		return (px-bx)*(ay-by) - (ax-bx)*(py-by)
	}
	for y := range size {
		for x := range size {
			d1 := edgeSign(x, y, apexX, apexY, leftX, leftY)
			d2 := edgeSign(x, y, leftX, leftY, rightX, rightY)
			d3 := edgeSign(x, y, rightX, rightY, apexX, apexY)
			hasNeg := d1 < 0 || d2 < 0 || d3 < 0
			hasPos := d1 > 0 || d2 > 0 || d3 > 0
			if !(hasNeg && hasPos) {
				img.SetRGBA(x, y, c)
			}
		}
	}
}

// demoItemImageFilename derives the content-addressed filename as handleImageUpload does (sha256 of the
// body, first 16 hex chars, plus an extension), so seeded images match itemImageName and look like real
// uploads.
func demoItemImageFilename(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])[:16] + ".png"
}

// seedDemoItems inserts demoItemSpecs, matching items.go handlePost's INSERT columns, except image_file
// is set: each spec renders a placeholder PNG (drawDemoItemImage) written under configDir/uploads/items
// with handleImageUpload's naming and permissions, so the row points at a real file.
func seedDemoItems(tx *sql.Tx, configDir string) ([]demoItemRow, error) {
	imageDir := filepath.Join(configDir, "uploads", "items")
	if err := os.MkdirAll(imageDir, 0o755); err != nil {
		return nil, fmt.Errorf("demo items: image dir: %w", err)
	}

	rows := make([]demoItemRow, 0, len(demoItemSpecs))
	for i, s := range demoItemSpecs {
		options := s.options
		if options == nil {
			options = []ItemOption{}
		}
		optionsJSON, err := json.Marshal(options)
		if err != nil {
			return nil, fmt.Errorf("demo item %q: marshal options: %w", s.name, err)
		}

		imageBody := drawDemoItemImage(s.imageBG, s.imageShape)
		imageFile := demoItemImageFilename(imageBody)
		if err := os.WriteFile(itemImagePath(configDir, imageFile), imageBody, 0o644); err != nil {
			return nil, fmt.Errorf("demo item %q: write image: %w", s.name, err)
		}

		id := newID()
		createdAt := rfc3339(daysAgoAt(s.daysAgo, 9, 0))
		by := demoAttributionAt(i)
		_, err = tx.Exec(`
			INSERT INTO items (id, name, description, price_cents, option_label, options_json,
				image_file, public, stock_mode, stock_qty, allow_backorder, sort_order, created_at, updated_at, created_by, updated_by)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, s.name, s.description, s.priceCents, s.optionLabel, string(optionsJSON),
			imageFile, s.public, s.stockMode, s.stockQty, s.allowBackorder, s.sortOrder, createdAt, createdAt, by, by,
		)
		if err != nil {
			return nil, err
		}

		row := demoItemRow{id: id, name: s.name, priceCents: s.priceCents, public: s.public, stockMode: s.stockMode, options: s.options}
		if len(s.options) > 0 {
			// Pick the last option (the positive-delta one, by construction of demoItemSpecs) so the seeded order
			// shows a non-zero option price adjustment.
			opt := s.options[len(s.options)-1]
			row.optionName, row.optionDelta = opt.Name, opt.DeltaCents
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// seedDemoItemOrder inserts one quote_requests row shaped like handlePublicItemOrder's INSERT, for the
// first public in_stock item ("Cable Clip 8-Pack"); selecting on those criteria stays stable if another
// in_stock item without options is added. Quantity and total follow price plus option delta.
func seedDemoItemOrder(tx *sql.Tx, items []demoItemRow) error {
	var target *demoItemRow
	for i := range items {
		if items[i].public && items[i].stockMode == stockModeInStock {
			target = &items[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("no seeded public in_stock item to order")
	}

	const quantity = 2
	total := (target.priceCents + target.optionDelta) * quantity
	created := daysAgoAt(4, 14, 20)

	_, err := tx.Exec(`INSERT INTO quote_requests
	  (id, status, kind, name, email, note, material, quantity, weight_g, time_seconds,
	   price_cents, gcode_filename, gcode_path, gcode_bytes, created_at,
	   item_id, item_name, option_name, tax_inclusive)
	  VALUES (?, 'new', 'item', ?, ?, '', '', ?, 0, 0, ?, '', '', 0, ?, ?, ?, ?, 0)`,
		newID(), "Emerson Vance", "emerson.vance@example.com", quantity, total,
		rfc3339(created), target.id, target.name, target.optionName,
	)
	if err != nil {
		return err
	}
	return nil
}

// demoItemSaleSpec is one finalized gallery order: item is the demoItemSpecs name, option the option name
// ("" for none), and daysAgo when it was finalized. Dates stay after each item's created_at, and the
// spread (clips and stands recent, trays older) makes the 30- and 90-day Top items rankings differ.
type demoItemSaleSpec struct {
	item, option string
	quantity     int
	daysAgo      int
	buyer        string
}

var demoItemSaleSpecs = []demoItemSaleSpec{
	{"Cable Clip 8-Pack", "White", 4, 3, "Marlow Pike"},
	{"Cable Clip 8-Pack", "Black", 6, 9, "Tamsin Okafor"},
	{"Cable Clip 8-Pack", "White", 2, 20, "Idris Lund"},
	{"Cable Clip 8-Pack", "Black", 3, 37, "Wren Castellan"},
	{"Phone Stand", "", 3, 6, "Odalys Brandt"},
	{"Phone Stand", "", 2, 14, "Kestrel Moon"},
	{"Phone Stand", "", 4, 33, "Piers Halloran"},
	{"Desk Organizer Tray", "Large", 7, 45, "Linnea Arroyo"},
	{"Desk Organizer Tray", "Small", 4, 58, "Fenwick Adair"},
	{"Desk Organizer Tray", "Large", 3, 71, "Saoirse Vale"},
	{"Desk Organizer Tray", "Small", 2, 82, "Bastian Rowe"},
}

// seedDemoItemSales inserts a finalized quote_requests row and its item_sales ledger row for each of
// demoItemSaleSpecs, shaped like handlePublicItemOrder's INSERT plus handleStatus's finalize, so the Top
// items panel has history in every window. Stock is left as seeded (the demo shelf is not replayed).
func seedDemoItemSales(tx *sql.Tx, items []demoItemRow) error {
	byName := make(map[string]demoItemRow, len(items))
	for _, it := range items {
		byName[it.name] = it
	}
	for i, s := range demoItemSaleSpecs {
		item, ok := byName[s.item]
		if !ok {
			return fmt.Errorf("demo item sale %d: no seeded item %q", i, s.item)
		}
		delta := 0
		if s.option != "" {
			found := false
			for _, o := range item.options {
				if o.Name == s.option {
					delta, found = o.DeltaCents, true
				}
			}
			if !found {
				return fmt.Errorf("demo item sale %d: item %q has no option %q", i, s.item, s.option)
			}
		}
		total := (item.priceCents + delta) * s.quantity
		finalized := daysAgoAt(s.daysAgo, 15, 40)
		created := finalized.Add(-26 * time.Hour)
		by := demoAttributionAt(i)
		id := newID()

		if _, err := tx.Exec(`INSERT INTO quote_requests
		  (id, status, kind, name, email, note, material, quantity, weight_g, time_seconds,
		   price_cents, gcode_filename, gcode_path, gcode_bytes, created_at, finalized_at,
		   item_id, item_name, option_name, updated_by, tax_inclusive)
		  VALUES (?, 'finalized', 'item', ?, ?, '', '', ?, 0, 0, ?, '', '', 0, ?, ?, ?, ?, ?, ?, 0)`,
			id, s.buyer, fmt.Sprintf("buyer%d@example.com", i+1), s.quantity, total,
			rfc3339(created), rfc3339(finalized), item.id, item.name, s.option, by,
		); err != nil {
			return fmt.Errorf("demo item sale %d: request: %w", i, err)
		}
		if _, err := tx.Exec(`INSERT INTO item_sales
		  (request_id, item_id, item_name, option_name, quantity, revenue_cents, sold_at, recorded_by)
		  VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			id, item.id, item.name, s.option, s.quantity, total, rfc3339(finalized), by,
		); err != nil {
			return fmt.Errorf("demo item sale %d: ledger: %w", i, err)
		}
	}
	return nil
}
