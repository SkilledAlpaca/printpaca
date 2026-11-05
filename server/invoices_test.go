package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type invoicesListResponse struct {
	Invoices []InvoiceSummary `json:"invoices"`
}

func postInvoice(t *testing.T, mux *http.ServeMux, body string) *httptest.ResponseRecorder {
	return serveReq(mux, httptest.NewRequest("POST", "/api/invoices", strings.NewReader(body)))
}

func putInvoice(t *testing.T, mux *http.ServeMux, id, body string) *httptest.ResponseRecorder {
	return serveReq(mux, httptest.NewRequest("PUT", "/api/invoices/"+id, strings.NewReader(body)))
}

func getInvoice(t *testing.T, mux *http.ServeMux, id string) *httptest.ResponseRecorder {
	return serveReq(mux, httptest.NewRequest("GET", "/api/invoices/"+id, nil))
}

func setInvoiceStatus(t *testing.T, mux *http.ServeMux, id, status string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	body := fmt.Sprintf(`{"status":%q}`, status)
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/invoices/"+id+"/status", strings.NewReader(body)))
	return rec
}

func deleteInvoice(t *testing.T, mux *http.ServeMux, id string) *httptest.ResponseRecorder {
	return serveReq(mux, httptest.NewRequest("DELETE", "/api/invoices/"+id, nil))
}

func mustUnmarshalInvoice(t *testing.T, rec *httptest.ResponseRecorder) Invoice {
	t.Helper()
	var inv Invoice
	if err := json.Unmarshal(rec.Body.Bytes(), &inv); err != nil {
		t.Fatalf("unmarshal invoice: %v (body %s)", err, rec.Body.String())
	}
	return inv
}

// ---------------------------------------------------------------------------
// computeTotals vectors
// ---------------------------------------------------------------------------

// invoiceTotalsVector is one row of testdata/invoice-totals.json, the vector table web/src/lib/engine/invoice.test.ts
// reads too, so the Go and TypeScript totals can't drift. It pins one computeTotals call to an expected result.
type invoiceTotalsVector struct {
	Name                 string             `json:"name"`
	Lines                []invoiceLineInput `json:"lines"`
	InvoiceDiscountCents int                `json:"invoiceDiscountCents"`
	TaxBp                int                `json:"taxBp"`
	LineAmounts          []int              `json:"lineAmounts"`
	SubtotalCents        int                `json:"subtotalCents"`
	TaxCents             int                `json:"taxCents"`
	TotalCents           int                `json:"totalCents"`
}

func loadInvoiceTotalsVectors(t *testing.T) []invoiceTotalsVector {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "testdata", "invoice-totals.json"))
	if err != nil {
		t.Fatalf("reading shared invoice-totals fixture: %v", err)
	}
	var vs []invoiceTotalsVector
	if err := json.Unmarshal(data, &vs); err != nil {
		t.Fatalf("parsing shared invoice-totals fixture: %v", err)
	}
	if len(vs) == 0 {
		t.Fatal("shared invoice-totals fixture is empty")
	}
	return vs
}

func TestComputeTotalsVectors(t *testing.T) {
	for _, v := range loadInvoiceTotalsVectors(t) {
		t.Run(v.Name, func(t *testing.T) {
			got := computeTotals(v.Lines, v.InvoiceDiscountCents, v.TaxBp)
			if !reflect.DeepEqual(got.LineAmounts, v.LineAmounts) {
				t.Errorf("lineAmounts = %v, want %v", got.LineAmounts, v.LineAmounts)
			}
			if got.SubtotalCents != v.SubtotalCents {
				t.Errorf("subtotalCents = %d, want %d", got.SubtotalCents, v.SubtotalCents)
			}
			if got.TaxCents != v.TaxCents {
				t.Errorf("taxCents = %d, want %d", got.TaxCents, v.TaxCents)
			}
			if got.TotalCents != v.TotalCents {
				t.Errorf("totalCents = %d, want %d", got.TotalCents, v.TotalCents)
			}
		})
	}
}

func TestRoundHalfUpEpsilon(t *testing.T) {
	for _, c := range []struct {
		in   float64
		want int
	}{{1.005 * 100, 101}, {100.5, 101}, {100.4, 100}, {0.5, 1}, {0, 0}} {
		if got := roundHalfUp(c.in); got != c.want {
			t.Errorf("roundHalfUp(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestCreateInvoiceMatchesVector round-trips one totals vector through the
// live API to confirm the stored/served totals agree with computeTotals, not
// just the unit test above.
func TestCreateInvoiceMatchesVector(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	body := `{"clientName":"Walk-in","taxBp":825,"lines":[{"description":"Widget","quantity":1,"unitPriceCents":10000}]}`
	rec := postInvoice(t, mux, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST want 201, got %d %s", rec.Code, rec.Body.String())
	}
	inv := mustUnmarshalInvoice(t, rec)
	if inv.SubtotalCents != 10000 || inv.TaxCents != 825 || inv.TotalCents != 10825 {
		t.Fatalf("totals = %+v, want subtotal 10000, tax 825, total 10825", inv)
	}
	if len(inv.Lines) != 1 || inv.Lines[0].AmountCents != 10000 {
		t.Fatalf("line amountCents = %+v, want 10000", inv.Lines)
	}
}

// The quote-conversion fallback-tax path taxes only the product line, never the auto-added Shipping line
// (mirrors convertQuote's convertPreview test). taxBp is omitted so the default 8% applies to the 2500c product line, not the 1000c Shipping.
func TestCreateInvoiceQuoteConversionShippingUntaxed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("invoicing:\n  salesTaxPct: 8\n"), 0o644); err != nil {
		t.Fatalf("write config.yaml: %v", err)
	}
	mux := newTestMux(t, dir)
	body := `{"clientName":"Walk-in","lines":[
		{"description":"Widget","quantity":2,"unitPriceCents":1250,"quoteId":"q1"},
		{"description":"Shipping","kind":"shipping","quantity":1,"unitPriceCents":1000,"quoteId":"q1"}
	]}`
	rec := postInvoice(t, mux, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST want 201, got %d %s", rec.Code, rec.Body.String())
	}
	inv := mustUnmarshalInvoice(t, rec)
	if inv.TaxBp != 800 {
		t.Fatalf("taxBp = %d, want 800 (shop default)", inv.TaxBp)
	}
	if inv.SubtotalCents != 3500 {
		t.Fatalf("subtotalCents = %d, want 3500", inv.SubtotalCents)
	}
	if inv.TaxCents != 200 {
		t.Fatalf("taxCents = %d, want 200 (8%% of the 2500c product line only)", inv.TaxCents)
	}
	if inv.TotalCents != 3700 {
		t.Fatalf("totalCents = %d, want 3700 (2500 product + 200 tax + 1000 shipping)", inv.TotalCents)
	}
}

// The tax exemption keys off the line kind, not the description: a "Shipping" line without kind "shipping" is
// taxed, and a differently described line with kind "shipping" is exempt.
func TestCreateInvoiceShippingExemptionKeysOffKind(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("invoicing:\n  salesTaxPct: 8\n"), 0o644); err != nil {
		t.Fatalf("write config.yaml: %v", err)
	}
	mux := newTestMux(t, dir)
	for _, c := range []struct {
		name, line string
		tax, total int
	}{
		{"described Shipping, no kind", `{"description":"Shipping","quantity":1,"unitPriceCents":1000}`, 280, 3780},
		{"kind shipping, other description", `{"description":"Postage","kind":"shipping","quantity":1,"unitPriceCents":1000}`, 200, 3700},
	} {
		t.Run(c.name, func(t *testing.T) {
			body := `{"clientName":"Walk-in","lines":[{"description":"Widget","quantity":2,"unitPriceCents":1250},` + c.line + `]}`
			rec := postInvoice(t, mux, body)
			if rec.Code != http.StatusCreated {
				t.Fatalf("POST want 201, got %d %s", rec.Code, rec.Body.String())
			}
			inv := mustUnmarshalInvoice(t, rec)
			if inv.TaxCents != c.tax || inv.TotalCents != c.total {
				t.Fatalf("tax/total = %d/%d, want %d/%d", inv.TaxCents, inv.TotalCents, c.tax, c.total)
			}
			if inv.Lines[0].Kind != "item" {
				t.Errorf("default kind = %q, want item", inv.Lines[0].Kind)
			}
		})
	}
}

func TestCreateInvoiceRejectsUnknownLineKind(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	rec := postInvoice(t, mux, `{"clientName":"X","lines":[{"description":"W","kind":"fee","quantity":1,"unitPriceCents":100}]}`)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "kind: must be item or shipping") {
		t.Fatalf("want 422 naming kind, got %d %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Client snapshot / freeform
// ---------------------------------------------------------------------------

func TestCreateInvoiceSnapshotsClientAndSurvivesDeletion(t *testing.T) {
	mux := newTestMux(t, t.TempDir())

	rec := postClient(t, mux, `{"name":"Beta Print","email":"beta@example.com","billingAddress":"1 Main"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create client: %d %s", rec.Code, rec.Body.String())
	}
	var cl Client
	if err := json.Unmarshal(rec.Body.Bytes(), &cl); err != nil {
		t.Fatalf("unmarshal client: %v", err)
	}

	body := fmt.Sprintf(`{"clientId":%q,"lines":[{"description":"Widget","quantity":1,"unitPriceCents":100}]}`, cl.ID)
	rec = postInvoice(t, mux, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST want 201, got %d %s", rec.Code, rec.Body.String())
	}
	created := mustUnmarshalInvoice(t, rec)
	if created.ClientID != cl.ID || created.ClientName != "Beta Print" || created.ClientEmail != "beta@example.com" || created.ClientBillingAddress != "1 Main" {
		t.Fatalf("snapshot mismatch: %+v", created)
	}

	rec = serveReq(mux, httptest.NewRequest("DELETE", "/api/clients/"+cl.ID, nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete client: %d", rec.Code)
	}

	rec = getInvoice(t, mux, created.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET after client delete: %d %s", rec.Code, rec.Body.String())
	}
	after := mustUnmarshalInvoice(t, rec)
	if after.ClientID != cl.ID || after.ClientName != "Beta Print" || after.ClientEmail != "beta@example.com" {
		t.Fatalf("snapshot lost after client delete: %+v", after)
	}
}

func TestCreateInvoiceFreeformClientName(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	rec := postInvoice(t, mux, `{"clientName":"Walk-in","lines":[]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST want 201, got %d %s", rec.Code, rec.Body.String())
	}
	inv := mustUnmarshalInvoice(t, rec)
	if inv.ClientID != "" || inv.ClientName != "Walk-in" {
		t.Fatalf("freeform client mismatch: %+v", inv)
	}
}

func TestCreateInvoiceWithoutClientOrNameIs422(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	rec := postInvoice(t, mux, `{"lines":[]}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d %s", rec.Code, rec.Body.String())
	}
	var errBody apiErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &errBody); err != nil {
		t.Fatalf("unmarshal errors: %v", err)
	}
	if !containsStr(errBody.Errors, "clientName: required") {
		t.Fatalf("errors = %v, want to contain clientName: required", errBody.Errors)
	}
}

// ---------------------------------------------------------------------------
// Numbering
// ---------------------------------------------------------------------------

func TestInvoiceNumberingSequenceWithinYear(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	year := time.Now().UTC().Year()

	rec1 := postInvoice(t, mux, `{"clientName":"A","lines":[]}`)
	rec2 := postInvoice(t, mux, `{"clientName":"B","lines":[]}`)
	if rec1.Code != http.StatusCreated || rec2.Code != http.StatusCreated {
		t.Fatalf("create failed: %d %s / %d %s", rec1.Code, rec1.Body.String(), rec2.Code, rec2.Body.String())
	}
	inv1 := mustUnmarshalInvoice(t, rec1)
	inv2 := mustUnmarshalInvoice(t, rec2)

	want1 := fmt.Sprintf("INV-%d-0001", year)
	want2 := fmt.Sprintf("INV-%d-0002", year)
	if inv1.Number != want1 {
		t.Errorf("first number = %q, want %q", inv1.Number, want1)
	}
	if inv2.Number != want2 {
		t.Errorf("second number = %q, want %q", inv2.Number, want2)
	}
}

func TestInvoicesYearSeqUniqueIndexEnforced(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)

	now := time.Now().UTC().Format(time.RFC3339)
	insert := func(id string) error {
		_, err := db.Exec(`
			INSERT INTO invoices (id, number, number_year, number_seq, status, client_name, created_at, updated_at)
			VALUES (?, ?, 2026, 1, 'draft', 'x', ?, ?)`, id, "INV-2026-000"+id, now, now)
		return err
	}
	if err := insert("1"); err != nil {
		t.Fatalf("first insert should succeed: %v", err)
	}
	if err := insert("2"); err == nil {
		t.Fatalf("second insert with duplicate (year, seq) should fail the UNIQUE index")
	}
}

// ---------------------------------------------------------------------------
// PUT
// ---------------------------------------------------------------------------

func TestPutInvoiceReplacesLinesAndRecomputes(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	rec := postInvoice(t, mux, `{"clientName":"A","taxBp":1000,"lines":[{"description":"One","quantity":1,"unitPriceCents":100}]}`)
	created := mustUnmarshalInvoice(t, rec)
	if created.SubtotalCents != 100 {
		t.Fatalf("initial subtotal = %d, want 100", created.SubtotalCents)
	}

	rec = putInvoice(t, mux, created.ID, `{"clientName":"A","taxBp":1000,"lines":[
		{"description":"Two","quantity":2,"unitPriceCents":300},
		{"description":"Three","quantity":1,"unitPriceCents":50}
	]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT want 200, got %d %s", rec.Code, rec.Body.String())
	}
	updated := mustUnmarshalInvoice(t, rec)
	if len(updated.Lines) != 2 {
		t.Fatalf("lines = %+v, want 2", updated.Lines)
	}
	// subtotal = 2*300 + 1*50 = 650; tax = round(650*1000/10000) = 65
	if updated.SubtotalCents != 650 {
		t.Errorf("subtotal = %d, want 650", updated.SubtotalCents)
	}
	if updated.TaxCents != 65 {
		t.Errorf("tax = %d, want 65", updated.TaxCents)
	}
	if updated.TotalCents != 715 {
		t.Errorf("total = %d, want 715", updated.TotalCents)
	}
}

// A converted invoice with a Shipping line, saved unchanged, must keep identical totals: the update path must use
// computeConvertTotals like create's default-tax fallback, not plain computeTotals.
func TestPutConvertedInvoiceNoOpKeepsShippingExempt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("invoicing:\n  salesTaxPct: 10\n"), 0o644); err != nil {
		t.Fatalf("write config.yaml: %v", err)
	}
	mux := newTestMux(t, dir)
	body := `{"clientName":"Walk-in","lines":[
		{"description":"Widget","quantity":1,"unitPriceCents":10000,"quoteId":"q1"},
		{"description":"Shipping","kind":"shipping","quantity":1,"unitPriceCents":1000,"quoteId":"q1"}
	]}`
	rec := postInvoice(t, mux, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST want 201, got %d %s", rec.Code, rec.Body.String())
	}
	created := mustUnmarshalInvoice(t, rec)
	// subtotal = 10000 + 1000 = 11000; tax = 10% of the 10000c Widget line
	// only = 1000, never the 1000c Shipping line; total = 11000 + 1000 = 12000.
	if created.SubtotalCents != 11000 || created.TaxCents != 1000 || created.TotalCents != 12000 {
		t.Fatalf("created totals = %+v, want subtotal 11000, tax 1000, total 12000", created.InvoiceSummary)
	}

	rec = putInvoice(t, mux, created.ID, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT want 200, got %d %s", rec.Code, rec.Body.String())
	}
	updated := mustUnmarshalInvoice(t, rec)
	if updated.SubtotalCents != created.SubtotalCents || updated.TaxCents != created.TaxCents || updated.TotalCents != created.TotalCents {
		t.Fatalf("no-op PUT changed totals: created %+v, updated %+v", created.InvoiceSummary, updated.InvoiceSummary)
	}
}

// TestPutConvertedInvoiceQuantityEditKeepsShippingExempt confirms editing a
// converted invoice's non-shipping line still exempts the Shipping line from
// tax after save, not just on creation.
func TestPutConvertedInvoiceQuantityEditKeepsShippingExempt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("invoicing:\n  salesTaxPct: 10\n"), 0o644); err != nil {
		t.Fatalf("write config.yaml: %v", err)
	}
	mux := newTestMux(t, dir)
	body := `{"clientName":"Walk-in","lines":[
		{"description":"Widget","quantity":1,"unitPriceCents":10000,"quoteId":"q1"},
		{"description":"Shipping","kind":"shipping","quantity":1,"unitPriceCents":1000,"quoteId":"q1"}
	]}`
	rec := postInvoice(t, mux, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST want 201, got %d %s", rec.Code, rec.Body.String())
	}
	created := mustUnmarshalInvoice(t, rec)

	rec = putInvoice(t, mux, created.ID, `{"clientName":"Walk-in","lines":[
		{"description":"Widget","quantity":3,"unitPriceCents":10000,"quoteId":"q1"},
		{"description":"Shipping","kind":"shipping","quantity":1,"unitPriceCents":1000,"quoteId":"q1"}
	]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT want 200, got %d %s", rec.Code, rec.Body.String())
	}
	updated := mustUnmarshalInvoice(t, rec)
	// subtotal = 3*10000 + 1000 = 31000; tax = 10% of the 30000c Widget line
	// only = 3000, never the 1000c Shipping line; total = 31000 + 3000 = 34000.
	if updated.SubtotalCents != 31000 {
		t.Errorf("subtotal = %d, want 31000", updated.SubtotalCents)
	}
	if updated.TaxCents != 3000 {
		t.Errorf("tax = %d, want 3000 (shipping stays exempt after the edit)", updated.TaxCents)
	}
	if updated.TotalCents != 34000 {
		t.Errorf("total = %d, want 34000", updated.TotalCents)
	}
}

func TestPutSentInvoiceIs409(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	rec := postInvoice(t, mux, `{"clientName":"A","lines":[]}`)
	created := mustUnmarshalInvoice(t, rec)

	rec = setInvoiceStatus(t, mux, created.ID, "sent")
	if rec.Code != http.StatusOK {
		t.Fatalf("mark sent: %d %s", rec.Code, rec.Body.String())
	}

	rec = putInvoice(t, mux, created.ID, `{"clientName":"A","lines":[]}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("PUT on sent invoice want 409, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestPutInvoiceReSnapshotsOnClientChange(t *testing.T) {
	mux := newTestMux(t, t.TempDir())

	rec := postClient(t, mux, `{"name":"Client One","email":"one@example.com"}`)
	var c1 Client
	json.Unmarshal(rec.Body.Bytes(), &c1)
	rec = postClient(t, mux, `{"name":"Client Two","email":"two@example.com"}`)
	var c2 Client
	json.Unmarshal(rec.Body.Bytes(), &c2)

	rec = postInvoice(t, mux, fmt.Sprintf(`{"clientId":%q,"lines":[]}`, c1.ID))
	created := mustUnmarshalInvoice(t, rec)
	if created.ClientName != "Client One" {
		t.Fatalf("initial snapshot = %q, want Client One", created.ClientName)
	}

	rec = putInvoice(t, mux, created.ID, fmt.Sprintf(`{"clientId":%q,"lines":[]}`, c2.ID))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT want 200, got %d %s", rec.Code, rec.Body.String())
	}
	updated := mustUnmarshalInvoice(t, rec)
	if updated.ClientID != c2.ID || updated.ClientName != "Client Two" || updated.ClientEmail != "two@example.com" {
		t.Fatalf("re-snapshot mismatch: %+v", updated)
	}
}

// ---------------------------------------------------------------------------
// Status transitions
// ---------------------------------------------------------------------------

func TestInvoiceStatusAllowedTransitions(t *testing.T) {
	cases := []struct {
		name string
		path []string // status sequence to reach the "from" state, then the target
	}{
		{"draft to sent", []string{"sent"}},
		{"draft to canceled", []string{"canceled"}},
		{"draft to sent to paid", []string{"sent", "paid"}},
		{"draft to sent to canceled", []string{"sent", "canceled"}},
		{"draft to sent to paid to sent (correction)", []string{"sent", "paid", "sent"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux := newTestMux(t, t.TempDir())
			rec := postInvoice(t, mux, `{"clientName":"A","lines":[]}`)
			created := mustUnmarshalInvoice(t, rec)

			var last *httptest.ResponseRecorder
			for _, status := range tc.path {
				last = setInvoiceStatus(t, mux, created.ID, status)
				if last.Code != http.StatusOK {
					t.Fatalf("transition to %s failed: %d %s", status, last.Code, last.Body.String())
				}
			}
			final := mustUnmarshalInvoice(t, last)
			if final.Status != tc.path[len(tc.path)-1] {
				t.Fatalf("final status = %q, want %q", final.Status, tc.path[len(tc.path)-1])
			}
		})
	}
}

func TestInvoiceStatusForbiddenTransitions(t *testing.T) {
	cases := []struct {
		name string
		path []string // statuses to reach the starting state
		to   string   // forbidden target from that state
	}{
		{"draft to paid directly", nil, "paid"},
		{"canceled to sent", []string{"canceled"}, "sent"},
		{"paid to canceled", []string{"sent", "paid"}, "canceled"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux := newTestMux(t, t.TempDir())
			rec := postInvoice(t, mux, `{"clientName":"A","lines":[]}`)
			created := mustUnmarshalInvoice(t, rec)

			for _, status := range tc.path {
				rec := setInvoiceStatus(t, mux, created.ID, status)
				if rec.Code != http.StatusOK {
					t.Fatalf("setup transition to %s failed: %d %s", status, rec.Code, rec.Body.String())
				}
			}

			rec = setInvoiceStatus(t, mux, created.ID, tc.to)
			if rec.Code != http.StatusConflict {
				t.Fatalf("transition to %s want 409, got %d %s", tc.to, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestMarkSentFillsDatesFromConfigDueDays(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	rec := postInvoice(t, mux, `{"clientName":"A","lines":[]}`)
	created := mustUnmarshalInvoice(t, rec)
	if created.IssueDate != "" || created.DueDate != "" {
		t.Fatalf("new draft should have empty dates: %+v", created)
	}

	rec = setInvoiceStatus(t, mux, created.ID, "sent")
	if rec.Code != http.StatusOK {
		t.Fatalf("mark sent: %d %s", rec.Code, rec.Body.String())
	}
	sent := mustUnmarshalInvoice(t, rec)

	wantIssue := time.Now().UTC().Format("2006-01-02")
	if sent.IssueDate != wantIssue {
		t.Errorf("issueDate = %q, want %q", sent.IssueDate, wantIssue)
	}
	issueParsed, err := time.Parse("2006-01-02", sent.IssueDate)
	if err != nil {
		t.Fatalf("parse issueDate: %v", err)
	}
	wantDue := issueParsed.AddDate(0, 0, 30).Format("2006-01-02") // defaults.yaml dueDays: 30
	if sent.DueDate != wantDue {
		t.Errorf("dueDate = %q, want %q (issueDate + 30 days)", sent.DueDate, wantDue)
	}
}

// ---------------------------------------------------------------------------
// DELETE
// ---------------------------------------------------------------------------

func TestDeleteDraftInvoiceIs204DeleteSentIs409(t *testing.T) {
	mux := newTestMux(t, t.TempDir())

	rec := postInvoice(t, mux, `{"clientName":"A","lines":[]}`)
	draft := mustUnmarshalInvoice(t, rec)
	rec = deleteInvoice(t, mux, draft.ID)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete draft want 204, got %d %s", rec.Code, rec.Body.String())
	}
	rec = getInvoice(t, mux, draft.ID)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get deleted draft want 404, got %d", rec.Code)
	}

	rec = postInvoice(t, mux, `{"clientName":"B","lines":[]}`)
	sent := mustUnmarshalInvoice(t, rec)
	rec = setInvoiceStatus(t, mux, sent.ID, "sent")
	if rec.Code != http.StatusOK {
		t.Fatalf("mark sent: %d %s", rec.Code, rec.Body.String())
	}
	rec = deleteInvoice(t, mux, sent.ID)
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete sent want 409, got %d %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// List ordering
// ---------------------------------------------------------------------------

func TestInvoicesListOrdering(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	rec1 := postInvoice(t, mux, `{"clientName":"A","lines":[]}`)
	rec2 := postInvoice(t, mux, `{"clientName":"B","lines":[]}`)
	rec3 := postInvoice(t, mux, `{"clientName":"C","lines":[]}`)
	inv1 := mustUnmarshalInvoice(t, rec1)
	inv2 := mustUnmarshalInvoice(t, rec2)
	inv3 := mustUnmarshalInvoice(t, rec3)

	rec := serveReq(mux, httptest.NewRequest("GET", "/api/invoices", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET want 200, got %d", rec.Code)
	}
	var list invoicesListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(list.Invoices) != 3 {
		t.Fatalf("invoices len = %d, want 3", len(list.Invoices))
	}
	want := []string{inv3.Number, inv2.Number, inv1.Number} // newest (highest seq) first
	got := []string{list.Invoices[0].Number, list.Invoices[1].Number, list.Invoices[2].Number}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	// InvoiceSummary has no Lines field at all, so "without lines" is
	// enforced at compile time here; TestInvoicesListBodyOmitsLines below
	// also checks the wire format directly.
}

func TestInvoicesListBodyOmitsLines(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	postInvoice(t, mux, `{"clientName":"A","lines":[{"description":"X","quantity":1,"unitPriceCents":100}]}`)

	rec := serveReq(mux, httptest.NewRequest("GET", "/api/invoices", nil))
	if strings.Contains(rec.Body.String(), `"lines"`) {
		t.Fatalf("list body should not mention lines: %s", rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Pagination
// ---------------------------------------------------------------------------

// TestInvoicesListPaginationLimit pins ?limit=: capped rows in the existing
// (newest-first) order, plus X-Total-Count naming the full row count.
func TestInvoicesListPaginationLimit(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	postInvoice(t, mux, `{"clientName":"A","lines":[]}`)
	postInvoice(t, mux, `{"clientName":"B","lines":[]}`)
	rec3 := postInvoice(t, mux, `{"clientName":"C","lines":[]}`)
	inv3 := mustUnmarshalInvoice(t, rec3)

	rec := serveReq(mux, httptest.NewRequest("GET", "/api/invoices?limit=1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if got, want := rec.Header().Get("X-Total-Count"), "3"; got != want {
		t.Errorf("X-Total-Count = %q, want %q", got, want)
	}
	var list invoicesListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(list.Invoices) != 1 || list.Invoices[0].Number != inv3.Number {
		t.Fatalf("invoices = %+v, want the single newest invoice (%s)", list.Invoices, inv3.Number)
	}
}

// TestInvoicesListPaginationOffset pins ?offset=: skips rows in the existing
// order and still sets X-Total-Count.
func TestInvoicesListPaginationOffset(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	rec1 := postInvoice(t, mux, `{"clientName":"A","lines":[]}`)
	postInvoice(t, mux, `{"clientName":"B","lines":[]}`)
	postInvoice(t, mux, `{"clientName":"C","lines":[]}`)
	inv1 := mustUnmarshalInvoice(t, rec1)

	rec := serveReq(mux, httptest.NewRequest("GET", "/api/invoices?offset=2", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if got, want := rec.Header().Get("X-Total-Count"), "3"; got != want {
		t.Errorf("X-Total-Count = %q, want %q", got, want)
	}
	var list invoicesListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(list.Invoices) != 1 || list.Invoices[0].Number != inv1.Number {
		t.Fatalf("invoices = %+v, want the single oldest invoice (%s)", list.Invoices, inv1.Number)
	}
}

// TestInvoicesListAbsentPaginationParamsReturnsAllUnchanged pins the
// backward-compatibility guarantee: no query params means every row, no
// X-Total-Count header, exactly as before pagination existed.
func TestInvoicesListAbsentPaginationParamsReturnsAllUnchanged(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	postInvoice(t, mux, `{"clientName":"A","lines":[]}`)
	postInvoice(t, mux, `{"clientName":"B","lines":[]}`)

	rec := serveReq(mux, httptest.NewRequest("GET", "/api/invoices", nil))
	if got := rec.Header().Get("X-Total-Count"); got != "" {
		t.Errorf("X-Total-Count = %q, want unset when pagination isn't used", got)
	}
	var list invoicesListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(list.Invoices) != 2 {
		t.Fatalf("invoices len = %d, want 2", len(list.Invoices))
	}
}

// TestInvoicesListInvalidPaginationParamsIs400 pins validation of ?limit=/
// ?offset=.
func TestInvoicesListInvalidPaginationParamsIs400(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	cases := []string{
		"/api/invoices?limit=0",
		"/api/invoices?limit=abc",
		"/api/invoices?limit=501",
		"/api/invoices?offset=-1",
	}
	for _, path := range cases {
		t.Run(path, func(t *testing.T) {
			rec := serveReq(mux, httptest.NewRequest("GET", path, nil))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 404s and malformed input
// ---------------------------------------------------------------------------

func TestInvoices404s(t *testing.T) {
	mux := newTestMux(t, t.TempDir())

	rec := getInvoice(t, mux, "does-not-exist")
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET unknown want 404, got %d", rec.Code)
	}
	rec = putInvoice(t, mux, "does-not-exist", `{"clientName":"A","lines":[]}`)
	if rec.Code != http.StatusNotFound {
		t.Errorf("PUT unknown want 404, got %d", rec.Code)
	}
	rec = deleteInvoice(t, mux, "does-not-exist")
	if rec.Code != http.StatusNotFound {
		t.Errorf("DELETE unknown want 404, got %d", rec.Code)
	}
	rec = setInvoiceStatus(t, mux, "does-not-exist", "sent")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status unknown want 404, got %d", rec.Code)
	}
}

func TestInvoicesUnknownFieldIs400(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	rec := postInvoice(t, mux, `{"clientName":"A","lines":[],"favoriteColor":"blue"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestInvoicesOversizedBodyIs413(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	body := `{"clientName":"` + strings.Repeat("x", invoicesMaxBytes+1) + `"}`
	rec := postInvoice(t, mux, body)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("want 413, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Config: invoicing
// ---------------------------------------------------------------------------

func TestInvoicingConfigDefaultsLoad(t *testing.T) {
	cfg := loadTestConfig(t, t.TempDir())
	if cfg.Invoicing.NumberPrefix != "INV" {
		t.Errorf("numberPrefix = %q, want INV", cfg.Invoicing.NumberPrefix)
	}
	if cfg.Invoicing.SalesTaxPct != 0 {
		t.Errorf("salesTaxPct = %v, want 0", cfg.Invoicing.SalesTaxPct)
	}
	if cfg.Invoicing.DueDays != 30 {
		t.Errorf("dueDays = %v, want 30", cfg.Invoicing.DueDays)
	}
}

func TestInvoicingConfigOutOfRangeSalesTaxPctFailsValidation(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("invoicing:\n  salesTaxPct: 100.5\n"), 0o644); err != nil {
		t.Fatalf("write config.yaml: %v", err)
	}
	_, err := LoadConfig(dir)
	if err == nil || !strings.Contains(err.Error(), "invoicing.salesTaxPct") {
		t.Fatalf("want invoicing.salesTaxPct validation error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// POST /api/invoices/{id}/send
// ---------------------------------------------------------------------------

// newInvoicesMuxWithDialer is newTestMux but builds the invoices resource by hand so smtpDialer can be swapped for the in-process fake
// SMTP server. configure, if non-nil, edits the loaded Config (typically Smtp) before it's wrapped in a ConfigStore.
func newInvoicesMuxWithDialer(t *testing.T, dir string, dial smtpDialer, configure func(cfg *Config)) *http.ServeMux {
	t.Helper()
	db := openTestDB(t, dir)
	cfg := loadTestConfig(t, dir)
	if configure != nil {
		configure(cfg)
	}
	store := NewConfigStore(cfg)

	mux := http.NewServeMux()
	cl := newClients(db)
	mux.HandleFunc("POST /api/clients", cl.handlePost)

	inv := newInvoices(db, store)
	inv.dial = dial
	mux.HandleFunc("GET /api/invoices/{id}", inv.handleGet)
	mux.HandleFunc("POST /api/invoices", inv.handlePost)
	mux.HandleFunc("POST /api/invoices/{id}/status", inv.handleStatus)
	mux.HandleFunc("POST /api/invoices/{id}/send", inv.handleSend)
	return mux
}

func sendInvoice(t *testing.T, mux *http.ServeMux, id, body string) *httptest.ResponseRecorder {
	return serveReq(mux, httptest.NewRequest("POST", "/api/invoices/"+id+"/send", strings.NewReader(body)))
}

func TestInvoiceSendUnconfiguredSmtpIs409(t *testing.T) {
	mux := newInvoicesMuxWithDialer(t, t.TempDir(), dialSMTP, nil) // Smtp.Host == "" by default
	created := mustUnmarshalInvoice(t, postInvoice(t, mux, `{"clientName":"Walk-in","lines":[]}`))

	rec := sendInvoice(t, mux, created.ID, `{}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body: %s", rec.Code, rec.Body.String())
	}
}

func TestInvoiceSendNoRecipientIs422(t *testing.T) {
	fake := &fakeSMTP{noSTARTTLS: true}
	mux := newInvoicesMuxWithDialer(t, t.TempDir(), fake.dialer(t), func(cfg *Config) {
		cfg.Smtp = Smtp{Host: "localhost", Port: 587, From: "quotes@example.com", To: "owner@example.com"}
	})
	created := mustUnmarshalInvoice(t, postInvoice(t, mux, `{"clientName":"Walk-in","lines":[]}`)) // no client -> no email on file

	rec := sendInvoice(t, mux, created.ID, `{}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body: %s", rec.Code, rec.Body.String())
	}
}

// Send happy path: recipient defaults to the client snapshot email, a channel="email" event is recorded, and a draft flips to "sent"
// with dates from cfg.Invoicing.DueDays, as handleStatus's draft->sent PATCH does.
func TestInvoiceSendSuccessFlipsDraftToSentAndRecordsEvent(t *testing.T) {
	fake := &fakeSMTP{noSTARTTLS: true}
	mux := newInvoicesMuxWithDialer(t, t.TempDir(), fake.dialer(t), func(cfg *Config) {
		cfg.Smtp = Smtp{Host: "localhost", Port: 587, From: "quotes@example.com", To: "owner@example.com"}
		cfg.Invoicing.DueDays = 15
	})

	rec := postClient(t, mux, `{"name":"Beta Print","email":"beta@example.com"}`)
	var cl Client
	if err := json.Unmarshal(rec.Body.Bytes(), &cl); err != nil {
		t.Fatalf("unmarshal client: %v", err)
	}
	created := mustUnmarshalInvoice(t, postInvoice(t, mux, fmt.Sprintf(`{"clientId":%q,"lines":[{"description":"Widget","quantity":1,"unitPriceCents":100}]}`, cl.ID)))
	if created.Status != "draft" {
		t.Fatalf("created status = %q, want draft", created.Status)
	}

	rec = sendInvoice(t, mux, created.ID, `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("send status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	sent := mustUnmarshalInvoice(t, rec)
	if sent.Status != "sent" {
		t.Fatalf("status after send = %q, want sent", sent.Status)
	}
	if sent.IssueDate == "" || sent.DueDate == "" {
		t.Fatalf("issueDate/dueDate not filled: %+v", sent)
	}
	if len(sent.Sends) != 1 || sent.Sends[0].Channel != "email" || sent.Sends[0].Recipient != "beta@example.com" {
		t.Fatalf("sends = %+v, want one email send to beta@example.com", sent.Sends)
	}

	got := fake.snapshot()
	if len(got.rcptTo) != 1 || got.rcptTo[0] != "TO:<beta@example.com>" {
		t.Errorf("fake server rcptTo = %v, want beta@example.com", got.rcptTo)
	}
}

// An explicit `to` beats the frozen client snapshot email if it matches some client on file (here a second client with the override address).
func TestInvoiceSendExplicitToOverridesClientEmail(t *testing.T) {
	fake := &fakeSMTP{noSTARTTLS: true}
	mux := newInvoicesMuxWithDialer(t, t.TempDir(), fake.dialer(t), func(cfg *Config) {
		cfg.Smtp = Smtp{Host: "localhost", Port: 587, From: "quotes@example.com", To: "owner@example.com"}
	})
	rec := postClient(t, mux, `{"name":"Beta Print","email":"beta@example.com"}`)
	var cl Client
	json.Unmarshal(rec.Body.Bytes(), &cl)
	postClient(t, mux, `{"name":"Override Co","email":"override@example.com"}`)
	created := mustUnmarshalInvoice(t, postInvoice(t, mux, fmt.Sprintf(`{"clientId":%q,"lines":[]}`, cl.ID)))

	rec = sendInvoice(t, mux, created.ID, `{"to":"override@example.com"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("send status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	sent := mustUnmarshalInvoice(t, rec)
	if len(sent.Sends) != 1 || sent.Sends[0].Recipient != "override@example.com" {
		t.Fatalf("sends = %+v, want recipient override@example.com", sent.Sends)
	}
}

// An explicit `to` matching neither the snapshot email nor any client on file is rejected, though the request is otherwise well-formed.
func TestInvoiceSendExplicitToNotAClientIs422(t *testing.T) {
	fake := &fakeSMTP{noSTARTTLS: true}
	mux := newInvoicesMuxWithDialer(t, t.TempDir(), fake.dialer(t), func(cfg *Config) {
		cfg.Smtp = Smtp{Host: "localhost", Port: 587, From: "quotes@example.com", To: "owner@example.com"}
	})
	rec := postClient(t, mux, `{"name":"Beta Print","email":"beta@example.com"}`)
	var cl Client
	json.Unmarshal(rec.Body.Bytes(), &cl)
	created := mustUnmarshalInvoice(t, postInvoice(t, mux, fmt.Sprintf(`{"clientId":%q,"lines":[]}`, cl.ID)))

	rec = sendInvoice(t, mux, created.ID, `{"to":"stranger@example.com"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("send status = %d, want 422; body: %s", rec.Code, rec.Body.String())
	}
	var body apiErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal error body: %v", err)
	}
	if len(body.Errors) != 1 || !strings.Contains(body.Errors[0], "recipient is not a client on file") {
		t.Fatalf("errors = %v, want recipient-not-a-client message", body.Errors)
	}
	if len(fake.snapshot().rcptTo) != 0 {
		t.Error("an unknown recipient should never reach the SMTP conversation")
	}
}

// TestInvoiceSendNeverDowngradesLaterStatus: sending on an already-"sent"
// invoice records another delivery event but leaves status alone.
func TestInvoiceSendNeverDowngradesLaterStatus(t *testing.T) {
	fake := &fakeSMTP{noSTARTTLS: true}
	mux := newInvoicesMuxWithDialer(t, t.TempDir(), fake.dialer(t), func(cfg *Config) {
		cfg.Smtp = Smtp{Host: "localhost", Port: 587, From: "quotes@example.com", To: "owner@example.com"}
	})
	postClient(t, mux, `{"name":"Someone","email":"someone@example.com"}`) // recipient restriction (A3): to must be a client on file
	created := mustUnmarshalInvoice(t, postInvoice(t, mux, `{"clientName":"Walk-in","lines":[]}`))

	statusRec := setInvoiceStatus(t, mux, created.ID, "sent")
	if statusRec.Code != http.StatusOK {
		t.Fatalf("manual mark-sent failed: %d %s", statusRec.Code, statusRec.Body.String())
	}

	rec := sendInvoice(t, mux, created.ID, `{"to":"someone@example.com"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("send status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	sent := mustUnmarshalInvoice(t, rec)
	if sent.Status != "sent" {
		t.Fatalf("status = %q, want sent (unchanged)", sent.Status)
	}
	if len(sent.Sends) != 2 {
		t.Fatalf("sends = %+v, want 2 (one manual from the status PATCH, one email)", sent.Sends)
	}
	if sent.Sends[0].Channel != "manual" || sent.Sends[1].Channel != "email" {
		t.Fatalf("sends channels = [%s, %s], want [manual, email]", sent.Sends[0].Channel, sent.Sends[1].Channel)
	}
}

// A non-admin gets the fixed message, not the SMTP server's text.
func TestInvoiceSendFailureHidesDetailFromNonAdmin(t *testing.T) {
	fake := &fakeSMTP{noSTARTTLS: true, rejectRcptTo: "beta"}
	mux := newInvoicesMuxWithDialer(t, t.TempDir(), fake.dialer(t), func(cfg *Config) {
		cfg.Smtp = Smtp{Host: "localhost", Port: 587, From: "quotes@example.com", To: "owner@example.com"}
	})
	postClient(t, mux, `{"name":"Beta Print","email":"beta@example.com"}`)
	created := mustUnmarshalInvoice(t, postInvoice(t, mux, `{"clientName":"Walk-in","lines":[]}`))

	req := httptest.NewRequest("POST", "/api/invoices/"+created.ID+"/send", strings.NewReader(`{"to":"beta@example.com"}`))
	req = req.WithContext(withIdentity(req.Context(), identity{username: "flo"}))
	rec := serveReq(mux, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body: %s", rec.Code, rec.Body.String())
	}
	var body apiErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal error body: %v", err)
	}
	if len(body.Errors) != 1 || body.Errors[0] != sendFailureNonAdminMessage {
		t.Fatalf("errors = %v, want the fixed non-admin message", body.Errors)
	}
}

// An SMTP failure surfaces the server's error text and records no send event and no status change.
func TestInvoiceSendFailureRecordsNothing(t *testing.T) {
	fake := &fakeSMTP{noSTARTTLS: true, rejectRcptTo: "beta"}
	mux := newInvoicesMuxWithDialer(t, t.TempDir(), fake.dialer(t), func(cfg *Config) {
		cfg.Smtp = Smtp{Host: "localhost", Port: 587, From: "quotes@example.com", To: "owner@example.com"}
	})
	postClient(t, mux, `{"name":"Beta Print","email":"beta@example.com"}`) // recipient restriction (A3): to must be a client on file
	created := mustUnmarshalInvoice(t, postInvoice(t, mux, `{"clientName":"Walk-in","lines":[]}`))

	rec := sendInvoice(t, mux, created.ID, `{"to":"beta@example.com"}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body: %s", rec.Code, rec.Body.String())
	}
	var body apiErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal error body: %v", err)
	}
	if len(body.Errors) != 1 || !strings.Contains(body.Errors[0], "550") {
		t.Fatalf("errors = %v, want the server's verbatim 550 response", body.Errors)
	}

	after := mustUnmarshalInvoice(t, getInvoice(t, mux, created.ID))
	if after.Status != "draft" {
		t.Errorf("status after failed send = %q, want draft (unchanged)", after.Status)
	}
	if len(after.Sends) != 0 {
		t.Errorf("sends after failed send = %+v, want none", after.Sends)
	}
}

// The ordinary draft->sent PATCH records a channel="manual" event with an empty recipient even when the client has an email:
// a manual send has no delivery address, and the UI renders an empty recipient as "-".
func TestInvoiceStatusPatchToSentRecordsManualEvent(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	rec := postClient(t, mux, `{"name":"Beta Print","email":"beta@example.com"}`)
	var cl Client
	if err := json.Unmarshal(rec.Body.Bytes(), &cl); err != nil {
		t.Fatalf("unmarshal client: %v", err)
	}
	created := mustUnmarshalInvoice(t, postInvoice(t, mux, fmt.Sprintf(`{"clientId":%q,"lines":[]}`, cl.ID)))

	rec = setInvoiceStatus(t, mux, created.ID, "sent")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	sent := mustUnmarshalInvoice(t, rec)
	if len(sent.Sends) != 1 || sent.Sends[0].Channel != "manual" {
		t.Fatalf("sends = %+v, want one manual event", sent.Sends)
	}
	if sent.Sends[0].Recipient != "" {
		t.Errorf("recipient = %q, want empty (manual sends record no delivery address)", sent.Sends[0].Recipient)
	}
}

// The paid->sent correction undoes a "marked paid" mistake, not a delivery, so it must not add a second manual event.
func TestInvoiceStatusPaidToSentCorrectionRecordsNoEvent(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	created := mustUnmarshalInvoice(t, postInvoice(t, mux, `{"clientName":"Walk-in","lines":[]}`))

	for _, status := range []string{"sent", "paid", "sent"} {
		rec := setInvoiceStatus(t, mux, created.ID, status)
		if rec.Code != http.StatusOK {
			t.Fatalf("transition to %s failed: %d %s", status, rec.Code, rec.Body.String())
		}
	}

	final := mustUnmarshalInvoice(t, getInvoice(t, mux, created.ID))
	if len(final.Sends) != 1 || final.Sends[0].Channel != "manual" {
		t.Fatalf("sends = %+v, want exactly one manual event (from the original draft->sent, none from the paid->sent correction)", final.Sends)
	}
}

// The balancing payment ("other" / "marked as paid") synthesized by sent->paid must not survive the paid->sent correction,
// or balanceCents reads 0 on an unpaid invoice and dashboard revenue counts money that never arrived.
func TestInvoiceStatusPaidToSentCorrectionRemovesBalancingPayment(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	inv := newSentInvoice(t, mux, 10000)

	rec := setInvoiceStatus(t, mux, inv.ID, "paid")
	if rec.Code != http.StatusOK {
		t.Fatalf("sent->paid: %d %s", rec.Code, rec.Body.String())
	}
	paid := mustUnmarshalInvoice(t, rec)
	if len(paid.Payments) != 1 || paid.Payments[0].Method != "other" || paid.Payments[0].Note != "marked as paid" {
		t.Fatalf("payments after sent->paid = %+v, want one synthesized balancing payment", paid.Payments)
	}

	rec = setInvoiceStatus(t, mux, inv.ID, "sent")
	if rec.Code != http.StatusOK {
		t.Fatalf("paid->sent: %d %s", rec.Code, rec.Body.String())
	}
	corrected := mustUnmarshalInvoice(t, rec)
	if len(corrected.Payments) != 0 {
		t.Fatalf("payments after paid->sent correction = %+v, want zero (the balancing payment must be reversed)", corrected.Payments)
	}
	if corrected.BalanceCents != 10000 {
		t.Fatalf("balanceCents = %d, want 10000", corrected.BalanceCents)
	}
}

// An operator-entered payment made before sent->paid must survive the paid->sent correction; only the auto balancing payment is reversed.
func TestInvoiceStatusPaidToSentCorrectionKeepsOperatorPayment(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	inv := newSentInvoice(t, mux, 10000)

	rec := postPayment(t, mux, inv.ID, `{"paidDate":"2026-01-10","amountCents":4000,"method":"cash"}`)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("operator payment: %d %s", rec.Code, rec.Body.String())
	}

	rec = setInvoiceStatus(t, mux, inv.ID, "paid")
	if rec.Code != http.StatusOK {
		t.Fatalf("sent->paid: %d %s", rec.Code, rec.Body.String())
	}
	paid := mustUnmarshalInvoice(t, rec)
	if len(paid.Payments) != 2 {
		t.Fatalf("payments after sent->paid = %+v, want operator payment + balancing payment", paid.Payments)
	}

	rec = setInvoiceStatus(t, mux, inv.ID, "sent")
	if rec.Code != http.StatusOK {
		t.Fatalf("paid->sent: %d %s", rec.Code, rec.Body.String())
	}
	corrected := mustUnmarshalInvoice(t, rec)
	if len(corrected.Payments) != 1 || corrected.Payments[0].AmountCents != 4000 || corrected.Payments[0].Method != "cash" {
		t.Fatalf("payments after paid->sent correction = %+v, want exactly the 4000 operator payment", corrected.Payments)
	}
}

// Attribution (identity.go): create stamps createdBy/updatedBy; PUT, the status PATCH, and a draft->sent handleSend move only updatedBy.

func TestInvoiceCreateAndUpdateStampAttribution(t *testing.T) {
	mux, db := newTestMuxWithDB(t, t.TempDir())
	for _, username := range []string{"flo", "bob", "gus"} {
		if _, _, err := createUser(db, username, "password123", []string{"invoices"}, ""); err != nil {
			t.Fatalf("createUser %s: %v", username, err)
		}
	}

	req := withCtxIdentity(httptest.NewRequest("POST", "/api/invoices", strings.NewReader(`{"clientName":"Walk-in","lines":[]}`)), identity{username: "flo"})
	rec := serveReq(mux, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, body %s", rec.Code, rec.Body.String())
	}
	created := mustUnmarshalInvoice(t, rec)
	if created.CreatedBy != "flo" || created.UpdatedBy != "flo" {
		t.Fatalf("createdBy=%q updatedBy=%q, want both %q", created.CreatedBy, created.UpdatedBy, "flo")
	}

	req = withCtxIdentity(httptest.NewRequest("PUT", "/api/invoices/"+created.ID, strings.NewReader(`{"clientName":"Walk-in","notes":"edited","lines":[]}`)), identity{username: "bob"})
	rec = serveReq(mux, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: status = %d, body %s", rec.Code, rec.Body.String())
	}
	updated := mustUnmarshalInvoice(t, rec)
	if updated.CreatedBy != "flo" {
		t.Fatalf("update must not touch createdBy: got %q, want %q", updated.CreatedBy, "flo")
	}
	if updated.UpdatedBy != "bob" {
		t.Fatalf("update: updatedBy = %q, want %q", updated.UpdatedBy, "bob")
	}

	req = withCtxIdentity(httptest.NewRequest("POST", "/api/invoices/"+created.ID+"/status", strings.NewReader(`{"status":"sent"}`)), identity{username: "gus"})
	rec = serveReq(mux, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: status = %d, body %s", rec.Code, rec.Body.String())
	}
	sent := mustUnmarshalInvoice(t, rec)
	if sent.CreatedBy != "flo" {
		t.Fatalf("status PATCH must not touch createdBy: got %q, want %q", sent.CreatedBy, "flo")
	}
	if sent.UpdatedBy != "gus" {
		t.Fatalf("status PATCH: updatedBy = %q, want %q", sent.UpdatedBy, "gus")
	}
}

func TestInvoiceCreateWithNoContextIdentityStampsAdmin(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	rec := postInvoice(t, mux, `{"clientName":"Walk-in","lines":[]}`)
	created := mustUnmarshalInvoice(t, rec)
	if created.CreatedBy != "admin" || created.UpdatedBy != "admin" {
		t.Fatalf("createdBy=%q updatedBy=%q, want both %q (identityFor's admin fallback)", created.CreatedBy, created.UpdatedBy, "admin")
	}
}

// "cancelled" is not a status: only "canceled" is accepted.
func TestInvoiceStatusRejectsBritishSpelling(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	inv := mustUnmarshalInvoice(t, postInvoice(t, mux, `{"clientName":"A","lines":[]}`))
	if rec := setInvoiceStatus(t, mux, inv.ID, "cancelled"); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status cancelled: %d %s, want 422", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Optimistic concurrency (decision 5)
// ---------------------------------------------------------------------------

func TestInvoicePutWithStaleVersionIs409(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	rec := postInvoice(t, mux, `{"clientName":"A","lines":[{"description":"X","quantity":1,"unitPriceCents":100}]}`)
	inv := mustUnmarshalInvoice(t, rec)
	if inv.Version != 1 {
		t.Fatalf("new invoice version = %d, want 1", inv.Version)
	}

	body := func(note string, version int) string {
		return fmt.Sprintf(`{"clientName":"A","notes":%q,"version":%d,"lines":[{"description":"X","quantity":1,"unitPriceCents":100}]}`, note, version)
	}
	rec = putInvoice(t, mux, inv.ID, body("tab one", inv.Version))
	if rec.Code != http.StatusOK {
		t.Fatalf("first save: want 200, got %d %s", rec.Code, rec.Body.String())
	}
	saved := mustUnmarshalInvoice(t, rec)
	if saved.Version != 2 {
		t.Errorf("version after save = %d, want 2", saved.Version)
	}

	// The second tab still holds version 1.
	rec = putInvoice(t, mux, inv.ID, body("tab two", inv.Version))
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale save: want 409, got %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "changed in another tab") {
		t.Errorf("409 body %q should say the invoice was changed elsewhere", rec.Body.String())
	}
	got := mustUnmarshalInvoice(t, getInvoice(t, mux, inv.ID))
	if got.Notes != "tab one" {
		t.Errorf("notes = %q, want the first tab's save to stand", got.Notes)
	}

	// Overwrite: the UI reloads the version and saves again.
	rec = putInvoice(t, mux, inv.ID, body("tab two", got.Version))
	if rec.Code != http.StatusOK {
		t.Fatalf("overwrite with the current version: want 200, got %d %s", rec.Code, rec.Body.String())
	}

	// A body with no version still saves (API callers that predate the field).
	rec = putInvoice(t, mux, inv.ID, `{"clientName":"A","lines":[{"description":"X","quantity":1,"unitPriceCents":100}]}`)
	if rec.Code != http.StatusOK {
		t.Errorf("no version: want 200, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestInvoiceVersionBumpsOnStatusChange(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	inv := newSentInvoice(t, mux, 500)
	if inv.Version != 2 {
		t.Errorf("version after Mark sent = %d, want 2", inv.Version)
	}
	rec := postPayment(t, mux, inv.ID, `{"paidDate":"2026-01-15","amountCents":500}`)
	paid := mustUnmarshalInvoice(t, rec)
	if paid.Version != 3 {
		t.Errorf("version after the payment flips it to paid = %d, want 3", paid.Version)
	}
}

// Converting a saved quote (a line with a quoteId) fills the issue date with today and the due date from the
// default terms; a manual invoice keeps what the editor sent.
func TestInvoiceFromQuoteDefaultsDates(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	rec := postInvoice(t, mux, `{"clientName":"A","lines":[{"description":"X","quantity":1,"unitPriceCents":100,"quoteId":"q1"}]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("convert: %d %s", rec.Code, rec.Body.String())
	}
	inv := mustUnmarshalInvoice(t, rec)
	if inv.IssueDate == "" || inv.DueDate == "" || inv.DueDate <= inv.IssueDate {
		t.Errorf("dates = %q / %q, want issue today and a later due date", inv.IssueDate, inv.DueDate)
	}
	rec = postInvoice(t, mux, `{"clientName":"A","lines":[{"description":"X","quantity":1,"unitPriceCents":100}]}`)
	manual := mustUnmarshalInvoice(t, rec)
	if manual.IssueDate != "" || manual.DueDate != "" {
		t.Errorf("manual invoice dates = %q / %q, want them left blank", manual.IssueDate, manual.DueDate)
	}
}

// The widest invoice the validators accept (100 lines, each at the quantity and unit-price maxima, the maximum
// per-line discount and a 100% tax rate) must total exactly, far inside int64 and below 2^53 so the browser's
// doubles stay exact too.
func TestInvoiceTotalsAtValidatorExtremes(t *testing.T) {
	const (
		maxQty  = 100000
		unitMax = 100_000_000
		lines   = 100
		taxBp   = maxSalesTaxPct * 100
	)
	in := make([]invoiceLineInput, lines)
	for i := range in {
		in[i] = invoiceLineInput{Description: "x", Quantity: maxQty, UnitPriceCents: unitMax, DiscountCents: 0}
	}
	if p := validateInvoiceInput(&invoiceInput{ClientName: "X", Lines: in, TaxBp: func() *int { v := taxBp; return &v }()}); len(p) != 0 {
		t.Fatalf("the extreme invoice failed validation: %v", p)
	}
	got := computeTotals(in, 0, taxBp)
	wantSubtotal := lines * maxQty * unitMax // 1e15
	if got.SubtotalCents != wantSubtotal || got.TaxCents != wantSubtotal || got.TotalCents != 2*wantSubtotal {
		t.Errorf("subtotal/tax/total = %d/%d/%d, want %d/%d/%d", got.SubtotalCents, got.TaxCents, got.TotalCents, wantSubtotal, wantSubtotal, 2*wantSubtotal)
	}
	if got.TotalCents <= 0 || got.TotalCents >= 1<<53 {
		t.Errorf("total %d is outside (0, 2^53)", got.TotalCents)
	}
	over := validateInvoiceInput(&invoiceInput{ClientName: "X", Lines: in[:1], TaxBp: func() *int { v := taxBp + 1; return &v }()})
	if len(over) == 0 {
		t.Error("taxBp above the ceiling validated clean")
	}
}
