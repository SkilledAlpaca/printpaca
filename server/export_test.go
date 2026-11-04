package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newExportTestSetup is newTestMux plus the *sql.DB, to seed quote_requests rows by SQL (the admin API has no POST for requests).
func newExportTestSetup(t *testing.T) (*http.ServeMux, *sql.DB) {
	t.Helper()
	dir := t.TempDir()
	db := openTestDB(t, dir)
	cfg := loadTestConfig(t, dir)
	mux := routesMux(db, NewConfigStore(cfg), newAuthState("", nil), dir, false)
	return mux, db
}

func getExport(t *testing.T, mux *http.ServeMux) *httptest.ResponseRecorder {
	return serveReq(mux, httptest.NewRequest("GET", "/api/export", nil))
}

// Seeds one of each record kind and asserts the export carries them all, invoices with their nested lines/payments/sends as in GET /api/invoices/{id}.
func TestExportBundlesEverythingWithNestedInvoiceDetail(t *testing.T) {
	mux, db := newExportTestSetup(t)

	clientRec := postClient(t, mux, `{"name":"Beta Print","email":"beta@example.com"}`)
	if clientRec.Code != http.StatusCreated {
		t.Fatalf("create client: %d %s", clientRec.Code, clientRec.Body.String())
	}

	inv := newSentInvoice(t, mux, 10000) // also records a manual invoice_send
	payRec := postPayment(t, mux, inv.ID, `{"paidDate":"2026-01-15","amountCents":4000,"method":"cash"}`)
	if payRec.Code != http.StatusCreated {
		t.Fatalf("create payment: %d %s", payRec.Code, payRec.Body.String())
	}

	quoteRec := httptest.NewRecorder()
	mux.ServeHTTP(quoteRec, httptest.NewRequest("POST", "/api/quotes", strings.NewReader(
		`{"label":"Widget","clientName":"","project":"","reference":"","status":"draft","statusLog":[],`+
			`"inputs":{"parts":[{"label":"Part 1","weightG":10,"pricePerKg":20,"wastePct":0,"timeHours":1,"timeMinutes":0,"watts":100,`+
			`"energy":{"mode":"flat","flatRate":0.1},"failureRatePct":0,"prepMinutes":0,"laborRate":0,`+
			`"machineCost":0,"machineLifeHours":0,"monthlyFixed":0,"monthlyHours":0,"quantity":1}]},"options":{}}`)))
	if quoteRec.Code != http.StatusCreated {
		t.Fatalf("seed quote: %d %s", quoteRec.Code, quoteRec.Body.String())
	}
	var seededQuote Quote
	if err := json.Unmarshal(quoteRec.Body.Bytes(), &seededQuote); err != nil {
		t.Fatalf("unmarshal seeded quote: %v", err)
	}

	if _, err := db.Exec(`
		INSERT INTO quote_requests (id, status, name, email, note, material, quantity, weight_g, time_seconds, price_cents, gcode_filename, gcode_path, gcode_bytes, created_at)
		VALUES ('r1', 'new', 'Ada', 'ada@example.com', '', 'PLA', 1, 10, 100, 500, 'model.gcode', '/tmp/model.gcode.gz', 1234, '2026-01-02T00:00:00Z')`,
	); err != nil {
		t.Fatalf("seed request: %v", err)
	}

	itemRec := postItem(t, mux, `{"name":"Benchy","priceCents":1500,"public":true,`+
		`"optionGroups":[{"label":"Color","required":true,"options":[{"name":"Red","deltaCents":0}]}]}`)
	if itemRec.Code != http.StatusCreated {
		t.Fatalf("create item: %d %s", itemRec.Code, itemRec.Body.String())
	}

	rec := getExport(t, mux)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/export: %d %s", rec.Code, rec.Body.String())
	}

	cd := rec.Header().Get("Content-Disposition")
	if !strings.HasPrefix(cd, "attachment; filename=") || !strings.HasSuffix(cd, ".json\"") {
		t.Fatalf("Content-Disposition = %q, want attachment with a .json filename", cd)
	}

	var resp exportPayload
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal export: %v (body %s)", err, rec.Body.String())
	}

	if _, err := time.Parse(time.RFC3339, resp.ExportedAt); err != nil {
		t.Fatalf("exportedAt = %q, want RFC3339: %v", resp.ExportedAt, err)
	}
	if resp.AppVersion == "" {
		t.Fatalf("appVersion is empty")
	}
	if resp.FormatVersion != 1 {
		t.Fatalf("formatVersion = %d, want 1", resp.FormatVersion)
	}
	if !strings.Contains(rec.Body.String(), `"formatVersion":1`) {
		t.Fatalf("export body lacks the formatVersion key: %.200s", rec.Body.String())
	}
	if resp.Config == nil || len(resp.Config.Materials) == 0 {
		t.Fatalf("config missing or has no materials: %+v", resp.Config)
	}

	if len(resp.Clients) != 1 || resp.Clients[0].Name != "Beta Print" {
		t.Fatalf("clients = %+v, want one client named Beta Print", resp.Clients)
	}

	if len(resp.Invoices) != 1 {
		t.Fatalf("invoices len = %d, want 1", len(resp.Invoices))
	}
	got := resp.Invoices[0]
	if len(got.Lines) != 1 || got.Lines[0].AmountCents != 10000 {
		t.Fatalf("invoice lines = %+v, want one line amounting to 10000", got.Lines)
	}
	if len(got.Payments) != 1 || got.Payments[0].AmountCents != 4000 {
		t.Fatalf("invoice payments = %+v, want one payment of 4000", got.Payments)
	}
	if got.PaidCents != 4000 || got.BalanceCents != 6000 {
		t.Fatalf("paidCents/balanceCents = %d/%d, want 4000/6000", got.PaidCents, got.BalanceCents)
	}
	if len(got.Sends) != 1 || got.Sends[0].Channel != "manual" {
		t.Fatalf("invoice sends = %+v, want one manual send", got.Sends)
	}

	if len(resp.Quotes) != 1 {
		t.Fatalf("quotes len = %d, want 1", len(resp.Quotes))
	}
	if resp.Quotes[0].ID != seededQuote.ID || resp.Quotes[0].Label != "Widget" {
		t.Fatalf("quotes[0] = %+v, want id %q label Widget", resp.Quotes[0], seededQuote.ID)
	}

	if len(resp.Requests) != 1 {
		t.Fatalf("requests len = %d, want 1", len(resp.Requests))
	}
	req := resp.Requests[0]
	if req.ID != "r1" || req.Status != "new" || req.Name != "Ada" || req.Email != "ada@example.com" || req.CreatedAt != "2026-01-02T00:00:00Z" {
		t.Fatalf("requests[0] = %+v, want the seeded r1 metadata", req)
	}

	if len(resp.Items) != 1 || resp.Items[0].Name != "Benchy" {
		t.Fatalf("items = %+v, want one item named Benchy", resp.Items)
	}
	item := resp.Items[0]
	if item.PriceCents != 1500 || !item.Public || len(item.OptionGroups) != 1 || item.OptionGroups[0].Label != "Color" || item.OptionGroups[0].ID == "" || item.OptionGroups[0].Options[0].Name != "Red" {
		t.Fatalf("items[0] = %+v, want the seeded item's full fields", item)
	}
}

// Seeds items out of order including a non-public one; export bundles every item (admin-only, no public filter) in sort_order-then-name order like GET /api/items.
func TestExportIncludesNonPublicItemsOrderedBySortOrderThenName(t *testing.T) {
	mux, _ := newExportTestSetup(t)

	rec := postItem(t, mux, `{"name":"Zeta Item","priceCents":100,"sortOrder":1}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item Zeta: %d %s", rec.Code, rec.Body.String())
	}
	rec = postItem(t, mux, `{"name":"Alpha Item","priceCents":200,"sortOrder":1}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item Alpha: %d %s", rec.Code, rec.Body.String())
	}
	rec = postItem(t, mux, `{"name":"Hidden Item","priceCents":300,"sortOrder":0}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item Hidden: %d %s", rec.Code, rec.Body.String())
	}

	exportRec := getExport(t, mux)
	if exportRec.Code != http.StatusOK {
		t.Fatalf("GET /api/export: %d %s", exportRec.Code, exportRec.Body.String())
	}
	var resp exportPayload
	if err := json.Unmarshal(exportRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal export: %v", err)
	}

	if len(resp.Items) != 3 {
		t.Fatalf("items len = %d, want 3", len(resp.Items))
	}
	var names []string
	for _, it := range resp.Items {
		names = append(names, it.Name)
		if it.Public {
			t.Fatalf("item %q unexpectedly public (none of the seeded items were)", it.Name)
		}
	}
	want := []string{"Hidden Item", "Alpha Item", "Zeta Item"}
	if names[0] != want[0] || names[1] != want[1] || names[2] != want[2] {
		t.Fatalf("items order = %v, want %v (sort_order then name)", names, want)
	}
}

// The request bundle must never leak stored G-code filesystem details; check the raw response bytes so a future field or a switch to serving QuoteRequest can't reintroduce it.
func TestExportRequestsExcludeGcode(t *testing.T) {
	mux, db := newExportTestSetup(t)

	if _, err := db.Exec(`
		INSERT INTO quote_requests (id, status, name, email, note, material, quantity, weight_g, time_seconds, price_cents, gcode_filename, gcode_path, gcode_bytes, created_at)
		VALUES ('r1', 'new', 'Ada', 'ada@example.com', '', 'PLA', 1, 10, 100, 500, 'secret-model.gcode', '/data/uploads/secret-model.gcode.gz', 999999, '2026-01-02T00:00:00Z')`,
	); err != nil {
		t.Fatalf("seed request: %v", err)
	}

	rec := getExport(t, mux)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/export: %d %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	for _, needle := range []string{"gcodeFilename", "gcodePath", "gcodeBytes", "secret-model.gcode", "/data/uploads"} {
		if strings.Contains(body, needle) {
			t.Fatalf("export body unexpectedly contains %q", needle)
		}
	}
}

// Every exported record shape carries createdBy/updatedBy; quote_requests has no created_by, so it exports just updatedBy.
func TestExportIncludesAttributionColumns(t *testing.T) {
	mux, db := newExportTestSetup(t)
	if _, _, err := createUser(db, "flo", "password123", []string{"clients", "items"}, ""); err != nil {
		t.Fatalf("createUser flo: %v", err)
	}
	if _, _, err := createUser(db, "gus", "password123", []string{"invoices"}, ""); err != nil {
		t.Fatalf("createUser gus: %v", err)
	}
	if _, _, err := createUser(db, "mia", "password123", []string{"invoices"}, ""); err != nil {
		t.Fatalf("createUser mia: %v", err)
	}
	if _, _, err := createUser(db, "bob", "password123", []string{"quotes"}, ""); err != nil {
		t.Fatalf("createUser bob: %v", err)
	}

	clientReq := withCtxIdentity(httptest.NewRequest("POST", "/api/clients", strings.NewReader(`{"name":"Beta Print"}`)), identity{username: "flo"})
	clientRec := serveReq(mux, clientReq)
	if clientRec.Code != http.StatusCreated {
		t.Fatalf("create client: %d %s", clientRec.Code, clientRec.Body.String())
	}

	invReq := withCtxIdentity(httptest.NewRequest("POST", "/api/invoices", strings.NewReader(`{"clientName":"Walk-in","lines":[{"description":"Item","quantity":1,"unitPriceCents":10000}]}`)), identity{username: "gus"})
	invRec := serveReq(mux, invReq)
	if invRec.Code != http.StatusCreated {
		t.Fatalf("create invoice: %d %s", invRec.Code, invRec.Body.String())
	}
	inv := mustUnmarshalInvoice(t, invRec)
	statusReq := withCtxIdentity(httptest.NewRequest("POST", "/api/invoices/"+inv.ID+"/status", strings.NewReader(`{"status":"sent"}`)), identity{username: "gus"})
	statusRec := serveReq(mux, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("send invoice: %d %s", statusRec.Code, statusRec.Body.String())
	}
	payReq := withCtxIdentity(httptest.NewRequest("POST", "/api/invoices/"+inv.ID+"/payments", strings.NewReader(`{"paidDate":"2026-01-15","amountCents":4000,"method":"cash"}`)), identity{username: "mia"})
	payRec := serveReq(mux, payReq)
	if payRec.Code != http.StatusCreated {
		t.Fatalf("create payment: %d %s", payRec.Code, payRec.Body.String())
	}

	quoteReq := withCtxIdentity(httptest.NewRequest("POST", "/api/quotes", strings.NewReader(quoteBodyFor(""))), identity{username: "bob"})
	quoteRec := serveReq(mux, quoteReq)
	if quoteRec.Code != http.StatusCreated {
		t.Fatalf("create quote: %d %s", quoteRec.Code, quoteRec.Body.String())
	}

	itemReq := withCtxIdentity(httptest.NewRequest("POST", "/api/items", strings.NewReader(`{"name":"Benchy","priceCents":1500}`)), identity{username: "flo"})
	itemRec := serveReq(mux, itemReq)
	if itemRec.Code != http.StatusCreated {
		t.Fatalf("create item: %d %s", itemRec.Code, itemRec.Body.String())
	}

	if _, err := db.Exec(`
		INSERT INTO quote_requests (id, status, name, email, note, material, quantity, weight_g, time_seconds, price_cents, gcode_filename, gcode_path, gcode_bytes, created_at, updated_by)
		VALUES ('r1', 'viewed', 'Ada', 'ada@example.com', '', 'PLA', 1, 10, 100, 500, '', '', 0, '2026-01-02T00:00:00Z', 'mallory')`,
	); err != nil {
		t.Fatalf("seed request: %v", err)
	}

	rec := getExport(t, mux)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/export: %d %s", rec.Code, rec.Body.String())
	}
	var resp exportPayload
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal export: %v (body %s)", err, rec.Body.String())
	}

	if len(resp.Clients) != 1 || resp.Clients[0].CreatedBy != "flo" || resp.Clients[0].UpdatedBy != "flo" {
		t.Fatalf("clients = %+v, want one client created/updated by flo", resp.Clients)
	}
	if len(resp.Invoices) != 1 {
		t.Fatalf("invoices len = %d, want 1", len(resp.Invoices))
	}
	gotInv := resp.Invoices[0]
	if gotInv.CreatedBy != "gus" || gotInv.UpdatedBy != "gus" {
		t.Fatalf("invoice createdBy=%q updatedBy=%q, want both gus", gotInv.CreatedBy, gotInv.UpdatedBy)
	}
	if len(gotInv.Payments) != 1 || gotInv.Payments[0].CreatedBy != "mia" || gotInv.Payments[0].UpdatedBy != "mia" {
		t.Fatalf("invoice payments = %+v, want one payment created/updated by mia", gotInv.Payments)
	}
	if len(resp.Quotes) != 1 || resp.Quotes[0].CreatedBy != "bob" || resp.Quotes[0].UpdatedBy != "bob" {
		t.Fatalf("quotes = %+v, want one quote created/updated by bob", resp.Quotes)
	}
	if len(resp.Items) != 1 || resp.Items[0].CreatedBy != "flo" || resp.Items[0].UpdatedBy != "flo" {
		t.Fatalf("items = %+v, want one item created/updated by flo", resp.Items)
	}
	if len(resp.Requests) != 1 || resp.Requests[0].UpdatedBy != "mallory" {
		t.Fatalf("requests = %+v, want the seeded request's updatedBy mallory", resp.Requests)
	}
}

// The export carries each invoice line's kind, and the totals it reports keep a shipping line out of tax.
func TestExportIncludesInvoiceLineKind(t *testing.T) {
	mux, _ := newExportTestSetup(t)
	rec := postInvoice(t, mux, `{"clientName":"A","taxBp":1000,"lines":[`+
		`{"description":"Widget","quantity":1,"unitPriceCents":1000},`+
		`{"description":"Postage","kind":"shipping","quantity":1,"unitPriceCents":500}]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create invoice: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Invoices []Invoice `json:"invoices"`
	}
	exp := getExport(t, mux)
	if err := json.Unmarshal(exp.Body.Bytes(), &out); err != nil || len(out.Invoices) != 1 {
		t.Fatalf("export invoices = %d (%v)", len(out.Invoices), err)
	}
	lines := out.Invoices[0].Lines
	if len(lines) != 2 || lines[0].Kind != "item" || lines[1].Kind != "shipping" {
		t.Errorf("export line kinds = %+v, want item then shipping", lines)
	}
	if !strings.Contains(exp.Body.String(), `"kind":"shipping"`) {
		t.Error(`export JSON lacks "kind":"shipping"`)
	}
	if out.Invoices[0].TaxCents != 100 {
		t.Errorf("taxCents = %d, want 100 (10%% of the 1000c item line only)", out.Invoices[0].TaxCents)
	}
}
