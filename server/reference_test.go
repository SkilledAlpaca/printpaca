package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestDerivedReferencePrefix(t *testing.T) {
	for name, want := range map[string]string{
		"Hot Layer Prints":       "HLP",
		"hot layer":              "HL",
		"Printpaca":              "PRI",
		"AB":                     "AB",
		"Hot Layer 3D Prints Co": "HL3P", // capped at four initials
		"  Spaced   Out  ":       "SO",
		"Ben's 3D-Print Shop":    "B3PS",
		"":                       "REQ",
		"!!!":                    "REQ",
		"日本語":                    "REQ", // no ASCII letters or digits
		"Zürich Prints":          "ZRP", // splits at the non-ASCII rune; each fragment is a word
	} {
		if got := derivedReferencePrefix(name); got != want {
			t.Errorf("derivedReferencePrefix(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestReferencePrefixPrefersConfiguredValue(t *testing.T) {
	cfg := &Config{}
	cfg.Quote.BusinessName = "Hot Layer Prints"
	if got := referencePrefix(cfg); got != "HLP" {
		t.Errorf("derived prefix = %q, want HLP", got)
	}
	cfg.Quote.ReferencePrefix = "shop"
	if got := referencePrefix(cfg); got != "SHOP" {
		t.Errorf("configured prefix = %q, want SHOP (uppercased)", got)
	}
}

func TestConfigRejectsBadReferencePrefix(t *testing.T) {
	dir := t.TempDir()
	cfg := loadTestConfig(t, dir)
	cfg.Quote.ReferencePrefix = "bad prefix!"
	problems := validate(cfg)
	found := false
	for _, p := range problems {
		if strings.HasPrefix(p, "quote.referencePrefix:") {
			found = true
		}
	}
	if !found {
		t.Errorf("validate() = %v, want a quote.referencePrefix problem", problems)
	}
	cfg.Quote.ReferencePrefix = ""
	for _, p := range validate(cfg) {
		if strings.HasPrefix(p, "quote.referencePrefix:") {
			t.Errorf("blank prefix rejected: %s", p)
		}
	}
}

// The counter starts at 1001, increments per issue, and a rolled-back transaction leaves no gap.
func TestNextRequestReferenceSequenceAndRollback(t *testing.T) {
	db := openTestDB(t, t.TempDir())

	issue := func(commit bool) string {
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		ref, err := nextRequestReference(tx, "HL")
		if err != nil {
			t.Fatal(err)
		}
		if commit {
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
		} else {
			tx.Rollback()
		}
		return ref
	}

	if got := peekRequestReference(db, "HL"); got != "HL-1001" {
		t.Errorf("peek before any issue = %q, want HL-1001", got)
	}
	if got := issue(true); got != "HL-1001" {
		t.Errorf("first = %q, want HL-1001", got)
	}
	if got := issue(false); got != "HL-1002" {
		t.Errorf("second (rolled back) = %q, want HL-1002", got)
	}
	if got := issue(true); got != "HL-1002" {
		t.Errorf("third after rollback = %q, want HL-1002 (no gap)", got)
	}
	if got := peekRequestReference(db, "HL"); got != "HL-1003" {
		t.Errorf("peek = %q, want HL-1003 and the counter must not move", got)
	}
	if got := peekRequestReference(db, "HL"); got != "HL-1003" {
		t.Errorf("second peek = %q, want HL-1003", got)
	}
}

// Migration 3 adds the two columns with safe defaults and keeps existing rows.
func TestMigrationAddsRequestReferenceColumns(t *testing.T) {
	dir := t.TempDir()
	raw, err := openRawSQLite(filepath.Join(dir, "printpaca.db"))
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	for i := 0; i < 2; i++ {
		if err := runMigration(raw, i); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := raw.Exec(`INSERT INTO quote_requests (id, name, email, material, quantity, created_at, time_seconds, price_cents)
		VALUES ('old', 'A', 'a@example.com', 'PLA', 1, 'x', 3600, 250)`); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	db := openTestDB(t, dir)
	var needs int
	var ref string
	var price int
	if err := db.QueryRow(`SELECT needs_print_time, reference, price_cents FROM quote_requests WHERE id = 'old'`).Scan(&needs, &ref, &price); err != nil {
		t.Fatal(err)
	}
	if needs != 0 || ref != "" || price != 250 {
		t.Errorf("migrated row = needs %d, ref %q, price %d; want 0, empty, 250", needs, ref, price)
	}
	if _, err := db.Exec(`UPDATE quote_requests SET needs_print_time = 2 WHERE id = 'old'`); err == nil {
		t.Error("needs_print_time accepted 2, want the CHECK constraint to reject it")
	}
}

// Item orders and estimates draw from one sequence; the response and the stored row carry the same reference.
func TestPublicSubmissionsGetSequentialReferences(t *testing.T) {
	dir := t.TempDir()
	adminMux, publicMux, db, cfg := newRequestsTestSetup(t, dir)
	_ = cfg

	rec := postItem(t, adminMux, `{"name":"Clip","priceCents":500,"public":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST item: %d %s", rec.Code, rec.Body.String())
	}
	var item Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}

	decodeRef := func(rec *httptest.ResponseRecorder) (id, ref string) {
		t.Helper()
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var b struct{ ID, Reference string }
		if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
			t.Fatal(err)
		}
		return b.ID, b.Reference
	}

	id1, ref1 := decodeRef(postPublicQuoteRequest(t, publicMux, publicQuoteRequestFields("PLA"), "", nil))
	id2, ref2 := decodeRef(postPublicItemOrder(t, publicMux, validItemOrderBody(item.ID, nil, 1)))
	if ref1 != "REQ-1001" || ref2 != "REQ-1002" {
		// The test config has no business name, so the fallback prefix applies.
		t.Fatalf("references = %q, %q; want REQ-1001, REQ-1002", ref1, ref2)
	}
	for id, want := range map[string]string{id1: ref1, id2: ref2} {
		var got string
		if err := db.QueryRow(`SELECT reference FROM quote_requests WHERE id = ?`, id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("stored reference for %s = %q, want %q", id, got, want)
		}
	}

	// The admin API exposes the reference.
	rec = serveReq(adminMux, httptest.NewRequest("GET", "/api/requests/"+id1, nil))
	var req QuoteRequest
	if err := json.Unmarshal(rec.Body.Bytes(), &req); err != nil {
		t.Fatal(err)
	}
	if req.Reference != ref1 {
		t.Errorf("admin Reference = %q, want %q", req.Reference, ref1)
	}
}

// The prefix follows the business name until quote.referencePrefix overrides it.
func TestPublicReferencePrefixFromBusinessName(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	cfg := newPublicTestConfig(t, dir)
	cfg.Quote.BusinessName = "Hot Layer Prints"
	store := NewConfigStore(cfg)
	mux, _ := buildPublicMux(store, db, dir, false, false)

	get := func() string {
		rec := postPublicQuoteRequest(t, mux, publicQuoteRequestFields("PLA"), "", nil)
		var b struct{ Reference string }
		json.Unmarshal(rec.Body.Bytes(), &b)
		return b.Reference
	}
	if got := get(); got != "HLP-1001" {
		t.Errorf("derived reference = %q, want HLP-1001", got)
	}
	cfg2 := *cfg
	cfg2.Quote.ReferencePrefix = "shop"
	store.Set(&cfg2)
	if got := get(); got != "SHOP-1002" {
		t.Errorf("after prefix override = %q, want SHOP-1002 (numbering continues)", got)
	}
}

// Demo mode shows a believable reference but never moves the counter or writes a row.
func TestPublicDemoModeReferenceDoesNotAdvanceCounter(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	cfg := newPublicTestConfig(t, dir)
	mux, _ := buildPublicMux(NewConfigStore(cfg), db, dir, false, true)
	for range 2 {
		rec := postPublicQuoteRequest(t, mux, publicQuoteRequestFields("PLA"), "", nil)
		var b struct{ Reference string }
		json.Unmarshal(rec.Body.Bytes(), &b)
		if b.Reference != "REQ-1001" {
			t.Errorf("demo reference = %q, want REQ-1001 each time", b.Reference)
		}
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM app_state WHERE key = ?`, requestRefSeqKey).Scan(&n)
	if n != 0 {
		t.Errorf("demo mode wrote the counter row")
	}
}

// A request without a print time is stored as "needs print time" with no price, and the admin API says so.
func TestPublicQuoteRequestWithoutPrintTime(t *testing.T) {
	for name, mutate := range map[string]func(map[string]string){
		"absent": func(f map[string]string) { delete(f, "timeSeconds") },
		"blank":  func(f map[string]string) { f["timeSeconds"] = "  " },
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			adminMux, publicMux, db, _ := newRequestsTestSetup(t, dir)
			fields := publicQuoteRequestFields("PLA")
			mutate(fields)
			rec := postPublicQuoteRequest(t, publicMux, fields, "cube.stl", []byte("solid x\nendsolid x\n"))
			if rec.Code != http.StatusCreated {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			var resp struct {
				ID             string
				PriceCents     int
				NeedsPrintTime bool
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatal(err)
			}
			if !resp.NeedsPrintTime || resp.PriceCents != 0 {
				t.Errorf("response = %+v, want needsPrintTime true and price 0", resp)
			}
			var needs, secs, price int
			if err := db.QueryRow(`SELECT needs_print_time, time_seconds, price_cents FROM quote_requests WHERE id = ?`, resp.ID).Scan(&needs, &secs, &price); err != nil {
				t.Fatal(err)
			}
			if needs != 1 || secs != 0 || price != 0 {
				t.Errorf("stored = needs %d, time %d, price %d; want 1, 0, 0", needs, secs, price)
			}
			rec = serveReq(adminMux, httptest.NewRequest("GET", "/api/requests/"+resp.ID, nil))
			var got QuoteRequest
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if !got.NeedsPrintTime {
				t.Errorf("admin needsPrintTime = false, want true: %s", rec.Body.String())
			}
		})
	}
}

// With a time, the flag stays off and the price is the engine's.
func TestPublicQuoteRequestWithPrintTimeIsNotFlagged(t *testing.T) {
	dir := t.TempDir()
	_, publicMux, db, cfg := newRequestsTestSetup(t, dir)
	rec := postPublicQuoteRequest(t, publicMux, publicQuoteRequestFields("PLA"), "", nil)
	var resp struct {
		ID             string
		PriceCents     int
		NeedsPrintTime bool
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.NeedsPrintTime || resp.PriceCents != expectedPublicPriceCents(t, cfg, "PLA") {
		t.Errorf("response = %+v, want an engine price and no flag", resp)
	}
	var needs int
	db.QueryRow(`SELECT needs_print_time FROM quote_requests WHERE id = ?`, resp.ID).Scan(&needs)
	if needs != 0 {
		t.Errorf("needs_print_time = %d, want 0", needs)
	}
}

// Orders and estimates each have their own publicRateLimit budget per IP.
func TestPublicOrdersAndEstimatesHaveSeparateRateBudgets(t *testing.T) {
	dir := t.TempDir()
	adminMux, publicMux, _, _ := newRequestsTestSetup(t, dir)
	rec := postItem(t, adminMux, `{"name":"Clip","priceCents":500,"public":true}`)
	var item Item
	json.Unmarshal(rec.Body.Bytes(), &item)

	order := func() int {
		b, _ := json.Marshal(validItemOrderBody(item.ID, nil, 1))
		req := httptest.NewRequest("POST", "/api/public/item-order", strings.NewReader(string(b)))
		req.Header.Set("Content-Type", "application/json")
		return serveReq(publicMux, req).Code
	}
	for i := range publicRateLimit {
		if code := postPublicQuoteRequest(t, publicMux, publicQuoteRequestFields("PLA"), "", nil).Code; code != http.StatusCreated {
			t.Fatalf("estimate %d = %d", i+1, code)
		}
	}
	if code := postPublicQuoteRequest(t, publicMux, publicQuoteRequestFields("PLA"), "", nil).Code; code != http.StatusTooManyRequests {
		t.Fatalf("estimate over budget = %d, want 429", code)
	}
	for i := range publicRateLimit {
		if code := order(); code != http.StatusCreated {
			t.Fatalf("order %d after exhausting estimates = %d, want 201", i+1, code)
		}
	}
	if code := order(); code != http.StatusTooManyRequests {
		t.Fatalf("order over budget = %d, want 429", code)
	}
}

func TestRequestNotificationForMissingPrintTime(t *testing.T) {
	cfg := &Config{Currency: Currency{Symbol: "$", Decimals: 2}}
	_, body := buildRequestNotification(cfg, QuoteRequest{
		ID: "abc", Reference: "HL-1001", Name: "A", Email: "a@example.com", Material: "PLA", Quantity: 1,
		WeightG: 10, NeedsPrintTime: true,
	})
	for _, want := range []string{"Print time: not provided", "Price: to be confirmed", "Reference: HL-1001", "Request ID: abc"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "$0.00") {
		t.Errorf("body shows a $0.00 price:\n%s", body)
	}
}

func TestHandleDemoReportsShopURLWhenShopOnMainPort(t *testing.T) {
	for _, on := range []bool{true, false} {
		dir := t.TempDir()
		db := openTestDB(t, dir)
		cfg := loadTestConfig(t, dir)
		cfg.Site.ShopOnMainPort = on
		auth := newAuthState("secret", nil)
		mux := routesMux(db, NewConfigStore(cfg), auth, dir, false)
		req := httptest.NewRequest("GET", "/api/demo", nil)
		req.Host = "localhost"
		rec := serveReq(auth.middleware(mux), req)
		var body struct {
			ShopURL string `json:"shopUrl"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		want := ""
		if on {
			want = "/"
		}
		if body.ShopURL != want {
			t.Errorf("shopOnMainPort=%v: shopUrl = %q, want %q", on, body.ShopURL, want)
		}
	}
}
