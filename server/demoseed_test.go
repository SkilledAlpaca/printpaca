package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// getJSON issues a GET against mux and decodes a 200 JSON body into out,
// failing the test on any non-200 status or decode error.
func getJSON(t *testing.T, mux *http.ServeMux, path string, out any) {
	t.Helper()
	rec := serveReq(mux, httptest.NewRequest("GET", path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: want 200, got %d: %s", path, rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
		t.Fatalf("GET %s: unmarshal: %v: %s", path, err, rec.Body.String())
	}
}

// Seeds a fresh database and reads everything back through the real REST read paths (not SQL), proving the rows are valid for a normal client.
func TestSeedDemoDataProducesValidReadableRecords(t *testing.T) {
	dir := t.TempDir()
	db, _, store := openTestEnv(t, dir)

	if err := seedDemoData(db, store, dir); err != nil {
		t.Fatalf("seedDemoData: %v", err)
	}

	mux := routesMux(db, store, newAuthState("", nil), dir, false)

	// --- clients --------------------------------------------------------
	var clientsResp struct {
		Clients []Client `json:"clients"`
	}
	getJSON(t, mux, "/api/clients", &clientsResp)
	if len(clientsResp.Clients) == 0 {
		t.Fatal("expected seeded clients, got none")
	}

	// --- quotes -----------------------------------------------------------
	var quotesResp struct {
		Quotes []Quote `json:"quotes"`
	}
	getJSON(t, mux, "/api/quotes", &quotesResp)
	if len(quotesResp.Quotes) == 0 {
		t.Fatal("expected seeded quotes, got none")
	}
	quoteStatuses := map[string]bool{}
	for _, q := range quotesResp.Quotes {
		quoteStatuses[q.Status] = true
		if len(q.FormState) == 0 || string(q.FormState) == "null" {
			t.Fatalf("quote %s: formState missing from the read path", q.ID)
		}
	}
	if len(quoteStatuses) < 2 {
		t.Fatalf("expected quotes to span more than one status, got %v", quoteStatuses)
	}

	// Exactly one seeded quote is the 3-part walk-in order, stored as one
	// unit (quantity 1) priced at its item revenue.
	multi := 0
	for _, q := range quotesResp.Quotes {
		if q.PartCount < 2 {
			continue
		}
		multi++
		if q.PartCount != 3 || q.Quantity != 1 || q.ClientID != nil || !q.Valid ||
			q.ResolvedPriceCents == nil || *q.ResolvedPriceCents <= 0 || q.PerUnitTotalCents != q.BatchTotalCents {
			t.Fatalf("multi-part demo quote has unexpected columns: %+v", q)
		}
	}
	if multi != 1 {
		t.Fatalf("want exactly one multi-part demo quote, got %d", multi)
	}

	// Every seeded quote, single-part or not, carries a {parts, pricing} inputs bag and the final
	// formState web/src/lib/state.svelte.ts reads (PartFormState / OrderForm keys; no per-part id). A key
	// drifting from the editor's would silently restore as a default.
	partKeys := []string{"label", "weightG", "timeHours", "timeMinutes", "pricePerKg", "wastePct", "printerId", "materialId", "watts", "machineCost", "machineLifeHours", "flatRate", "offPeakFrac", "failureRatePct", "prepMinutes", "laborRate", "monthlyFixed", "monthlyHours", "quantity", "hardware", "presetName", "presetModified", "salePriceOverride", "spoolOverride"}
	orderKeys := []string{"sellingEnabled", "strategy", "marginPct", "markupMultiplier", "taxRatePct", "shippingCharge", "shippingCost", "feeScheduleName"}
	for _, q := range quotesResp.Quotes {
		order, err := decodeQuoteInputs(q.Inputs)
		if err != nil {
			t.Fatalf("quote %s: inputs are not a {parts, pricing} bag: %v", q.ID, err)
		}
		if len(order.Parts) != q.PartCount {
			t.Errorf("quote %s: inputs carry %d parts, part_count is %d", q.ID, len(order.Parts), q.PartCount)
		}
		var fs struct {
			Version int              `json:"version"`
			Parts   []map[string]any `json:"parts"`
			Order   map[string]any   `json:"order"`
		}
		if err := json.Unmarshal(q.FormState, &fs); err != nil {
			t.Fatalf("quote %s: formState: %v", q.ID, err)
		}
		if fs.Version != 1 || len(fs.Parts) != q.PartCount {
			t.Fatalf("quote %s: formState version=%d parts=%d, want 1 and %d", q.ID, fs.Version, len(fs.Parts), q.PartCount)
		}
		for i, p := range fs.Parts {
			for _, k := range partKeys {
				if _, ok := p[k]; !ok {
					t.Errorf("quote %s part %d formState missing key %q", q.ID, i, k)
				}
			}
			if _, ok := p["id"]; ok {
				t.Errorf("quote %s part %d formState carries an id", q.ID, i)
			}
			if len(p) != len(partKeys) {
				t.Errorf("quote %s part %d formState has %d keys, want %d", q.ID, i, len(p), len(partKeys))
			}
		}
		for _, k := range orderKeys {
			if _, ok := fs.Order[k]; !ok {
				t.Errorf("quote %s order formState missing key %q", q.ID, k)
			}
		}
		if len(fs.Order) != len(orderKeys) {
			t.Errorf("quote %s order formState has %d keys, want %d", q.ID, len(fs.Order), len(orderKeys))
		}
		if selling, _ := fs.Order["sellingEnabled"].(bool); selling != (order.Pricing != nil) {
			t.Errorf("quote %s: formState sellingEnabled=%v but inputs pricing present=%v", q.ID, selling, order.Pricing != nil)
		}
	}

	// Check the raw quotes.inputs column unmarshals with a non-empty formState; a successful GET alone can't tell an empty formState from a real one.
	var rawInputs string
	if err := db.QueryRow(`SELECT inputs FROM quotes LIMIT 1`).Scan(&rawInputs); err != nil {
		t.Fatalf("read raw quotes.inputs: %v", err)
	}
	var wrapper storedQuoteInputs
	if err := json.Unmarshal([]byte(rawInputs), &wrapper); err != nil {
		t.Fatalf("quotes.inputs does not unmarshal to the {inputs,options,formState} wrapper: %v", err)
	}
	if len(wrapper.Inputs) == 0 {
		t.Fatal("wrapper.Inputs is empty")
	}
	if len(wrapper.FormState) == 0 || string(wrapper.FormState) == "{}" || string(wrapper.FormState) == "null" {
		t.Fatalf("wrapper.FormState is empty, want a real editor snapshot: %s", wrapper.FormState)
	}

	// --- invoices + payments ---------------------------------------------
	var invResp struct {
		Invoices []InvoiceSummary `json:"invoices"`
	}
	getJSON(t, mux, "/api/invoices", &invResp)
	if len(invResp.Invoices) == 0 {
		t.Fatal("expected seeded invoices, got none")
	}
	invStatuses := map[string]bool{}
	paidChecked := 0
	for _, s := range invResp.Invoices {
		invStatuses[s.Status] = true
		if s.Status != "paid" {
			continue
		}
		var full Invoice
		getJSON(t, mux, "/api/invoices/"+s.ID, &full)
		if len(full.Payments) == 0 {
			t.Fatalf("paid invoice %s has no payments", s.ID)
		}
		sum := 0
		for _, p := range full.Payments {
			sum += p.AmountCents
		}
		if sum != full.TotalCents {
			t.Fatalf("paid invoice %s: payments sum %d cents != total %d cents", s.ID, sum, full.TotalCents)
		}
		paidChecked++
	}
	if paidChecked == 0 {
		t.Fatal("expected at least one paid invoice with payments summing to its total")
	}
	if len(invStatuses) < 2 {
		t.Fatalf("expected invoices to span more than one status, got %v", invStatuses)
	}

	// --- items --------------------------------------------------------
	var itemsResp struct {
		Items []Item `json:"items"`
	}
	getJSON(t, mux, "/api/items", &itemsResp)
	if len(itemsResp.Items) == 0 {
		t.Fatal("expected seeded items, got none")
	}
	var nPublic, nNonPublic int
	var inStockItem, readyToPrintItem *Item
	for i := range itemsResp.Items {
		it := &itemsResp.Items[i]
		if it.Public {
			nPublic++
		} else {
			nNonPublic++
		}
		if it.Public && it.StockMode == stockModeInStock && inStockItem == nil {
			inStockItem = it
		}
		if it.Public && it.StockMode == stockModeReadyToPrint && readyToPrintItem == nil {
			readyToPrintItem = it
		}
	}
	if nPublic < 2 {
		t.Fatalf("expected at least 2 public items, got %d", nPublic)
	}
	if nNonPublic == 0 {
		t.Fatal("expected at least 1 non-public item, got none")
	}
	if inStockItem == nil {
		t.Fatal("expected a public in_stock item")
	}
	if inStockItem.OptionLabel == "" || len(inStockItem.Options) < 2 {
		t.Fatalf("expected the seeded in_stock item to have a two-option list, got %+v", inStockItem)
	}
	hasPositiveDelta := false
	for _, o := range inStockItem.Options {
		if o.DeltaCents > 0 {
			hasPositiveDelta = true
		}
	}
	if !hasPositiveDelta {
		t.Fatalf("expected one of the seeded in_stock item's options to have a positive deltaCents, got %+v", inStockItem.Options)
	}
	if inStockItem.StockQty <= 0 {
		t.Fatalf("expected the seeded in_stock item to have stockQty > 0, got %d", inStockItem.StockQty)
	}
	if readyToPrintItem == nil {
		t.Fatal("expected a public ready_to_print item")
	}
	if len(readyToPrintItem.Options) != 0 {
		t.Fatalf("expected the seeded ready_to_print item to have no options, got %+v", readyToPrintItem.Options)
	}
	// Seeded items are the exception to the no-file convention: each carries a real on-disk placeholder photo, content-addressed like an upload (itemImageName).
	for _, it := range itemsResp.Items {
		if it.ImageFile == "" {
			t.Fatalf("expected every seeded item to carry an image file, item %q has none", it.Name)
		}
		if !itemImageName.MatchString(it.ImageFile) {
			t.Fatalf("item %q: image file %q does not match the itemImageName pattern", it.Name, it.ImageFile)
		}
		info, err := os.Stat(itemImagePath(dir, it.ImageFile))
		if err != nil {
			t.Fatalf("item %q: stat seeded image %q: %v", it.Name, it.ImageFile, err)
		}
		if info.Size() == 0 {
			t.Fatalf("item %q: seeded image %q is empty", it.Name, it.ImageFile)
		}
	}

	// The public gallery must serve only the public items, never the
	// non-public one -- proves the public filter with real seeded data.
	publicMux, _ := buildPublicMux(store, db, dir, false, false)
	var publicItemsResp struct {
		Items []publicItem `json:"items"`
	}
	getJSON(t, publicMux, "/api/public/items", &publicItemsResp)
	if len(publicItemsResp.Items) != nPublic {
		t.Fatalf("public gallery: got %d items, want %d (public only)", len(publicItemsResp.Items), nPublic)
	}
	for _, pi := range publicItemsResp.Items {
		for _, it := range itemsResp.Items {
			if it.ID == pi.ID && !it.Public {
				t.Fatalf("non-public item %q leaked into the public gallery", it.Name)
			}
		}
	}

	// --- item order in the requests inbox --------------------------------
	var reqResp struct {
		Requests []QuoteRequest `json:"requests"`
	}
	getJSON(t, mux, "/api/requests", &reqResp)
	var itemOrder *QuoteRequest
	for i := range reqResp.Requests {
		if reqResp.Requests[i].Kind == "item" && reqResp.Requests[i].Status != "finalized" { // the finalized ones are the item_sales history
			itemOrder = &reqResp.Requests[i]
			break
		}
	}
	if itemOrder == nil {
		t.Fatal("expected a seeded item-order request (kind = \"item\"), got none")
	}
	if itemOrder.ItemID != inStockItem.ID {
		t.Fatalf("seeded item order references item %q, want the in_stock item %q", itemOrder.ItemID, inStockItem.ID)
	}
	if itemOrder.ItemName == "" || itemOrder.Quantity <= 0 || itemOrder.PriceCents <= 0 {
		t.Fatalf("seeded item order missing snapshot fields: %+v", itemOrder)
	}
	if itemOrder.Status != "new" && itemOrder.Status != "viewed" {
		t.Fatalf("seeded item order status = %q, want \"new\" or \"viewed\"", itemOrder.Status)
	}
}

// The seeded finalized gallery orders and their item_sales rows pair up one to one, span at least three public items, and give the 30- and 90-day Top items windows different rankings.
func TestSeedDemoDataSeedsItemSales(t *testing.T) {
	dir := t.TempDir()
	db, _, store := openTestEnv(t, dir)
	if err := seedDemoData(db, store, dir); err != nil {
		t.Fatalf("seedDemoData: %v", err)
	}

	var nSales, nPaired, nItems int
	if err := db.QueryRow(`SELECT COUNT(*) FROM item_sales`).Scan(&nSales); err != nil {
		t.Fatalf("count item_sales: %v", err)
	}
	if nSales < 8 || nSales > 12 {
		t.Fatalf("item_sales rows = %d, want 8 to 12", nSales)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM item_sales s JOIN quote_requests q ON q.id = s.request_id
		WHERE q.kind = 'item' AND q.status = 'finalized' AND q.finalized_at = s.sold_at
		  AND q.price_cents = s.revenue_cents AND q.quantity = s.quantity AND q.item_id = s.item_id
		  AND q.option_name = s.option_name`).Scan(&nPaired); err != nil {
		t.Fatalf("count paired: %v", err)
	}
	if nPaired != nSales {
		t.Fatalf("paired sales = %d, want all %d to match a finalized item order", nPaired, nSales)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM items WHERE public = 1 AND id IN (SELECT item_id FROM item_sales)`).Scan(&nItems); err != nil {
		t.Fatalf("count sold items: %v", err)
	}
	if nItems < 3 {
		t.Fatalf("sold public items = %d, want at least 3", nItems)
	}

	mux := routesMux(db, store, newAuthState("", nil), dir, false)
	var d30, d90 topItemsResponse
	getJSON(t, mux, "/api/dashboard/top-items?days=30", &d30)
	getJSON(t, mux, "/api/dashboard/top-items?days=90", &d90)
	if len(d30.Items) == 0 || len(d90.Items) == 0 {
		t.Fatalf("empty ranking: 30d %d items, 90d %d items", len(d30.Items), len(d90.Items))
	}
	if d30.Items[0].ItemID == d90.Items[0].ItemID {
		t.Fatalf("30d and 90d windows both lead with %q, want different rankings", d30.Items[0].Name)
	}
}

// ---------------------------------------------------------------------------
// Employee accounts + users.multiUser: true.
// ---------------------------------------------------------------------------

// Pins config.yaml gaining users.multiUser: true (on disk and in the live store) and the users table gaining demoEmployeeSpecs'
// employees, each with granted permissions and a real bcrypt hash of demoEmployeePassword that a login can verify.
func TestSeedDemoDataSeedsMultiUserModeAndEmployees(t *testing.T) {
	dir := t.TempDir()
	db, _, store := openTestEnv(t, dir)

	if err := seedDemoData(db, store, dir); err != nil {
		t.Fatalf("seedDemoData: %v", err)
	}

	if !store.Get().Users.MultiUser {
		t.Fatal("expected users.multiUser: true in the live store after a fresh demo seed")
	}
	onDisk := loadTestConfig(t, dir)
	if !onDisk.Users.MultiUser {
		t.Fatal("expected users.multiUser: true in config.yaml on disk after a fresh demo seed")
	}

	if len(demoEmployeeSpecs) == 0 {
		t.Fatal("test precondition: demoEmployeeSpecs must be non-empty")
	}
	for _, spec := range demoEmployeeSpecs {
		u, ok, err := getUserByUsername(db, spec.username)
		if err != nil {
			t.Fatalf("getUserByUsername(%s): %v", spec.username, err)
		}
		if !ok {
			t.Fatalf("expected seeded employee %q to exist", spec.username)
		}
		if u.Disabled {
			t.Fatalf("seeded employee %q must not be disabled", spec.username)
		}
		gotPerms := append([]string(nil), u.Permissions...)
		sort.Strings(gotPerms)
		wantPerms := append([]string(nil), spec.permissions...)
		sort.Strings(wantPerms)
		if strings.Join(gotPerms, ",") != strings.Join(wantPerms, ",") {
			t.Fatalf("seeded employee %q permissions = %v, want %v", spec.username, gotPerms, wantPerms)
		}
		if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(demoEmployeePassword)); err != nil {
			t.Fatalf("seeded employee %q password hash does not verify against %q: %v", spec.username, demoEmployeePassword, err)
		}
	}

	// Pin distinct, non-overlapping grants directly; identical permissions would pass every check above.
	if len(demoEmployeeSpecs) >= 2 {
		seen := map[string]bool{}
		for _, p := range demoEmployeeSpecs[0].permissions {
			seen[p] = true
		}
		for _, p := range demoEmployeeSpecs[1].permissions {
			if seen[p] {
				t.Fatalf("demoEmployeeSpecs[0] and [1] both grant %q -- want disjoint grants", p)
			}
		}
	}
}

// Attribution stamps: seeded records must show a mix of authors, not uniformly "admin" (see demoAttributionAt).

// scanAttributionColumn runs query (which must select exactly one string
// column) and returns every row's value, failing the test on any error.
func scanAttributionColumn(t *testing.T, db *sql.DB, query string) []string {
	t.Helper()
	rows, err := db.Query(query)
	if err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scan %q: %v", query, err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %q: %v", query, err)
	}
	return out
}

// Every attribution-bearing table seeded here (quote_requests: updated_by only) comes out with a mix of demoAttributionUsernames,
// not the schema's empty-string default, so the demo shows content for the "Created by / Updated by" display.
func TestSeedDemoDataStampsAttribution(t *testing.T) {
	dir := t.TempDir()
	db, _, store := openTestEnv(t, dir)

	if err := seedDemoData(db, store, dir); err != nil {
		t.Fatalf("seedDemoData: %v", err)
	}

	isKnownAuthor := func(v string) bool {
		return slices.Contains(demoAttributionUsernames, v)
	}

	// clients/quotes/invoices/payments/items: every row must carry a known,
	// non-empty author on both columns (or the one column payments/items
	// have, per their own schema).
	for _, tbl := range []struct {
		table   string
		columns []string
	}{
		{"clients", []string{"created_by", "updated_by"}},
		{"quotes", []string{"created_by", "updated_by"}},
		{"invoices", []string{"created_by", "updated_by"}},
		{"payments", []string{"created_by", "updated_by"}},
		{"items", []string{"created_by", "updated_by"}},
	} {
		seenAcrossTable := map[string]bool{}
		for _, col := range tbl.columns {
			vals := scanAttributionColumn(t, db, "SELECT "+col+" FROM "+tbl.table)
			if len(vals) == 0 {
				t.Fatalf("%s.%s: expected seeded rows, got none", tbl.table, col)
			}
			for _, v := range vals {
				if v == "" {
					t.Fatalf("%s.%s: expected every seeded row to be attributed, found an empty value", tbl.table, col)
				}
				if !isKnownAuthor(v) {
					t.Fatalf("%s.%s: value %q is not one of demoAttributionUsernames %v", tbl.table, col, v, demoAttributionUsernames)
				}
				seenAcrossTable[v] = true
			}
		}
		if len(seenAcrossTable) < 2 {
			t.Fatalf("%s: expected a mix of authors across created_by/updated_by, got only %v", tbl.table, seenAcrossTable)
		}
	}

	// quote_requests.updated_by: "" for a still-"new" request (nobody on
	// staff has touched it), a known author for "viewed"/"finalized".
	rows, err := db.Query(`SELECT status, updated_by FROM quote_requests WHERE kind != 'item'`)
	if err != nil {
		t.Fatalf("query quote_requests: %v", err)
	}
	defer rows.Close()
	var sawNewEmpty, sawTouchedAttributed bool
	for rows.Next() {
		var status, updatedBy string
		if err := rows.Scan(&status, &updatedBy); err != nil {
			t.Fatalf("scan quote_requests: %v", err)
		}
		if status == "new" {
			if updatedBy != "" {
				t.Fatalf("quote_requests: a still-new request must have updated_by = '', got %q", updatedBy)
			}
			sawNewEmpty = true
			continue
		}
		if updatedBy == "" || !isKnownAuthor(updatedBy) {
			t.Fatalf("quote_requests: status %q must have a known updated_by author, got %q", status, updatedBy)
		}
		sawTouchedAttributed = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate quote_requests: %v", err)
	}
	if !sawNewEmpty {
		t.Fatal("expected at least one still-\"new\" seeded request with updated_by = ''")
	}
	if !sawTouchedAttributed {
		t.Fatal("expected at least one seeded request past \"new\" with a known updated_by author")
	}
}

// Seeds two separate databases and CONFIG_DIRs and checks the generated placeholder images (filenames and bytes) are identical:
// drawDemoItemImage uses no randomness or clock, and this exercises that through the real seeding path.
func TestSeedDemoItemImagesAreDeterministic(t *testing.T) {
	seedInto := func(t *testing.T) (dir string, items []Item) {
		t.Helper()
		dir = t.TempDir()
		db, _, store := openTestEnv(t, dir)
		if err := seedDemoData(db, store, dir); err != nil {
			t.Fatalf("seedDemoData: %v", err)
		}

		mux := routesMux(db, store, newAuthState("", nil), dir, false)
		var resp struct {
			Items []Item `json:"items"`
		}
		getJSON(t, mux, "/api/items", &resp)
		sort.Slice(resp.Items, func(i, j int) bool { return resp.Items[i].SortOrder < resp.Items[j].SortOrder })
		return dir, resp.Items
	}

	dir1, items1 := seedInto(t)
	dir2, items2 := seedInto(t)

	if len(items1) != len(demoItemSpecs) || len(items2) != len(demoItemSpecs) {
		t.Fatalf("expected %d seeded items in each run, got %d and %d", len(demoItemSpecs), len(items1), len(items2))
	}

	for i := range items1 {
		it1, it2 := items1[i], items2[i]
		if it1.ImageFile == "" || it2.ImageFile == "" {
			t.Fatalf("item %d (%q): expected a non-empty image file, got %q and %q", i, it1.Name, it1.ImageFile, it2.ImageFile)
		}
		if !itemImageName.MatchString(it1.ImageFile) || !itemImageName.MatchString(it2.ImageFile) {
			t.Fatalf("item %d (%q): image file (%q, %q) does not match the itemImageName pattern", i, it1.Name, it1.ImageFile, it2.ImageFile)
		}
		if it1.ImageFile != it2.ImageFile {
			t.Fatalf("item %d (%q): image filename differs across seed runs (%q vs %q), want deterministic content-addressed naming", i, it1.Name, it1.ImageFile, it2.ImageFile)
		}

		b1, err := os.ReadFile(itemImagePath(dir1, it1.ImageFile))
		if err != nil {
			t.Fatalf("item %d (%q): read image from run 1: %v", i, it1.Name, err)
		}
		b2, err := os.ReadFile(itemImagePath(dir2, it2.ImageFile))
		if err != nil {
			t.Fatalf("item %d (%q): read image from run 2: %v", i, it2.Name, err)
		}
		if len(b1) == 0 {
			t.Fatalf("item %d (%q): seeded image is empty", i, it1.Name)
		}
		if !bytes.Equal(b1, b2) {
			t.Fatalf("item %d (%q): image bytes differ across seed runs, want byte-identical output", i, it1.Name)
		}
	}
}

// seedDemoData must never clobber or add to a database that already has clients, real or from a previous seed,
// and a no-op seed must write no config (a non-demo boot never reaches seedDemoConfig; main.go gates on DEMO_MODE=1).
func TestSeedDemoDataNoOpOnNonEmptyDB(t *testing.T) {
	dir := t.TempDir()
	db, _, store := openTestEnv(t, dir)

	now := "2020-01-01T00:00:00Z"
	if _, err := db.Exec(`
		INSERT INTO clients (id, name, email, phone, billing_address, shipping_address, notes, created_at, updated_at)
		VALUES (?, 'Existing Client', '', '', '', '', '', ?, ?)`,
		newID(), now, now,
	); err != nil {
		t.Fatalf("insert preexisting client: %v", err)
	}
	if err := seedDemoData(db, store, dir); err != nil {
		t.Fatalf("seedDemoData: %v", err)
	}

	t.Run("rows untouched", func(t *testing.T) {
		for table, want := range map[string]int{"clients": 1, "quotes": 0, "invoices": 0} {
			var n int
			if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
				t.Fatalf("count %s: %v", table, err)
			}
			if n != want {
				t.Fatalf("seedDemoData was not a no-op: expected %d %s, got %d", want, table, n)
			}
		}
	})
	t.Run("writes no config", func(t *testing.T) {
		if _, err := os.Stat(filepath.Join(dir, "config.yaml")); !os.IsNotExist(err) {
			t.Fatalf("expected no config.yaml to be written on a non-fresh (no-op) seed, stat err = %v", err)
		}
		if len(store.Get().Hardware) != 0 {
			t.Fatalf("expected no hardware catalog on a non-fresh (no-op) seed, got %+v", store.Get().Hardware)
		}
	})
}

// config.yaml seed (seedDemoConfig): hardware catalog, public materials, intro text.

// wantDemoHardware is the exact catalog seedDemoConfig's demoHardwareCatalog
// should produce once round-tripped through config.yaml, in order.
var wantDemoHardware = []Hardware{
	{Name: "6x2 neodymium magnet", UnitCost: 0.11, LostOnFailure: true},
	{Name: "Earring hook (pair)", UnitCost: 0.08, LostOnFailure: false},
	{Name: "M3 heat-set insert", UnitCost: 0.06, LostOnFailure: false},
}

// A fresh demo boot must write config.yaml with the three hardware entries, the full materials list with exactly "pla" and "petg" public,
// and a public intro text, AND the store handed to seedDemoData must already reflect it on return, since LoadConfig runs before seeding.
// Wanted materials come from defaultConfig(), so the test can't drift from defaults.yaml.
func TestSeedDemoDataWritesConfig(t *testing.T) {
	dir := t.TempDir()
	db, _, store := openTestEnv(t, dir)

	if len(store.Get().Hardware) != 0 {
		t.Fatalf("precondition: expected no hardware before seeding, got %+v", store.Get().Hardware)
	}

	defaults, err := defaultConfig()
	if err != nil {
		t.Fatalf("defaultConfig: %v", err)
	}
	wantMaterials := make([]Material, len(defaults.Materials))
	copy(wantMaterials, defaults.Materials)
	for i := range wantMaterials {
		if wantMaterials[i].ID == "pla" || wantMaterials[i].ID == "petg" {
			wantMaterials[i].Public = true
		}
	}

	if err := seedDemoData(db, store, dir); err != nil {
		t.Fatalf("seedDemoData: %v", err)
	}

	// The file itself.
	path := filepath.Join(dir, "config.yaml")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected config.yaml to exist after a fresh demo seed: %v", err)
	}
	onDisk := loadTestConfig(t, dir)

	checkSeededConfig := func(t *testing.T, got *Config) {
		t.Helper()
		if len(got.Hardware) != len(wantDemoHardware) {
			t.Fatalf("hardware = %+v, want %+v", got.Hardware, wantDemoHardware)
		}
		for i, want := range wantDemoHardware {
			if got.Hardware[i] != want {
				t.Fatalf("hardware[%d] = %+v, want %+v", i, got.Hardware[i], want)
			}
		}
		if len(got.Materials) != len(wantMaterials) {
			t.Fatalf("materials = %+v, want %+v", got.Materials, wantMaterials)
		}
		nPublic := 0
		for i, want := range wantMaterials {
			if got.Materials[i] != want {
				t.Fatalf("materials[%d] = %+v, want %+v", i, got.Materials[i], want)
			}
			if got.Materials[i].Public {
				nPublic++
			}
		}
		if nPublic != 2 {
			t.Fatalf("expected exactly 2 public materials (pla, petg), got %d in %+v", nPublic, got.Materials)
		}
		if got.PublicQuotes.IntroText != demoIntroText {
			t.Fatalf("publicQuotes.introText = %q, want %q", got.PublicQuotes.IntroText, demoIntroText)
		}
		if got.CustomCss != demoCustomCss {
			t.Fatalf("customCss = %q, want the comment-only demo placeholder", got.CustomCss)
		}
		// publicQuotes is a struct and struct sections merge by key: seedDemoConfig sets only introText/customCss,
		// so printer/preset must keep their defaults.yaml values, not be blanked by a full-struct marshal.
		if got.PublicQuotes.Printer != defaults.PublicQuotes.Printer {
			t.Fatalf("publicQuotes.printer = %q, want the untouched default %q", got.PublicQuotes.Printer, defaults.PublicQuotes.Printer)
		}
		if got.PublicQuotes.Preset != defaults.PublicQuotes.Preset {
			t.Fatalf("publicQuotes.preset = %q, want the untouched default %q", got.PublicQuotes.Preset, defaults.PublicQuotes.Preset)
		}
	}

	t.Run("on disk", func(t *testing.T) { checkSeededConfig(t, onDisk) })
	// The in-memory store seedDemoData was handed, without any reload by the
	// test itself: this is what proves the running demo sees everything on
	// first boot.
	t.Run("live store", func(t *testing.T) { checkSeededConfig(t, store.Get()) })
}

// An operator-supplied config.yaml present before first boot must be left byte-for-byte untouched.
func TestSeedDemoDataPreservesExistingConfigYaml(t *testing.T) {
	dir := t.TempDir()
	const existing = "# hand-edited by the operator before first boot\n" +
		"# no hardware/materials/publicQuotes keys here on purpose\n" +
		"energy:\n" +
		"  flatRate: 0.5\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(existing), 0o644); err != nil {
		t.Fatalf("seed existing config.yaml: %v", err)
	}

	db, _, store := openTestEnv(t, dir)

	if err := seedDemoData(db, store, dir); err != nil {
		t.Fatalf("seedDemoData: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatalf("read config.yaml after seed: %v", err)
	}
	if string(got) != existing {
		t.Fatalf("config.yaml was modified by seedDemoData:\ngot:\n%s\nwant (unchanged):\n%s", got, existing)
	}
	if len(store.Get().Hardware) != 0 {
		t.Fatalf("expected no hardware catalog injected into a pre-existing config.yaml, got %+v", store.Get().Hardware)
	}
	for _, m := range store.Get().Materials {
		if m.Public {
			t.Fatalf("expected no material to be made public against a pre-existing config.yaml, got %+v", store.Get().Materials)
		}
	}
	if got := store.Get().PublicQuotes.IntroText; got != "" {
		t.Fatalf("expected no introText injected into a pre-existing config.yaml, got %q", got)
	}
	if got := store.Get().Energy.FlatRate; got != 0.5 {
		t.Fatalf("expected the operator's own energy.flatRate (0.5) to survive the seed, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// Payment references
// ---------------------------------------------------------------------------

// The reference column must show both states in the demo (some payments with one, some without), like demoClientSpecs' mix of clients with and without email.
func TestSeedDemoDataPaymentsHaveMixedReferences(t *testing.T) {
	dir := t.TempDir()
	db, _, store := openTestEnv(t, dir)

	if err := seedDemoData(db, store, dir); err != nil {
		t.Fatalf("seedDemoData: %v", err)
	}

	rows, err := db.Query(`SELECT reference FROM payments`)
	if err != nil {
		t.Fatalf("query payments: %v", err)
	}
	defer rows.Close()

	var withRef, withoutRef int
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			t.Fatalf("scan reference: %v", err)
		}
		if ref == "" {
			withoutRef++
		} else {
			withRef++
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate payments: %v", err)
	}
	if withRef == 0 {
		t.Fatal("expected at least one seeded payment to carry a reference")
	}
	if withoutRef == 0 {
		t.Fatal("expected at least one seeded payment to carry no reference, to show that state too")
	}
}

// demoseed.go promises internally consistent data: no seeded quote step, invoice issue date, or payment date may fall before its client's created_at.
func TestSeedDemoDataRecordsDoNotPredateTheirClient(t *testing.T) {
	dir := t.TempDir()
	db, _, store := openTestEnv(t, dir)

	if err := seedDemoData(db, store, dir); err != nil {
		t.Fatalf("seedDemoData: %v", err)
	}

	clientCreatedAt := map[string]string{}
	rows, err := db.Query(`SELECT id, created_at FROM clients`)
	if err != nil {
		t.Fatalf("query clients: %v", err)
	}
	for rows.Next() {
		var id, createdAt string
		if err := rows.Scan(&id, &createdAt); err != nil {
			t.Fatalf("scan client: %v", err)
		}
		clientCreatedAt[id] = createdAt
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate clients: %v", err)
	}
	if len(clientCreatedAt) == 0 {
		t.Fatal("expected seeded clients, got none")
	}

	// Quote status_log entries: every step's "at" must be >= its client's
	// created_at. RFC3339 UTC timestamps compare correctly as strings.
	qrows, err := db.Query(`SELECT id, client_id, status_log FROM quotes WHERE client_id != ''`)
	if err != nil {
		t.Fatalf("query quotes: %v", err)
	}
	for qrows.Next() {
		var id, clientID, statusLogRaw string
		if err := qrows.Scan(&id, &clientID, &statusLogRaw); err != nil {
			t.Fatalf("scan quote: %v", err)
		}
		createdAt, ok := clientCreatedAt[clientID]
		if !ok {
			continue // walk-in/unknown client id, nothing to compare against
		}
		var log []struct {
			Status string `json:"status"`
			At     string `json:"at"`
		}
		if err := json.Unmarshal([]byte(statusLogRaw), &log); err != nil {
			t.Fatalf("quote %s: unmarshal status_log: %v", id, err)
		}
		for _, entry := range log {
			if entry.At < createdAt {
				t.Errorf("quote %s: status %q at %s predates its client (created %s)", id, entry.Status, entry.At, createdAt)
			}
		}
	}
	if err := qrows.Err(); err != nil {
		t.Fatalf("iterate quotes: %v", err)
	}

	// Invoice issue_date ("YYYY-MM-DD") vs client created_at (RFC3339): compare
	// by date prefix so a same-day issue is never flagged as predating a
	// client created earlier that same day.
	irows, err := db.Query(`SELECT id, client_id, issue_date FROM invoices WHERE client_id != '' AND issue_date != ''`)
	if err != nil {
		t.Fatalf("query invoices: %v", err)
	}
	invoiceClient := map[string]string{} // invoice id -> client id, for the payments join below
	for irows.Next() {
		var id, clientID, issueDate string
		if err := irows.Scan(&id, &clientID, &issueDate); err != nil {
			t.Fatalf("scan invoice: %v", err)
		}
		invoiceClient[id] = clientID
		createdAt, ok := clientCreatedAt[clientID]
		if !ok {
			continue
		}
		if issueDate < createdAt[:10] {
			t.Errorf("invoice %s: issue_date %s predates its client (created %s)", id, issueDate, createdAt)
		}
	}
	if err := irows.Err(); err != nil {
		t.Fatalf("iterate invoices: %v", err)
	}

	// Payment paid_date ("YYYY-MM-DD") vs the owning invoice's client's
	// created_at.
	prows, err := db.Query(`SELECT id, invoice_id, paid_date FROM payments`)
	if err != nil {
		t.Fatalf("query payments: %v", err)
	}
	for prows.Next() {
		var id, invoiceID, paidDate string
		if err := prows.Scan(&id, &invoiceID, &paidDate); err != nil {
			t.Fatalf("scan payment: %v", err)
		}
		clientID, ok := invoiceClient[invoiceID]
		if !ok {
			continue
		}
		createdAt, ok := clientCreatedAt[clientID]
		if !ok {
			continue
		}
		if paidDate < createdAt[:10] {
			t.Errorf("payment %s: paid_date %s predates its client (created %s)", id, paidDate, createdAt)
		}
	}
	if err := prows.Err(); err != nil {
		t.Fatalf("iterate payments: %v", err)
	}
}

// Invoice numbers must increase with issue date, as nextInvoiceNumber does in the real app; the Invoices tab orders by number_year, number_seq DESC.
func TestSeedDemoInvoiceNumbersMonotonicWithIssueDate(t *testing.T) {
	dir := t.TempDir()
	db, _, store := openTestEnv(t, dir)

	if err := seedDemoData(db, store, dir); err != nil {
		t.Fatalf("seedDemoData: %v", err)
	}

	rows, err := db.Query(`SELECT number, issue_date FROM invoices ORDER BY number_year, number_seq`)
	if err != nil {
		t.Fatalf("query invoices: %v", err)
	}
	defer rows.Close()

	var prevNumber, prevIssueDate string
	n := 0
	for rows.Next() {
		var number, issueDate string
		if err := rows.Scan(&number, &issueDate); err != nil {
			t.Fatalf("scan invoice: %v", err)
		}
		if n > 0 && issueDate < prevIssueDate {
			t.Errorf("invoice %s (issue_date %s) numbered after %s (issue_date %s), but issued earlier -- numbering is not monotonic with issue date",
				number, issueDate, prevNumber, prevIssueDate)
		}
		prevNumber, prevIssueDate = number, issueDate
		n++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate invoices: %v", err)
	}
	if n != 19 {
		t.Fatalf("expected 19 seeded invoices, got %d", n)
	}
}

// Sorting invoice plans by issueDaysAgo before numbering must not scramble which client, lines, and payments belong to which invoice.
func TestSeedDemoInvoiceAttributionMatchesIntendedPlan(t *testing.T) {
	dir := t.TempDir()
	db, _, store := openTestEnv(t, dir)

	if err := seedDemoData(db, store, dir); err != nil {
		t.Fatalf("seedDemoData: %v", err)
	}

	// The standalone Casey Nakamura invoice (issued 48d ago) must still
	// carry its own "Resin minis (10)" line and its own "other"/"Venmo"
	// payment, not another plan's.
	var invID string
	if err := db.QueryRow(`SELECT id FROM invoices WHERE client_name = ? AND issue_date = ?`,
		"Casey Nakamura", ymd(daysAgoAt(48, 11, 0))).Scan(&invID); err != nil {
		t.Fatalf("find Casey Nakamura's invoice: %v", err)
	}
	var lineDescription string
	if err := db.QueryRow(`SELECT description FROM invoice_lines WHERE invoice_id = ?`, invID).Scan(&lineDescription); err != nil {
		t.Fatalf("query invoice line: %v", err)
	}
	if lineDescription != "Resin minis (10)" {
		t.Errorf("Casey Nakamura invoice line = %q, want %q", lineDescription, "Resin minis (10)")
	}
	var method, note string
	if err := db.QueryRow(`SELECT method, note FROM payments WHERE invoice_id = ?`, invID).Scan(&method, &note); err != nil {
		t.Fatalf("query payment: %v", err)
	}
	if method != "other" || note != "Venmo" {
		t.Errorf("Casey Nakamura payment = method %q note %q, want other/Venmo", method, note)
	}

	// The quote-converted Riley Fontaine invoice (issued 16d ago) must still
	// carry its own PayPal payment.
	if err := db.QueryRow(`SELECT id FROM invoices WHERE client_name = ? AND issue_date = ?`,
		"Riley Fontaine", ymd(daysAgoAt(16, 11, 0))).Scan(&invID); err != nil {
		t.Fatalf("find Riley Fontaine's invoice: %v", err)
	}
	var reference string
	if err := db.QueryRow(`SELECT method, reference FROM payments WHERE invoice_id = ?`, invID).Scan(&method, &reference); err != nil {
		t.Fatalf("query payment: %v", err)
	}
	if method != "PayPal" || reference != "PayPal 8823" {
		t.Errorf("Riley Fontaine payment = method %q reference %q, want PayPal/\"PayPal 8823\"", method, reference)
	}
}

// TestDemoDBIsEmpty is a small unit check on the emptiness gate itself,
// independent of seedDemoData's own internal use of it.
func TestDemoDBIsEmpty(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)

	empty, err := demoDBIsEmpty(db)
	if err != nil {
		t.Fatalf("demoDBIsEmpty: %v", err)
	}
	if !empty {
		t.Fatal("expected a fresh database to be reported empty")
	}

	now := "2020-01-01T00:00:00Z"
	if _, err := db.Exec(`
		INSERT INTO clients (id, name, email, phone, billing_address, shipping_address, notes, created_at, updated_at)
		VALUES (?, 'Someone', '', '', '', '', '', ?, ?)`,
		newID(), now, now,
	); err != nil {
		t.Fatalf("insert client: %v", err)
	}

	empty, err = demoDBIsEmpty(db)
	if err != nil {
		t.Fatalf("demoDBIsEmpty: %v", err)
	}
	if empty {
		t.Fatal("expected a database with a client to be reported non-empty")
	}
}

// A row in ANY of the user-populated tables makes the database non-empty, not just clients: a database with
// quotes but no clients is real data, and DEMO_MODE must not seed over it.
func TestDemoDBIsEmptyCountsEveryTable(t *testing.T) {
	const now = "2020-01-01T00:00:00Z"
	cases := []struct{ table, insert string }{
		{"clients", `INSERT INTO clients (id, name, created_at, updated_at) VALUES ('x', 'Someone', '` + now + `', '` + now + `')`},
		{"quotes", `INSERT INTO quotes (id, saved_at, inputs, quantity, per_unit_total_cents, batch_total_cents, valid) VALUES ('x', '` + now + `', '{}', 1, 0, 0, 0)`},
		{"invoices", `INSERT INTO invoices (id, number, number_year, number_seq, client_name, created_at, updated_at) VALUES ('x', 'INV-1', 2020, 1, 'Someone', '` + now + `', '` + now + `')`},
		{"items", `INSERT INTO items (id, name, price_cents, created_at, updated_at) VALUES ('x', 'Thing', 100, '` + now + `', '` + now + `')`},
		{"quote_requests", `INSERT INTO quote_requests (id, name, email, material, quantity, created_at) VALUES ('x', 'Someone', 'a@b.c', 'PLA', 1, '` + now + `')`},
		{"users", `INSERT INTO users (id, username, password_hash, created_at, updated_at) VALUES ('x', 'bob', 'h', '` + now + `', '` + now + `')`},
	}
	for _, tc := range cases {
		t.Run(tc.table, func(t *testing.T) {
			db := openTestDB(t, t.TempDir())
			if _, err := db.Exec(tc.insert); err != nil {
				t.Fatalf("insert into %s: %v", tc.table, err)
			}
			var clients int
			if err := db.QueryRow(`SELECT COUNT(*) FROM clients`).Scan(&clients); err != nil {
				t.Fatal(err)
			}
			if tc.table != "clients" && clients != 0 {
				t.Fatalf("setup: clients has %d rows, want 0", clients)
			}
			empty, err := demoDBIsEmpty(db)
			if err != nil {
				t.Fatalf("demoDBIsEmpty: %v", err)
			}
			if empty {
				t.Fatalf("a database with a row only in %s must not be reported empty", tc.table)
			}
		})
	}
}

// A pre-commit seed failure must fail loudly and roll back everything (no rows, no demo_seeded marker) so a later boot can retry.
// An operator config without "etsy-us" must name it rather than seed quotes against a zero-value FeeSchedule. A plain file as
// CONFIG_DIR makes seedDemoItems' MkdirAll fail with ENOTDIR on any user (permission bits don't bind root in the docker gate).
func TestSeedDemoDataFailsLoudlyAndRollsBack(t *testing.T) {
	cases := []struct {
		name      string
		setup     func(t *testing.T, dir string, cfg *Config) (configDir string)
		wantErr   string
		retryable bool
	}{
		{"missing etsy-us fee schedule", func(t *testing.T, dir string, cfg *Config) string {
			if _, ok := cfg.FeeSchedules["etsy-us"]; !ok {
				t.Fatal("test precondition: defaults.yaml must define etsy-us")
			}
			delete(cfg.FeeSchedules, "etsy-us")
			return dir
		}, "etsy-us", false},
		{"CONFIG_DIR is a file", func(t *testing.T, dir string, cfg *Config) string {
			bad := filepath.Join(dir, "config-dir-is-a-file")
			if err := os.WriteFile(bad, []byte("x"), 0o644); err != nil {
				t.Fatalf("pre-create CONFIG_DIR as a file: %v", err)
			}
			return bad
		}, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			db := openTestDB(t, dir)
			cfg := loadTestConfig(t, dir)
			configDir := tc.setup(t, dir, cfg)
			store := NewConfigStore(cfg)

			err := seedDemoData(db, store, configDir)
			if err == nil {
				t.Fatal("seedDemoData: want an error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %v, want it to contain %q", err, tc.wantErr)
			}
			empty, err := demoDBIsEmpty(db)
			if err != nil {
				t.Fatalf("demoDBIsEmpty: %v", err)
			}
			if !empty {
				t.Fatal("a failed seed must roll back so demoDBIsEmpty stays true and a later boot can retry")
			}
			if seeded, err := demoSeedMarkerPresent(db); err != nil {
				t.Fatalf("demoSeedMarkerPresent: %v", err)
			} else if seeded {
				t.Fatal("demo_seeded marker must not be committed when the seed fails before tx.Commit")
			}

			if !tc.retryable {
				return
			}
			// The failed attempt must not leave anything behind that makes a clean retry fail too.
			if err := seedDemoData(db, store, dir); err != nil {
				t.Fatalf("retry seedDemoData with a usable CONFIG_DIR: %v", err)
			}
			if seeded, err := demoSeedMarkerPresent(db); err != nil {
				t.Fatalf("demoSeedMarkerPresent after retry: %v", err)
			} else if !seeded {
				t.Fatal("expected demo_seeded marker to be present after a successful retry")
			}
		})
	}
}

// seedDemoConfig must surface a wrapped error when it cannot write config.yaml, so the failure is retryable (it runs before tx.Commit).
// A plain file as configDir makes os.Stat fail with ENOTDIR on any user, unlike os.Chmod which root bypasses.
func TestSeedDemoConfigFailsLoudlyWhenWriteFails(t *testing.T) {
	dir := t.TempDir()
	notADir := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(notADir, []byte("x"), 0o644); err != nil {
		t.Fatalf("pre-create configDir as a file: %v", err)
	}

	if err := seedDemoConfig(notADir); err == nil {
		t.Fatal("seedDemoConfig: want an error when configDir is not a directory, got nil")
	}
}

// withDemoNow overrides demoNow for one test and restores it afterwards.
func withDemoNow(t *testing.T, now time.Time) {
	t.Helper()
	prev := demoNow
	demoNow = now
	t.Cleanup(func() { demoNow = prev })
}

// The Dashboard "This month" revenue and the revenue-by-month chart read payment dates, so the seed must put
// at least two payments on paid invoices in the current calendar month on any day it runs (including the 1st)
// and spread paid invoices across at least six calendar months.
func TestSeedDemoDataPaymentsCoverCurrentMonthAndSixMonths(t *testing.T) {
	cases := []struct {
		name string
		now  time.Time
	}{
		{"first of month", time.Date(2026, time.March, 1, 9, 0, 0, 0, time.UTC)},
		{"first of year", time.Date(2026, time.January, 1, 0, 5, 0, 0, time.UTC)},
		{"second of month", time.Date(2026, time.August, 2, 23, 30, 0, 0, time.UTC)},
		{"mid month", time.Date(2026, time.November, 15, 12, 0, 0, 0, time.UTC)},
		{"31st", time.Date(2026, time.October, 31, 18, 0, 0, 0, time.UTC)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withDemoNow(t, tc.now)
			dir := t.TempDir()
			db, _, store := openTestEnv(t, dir)
			if err := seedDemoData(db, store, dir); err != nil {
				t.Fatalf("seedDemoData: %v", err)
			}

			rows, err := db.Query(`SELECT p.paid_date, p.invoice_id, i.issue_date FROM payments p JOIN invoices i ON i.id = p.invoice_id WHERE i.status = 'paid'`)
			if err != nil {
				t.Fatalf("query payments: %v", err)
			}
			defer rows.Close()
			thisMonth := tc.now.Format("2006-01")
			inMonth := 0
			months := map[string]bool{}
			for rows.Next() {
				var paid, invoiceID, issued string
				if err := rows.Scan(&paid, &invoiceID, &issued); err != nil {
					t.Fatalf("scan: %v", err)
				}
				if paid > tc.now.Format("2006-01-02") {
					t.Errorf("payment on %s is after the seed date %s", paid, tc.now.Format("2006-01-02"))
				}
				if paid < issued {
					t.Errorf("invoice %s: payment %s precedes issue date %s", invoiceID, paid, issued)
				}
				if paid[:7] == thisMonth {
					inMonth++
				}
				months[paid[:7]] = true
			}
			if err := rows.Err(); err != nil {
				t.Fatalf("iterate: %v", err)
			}
			if inMonth < 2 {
				t.Errorf("payments in %s = %d, want at least 2", thisMonth, inMonth)
			}
			if len(months) < 6 {
				t.Errorf("paid invoices span %d calendar months (%v), want at least 6", len(months), months)
			}
		})
	}
}
