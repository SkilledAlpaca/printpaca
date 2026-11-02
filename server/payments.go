package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
)

const paymentsMaxBytes = 64 * 1024 // 64 KiB

// Payment is one payment record, served as part of a full Invoice.
// Payments are append-and-delete only: there is no edit endpoint.
type Payment struct {
	ID          string `json:"id"`
	PaidDate    string `json:"paidDate"`
	AmountCents int    `json:"amountCents"`
	Method      string `json:"method"`
	Note        string `json:"note"`
	// Reference points at the external record the money moved through ("Square #4821", "cash").
	// Optional; '' on payments recorded before this field existed.
	Reference string `json:"reference"`
	CreatedAt string `json:"createdAt"`
	// CreatedBy/UpdatedBy follow Quote.CreatedBy. Payments are never edited, so UpdatedBy equals CreatedBy.
	CreatedBy string `json:"createdBy"`
	UpdatedBy string `json:"updatedBy"`
}

// paymentInput is the POST /api/invoices/{id}/payments body.
type paymentInput struct {
	PaidDate    string `json:"paidDate"`
	AmountCents int    `json:"amountCents"`
	Method      string `json:"method"`
	Note        string `json:"note"`
	Reference   string `json:"reference"`
}

// validatePaymentInput trims fields in place and returns one message per problem.
func validatePaymentInput(in *paymentInput) []string {
	in.PaidDate = strings.TrimSpace(in.PaidDate)
	in.Method = strings.TrimSpace(in.Method)
	in.Note = strings.TrimSpace(in.Note)
	in.Reference = strings.TrimSpace(in.Reference)

	var problems []string
	if in.PaidDate == "" {
		problems = append(problems, "paidDate: required")
	} else if _, err := time.Parse("2006-01-02", in.PaidDate); err != nil {
		problems = append(problems, "paidDate: must be YYYY-MM-DD")
	}
	if in.AmountCents < 1 || in.AmountCents > 100_000_000 {
		problems = append(problems, "amountCents: must be 1-100000000")
	}
	if len(in.Method) > 50 {
		problems = append(problems, "method: max 50 characters")
	}
	if len(in.Note) > 500 {
		problems = append(problems, "note: max 500 characters")
	}
	// 200-character cap, matching quotes.reference: an external-record pointer is short by nature.
	if len(in.Reference) > 200 {
		problems = append(problems, "reference: max 200 characters")
	}
	return problems
}

// loadPayments reads an invoice's payments ordered by paid_date then created_at, and returns their sum.
func loadPayments(db *sql.DB, invoiceID string) ([]Payment, int, error) {
	rows, err := db.Query(`
		SELECT id, paid_date, amount_cents, method, note, reference, created_at, created_by, updated_by
		FROM payments WHERE invoice_id = ? ORDER BY paid_date, created_at`, invoiceID)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	// Empty, not nil, so an unpaid invoice serializes "payments":[].
	payments := []Payment{}
	sum := 0
	for rows.Next() {
		var p Payment
		if err := rows.Scan(&p.ID, &p.PaidDate, &p.AmountCents, &p.Method, &p.Note, &p.Reference, &p.CreatedAt, &p.CreatedBy, &p.UpdatedBy); err != nil {
			return nil, 0, err
		}
		payments = append(payments, p)
		sum += p.AmountCents
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return payments, sum, nil
}

// paymentSums returns amount_cents summed per invoice_id; an invoice with no payments is absent from the map.
func paymentSums(db *sql.DB) (map[string]int, error) {
	rows, err := db.Query(`SELECT invoice_id, SUM(amount_cents) FROM payments GROUP BY invoice_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sums := map[string]int{}
	for rows.Next() {
		var id string
		var sum int
		if err := rows.Scan(&id, &sum); err != nil {
			return nil, err
		}
		sums[id] = sum
	}
	return sums, rows.Err()
}

// insertPayment inserts one payment row inside the caller's transaction.
// handleStatus's manual sent->paid balancing payment passes "" for reference.
// by is stamped onto both created_by and updated_by.
func insertPayment(tx *sql.Tx, invoiceID, paidDate string, amountCents int, method, note, reference, createdAt, by string) error {
	_, err := tx.Exec(`
		INSERT INTO payments (id, invoice_id, paid_date, amount_cents, method, note, reference, created_at, created_by, updated_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		newID(), invoiceID, paidDate, amountCents, method, note, reference, createdAt, by, by)
	return err
}

// handlePaymentPost implements POST /api/invoices/{id}/payments, allowed only on a sent invoice.
// The insert and the conditional paid-status flip share one transaction.
func (iv *invoices) handlePaymentPost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	existing, found, err := iv.load(id)
	if err != nil {
		log.Printf("payments load invoice: %v", err)
		http.Error(w, "invoices unavailable", http.StatusInternalServerError)
		return
	}
	if !found {
		writeAPIError(w, http.StatusNotFound, "invoice not found")
		return
	}
	if existing.Status != "sent" {
		writeAPIError(w, http.StatusConflict, "payments can only be recorded on sent invoices")
		return
	}

	in, ok := decodeJSONBody[paymentInput](w, r, paymentsMaxBytes)
	if !ok {
		return
	}
	if problems := validatePaymentInput(&in); len(problems) > 0 {
		writeAPIError(w, http.StatusUnprocessableEntity, problems...)
		return
	}

	by := identityFor(r).username
	now := time.Now().UTC().Format(time.RFC3339)

	tx, err := iv.db.Begin()
	if err != nil {
		log.Printf("payments begin: %v", err)
		http.Error(w, "invoices not writable", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	if err := insertPayment(tx, id, in.PaidDate, in.AmountCents, in.Method, in.Note, in.Reference, now, by); err != nil {
		log.Printf("payments insert: %v", err)
		http.Error(w, "invoices not writable", http.StatusInternalServerError)
		return
	}
	// newPaid is read inside this transaction, not from existing.PaidCents (loaded outside it): a concurrent
	// insert could leave two payments that jointly cover the total each seeing a stale value, so none flips
	// it. tx holds the single pooled connection (SetMaxOpenConns(1)), so this SUM is authoritative.
	var newPaid int
	if err := tx.QueryRow(`SELECT COALESCE(SUM(amount_cents), 0) FROM payments WHERE invoice_id = ?`, id).Scan(&newPaid); err != nil {
		log.Printf("payments sum: %v", err)
		http.Error(w, "invoices not writable", http.StatusInternalServerError)
		return
	}
	// Overpayment is allowed (balance may go negative); the only gate is reaching the total.
	if newPaid >= existing.TotalCents {
		if _, err := tx.Exec(`UPDATE invoices SET status = 'paid', updated_at = ?, updated_by = ? WHERE id = ?`, now, by, id); err != nil {
			log.Printf("payments flip paid: %v", err)
			http.Error(w, "invoices not writable", http.StatusInternalServerError)
			return
		}
	}

	if err := tx.Commit(); err != nil {
		log.Printf("payments commit: %v", err)
		http.Error(w, "invoices not writable", http.StatusInternalServerError)
		return
	}

	full, _, err := iv.load(id)
	if err != nil {
		log.Printf("payments load after insert: %v", err)
		http.Error(w, "invoices not writable", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(full)
}

// handlePaymentDelete implements DELETE /api/invoices/{id}/payments/{paymentId} on sent or paid invoices.
// The delete and the conditional paid->sent flip share one transaction.
// Returns 200 with the full Invoice (not 204): the caller needs the recomputed status and balance.
func (iv *invoices) handlePaymentDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	paymentID := r.PathValue("paymentId")

	existing, found, err := iv.load(id)
	if err != nil {
		log.Printf("payments load invoice: %v", err)
		http.Error(w, "invoices unavailable", http.StatusInternalServerError)
		return
	}
	if !found {
		writeAPIError(w, http.StatusNotFound, "invoice not found")
		return
	}
	if existing.Status != "sent" && existing.Status != "paid" {
		writeAPIError(w, http.StatusConflict, "payments can only be deleted from sent or paid invoices")
		return
	}

	var one int
	err = iv.db.QueryRow(`SELECT 1 FROM payments WHERE id = ? AND invoice_id = ?`, paymentID, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		writeAPIError(w, http.StatusNotFound, "payment not found")
		return
	}
	if err != nil {
		log.Printf("payments read: %v", err)
		http.Error(w, "invoices unavailable", http.StatusInternalServerError)
		return
	}

	by := identityFor(r).username
	now := time.Now().UTC().Format(time.RFC3339)

	tx, err := iv.db.Begin()
	if err != nil {
		log.Printf("payments begin: %v", err)
		http.Error(w, "invoices not writable", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM payments WHERE id = ? AND invoice_id = ?`, paymentID, id); err != nil {
		log.Printf("payments delete: %v", err)
		http.Error(w, "invoices not writable", http.StatusInternalServerError)
		return
	}
	// newPaid is read inside this transaction for the same reason as in handlePaymentPost:
	// a concurrent delete could otherwise leave the invoice "paid" with payments no longer covering it.
	var newPaid int
	if err := tx.QueryRow(`SELECT COALESCE(SUM(amount_cents), 0) FROM payments WHERE invoice_id = ?`, id).Scan(&newPaid); err != nil {
		log.Printf("payments sum: %v", err)
		http.Error(w, "invoices not writable", http.StatusInternalServerError)
		return
	}
	if existing.Status == "paid" && newPaid < existing.TotalCents {
		if _, err := tx.Exec(`UPDATE invoices SET status = 'sent', updated_at = ?, updated_by = ? WHERE id = ?`, now, by, id); err != nil {
			log.Printf("payments flip sent: %v", err)
			http.Error(w, "invoices not writable", http.StatusInternalServerError)
			return
		}
	}

	if err := tx.Commit(); err != nil {
		log.Printf("payments commit: %v", err)
		http.Error(w, "invoices not writable", http.StatusInternalServerError)
		return
	}

	full, _, err := iv.load(id)
	if err != nil {
		log.Printf("payments load after delete: %v", err)
		http.Error(w, "invoices not writable", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(full)
}
