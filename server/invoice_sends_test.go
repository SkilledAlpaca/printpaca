package main

import (
	"testing"
)

// TestInvoiceSendsCascadeOnInvoiceDelete pins the ON DELETE CASCADE
// relationship: deleting the parent invoice must not orphan its send rows,
// mirroring payments' relationship to invoices.
func TestInvoiceSendsCascadeOnInvoiceDelete(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)

	if _, err := db.Exec(`
		INSERT INTO invoices (id, number, number_year, number_seq, status, client_name, created_at, updated_at)
		VALUES ('inv-1', 'INV-2026-0001', 2026, 1, 'draft', 'Walk-in', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := insertInvoiceSend(tx, "inv-1", "email", "client@example.com", "2026-01-02T00:00:00Z"); err != nil {
		t.Fatalf("insertInvoiceSend: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	sends, err := loadInvoiceSends(db, "inv-1")
	if err != nil {
		t.Fatalf("loadInvoiceSends: %v", err)
	}
	if len(sends) != 1 || sends[0].Channel != "email" || sends[0].Recipient != "client@example.com" {
		t.Fatalf("sends = %+v, want one email send to client@example.com", sends)
	}

	if _, err := db.Exec(`DELETE FROM invoices WHERE id = 'inv-1'`); err != nil {
		t.Fatalf("delete invoice: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM invoice_sends WHERE invoice_id = 'inv-1'`).Scan(&count); err != nil {
		t.Fatalf("count orphan sends: %v", err)
	}
	if count != 0 {
		t.Fatalf("invoice_sends rows survived invoice delete: %d, want 0 (cascade)", count)
	}
}

// TestLoadInvoiceSendsEmptyIsEmptySlice pins the "never nil" convention
// loadPayments/handleList already follow, so a never-sent invoice
// serializes "sends":[] rather than "sends":null.
func TestLoadInvoiceSendsEmptyIsEmptySlice(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)

	sends, err := loadInvoiceSends(db, "does-not-exist")
	if err != nil {
		t.Fatalf("loadInvoiceSends: %v", err)
	}
	if sends == nil {
		t.Fatal("loadInvoiceSends returned nil, want empty slice")
	}
	if len(sends) != 0 {
		t.Fatalf("sends = %+v, want empty", sends)
	}
}
