package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/color"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// publicItemsResponse mirrors handlePublicItems' response envelope
// (server/public.go).
type publicItemsResponse struct {
	Items []publicItem `json:"items"`
}

// Seeds one public in_stock item (option plus uploaded image), one public ready_to_print item, and one non-public item through the admin routes, then asserts GET /api/public/items on the PUBLIC mux returns exactly the two public items in sort_order,
// with imageUrl/stock populated and no internal fields (imageFile/createdAt/updatedAt/sortOrder), and that the public mux serves the image via /item-images/{file}.
func TestPublicItems(t *testing.T) {
	dir := t.TempDir()
	adminMux, publicMux, _, _ := newRequestsTestSetup(t, dir)

	// Non-public item first (sort_order 0): must never appear, and its
	// lower sort_order would otherwise put it first if the WHERE clause
	// were missing.
	rec := postItem(t, adminMux, `{"name":"Hidden","priceCents":100,"sortOrder":0}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST hidden item want 201, got %d %s", rec.Code, rec.Body.String())
	}

	// Public in_stock item with an option, an image, and allowBackorder set.
	rec = postItem(t, adminMux, `{"name":"Benchy","priceCents":1500,"public":true,`+
		`"stockMode":"in_stock","stockQty":3,"allowBackorder":true,"optionLabel":"Color",`+
		`"options":[{"name":"Red","deltaCents":0}],"sortOrder":1}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST item A want 201, got %d %s", rec.Code, rec.Body.String())
	}
	var itemA Item
	if err := json.Unmarshal(rec.Body.Bytes(), &itemA); err != nil {
		t.Fatalf("unmarshal item A: %v", err)
	}
	rec = uploadItemImage(t, adminMux, itemA.ID, testPNG(t, color.RGBA{10, 20, 30, 255}))
	if rec.Code != http.StatusOK {
		t.Fatalf("upload image want 200, got %d %s", rec.Code, rec.Body.String())
	}
	var uploadedA Item
	if err := json.Unmarshal(rec.Body.Bytes(), &uploadedA); err != nil {
		t.Fatalf("unmarshal uploaded A: %v", err)
	}

	// Public ready_to_print item, no options, no image.
	rec = postItem(t, adminMux, `{"name":"Widget","priceCents":500,"public":true,"sortOrder":2}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST item B want 201, got %d %s", rec.Code, rec.Body.String())
	}
	var itemB Item
	if err := json.Unmarshal(rec.Body.Bytes(), &itemB); err != nil {
		t.Fatalf("unmarshal item B: %v", err)
	}

	rec = serveReq(publicMux, httptest.NewRequest("GET", "/api/public/items", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/public/items want 200, got %d %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	for _, key := range []string{`"imageFile"`, `"createdAt"`, `"updatedAt"`, `"sortOrder"`, `"public"`} {
		if strings.Contains(body, key) {
			t.Errorf("response body leaks internal field %s: %s", key, body)
		}
	}
	if !strings.Contains(body, `"options":[]`) {
		t.Errorf("expected literal \"options\":[] for the empty-options item, body = %s", body)
	}
	// publicItemStock.Qty has no `omitempty`: a ready_to_print item must emit "qty":0 so the frontend's qty-0 "Out of stock" state is never confused with a mode where quantity doesn't apply.
	// json.Unmarshal can't tell a present 0 from an absent key, so this is a raw-body substring check.
	if !strings.Contains(body, `"mode":"ready_to_print","qty":0`) {
		t.Errorf("ready_to_print stock must still emit qty explicitly, body = %s", body)
	}
	// allowBackorder likewise has no `omitempty` on publicItemStock: the
	// ready_to_print item's false value must still appear explicitly.
	if !strings.Contains(body, `"allowBackorder":true`) {
		t.Errorf("expected allowBackorder:true for the backorder-enabled item, body = %s", body)
	}
	if !strings.Contains(body, `"allowBackorder":false`) {
		t.Errorf("expected allowBackorder:false for the ready_to_print item, body = %s", body)
	}

	var got publicItemsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal public items: %v", err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("items count = %d, want 2: %+v", len(got.Items), got.Items)
	}

	first, second := got.Items[0], got.Items[1]
	if first.ID != itemA.ID {
		t.Errorf("first item id = %s, want %s (sort_order order)", first.ID, itemA.ID)
	}
	if first.Name != "Benchy" {
		t.Errorf("first name = %q, want Benchy", first.Name)
	}
	if want := "/item-images/" + uploadedA.ImageFile; first.ImageURL != want {
		t.Errorf("first imageUrl = %q, want %q", first.ImageURL, want)
	}
	if first.Stock.Mode != stockModeInStock || first.Stock.Qty != 3 || !first.Stock.AllowBackorder {
		t.Errorf("first stock = %+v, want {in_stock 3 true}", first.Stock)
	}
	if len(first.Options) != 1 || first.Options[0].Name != "Red" {
		t.Errorf("first options = %+v, want [{Red 0}]", first.Options)
	}

	if second.ID != itemB.ID {
		t.Errorf("second item id = %s, want %s", second.ID, itemB.ID)
	}
	if second.ImageURL != "" {
		t.Errorf("second imageUrl = %q, want empty", second.ImageURL)
	}
	if second.Stock.Mode != stockModeReadyToPrint {
		t.Errorf("second stock mode = %q, want ready_to_print", second.Stock.Mode)
	}
	if second.Options == nil || len(second.Options) != 0 {
		t.Errorf("second options = %+v, want empty non-nil slice", second.Options)
	}

	// The image route must be reachable through the PUBLIC mux too, not
	// just the admin one (itemImageHandler is also registered on
	// buildPublicMux).
	rec = serveReq(publicMux, httptest.NewRequest("GET", "/item-images/"+uploadedA.ImageFile, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /item-images/%s on public mux want 200, got %d", uploadedA.ImageFile, rec.Code)
	}
}

// handlePublicItems serves a cached response within publicItemsCacheTTL: a second GET returns the same bytes even after an item was added, and a GET after the TTL reflects it.
// A fake clock (publicItemsNow) avoids sleeping, and is restored afterward since tests run sequentially.
func TestPublicItemsCache(t *testing.T) {
	origNow := publicItemsNow
	fakeNow := time.Unix(1_800_000_000, 0)
	publicItemsNow = func() time.Time { return fakeNow }
	t.Cleanup(func() { publicItemsNow = origNow })

	dir := t.TempDir()
	adminMux, publicMux, _, _ := newRequestsTestSetup(t, dir)

	rec := postItem(t, adminMux, `{"name":"First","priceCents":100,"public":true,"sortOrder":0}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST first item want 201, got %d %s", rec.Code, rec.Body.String())
	}

	get := func() *httptest.ResponseRecorder {
		t.Helper()
		rec := serveReq(publicMux, httptest.NewRequest("GET", "/api/public/items", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/public/items want 200, got %d %s", rec.Code, rec.Body.String())
		}
		return rec
	}

	first := get().Body.String()

	rec = postItem(t, adminMux, `{"name":"Second","priceCents":200,"public":true,"sortOrder":1}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST second item want 201, got %d %s", rec.Code, rec.Body.String())
	}

	second := get().Body.String()
	if second != first {
		t.Fatalf("GET within TTL returned a different body after adding an item:\nfirst  = %s\nsecond = %s", first, second)
	}
	if strings.Contains(second, "Second") {
		t.Errorf("cached response leaked the item added after it was cached: %s", second)
	}

	fakeNow = fakeNow.Add(2 * publicItemsCacheTTL)

	third := get().Body.String()
	if third == first {
		t.Errorf("GET after TTL expiry returned the stale cached body: %s", third)
	}
	if !strings.Contains(third, "Second") {
		t.Errorf("GET after TTL expiry did not pick up the new item: %s", third)
	}
}

// Each postPublicItemOrder call gets a distinct source IP: the handler shares its rate limiter with handlePublicQuoteRequest (5 per hour per IP, keyed on r.RemoteAddr with trustProxy false), and more than five cases against one mux would 429.
var publicItemOrderTestIPCounter atomic.Int32

// postPublicItemOrder POSTs a JSON body (marshaled from body, typically a
// map[string]any so individual cases can add/omit/override fields) to
// /api/public/item-order on mux.
func postPublicItemOrder(t *testing.T, mux *http.ServeMux, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal item order body: %v", err)
	}
	req := httptest.NewRequest("POST", "/api/public/item-order", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	n := publicItemOrderTestIPCounter.Add(1)
	req.RemoteAddr = fmt.Sprintf("203.0.113.%d:12345", n%250+1)
	rec := serveReq(mux, req)
	return rec
}

// validItemOrderBody returns a fresh, valid /api/public/item-order body for
// itemID/optionName/quantity, ready for a test case to override individual
// fields on top of.
func validItemOrderBody(itemID, optionName string, quantity int) map[string]any {
	return map[string]any{
		"itemId":     itemID,
		"optionName": optionName,
		"quantity":   quantity,
		"name":       "Buyer",
		"email":      "buyer@example.com",
		"note":       "gift wrap please",
	}
}

// Happy path: a valid order against a public in_stock item with an option is priced server-side ((priceCents+deltaCents)*quantity), lands in quote_requests with kind 'item' and the snapshot columns, and shows in admin GET /api/requests.
// A client-supplied priceCents is rejected (DisallowUnknownFields): the server is the only price authority.
func TestPublicItemOrder(t *testing.T) {
	dir := t.TempDir()
	adminMux, publicMux, db, _ := newRequestsTestSetup(t, dir)

	rec := postItem(t, adminMux, `{"name":"Benchy","priceCents":1500,"public":true,`+
		`"stockMode":"in_stock","stockQty":3,"optionLabel":"Color",`+
		`"options":[{"name":"Glow","deltaCents":250}]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST item want 201, got %d %s", rec.Code, rec.Body.String())
	}
	var item Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("unmarshal item: %v", err)
	}

	// A client-supplied priceCents must be rejected (DisallowUnknownFields), not ignored. The 400 body must be the fixed message, never err.Error(): a body-read timeout's error can include the listener's bind address and port.
	priced := validItemOrderBody(item.ID, "Glow", 2)
	priced["priceCents"] = 999
	rec = postPublicItemOrder(t, publicMux, priced)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("order with client priceCents want 400, got %d %s", rec.Code, rec.Body.String())
	}
	var badBody apiErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &badBody); err != nil {
		t.Fatalf("unmarshal 400 body: %v", err)
	}
	if len(badBody.Errors) != 1 || badBody.Errors[0] != "invalid request body" {
		t.Errorf("400 errors = %v, want exactly [\"invalid request body\"] (no echoed decode error)", badBody.Errors)
	}

	rec = postPublicItemOrder(t, publicMux, validItemOrderBody(item.ID, "Glow", 2))
	if rec.Code != http.StatusCreated {
		t.Fatalf("valid order want 201, got %d %s", rec.Code, rec.Body.String())
	}
	var accepted struct {
		ID         string `json:"id"`
		PriceCents int    `json:"priceCents"`
		Message    string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &accepted); err != nil {
		t.Fatalf("unmarshal accepted response: %v", err)
	}
	const wantTotal = (1500 + 250) * 2
	if accepted.PriceCents != wantTotal {
		t.Errorf("priceCents = %d, want %d", accepted.PriceCents, wantTotal)
	}
	if accepted.Message != "Order request received, subject to review" {
		t.Errorf("message = %q, want the order-accepted message", accepted.Message)
	}
	if accepted.ID == "" {
		t.Error("accepted response id is empty")
	}

	var status, kind, itemName, material string
	var quantity, priceCents int
	err := db.QueryRow(`SELECT status, kind, quantity, price_cents, item_name, material
		FROM quote_requests WHERE id = ?`, accepted.ID).
		Scan(&status, &kind, &quantity, &priceCents, &itemName, &material)
	if err != nil {
		t.Fatalf("query stored order: %v", err)
	}
	if status != "new" {
		t.Errorf("status = %q, want new", status)
	}
	if kind != "item" {
		t.Errorf("kind = %q, want item", kind)
	}
	if quantity != 2 {
		t.Errorf("quantity = %d, want 2", quantity)
	}
	if priceCents != wantTotal {
		t.Errorf("price_cents = %d, want %d", priceCents, wantTotal)
	}
	if itemName != "Benchy" {
		t.Errorf("item_name = %q, want Benchy (name snapshot)", itemName)
	}
	if material != "" {
		t.Errorf("material = %q, want empty", material)
	}

	rec = serveReq(adminMux, httptest.NewRequest("GET", "/api/requests", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/requests want 200, got %d %s", rec.Code, rec.Body.String())
	}
	var listResp struct {
		Requests []QuoteRequest `json:"requests"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal requests list: %v", err)
	}
	var found *QuoteRequest
	for i := range listResp.Requests {
		if listResp.Requests[i].ID == accepted.ID {
			found = &listResp.Requests[i]
		}
	}
	if found == nil {
		t.Fatalf("order %s not found in admin requests list", accepted.ID)
	}
	if found.Kind != "item" {
		t.Errorf("admin list kind = %q, want item", found.Kind)
	}
	if found.ItemID != item.ID {
		t.Errorf("admin list itemId = %q, want %q", found.ItemID, item.ID)
	}
	if found.ItemName != "Benchy" {
		t.Errorf("admin list itemName = %q, want Benchy", found.ItemName)
	}
	if found.OptionName != "Glow" {
		t.Errorf("admin list optionName = %q, want Glow", found.OptionName)
	}
}

// Every validation rule produces a 422 (or a 201 for the two ready_to_print/stock-boundary happy cases).
func TestPublicItemOrderValidation(t *testing.T) {
	dir := t.TempDir()
	adminMux, publicMux, _, _ := newRequestsTestSetup(t, dir)

	mustPostItem := func(body string) Item {
		t.Helper()
		rec := postItem(t, adminMux, body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("POST item want 201, got %d %s", rec.Code, rec.Body.String())
		}
		var it Item
		if err := json.Unmarshal(rec.Body.Bytes(), &it); err != nil {
			t.Fatalf("unmarshal item: %v", err)
		}
		return it
	}

	withOptions := mustPostItem(`{"name":"WithOptions","priceCents":1000,"public":true,` +
		`"stockMode":"in_stock","stockQty":3,"optionLabel":"Color",` +
		`"options":[{"name":"Red","deltaCents":0}]}`)
	noOptions := mustPostItem(`{"name":"NoOptions","priceCents":500,"public":true}`)
	outOfStock := mustPostItem(`{"name":"OutOfStock","priceCents":500,"public":true,` +
		`"stockMode":"in_stock","stockQty":0}`)
	hidden := mustPostItem(`{"name":"Hidden","priceCents":500,"public":false}`)
	// allowBackorder: same stockQty:3 cap as withOptions, but the flag lifts
	// it up to the global maxItemOrderQty instead.
	backorder := mustPostItem(`{"name":"Backorder","priceCents":500,"public":true,` +
		`"stockMode":"in_stock","stockQty":3,"allowBackorder":true}`)

	cases := []struct {
		name       string
		body       map[string]any
		wantStatus int
		wantErrSub string // empty = don't check message content, just status
	}{
		{
			name:       "unknown itemId",
			body:       validItemOrderBody("nonexistent-id", "", 1),
			wantStatus: http.StatusUnprocessableEntity,
			wantErrSub: "item: no longer available",
		},
		{
			name:       "non-public item",
			body:       validItemOrderBody(hidden.ID, "", 1),
			wantStatus: http.StatusUnprocessableEntity,
			wantErrSub: "item: no longer available",
		},
		{
			name:       "optionName missing when item has options",
			body:       validItemOrderBody(withOptions.ID, "", 1),
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name:       "optionName present when item has none",
			body:       validItemOrderBody(noOptions.ID, "AnyOption", 1),
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name:       "optionName not in the list",
			body:       validItemOrderBody(withOptions.ID, "Blue", 1),
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name:       "quantity 0",
			body:       validItemOrderBody(noOptions.ID, "", 0),
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name:       "quantity over max",
			body:       validItemOrderBody(noOptions.ID, "", maxItemOrderQty+1),
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name:       "quantity exceeds stock",
			body:       validItemOrderBody(withOptions.ID, "Red", 4),
			wantStatus: http.StatusUnprocessableEntity,
			wantErrSub: "quantity: only 3 in stock",
		},
		{
			name:       "stock qty 0",
			body:       validItemOrderBody(outOfStock.ID, "", 1),
			wantStatus: http.StatusUnprocessableEntity,
			wantErrSub: "item: no longer available",
		},
		{
			name:       "ready_to_print ignores stock",
			body:       validItemOrderBody(noOptions.ID, "", 50),
			wantStatus: http.StatusCreated,
		},
		{
			name:       "allowBackorder accepts quantity beyond stockQty",
			body:       validItemOrderBody(backorder.ID, "", 4),
			wantStatus: http.StatusCreated,
		},
		{
			name:       "allowBackorder accepts up to the global max",
			body:       validItemOrderBody(backorder.ID, "", maxItemOrderQty),
			wantStatus: http.StatusCreated,
		},
		{
			name:       "allowBackorder still rejects beyond the global max",
			body:       validItemOrderBody(backorder.ID, "", maxItemOrderQty+1),
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name: "empty name",
			body: func() map[string]any {
				b := validItemOrderBody(noOptions.ID, "", 1)
				b["name"] = ""
				return b
			}(),
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name: "empty email",
			body: func() map[string]any {
				b := validItemOrderBody(noOptions.ID, "", 1)
				b["email"] = ""
				return b
			}(),
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name: "email without @",
			body: func() map[string]any {
				b := validItemOrderBody(noOptions.ID, "", 1)
				b["email"] = "not-an-email"
				return b
			}(),
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name: "note too long",
			body: func() map[string]any {
				b := validItemOrderBody(noOptions.ID, "", 1)
				b["note"] = strings.Repeat("x", 2001)
				return b
			}(),
			wantStatus: http.StatusUnprocessableEntity,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postPublicItemOrder(t, publicMux, tc.body)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantErrSub != "" && !strings.Contains(rec.Body.String(), tc.wantErrSub) {
				t.Errorf("body = %s, want it to contain %q", rec.Body.String(), tc.wantErrSub)
			}
		})
	}
}

// Item orders land in the same quote_requests table, so they must count against and be rejected by the same publicMaxOpenRequests cap as handlePublicQuoteRequest.
// Seeds the rows directly (the per-IP rate limit makes real submissions impractically slow) because the cap is a const.
func TestPublicItemOrderBackpressure(t *testing.T) {
	dir := t.TempDir()
	adminMux, publicMux, db, _ := newRequestsTestSetup(t, dir)

	rec := postItem(t, adminMux, `{"name":"Widget","priceCents":500,"public":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST item want 201, got %d %s", rec.Code, rec.Body.String())
	}
	var item Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("unmarshal item: %v", err)
	}

	now := "2026-01-01T00:00:00Z"
	for i := range publicMaxOpenRequests {
		if _, err := db.Exec(
			`INSERT INTO quote_requests (id, name, email, material, quantity, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			fmt.Sprintf("filler-%d", i), "Filler", "filler@example.com", "PLA", 1, now,
		); err != nil {
			t.Fatalf("seed filler request %d: %v", i, err)
		}
	}

	rec = postPublicItemOrder(t, publicMux, validItemOrderBody(item.ID, "", 1))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("order at open-requests cap want 503, got %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "too many pending requests") {
		t.Errorf("body = %s, want it to contain the same message handlePublicQuoteRequest uses", rec.Body.String())
	}
}

// The quote_requests INSERT failure branch in handlePublicItemOrder must respond through writeAPIError like every other error path, not plain-text http.Error.
// A BEFORE INSERT trigger fails only the INSERT (not the earlier countOpenRequests SELECT), reaching exactly that branch.
func TestPublicItemOrderInsertFailureUsesWriteAPIError(t *testing.T) {
	dir := t.TempDir()
	adminMux, publicMux, db, _ := newRequestsTestSetup(t, dir)

	rec := postItem(t, adminMux, `{"name":"Widget","priceCents":500,"public":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST item want 201, got %d %s", rec.Code, rec.Body.String())
	}
	var item Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("unmarshal item: %v", err)
	}

	if _, err := db.Exec(`CREATE TRIGGER block_quote_requests_insert
		BEFORE INSERT ON quote_requests
		BEGIN SELECT RAISE(ABORT, 'insert blocked for test'); END`); err != nil {
		t.Fatalf("create blocking trigger: %v", err)
	}

	rec = postPublicItemOrder(t, publicMux, validItemOrderBody(item.ID, "", 1))
	assertWriteAPIErrorShape(t, rec, http.StatusInternalServerError)
}

// assertWriteAPIErrorShape asserts writeAPIError's wire shape: application/json, wantStatus, and a body of {"errors": [...]} with at least one message, which the public API client relies on for res.json().
func assertWriteAPIErrorShape(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, wantStatus, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json (want writeAPIError's shape, not http.Error's text/plain); body = %s", ct, rec.Body.String())
	}
	var body apiErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body did not decode as {\"errors\":[...]}: %v; body = %s", err, rec.Body.String())
	}
	if len(body.Errors) == 0 {
		t.Errorf("errors slice is empty, want at least one message; body = %s", rec.Body.String())
	}
}
