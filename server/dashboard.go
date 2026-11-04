package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

type dashboard struct {
	db    *sql.DB
	store *ConfigStore
	// clock returns the current time; tests replace it to pin a month boundary.
	clock func() time.Time
}

func newDashboard(db *sql.DB, store *ConfigStore) *dashboard {
	return &dashboard{db: db, store: store, clock: time.Now}
}

// revenueMonth is one bucket of dashboardResponse.RevenueByMonth.
type revenueMonth struct {
	Month string `json:"month"`
	Cents int    `json:"cents"`
}

// dashboardResponse is the GET /api/dashboard body; everything is computed server-side.
type dashboardResponse struct {
	RevenueMonthCents int              `json:"revenueMonthCents"`
	RevenueYearCents  int              `json:"revenueYearCents"`
	OutstandingCents  int              `json:"outstandingCents"`
	OverdueCount      int              `json:"overdueCount"`
	RecentInvoices    []InvoiceSummary `json:"recentInvoices"`
	RevenueByMonth    []revenueMonth   `json:"revenueByMonth"`

	// Quote tiles, computed over the quotes table's own columns.
	QuotesSavedThisMonth   int      `json:"quotesSavedThisMonth"`
	QuotesInvoicedSharePct *float64 `json:"quotesInvoicedSharePct"`
	QuotesAvgMarginPct     *float64 `json:"quotesAvgMarginPct"`

	// NeedsAttention is the dashboard's "Needs attention" list: up to attentionPerKind rows per kind, in the
	// order new requests, requests needing a print time, overdue invoices, draft invoices. AttentionCounts
	// carries the full count per kind so the page can say "and 4 more". Each list is present only when the
	// caller may open that tab (requests, invoices).
	NeedsAttention  []attentionItem `json:"needsAttention"`
	AttentionCounts attentionCounts `json:"attentionCounts"`
}

// attentionPerKind caps each kind of NeedsAttention row.
const attentionPerKind = 10

// Kinds of attentionItem.
const (
	attentionNewRequest       = "new-request"
	attentionPrintTimeRequest = "print-time-request"
	attentionOverdueInvoice   = "overdue-invoice"
	attentionDraftInvoice     = "draft-invoice"
)

// attentionItem is one row of NeedsAttention. Name is the requester or client, Number the invoice number
// (invoices only), AmountCents the request price or the invoice balance (overdue) or total (draft), and Date
// the due date (overdue invoices), the creation date (drafts), or the submission time (requests).
type attentionItem struct {
	Kind        string `json:"kind"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Number      string `json:"number"`
	AmountCents int    `json:"amountCents"`
	Date        string `json:"date"`
}

// attentionCounts is the full number of rows behind each NeedsAttention kind.
type attentionCounts struct {
	NewRequests       int `json:"newRequests"`
	PrintTimeRequests int `json:"printTimeRequests"`
	OverdueInvoices   int `json:"overdueInvoices"`
	DraftInvoices     int `json:"draftInvoices"`
}

// effectivePaymentsCTE is the "effective_payments" CTE the revenue queries read. A payment counts toward
// revenue only up to the invoice total: payments are taken in paid-date order (then created_at, then id) and
// each is capped at the total minus what the earlier ones already covered, so an overpayment's excess never
// reaches the dashboard (the invoice shows it as "Overpaid"). The window runs over every payment of an
// invoice, so callers filter on paid_date outside the CTE, never inside it.
const effectivePaymentsCTE = `effective_payments AS (
	SELECT p.paid_date AS paid_date,
	       MAX(0, MIN(p.amount_cents, i.total_cents - COALESCE(SUM(p.amount_cents) OVER (
	         PARTITION BY p.invoice_id ORDER BY p.paid_date, p.created_at, p.id
	         ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING), 0))) AS amount_cents
	FROM payments p JOIN invoices i ON i.id = p.invoice_id
)`

// handleGet bounds every aggregate to a 12-month window and a 10-row recent list, not all history.
// The indexes each query relies on come from the baseline schema in db.go.
func (d *dashboard) handleGet(w http.ResponseWriter, r *http.Request) {
	// Every "this month", "this year", and "today" below is in the configured time zone (config timezone):
	// payments carry a plain YYYY-MM-DD paid_date, so the local calendar decides which month a payment
	// belongs to. Stored timestamps (quotes.saved_at) are UTC and are compared as instants.
	loc := d.store.Get().Location()
	now := d.clock().In(loc)
	today := now.Format("2006-01-02")

	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
	monthEnd := monthStart.AddDate(0, 1, 0)
	yearStart := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, loc)
	yearEnd := yearStart.AddDate(1, 0, 0)
	windowStart := monthStart.AddDate(0, -11, 0) // first of the 12-month window

	resp := dashboardResponse{
		RecentInvoices: []InvoiceSummary{},
		RevenueByMonth: []revenueMonth{},
		NeedsAttention: []attentionItem{},
	}
	var overdueItems []attentionItem

	// Revenue is cash-basis: payment amounts bucketed by payment date, not invoice date or status. Half-open
	// paid_date ranges let payments_paid_date serve range scans (substr() prefixes force full scans).
	if err := d.db.QueryRow(`WITH `+effectivePaymentsCTE+` SELECT COALESCE(SUM(amount_cents), 0) FROM effective_payments WHERE paid_date >= ? AND paid_date < ?`,
		monthStart.Format("2006-01-02"), monthEnd.Format("2006-01-02")).Scan(&resp.RevenueMonthCents); err != nil {
		log.Printf("dashboard revenue month: %v", err)
		http.Error(w, "dashboard unavailable", http.StatusInternalServerError)
		return
	}
	if err := d.db.QueryRow(`WITH `+effectivePaymentsCTE+` SELECT COALESCE(SUM(amount_cents), 0) FROM effective_payments WHERE paid_date >= ? AND paid_date < ?`,
		yearStart.Format("2006-01-02"), yearEnd.Format("2006-01-02")).Scan(&resp.RevenueYearCents); err != nil {
		log.Printf("dashboard revenue year: %v", err)
		http.Error(w, "dashboard unavailable", http.StatusInternalServerError)
		return
	}

	// Outstanding = sum over sent invoices of (total - paid); overdueCount shares the balance > 0 gate.
	// Filter on status first, then join payments only for those invoices.
	rows, err := d.db.Query(`
		SELECT i.id, i.due_date, i.total_cents, COALESCE(SUM(p.amount_cents), 0), i.number, i.client_name
		FROM invoices i
		LEFT JOIN payments p ON p.invoice_id = i.id
		WHERE i.status = 'sent'
		GROUP BY i.id`)
	if err != nil {
		log.Printf("dashboard outstanding: %v", err)
		http.Error(w, "dashboard unavailable", http.StatusInternalServerError)
		return
	}
	for rows.Next() {
		var id, dueDate, number, clientName string
		var total, paid int
		if err := rows.Scan(&id, &dueDate, &total, &paid, &number, &clientName); err != nil {
			rows.Close()
			log.Printf("dashboard outstanding scan: %v", err)
			http.Error(w, "dashboard unavailable", http.StatusInternalServerError)
			return
		}
		balance := total - paid
		if balance > 0 {
			resp.OutstandingCents += balance
			if dueDate != "" && dueDate < today {
				resp.OverdueCount++
				overdueItems = append(overdueItems, attentionItem{Kind: attentionOverdueInvoice, ID: id, Name: clientName, Number: number, AmountCents: balance, Date: dueDate})
			}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		log.Printf("dashboard outstanding read: %v", err)
		http.Error(w, "dashboard unavailable", http.StatusInternalServerError)
		return
	}
	rows.Close()

	// Recent invoices: newest 10 by created_at, in the InvoiceSummary shape.
	// number_seq increases in insertion order, so it breaks ties within the same created_at second.
	// paidCents is joined in the same query, scoped to these 10 invoices.
	recentRows, err := d.db.Query(`
		SELECT i.id, i.number, i.status, i.client_id, i.client_name, i.client_email, i.client_billing_address, i.client_shipping_address,
		       i.issue_date, i.due_date, i.tax_bp, i.discount_cents, i.notes, i.subtotal_cents, i.tax_cents, i.total_cents, i.created_at, i.updated_at,
		       i.created_by, i.updated_by, i.version,
		       COALESCE(SUM(p.amount_cents), 0)
		FROM (
			SELECT id, number, number_year, number_seq, status, client_id, client_name, client_email, client_billing_address, client_shipping_address,
			       issue_date, due_date, tax_bp, discount_cents, notes, subtotal_cents, tax_cents, total_cents, created_at, updated_at, created_by, updated_by, version
			FROM invoices ORDER BY created_at DESC, number_year DESC, number_seq DESC LIMIT 10
		) i
		LEFT JOIN payments p ON p.invoice_id = i.id
		GROUP BY i.id
		ORDER BY i.created_at DESC, i.number_year DESC, i.number_seq DESC`)
	if err != nil {
		log.Printf("dashboard recent invoices: %v", err)
		http.Error(w, "dashboard unavailable", http.StatusInternalServerError)
		return
	}
	for recentRows.Next() {
		var paidCents int
		s, err := scanInvoiceSummary(recentRows, &paidCents)
		if err != nil {
			recentRows.Close()
			log.Printf("dashboard recent invoices scan: %v", err)
			http.Error(w, "dashboard unavailable", http.StatusInternalServerError)
			return
		}
		s.PaidCents = paidCents
		s.BalanceCents = s.TotalCents - paidCents
		resp.RecentInvoices = append(resp.RecentInvoices, s)
	}
	if err := recentRows.Err(); err != nil {
		recentRows.Close()
		log.Printf("dashboard recent invoices read: %v", err)
		http.Error(w, "dashboard unavailable", http.StatusInternalServerError)
		return
	}
	recentRows.Close()

	// revenueByMonth: 12 buckets from one grouped query; gaps are zero-filled in Go, never in SQL.
	monthSums, err := monthlyRevenueSums(d.db, windowStart.Format("2006-01-02"))
	if err != nil {
		log.Printf("dashboard revenue by month: %v", err)
		http.Error(w, "dashboard unavailable", http.StatusInternalServerError)
		return
	}
	resp.RevenueByMonth = last12Months(now, monthSums)

	if err := d.loadAttention(r, &resp, overdueItems); err != nil {
		log.Printf("dashboard attention: %v", err)
		http.Error(w, "dashboard unavailable", http.StatusInternalServerError)
		return
	}

	if err := d.loadQuoteTiles(&resp, monthStart, monthEnd); err != nil {
		log.Printf("dashboard quote tiles: %v", err)
		http.Error(w, "dashboard unavailable", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// loadAttention fills resp.NeedsAttention and resp.AttentionCounts. overdue is the overdue invoices handleGet
// already found (oldest due date first after the sort below). Requests are listed only for callers who may
// open the Requests tab, invoices only for those who may open Invoices.
func (d *dashboard) loadAttention(r *http.Request, resp *dashboardResponse, overdue []attentionItem) error {
	id := identityFor(r)
	allowed := func(key string) (bool, error) {
		if id.isAdmin {
			return true, nil
		}
		perms, err := userPermissions(d.db, id.username)
		if err != nil {
			return false, err
		}
		return hasPermission(perms, key), nil
	}

	canRequests, err := allowed("requests")
	if err != nil {
		return err
	}
	if canRequests {
		for _, q := range []struct {
			kind, where string
			count       *int
		}{
			{attentionNewRequest, "status = 'new' AND needs_print_time = 0", &resp.AttentionCounts.NewRequests},
			{attentionPrintTimeRequest, "status != 'finalized' AND needs_print_time = 1", &resp.AttentionCounts.PrintTimeRequests},
		} {
			if err := d.db.QueryRow(`SELECT COUNT(*) FROM quote_requests WHERE ` + q.where).Scan(q.count); err != nil {
				return err
			}
			rows, err := d.db.Query(`SELECT id, name, price_cents, created_at FROM quote_requests WHERE `+q.where+` ORDER BY created_at DESC, rowid DESC LIMIT ?`, attentionPerKind)
			if err != nil {
				return err
			}
			for rows.Next() {
				it := attentionItem{Kind: q.kind}
				if err := rows.Scan(&it.ID, &it.Name, &it.AmountCents, &it.Date); err != nil {
					rows.Close()
					return err
				}
				resp.NeedsAttention = append(resp.NeedsAttention, it)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
		}
	}

	canInvoices, err := allowed("invoices")
	if err != nil {
		return err
	}
	if canInvoices {
		sort.SliceStable(overdue, func(a, b int) bool { return overdue[a].Date < overdue[b].Date })
		resp.AttentionCounts.OverdueInvoices = len(overdue)
		resp.NeedsAttention = append(resp.NeedsAttention, overdue[:min(len(overdue), attentionPerKind)]...)

		if err := d.db.QueryRow(`SELECT COUNT(*) FROM invoices WHERE status = 'draft'`).Scan(&resp.AttentionCounts.DraftInvoices); err != nil {
			return err
		}
		rows, err := d.db.Query(`SELECT id, number, client_name, total_cents, created_at FROM invoices WHERE status = 'draft' ORDER BY number_year DESC, number_seq DESC LIMIT ?`, attentionPerKind)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			it := attentionItem{Kind: attentionDraftInvoice}
			if err := rows.Scan(&it.ID, &it.Number, &it.Name, &it.AmountCents, &it.Date); err != nil {
				return err
			}
			resp.NeedsAttention = append(resp.NeedsAttention, it)
		}
		return rows.Err()
	}
	return nil
}

// loadQuoteTiles: savedThisMonth counts quotes.saved_at in the half-open local month [monthStart, monthEnd);
// invoicedSharePct is the 0-100 share of this month's quotes with invoiced_as set (nil if none saved);
// avgMarginPct is the mean achieved_margin_pct over all invoiced quotes with a margin (nil if none).
func (d *dashboard) loadQuoteTiles(resp *dashboardResponse, monthStart, monthEnd time.Time) error {
	// saved_at is an RFC 3339 UTC string, so the bounds are converted to UTC before the string comparison.
	monthStartStr := monthStart.UTC().Format(time.RFC3339)
	monthEndStr := monthEnd.UTC().Format(time.RFC3339)

	var savedThisMonth, invoicedThisMonth int
	err := d.db.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(CASE WHEN invoiced_as IS NOT NULL AND invoiced_as != '' THEN 1 ELSE 0 END), 0)
		FROM quotes WHERE saved_at >= ? AND saved_at < ?`,
		monthStartStr, monthEndStr).Scan(&savedThisMonth, &invoicedThisMonth)
	if err != nil {
		return err
	}
	resp.QuotesSavedThisMonth = savedThisMonth
	if savedThisMonth > 0 {
		pct := float64(invoicedThisMonth) / float64(savedThisMonth) * 100
		resp.QuotesInvoicedSharePct = &pct
	}

	var avgMargin sql.NullFloat64
	err = d.db.QueryRow(`
		SELECT AVG(achieved_margin_pct) FROM quotes
		WHERE invoiced_as IS NOT NULL AND invoiced_as != '' AND achieved_margin_pct IS NOT NULL`,
	).Scan(&avgMargin)
	if err != nil {
		return err
	}
	if avgMargin.Valid {
		v := avgMargin.Float64
		resp.QuotesAvgMarginPct = &v
	}
	return nil
}

// monthlyRevenueSums returns amount_cents summed per "YYYY-MM" for payments on or after since.
// Gap-filling happens in last12Months; bounding on since lets payments_paid_date serve a range scan.
func monthlyRevenueSums(db *sql.DB, since string) (map[string]int, error) {
	rows, err := db.Query(`WITH `+effectivePaymentsCTE+` SELECT substr(paid_date, 1, 7), SUM(amount_cents) FROM effective_payments WHERE paid_date >= ? GROUP BY 1`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sums := map[string]int{}
	for rows.Next() {
		var month string
		var sum int
		if err := rows.Scan(&month, &sum); err != nil {
			return nil, err
		}
		sums[month] = sum
	}
	return sums, rows.Err()
}

// last12Months builds 12 "YYYY-MM" buckets ending at now's month in now's own location, oldest first,
// zero-filling gaps. The labels are calendar months, so the arithmetic runs in UTC on the local year and month.
func last12Months(now time.Time, sums map[string]int) []revenueMonth {
	out := make([]revenueMonth, 0, 12)
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -11, 0)
	for i := range 12 {
		key := start.AddDate(0, i, 0).Format("2006-01")
		out = append(out, revenueMonth{Month: key, Cents: sums[key]})
	}
	return out
}

// topItemsMax caps the ranking; the totals still cover every sale in the window.
const topItemsMax = 10

// topItemOption is one entry of topItem.Options.
type topItemOption struct {
	Name  string `json:"name"`
	Units int    `json:"units"`
}

// topItem is one ranked gallery item in topItemsResponse.
type topItem struct {
	ItemID        string          `json:"itemId"`
	Name          string          `json:"name"`
	Removed       bool            `json:"removed"`
	Units         int             `json:"units"`
	GalleryUnits  int             `json:"galleryUnits"`
	InPersonUnits int             `json:"inPersonUnits"`
	Orders        int             `json:"orders"`
	RevenueCents  int             `json:"revenueCents"`
	Options       []topItemOption `json:"options"`
}

// topItemsResponse is the GET /api/dashboard/top-items body.
type topItemsResponse struct {
	Days              int       `json:"days"`
	TotalUnits        int       `json:"totalUnits"`
	GalleryUnits      int       `json:"galleryUnits"`
	InPersonUnits     int       `json:"inPersonUnits"`
	TotalRevenueCents int       `json:"totalRevenueCents"`
	Items             []topItem `json:"items"`
}

// topItemSalesSQL is the window's sales from both sources, one row per sale: gallery orders from the
// append-only item_sales ledger (not quote_requests, whose rows the Requests tab can delete), and in-person
// sales from invoice lines linked to an item, counted while their invoice is sent or paid and dated by its
// issue date (a live query, so a cancel or a paid->sent correction needs no reversal row). These are booked
// sales, not payments: an unpaid sent invoice counts. An in-person line's revenue is its line amount, less
// its pro-rata share of the invoice-level discount: line * max(0, subtotal - discount) / subtotal, rounded
// per line. Tax is never included. ?1 is the RFC 3339
// window start, ?2 the same instant's YYYY-MM-DD date. An in-person line has no item_name column, so its
// description stands in as the snapshot name for a deleted item.
const topItemSalesSQL = `
	SELECT item_id, item_name, option_name, quantity, revenue_cents, sold_at, request_id AS order_id, 'gallery' AS source
	FROM item_sales WHERE sold_at >= ?1
	UNION ALL
	SELECT l.item_id, l.description, l.option_name, CAST(l.quantity AS INTEGER),
	       CAST(ROUND((ROUND(l.quantity * l.unit_price_cents + 1e-9) - l.discount_cents) *
	            CASE WHEN i.discount_cents > 0 AND i.subtotal_cents > 0
	                 THEN MAX(i.subtotal_cents - i.discount_cents, 0) * 1.0 / i.subtotal_cents
	                 ELSE 1.0 END) AS INTEGER),
	       i.issue_date, i.id, 'in_person'
	FROM invoice_lines l JOIN invoices i ON i.id = l.invoice_id
	WHERE l.item_id != '' AND i.status IN ('sent', 'paid') AND i.issue_date >= ?2`

// handleTopItems ranks gallery items by units sold over the last N days, across gallery orders and in-person
// invoice lines (topItemSalesSQL). days is 30 (the default), 90, or 365.
func (d *dashboard) handleTopItems(w http.ResponseWriter, r *http.Request) {
	days := 30
	if raw := r.URL.Query().Get("days"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || (n != 30 && n != 90 && n != 365) {
			writeAPIError(w, http.StatusBadRequest, "days: must be 30, 90, or 365")
			return
		}
		days = n
	}
	// The window starts N days ago: the instant (UTC, for sold_at) and the local calendar date (for an
	// invoice's issue_date, which is a plain date in the configured time zone).
	start := d.clock().AddDate(0, 0, -days)
	since := start.UTC().Format(time.RFC3339)
	sinceDate := start.In(d.store.Get().Location()).Format("2006-01-02")

	resp := topItemsResponse{Days: days, Items: []topItem{}}

	// One row per item. The name is the live items.name when the item still exists, else the snapshot name
	// from its most recent sale in the window.
	rows, err := d.db.Query(`
		WITH sales AS (`+topItemSalesSQL+`)
		SELECT s.item_id, i.name, COALESCE((
		         SELECT s2.item_name FROM sales s2
		         WHERE s2.item_id = s.item_id
		         ORDER BY s2.sold_at DESC, s2.order_id DESC LIMIT 1), ''),
		       SUM(s.quantity), SUM(CASE WHEN s.source = 'gallery' THEN s.quantity ELSE 0 END),
		       COUNT(DISTINCT s.order_id), SUM(s.revenue_cents)
		FROM sales s
		LEFT JOIN items i ON i.id = s.item_id
		GROUP BY s.item_id`, since, sinceDate)
	if err != nil {
		log.Printf("dashboard top items: %v", err)
		http.Error(w, "dashboard unavailable", http.StatusInternalServerError)
		return
	}
	var all []topItem
	for rows.Next() {
		var it topItem
		var liveName sql.NullString
		var snapshotName string
		if err := rows.Scan(&it.ItemID, &liveName, &snapshotName, &it.Units, &it.GalleryUnits, &it.Orders, &it.RevenueCents); err != nil {
			rows.Close()
			log.Printf("dashboard top items scan: %v", err)
			http.Error(w, "dashboard unavailable", http.StatusInternalServerError)
			return
		}
		if liveName.Valid {
			it.Name = liveName.String
		} else {
			it.Name = snapshotName
			it.Removed = true
		}
		it.InPersonUnits = it.Units - it.GalleryUnits
		it.Options = []topItemOption{}
		resp.TotalUnits += it.Units
		resp.GalleryUnits += it.GalleryUnits
		resp.InPersonUnits += it.InPersonUnits
		resp.TotalRevenueCents += it.RevenueCents
		all = append(all, it)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		log.Printf("dashboard top items read: %v", err)
		http.Error(w, "dashboard unavailable", http.StatusInternalServerError)
		return
	}
	rows.Close()

	sort.SliceStable(all, func(a, b int) bool {
		x, y := all[a], all[b]
		if x.Units != y.Units {
			return x.Units > y.Units
		}
		if x.RevenueCents != y.RevenueCents {
			return x.RevenueCents > y.RevenueCents
		}
		return strings.ToLower(x.Name) < strings.ToLower(y.Name)
	})
	if len(all) > topItemsMax {
		all = all[:topItemsMax]
	}
	if len(all) > 0 {
		pos := make(map[string]int, len(all))
		for i, it := range all {
			pos[it.ItemID] = i
		}
		optRows, err := d.db.Query(`
			WITH sales AS (`+topItemSalesSQL+`)
			SELECT item_id, option_name, SUM(quantity) FROM sales
			WHERE option_name != ''
			GROUP BY item_id, option_name`, since, sinceDate)
		if err != nil {
			log.Printf("dashboard top items options: %v", err)
			http.Error(w, "dashboard unavailable", http.StatusInternalServerError)
			return
		}
		for optRows.Next() {
			var itemID, name string
			var units int
			if err := optRows.Scan(&itemID, &name, &units); err != nil {
				optRows.Close()
				log.Printf("dashboard top items options scan: %v", err)
				http.Error(w, "dashboard unavailable", http.StatusInternalServerError)
				return
			}
			if i, ok := pos[itemID]; ok {
				all[i].Options = append(all[i].Options, topItemOption{Name: name, Units: units})
			}
		}
		if err := optRows.Err(); err != nil {
			optRows.Close()
			log.Printf("dashboard top items options read: %v", err)
			http.Error(w, "dashboard unavailable", http.StatusInternalServerError)
			return
		}
		optRows.Close()
		for i := range all {
			opts := all[i].Options
			sort.Slice(opts, func(a, b int) bool {
				if opts[a].Units != opts[b].Units {
					return opts[a].Units > opts[b].Units
				}
				return opts[a].Name < opts[b].Name
			})
		}
	}
	resp.Items = append(resp.Items, all...)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
