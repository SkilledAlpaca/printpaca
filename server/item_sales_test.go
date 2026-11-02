package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// salesRows counts item_sales rows for one request id.
func salesRows(t *testing.T, db *sql.DB, requestID string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM item_sales WHERE request_id = ?`, requestID).Scan(&n); err != nil {
		t.Fatalf("count item_sales: %v", err)
	}
	return n
}

// Finalizing an item order records exactly one sale with the request's snapshot; a quote request records none.
func TestFinalizeItemOrderRecordsSale(t *testing.T) {
	dir := t.TempDir()
	adminMux, publicMux, db, _ := newRequestsTestSetup(t, dir)

	id := insertItemOrderRequest(t, db, "item-1", "Benchy", 3, "viewed")
	if _, err := db.Exec(`UPDATE quote_requests SET price_cents = 4500, option_name = 'Red' WHERE id = ?`, id); err != nil {
		t.Fatalf("set price: %v", err)
	}
	if rec := setRequestStatus(t, adminMux, id, "finalized"); rec.Code != http.StatusOK {
		t.Fatalf("finalize: %d %s", rec.Code, rec.Body.String())
	}

	var itemID, name, option, soldAt, by string
	var qty, revenue int
	err := db.QueryRow(`SELECT item_id, item_name, option_name, quantity, revenue_cents, sold_at, recorded_by FROM item_sales WHERE request_id = ?`, id).
		Scan(&itemID, &name, &option, &qty, &revenue, &soldAt, &by)
	if err != nil {
		t.Fatalf("read sale: %v", err)
	}
	var finalizedAt string
	if err := db.QueryRow(`SELECT finalized_at FROM quote_requests WHERE id = ?`, id).Scan(&finalizedAt); err != nil {
		t.Fatalf("read finalized_at: %v", err)
	}
	if itemID != "item-1" || name != "Benchy" || option != "Red" || qty != 3 || revenue != 4500 || soldAt != finalizedAt {
		t.Errorf("sale = %q %q %q %d %d %q, want item-1 Benchy Red 3 4500 %q", itemID, name, option, qty, revenue, soldAt, finalizedAt)
	}
	if n := salesRows(t, db, id); n != 1 {
		t.Errorf("sales rows for the request = %d, want 1", n)
	}

	quoteID := seedQuoteRequest(t, publicMux, "PLA")
	getRequest(t, adminMux, quoteID)
	if rec := setRequestStatus(t, adminMux, quoteID, "finalized"); rec.Code != http.StatusOK {
		t.Fatalf("finalize quote: %d %s", rec.Code, rec.Body.String())
	}
	if n := salesRows(t, db, quoteID); n != 0 {
		t.Errorf("sales rows for a quote request = %d, want 0", n)
	}
}

// A failing sale insert rolls the whole finalize back: status and stock are unchanged.
func TestFinalizeItemOrderSaleInsertFailureRollsBack(t *testing.T) {
	dir := t.TempDir()
	adminMux, _, db, _ := newRequestsTestSetup(t, dir)

	rec := postItem(t, adminMux, `{"name":"Benchy","priceCents":1500,"public":true,"stockMode":"in_stock","stockQty":5}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST item: %d %s", rec.Code, rec.Body.String())
	}
	var item Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("unmarshal item: %v", err)
	}
	id := insertItemOrderRequest(t, db, item.ID, item.Name, 2, "viewed")
	if _, err := db.Exec(`INSERT INTO item_sales (request_id, item_id, item_name, quantity, revenue_cents, sold_at)
		VALUES (?, 'x', 'x', 1, 1, '2026-01-01T00:00:00Z')`, id); err != nil {
		t.Fatalf("pre-insert sale: %v", err)
	}

	rec = setRequestStatus(t, adminMux, id, "finalized")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("finalize with a conflicting sale row: %d, want 500; body = %s", rec.Code, rec.Body.String())
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM quote_requests WHERE id = ?`, id).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "viewed" {
		t.Errorf("status after failed finalize = %q, want viewed", status)
	}
	if _, qty := itemStock(t, db, item.ID); qty != 5 {
		t.Errorf("stock_qty after failed finalize = %d, want 5 (unchanged)", qty)
	}
}

// Deleting a finalized item request keeps its sale.
func TestDeleteFinalizedItemRequestKeepsSale(t *testing.T) {
	dir := t.TempDir()
	adminMux, _, db, _ := newRequestsTestSetup(t, dir)

	id := insertItemOrderRequest(t, db, "item-1", "Benchy", 1, "viewed")
	if rec := setRequestStatus(t, adminMux, id, "finalized"); rec.Code != http.StatusOK {
		t.Fatalf("finalize: %d %s", rec.Code, rec.Body.String())
	}
	if rec := deleteRequest(t, adminMux, id); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if n := salesRows(t, db, id); n != 1 {
		t.Errorf("sales rows after deleting the request = %d, want 1", n)
	}
}

// insertSale inserts one item_sales row sold daysAgo days ago (plus a minute, so a window edge never races the request clock).
func insertSale(t *testing.T, db *sql.DB, itemID, name, option string, qty, revenue, daysAgo int) {
	t.Helper()
	soldAt := time.Now().UTC().AddDate(0, 0, -daysAgo).Add(time.Minute).Format(time.RFC3339)
	if _, err := db.Exec(`INSERT INTO item_sales (request_id, item_id, item_name, option_name, quantity, revenue_cents, sold_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, newID(), itemID, name, option, qty, revenue, soldAt); err != nil {
		t.Fatalf("insert sale: %v", err)
	}
}

func insertGalleryItem(t *testing.T, db *sql.DB, id, name string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(`INSERT INTO items (id, name, price_cents, created_at, updated_at) VALUES (?, ?, 1000, ?, ?)`, id, name, now, now); err != nil {
		t.Fatalf("insert item: %v", err)
	}
}

func getTopItems(t *testing.T, mux *http.ServeMux, query string) topItemsResponse {
	t.Helper()
	var out topItemsResponse
	getJSON(t, mux, "/api/dashboard/top-items"+query, &out)
	return out
}

func TestTopItemsWindowsAndValidation(t *testing.T) {
	mux, db := newTestMuxWithDB(t, t.TempDir())
	insertGalleryItem(t, db, "a", "Alpha")
	insertSale(t, db, "a", "Alpha", "", 2, 2000, 10)
	insertSale(t, db, "a", "Alpha", "", 3, 3000, 60)
	insertSale(t, db, "a", "Alpha", "", 4, 4000, 200)
	insertSale(t, db, "a", "Alpha", "", 5, 5000, 400)

	for _, tc := range []struct {
		query string
		days  int
		units int
		cents int
	}{
		{"", 30, 2, 2000},
		{"?days=30", 30, 2, 2000},
		{"?days=90", 90, 5, 5000},
		{"?days=365", 365, 9, 9000},
	} {
		got := getTopItems(t, mux, tc.query)
		if got.Days != tc.days || got.TotalUnits != tc.units || got.TotalRevenueCents != tc.cents {
			t.Errorf("%q: days/units/cents = %d/%d/%d, want %d/%d/%d", tc.query, got.Days, got.TotalUnits, got.TotalRevenueCents, tc.days, tc.units, tc.cents)
		}
	}

	for _, q := range []string{"?days=7", "?days=abc", "?days=-30", "?days=0"} {
		rec := serveReq(mux, httptest.NewRequest("GET", "/api/dashboard/top-items"+q, nil))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", q, rec.Code)
		}
	}
}

func TestTopItemsRankingTieBreaks(t *testing.T) {
	mux, db := newTestMuxWithDB(t, t.TempDir())
	for id, name := range map[string]string{"1": "zebra", "2": "Apple", "3": "Mango", "4": "Cherry", "5": "Banana"} {
		insertGalleryItem(t, db, id, name)
	}
	insertSale(t, db, "1", "zebra", "", 5, 1000, 1) // 5 units, lowest revenue of the 5-unit group
	insertSale(t, db, "2", "Apple", "", 5, 2000, 1) // 5 units, same revenue as Mango: name decides
	insertSale(t, db, "3", "Mango", "", 5, 2000, 1)
	insertSale(t, db, "4", "Cherry", "", 9, 100, 1)  // most units
	insertSale(t, db, "5", "Banana", "", 1, 9999, 1) // least units despite top revenue

	got := getTopItems(t, mux, "")
	var names []string
	for _, it := range got.Items {
		names = append(names, it.Name)
	}
	want := []string{"Cherry", "Apple", "Mango", "zebra", "Banana"}
	if fmt.Sprint(names) != fmt.Sprint(want) {
		t.Fatalf("order = %v, want %v", names, want)
	}
}

func TestTopItemsCapsAtTenButTotalsCoverEverySale(t *testing.T) {
	mux, db := newTestMuxWithDB(t, t.TempDir())
	totalUnits, totalCents := 0, 0
	for i := 1; i <= 12; i++ {
		id := fmt.Sprintf("i%02d", i)
		insertGalleryItem(t, db, id, "Item "+id)
		insertSale(t, db, id, "Item "+id, "", i, i*100, 1)
		totalUnits += i
		totalCents += i * 100
	}
	got := getTopItems(t, mux, "")
	if len(got.Items) != 10 {
		t.Fatalf("items = %d, want 10", len(got.Items))
	}
	if got.TotalUnits != totalUnits || got.TotalRevenueCents != totalCents {
		t.Errorf("totals = %d/%d, want %d/%d (all 12 items)", got.TotalUnits, got.TotalRevenueCents, totalUnits, totalCents)
	}
	if got.Items[0].Units != 12 || got.Items[9].Units != 3 {
		t.Errorf("cap kept units %d..%d, want 12..3", got.Items[0].Units, got.Items[9].Units)
	}
}

func TestTopItemsOptionBreakdown(t *testing.T) {
	mux, db := newTestMuxWithDB(t, t.TempDir())
	insertGalleryItem(t, db, "a", "Alpha")
	insertSale(t, db, "a", "Alpha", "Red", 2, 2000, 1)
	insertSale(t, db, "a", "Alpha", "Red", 2, 2000, 2)
	insertSale(t, db, "a", "Alpha", "Blue", 4, 4000, 3)
	insertSale(t, db, "a", "Alpha", "Azure", 1, 1000, 3)
	insertSale(t, db, "a", "Alpha", "", 3, 3000, 4)

	got := getTopItems(t, mux, "")
	if len(got.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(got.Items))
	}
	it := got.Items[0]
	if it.Units != 12 || it.Orders != 5 || it.RevenueCents != 12000 {
		t.Errorf("totals = %d units, %d orders, %d cents; want 12, 5, 12000 (the empty-option sale counts)", it.Units, it.Orders, it.RevenueCents)
	}
	want := []topItemOption{{"Blue", 4}, {"Red", 4}, {"Azure", 1}}
	if fmt.Sprint(it.Options) != fmt.Sprint(want) {
		t.Errorf("options = %v, want %v (units desc, then name asc; empty name omitted)", it.Options, want)
	}
}

func TestTopItemsNames(t *testing.T) {
	mux, db := newTestMuxWithDB(t, t.TempDir())

	// Renamed: the live name wins over the sale snapshots.
	insertGalleryItem(t, db, "live", "New name")
	insertSale(t, db, "live", "Old name", "", 1, 100, 5)

	// Removed: no items row, so the newest snapshot in the window names it.
	insertSale(t, db, "gone", "Older snapshot", "", 1, 100, 20)
	insertSale(t, db, "gone", "Newest snapshot", "", 1, 100, 2)
	insertSale(t, db, "gone", "Outside window", "", 1, 100, 80)

	got := getTopItems(t, mux, "")
	byID := map[string]topItem{}
	for _, it := range got.Items {
		byID[it.ItemID] = it
	}
	if it := byID["live"]; it.Name != "New name" || it.Removed {
		t.Errorf("renamed item = %q removed=%v, want \"New name\" removed=false", it.Name, it.Removed)
	}
	if it := byID["gone"]; it.Name != "Newest snapshot" || !it.Removed {
		t.Errorf("removed item = %q removed=%v, want \"Newest snapshot\" removed=true", it.Name, it.Removed)
	}
}

func TestTopItemsEmptyServesArraysNotNull(t *testing.T) {
	mux, _ := newTestMuxWithDB(t, t.TempDir())
	rec := serveReq(mux, httptest.NewRequest("GET", "/api/dashboard/top-items", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(raw["items"]) != "[]" {
		t.Errorf("items = %s, want []", raw["items"])
	}

	// A populated item without options still serves "options": [].
	mux2, db := newTestMuxWithDB(t, t.TempDir())
	insertGalleryItem(t, db, "a", "Alpha")
	insertSale(t, db, "a", "Alpha", "", 1, 100, 1)
	rec = serveReq(mux2, httptest.NewRequest("GET", "/api/dashboard/top-items", nil))
	var withItem struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &withItem); err != nil || len(withItem.Items) != 1 {
		t.Fatalf("unmarshal populated: %v %s", err, rec.Body.String())
	}
	if string(withItem.Items[0]["options"]) != "[]" {
		t.Errorf("options = %s, want []", withItem.Items[0]["options"])
	}
}
