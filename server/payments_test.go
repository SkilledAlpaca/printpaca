package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// postPayment/deletePayment mirror postInvoice/setInvoiceStatus's helper
// shape (invoices_test.go).
func postPayment(t *testing.T, mux *http.ServeMux, invoiceID, body string) *httptest.ResponseRecorder {
	return serveReq(mux, httptest.NewRequest("POST", "/api/invoices/"+invoiceID+"/payments", strings.NewReader(body)))
}

func deletePayment(t *testing.T, mux *http.ServeMux, invoiceID, paymentID string) *httptest.ResponseRecorder {
	return serveReq(mux, httptest.NewRequest("DELETE", "/api/invoices/"+invoiceID+"/payments/"+paymentID, nil))
}

// newSentInvoice creates a draft invoice with one line totaling totalCents
// (no tax, no discount: defaults.yaml's salesTaxPct is 0) and marks it sent,
// the shared starting point for the payment tests below.
func newSentInvoice(t *testing.T, mux *http.ServeMux, totalCents int) Invoice {
	t.Helper()
	body := fmt.Sprintf(`{"clientName":"A","lines":[{"description":"Item","quantity":1,"unitPriceCents":%d}]}`, totalCents)
	rec := postInvoice(t, mux, body)
	inv := mustUnmarshalInvoice(t, rec)
	rec = setInvoiceStatus(t, mux, inv.ID, "sent")
	return mustUnmarshalInvoice(t, rec)
}

// ---------------------------------------------------------------------------
// Recording payments
// ---------------------------------------------------------------------------

func TestRecordPaymentOnSentInvoiceDropsBalance(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	inv := newSentInvoice(t, mux, 10000)

	rec := postPayment(t, mux, inv.ID, `{"paidDate":"2026-01-15","amountCents":4000,"method":"cash","note":"deposit"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d %s", rec.Code, rec.Body.String())
	}
	updated := mustUnmarshalInvoice(t, rec)
	if updated.Status != "sent" {
		t.Errorf("status = %q, want sent", updated.Status)
	}
	if updated.PaidCents != 4000 {
		t.Errorf("paidCents = %d, want 4000", updated.PaidCents)
	}
	if updated.BalanceCents != 6000 {
		t.Errorf("balanceCents = %d, want 6000", updated.BalanceCents)
	}
	if len(updated.Payments) != 1 || updated.Payments[0].AmountCents != 4000 || updated.Payments[0].Method != "cash" || updated.Payments[0].Note != "deposit" {
		t.Fatalf("payments = %+v", updated.Payments)
	}
}

func TestRecordPaymentWithReferenceRoundTrips(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	inv := newSentInvoice(t, mux, 10000)

	rec := postPayment(t, mux, inv.ID, `{"paidDate":"2026-01-15","amountCents":4000,"method":"other","reference":"Square #4821"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d %s", rec.Code, rec.Body.String())
	}
	updated := mustUnmarshalInvoice(t, rec)
	if len(updated.Payments) != 1 || updated.Payments[0].Reference != "Square #4821" {
		t.Fatalf("payments = %+v", updated.Payments)
	}
}

func TestRecordPaymentWithoutReferenceStaysEmpty(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	inv := newSentInvoice(t, mux, 10000)

	rec := postPayment(t, mux, inv.ID, `{"paidDate":"2026-01-15","amountCents":4000}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d %s", rec.Code, rec.Body.String())
	}
	updated := mustUnmarshalInvoice(t, rec)
	if len(updated.Payments) != 1 || updated.Payments[0].Reference != "" {
		t.Fatalf("payments = %+v, want empty reference", updated.Payments)
	}
}

func TestSecondPaymentReachingTotalFlipsToPaid(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	inv := newSentInvoice(t, mux, 10000)

	postPayment(t, mux, inv.ID, `{"paidDate":"2026-01-10","amountCents":6000}`)
	rec := postPayment(t, mux, inv.ID, `{"paidDate":"2026-01-15","amountCents":4000}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d %s", rec.Code, rec.Body.String())
	}
	updated := mustUnmarshalInvoice(t, rec)
	if updated.Status != "paid" {
		t.Errorf("status = %q, want paid", updated.Status)
	}
	if updated.BalanceCents != 0 {
		t.Errorf("balanceCents = %d, want 0", updated.BalanceCents)
	}
}

func TestOverpaymentAllowedAndFlipsToPaid(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	inv := newSentInvoice(t, mux, 10000)

	rec := postPayment(t, mux, inv.ID, `{"paidDate":"2026-01-15","amountCents":15000}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d %s", rec.Code, rec.Body.String())
	}
	updated := mustUnmarshalInvoice(t, rec)
	if updated.Status != "paid" {
		t.Errorf("status = %q, want paid", updated.Status)
	}
	if updated.BalanceCents != -5000 {
		t.Errorf("balanceCents = %d, want -5000 (overpayment)", updated.BalanceCents)
	}
}

func TestPaymentOnNonSentInvoiceIs409(t *testing.T) {
	mux := newTestMux(t, t.TempDir())

	// draft
	rec := postInvoice(t, mux, `{"clientName":"A","lines":[{"description":"X","quantity":1,"unitPriceCents":100}]}`)
	draft := mustUnmarshalInvoice(t, rec)
	rec = postPayment(t, mux, draft.ID, `{"paidDate":"2026-01-15","amountCents":100}`)
	if rec.Code != http.StatusConflict {
		t.Errorf("draft: want 409, got %d %s", rec.Code, rec.Body.String())
	}

	// paid (fully paid via one payment, auto-flipped)
	sent := newSentInvoice(t, mux, 100)
	rec = postPayment(t, mux, sent.ID, `{"paidDate":"2026-01-15","amountCents":100}`)
	paid := mustUnmarshalInvoice(t, rec)
	if paid.Status != "paid" {
		t.Fatalf("setup: want paid, got %q", paid.Status)
	}
	rec = postPayment(t, mux, paid.ID, `{"paidDate":"2026-01-16","amountCents":50}`)
	if rec.Code != http.StatusConflict {
		t.Errorf("paid: want 409, got %d %s", rec.Code, rec.Body.String())
	}

	// canceled
	sent2 := newSentInvoice(t, mux, 100)
	rec = setInvoiceStatus(t, mux, sent2.ID, "canceled")
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel: %d %s", rec.Code, rec.Body.String())
	}
	rec = postPayment(t, mux, sent2.ID, `{"paidDate":"2026-01-16","amountCents":50}`)
	if rec.Code != http.StatusConflict {
		t.Errorf("canceled: want 409, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestPaymentValidation(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"missing date", `{"amountCents":100}`, "paidDate: required"},
		{"bad date", `{"paidDate":"01/15/2026","amountCents":100}`, "paidDate: must be YYYY-MM-DD"},
		{"zero amount", `{"paidDate":"2026-01-15","amountCents":0}`, "amountCents: must be 1-100000000"},
		{"negative amount", `{"paidDate":"2026-01-15","amountCents":-5}`, "amountCents: must be 1-100000000"},
		{"51-char method", fmt.Sprintf(`{"paidDate":"2026-01-15","amountCents":100,"method":%q}`, strings.Repeat("x", 51)), "method: max 50 characters"},
		{"201-char reference", fmt.Sprintf(`{"paidDate":"2026-01-15","amountCents":100,"reference":%q}`, strings.Repeat("x", 201)), "reference: max 200 characters"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux := newTestMux(t, t.TempDir())
			inv := newSentInvoice(t, mux, 10000)
			rec := postPayment(t, mux, inv.ID, tc.body)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("want 422, got %d %s", rec.Code, rec.Body.String())
			}
			var errBody apiErrorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &errBody); err != nil {
				t.Fatalf("unmarshal errors: %v", err)
			}
			if !containsStr(errBody.Errors, tc.want) {
				t.Fatalf("errors = %v, want to contain %q", errBody.Errors, tc.want)
			}
		})
	}
}

// Paid-total decisions must be read inside the transaction, not from a pre-transaction load(), so concurrent handlers can't act on a stale PaidCents.

// Two concurrent handlePaymentPost calls, each below the total but covering it together (double-click, two tabs). A pre-transaction read of
// PaidCents=0 let neither reach the total; re-reading via SUM inside the transaction means whichever commits second sees both and flips to paid.
func TestConcurrentPaymentsJointlyCoveringTotalFlipToPaid(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	inv := newSentInvoice(t, mux, 10000)

	var wg sync.WaitGroup
	codes := make([]int, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		codes[0] = postPayment(t, mux, inv.ID, `{"paidDate":"2026-01-15","amountCents":5000}`).Code
	}()
	go func() {
		defer wg.Done()
		codes[1] = postPayment(t, mux, inv.ID, `{"paidDate":"2026-01-16","amountCents":5000}`).Code
	}()
	wg.Wait()

	for i, c := range codes {
		if c != http.StatusCreated {
			t.Fatalf("payment %d: want 201, got %d", i, c)
		}
	}

	final := mustUnmarshalInvoice(t, getInvoice(t, mux, inv.ID))
	if final.Status != "paid" {
		t.Fatalf("status = %q, want paid", final.Status)
	}
	if final.PaidCents != 10000 {
		t.Errorf("paidCents = %d, want 10000", final.PaidCents)
	}
	if final.BalanceCents != 0 {
		t.Errorf("balanceCents = %d, want 0", final.BalanceCents)
	}
	if len(final.Payments) != 2 {
		t.Errorf("payments = %+v, want 2", final.Payments)
	}
}

// A paid invoice with payments 600+600+9800 (total 10000) and two concurrent deletes of the 600-cent payments. Each deletion alone leaves
// 10400 >= total, only both together drop below (9800). The in-transaction SUM re-read makes the second commit see both and flip to sent.
func TestConcurrentDeletesLeavingPaidBelowTotalFlipToSent(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	inv := newSentInvoice(t, mux, 10000)

	postPayment(t, mux, inv.ID, `{"paidDate":"2026-01-01","amountCents":600}`)
	postPayment(t, mux, inv.ID, `{"paidDate":"2026-01-02","amountCents":600}`)
	rec := postPayment(t, mux, inv.ID, `{"paidDate":"2026-01-03","amountCents":9800}`)
	paid := mustUnmarshalInvoice(t, rec)
	if paid.Status != "paid" {
		t.Fatalf("setup: want paid, got %q", paid.Status)
	}
	if len(paid.Payments) != 3 {
		t.Fatalf("setup: want 3 payments, got %+v", paid.Payments)
	}
	idA := paid.Payments[0].ID // 600, 2026-01-01
	idB := paid.Payments[1].ID // 600, 2026-01-02

	var wg sync.WaitGroup
	codes := make([]int, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		codes[0] = deletePayment(t, mux, inv.ID, idA).Code
	}()
	go func() {
		defer wg.Done()
		codes[1] = deletePayment(t, mux, inv.ID, idB).Code
	}()
	wg.Wait()

	for i, c := range codes {
		if c != http.StatusOK {
			t.Fatalf("delete %d: want 200, got %d", i, c)
		}
	}

	final := mustUnmarshalInvoice(t, getInvoice(t, mux, inv.ID))
	if final.Status != "sent" {
		t.Errorf("status = %q, want sent (payments no longer cover total)", final.Status)
	}
	if final.PaidCents != 9800 {
		t.Errorf("paidCents = %d, want 9800", final.PaidCents)
	}
	if final.BalanceCents != 200 {
		t.Errorf("balanceCents = %d, want 200", final.BalanceCents)
	}
}

// ---------------------------------------------------------------------------
// Deleting payments
// ---------------------------------------------------------------------------

func TestDeletePaymentFlipsPaidBackToSent(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	inv := newSentInvoice(t, mux, 10000)
	rec := postPayment(t, mux, inv.ID, `{"paidDate":"2026-01-15","amountCents":10000}`)
	paid := mustUnmarshalInvoice(t, rec)
	if paid.Status != "paid" {
		t.Fatalf("setup: want paid, got %q", paid.Status)
	}
	paymentID := paid.Payments[0].ID

	rec = deletePayment(t, mux, inv.ID, paymentID)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d %s", rec.Code, rec.Body.String())
	}
	after := mustUnmarshalInvoice(t, rec)
	if after.Status != "sent" {
		t.Errorf("status = %q, want sent", after.Status)
	}
	if after.PaidCents != 0 || after.BalanceCents != 10000 {
		t.Errorf("paidCents/balanceCents = %d/%d, want 0/10000", after.PaidCents, after.BalanceCents)
	}
	if len(after.Payments) != 0 {
		t.Errorf("payments = %+v, want empty", after.Payments)
	}
}

func TestDeletePaymentPartialLeavesSentStatus(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	inv := newSentInvoice(t, mux, 10000)
	rec := postPayment(t, mux, inv.ID, `{"paidDate":"2026-01-15","amountCents":4000}`)
	afterFirst := mustUnmarshalInvoice(t, rec)
	paymentID := afterFirst.Payments[0].ID

	rec = deletePayment(t, mux, inv.ID, paymentID)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d %s", rec.Code, rec.Body.String())
	}
	after := mustUnmarshalInvoice(t, rec)
	if after.Status != "sent" {
		t.Errorf("status = %q, want sent (unchanged)", after.Status)
	}
}

func TestDeleteUnknownPaymentIs404(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	inv := newSentInvoice(t, mux, 10000)

	rec := deletePayment(t, mux, inv.ID, "does-not-exist")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown payment: want 404, got %d %s", rec.Code, rec.Body.String())
	}

	rec = deletePayment(t, mux, "does-not-exist", "also-missing")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown invoice: want 404, got %d %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Manual status endpoint: balancing payment
// ---------------------------------------------------------------------------

func TestManualMarkPaidCreatesBalancingPayment(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	inv := newSentInvoice(t, mux, 10000)
	postPayment(t, mux, inv.ID, `{"paidDate":"2026-01-10","amountCents":3000}`)

	rec := setInvoiceStatus(t, mux, inv.ID, "paid")
	if rec.Code != http.StatusOK {
		t.Fatalf("mark paid: %d %s", rec.Code, rec.Body.String())
	}
	updated := mustUnmarshalInvoice(t, rec)
	if updated.Status != "paid" {
		t.Fatalf("status = %q, want paid", updated.Status)
	}
	if updated.BalanceCents != 0 {
		t.Errorf("balanceCents = %d, want 0", updated.BalanceCents)
	}
	if len(updated.Payments) != 2 {
		t.Fatalf("payments = %+v, want 2 (original + balancing)", updated.Payments)
	}
	balancing := updated.Payments[len(updated.Payments)-1]
	if balancing.AmountCents != 7000 {
		t.Errorf("balancing amountCents = %d, want 7000", balancing.AmountCents)
	}
	if balancing.Note != "marked as paid" {
		t.Errorf("balancing note = %q, want %q", balancing.Note, "marked as paid")
	}
	if balancing.Method != "other" {
		t.Errorf("balancing method = %q, want other", balancing.Method)
	}
	wantDate := time.Now().UTC().Format("2006-01-02")
	if balancing.PaidDate != wantDate {
		t.Errorf("balancing paidDate = %q, want %q", balancing.PaidDate, wantDate)
	}
}

func TestManualMarkPaidSkipsBalancingPaymentWhenAlreadyCovered(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	inv := newSentInvoice(t, mux, 10000)
	rec := postPayment(t, mux, inv.ID, `{"paidDate":"2026-01-10","amountCents":10000}`)
	paid := mustUnmarshalInvoice(t, rec)
	if paid.Status != "paid" {
		t.Fatalf("setup: want paid (auto-flip), got %q", paid.Status)
	}

	// Correct back to sent, then mark paid manually again: balance is
	// already 0, so no second balancing payment should appear.
	rec = setInvoiceStatus(t, mux, inv.ID, "sent")
	if rec.Code != http.StatusOK {
		t.Fatalf("correction to sent: %d %s", rec.Code, rec.Body.String())
	}
	rec = setInvoiceStatus(t, mux, inv.ID, "paid")
	if rec.Code != http.StatusOK {
		t.Fatalf("mark paid: %d %s", rec.Code, rec.Body.String())
	}
	final := mustUnmarshalInvoice(t, rec)
	if len(final.Payments) != 1 {
		t.Fatalf("payments = %+v, want still just 1 (no balancing payment added)", final.Payments)
	}
}

// Attribution (identity.go): a payment is append-and-delete only, so insertPayment stamps created_by and updated_by identically from the acting
// identity; the invoices row it mutates (auto paid/sent flip, balancing payment) moves its updated_by from the same identity.

func TestPaymentPostStampsCreatedByAndUpdatedByAndFlipsInvoiceUpdatedBy(t *testing.T) {
	mux, db := newTestMuxWithDB(t, t.TempDir())
	if _, _, err := createUser(db, "flo", "password123", []string{"invoices"}, ""); err != nil {
		t.Fatalf("createUser flo: %v", err)
	}
	inv := newSentInvoice(t, mux, 10000)

	req := withCtxIdentity(httptest.NewRequest("POST", "/api/invoices/"+inv.ID+"/payments", strings.NewReader(`{"paidDate":"2026-01-10","amountCents":10000}`)), identity{username: "flo"})
	rec := serveReq(mux, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("post payment: status = %d, body %s", rec.Code, rec.Body.String())
	}
	full := mustUnmarshalInvoice(t, rec)
	if full.Status != "paid" {
		t.Fatalf("setup: want auto-flip to paid, got %q", full.Status)
	}
	if len(full.Payments) != 1 {
		t.Fatalf("payments = %+v, want 1", full.Payments)
	}
	pay := full.Payments[0]
	if pay.CreatedBy != "flo" || pay.UpdatedBy != "flo" {
		t.Fatalf("payment createdBy=%q updatedBy=%q, want both %q", pay.CreatedBy, pay.UpdatedBy, "flo")
	}
	if full.UpdatedBy != "flo" {
		t.Fatalf("invoice updatedBy after auto-flip = %q, want %q", full.UpdatedBy, "flo")
	}
}

func TestPaymentDeleteFlipsInvoiceUpdatedByToDeletingIdentity(t *testing.T) {
	mux, db := newTestMuxWithDB(t, t.TempDir())
	if _, _, err := createUser(db, "flo", "password123", []string{"invoices"}, ""); err != nil {
		t.Fatalf("createUser flo: %v", err)
	}
	if _, _, err := createUser(db, "gus", "password123", []string{"invoices"}, ""); err != nil {
		t.Fatalf("createUser gus: %v", err)
	}
	inv := newSentInvoice(t, mux, 10000)

	postReq := withCtxIdentity(httptest.NewRequest("POST", "/api/invoices/"+inv.ID+"/payments", strings.NewReader(`{"paidDate":"2026-01-10","amountCents":10000}`)), identity{username: "flo"})
	postRec := serveReq(mux, postReq)
	full := mustUnmarshalInvoice(t, postRec)
	if full.Status != "paid" || len(full.Payments) != 1 {
		t.Fatalf("setup: want one payment and status paid, got %+v", full)
	}

	delReq := withCtxIdentity(httptest.NewRequest("DELETE", "/api/invoices/"+inv.ID+"/payments/"+full.Payments[0].ID, nil), identity{username: "gus"})
	delRec := serveReq(mux, delReq)
	if delRec.Code != http.StatusOK {
		t.Fatalf("delete payment: status = %d, body %s", delRec.Code, delRec.Body.String())
	}
	after := mustUnmarshalInvoice(t, delRec)
	if after.Status != "sent" {
		t.Fatalf("want flip back to sent, got %q", after.Status)
	}
	if after.UpdatedBy != "gus" {
		t.Fatalf("invoice updatedBy after delete-flip = %q, want %q", after.UpdatedBy, "gus")
	}
}

func TestManualMarkPaidBalancingPaymentStampsActingIdentity(t *testing.T) {
	mux, db := newTestMuxWithDB(t, t.TempDir())
	if _, _, err := createUser(db, "mia", "password123", []string{"invoices"}, ""); err != nil {
		t.Fatalf("createUser mia: %v", err)
	}
	inv := newSentInvoice(t, mux, 10000)

	req := withCtxIdentity(httptest.NewRequest("POST", "/api/invoices/"+inv.ID+"/status", strings.NewReader(`{"status":"paid"}`)), identity{username: "mia"})
	rec := serveReq(mux, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("manual mark paid: status = %d, body %s", rec.Code, rec.Body.String())
	}
	paid := mustUnmarshalInvoice(t, rec)
	if len(paid.Payments) != 1 {
		t.Fatalf("payments = %+v, want 1 balancing payment", paid.Payments)
	}
	balancing := paid.Payments[0]
	if balancing.CreatedBy != "mia" || balancing.UpdatedBy != "mia" {
		t.Fatalf("balancing payment createdBy=%q updatedBy=%q, want both %q", balancing.CreatedBy, balancing.UpdatedBy, "mia")
	}
	if paid.UpdatedBy != "mia" {
		t.Fatalf("invoice updatedBy = %q, want %q", paid.UpdatedBy, "mia")
	}
}

// Canceling an invoice that has payments is a 409 that names the fix; the invoice stays sent.
func TestCancelInvoiceWithPaymentsIs409(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	inv := newSentInvoice(t, mux, 10000)
	if rec := postPayment(t, mux, inv.ID, `{"paidDate":"2026-01-15","amountCents":2500}`); rec.Code != http.StatusCreated {
		t.Fatalf("payment: %d %s", rec.Code, rec.Body.String())
	}
	rec := setInvoiceStatus(t, mux, inv.ID, "canceled")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "remove the payments") {
		t.Fatalf("want 409 mentioning removing payments, got %d %s", rec.Code, rec.Body.String())
	}
	got := mustUnmarshalInvoice(t, getInvoice(t, mux, inv.ID))
	if got.Status != "sent" {
		t.Errorf("status = %q, want sent", got.Status)
	}
	// Removing the payment unblocks the cancel.
	if rec := deletePayment(t, mux, inv.ID, got.Payments[0].ID); rec.Code != http.StatusOK {
		t.Fatalf("delete payment: %d %s", rec.Code, rec.Body.String())
	}
	if rec := setInvoiceStatus(t, mux, inv.ID, "canceled"); rec.Code != http.StatusOK {
		t.Fatalf("cancel after removing payments: %d %s", rec.Code, rec.Body.String())
	}
}

// A canceled invoice has no payments to delete: the delete is a 409, like a draft.
func TestDeletePaymentOnCanceledInvoiceIs409(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	inv := newSentInvoice(t, mux, 10000)
	if rec := setInvoiceStatus(t, mux, inv.ID, "canceled"); rec.Code != http.StatusOK {
		t.Fatalf("cancel: %d %s", rec.Code, rec.Body.String())
	}
	if rec := deletePayment(t, mux, inv.ID, "p1"); rec.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d %s", rec.Code, rec.Body.String())
	}
}

// Deleting a payment from a draft invoice stays a 409.
func TestDeletePaymentOnDraftInvoiceIs409(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	rec := postInvoice(t, mux, `{"clientName":"A","lines":[{"description":"X","quantity":1,"unitPriceCents":100}]}`)
	draft := mustUnmarshalInvoice(t, rec)
	if rec := deletePayment(t, mux, draft.ID, "nope"); rec.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d", rec.Code)
	}
}
