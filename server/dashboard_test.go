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

// Seeds invoices and payments across three payment months and every relevant status, then asserts every
// dashboardResponse field: revenue, the zero-filled 12-month series, outstanding, overdue (balance > 0, not status alone), recentInvoices order.
func TestDashboardAggregatesAcrossMonthsAndStatuses(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	now := time.Now().UTC()

	monthStart := func(offset int) time.Time {
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, offset, 0)
	}
	monthKey := func(offset int) string { return monthStart(offset).Format("2006-01") }
	pastDue := now.AddDate(0, 0, -30).Format("2006-01-02")
	futureDue := now.AddDate(0, 0, 30).Format("2006-01-02")

	// Invoice A: sent, overdue (due date past, balance > 0), partially paid
	// across two different months so the balance stays positive throughout.
	rec := postInvoice(t, mux, fmt.Sprintf(`{"clientName":"A","issueDate":"2020-01-01","dueDate":%q,"lines":[{"description":"X","quantity":1,"unitPriceCents":10000}]}`, pastDue))
	invA := mustUnmarshalInvoice(t, rec)
	rec = setInvoiceStatus(t, mux, invA.ID, "sent")
	invA = mustUnmarshalInvoice(t, rec)
	postPayment(t, mux, invA.ID, fmt.Sprintf(`{"paidDate":"%s-10","amountCents":2500}`, monthKey(-2)))
	postPayment(t, mux, invA.ID, fmt.Sprintf(`{"paidDate":"%s-10","amountCents":1000}`, monthKey(-1)))

	// Invoice B: sent, zero-total, overdue by due date alone; balance is
	// exactly 0 (not > 0), so it must be excluded from overdueCount even
	// though its due date is in the past.
	rec = postInvoice(t, mux, fmt.Sprintf(`{"clientName":"B","issueDate":"2020-01-01","dueDate":%q,"lines":[]}`, pastDue))
	invB := mustUnmarshalInvoice(t, rec)
	rec = setInvoiceStatus(t, mux, invB.ID, "sent")
	mustUnmarshalInvoice(t, rec)

	// Invoice C: sent, not overdue (due date in the future), balance > 0
	// contributes to outstanding only.
	rec = postInvoice(t, mux, fmt.Sprintf(`{"clientName":"C","issueDate":"2020-01-01","dueDate":%q,"lines":[{"description":"X","quantity":1,"unitPriceCents":3000}]}`, futureDue))
	invC := mustUnmarshalInvoice(t, rec)
	rec = setInvoiceStatus(t, mux, invC.ID, "sent")
	mustUnmarshalInvoice(t, rec)

	// Invoice D: sent, fully paid this month (auto-flips to paid); excluded
	// from outstanding/overdue, counted as revenue this month/year.
	rec = postInvoice(t, mux, `{"clientName":"D","lines":[{"description":"X","quantity":1,"unitPriceCents":8000}]}`)
	invD := mustUnmarshalInvoice(t, rec)
	rec = setInvoiceStatus(t, mux, invD.ID, "sent")
	invD = mustUnmarshalInvoice(t, rec)
	rec = postPayment(t, mux, invD.ID, fmt.Sprintf(`{"paidDate":"%s-05","amountCents":8000}`, monthKey(0)))
	invD = mustUnmarshalInvoice(t, rec)
	if invD.Status != "paid" {
		t.Fatalf("setup: invoice D want paid, got %q", invD.Status)
	}

	// Invoice E: draft, never touches revenue/outstanding/overdue.
	postInvoice(t, mux, `{"clientName":"E","lines":[]}`)

	rec = serveReq(mux, httptest.NewRequest("GET", "/api/dashboard", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/dashboard: %d %s", rec.Code, rec.Body.String())
	}
	var got dashboardResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal dashboard: %v", err)
	}

	// --- revenueByMonth: 12 buckets, oldest first, zero-filled. ---
	if len(got.RevenueByMonth) != 12 {
		t.Fatalf("revenueByMonth len = %d, want 12", len(got.RevenueByMonth))
	}
	if got.RevenueByMonth[11].Month != monthKey(0) {
		t.Errorf("last bucket month = %q, want %q (current month)", got.RevenueByMonth[11].Month, monthKey(0))
	}
	if got.RevenueByMonth[0].Month != monthKey(-11) {
		t.Errorf("first bucket month = %q, want %q", got.RevenueByMonth[0].Month, monthKey(-11))
	}
	wantByMonth := map[string]int{monthKey(-2): 2500, monthKey(-1): 1000, monthKey(0): 8000}
	for _, b := range got.RevenueByMonth {
		want := wantByMonth[b.Month] // 0 for every unseeded month: exercises the zero-fill
		if b.Cents != want {
			t.Errorf("revenueByMonth[%s] = %d, want %d", b.Month, b.Cents, want)
		}
	}

	// --- revenueMonthCents / revenueYearCents ---
	if got.RevenueMonthCents != 8000 {
		t.Errorf("revenueMonthCents = %d, want 8000", got.RevenueMonthCents)
	}
	wantYear := 8000
	if monthStart(-1).Year() == now.Year() {
		wantYear += 1000
	}
	if monthStart(-2).Year() == now.Year() {
		wantYear += 2500
	}
	if got.RevenueYearCents != wantYear {
		t.Errorf("revenueYearCents = %d, want %d", got.RevenueYearCents, wantYear)
	}

	// --- outstanding / overdue ---
	// A: balance 10000-2500-1000=6500 (overdue). B: balance 0 (excluded).
	// C: balance 3000 (not overdue, due date in the future).
	wantOutstanding := 6500 + 3000
	if got.OutstandingCents != wantOutstanding {
		t.Errorf("outstandingCents = %d, want %d", got.OutstandingCents, wantOutstanding)
	}
	if got.OverdueCount != 1 {
		t.Errorf("overdueCount = %d, want 1 (only A; B's balance is 0)", got.OverdueCount)
	}

	// --- recentInvoices: newest 10 by created_at, newest first. ---
	if len(got.RecentInvoices) != 5 {
		t.Fatalf("recentInvoices len = %d, want 5", len(got.RecentInvoices))
	}
	wantOrder := []string{"E", "D", "C", "B", "A"} // newest (E, created last) first
	for i, name := range wantOrder {
		if got.RecentInvoices[i].ClientName != name {
			t.Errorf("recentInvoices[%d].clientName = %q, want %q", i, got.RecentInvoices[i].ClientName, name)
		}
	}
	for _, s := range got.RecentInvoices {
		if s.ClientName == "A" {
			if s.PaidCents != 3500 || s.BalanceCents != 6500 {
				t.Errorf("invoice A summary paid/balance = %d/%d, want 3500/6500", s.PaidCents, s.BalanceCents)
			}
		}
	}
}

// Revenue aggregates are cash-basis sums over ALL payments while recentInvoices is bounded to the newest 10.
// Seeds 11 sent+paid invoices so the oldest is excluded from recentInvoices but its payment still counts.
func TestDashboardRevenueUnaffectedByRecentInvoicesBound(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	now := time.Now().UTC()
	monthKey := now.Format("2006-01-02")

	var oldestID string
	for i := range 11 {
		rec := postInvoice(t, mux, fmt.Sprintf(`{"clientName":"C%d","lines":[{"description":"X","quantity":1,"unitPriceCents":1000}]}`, i))
		inv := mustUnmarshalInvoice(t, rec)
		rec = setInvoiceStatus(t, mux, inv.ID, "sent")
		mustUnmarshalInvoice(t, rec)
		rec = postPayment(t, mux, inv.ID, fmt.Sprintf(`{"paidDate":"%s","amountCents":1000}`, monthKey))
		mustUnmarshalInvoice(t, rec)
		if i == 0 {
			oldestID = inv.ID // created first, so oldest by created_at -- excluded once 11 exist
		}
	}

	rec := serveReq(mux, httptest.NewRequest("GET", "/api/dashboard", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/dashboard: %d %s", rec.Code, rec.Body.String())
	}
	var got dashboardResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal dashboard: %v", err)
	}

	if len(got.RecentInvoices) != 10 {
		t.Fatalf("recentInvoices len = %d, want 10", len(got.RecentInvoices))
	}
	for _, s := range got.RecentInvoices {
		if s.ID == oldestID {
			t.Fatalf("oldest invoice %s should be excluded from the 10-row recent list", oldestID)
		}
	}

	// All 11 payments (1000 cents each) count toward revenue regardless of
	// which invoices made the recent-list cut.
	if got.RevenueMonthCents != 11000 {
		t.Errorf("revenueMonthCents = %d, want 11000 (all 11 payments, not just the 10 recent invoices)", got.RevenueMonthCents)
	}
	if got.RevenueYearCents != 11000 {
		t.Errorf("revenueYearCents = %d, want 11000", got.RevenueYearCents)
	}
	if got.RevenueByMonth[11].Cents != 11000 {
		t.Errorf("current month bucket = %d, want 11000", got.RevenueByMonth[11].Cents)
	}
}

// A payment dated before `since` must not appear in the returned map; one dated exactly on `since` must.
func TestMonthlyRevenueSumsExcludesPaymentsBeforeSince(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(`INSERT INTO invoices (id, number, number_year, number_seq, client_name, total_cents, created_at, updated_at)
		VALUES ('inv1', 'INV-1', 2025, 1, 'A', 1000, ?, ?)`, now, now); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}
	// One payment exactly on the boundary date, one the day before it.
	if _, err := db.Exec(`INSERT INTO payments (id, invoice_id, paid_date, amount_cents, created_at) VALUES ('p1', 'inv1', '2025-01-15', 700, ?)`, now); err != nil {
		t.Fatalf("seed payment 1: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO payments (id, invoice_id, paid_date, amount_cents, created_at) VALUES ('p2', 'inv1', '2025-01-14', 300, ?)`, now); err != nil {
		t.Fatalf("seed payment 2: %v", err)
	}

	sums, err := monthlyRevenueSums(db, "2025-01-15")
	if err != nil {
		t.Fatalf("monthlyRevenueSums: %v", err)
	}
	if sums["2025-01"] != 700 {
		t.Errorf(`sums["2025-01"] = %d, want 700 (the day-before payment must be excluded by since)`, sums["2025-01"])
	}
}

func TestDashboardEmptyDatabaseServesZeroedResponse(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	rec := serveReq(mux, httptest.NewRequest("GET", "/api/dashboard", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/dashboard: %d %s", rec.Code, rec.Body.String())
	}
	var got dashboardResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal dashboard: %v", err)
	}
	if got.RevenueMonthCents != 0 || got.RevenueYearCents != 0 || got.OutstandingCents != 0 || got.OverdueCount != 0 {
		t.Errorf("non-zero aggregate on an empty database: %+v", got)
	}
	if len(got.RecentInvoices) != 0 {
		t.Errorf("recentInvoices = %+v, want empty", got.RecentInvoices)
	}
	if len(got.RevenueByMonth) != 12 {
		t.Fatalf("revenueByMonth len = %d, want 12 (still fully zero-filled)", len(got.RevenueByMonth))
	}
	for _, b := range got.RevenueByMonth {
		if b.Cents != 0 {
			t.Errorf("revenueByMonth[%s] = %d, want 0", b.Month, b.Cents)
		}
	}
	if got.QuotesSavedThisMonth != 0 {
		t.Errorf("quotesSavedThisMonth = %d, want 0", got.QuotesSavedThisMonth)
	}
	if got.QuotesInvoicedSharePct != nil {
		t.Errorf("quotesInvoicedSharePct = %v, want nil (nothing saved this month)", got.QuotesInvoicedSharePct)
	}
	if got.QuotesAvgMarginPct != nil {
		t.Errorf("quotesAvgMarginPct = %v, want nil (no invoiced quotes)", got.QuotesAvgMarginPct)
	}
}

// seedDashboardQuote inserts a quotes row with only the columns the quote tiles read; other NOT NULL columns get placeholders.
func seedDashboardQuote(t *testing.T, db *sql.DB, id, savedAt string, invoicedAs *string, marginPct *float64) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO quotes (id, saved_at, label, client_name, project, reference, status, status_log, invoiced_as, inputs, quantity, per_unit_total_cents, batch_total_cents, achieved_margin_pct, valid)
		VALUES (?, ?, '', '', '', '', 'draft', '[]', ?, '{}', 1, 0, 0, ?, 0)`,
		id, savedAt, invoicedAs, marginPct)
	if err != nil {
		t.Fatalf("seed quote %s: %v", id, err)
	}
}

// Pins the quote tiles (loadQuoteTiles) against a hand-computed population: A this month, invoiced, margin 20; B this month, not invoiced; C this month, invoiced_as "" (as unset);
// D last month, invoiced, margin 60 (counts only toward the unwindowed avgMarginPct). Expected: savedThisMonth 3, invoicedSharePct 1/3*100, avgMarginPct mean(20, 60) = 40.
func TestDashboardQuoteTiles(t *testing.T) {
	dir := t.TempDir()
	mux := newTestMux(t, dir)
	db := openTestDB(t, dir)

	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	thisMonth := monthStart.Add(2 * time.Hour).Format(time.RFC3339)
	lastMonth := monthStart.AddDate(0, -1, 5).Format(time.RFC3339)

	invA, invD := "inv-a", "inv-d"
	marginA, marginD := 20.0, 60.0
	seedDashboardQuote(t, db, "a", thisMonth, &invA, &marginA)
	seedDashboardQuote(t, db, "b", thisMonth, nil, nil)
	empty := ""
	seedDashboardQuote(t, db, "c", thisMonth, &empty, nil)
	seedDashboardQuote(t, db, "d", lastMonth, &invD, &marginD)

	rec := serveReq(mux, httptest.NewRequest("GET", "/api/dashboard", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/dashboard: %d %s", rec.Code, rec.Body.String())
	}
	var got dashboardResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal dashboard: %v", err)
	}

	if got.QuotesSavedThisMonth != 3 {
		t.Errorf("quotesSavedThisMonth = %d, want 3", got.QuotesSavedThisMonth)
	}
	if got.QuotesInvoicedSharePct == nil {
		t.Fatalf("quotesInvoicedSharePct = nil, want a value")
	}
	wantShare := 1.0 / 3.0 * 100
	if diff := *got.QuotesInvoicedSharePct - wantShare; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("quotesInvoicedSharePct = %v, want %v", *got.QuotesInvoicedSharePct, wantShare)
	}
	if got.QuotesAvgMarginPct == nil || *got.QuotesAvgMarginPct != 40 {
		t.Errorf("quotesAvgMarginPct = %v, want 40", got.QuotesAvgMarginPct)
	}
}

// dashboardAt serves GET /api/dashboard with the clock pinned to instant, in the zone name.
func dashboardAt(t *testing.T, db *sql.DB, cfg *Config, instant time.Time) dashboardResponse {
	t.Helper()
	d := newDashboard(db, NewConfigStore(cfg))
	d.clock = func() time.Time { return instant }
	rec := httptest.NewRecorder()
	d.handleGet(rec, httptest.NewRequest("GET", "/api/dashboard", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("dashboard status %d: %s", rec.Code, rec.Body.String())
	}
	var resp dashboardResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

// The month, year, revenue-by-month buckets, and overdue check follow the configured time zone. At
// 2026-11-01T03:30Z it is still Oct 31 in Los Angeles (UTC-7) but already Nov 1 in UTC.
func TestDashboardMonthBoundaryFollowsConfiguredTimezone(t *testing.T) {
	instant := time.Date(2026, 11, 1, 3, 30, 0, 0, time.UTC)
	for _, tc := range []struct {
		zone                     string
		wantMonthCents, wantOver int
		wantLastBucket           string
		wantLastBucketCents      int
		wantQuotesSavedThisMonth int
	}{
		// UTC: Nov 1. Only the Nov 1 payment is "this month", the sent invoice due Oct 31 is overdue, and the
		// quote saved 2026-11-01T01:00Z is this month's.
		{"UTC", 500, 1, "2026-11", 500, 1},
		// Los Angeles: Oct 31. The Oct 31 payment is this month, nothing is overdue yet (due today), and the
		// quote saved at 01:00Z on Nov 1 belongs to Oct 31 local, which is this month.
		{"America/Los_Angeles", 1000, 0, "2026-10", 1000, 1},
	} {
		t.Run(tc.zone, func(t *testing.T) {
			dir := t.TempDir()
			db, cfg, _ := openTestEnv(t, dir)
			cfg.Timezone = tc.zone
			mustExec := func(q string, args ...any) {
				t.Helper()
				if _, err := db.Exec(q, args...); err != nil {
					t.Fatal(err)
				}
			}
			mustExec(`INSERT INTO invoices (id, number, number_year, number_seq, status, client_name, due_date, total_cents, created_at, updated_at)
				VALUES ('i1', 'INV-2026-0001', 2026, 1, 'sent', 'A', '2026-10-31', 5000, 't', 't')`)
			mustExec(`INSERT INTO payments (id, invoice_id, paid_date, amount_cents, created_at) VALUES ('p1', 'i1', '2026-10-31', 1000, 't')`)
			mustExec(`INSERT INTO payments (id, invoice_id, paid_date, amount_cents, created_at) VALUES ('p2', 'i1', '2026-11-01', 500, 't')`)
			mustExec(`INSERT INTO quotes (id, saved_at, inputs, quantity, per_unit_total_cents, batch_total_cents, valid) VALUES ('q1', '2026-11-01T01:00:00Z', '{}', 1, 0, 0, 1)`)

			resp := dashboardAt(t, db, cfg, instant)
			if resp.RevenueMonthCents != tc.wantMonthCents {
				t.Errorf("revenueMonthCents = %d, want %d", resp.RevenueMonthCents, tc.wantMonthCents)
			}
			if resp.RevenueYearCents != 1500 {
				t.Errorf("revenueYearCents = %d, want 1500", resp.RevenueYearCents)
			}
			if resp.OverdueCount != tc.wantOver {
				t.Errorf("overdueCount = %d, want %d", resp.OverdueCount, tc.wantOver)
			}
			last := resp.RevenueByMonth[len(resp.RevenueByMonth)-1]
			if last.Month != tc.wantLastBucket || last.Cents != tc.wantLastBucketCents {
				t.Errorf("last bucket = %+v, want %s / %d", last, tc.wantLastBucket, tc.wantLastBucketCents)
			}
			if resp.QuotesSavedThisMonth != tc.wantQuotesSavedThisMonth {
				t.Errorf("quotesSavedThisMonth = %d, want %d", resp.QuotesSavedThisMonth, tc.wantQuotesSavedThisMonth)
			}
		})
	}
}

// A quote saved just before local midnight at the month boundary is last month's in a zone behind UTC.
func TestDashboardQuoteTilesUseLocalMonth(t *testing.T) {
	db, cfg, _ := openTestEnv(t, t.TempDir())
	cfg.Timezone = "America/Los_Angeles"
	// 2026-11-01T06:30Z is Oct 31 23:30 in Los Angeles: October, so not November's quote.
	if _, err := db.Exec(`INSERT INTO quotes (id, saved_at, inputs, quantity, per_unit_total_cents, batch_total_cents, valid) VALUES ('q1', '2026-11-01T06:30:00Z', '{}', 1, 0, 0, 1)`); err != nil {
		t.Fatal(err)
	}
	// Clock: Nov 1 12:00 local (19:00Z). q1 is October's.
	resp := dashboardAt(t, db, cfg, time.Date(2026, 11, 1, 19, 0, 0, 0, time.UTC))
	if resp.QuotesSavedThisMonth != 0 {
		t.Errorf("quotesSavedThisMonth = %d, want 0", resp.QuotesSavedThisMonth)
	}
}

// saved_at carries milliseconds since the quote editor started versioning saves ("...:00.123Z"); older rows
// have whole seconds ("...:00Z"). As strings "." sorts before "Z", so a millisecond timestamp in the first
// second of a month compared as less than the whole-second boundary and fell into the previous month.
func TestDashboardMonthBoundaryWithMillisecondSavedAt(t *testing.T) {
	db, cfg, _ := openTestEnv(t, t.TempDir())
	cfg.Timezone = "UTC"
	rows := []struct{ id, savedAt string }{
		{"oct-last", "2026-10-31T23:59:59.999Z"},
		{"nov-first-ms", "2026-11-01T00:00:00.123Z"},
		{"nov-first-ms-zero", "2026-11-01T00:00:00.000Z"},
		{"nov-first-legacy", "2026-11-01T00:00:00Z"},
		{"nov-mid", "2026-11-15T12:00:00.500Z"},
		{"nov-last-ms", "2026-11-30T23:59:59.999Z"},
		{"dec-first-ms", "2026-12-01T00:00:00.001Z"},
		{"dec-first-ms-zero", "2026-12-01T00:00:00.000Z"},
		{"dec-first-legacy", "2026-12-01T00:00:00Z"},
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO quotes (id, saved_at, inputs, quantity, per_unit_total_cents, batch_total_cents, valid) VALUES (?, ?, '{}', 1, 0, 0, 1)`, r.id, r.savedAt); err != nil {
			t.Fatal(err)
		}
	}
	resp := dashboardAt(t, db, cfg, time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC))
	if want := 5; resp.QuotesSavedThisMonth != want {
		t.Errorf("quotesSavedThisMonth = %d, want %d (the five November rows, whichever precision saved_at has)", resp.QuotesSavedThisMonth, want)
	}
}

// The balancing payment of a manual sent->paid gets today's date in the configured zone, not UTC's.
func TestMarkPaidBalancingPaymentUsesConfiguredTimezone(t *testing.T) {
	dir := t.TempDir()
	db, cfg, store := openTestEnv(t, dir)
	cfg.Timezone = "Pacific/Kiritimati" // UTC+14: its date is ahead of UTC's for 14 hours a day
	mux := routesMux(db, store, newAuthState("", nil), dir, false)
	inv := newSentInvoice(t, mux, 1000)
	before := time.Now().In(cfg.Location()).Format("2006-01-02")
	rec := setInvoiceStatus(t, mux, inv.ID, "paid")
	after := time.Now().In(cfg.Location()).Format("2006-01-02")
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d %s", rec.Code, rec.Body.String())
	}
	got := mustUnmarshalInvoice(t, rec)
	if len(got.Payments) != 1 || (got.Payments[0].PaidDate != before && got.Payments[0].PaidDate != after) {
		t.Errorf("payments = %+v, want one dated %s", got.Payments, before)
	}
}

// Revenue counts a payment only up to the invoice total (decision 9): an overpayment's excess never reaches
// the dashboard, whichever month it lands in.
func TestDashboardRevenueCapsOverpayment(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	cfg := loadTestConfig(t, dir)
	cfg.Timezone = "UTC"
	d := newDashboard(db, NewConfigStore(cfg))
	d.clock = func() time.Time { return time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC) }

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(`INSERT INTO invoices (id, number, number_year, number_seq, status, client_name, total_cents, created_at, updated_at)
		VALUES ('inv1', 'INV-1', 2026, 1, 'paid', 'A', 1484, ?, ?)`, now, now); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}
	for _, p := range []struct {
		id, date string
		cents    int
	}{{"p1", "2026-02-10", 501}, {"p2", "2026-03-05", 2000}} {
		if _, err := db.Exec(`INSERT INTO payments (id, invoice_id, paid_date, amount_cents, created_at) VALUES (?, 'inv1', ?, ?, ?)`, p.id, p.date, p.cents, now); err != nil {
			t.Fatalf("seed payment: %v", err)
		}
	}

	rec := serveReq(http.HandlerFunc(d.handleGet), httptest.NewRequest("GET", "/api/dashboard", nil))
	var resp dashboardResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, rec.Body.String())
	}
	if resp.RevenueMonthCents != 983 {
		t.Errorf("revenueMonthCents = %d, want 983 (1484 total - 501 earlier)", resp.RevenueMonthCents)
	}
	if resp.RevenueYearCents != 1484 {
		t.Errorf("revenueYearCents = %d, want 1484 (capped at the invoice total)", resp.RevenueYearCents)
	}
}

func TestDashboardNeedsAttention(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	cfg := loadTestConfig(t, dir)
	cfg.Timezone = "UTC"
	d := newDashboard(db, NewConfigStore(cfg))
	d.clock = func() time.Time { return time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC) }

	now := time.Now().UTC().Format(time.RFC3339)
	inv := func(id string, seq int, status, due string, total int) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO invoices (id, number, number_year, number_seq, status, client_name, due_date, total_cents, created_at, updated_at)
			VALUES (?, ?, 2026, ?, ?, 'Casey', ?, ?, ?, ?)`, id, fmt.Sprintf("INV-%d", seq), seq, status, due, total, now, now); err != nil {
			t.Fatalf("seed invoice: %v", err)
		}
	}
	inv("overdue", 1, "sent", "2026-03-01", 1000)
	inv("notdue", 2, "sent", "2026-04-01", 1000)
	inv("draft", 3, "draft", "", 700)
	req := func(id, status string, needsTime int) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO quote_requests (id, status, name, email, material, quantity, created_at, needs_print_time)
			VALUES (?, ?, 'Visitor', 'v@example.com', 'PLA', 1, ?, ?)`, id, status, now, needsTime); err != nil {
			t.Fatalf("seed request: %v", err)
		}
	}
	req("r-new", "new", 0)
	req("r-time", "viewed", 1)
	req("r-done", "finalized", 0)

	rec := serveReq(http.HandlerFunc(d.handleGet), httptest.NewRequest("GET", "/api/dashboard", nil))
	var resp dashboardResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var kinds []string
	for _, it := range resp.NeedsAttention {
		kinds = append(kinds, it.Kind+":"+it.ID)
	}
	want := "overdue-invoice:overdue draft-invoice:draft print-time-request:r-time new-request:r-new"
	if got := strings.Join(kinds, " "); got != want {
		t.Errorf("needsAttention = %q, want %q", got, want)
	}
	if c := resp.AttentionCounts; c.NewRequests != 1 || c.PrintTimeRequests != 1 || c.OverdueInvoices != 1 || c.DraftInvoices != 1 {
		t.Errorf("attentionCounts = %+v, want one of each", c)
	}
	if resp.NeedsAttention[0].Number != "INV-1" || resp.NeedsAttention[0].AmountCents != 1000 {
		t.Errorf("overdue row = %+v, want INV-1 with its 1000 balance", resp.NeedsAttention[0])
	}
}

func TestDashboardNeedsAttentionCapsAndSentQuotes(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	cfg := loadTestConfig(t, dir)
	cfg.Timezone = "UTC"
	d := newDashboard(db, NewConfigStore(cfg))
	d.clock = func() time.Time { return time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC) }

	now := time.Now().UTC().Format(time.RFC3339)
	for i := 1; i <= 5; i++ {
		if _, err := db.Exec(`INSERT INTO invoices (id, number, number_year, number_seq, status, client_name, total_cents, created_at, updated_at)
			VALUES (?, ?, 2026, ?, 'draft', 'Casey', 500, ?, ?)`, fmt.Sprintf("d%d", i), fmt.Sprintf("INV-%d", i), i, now, now); err != nil {
			t.Fatalf("seed invoice: %v", err)
		}
	}
	quote := func(id, status, invoicedAs string) {
		t.Helper()
		var inv any
		if invoicedAs != "" {
			inv = invoicedAs
		}
		if _, err := db.Exec(`INSERT INTO quotes (id, saved_at, label, client_name, status, invoiced_as, inputs, quantity, per_unit_total_cents, batch_total_cents, resolved_price_cents, valid)
			VALUES (?, ?, 'Lamp', 'Dana', ?, ?, '{}', 1, 100, 100, 2500, 1)`, id, now, status, inv); err != nil {
			t.Fatalf("seed quote: %v", err)
		}
	}
	quote("q-sent", "sent", "")
	quote("q-draft", "draft", "")
	quote("q-invoiced", "sent", "inv-x")

	rec := serveReq(http.HandlerFunc(d.handleGet), httptest.NewRequest("GET", "/api/dashboard", nil))
	var resp dashboardResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	drafts, sent := 0, 0
	for _, it := range resp.NeedsAttention {
		switch it.Kind {
		case "draft-invoice":
			drafts++
		case "sent-quote":
			sent++
			if it.ID != "q-sent" || it.Name != "Dana" || it.AmountCents != 2500 {
				t.Errorf("sent quote row = %+v", it)
			}
		}
	}
	if drafts != 3 || resp.AttentionCounts.DraftInvoices != 5 {
		t.Errorf("drafts listed %d (want 3) of %d (want 5)", drafts, resp.AttentionCounts.DraftInvoices)
	}
	if sent != 1 || resp.AttentionCounts.SentQuotes != 1 {
		t.Errorf("sent quotes listed %d of %d, want 1 of 1 (draft and invoiced quotes excluded)", sent, resp.AttentionCounts.SentQuotes)
	}
}
