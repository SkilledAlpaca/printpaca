package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const invoicesMaxBytes = 256 * 1024 // 256 KiB

// invoiceVersionConflictMessage is the 409 body for a PUT that carries a stale version. The web app shows it
// beside Reload and Overwrite buttons, so keep it a full sentence.
const invoiceVersionConflictMessage = "This was changed in another tab or by another user."

// invoiceStatusTransitions enumerates every allowed status transition. Any other pair is a 409.
var invoiceStatusTransitions = map[string]map[string]bool{
	"draft":    {"sent": true, "canceled": true},
	"sent":     {"paid": true, "canceled": true},
	"paid":     {"sent": true}, // correction
	"canceled": {},
}

var invoiceStatuses = map[string]bool{"draft": true, "sent": true, "paid": true, "canceled": true}

type invoices struct {
	db    *sql.DB
	store *ConfigStore
	// dial is the SMTP dialer handleSend uses; tests overwrite it with a stub.
	dial smtpDialer
	// afterLoad, if set, runs right after a handler's pre-transaction load; tests use it to land a competing
	// write in the window between the load and the transaction.
	afterLoad func()
}

func newInvoices(db *sql.DB, store *ConfigStore) *invoices {
	return &invoices{db: db, store: store, dial: dialSMTP}
}

// defaultTaxBp converts the default sales tax percentage to integer basis points at the API boundary,
// the only place a float tax rate may exist; everything stored or computed downstream is basis points.
// cfg is the caller's per-request snapshot.
func (iv *invoices) defaultTaxBp(cfg *Config) int {
	return roundHalfUp(cfg.Invoicing.SalesTaxPct * 100)
}

// InvoiceLine is one invoice line, served as part of a full Invoice. amountCents is computed, never accepted.
type InvoiceLine struct {
	ID             string  `json:"id"`
	Description    string  `json:"description"`
	Quantity       float64 `json:"quantity"`
	UnitPriceCents int     `json:"unitPriceCents"`
	DiscountCents  int     `json:"discountCents"`
	QuoteID        string  `json:"quoteId"`
	// Kind is invoiceLineKindItem or invoiceLineKindShipping; computeTotals exempts shipping lines from tax.
	Kind string `json:"kind"`
	// ItemID/OptionName link the line to a gallery item (empty: unlinked); StockTaken is the units a send
	// took off the shelf, set by takeInvoiceStock and read-only to clients.
	ItemID      string `json:"itemId"`
	OptionName  string `json:"optionName"`
	StockTaken  int    `json:"stockTaken"`
	AmountCents int    `json:"amountCents"`
}

// InvoiceSummary is the list endpoint's shape: every Invoice field except lines, payments, and sends.
type InvoiceSummary struct {
	ID                    string `json:"id"`
	Number                string `json:"number"`
	Status                string `json:"status"`
	ClientID              string `json:"clientId"`
	ClientName            string `json:"clientName"`
	ClientEmail           string `json:"clientEmail"`
	ClientBillingAddress  string `json:"clientBillingAddress"`
	ClientShippingAddress string `json:"clientShippingAddress"`
	IssueDate             string `json:"issueDate"`
	DueDate               string `json:"dueDate"`
	TaxBp                 int    `json:"taxBp"`
	DiscountCents         int    `json:"discountCents"`
	Notes                 string `json:"notes"`
	SubtotalCents         int    `json:"subtotalCents"`
	TaxCents              int    `json:"taxCents"`
	TotalCents            int    `json:"totalCents"`
	CreatedAt             string `json:"createdAt"`
	UpdatedAt             string `json:"updatedAt"`
	PaidCents             int    `json:"paidCents"`
	BalanceCents          int    `json:"balanceCents"`
	// CreatedBy/UpdatedBy follow Quote.CreatedBy. UpdatedBy moves on every mutating write (handlePut,
	// handleStatus, and handleSend on draft->sent).
	CreatedBy string `json:"createdBy"`
	UpdatedBy string `json:"updatedBy"`
	// Version is the optimistic-concurrency token: it starts at 1 and every write to the invoice row bumps it.
	// A PUT that carries a different version is a 409 (handlePut).
	Version int `json:"version"`
}

// Invoice is the shape served by GET (single), POST, PUT, and the status endpoint.
// clientId is a soft link and may point at a deleted client: the client* fields are a snapshot frozen at
// creation (or the last clientId change). Go flattens the embedded InvoiceSummary into the same JSON object.
type Invoice struct {
	InvoiceSummary
	Lines    []InvoiceLine `json:"lines"`
	Payments []Payment     `json:"payments"`
	// Sends is the delivery history, oldest first. Only on the full Invoice, like Payments.
	Sends []InvoiceSend `json:"sends"`
	// RequestID and RequestReference name the shop request this invoice was created from ('' when it was not),
	// so the invoice can say "Created from request HLP-1017" and link back. Only on the full Invoice.
	RequestID        string `json:"requestId"`
	RequestReference string `json:"requestReference"`
}

// invoiceColumns lists the 21 invoices columns every read selects, in scanInvoiceSummary's order, so the
// SELECT and the Scan can't drift.
const invoiceColumns = `id, number, status, client_id, client_name, client_email, client_billing_address, client_shipping_address,
	issue_date, due_date, tax_bp, discount_cents, notes, subtotal_cents, tax_cents, total_cents, created_at, updated_at, created_by, updated_by, version`

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanInvoiceSummary scans one invoiceColumns row. extra receives destinations for columns appended after
// invoiceColumns (dashboard.go's paidCents), since database/sql needs every selected column in one Scan.
func scanInvoiceSummary(row rowScanner, extra ...any) (InvoiceSummary, error) {
	var s InvoiceSummary
	dest := append([]any{
		&s.ID, &s.Number, &s.Status, &s.ClientID, &s.ClientName, &s.ClientEmail, &s.ClientBillingAddress, &s.ClientShippingAddress,
		&s.IssueDate, &s.DueDate, &s.TaxBp, &s.DiscountCents, &s.Notes, &s.SubtotalCents, &s.TaxCents, &s.TotalCents, &s.CreatedAt, &s.UpdatedAt,
		&s.CreatedBy, &s.UpdatedBy, &s.Version,
	}, extra...)
	err := row.Scan(dest...)
	return s, err
}

// invoiceLineInput is one line of a POST/PUT request body.
type invoiceLineInput struct {
	Description    string  `json:"description"`
	Quantity       float64 `json:"quantity"`
	UnitPriceCents int     `json:"unitPriceCents"`
	DiscountCents  int     `json:"discountCents"`
	QuoteID        string  `json:"quoteId"`
	// Kind is "item" or "shipping"; absent or empty means "item".
	Kind       string `json:"kind"`
	ItemID     string `json:"itemId"`
	OptionName string `json:"optionName"`
	// StockTaken is accepted so a client can echo a served line back, and always ignored: only
	// takeInvoiceStock writes stock_taken.
	StockTaken int `json:"stockTaken"`
}

// invoiceInput is the POST/PUT body. TaxBp is a pointer so an absent field differs from an explicit 0%.
type invoiceInput struct {
	ClientID      string             `json:"clientId"`
	ClientName    string             `json:"clientName"`
	IssueDate     string             `json:"issueDate"`
	DueDate       string             `json:"dueDate"`
	TaxBp         *int               `json:"taxBp"`
	DiscountCents int                `json:"discountCents"`
	Notes         string             `json:"notes"`
	Lines         []invoiceLineInput `json:"lines"`
	// Version is the invoice version the editor loaded (PUT only). Absent skips the check; a different value
	// is a 409 so a save never silently overwrites another tab's or user's change.
	Version *int `json:"version"`
	// RequestID, on POST only, converts that quote request: the new invoice is linked to it and the request is
	// finalized in the same transaction (see linkRequestToInvoice).
	RequestID string `json:"requestId"`
}

type invoiceStatusInput struct {
	Status string `json:"status"`
}

// validateInvoiceInput trims fields in place and returns one message per problem for the non-client fields.
// Client resolution is resolveClientSnapshot's job since it needs a database lookup.
func validateInvoiceInput(in *invoiceInput) []string {
	in.ClientID = strings.TrimSpace(in.ClientID)
	in.ClientName = strings.TrimSpace(in.ClientName)
	in.Notes = strings.TrimSpace(in.Notes)
	in.IssueDate = strings.TrimSpace(in.IssueDate)
	in.DueDate = strings.TrimSpace(in.DueDate)

	var problems []string
	if len(in.ClientName) > 200 {
		problems = append(problems, "clientName: max 200 characters")
	}
	if len(in.Notes) > 2000 {
		problems = append(problems, "notes: max 2000 characters")
	}
	if in.IssueDate != "" {
		if _, err := time.Parse("2006-01-02", in.IssueDate); err != nil {
			problems = append(problems, "issueDate: must be YYYY-MM-DD")
		}
	}
	if in.DueDate != "" {
		if _, err := time.Parse("2006-01-02", in.DueDate); err != nil {
			problems = append(problems, "dueDate: must be YYYY-MM-DD")
		}
	}
	if in.TaxBp != nil && (*in.TaxBp < 0 || *in.TaxBp > maxSalesTaxPct*100) {
		problems = append(problems, fmt.Sprintf("taxBp: must be 0-%d", maxSalesTaxPct*100))
	}
	if in.DiscountCents < 0 || in.DiscountCents > 100_000_000 {
		problems = append(problems, "discountCents: must be 0-100000000")
	}
	if len(in.Lines) > 100 {
		problems = append(problems, "lines: max 100")
	}
	for i := range in.Lines {
		l := &in.Lines[i]
		l.Description = strings.TrimSpace(l.Description)
		l.QuoteID = strings.TrimSpace(l.QuoteID)
		l.Kind = strings.TrimSpace(l.Kind)
		if l.Kind == "" {
			l.Kind = invoiceLineKindItem
		}
		if l.Kind != invoiceLineKindItem && l.Kind != invoiceLineKindShipping {
			problems = append(problems, fmt.Sprintf("lines[%d].kind: must be item or shipping", i))
		}
		l.ItemID = strings.TrimSpace(l.ItemID)
		l.OptionName = strings.TrimSpace(l.OptionName)
		if l.Description == "" {
			problems = append(problems, fmt.Sprintf("lines[%d].description: required", i))
		} else if len(l.Description) > 500 {
			problems = append(problems, fmt.Sprintf("lines[%d].description: max 500 characters", i))
		}
		if l.Quantity <= 0 || l.Quantity > 100000 {
			problems = append(problems, fmt.Sprintf("lines[%d].quantity: must be > 0 and <= 100000", i))
		}
		if l.UnitPriceCents < 0 || l.UnitPriceCents > 100_000_000 {
			problems = append(problems, fmt.Sprintf("lines[%d].unitPriceCents: must be 0-100000000", i))
		}
		if l.DiscountCents < 0 || l.DiscountCents > 100_000_000 {
			problems = append(problems, fmt.Sprintf("lines[%d].discountCents: must be 0-100000000", i))
		}
	}
	return problems
}

// validateInvoiceLinks checks the lines that carry an itemId: the item must exist, optionName must be empty or
// a valid option summary for its groups ("Color: Red · Size: Large"), and quantity must be a whole number 1-maxItemOrderQty (stock moves in whole units).
// One message per problem; unlinked lines are validateInvoiceInput's business alone. A non-nil error means a
// lookup failed; the caller should 500.
func validateInvoiceLinks(db *sql.DB, lines []invoiceLineInput) ([]string, error) {
	var problems []string
	for i, l := range lines {
		if l.ItemID == "" {
			continue
		}
		var groupsJSON string
		err := db.QueryRow(`SELECT option_groups_json FROM items WHERE id = ?`, l.ItemID).Scan(&groupsJSON)
		if errors.Is(err, sql.ErrNoRows) {
			problems = append(problems, fmt.Sprintf("lines[%d].itemId: unknown item", i))
			continue
		}
		if err != nil {
			return nil, err
		}
		if l.OptionName != "" {
			var groups []ItemOptionGroup
			if err := json.Unmarshal([]byte(groupsJSON), &groups); err != nil {
				return nil, err
			}
			if _, ok := optionSummaryDelta(groups, l.OptionName); !ok {
				problems = append(problems, fmt.Sprintf("lines[%d].optionName: not a selection of the item's options", i))
			}
		}
		if l.Quantity != math.Trunc(l.Quantity) || l.Quantity < 1 || l.Quantity > maxItemOrderQty {
			problems = append(problems, fmt.Sprintf("lines[%d].quantity: must be a whole number 1-%d for an item line", i, maxItemOrderQty))
		}
	}
	return problems, nil
}

// linksValid writes the 400 (or 500) response and returns false when a linked line fails validateInvoiceLinks.
func (iv *invoices) linksValid(w http.ResponseWriter, lines []invoiceLineInput) bool {
	problems, err := validateInvoiceLinks(iv.db, lines)
	if err != nil {
		log.Printf("invoices validate links: %v", err)
		http.Error(w, "invoices unavailable", http.StatusInternalServerError)
		return false
	}
	if len(problems) > 0 {
		writeAPIError(w, http.StatusBadRequest, problems...)
		return false
	}
	return true
}

// takeInvoiceStock runs inside the draft->sent transaction of both handleStatus and handleSend, so stock is
// taken exactly once per invoice, in the same commit as the status change. Each linked line whose item is
// in_stock loses min(stock_qty, quantity), and stock_taken records that actual number; a ready_to_print or
// deleted item takes nothing. Low stock is never an error: a counter sale past the recorded stock floors at
// zero, as gallery finalize does.
func takeInvoiceStock(tx *sql.Tx, invoiceID, by, now string) error {
	type linked struct {
		id, itemID string
		qty        int
	}
	rows, err := tx.Query(`SELECT id, item_id, quantity FROM invoice_lines WHERE invoice_id = ? AND item_id != ''`, invoiceID)
	if err != nil {
		return err
	}
	var lines []linked
	for rows.Next() {
		var l linked
		var qty float64
		if err := rows.Scan(&l.id, &l.itemID, &qty); err != nil {
			rows.Close()
			return err
		}
		l.qty = int(qty)
		lines = append(lines, l)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, l := range lines {
		var stockQty int
		err := tx.QueryRow(`SELECT stock_qty FROM items WHERE id = ? AND stock_mode = ?`, l.itemID, stockModeInStock).Scan(&stockQty)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		taken := min(stockQty, l.qty)
		if taken <= 0 {
			continue
		}
		if _, err := tx.Exec(`UPDATE items SET stock_qty = stock_qty - ?, updated_at = ?, updated_by = ? WHERE id = ?`,
			taken, now, by, l.itemID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE invoice_lines SET stock_taken = ? WHERE id = ?`, taken, l.id); err != nil {
			return err
		}
	}
	return nil
}

// restoreInvoiceStock runs inside the sent->canceled transaction: it puts each line's stock_taken back on
// its item if the item still exists (whatever its stock mode is now), then zeroes stock_taken so a repeat
// can't restore twice.
func restoreInvoiceStock(tx *sql.Tx, invoiceID, by, now string) error {
	if _, err := tx.Exec(`
		UPDATE items SET stock_qty = stock_qty + (
		    SELECT COALESCE(SUM(l.stock_taken), 0) FROM invoice_lines l WHERE l.invoice_id = ? AND l.item_id = items.id),
		  updated_at = ?, updated_by = ?
		WHERE id IN (SELECT item_id FROM invoice_lines WHERE invoice_id = ? AND stock_taken > 0)`,
		invoiceID, now, by, invoiceID); err != nil {
		return err
	}
	_, err := tx.Exec(`UPDATE invoice_lines SET stock_taken = 0 WHERE invoice_id = ?`, invoiceID)
	return err
}

// clientSnapshot is the frozen client identity stored on an invoice.
type clientSnapshot struct {
	ClientID              string
	ClientName            string
	ClientEmail           string
	ClientBillingAddress  string
	ClientShippingAddress string
}

// resolveClientSnapshot freezes a client record's identity fields (422 "clientId: unknown client" if gone),
// or falls back to a freeform clientName for walk-ins and quote conversions (clientId empty: name required).
// A non-nil error means the lookup failed; the caller should 500.
func resolveClientSnapshot(db *sql.DB, clientID, clientName string) (clientSnapshot, []string, error) {
	if clientID != "" {
		var cl Client
		err := db.QueryRow(`
			SELECT id, name, email, phone, billing_address, shipping_address, notes, created_at, updated_at
			FROM clients WHERE id = ?`, clientID,
		).Scan(&cl.ID, &cl.Name, &cl.Email, &cl.Phone, &cl.BillingAddress, &cl.ShippingAddress, &cl.Notes, &cl.CreatedAt, &cl.UpdatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return clientSnapshot{}, []string{"clientId: unknown client"}, nil
		}
		if err != nil {
			return clientSnapshot{}, nil, err
		}
		return clientSnapshot{
			ClientID:              clientID,
			ClientName:            cl.Name,
			ClientEmail:           cl.Email,
			ClientBillingAddress:  cl.BillingAddress,
			ClientShippingAddress: cl.ShippingAddress,
		}, nil, nil
	}
	if clientName == "" {
		return clientSnapshot{}, []string{"clientName: required"}, nil
	}
	return clientSnapshot{ClientName: clientName}, nil, nil
}

// halfUpBias is 0.5 plus a 1e-9 epsilon. The epsilon corrects IEEE 754 ties that land just under .5 (for
// example 1.005 * 100 is 100.49999999999999). Keep the literal identical to halfUpBias in
// web/src/lib/engine/invoice.ts so Go and TypeScript round the same values the same way.
const halfUpBias = 0.500000001

// roundHalfUp is Floor(x + halfUpBias). Every call site passes a non-negative x, so it matches
// half-away-from-zero for the values invoices produce.
func roundHalfUp(x float64) int {
	return int(math.Floor(x + halfUpBias))
}

type invoiceTotalsResult struct {
	LineAmounts   []int
	SubtotalCents int
	TaxCents      int
	TotalCents    int
}

// Invoice line kinds. computeTotals exempts invoiceLineKindShipping lines from tax; the description is free text.
const (
	invoiceLineKindItem     = "item"
	invoiceLineKindShipping = "shipping"
)

// quoteConversionShippingDescription is the description convertQuote.ts's invoiceLinesFromQuote gives the
// Shipping line it adds (keep byte-identical with SHIPPING_LINE_DESCRIPTION). It is display text only; the
// line's kind carries the tax exemption.
const quoteConversionShippingDescription = "Shipping"

// computeTotals is the frozen totals formula, used by every path that computes invoice totals:
//
//	lineAmount        = round(quantity * unitPriceCents) - lineDiscountCents
//	subtotal          = sum(lineAmounts)
//	taxableSubtotal   = sum(lineAmounts of every line whose kind is not "shipping")
//	taxable           = max(0, taxableSubtotal - invoiceDiscountCents)
//	tax               = round(taxable * taxBp / 10000)
//	total             = max(0, subtotal - invoiceDiscountCents) + tax
//
// Tax applies to item revenue only, never shipping; the Shipping line still counts toward subtotal and total.
// quantity is the only float; everything else is integer cents (or basis points).
// Rounding is half-up with a 1e-9 epsilon (halfUpBias), identical in web/src/lib/engine/invoice.ts.
// With no shipping line, taxableSubtotal equals subtotal, so ordinary invoices are unchanged.
func computeTotals(lines []invoiceLineInput, discountCents, taxBp int) invoiceTotalsResult {
	lineAmounts := make([]int, len(lines))
	subtotal := 0
	taxableSubtotal := 0
	for i, l := range lines {
		amt := roundHalfUp(l.Quantity*float64(l.UnitPriceCents)) - l.DiscountCents
		lineAmounts[i] = amt
		subtotal += amt
		if l.Kind != invoiceLineKindShipping {
			taxableSubtotal += amt
		}
	}
	taxableBase := max(taxableSubtotal-discountCents, 0)
	tax := roundHalfUp(float64(taxableBase) * float64(taxBp) / 10000)
	totalBase := max(subtotal-discountCents, 0)
	total := totalBase + tax
	return invoiceTotalsResult{LineAmounts: lineAmounts, SubtotalCents: subtotal, TaxCents: tax, TotalCents: total}
}

// nextInvoiceNumber computes the next per-year sequence number inside the caller's transaction.
// The single DB connection serializes writers, so MAX(number_seq)+1 can't race.
func nextInvoiceNumber(tx *sql.Tx, prefix string, year int) (number string, seq int, err error) {
	var maxSeq sql.NullInt64
	if err = tx.QueryRow(`SELECT MAX(number_seq) FROM invoices WHERE number_year = ?`, year).Scan(&maxSeq); err != nil {
		return "", 0, err
	}
	seq = 1
	if maxSeq.Valid {
		seq = int(maxSeq.Int64) + 1
	}
	number = fmt.Sprintf("%s-%d-%04d", prefix, year, seq)
	return number, seq, nil
}

// normalizeLineKind maps an empty kind (callers that skip validateInvoiceInput, like the demo seeder) to "item".
func normalizeLineKind(kind string) string {
	if kind == "" {
		return invoiceLineKindItem
	}
	return kind
}

// insertInvoiceLines inserts lines with positions from array order. Callers delete prior lines first (PUT).
func insertInvoiceLines(tx *sql.Tx, invoiceID string, lines []invoiceLineInput) error {
	for i, l := range lines {
		if _, err := tx.Exec(`
			INSERT INTO invoice_lines (id, invoice_id, position, description, quantity, unit_price_cents, discount_cents, quote_id, item_id, option_name, kind)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			newID(), invoiceID, i, l.Description, l.Quantity, l.UnitPriceCents, l.DiscountCents, l.QuoteID, l.ItemID, l.OptionName, normalizeLineKind(l.Kind)); err != nil {
			return err
		}
	}
	return nil
}

// load reads one invoice with its lines, recomputing each amountCents (never persisted); invoice totals come
// from the scanned columns and only LineAmounts is consumed from computeTotals. lineInputs carry Kind, so
// the totals would also be right if a caller used them.
func (iv *invoices) load(id string) (*Invoice, bool, error) {
	summary, err := scanInvoiceSummary(iv.db.QueryRow(`
		SELECT `+invoiceColumns+`
		FROM invoices WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	inv := Invoice{InvoiceSummary: summary}

	rows, err := iv.db.Query(`
		SELECT id, description, quantity, unit_price_cents, discount_cents, quote_id, item_id, option_name, stock_taken, kind
		FROM invoice_lines WHERE invoice_id = ? ORDER BY position`, id)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	var lineInputs []invoiceLineInput
	lines := []InvoiceLine{}
	for rows.Next() {
		var ln InvoiceLine
		if err := rows.Scan(&ln.ID, &ln.Description, &ln.Quantity, &ln.UnitPriceCents, &ln.DiscountCents, &ln.QuoteID, &ln.ItemID, &ln.OptionName, &ln.StockTaken, &ln.Kind); err != nil {
			return nil, false, err
		}
		lines = append(lines, ln)
		lineInputs = append(lineInputs, invoiceLineInput{Quantity: ln.Quantity, UnitPriceCents: ln.UnitPriceCents, DiscountCents: ln.DiscountCents, Kind: ln.Kind})
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	// Close before loadPayments/loadInvoiceSends open their own *sql.Rows: the pool has one connection, so
	// two open Rows deadlock. The defer above stays as a no-op for the loop's error returns.
	rows.Close()

	totals := computeTotals(lineInputs, inv.DiscountCents, inv.TaxBp)
	for i := range lines {
		lines[i].AmountCents = totals.LineAmounts[i]
	}
	inv.Lines = lines

	payments, paidCents, err := loadPayments(iv.db, id)
	if err != nil {
		return nil, false, err
	}
	inv.Payments = payments
	inv.PaidCents = paidCents
	inv.BalanceCents = inv.TotalCents - paidCents

	sends, err := loadInvoiceSends(iv.db, id)
	if err != nil {
		return nil, false, err
	}
	inv.Sends = sends

	err = iv.db.QueryRow(`SELECT id, reference FROM quote_requests WHERE invoice_id = ? ORDER BY created_at DESC LIMIT 1`, id).Scan(&inv.RequestID, &inv.RequestReference)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}

	return &inv, true, nil
}

func (iv *invoices) handleList(w http.ResponseWriter, r *http.Request) {
	page, ok, msg := parsePagination(r)
	if !ok {
		writeAPIError(w, http.StatusBadRequest, msg)
		return
	}

	// Read in full and close before the invoices query opens its own *sql.Rows (one connection: deadlock).
	sums, err := paymentSums(iv.db)
	if err != nil {
		log.Printf("invoices payment sums: %v", err)
		http.Error(w, "invoices unavailable", http.StatusInternalServerError)
		return
	}

	rows, err := iv.db.Query(`
		SELECT ` + invoiceColumns + `
		FROM invoices ORDER BY number_year DESC, number_seq DESC`)
	if err != nil {
		log.Printf("invoices query: %v", err)
		http.Error(w, "invoices unavailable", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	// Empty, not nil, so a fresh database serializes "invoices":[].
	list := []InvoiceSummary{}
	for rows.Next() {
		s, err := scanInvoiceSummary(rows)
		if err != nil {
			log.Printf("invoices scan: %v", err)
			http.Error(w, "invoices unavailable", http.StatusInternalServerError)
			return
		}
		s.PaidCents = sums[s.ID]
		s.BalanceCents = s.TotalCents - s.PaidCents
		list = append(list, s)
	}
	if err := rows.Err(); err != nil {
		log.Printf("invoices read: %v", err)
		http.Error(w, "invoices unavailable", http.StatusInternalServerError)
		return
	}

	list = paginate(w, list, page)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Invoices []InvoiceSummary `json:"invoices"`
	}{Invoices: list})
}

func (iv *invoices) handleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	inv, found, err := iv.load(id)
	if err != nil {
		log.Printf("invoices load: %v", err)
		http.Error(w, "invoices unavailable", http.StatusInternalServerError)
		return
	}
	if !found {
		writeAPIError(w, http.StatusNotFound, "invoice not found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(inv)
}

func (iv *invoices) handlePost(w http.ResponseWriter, r *http.Request) {
	cfg := iv.store.Get() // one snapshot for this request
	in, ok := decodeJSONBody[invoiceInput](w, r, invoicesMaxBytes)
	if !ok {
		return
	}
	in.RequestID = strings.TrimSpace(in.RequestID)
	problems := validateInvoiceInput(&in)
	if in.RequestID != "" {
		// The route is gated on "invoices", but converting a request finalizes it, which is the "requests"
		// area. Checked before prepareRequestConversion so its messages (they name the linked invoice) don't
		// reach a caller without the grant.
		ok, err := callerHasPermission(iv.db, r, "requests")
		if err != nil {
			log.Printf("invoices request permission: %v", err)
			http.Error(w, "permission check failed", http.StatusInternalServerError)
			return
		}
		if !ok {
			writeAuthError(w, http.StatusForbidden, "missing permission: requests")
			return
		}
		status, msg, err := prepareRequestConversion(iv.db, &in)
		if err != nil {
			log.Printf("invoices request conversion: %v", err)
			http.Error(w, "invoices unavailable", http.StatusInternalServerError)
			return
		}
		if msg != "" {
			writeAPIError(w, status, msg)
			return
		}
	}
	if !iv.linksValid(w, in.Lines) {
		return
	}

	snap, snapProblems, err := resolveClientSnapshot(iv.db, in.ClientID, in.ClientName)
	if err != nil {
		log.Printf("invoices resolve client: %v", err)
		http.Error(w, "invoices not writable", http.StatusInternalServerError)
		return
	}
	problems = append(problems, snapProblems...)
	if len(problems) > 0 {
		writeAPIError(w, http.StatusUnprocessableEntity, problems...)
		return
	}

	// A conversion (from a saved quote, whose lines carry a quoteId, or from a request) starts with the issue
	// date today and the due date from the default terms, the same dates a draft gets on Mark sent. A manual
	// invoice keeps the dates the editor sent.
	if in.RequestID != "" || slices.ContainsFunc(in.Lines, func(l invoiceLineInput) bool { return l.QuoteID != "" }) {
		var err error
		in.IssueDate, in.DueDate, err = fillSentDates(cfg, in.IssueDate, in.DueDate)
		if err != nil {
			problems = append(problems, "issueDate: must be YYYY-MM-DD")
			writeAPIError(w, http.StatusUnprocessableEntity, problems...)
			return
		}
	}

	// taxBp is the caller's explicit value (a manual invoice, or a quote whose price baked in tax:
	// convertTaxBp sends 0) or the shop default; computeTotals applies the same formula either way.
	taxBp := iv.defaultTaxBp(cfg)
	if in.TaxBp != nil {
		taxBp = *in.TaxBp
	}
	totals := computeTotals(in.Lines, in.DiscountCents, taxBp)

	by := identityFor(r).username
	now := time.Now().UTC().Format(time.RFC3339)
	year := time.Now().UTC().Year()
	id := newID()

	tx, err := iv.db.Begin()
	if err != nil {
		log.Printf("invoices begin: %v", err)
		http.Error(w, "invoices not writable", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	number, seq, err := nextInvoiceNumber(tx, cfg.Invoicing.NumberPrefix, year)
	if err != nil {
		log.Printf("invoices number: %v", err)
		http.Error(w, "invoices not writable", http.StatusInternalServerError)
		return
	}

	_, err = tx.Exec(`
		INSERT INTO invoices (
			id, number, number_year, number_seq, status,
			client_id, client_name, client_email, client_billing_address, client_shipping_address,
			issue_date, due_date, tax_bp, discount_cents, notes,
			subtotal_cents, tax_cents, total_cents, created_at, updated_at, created_by, updated_by
		) VALUES (?, ?, ?, ?, 'draft', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, number, year, seq,
		snap.ClientID, snap.ClientName, snap.ClientEmail, snap.ClientBillingAddress, snap.ClientShippingAddress,
		in.IssueDate, in.DueDate, taxBp, in.DiscountCents, in.Notes,
		totals.SubtotalCents, totals.TaxCents, totals.TotalCents, now, now, by, by)
	if err != nil {
		log.Printf("invoices insert: %v", err)
		http.Error(w, "invoices not writable", http.StatusInternalServerError)
		return
	}

	if err := insertInvoiceLines(tx, id, in.Lines); err != nil {
		log.Printf("invoices insert lines: %v", err)
		http.Error(w, "invoices not writable", http.StatusInternalServerError)
		return
	}

	if in.RequestID != "" {
		if err := linkRequestToInvoice(tx, in.RequestID, id, by, now); err != nil {
			if errors.Is(err, errRequestAlreadyInvoiced) {
				writeAPIError(w, http.StatusConflict, "this request was already converted to an invoice; reload to see it")
				return
			}
			log.Printf("invoices link request: %v", err)
			http.Error(w, "invoices not writable", http.StatusInternalServerError)
			return
		}
	}

	if err := tx.Commit(); err != nil {
		log.Printf("invoices commit: %v", err)
		http.Error(w, "invoices not writable", http.StatusInternalServerError)
		return
	}

	full, _, err := iv.load(id)
	if err != nil {
		log.Printf("invoices load after insert: %v", err)
		http.Error(w, "invoices not writable", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(full)
}

func (iv *invoices) handlePut(w http.ResponseWriter, r *http.Request) {
	cfg := iv.store.Get() // one snapshot for this request
	id := r.PathValue("id")
	existing, found, err := iv.load(id)
	if err != nil {
		log.Printf("invoices load: %v", err)
		http.Error(w, "invoices unavailable", http.StatusInternalServerError)
		return
	}
	if !found {
		writeAPIError(w, http.StatusNotFound, "invoice not found")
		return
	}
	if iv.afterLoad != nil {
		iv.afterLoad()
	}
	if existing.Status != "draft" {
		writeAPIError(w, http.StatusConflict, "only draft invoices can be edited")
		return
	}

	in, ok := decodeJSONBody[invoiceInput](w, r, invoicesMaxBytes)
	if !ok {
		return
	}
	if in.Version != nil && *in.Version != existing.Version {
		writeAPIError(w, http.StatusConflict, invoiceVersionConflictMessage)
		return
	}
	problems := validateInvoiceInput(&in)
	if !iv.linksValid(w, in.Lines) {
		return
	}

	// Client data is re-snapshotted only when clientId itself changes (a different client, or to/from
	// freeform).
	var snap clientSnapshot
	if in.ClientID != existing.ClientID {
		s, snapProblems, err := resolveClientSnapshot(iv.db, in.ClientID, in.ClientName)
		if err != nil {
			log.Printf("invoices resolve client: %v", err)
			http.Error(w, "invoices not writable", http.StatusInternalServerError)
			return
		}
		problems = append(problems, snapProblems...)
		snap = s
	} else if in.ClientID == "" {
		// Freeform (no client record): the name is directly editable and doesn't trigger a re-snapshot.
		if in.ClientName == "" {
			problems = append(problems, "clientName: required")
		}
		snap = clientSnapshot{ClientName: in.ClientName}
	} else {
		snap = clientSnapshot{
			ClientID:              existing.ClientID,
			ClientName:            existing.ClientName,
			ClientEmail:           existing.ClientEmail,
			ClientBillingAddress:  existing.ClientBillingAddress,
			ClientShippingAddress: existing.ClientShippingAddress,
		}
	}

	if len(problems) > 0 {
		writeAPIError(w, http.StatusUnprocessableEntity, problems...)
		return
	}

	taxBp := iv.defaultTaxBp(cfg)
	if in.TaxBp != nil {
		taxBp = *in.TaxBp
	}
	totals := computeTotals(in.Lines, in.DiscountCents, taxBp)
	now := time.Now().UTC().Format(time.RFC3339)

	tx, err := iv.db.Begin()
	if err != nil {
		log.Printf("invoices begin: %v", err)
		http.Error(w, "invoices not writable", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	// A version the editor loaded is part of the WHERE, so a competing write that lands between the check
	// above and this statement still loses.
	versionClause := ""
	args := []any{
		snap.ClientID, snap.ClientName, snap.ClientEmail, snap.ClientBillingAddress, snap.ClientShippingAddress,
		in.IssueDate, in.DueDate, taxBp, in.DiscountCents, in.Notes,
		totals.SubtotalCents, totals.TaxCents, totals.TotalCents, now, identityFor(r).username, id,
	}
	if in.Version != nil {
		versionClause = " AND version = ?"
		args = append(args, *in.Version)
	}
	res, err := tx.Exec(`
		UPDATE invoices SET
			client_id = ?, client_name = ?, client_email = ?, client_billing_address = ?, client_shipping_address = ?,
			issue_date = ?, due_date = ?, tax_bp = ?, discount_cents = ?, notes = ?,
			subtotal_cents = ?, tax_cents = ?, total_cents = ?, updated_at = ?, updated_by = ?, version = version + 1
		WHERE id = ? AND status = 'draft'`+versionClause, args...)
	if err != nil {
		log.Printf("invoices update: %v", err)
		http.Error(w, "invoices not writable", http.StatusInternalServerError)
		return
	}
	// The draft check above ran before this transaction; a send that landed in between left lines that
	// carry stock_taken, which the delete below would drop.
	if n, _ := res.RowsAffected(); n != 1 {
		var status string
		if err := tx.QueryRow(`SELECT status FROM invoices WHERE id = ?`, id).Scan(&status); err == nil && status == "draft" {
			writeAPIError(w, http.StatusConflict, invoiceVersionConflictMessage)
			return
		}
		writeAPIError(w, http.StatusConflict, "only draft invoices can be edited")
		return
	}

	if _, err := tx.Exec(`DELETE FROM invoice_lines WHERE invoice_id = ?`, id); err != nil {
		log.Printf("invoices delete lines: %v", err)
		http.Error(w, "invoices not writable", http.StatusInternalServerError)
		return
	}
	if err := insertInvoiceLines(tx, id, in.Lines); err != nil {
		log.Printf("invoices insert lines: %v", err)
		http.Error(w, "invoices not writable", http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(); err != nil {
		log.Printf("invoices commit: %v", err)
		http.Error(w, "invoices not writable", http.StatusInternalServerError)
		return
	}

	full, _, err := iv.load(id)
	if err != nil {
		log.Printf("invoices load after update: %v", err)
		http.Error(w, "invoices not writable", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(full)
}

func (iv *invoices) handleStatus(w http.ResponseWriter, r *http.Request) {
	cfg := iv.store.Get() // one snapshot for this request
	id := r.PathValue("id")

	in, ok := decodeJSONBody[invoiceStatusInput](w, r, invoicesMaxBytes)
	if !ok {
		return
	}
	status := strings.TrimSpace(in.Status)
	if !invoiceStatuses[status] {
		writeAPIError(w, http.StatusUnprocessableEntity, "status: must be draft, sent, paid, or canceled")
		return
	}

	existing, found, err := iv.load(id)
	if err != nil {
		log.Printf("invoices load: %v", err)
		http.Error(w, "invoices unavailable", http.StatusInternalServerError)
		return
	}
	if !found {
		writeAPIError(w, http.StatusNotFound, "invoice not found")
		return
	}
	if iv.afterLoad != nil {
		iv.afterLoad()
	}

	if !invoiceStatusTransitions[existing.Status][status] {
		writeAPIError(w, http.StatusConflict, fmt.Sprintf("can't change status from %s to %s; reload to see the latest status", existing.Status, status))
		return
	}

	if status == "canceled" && len(existing.Payments) > 0 {
		writeAPIError(w, http.StatusConflict, "this invoice has payments; remove the payments before canceling it")
		return
	}

	issueDate := existing.IssueDate
	dueDate := existing.DueDate
	if existing.Status == "draft" && status == "sent" {
		var err error
		issueDate, dueDate, err = fillSentDates(cfg, issueDate, dueDate)
		if err != nil {
			// issueDate was just set by fillSentDates or validated on create/update, so this can't happen;
			// treat it as a server bug rather than skip the due date.
			log.Printf("invoices: invalid stored issueDate %q: %v", issueDate, err)
			http.Error(w, "invoices not writable", http.StatusInternalServerError)
			return
		}
	}

	by := identityFor(r).username
	now := time.Now().UTC().Format(time.RFC3339)

	// The status update, the balancing payment (manual sent->paid), the manual delivery event (draft->sent),
	// and the stock taken or restored (draft->sent, sent->canceled) share one transaction.
	tx, err := iv.db.Begin()
	if err != nil {
		log.Printf("invoices begin: %v", err)
		http.Error(w, "invoices not writable", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	// Conditional on the status loaded above: two racing requests both pass the transition check on that
	// stale snapshot, and only the first may apply its side effects (stock, balancing payment, send event).
	// Canceling also requires no payments, checked in the same statement so a payment posted after the load
	// above can't slip through.
	noPayments := ""
	if status == "canceled" {
		noPayments = " AND NOT EXISTS (SELECT 1 FROM payments WHERE invoice_id = invoices.id)"
	}
	res, err := tx.Exec(`UPDATE invoices SET status = ?, issue_date = ?, due_date = ?, updated_at = ?, updated_by = ?, version = version + 1 WHERE id = ? AND status = ?`+noPayments,
		status, issueDate, dueDate, now, by, id, existing.Status)
	if err != nil {
		log.Printf("invoices status update: %v", err)
		http.Error(w, "invoices not writable", http.StatusInternalServerError)
		return
	}
	if n, _ := res.RowsAffected(); n != 1 {
		if status == "canceled" {
			var paid int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM payments WHERE invoice_id = ?`, id).Scan(&paid); err == nil && paid > 0 {
				writeAPIError(w, http.StatusConflict, "this invoice has payments; remove the payments before canceling it")
				return
			}
		}
		writeAPIError(w, http.StatusConflict, fmt.Sprintf("can't change status from %s to %s; reload to see the latest status", existing.Status, status))
		return
	}

	// A manual sent->paid auto-creates a balancing payment for the outstanding balance (skipped when <= 0).
	// The balance is read inside this transaction, not from existing.BalanceCents, which a concurrent payment
	// post or delete could make stale. TotalCents can't change concurrently: PUT edits only drafts.
	if existing.Status == "sent" && status == "paid" {
		var paidCents int
		if err := tx.QueryRow(`SELECT COALESCE(SUM(amount_cents), 0) FROM payments WHERE invoice_id = ?`, id).Scan(&paidCents); err != nil {
			log.Printf("invoices paid sum: %v", err)
			http.Error(w, "invoices not writable", http.StatusInternalServerError)
			return
		}
		if balance := existing.TotalCents - paidCents; balance > 0 {
			today := cfg.now().Format("2006-01-02")
			if err := insertPayment(tx, id, today, balance, "other", "marked as paid", "", now, by); err != nil {
				log.Printf("invoices balancing payment: %v", err)
				http.Error(w, "invoices not writable", http.StatusInternalServerError)
				return
			}
		}
	}

	// A manual PATCH from "draft" to "sent" records a channel="manual" delivery event (the owner asserts it
	// went out another way); the paid->sent correction is not a delivery. recipient stays empty (UI shows
	// "-"). handleSend records its own channel="email" event.
	if existing.Status == "draft" && status == "sent" {
		if err := insertInvoiceSend(tx, id, "manual", "", now); err != nil {
			log.Printf("invoices manual send record: %v", err)
			http.Error(w, "invoices not writable", http.StatusInternalServerError)
			return
		}
		if err := takeInvoiceStock(tx, id, by, now); err != nil {
			log.Printf("invoices take stock: %v", err)
			http.Error(w, "invoices not writable", http.StatusInternalServerError)
			return
		}
	}

	// sent->canceled puts back exactly the units draft->sent took.
	if existing.Status == "sent" && status == "canceled" {
		if err := restoreInvoiceStock(tx, id, by, now); err != nil {
			log.Printf("invoices restore stock: %v", err)
			http.Error(w, "invoices not writable", http.StatusInternalServerError)
			return
		}
	}

	// The paid->sent correction removes the sent->paid balancing payment it reverses: only the auto-created
	// row, identified by the exact method/note pair insertPayment wrote ("other" / "marked as paid"). A
	// hand-entered look-alike is left alone (an accepted false negative).
	if existing.Status == "paid" && status == "sent" {
		if _, err := tx.Exec(`DELETE FROM payments WHERE invoice_id = ? AND method = ? AND note = ?`,
			id, "other", "marked as paid"); err != nil {
			log.Printf("invoices balancing payment reversal: %v", err)
			http.Error(w, "invoices not writable", http.StatusInternalServerError)
			return
		}
	}

	if err := tx.Commit(); err != nil {
		log.Printf("invoices commit: %v", err)
		http.Error(w, "invoices not writable", http.StatusInternalServerError)
		return
	}

	full, _, err := iv.load(id)
	if err != nil {
		log.Printf("invoices load after status update: %v", err)
		http.Error(w, "invoices not writable", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(full)
}

// fillSentDates computes the issueDate/dueDate an invoice gets on first reaching "sent" (handleStatus or
// handleSend): issueDate defaults to today in the configured time zone, dueDate to issueDate + cfg.Invoicing.DueDays, each if unset.
func fillSentDates(cfg *Config, issueDate, dueDate string) (string, string, error) {
	if issueDate == "" {
		issueDate = cfg.now().Format("2006-01-02")
	}
	if dueDate == "" {
		base, err := time.Parse("2006-01-02", issueDate)
		if err != nil {
			return issueDate, "", err
		}
		dueDate = base.AddDate(0, 0, cfg.Invoicing.DueDays).Format("2006-01-02")
	}
	return issueDate, dueDate, nil
}

// invoiceSendInput is the POST /api/invoices/{id}/send body; `to` defaults to the frozen client email.
type invoiceSendInput struct {
	To string `json:"to"`
}

// handleSend implements POST /api/invoices/{id}/send: sends synchronously before touching the database, so a
// failed send never records a phantom delivery. 409 without SMTP; 422 with no usable recipient. On success it
// records an invoice_sends row and flips only a "draft" to "sent"; later statuses are never downgraded.
func (iv *invoices) handleSend(w http.ResponseWriter, r *http.Request) {
	cfg := iv.store.Get() // one snapshot for this request
	id := r.PathValue("id")

	existing, found, err := iv.load(id)
	if err != nil {
		log.Printf("invoices load: %v", err)
		http.Error(w, "invoices unavailable", http.StatusInternalServerError)
		return
	}
	if !found {
		writeAPIError(w, http.StatusNotFound, "invoice not found")
		return
	}
	if iv.afterLoad != nil {
		iv.afterLoad()
	}

	in, ok := decodeJSONBody[invoiceSendInput](w, r, invoicesMaxBytes)
	if !ok {
		return
	}

	if cfg.Smtp.Host == "" {
		writeAPIError(w, http.StatusConflict, "email isn't configured; set up SMTP in Settings")
		return
	}

	to := strings.TrimSpace(in.To)
	if to == "" {
		to = existing.ClientEmail
	}
	if to == "" {
		writeAPIError(w, http.StatusUnprocessableEntity, "to: this client has no email on file")
		return
	}
	if !strings.Contains(to, "@") {
		writeAPIError(w, http.StatusUnprocessableEntity, "to: enter a valid email address")
		return
	}

	// Recipient restriction: to must match the invoice's frozen client email (the default) or a client's
	// email on file (an explicit override). Anything else is rejected, so the endpoint can't email third
	// parties through the shop's SMTP relay.
	if !strings.EqualFold(to, existing.ClientEmail) {
		known, err := clientEmailExists(iv.db, to)
		if err != nil {
			log.Printf("invoices send: recipient lookup: %v", err)
			http.Error(w, "invoices unavailable", http.StatusInternalServerError)
			return
		}
		if !known {
			writeAPIError(w, http.StatusUnprocessableEntity, "recipient is not a client on file")
			return
		}
	}

	subject, textBody, htmlBody := renderInvoiceEmail(cfg, existing)
	if err := sendMailWith(cfg, iv.dial, to, subject, textBody, htmlBody); err != nil {
		// Verbatim, so the caller's status line shows the real cause.
		writeAPIError(w, http.StatusBadGateway, err.Error())
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)

	tx, err := iv.db.Begin()
	if err != nil {
		log.Printf("invoices begin: %v", err)
		http.Error(w, "invoices not writable", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	if err := insertInvoiceSend(tx, id, "email", to, now); err != nil {
		log.Printf("invoices send record: %v", err)
		http.Error(w, "invoices not writable", http.StatusInternalServerError)
		return
	}

	if existing.Status == "draft" {
		issueDate, dueDate, err := fillSentDates(cfg, existing.IssueDate, existing.DueDate)
		if err != nil {
			log.Printf("invoices: invalid stored issueDate %q: %v", existing.IssueDate, err)
			http.Error(w, "invoices not writable", http.StatusInternalServerError)
			return
		}
		res, err := tx.Exec(`UPDATE invoices SET status = 'sent', issue_date = ?, due_date = ?, updated_at = ?, updated_by = ?, version = version + 1 WHERE id = ? AND status = 'draft'`,
			issueDate, dueDate, now, identityFor(r).username, id)
		if err != nil {
			log.Printf("invoices status update: %v", err)
			http.Error(w, "invoices not writable", http.StatusInternalServerError)
			return
		}
		// A concurrent send or status change got there first and took the stock; roll this one back whole.
		if n, _ := res.RowsAffected(); n != 1 {
			writeAPIError(w, http.StatusConflict, "can't change status from draft to sent; reload to see the latest status")
			return
		}
		if err := takeInvoiceStock(tx, id, identityFor(r).username, now); err != nil {
			log.Printf("invoices take stock: %v", err)
			http.Error(w, "invoices not writable", http.StatusInternalServerError)
			return
		}
	}

	if err := tx.Commit(); err != nil {
		log.Printf("invoices commit: %v", err)
		http.Error(w, "invoices not writable", http.StatusInternalServerError)
		return
	}

	full, _, err := iv.load(id)
	if err != nil {
		log.Printf("invoices load after send: %v", err)
		http.Error(w, "invoices not writable", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(full)
}

func (iv *invoices) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	existing, found, err := iv.load(id)
	if err != nil {
		log.Printf("invoices load: %v", err)
		http.Error(w, "invoices unavailable", http.StatusInternalServerError)
		return
	}
	if !found {
		writeAPIError(w, http.StatusNotFound, "invoice not found")
		return
	}
	if existing.Status != "draft" {
		writeAPIError(w, http.StatusConflict, "only draft invoices can be deleted; cancel instead")
		return
	}
	// The request that was converted to this draft goes back to "not invoiced" so it can be converted again.
	tx, err := iv.db.Begin()
	if err != nil {
		log.Printf("invoices begin: %v", err)
		http.Error(w, "invoices not writable", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE quote_requests SET invoice_id = '' WHERE invoice_id = ?`, id); err != nil {
		log.Printf("invoices unlink request: %v", err)
		http.Error(w, "invoices not writable", http.StatusInternalServerError)
		return
	}
	if _, err := tx.Exec(`DELETE FROM invoices WHERE id = ?`, id); err != nil {
		log.Printf("invoices delete: %v", err)
		http.Error(w, "invoices not writable", http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		log.Printf("invoices delete commit: %v", err)
		http.Error(w, "invoices not writable", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// paypalHandleRe and currencyCodeRe validate invoicing.paypalMe and currency.code.
// The handle is a bare PayPal.me name (no URL), so it is safe to splice into a link path unescaped.
var (
	paypalHandleRe = regexp.MustCompile(`^[A-Za-z0-9]{1,20}$`)
	currencyCodeRe = regexp.MustCompile(`^[A-Z]{3}$`)
)

// paypalPayLink builds the PayPal.me link for an invoice, or "" when no pay block applies (no handle,
// canceled, or nothing left to pay). With a currency code the open balance is appended as /<amount><CODE>
// (decimals digits, dot, no grouping). Mirrored by web/src/lib/paypal.ts; both run testdata/paypal-link.json.
func paypalPayLink(handle, code string, decimals int, status string, balanceCents int) string {
	if handle == "" || status == "canceled" || balanceCents <= 0 {
		return ""
	}
	link := "https://paypal.me/" + handle
	if code == "" {
		return link
	}
	if decimals < 0 {
		decimals = 0
	}
	scale := math.Pow(10, float64(decimals))
	amount := math.Floor(float64(balanceCents)/100*scale+0.5) / scale
	return link + "/" + strconv.FormatFloat(amount, 'f', decimals, 64) + code
}

// InvoiceSend is one recorded delivery event, served as part of a full Invoice. Sends are append-only.
// The status-flip side effects live in handleSend/handleStatus, which insert rows via insertInvoiceSend.
type InvoiceSend struct {
	ID        string `json:"id"`
	SentAt    string `json:"sentAt"`
	Channel   string `json:"channel"` // "email" | "manual"
	Recipient string `json:"recipient"`
}

// loadInvoiceSends reads every send event for invoiceID, oldest first, for load().
func loadInvoiceSends(db *sql.DB, invoiceID string) ([]InvoiceSend, error) {
	rows, err := db.Query(`
		SELECT id, sent_at, channel, recipient
		FROM invoice_sends WHERE invoice_id = ? ORDER BY sent_at, rowid`, invoiceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Empty, not nil, so a never-sent invoice serializes "sends":[].
	sends := []InvoiceSend{}
	for rows.Next() {
		var s InvoiceSend
		if err := rows.Scan(&s.ID, &s.SentAt, &s.Channel, &s.Recipient); err != nil {
			return nil, err
		}
		sends = append(sends, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sends, nil
}

// insertInvoiceSend inserts one send event row inside the caller's transaction.
// Shared by handleSend (channel "email") and handleStatus (channel "manual", recipient "", draft->sent only).
func insertInvoiceSend(tx *sql.Tx, invoiceID, channel, recipient, sentAt string) error {
	_, err := tx.Exec(`
		INSERT INTO invoice_sends (id, invoice_id, sent_at, channel, recipient)
		VALUES (?, ?, ?, ?, ?)`,
		newID(), invoiceID, sentAt, channel, recipient)
	return err
}
