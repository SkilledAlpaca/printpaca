package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newInvoiceItemsMux wires the item, client, invoice (including send), and top-items routes over one database,
// with SMTP pointed at the in-process fake so POST .../send works.
func newInvoiceItemsMux(t *testing.T) (*http.ServeMux, *sql.DB) {
	t.Helper()
	mux, db, _ := newInvoiceItemsMuxInv(t)
	return mux, db
}

// newInvoiceItemsMuxInv is newInvoiceItemsMux plus the invoices resource, for tests that set its afterLoad hook.
func newInvoiceItemsMuxInv(t *testing.T) (*http.ServeMux, *sql.DB, *invoices) {
	t.Helper()
	dir := t.TempDir()
	db := openTestDB(t, dir)
	cfg := loadTestConfig(t, dir)
	cfg.Smtp = Smtp{Host: "localhost", Port: 587, From: "quotes@example.com", To: "owner@example.com"}
	store := NewConfigStore(cfg)
	fake := &fakeSMTP{noSTARTTLS: true}

	mux := http.NewServeMux()
	it := newItems(db, dir)
	mux.HandleFunc("POST /api/items", it.handlePost)
	mux.HandleFunc("DELETE /api/items/{id}", it.handleDelete)
	mux.HandleFunc("POST /api/clients", newClients(db).handlePost)
	inv := newInvoices(db, store)
	inv.dial = fake.dialer(t)
	mux.HandleFunc("GET /api/invoices/{id}", inv.handleGet)
	mux.HandleFunc("POST /api/invoices", inv.handlePost)
	mux.HandleFunc("PUT /api/invoices/{id}", inv.handlePut)
	mux.HandleFunc("POST /api/invoices/{id}/status", inv.handleStatus)
	mux.HandleFunc("POST /api/invoices/{id}/send", inv.handleSend)
	mux.HandleFunc("GET /api/dashboard/top-items", newDashboard(db, store).handleTopItems)
	return mux, db, inv
}

func mustPostItem(t *testing.T, mux *http.ServeMux, body string) Item {
	t.Helper()
	rec := postItem(t, mux, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST item: %d %s", rec.Code, rec.Body.String())
	}
	var item Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("unmarshal item: %v", err)
	}
	return item
}

// itemInvoiceBody is a draft with one linked line; extra is spliced into the invoice object ("" for none).
func itemInvoiceBody(itemID, option string, qty int, extra string) string {
	return fmt.Sprintf(`{"clientName":"Walk-in",%s"lines":[{"description":"Widget","quantity":%d,"unitPriceCents":1000,"itemId":%q,"optionName":%q}]}`,
		extra, qty, itemID, option)
}

func mustPostInvoice(t *testing.T, mux *http.ServeMux, body string) Invoice {
	t.Helper()
	rec := postInvoice(t, mux, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST invoice: %d %s", rec.Code, rec.Body.String())
	}
	return mustUnmarshalInvoice(t, rec)
}

func mustSetStatus(t *testing.T, mux *http.ServeMux, id, status string) Invoice {
	t.Helper()
	rec := setInvoiceStatus(t, mux, id, status)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %s: %d %s", status, rec.Code, rec.Body.String())
	}
	return mustUnmarshalInvoice(t, rec)
}

func stockOf(t *testing.T, db *sql.DB, itemID string) int {
	t.Helper()
	_, qty := itemStock(t, db, itemID)
	return qty
}

func TestInvoiceLinkValidation(t *testing.T) {
	mux, _ := newInvoiceItemsMux(t)
	item := mustPostItem(t, mux, `{"name":"Dragon","priceCents":1000,"optionGroups":[{"label":"Color","required":true,"options":[{"name":"Red","deltaCents":0}]},{"label":"Size","required":false,"options":[{"name":"XL","deltaCents":300}]}],"stockMode":"in_stock","stockQty":5}`)

	for _, tc := range []struct{ name, body string }{
		{"unknown item", itemInvoiceBody("nope", "", 1, "")},
		{"bad option", itemInvoiceBody(item.ID, "Color: Green", 1, "")}, {"same group twice", itemInvoiceBody(item.ID, "Size: XL · Size: XL", 1, "")}, {"bare option name", itemInvoiceBody(item.ID, "Red", 1, "")},
		{"fractional quantity", strings.Replace(itemInvoiceBody(item.ID, "", 1, ""), `"quantity":1`, `"quantity":1.5`, 1)},
		{"zero quantity", itemInvoiceBody(item.ID, "", 0, "")},
		{"too-large quantity", itemInvoiceBody(item.ID, "", maxItemOrderQty+1, "")},
	} {
		rec := postInvoice(t, mux, tc.body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400; body %s", tc.name, rec.Code, rec.Body.String())
		} else if !strings.Contains(rec.Body.String(), "lines[0]") {
			t.Errorf("%s: error %s does not name the line", tc.name, rec.Body.String())
		}
	}

	// The same rules apply on PUT.
	draft := mustPostInvoice(t, mux, itemInvoiceBody(item.ID, "Color: Red", 2, ""))
	if rec := putInvoice(t, mux, draft.ID, itemInvoiceBody("nope", "", 1, "")); rec.Code != http.StatusBadRequest {
		t.Errorf("PUT unknown item: %d, want 400", rec.Code)
	}

	// Unlinked lines keep today's rules: fractional quantities are fine, zero is a 422.
	if rec := postInvoice(t, mux, `{"clientName":"Walk-in","lines":[{"description":"Hours","quantity":1.5,"unitPriceCents":100}]}`); rec.Code != http.StatusCreated {
		t.Errorf("unlinked fractional quantity: %d, want 201", rec.Code)
	}
	if rec := postInvoice(t, mux, `{"clientName":"Walk-in","lines":[{"description":"Hours","quantity":0,"unitPriceCents":100}]}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("unlinked zero quantity: %d, want 422", rec.Code)
	}

	// A linked line keeps its link, an editable price, and ignores a client-sent stockTaken.
	ok := mustPostInvoice(t, mux, strings.Replace(itemInvoiceBody(item.ID, "Size: XL · Color: Red", 2, ""), `"itemId"`, `"stockTaken":99,"itemId"`, 1))
	if ln := ok.Lines[0]; ln.ItemID != item.ID || ln.OptionName != "Size: XL · Color: Red" || ln.StockTaken != 0 {
		t.Errorf("line = item %q option %q taken %d, want %q \"Size: XL · Color: Red\" 0", ln.ItemID, ln.OptionName, ln.StockTaken, item.ID)
	}
}

func TestInvoiceSendTakesStockExactlyOnce(t *testing.T) {
	for _, via := range []string{"status", "send"} {
		t.Run(via, func(t *testing.T) {
			mux, db := newInvoiceItemsMux(t)
			item := mustPostItem(t, mux, `{"name":"Dragon","priceCents":1000,"stockMode":"in_stock","stockQty":5}`)
			cl := postClient(t, mux, `{"name":"Beta Print","email":"beta@example.com"}`)
			var client Client
			if err := json.Unmarshal(cl.Body.Bytes(), &client); err != nil {
				t.Fatalf("unmarshal client: %v", err)
			}
			draft := mustPostInvoice(t, mux, itemInvoiceBody(item.ID, "", 2, fmt.Sprintf(`"clientId":%q,`, client.ID)))
			if got := stockOf(t, db, item.ID); got != 5 {
				t.Fatalf("stock after draft = %d, want 5 (drafts reserve nothing)", got)
			}

			var sent Invoice
			if via == "status" {
				sent = mustSetStatus(t, mux, draft.ID, "sent")
			} else {
				rec := sendInvoice(t, mux, draft.ID, `{}`)
				if rec.Code != http.StatusOK {
					t.Fatalf("send: %d %s", rec.Code, rec.Body.String())
				}
				sent = mustUnmarshalInvoice(t, rec)
			}
			if got := stockOf(t, db, item.ID); got != 3 {
				t.Errorf("stock after send = %d, want 3", got)
			}
			if sent.Lines[0].StockTaken != 2 {
				t.Errorf("stockTaken = %d, want 2", sent.Lines[0].StockTaken)
			}

			// A second send of the now-sent invoice and the sent->paid->sent loop take nothing more.
			if via == "send" {
				if rec := sendInvoice(t, mux, draft.ID, `{}`); rec.Code != http.StatusOK {
					t.Fatalf("resend: %d %s", rec.Code, rec.Body.String())
				}
			}
			mustSetStatus(t, mux, draft.ID, "paid")
			mustSetStatus(t, mux, draft.ID, "sent")
			mustSetStatus(t, mux, draft.ID, "paid")
			if got := stockOf(t, db, item.ID); got != 3 {
				t.Errorf("stock after paid->sent->paid = %d, want 3", got)
			}
		})
	}
}

func TestInvoiceSendFloorsStockAndCancelRestoresExactly(t *testing.T) {
	mux, db := newInvoiceItemsMux(t)
	item := mustPostItem(t, mux, `{"name":"Dragon","priceCents":1000,"stockMode":"in_stock","stockQty":2}`)
	draft := mustPostInvoice(t, mux, itemInvoiceBody(item.ID, "", 5, ""))

	sent := mustSetStatus(t, mux, draft.ID, "sent")
	if got := stockOf(t, db, item.ID); got != 0 {
		t.Errorf("stock after oversell = %d, want 0 (floored)", got)
	}
	if sent.Lines[0].StockTaken != 2 {
		t.Errorf("stockTaken = %d, want 2 (the units actually removed)", sent.Lines[0].StockTaken)
	}

	canceled := mustSetStatus(t, mux, draft.ID, "canceled")
	if got := stockOf(t, db, item.ID); got != 2 {
		t.Errorf("stock after cancel = %d, want 2 (restores stock_taken, not quantity)", got)
	}
	if canceled.Lines[0].StockTaken != 0 {
		t.Errorf("stockTaken after cancel = %d, want 0", canceled.Lines[0].StockTaken)
	}

	// Canceling a draft never touches stock.
	d2 := mustPostInvoice(t, mux, itemInvoiceBody(item.ID, "", 1, ""))
	mustSetStatus(t, mux, d2.ID, "canceled")
	if got := stockOf(t, db, item.ID); got != 2 {
		t.Errorf("stock after canceling a draft = %d, want 2", got)
	}
}

func TestInvoiceStockSkipsReadyToPrintAndDeletedItems(t *testing.T) {
	mux, db := newInvoiceItemsMux(t)
	rtp := mustPostItem(t, mux, `{"name":"Made to order","priceCents":1000,"stockMode":"ready_to_print"}`)
	gone := mustPostItem(t, mux, `{"name":"Retired","priceCents":1000,"stockMode":"in_stock","stockQty":4}`)
	draft := mustPostInvoice(t, mux, fmt.Sprintf(`{"clientName":"Walk-in","lines":[
		{"description":"A","quantity":1,"unitPriceCents":100,"itemId":%q},
		{"description":"B","quantity":1,"unitPriceCents":100,"itemId":%q}]}`, rtp.ID, gone.ID))
	if rec := serveReq(mux, httptest.NewRequest("DELETE", "/api/items/"+gone.ID, nil)); rec.Code != http.StatusNoContent {
		t.Fatalf("delete item: %d %s", rec.Code, rec.Body.String())
	}

	sent := mustSetStatus(t, mux, draft.ID, "sent")
	for i, ln := range sent.Lines {
		if ln.StockTaken != 0 {
			t.Errorf("line %d stockTaken = %d, want 0", i, ln.StockTaken)
		}
	}
	if got := stockOf(t, db, rtp.ID); got != 0 {
		t.Errorf("ready_to_print stock = %d, want 0", got)
	}
	// Cancel with a deleted item and a zero stock_taken is still fine.
	mustSetStatus(t, mux, draft.ID, "canceled")
}

// Cancel restores onto an item even after its stock mode changed since the send.
func TestInvoiceCancelRestoresAfterStockModeChange(t *testing.T) {
	mux, db := newInvoiceItemsMux(t)
	item := mustPostItem(t, mux, `{"name":"Dragon","priceCents":1000,"stockMode":"in_stock","stockQty":5}`)
	draft := mustPostInvoice(t, mux, itemInvoiceBody(item.ID, "", 2, ""))
	mustSetStatus(t, mux, draft.ID, "sent")
	if _, err := db.Exec(`UPDATE items SET stock_mode = 'ready_to_print' WHERE id = ?`, item.ID); err != nil {
		t.Fatalf("change mode: %v", err)
	}
	mustSetStatus(t, mux, draft.ID, "canceled")
	if got := stockOf(t, db, item.ID); got != 5 {
		t.Errorf("stock after cancel = %d, want 5", got)
	}
}

// A failure partway through the stock step rolls back the status change and every stock write before it.
func TestInvoiceSendStockFailureRollsBack(t *testing.T) {
	mux, db := newInvoiceItemsMux(t)
	a := mustPostItem(t, mux, `{"name":"Alpha","priceCents":1000,"stockMode":"in_stock","stockQty":5}`)
	b := mustPostItem(t, mux, `{"name":"Bravo","priceCents":1000,"stockMode":"in_stock","stockQty":5}`)
	draft := mustPostInvoice(t, mux, fmt.Sprintf(`{"clientName":"Walk-in","lines":[
		{"description":"A","quantity":1,"unitPriceCents":100,"itemId":%q},
		{"description":"B","quantity":1,"unitPriceCents":100,"itemId":%q}]}`, a.ID, b.ID))
	if _, err := db.Exec(`CREATE TRIGGER fail_bravo BEFORE UPDATE OF stock_qty ON items WHEN OLD.name = 'Bravo'
		BEGIN SELECT RAISE(ABORT, 'forced'); END`); err != nil {
		t.Fatalf("trigger: %v", err)
	}

	if rec := setInvoiceStatus(t, mux, draft.ID, "sent"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: %d, want 500; body %s", rec.Code, rec.Body.String())
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM invoices WHERE id = ?`, draft.ID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "draft" {
		t.Errorf("status = %q, want draft", status)
	}
	if got := stockOf(t, db, a.ID); got != 5 {
		t.Errorf("Alpha stock = %d, want 5 (rolled back)", got)
	}
	var taken int
	if err := db.QueryRow(`SELECT COALESCE(SUM(stock_taken), 0) FROM invoice_lines WHERE invoice_id = ?`, draft.ID).Scan(&taken); err != nil || taken != 0 {
		t.Errorf("stock_taken sum = %d (err %v), want 0", taken, err)
	}
}

// issueDaysAgo is a YYYY-MM-DD issue date n days back, as an invoice body fragment.
func issueDaysAgo(n int) string {
	return fmt.Sprintf(`"issueDate":%q,`, time.Now().UTC().AddDate(0, 0, -n).Format("2006-01-02"))
}

func TestTopItemsCountsInPersonInvoiceLines(t *testing.T) {
	mux, db := newInvoiceItemsMux(t)
	item := mustPostItem(t, mux, `{"name":"Dragon","priceCents":1000,"optionGroups":[{"label":"Color","required":true,"options":[{"name":"Red","deltaCents":0},{"name":"Blue","deltaCents":0}]}],"stockMode":"ready_to_print"}`)

	sentInv := mustPostInvoice(t, mux, itemInvoiceBody(item.ID, "Color: Red", 2, issueDaysAgo(3)))
	mustSetStatus(t, mux, sentInv.ID, "sent")
	paidInv := mustPostInvoice(t, mux, itemInvoiceBody(item.ID, "Color: Blue", 3, issueDaysAgo(5)))
	mustSetStatus(t, mux, paidInv.ID, "sent")
	mustSetStatus(t, mux, paidInv.ID, "paid")
	mustPostInvoice(t, mux, itemInvoiceBody(item.ID, "Color: Red", 7, issueDaysAgo(1))) // draft: never counts
	canceled := mustPostInvoice(t, mux, itemInvoiceBody(item.ID, "Color: Red", 11, issueDaysAgo(1)))
	mustSetStatus(t, mux, canceled.ID, "sent")
	mustSetStatus(t, mux, canceled.ID, "canceled")
	old := mustPostInvoice(t, mux, itemInvoiceBody(item.ID, "Color: Red", 13, issueDaysAgo(60)))
	mustSetStatus(t, mux, old.ID, "sent")
	// An unlinked line on a sent invoice never counts.
	plain := mustPostInvoice(t, mux, `{"clientName":"Walk-in","lines":[{"description":"Hours","quantity":4,"unitPriceCents":100}]}`)
	mustSetStatus(t, mux, plain.ID, "sent")
	// A gallery sale for the same item merges into one ranking row.
	insertSale(t, db, item.ID, "Dragon", "Color: Red", 4, 4000, 2)

	var got topItemsResponse
	getJSON(t, mux, "/api/dashboard/top-items", &got)
	if len(got.Items) != 1 {
		t.Fatalf("items = %+v, want one merged row", got.Items)
	}
	it := got.Items[0]
	if it.Units != 9 || it.GalleryUnits != 4 || it.InPersonUnits != 5 || it.Orders != 3 || it.RevenueCents != 9000 {
		t.Errorf("item = units %d gallery %d in-person %d orders %d revenue %d; want 9 4 5 3 9000",
			it.Units, it.GalleryUnits, it.InPersonUnits, it.Orders, it.RevenueCents)
	}
	if got.TotalUnits != 9 || got.GalleryUnits != 4 || got.InPersonUnits != 5 || got.TotalRevenueCents != 9000 {
		t.Errorf("totals = %d/%d/%d/%d, want 9/4/5/9000", got.TotalUnits, got.GalleryUnits, got.InPersonUnits, got.TotalRevenueCents)
	}
	if fmt.Sprint(it.Options) != fmt.Sprint([]topItemOption{{"Color: Red", 6}, {"Color: Blue", 3}}) {
		t.Errorf("options = %v, want Color: Red 6, Color: Blue 3", it.Options)
	}

	// Issue-date windowing: the 60-day-old invoice joins the 90-day window.
	getJSON(t, mux, "/api/dashboard/top-items?days=90", &got)
	if got.InPersonUnits != 18 {
		t.Errorf("90-day in-person units = %d, want 18", got.InPersonUnits)
	}

	// Canceling a counted invoice drops it; the paid->sent correction keeps it.
	mustSetStatus(t, mux, sentInv.ID, "canceled")
	mustSetStatus(t, mux, paidInv.ID, "sent")
	getJSON(t, mux, "/api/dashboard/top-items", &got)
	if got.InPersonUnits != 3 {
		t.Errorf("in-person units after cancel = %d, want 3", got.InPersonUnits)
	}
}

// A line discount lowers revenue; a deleted item is named by its line description.
func TestTopItemsInPersonRevenueAndRemovedName(t *testing.T) {
	mux, _ := newInvoiceItemsMux(t)
	item := mustPostItem(t, mux, `{"name":"Retired","priceCents":1000,"stockMode":"ready_to_print"}`)
	body := strings.Replace(itemInvoiceBody(item.ID, "", 2, ""), `"itemId"`, `"discountCents":500,"itemId"`, 1)
	inv := mustPostInvoice(t, mux, body)
	mustSetStatus(t, mux, inv.ID, "sent")
	if rec := serveReq(mux, httptest.NewRequest("DELETE", "/api/items/"+item.ID, nil)); rec.Code != http.StatusNoContent {
		t.Fatalf("delete item: %d", rec.Code)
	}
	var got topItemsResponse
	getJSON(t, mux, "/api/dashboard/top-items", &got)
	if len(got.Items) != 1 || got.Items[0].RevenueCents != 1500 || !got.Items[0].Removed || got.Items[0].Name != "Widget" {
		t.Errorf("items = %+v, want one removed row named by the line description with 1500 revenue", got.Items)
	}
}

// raceOnce arms inv.afterLoad to run competing once, in the window between the next handler's load and its
// transaction: the deterministic stand-in for two requests that both loaded the same stale status.
func raceOnce(inv *invoices, competing func()) {
	inv.afterLoad = func() {
		inv.afterLoad = nil
		competing()
	}
}

// Two draft->sent requests holding the same stale "draft" snapshot take stock once; the loser gets 409.
func TestInvoiceSendRaceTakesStockOnce(t *testing.T) {
	for _, tc := range []struct{ name, first, second string }{
		{"status then status", "status", "status"},
		{"send then status", "send", "status"},
		{"status then send", "status", "send"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mux, db, inv := newInvoiceItemsMuxInv(t)
			item := mustPostItem(t, mux, `{"name":"Dragon","priceCents":1000,"stockMode":"in_stock","stockQty":5}`)
			cl := postClient(t, mux, `{"name":"Beta Print","email":"beta@example.com"}`)
			var client Client
			if err := json.Unmarshal(cl.Body.Bytes(), &client); err != nil {
				t.Fatalf("unmarshal client: %v", err)
			}
			draft := mustPostInvoice(t, mux, itemInvoiceBody(item.ID, "", 2, fmt.Sprintf(`"clientId":%q,`, client.ID)))

			do := func(kind string) *httptest.ResponseRecorder {
				if kind == "send" {
					return sendInvoice(t, mux, draft.ID, `{}`)
				}
				return setInvoiceStatus(t, mux, draft.ID, "sent")
			}
			var inner *httptest.ResponseRecorder
			raceOnce(inv, func() { inner = do(tc.first) })
			outer := do(tc.second)

			if inner.Code != http.StatusOK {
				t.Errorf("first (%s) = %d, want 200; body %s", tc.first, inner.Code, inner.Body.String())
			}
			if outer.Code != http.StatusConflict {
				t.Errorf("second (%s) = %d, want 409; body %s", tc.second, outer.Code, outer.Body.String())
			}
			if got := stockOf(t, db, item.ID); got != 3 {
				t.Errorf("stock = %d, want 3 (taken once)", got)
			}
			var sends int
			if err := db.QueryRow(`SELECT COUNT(*) FROM invoice_sends WHERE invoice_id = ?`, draft.ID).Scan(&sends); err != nil || sends != 1 {
				t.Errorf("invoice_sends = %d (err %v), want 1 (the loser rolls back its event)", sends, err)
			}
		})
	}
}

// Two sent->canceled requests on the same stale snapshot restore once.
func TestInvoiceCancelRaceRestoresOnce(t *testing.T) {
	mux, db, inv := newInvoiceItemsMuxInv(t)
	item := mustPostItem(t, mux, `{"name":"Dragon","priceCents":1000,"stockMode":"in_stock","stockQty":5}`)
	draft := mustPostInvoice(t, mux, itemInvoiceBody(item.ID, "", 2, ""))
	mustSetStatus(t, mux, draft.ID, "sent")

	var inner *httptest.ResponseRecorder
	raceOnce(inv, func() { inner = setInvoiceStatus(t, mux, draft.ID, "canceled") })
	outer := setInvoiceStatus(t, mux, draft.ID, "canceled")

	if inner.Code != http.StatusOK || outer.Code != http.StatusConflict {
		t.Errorf("codes = %d then %d, want 200 then 409", inner.Code, outer.Code)
	}
	if got := stockOf(t, db, item.ID); got != 5 {
		t.Errorf("stock = %d, want 5 (restored once)", got)
	}
}

// A PUT that loaded the invoice as a draft cannot rewrite its lines after a send landed.
func TestInvoicePutRaceAgainstSendIs409(t *testing.T) {
	mux, db, inv := newInvoiceItemsMuxInv(t)
	item := mustPostItem(t, mux, `{"name":"Dragon","priceCents":1000,"stockMode":"in_stock","stockQty":5}`)
	draft := mustPostInvoice(t, mux, itemInvoiceBody(item.ID, "", 2, ""))

	raceOnce(inv, func() { mustSetStatus(t, mux, draft.ID, "sent") })
	rec := putInvoice(t, mux, draft.ID, itemInvoiceBody(item.ID, "", 1, ""))
	if rec.Code != http.StatusConflict {
		t.Fatalf("PUT = %d, want 409; body %s", rec.Code, rec.Body.String())
	}
	var qty, taken int
	if err := db.QueryRow(`SELECT quantity, stock_taken FROM invoice_lines WHERE invoice_id = ?`, draft.ID).Scan(&qty, &taken); err != nil {
		t.Fatalf("read line: %v", err)
	}
	if qty != 2 || taken != 2 {
		t.Errorf("line = quantity %d stock_taken %d, want 2 and 2 (untouched)", qty, taken)
	}
}

// An invoice-level discount is pro-rated across the invoice's lines before it reaches Top items revenue:
// line * max(0, subtotal - discount) / subtotal. Shipping lines count in the subtotal.
func TestTopItemsProRatesInvoiceDiscount(t *testing.T) {
	mux, _ := newInvoiceItemsMux(t)
	item := mustPostItem(t, mux, `{"name":"Dragon","priceCents":1000,"stockMode":"ready_to_print"}`)

	// 2 x 1000 = 2000 less a 500 invoice discount: 1500.
	plain := mustPostInvoice(t, mux, itemInvoiceBody(item.ID, "", 2, `"discountCents":500,`))
	mustSetStatus(t, mux, plain.ID, "sent")

	// 2000 of items plus a 1000 shipping line, 1000 off: the item line keeps 2/3 of 2000 = 1333.
	withShipping := strings.Replace(itemInvoiceBody(item.ID, "", 2, `"discountCents":1000,`), `}]}`,
		`},{"description":"Shipping","kind":"shipping","quantity":1,"unitPriceCents":1000}]}`, 1)
	ship := mustPostInvoice(t, mux, withShipping)
	mustSetStatus(t, mux, ship.ID, "sent")

	var got topItemsResponse
	getJSON(t, mux, "/api/dashboard/top-items", &got)
	if len(got.Items) != 1 || got.Items[0].RevenueCents != 1500+1333 {
		t.Errorf("items = %+v, want one row with revenue %d", got.Items, 1500+1333)
	}
}

// A payment posted between the cancel's load and its transaction blocks the cancel with the payments 409,
// and the stock stays taken.
func TestInvoiceCancelRaceWithPaymentIs409(t *testing.T) {
	mux, db, inv := newInvoiceItemsMuxInv(t)
	item := mustPostItem(t, mux, `{"name":"Dragon","priceCents":1000,"stockMode":"in_stock","stockQty":5}`)
	draft := mustPostInvoice(t, mux, itemInvoiceBody(item.ID, "", 2, ""))
	mustSetStatus(t, mux, draft.ID, "sent")

	raceOnce(inv, func() {
		if _, err := db.Exec(`INSERT INTO payments (id, invoice_id, paid_date, amount_cents, created_at) VALUES ('p1', ?, '2026-01-01', 500, 't')`, draft.ID); err != nil {
			t.Error(err)
		}
	})
	rec := setInvoiceStatus(t, mux, draft.ID, "canceled")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "has payments") {
		t.Fatalf("cancel = %d %s, want 409 naming payments", rec.Code, rec.Body.String())
	}
	if got := stockOf(t, db, item.ID); got != 3 {
		t.Errorf("stock = %d, want 3 (cancel rolled back)", got)
	}
}
