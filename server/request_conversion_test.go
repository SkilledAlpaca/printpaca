package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func loadRequestJSON(t *testing.T, mux *http.ServeMux, id string) QuoteRequest {
	t.Helper()
	rec := getRequest(t, mux, id)
	var req QuoteRequest
	if err := json.Unmarshal(rec.Body.Bytes(), &req); err != nil {
		t.Fatalf("unmarshal request: %v (%s)", err, rec.Body.String())
	}
	return req
}

func convertBody(requestID string, extraLine string) string {
	return fmt.Sprintf(`{"clientName":"Visitor","requestId":%q,"lines":[{"description":"3D print: PLA","quantity":1,"unitPriceCents":1000%s}]}`, requestID, extraLine)
}

// Convert to invoice links the request both ways in one step (decision 10): the request is finalized with the
// invoice id and number, the invoice gets today's dates, and a second conversion is refused until the invoice
// is deleted or canceled.
func TestRequestConversionLinksAndBlocksDoubleInvoicing(t *testing.T) {
	adminMux, publicMux, db, _ := newRequestsTestSetup(t, t.TempDir())
	id := seedQuoteRequest(t, publicMux, "PLA")
	getRequest(t, adminMux, id) // new -> viewed

	rec := postInvoice(t, adminMux, convertBody(id, ""))
	if rec.Code != http.StatusCreated {
		t.Fatalf("convert: want 201, got %d %s", rec.Code, rec.Body.String())
	}
	inv := mustUnmarshalInvoice(t, rec)
	if inv.IssueDate == "" || inv.DueDate == "" {
		t.Errorf("dates = %q / %q, want a conversion to default both", inv.IssueDate, inv.DueDate)
	}
	req := loadRequestJSON(t, adminMux, id)
	if req.Status != "finalized" || req.InvoiceID != inv.ID || req.InvoiceNumber != inv.Number || req.InvoiceStatus != "draft" {
		t.Errorf("request = status %q invoice %q/%q/%q, want finalized and linked to %s %s", req.Status, req.InvoiceID, req.InvoiceNumber, req.InvoiceStatus, inv.ID, inv.Number)
	}
	if req.FinalizedAt == "" {
		t.Error("finalizedAt empty, want the retention clock started")
	}

	if rec = postInvoice(t, adminMux, convertBody(id, "")); rec.Code != http.StatusConflict {
		t.Errorf("second conversion: want 409, got %d %s", rec.Code, rec.Body.String())
	} else if !strings.Contains(rec.Body.String(), inv.Number) {
		t.Errorf("409 body %q should name %s", rec.Body.String(), inv.Number)
	}

	// Deleting the draft frees the request.
	if rec = deleteInvoice(t, adminMux, inv.ID); rec.Code != http.StatusNoContent {
		t.Fatalf("delete draft: %d %s", rec.Code, rec.Body.String())
	}
	if got := loadRequestJSON(t, adminMux, id); got.InvoiceID != "" || got.InvoiceNumber != "" || got.Status != "finalized" {
		t.Errorf("after deleting the invoice: invoiceId %q number %q status %q, want unlinked and still finalized", got.InvoiceID, got.InvoiceNumber, got.Status)
	}
	rec = postInvoice(t, adminMux, convertBody(id, ""))
	if rec.Code != http.StatusCreated {
		t.Fatalf("convert again: want 201, got %d %s", rec.Code, rec.Body.String())
	}

	// Canceling frees it too.
	second := mustUnmarshalInvoice(t, rec)
	setInvoiceStatus(t, adminMux, second.ID, "sent")
	setInvoiceStatus(t, adminMux, second.ID, "canceled")
	if rec = postInvoice(t, adminMux, convertBody(id, "")); rec.Code != http.StatusCreated {
		t.Errorf("convert after cancel: want 201, got %d %s", rec.Code, rec.Body.String())
	}

	if rec = postInvoice(t, adminMux, convertBody("nope", "")); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("unknown request: want 422, got %d %s", rec.Code, rec.Body.String())
	}
	_ = db
}

// An unfinalized item order converts with its gallery item linked and records NO item_sales row: the invoice
// send is then the single place the sale and the stock are counted. A request that was already finalized
// converts unlinked, because finalize already counted it.
func TestRequestConversionItemLinkAvoidsDoubleCounting(t *testing.T) {
	adminMux, _, db, _ := newRequestsTestSetup(t, t.TempDir())
	rec := postItem(t, adminMux, `{"name":"Benchy","priceCents":1500,"public":true,"stockMode":"in_stock","stockQty":5}`)
	var item Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("unmarshal item: %v", err)
	}
	line := fmt.Sprintf(`,"itemId":%q,"optionName":"Color: Gone"`, item.ID)

	viewed := insertItemOrderRequest(t, db, item.ID, item.Name, 1, "viewed")
	rec = postInvoice(t, adminMux, convertBody(viewed, line))
	if rec.Code != http.StatusCreated {
		t.Fatalf("convert viewed item order: %d %s", rec.Code, rec.Body.String())
	}
	inv := mustUnmarshalInvoice(t, rec)
	if inv.Lines[0].ItemID != item.ID {
		t.Errorf("line itemId = %q, want the gallery item carried over", inv.Lines[0].ItemID)
	}
	if inv.Lines[0].OptionName != "" {
		t.Errorf("line optionName = %q, want a no-longer-valid option dropped rather than failing the conversion", inv.Lines[0].OptionName)
	}
	var sales int
	if err := db.QueryRow(`SELECT COUNT(*) FROM item_sales`).Scan(&sales); err != nil || sales != 0 {
		t.Errorf("item_sales rows = %d (err %v), want 0 until the invoice is sent", sales, err)
	}
	if _, qty := itemStock(t, db, item.ID); qty != 5 {
		t.Errorf("stock after convert = %d, want 5 (the send takes it)", qty)
	}

	// A request the Finalize button handled has its sale in item_sales; that, not the status, drops the link.
	finalized := insertItemOrderRequest(t, db, item.ID, item.Name, 1, "viewed")
	if rec := setRequestStatus(t, adminMux, finalized, "finalized"); rec.Code != http.StatusOK {
		t.Fatalf("finalize: %d %s", rec.Code, rec.Body.String())
	}
	rec = postInvoice(t, adminMux, convertBody(finalized, line))
	if rec.Code != http.StatusCreated {
		t.Fatalf("convert finalized item order: %d %s", rec.Code, rec.Body.String())
	}
	if got := mustUnmarshalInvoice(t, rec); got.Lines[0].ItemID != "" {
		t.Errorf("already-finalized order: line itemId = %q, want it unlinked so the sale isn't counted twice", got.Lines[0].ItemID)
	}

	// An item deleted since the order still converts, unlinked.
	ghost := insertItemOrderRequest(t, db, "deleted-item", "Ghost", 1, "viewed")
	rec = postInvoice(t, adminMux, convertBody(ghost, `,"itemId":"deleted-item"`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("convert ghost: %d %s", rec.Code, rec.Body.String())
	}
}

// A request that still needs its print time can't be invoiced, and POST .../print-time prices it with the
// current config and clears the mark.
func TestRequestPrintTimeFlow(t *testing.T) {
	adminMux, publicMux, db, _ := newRequestsTestSetup(t, t.TempDir())
	id := seedQuoteRequest(t, publicMux, "PLA")
	if _, err := db.Exec(`UPDATE quote_requests SET needs_print_time = 1, time_seconds = 0, price_cents = 0, status = 'viewed' WHERE id = ?`, id); err != nil {
		t.Fatalf("mark: %v", err)
	}
	if got := loadRequestJSON(t, adminMux, id); !got.NeedsPrintTime {
		t.Fatal("needsPrintTime = false, want the marker on the JSON")
	}

	if rec := postInvoice(t, adminMux, convertBody(id, "")); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "print time") {
		t.Errorf("convert without time: want 409 naming the print time, got %d %s", rec.Code, rec.Body.String())
	}

	post := func(body string) *httptest.ResponseRecorder {
		return serveReq(adminMux, httptest.NewRequest("POST", "/api/requests/"+id+"/print-time", strings.NewReader(body)))
	}
	if rec := post(`{"timeSeconds":0}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("zero time: want 422, got %d %s", rec.Code, rec.Body.String())
	}
	rec := post(`{"timeSeconds":5400}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("print-time: want 200, got %d %s", rec.Code, rec.Body.String())
	}
	var req QuoteRequest
	if err := json.Unmarshal(rec.Body.Bytes(), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.NeedsPrintTime || req.TimeSeconds != 5400 || req.PriceCents <= 0 {
		t.Errorf("after print-time: needsPrintTime %v timeSeconds %d priceCents %d, want cleared, 5400 and a price", req.NeedsPrintTime, req.TimeSeconds, req.PriceCents)
	}
	if rec := post(`{"timeSeconds":5400}`); rec.Code != http.StatusConflict {
		t.Errorf("second print-time: want 409, got %d %s", rec.Code, rec.Body.String())
	}
	if rec := postInvoice(t, adminMux, convertBody(id, "")); rec.Code != http.StatusCreated {
		t.Errorf("convert after time: want 201, got %d %s", rec.Code, rec.Body.String())
	}
}

func salesAndStock(t *testing.T, db *sql.DB, itemID string) (sales, stock int) {
	t.Helper()
	if err := db.QueryRow(`SELECT COUNT(*) FROM invoice_lines l JOIN invoices i ON i.id = l.invoice_id WHERE l.item_id = ? AND i.status IN ('sent','paid')`, itemID).Scan(&sales); err != nil {
		t.Fatalf("count sales: %v", err)
	}
	_, stock = itemStock(t, db, itemID)
	return sales, stock
}

// Conversion finalizes the request without recording a sale, so a deleted draft or a canceled invoice leaves
// a "finalized" request that is still uncounted: the re-conversion must keep the item link, and sending it
// counts the sale and takes the stock exactly once.
func TestRequestReconversionKeepsItemLinkAfterDeleteOrCancel(t *testing.T) {
	adminMux, _, db, _ := newRequestsTestSetup(t, t.TempDir())
	rec := postItem(t, adminMux, `{"name":"Benchy","priceCents":1500,"public":true,"stockMode":"in_stock","stockQty":5}`)
	var item Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("unmarshal item: %v", err)
	}
	line := fmt.Sprintf(`,"itemId":%q`, item.ID)
	id := insertItemOrderRequest(t, db, item.ID, item.Name, 1, "viewed")

	// convert -> delete draft -> reconvert -> send
	first := mustUnmarshalInvoice(t, postInvoice(t, adminMux, convertBody(id, line)))
	if rec := deleteInvoice(t, adminMux, first.ID); rec.Code != http.StatusNoContent {
		t.Fatalf("delete draft: %d", rec.Code)
	}
	rec = postInvoice(t, adminMux, convertBody(id, line))
	if rec.Code != http.StatusCreated {
		t.Fatalf("reconvert: %d %s", rec.Code, rec.Body.String())
	}
	second := mustUnmarshalInvoice(t, rec)
	if second.Lines[0].ItemID != item.ID {
		t.Fatalf("reconverted line itemId = %q, want the link kept", second.Lines[0].ItemID)
	}
	setInvoiceStatus(t, adminMux, second.ID, "sent")
	if sales, stock := salesAndStock(t, db, item.ID); sales != 1 || stock != 4 {
		t.Errorf("after delete+reconvert+send: sales %d stock %d, want 1 and 4", sales, stock)
	}

	// send -> cancel -> reconvert -> send: the cancel restored the stock, the second send takes it once.
	setInvoiceStatus(t, adminMux, second.ID, "canceled")
	if _, stock := salesAndStock(t, db, item.ID); stock != 5 {
		t.Fatalf("stock after cancel = %d, want 5", stock)
	}
	rec = postInvoice(t, adminMux, convertBody(id, line))
	if rec.Code != http.StatusCreated {
		t.Fatalf("reconvert after cancel: %d %s", rec.Code, rec.Body.String())
	}
	third := mustUnmarshalInvoice(t, rec)
	if third.Lines[0].ItemID != item.ID {
		t.Fatalf("line itemId after cancel = %q, want the link kept", third.Lines[0].ItemID)
	}
	setInvoiceStatus(t, adminMux, third.ID, "sent")
	if sales, stock := salesAndStock(t, db, item.ID); sales != 1 || stock != 4 {
		t.Errorf("after cancel+reconvert+send: sales %d stock %d, want 1 and 4", sales, stock)
	}
}

// A Finalize that commits between prepareRequestConversion and the link transaction records the sale; the
// link transaction then drops the new invoice's item links, and a Finalize that loses the other way (the
// conversion committed first) is refused by the conditional UPDATE.
func TestRequestFinalizeRaceIsGuarded(t *testing.T) {
	adminMux, _, db, _ := newRequestsTestSetup(t, t.TempDir())
	rec := postItem(t, adminMux, `{"name":"Benchy","priceCents":1500,"public":true,"stockMode":"in_stock","stockQty":5}`)
	var item Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("unmarshal item: %v", err)
	}

	// Finalize landed after prepare: simulate by recording the sale, then running the link in a transaction.
	id := insertItemOrderRequest(t, db, item.ID, item.Name, 1, "viewed")
	invID := newID()
	if _, err := db.Exec(`INSERT INTO invoices (id, number, number_year, number_seq, status, client_name, issue_date, due_date, tax_bp, discount_cents, notes, subtotal_cents, tax_cents, total_cents, created_at, updated_at, created_by, updated_by)
	  VALUES (?, 'X-1', 2026, 1, 'draft', 'V', '2026-01-01', '2026-01-02', 0, 0, '', 0, 0, 0, '', '', '', '')`, invID); err != nil {
		t.Fatalf("insert invoice: %v", err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := insertInvoiceLines(tx, invID, []invoiceLineInput{{Description: "x", Quantity: 1, UnitPriceCents: 100, ItemID: item.ID}}); err != nil {
		t.Fatalf("lines: %v", err)
	}
	if _, err := tx.Exec(`INSERT INTO item_sales (request_id, item_id, item_name, quantity, revenue_cents, sold_at) VALUES (?, ?, 'Benchy', 1, 0, 'now')`, id, item.ID); err != nil {
		t.Fatal(err)
	}
	if err := linkRequestToInvoice(tx, id, invID, "bob", "now"); err != nil {
		t.Fatalf("link: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var linked int
	if err := db.QueryRow(`SELECT COUNT(*) FROM invoice_lines WHERE invoice_id = ? AND item_id != ''`, invID).Scan(&linked); err != nil || linked != 0 {
		t.Errorf("linked lines after racing finalize = %d (err %v), want 0", linked, err)
	}

	// Conversion won: a stale Finalize (status loaded before) cannot double-record. Drive the handler against a
	// request already finalized by a conversion: transition table gives 409 for finalized.
	id2 := insertItemOrderRequest(t, db, item.ID, item.Name, 1, "viewed")
	if rec := postInvoice(t, adminMux, convertBody(id2, fmt.Sprintf(`,"itemId":%q`, item.ID))); rec.Code != http.StatusCreated {
		t.Fatalf("convert: %d %s", rec.Code, rec.Body.String())
	}
	if rec := setRequestStatus(t, adminMux, id2, "finalized"); rec.Code != http.StatusConflict {
		t.Errorf("finalize after conversion: want 409, got %d", rec.Code)
	}
	var sales int
	if err := db.QueryRow(`SELECT COUNT(*) FROM item_sales WHERE request_id = ?`, id2).Scan(&sales); err != nil || sales != 0 {
		t.Errorf("item_sales for converted request = %d (err %v), want 0", sales, err)
	}
}
