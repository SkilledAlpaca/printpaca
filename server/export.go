package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// This file implements GET /api/export, a manual human-readable backup of every record table.
// Export only, deliberately: there is no import endpoint.
// config.yaml and the SQLite file remain the authoritative restore path.

// exportRequest is a quote request's metadata only (id, status, name, email, createdAt), not QuoteRequest.
// The stored G-code can run to tens of MB, so it is never bundled; GET /api/requests/{id}/gcode serves it.
type exportRequest struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	CreatedAt string `json:"createdAt"`
	// UpdatedBy: quote_requests has no created_by column, so this is the only attribution field.
	UpdatedBy string `json:"updatedBy"`
}

// exportFormatVersion is the current exportPayload layout.
const exportFormatVersion = 1

// exportPayload is the JSON document GET /api/export serves.
// Items carries every item row, public or not, in the full Item shape (export is admin-only).
// Image bytes are never embedded; they stay reachable at /item-images/{file}.
// FormatVersion identifies the document layout (exportFormatVersion); bump it when a field changes shape.
type exportPayload struct {
	FormatVersion int             `json:"formatVersion"`
	ExportedAt    string          `json:"exportedAt"`
	AppVersion    string          `json:"appVersion"`
	Config        *Config         `json:"config"`
	Clients       []Client        `json:"clients"`
	Invoices      []Invoice       `json:"invoices"`
	Quotes        []Quote         `json:"quotes"`
	Requests      []exportRequest `json:"requests"`
	Items         []Item          `json:"items"`
}

// exportClientsTx reads every client, in handleList's order, inside the caller's transaction.
func exportClientsTx(tx *sql.Tx) ([]Client, error) {
	rows, err := tx.Query(`
		SELECT id, name, email, phone, billing_address, shipping_address, notes, created_at, updated_at, created_by, updated_by
		FROM clients ORDER BY name COLLATE NOCASE, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []Client{}
	for rows.Next() {
		var cl Client
		if err := rows.Scan(&cl.ID, &cl.Name, &cl.Email, &cl.Phone, &cl.BillingAddress, &cl.ShippingAddress, &cl.Notes, &cl.CreatedAt, &cl.UpdatedAt, &cl.CreatedBy, &cl.UpdatedBy); err != nil {
			return nil, err
		}
		list = append(list, cl)
	}
	return list, rows.Err()
}

// attachInvoiceDetailTx fills inv's lines, payments, balances, and sends as load() does, in the caller's tx.
// Each sub-query is drained and closed before the next opens: one pooled connection, so open Rows deadlock.
// Like load(), only totals.LineAmounts is consumed; subtotal, tax, and total stay as scanned.
func attachInvoiceDetailTx(tx *sql.Tx, inv *Invoice) error {
	lrows, err := tx.Query(`
		SELECT id, description, quantity, unit_price_cents, discount_cents, quote_id, item_id, option_name, stock_taken, kind
		FROM invoice_lines WHERE invoice_id = ? ORDER BY position`, inv.ID)
	if err != nil {
		return err
	}
	var lineInputs []invoiceLineInput
	lines := []InvoiceLine{}
	for lrows.Next() {
		var ln InvoiceLine
		if err := lrows.Scan(&ln.ID, &ln.Description, &ln.Quantity, &ln.UnitPriceCents, &ln.DiscountCents, &ln.QuoteID, &ln.ItemID, &ln.OptionName, &ln.StockTaken, &ln.Kind); err != nil {
			lrows.Close()
			return err
		}
		lines = append(lines, ln)
		lineInputs = append(lineInputs, invoiceLineInput{Quantity: ln.Quantity, UnitPriceCents: ln.UnitPriceCents, DiscountCents: ln.DiscountCents, Kind: ln.Kind})
	}
	if err := lrows.Err(); err != nil {
		lrows.Close()
		return err
	}
	lrows.Close()

	totals := computeTotals(lineInputs, inv.DiscountCents, inv.TaxBp)
	for i := range lines {
		lines[i].AmountCents = totals.LineAmounts[i]
	}
	inv.Lines = lines

	prows, err := tx.Query(`
		SELECT id, paid_date, amount_cents, method, note, reference, created_at, created_by, updated_by
		FROM payments WHERE invoice_id = ? ORDER BY paid_date, created_at`, inv.ID)
	if err != nil {
		return err
	}
	payments := []Payment{}
	paidCents := 0
	for prows.Next() {
		var p Payment
		if err := prows.Scan(&p.ID, &p.PaidDate, &p.AmountCents, &p.Method, &p.Note, &p.Reference, &p.CreatedAt, &p.CreatedBy, &p.UpdatedBy); err != nil {
			prows.Close()
			return err
		}
		payments = append(payments, p)
		paidCents += p.AmountCents
	}
	if err := prows.Err(); err != nil {
		prows.Close()
		return err
	}
	prows.Close()
	inv.Payments = payments
	inv.PaidCents = paidCents
	inv.BalanceCents = inv.TotalCents - paidCents

	srows, err := tx.Query(`
		SELECT id, sent_at, channel, recipient
		FROM invoice_sends WHERE invoice_id = ? ORDER BY sent_at, rowid`, inv.ID)
	if err != nil {
		return err
	}
	defer srows.Close()
	sends := []InvoiceSend{}
	for srows.Next() {
		var s InvoiceSend
		if err := srows.Scan(&s.ID, &s.SentAt, &s.Channel, &s.Recipient); err != nil {
			return err
		}
		sends = append(sends, s)
	}
	if err := srows.Err(); err != nil {
		return err
	}
	inv.Sends = sends
	return nil
}

// exportInvoicesTx reads every invoice with lines, payments, and sends attached, in the caller's transaction.
// load() and its helpers take a *sql.DB, so the queries are duplicated in attachInvoiceDetailTx:
// a snapshot needs every read in one transaction, which also stops an interleaved write changing the answer.
func exportInvoicesTx(tx *sql.Tx) ([]Invoice, error) {
	rows, err := tx.Query(`
		SELECT ` + invoiceColumns + `
		FROM invoices ORDER BY number_year DESC, number_seq DESC`)
	if err != nil {
		return nil, err
	}
	list := []Invoice{}
	for rows.Next() {
		summary, err := scanInvoiceSummary(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, Invoice{InvoiceSummary: summary})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	for i := range list {
		if err := attachInvoiceDetailTx(tx, &list[i]); err != nil {
			return nil, err
		}
	}
	return list, nil
}

// exportQuotesTx reads every quote in GET /api/quotes order (newest saved_at first), in the caller's tx.
func exportQuotesTx(tx *sql.Tx) ([]Quote, error) {
	rows, err := tx.Query(`SELECT ` + quoteColumns + ` FROM quotes ORDER BY saved_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []Quote{}
	for rows.Next() {
		one, err := scanQuote(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, one)
	}
	return list, rows.Err()
}

// exportRequestsTx reads request metadata in handleList's order, leaving the gcode_* columns out entirely.
func exportRequestsTx(tx *sql.Tx) ([]exportRequest, error) {
	rows, err := tx.Query(`
		SELECT id, status, name, email, created_at, updated_by
		FROM quote_requests ORDER BY created_at DESC, rowid DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []exportRequest{}
	for rows.Next() {
		var req exportRequest
		if err := rows.Scan(&req.ID, &req.Status, &req.Name, &req.Email, &req.CreatedAt, &req.UpdatedBy); err != nil {
			return nil, err
		}
		list = append(list, req)
	}
	return list, rows.Err()
}

// exportItemsTx reads every item, public or not, in handleList's order, in the caller's transaction.
func exportItemsTx(tx *sql.Tx) ([]Item, error) {
	rows, err := tx.Query(`SELECT ` + itemColumns + ` FROM items ORDER BY sort_order, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []Item{}
	for rows.Next() {
		one, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, one)
	}
	return list, rows.Err()
}

// handleExport implements GET /api/export: one JSON document of every record table plus the effective config.
// Config never carries the integrations block or any secret (the SMTP password lives only in an env var).
// All reads share one transaction for a consistent snapshot; it is always rolled back since nothing writes.
func handleExport(store *ConfigStore, db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := store.Get() // one snapshot for this request

		tx, err := db.Begin()
		if err != nil {
			log.Printf("export begin: %v", err)
			http.Error(w, "export unavailable", http.StatusInternalServerError)
			return
		}
		defer tx.Rollback()

		clientsList, err := exportClientsTx(tx)
		if err != nil {
			log.Printf("export clients: %v", err)
			http.Error(w, "export unavailable", http.StatusInternalServerError)
			return
		}
		invoicesList, err := exportInvoicesTx(tx)
		if err != nil {
			log.Printf("export invoices: %v", err)
			http.Error(w, "export unavailable", http.StatusInternalServerError)
			return
		}
		quotesList, err := exportQuotesTx(tx)
		if err != nil {
			log.Printf("export quotes: %v", err)
			http.Error(w, "export unavailable", http.StatusInternalServerError)
			return
		}
		requestsList, err := exportRequestsTx(tx)
		if err != nil {
			log.Printf("export requests: %v", err)
			http.Error(w, "export unavailable", http.StatusInternalServerError)
			return
		}
		itemsList, err := exportItemsTx(tx)
		if err != nil {
			log.Printf("export items: %v", err)
			http.Error(w, "export unavailable", http.StatusInternalServerError)
			return
		}

		now := time.Now().UTC()
		payload := exportPayload{
			FormatVersion: exportFormatVersion,
			ExportedAt:    now.Format(time.RFC3339),
			AppVersion:    version, // main.go's build-time version (see handleHealthz)
			Config:        cfg,
			Clients:       clientsList,
			Invoices:      invoicesList,
			Quotes:        quotesList,
			Requests:      requestsList,
			Items:         itemsList,
		}

		filename := fmt.Sprintf("printpaca-export-%s.json", now.Format("2006-01-02"))
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			log.Printf("export encode: %v", err)
		}
	}
}
