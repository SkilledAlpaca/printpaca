package main

import (
	"database/sql"
	"encoding/json"
	"math"
	"strings"
	"testing"
)

// D64: the demo's fake spools (demo.go) are the one source for the demo Filament list, and every seeded quote
// and request must follow it. These tests fail when a material, price, spool or request price drifts.

// seedDemoForFilamentTests seeds a fresh database and returns it with the post-seed config (the live store, as
// the running demo serves it).
func seedDemoForFilamentTests(t *testing.T) (*sql.DB, *ConfigStore, string) {
	t.Helper()
	dir := t.TempDir()
	db, _, store := openTestEnv(t, dir)
	if err := seedDemoData(db, store, dir); err != nil {
		t.Fatalf("seedDemoData: %v", err)
	}
	return db, store, dir
}

func TestDemoMaterialsFollowThePrimarySpool(t *testing.T) {
	materials, err := demoSeedMaterials()
	if err != nil {
		t.Fatalf("demoSeedMaterials: %v", err)
	}
	spoolMaterials := map[string]bool{}
	for _, s := range demoSpools {
		spoolMaterials[strings.ToLower(s.Filament.Material)] = true
	}
	if len(materials) != len(spoolMaterials) {
		t.Fatalf("%d demo materials for %d spool materials: want one each", len(materials), len(spoolMaterials))
	}
	for _, m := range materials {
		if !spoolMaterials[m.ID] {
			t.Errorf("demo material %q has no demo spool", m.ID)
			continue
		}
		sp, ok := demoPrimarySpool(m.ID)
		if !ok {
			t.Errorf("material %q: no primary spool", m.ID)
			continue
		}
		wantPerKg := sp.Price / sp.InitialWeight * 1000
		if math.Abs(m.PricePerKg-wantPerKg) > 0.005 {
			t.Errorf("material %q pricePerKg = %v, primary spool #%d is %v per kg", m.ID, m.PricePerKg, sp.ID, wantPerKg)
		}
		if m.Density != sp.Filament.Density {
			t.Errorf("material %q density = %v, primary spool #%d is %v", m.ID, m.Density, sp.ID, sp.Filament.Density)
		}
	}
	// The seeded config.yaml carries the same list, so the Filament page shows it.
	_, store, _ := seedDemoForFilamentTests(t)
	got := store.Get().Materials
	if len(got) != len(materials) {
		t.Fatalf("live materials = %+v, want %+v", got, materials)
	}
	for i := range got {
		if got[i] != materials[i] {
			t.Errorf("live material[%d] = %+v, want %+v", i, got[i], materials[i])
		}
	}
}

func TestDemoSeededQuotesUseDemoMaterialsAndSpools(t *testing.T) {
	db, store, _ := seedDemoForFilamentTests(t)
	byID := map[string]Material{}
	for _, m := range store.Get().Materials {
		byID[m.ID] = m
	}
	rows, err := db.Query(`SELECT label, inputs FROM quotes`)
	if err != nil {
		t.Fatalf("query quotes: %v", err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var label, raw string
		if err := rows.Scan(&label, &raw); err != nil {
			t.Fatal(err)
		}
		var stored struct {
			Inputs struct {
				Parts []struct {
					QuoteInputs
				} `json:"parts"`
			} `json:"inputs"`
			FormState struct {
				Parts []struct {
					MaterialID    string  `json:"materialId"`
					PricePerKg    float64 `json:"pricePerKg"`
					SpoolOverride *struct {
						Label      string  `json:"label"`
						PricePerKg float64 `json:"pricePerKg"`
						Density    float64 `json:"density"`
						SpoolID    int     `json:"spoolId"`
					} `json:"spoolOverride"`
				} `json:"parts"`
			} `json:"formState"`
		}
		if err := json.Unmarshal([]byte(raw), &stored); err != nil {
			t.Fatalf("quote %q: unmarshal: %v", label, err)
		}
		if len(stored.FormState.Parts) == 0 || len(stored.FormState.Parts) != len(stored.Inputs.Parts) {
			t.Fatalf("quote %q: %d formState parts vs %d input parts", label, len(stored.FormState.Parts), len(stored.Inputs.Parts))
		}
		for i, p := range stored.FormState.Parts {
			n++
			m, ok := byID[p.MaterialID]
			if !ok {
				t.Errorf("quote %q part %d uses material %q outside the demo list", label, i, p.MaterialID)
				continue
			}
			if p.PricePerKg != m.PricePerKg || float64(stored.Inputs.Parts[i].PricePerKg) != m.PricePerKg {
				t.Errorf("quote %q part %d prices %s at form %v / engine %v per kg, demo filament is %v", label, i, m.ID, p.PricePerKg, float64(stored.Inputs.Parts[i].PricePerKg), m.PricePerKg)
			}
			so := p.SpoolOverride
			if so == nil {
				t.Errorf("quote %q part %d (%s) records no spool", label, i, m.ID)
				continue
			}
			var spool *demoSpool
			for j := range demoSpools {
				if demoSpools[j].ID == so.SpoolID {
					spool = &demoSpools[j]
				}
			}
			if spool == nil || !strings.EqualFold(spool.Filament.Material, m.ID) {
				t.Errorf("quote %q part %d (%s) records spool #%d, which is not a spool of that material", label, i, m.ID, so.SpoolID)
				continue
			}
			if so.PricePerKg != p.PricePerKg || so.Density != spool.Filament.Density || so.Label != demoSpoolLabel(*spool) {
				t.Errorf("quote %q part %d spool override %+v disagrees with spool #%d", label, i, *so, spool.ID)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("no seeded quote parts")
	}
}

func TestDemoSeededRequestsMatchThePublicEstimate(t *testing.T) {
	db, store, dir := seedDemoForFilamentTests(t)
	cfg := store.Get()
	publicMux, _ := buildPublicMux(store, db, dir, false, false)

	rows, err := db.Query(`SELECT name, material, quantity, weight_g, time_seconds, price_cents FROM quote_requests WHERE kind = 'quote' OR kind = ''`)
	if err != nil {
		t.Fatalf("query requests: %v", err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var name, material string
		var qty, secs, price int
		var weight float64
		if err := rows.Scan(&name, &material, &qty, &weight, &secs, &price); err != nil {
			t.Fatal(err)
		}
		n++
		var m *Material
		for i := range cfg.Materials {
			if cfg.Materials[i].Name == material {
				m = &cfg.Materials[i]
			}
		}
		if m == nil {
			t.Errorf("request from %q uses material %q outside the demo list", name, material)
			continue
		}
		if !m.Public {
			t.Errorf("request from %q uses %q, which the demo shop does not offer publicly", name, material)
		}
		want, ok := publicEstimateCents(cfg, *m, qty, weight, secs)
		if !ok || price != want {
			t.Errorf("request from %q priced %d, public estimate computes %d (ok=%v)", name, price, want, ok)
		}
		// And the HTTP estimate a visitor sees, not only the shared helper.
		rec := postPublicQuotePreview(t, publicMux, map[string]any{"material": material, "quantity": qty, "weightG": weight, "timeSeconds": secs})
		if rec.Code != 200 {
			t.Errorf("request from %q: preview status %d: %s", name, rec.Code, rec.Body.String())
			continue
		}
		var resp publicQuotePreviewResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if resp.PriceCents != price {
			t.Errorf("request from %q priced %d, preview endpoint says %d", name, price, resp.PriceCents)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if n != len(demoRequestSpecs) {
		t.Fatalf("read %d seeded requests, want %d", n, len(demoRequestSpecs))
	}
}

// Invoice lines made from quotes carry the quote's own price, and the seeded item order totals follow item
// price plus option delta.
func TestDemoInvoiceQuoteLinesAndItemOrdersFollowTheirPrices(t *testing.T) {
	db, _, _ := seedDemoForFilamentTests(t)

	rows, err := db.Query(`SELECT l.unit_price_cents, COALESCE(q.resolved_price_cents, q.recommended_price_cents), q.label
		FROM invoice_lines l JOIN quotes q ON q.id = l.quote_id WHERE l.kind = 'item'`)
	if err != nil {
		t.Fatalf("query quote lines: %v", err)
	}
	n := 0
	for rows.Next() {
		var line int
		var quote sql.NullInt64
		var label string
		if err := rows.Scan(&line, &quote, &label); err != nil {
			t.Fatal(err)
		}
		n++
		if !quote.Valid || int64(line) != quote.Int64 {
			t.Errorf("invoice line for quote %q = %d, quote price %v", label, line, quote)
		}
	}
	rows.Close()
	if n == 0 {
		t.Fatal("no invoice lines reference a seeded quote")
	}

	irows, err := db.Query(`SELECT r.price_cents, r.quantity, r.option_name, i.price_cents, i.option_groups_json
		FROM quote_requests r JOIN items i ON i.id = r.item_id WHERE r.kind = 'item'`)
	if err != nil {
		t.Fatalf("query item orders: %v", err)
	}
	defer irows.Close()
	m := 0
	for irows.Next() {
		var total, qty, itemPrice int
		var option, groupsJSON string
		if err := irows.Scan(&total, &qty, &option, &itemPrice, &groupsJSON); err != nil {
			t.Fatal(err)
		}
		var groups []ItemOptionGroup
		if groupsJSON != "" {
			if err := json.Unmarshal([]byte(groupsJSON), &groups); err != nil {
				t.Fatalf("groups: %v", err)
			}
		}
		delta, ok := optionSummaryDelta(groups, option)
		if !ok {
			t.Errorf("item order option %q not in the item's groups", option)
		}
		m++
		if want := (itemPrice + delta) * qty; total != want {
			t.Errorf("item order total = %d, want (item %d + option %d) x %d = %d", total, itemPrice, delta, qty, want)
		}
	}
	if m == 0 {
		t.Fatal("no seeded item orders")
	}
}
