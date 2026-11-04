package main

import (
	"compress/gzip"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// This file implements the admin-side quote request API, the inbox for requests the public listener stores.

type quoteRequests struct {
	db *sql.DB
	// demoMode mirrors DEMO_MODE (authState.demoMode): handleGet skips the new->viewed write.
	// demoWriteGuard only inspects unsafe methods, so demo GETs would otherwise flip seeded requests.
	demoMode bool
	// configDir backs handleStorage's dirSize call (CONFIG_DIR/uploads), which retention.go reclaims from.
	configDir string
	// store backs handlePrintTime, which prices a request from the current config.
	store *ConfigStore
}

func newQuoteRequests(db *sql.DB, store *ConfigStore, configDir string, demoMode bool) *quoteRequests {
	return &quoteRequests{db: db, store: store, configDir: configDir, demoMode: demoMode}
}

// QuoteRequest is the JSON shape of every requests endpoint: every quote_requests column (admin-only).
// Gcode columns encode four states, no schema change: never uploaded (all empty); stored (filename, path);
// pruned (filename, pruned_at); dropped at submission when uploads were full (filename only).
type QuoteRequest struct {
	ID          string  `json:"id"`
	Status      string  `json:"status"`
	Name        string  `json:"name"`
	Email       string  `json:"email"`
	Note        string  `json:"note"`
	Material    string  `json:"material"`
	Quantity    int     `json:"quantity"`
	WeightG     float64 `json:"weightG"`
	TimeSeconds int     `json:"timeSeconds"`
	PriceCents  int     `json:"priceCents"`
	// ShippingCents is the shipping charge the visitor saw beside the price at submit (0 for item orders).
	// It is not part of PriceCents; the request-to-invoice conversion adds it as a Shipping line.
	ShippingCents int `json:"shippingCents"`
	// TaxInclusive records whether PriceCents included tax at submit.
	TaxInclusive  bool   `json:"taxInclusive"`
	GcodeFilename string `json:"gcodeFilename"`
	GcodePath     string `json:"gcodePath"`
	GcodeBytes    int    `json:"gcodeBytes"`
	CreatedAt     string `json:"createdAt"`
	FinalizedAt   string `json:"finalizedAt"`
	GcodePrunedAt string `json:"gcodePrunedAt"`

	// Kind is "quote" (the default) or "item" (a gallery order). ItemID/ItemName/OptionName are set only for
	// "item"; the names are order-time snapshots, not a foreign key, so renames don't change past orders.
	Kind       string `json:"kind"`
	ItemID     string `json:"itemId"`
	ItemName   string `json:"itemName"`
	OptionName string `json:"optionName"`

	// NeedsPrintTime is true for a public estimate submitted without a print time: PriceCents is 0 and means
	// "to be confirmed". The owner enters the time when quoting or converting.
	NeedsPrintTime bool `json:"needsPrintTime"`
	// Reference is the short per-shop order reference shown to the visitor ("HL-1001"); "" for rows created
	// before it existed. The hex ID stays the identifier everywhere else.
	Reference string `json:"reference"`

	// UpdatedBy follows Quote.CreatedBy, but quote_requests has no created_by (the public listener has no
	// identity). It moves only on admin writes: handleGet's new->viewed and handleStatus's viewed->finalized.
	UpdatedBy string `json:"updatedBy"`

	// InvoiceID is the invoice this request was converted to ('' while it has not been). A converted request
	// is finalized and stays "finalized"; the link is what marks it "Invoiced" in the admin app. InvoiceNumber
	// and InvoiceStatus are read through that link, and are '' when the invoice was deleted.
	InvoiceID     string `json:"invoiceId"`
	InvoiceNumber string `json:"invoiceNumber"`
	InvoiceStatus string `json:"invoiceStatus"`

	// NeedsPrintTime marks a public estimate submitted without a print time: priceCents and timeSeconds are 0
	// until the owner enters the time (handlePrintTime).
	NeedsPrintTime bool `json:"needsPrintTime"`
}

// quoteRequestStatusTransitions allows only viewed->finalized (the owner must look first). Others are a 409.
var quoteRequestStatusTransitions = map[string]map[string]bool{
	"new":       {},
	"viewed":    {"finalized": true},
	"finalized": {},
}

// requestColumns lists the columns every quote_requests SELECT reads, in the order Scan expects.
// price_cents is the server's own computation (buildPublicQuoteInputs + ComputeQuote), not a browser price.
const requestColumns = `id, status, name, email, note, material, quantity, weight_g, time_seconds, price_cents, gcode_filename, gcode_path, gcode_bytes, created_at, finalized_at, gcode_pruned_at, kind, item_id, item_name, option_name, updated_by, shipping_cents, tax_inclusive,
	invoice_id, COALESCE((SELECT number FROM invoices WHERE id = quote_requests.invoice_id), ''), COALESCE((SELECT status FROM invoices WHERE id = quote_requests.invoice_id), ''), needs_print_time, reference`

// scanRequest reads one row (see requestColumns) into a QuoteRequest.
func scanRequest(row rowScanner) (QuoteRequest, error) {
	var req QuoteRequest
	err := row.Scan(&req.ID, &req.Status, &req.Name, &req.Email, &req.Note, &req.Material, &req.Quantity,
		&req.WeightG, &req.TimeSeconds, &req.PriceCents, &req.GcodeFilename, &req.GcodePath, &req.GcodeBytes,
		&req.CreatedAt, &req.FinalizedAt, &req.GcodePrunedAt,
		&req.Kind, &req.ItemID, &req.ItemName, &req.OptionName, &req.UpdatedBy, &req.ShippingCents, &req.TaxInclusive,
		&req.InvoiceID, &req.InvoiceNumber, &req.InvoiceStatus, &req.NeedsPrintTime, &req.Reference)
	if err != nil {
		return QuoteRequest{}, err
	}
	return req, nil
}

// load reads one quote request by id.
func (qr *quoteRequests) load(id string) (*QuoteRequest, bool, error) {
	row := qr.db.QueryRow(`SELECT `+requestColumns+` FROM quote_requests WHERE id = ?`, id)
	req, err := scanRequest(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &req, true, nil
}

func (qr *quoteRequests) handleList(w http.ResponseWriter, r *http.Request) {
	page, ok, msg := parsePagination(r)
	if !ok {
		writeAPIError(w, http.StatusBadRequest, msg)
		return
	}

	rows, err := qr.db.Query(`SELECT ` + requestColumns + ` FROM quote_requests ORDER BY created_at DESC, rowid DESC`)
	if err != nil {
		log.Printf("requests query: %v", err)
		http.Error(w, "requests unavailable", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	// Empty, not nil, so a fresh database serializes "requests":[].
	list := []QuoteRequest{}
	for rows.Next() {
		req, err := scanRequest(rows)
		if err != nil {
			log.Printf("requests scan: %v", err)
			http.Error(w, "requests unavailable", http.StatusInternalServerError)
			return
		}
		list = append(list, req)
	}
	if err := rows.Err(); err != nil {
		log.Printf("requests read: %v", err)
		http.Error(w, "requests unavailable", http.StatusInternalServerError)
		return
	}

	list = paginate(w, list, page)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Requests []QuoteRequest `json:"requests"`
	}{Requests: list})
}

// handleStorage serves GET /api/requests/storage: CONFIG_DIR/uploads usage against publicUploadsCapBytes.
// Nothing else tells the operator when storage-full silently stops files attaching to new requests.
// Reuses dirSize's uploads/items skip: gallery images don't count against the visitor-upload budget.
func (qr *quoteRequests) handleStorage(w http.ResponseWriter, r *http.Request) {
	used, err := dirSize(filepath.Join(qr.configDir, "uploads"))
	if err != nil {
		log.Printf("requests storage: dir size: %v", err)
		http.Error(w, "requests unavailable", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		UsedBytes int64 `json:"usedBytes"`
		CapBytes  int64 `json:"capBytes"`
	}{UsedBytes: used, CapBytes: publicUploadsCapBytes})
}

// handleGet loads one request and marks a "new" one "viewed": opening it is the owner viewing it.
// In demo mode that write is skipped (see the demoMode field).
func (qr *quoteRequests) handleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	req, found, err := qr.load(id)
	if err != nil {
		log.Printf("requests load: %v", err)
		http.Error(w, "requests unavailable", http.StatusInternalServerError)
		return
	}
	if !found {
		writeAPIError(w, http.StatusNotFound, "request not found")
		return
	}

	if req.Status == "new" && !qr.demoMode {
		by := identityFor(r).username
		if _, err := qr.db.Exec(`UPDATE quote_requests SET status = 'viewed', updated_by = ? WHERE id = ?`, by, id); err != nil {
			log.Printf("requests mark viewed: %v", err)
			http.Error(w, "requests unavailable", http.StatusInternalServerError)
			return
		}
		req.Status = "viewed"
		req.UpdatedBy = by
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(req)
}

// handleGcodeDownload streams the stored file ungzipped with its sanitized original filename.
// Despite the name the bytes may be STL or 3MF; all uploads share the "gcode" field and column names.
func (qr *quoteRequests) handleGcodeDownload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	req, found, err := qr.load(id)
	if err != nil {
		log.Printf("requests load: %v", err)
		http.Error(w, "requests unavailable", http.StatusInternalServerError)
		return
	}
	if !found {
		writeAPIError(w, http.StatusNotFound, "request not found")
		return
	}

	if req.GcodePrunedAt != "" {
		writeAPIError(w, http.StatusGone, fmt.Sprintf(
			"the uploaded file was pruned on %s: finalized requests keep their stored file for %d days after finalization, then the raw upload is deleted to reclaim storage (the request record, its metadata, and the quote/invoice derived from it are retained)",
			req.GcodePrunedAt, requestGcodeRetentionGraceDays))
		return
	}

	f, err := os.Open(req.GcodePath)
	if err != nil {
		if os.IsNotExist(err) {
			writeAPIError(w, http.StatusNotFound, "gcode file not found")
			return
		}
		log.Printf("requests open gcode: %v", err)
		http.Error(w, "requests unavailable", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	filename := req.GcodeFilename
	if filename == "" {
		filename = "upload.bin" // extension-neutral: matches sanitizeGcodeFilename's own default (server/public.go)
	}
	// The gzip reader is built before any header is written: a corrupt file fails here, so the 500 never
	// declares a Content-Length taken from a bogus trailer.
	gz, err := gzip.NewReader(f)
	if err != nil {
		log.Printf("requests gzip reader: %v", err)
		http.Error(w, "requests unavailable", http.StatusInternalServerError)
		return
	}
	defer gz.Close()

	// octet-stream, not text/plain: the bytes may be binary STL or zipped 3MF, never rendered inline.
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))

	// Content-Length comes from the gzip trailer's ISIZE (uncompressed size mod 2^32; RFC 1952), so a
	// connection dying mid-copy shows as a short download. The wrap is unreachable (uploads cap at 32 MiB).
	// A stat failure or a file under 4 bytes (corrupt) just skips the header.
	if info, statErr := f.Stat(); statErr == nil && info.Size() >= 4 {
		var trailer [4]byte
		if _, readErr := f.ReadAt(trailer[:], info.Size()-4); readErr == nil {
			isize := binary.LittleEndian.Uint32(trailer[:])
			w.Header().Set("Content-Length", strconv.FormatUint(uint64(isize), 10))
		}
	}

	if _, err := io.Copy(w, gz); err != nil {
		log.Printf("requests stream gcode: %v", err)
	}
}

// requestStatusMaxBytes bounds the POST body: one status field.
const requestStatusMaxBytes = 4 * 1024

type requestStatusInput struct {
	Status string `json:"status"`
}

func (qr *quoteRequests) handleStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	in, ok := decodeJSONBody[requestStatusInput](w, r, requestStatusMaxBytes)
	if !ok {
		return
	}
	status := strings.TrimSpace(in.Status)
	if status != "finalized" {
		writeAPIError(w, http.StatusUnprocessableEntity, "status: must be finalized")
		return
	}

	req, found, err := qr.load(id)
	if err != nil {
		log.Printf("requests load: %v", err)
		http.Error(w, "requests unavailable", http.StatusInternalServerError)
		return
	}
	if !found {
		writeAPIError(w, http.StatusNotFound, "request not found")
		return
	}
	if !quoteRequestStatusTransitions[req.Status][status] {
		writeAPIError(w, http.StatusConflict, fmt.Sprintf("can't change status from %s to %s; reload to see the latest status", req.Status, status))
		return
	}

	// finalized_at starts the retention grace clock; it is set only on the viewed->finalized transition.
	by := identityFor(r).username
	now := time.Now().UTC().Format(time.RFC3339)

	// The status transition and, for a kind "item" request, the stock decrement and the item_sales record
	// commit atomically: never a finalized order against stock that wasn't taken off the shelf or a sale
	// that wasn't recorded, or vice versa.
	tx, err := qr.db.Begin()
	if err != nil {
		log.Printf("requests status begin tx: %v", err)
		http.Error(w, "requests not writable", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`UPDATE quote_requests SET status = ?, finalized_at = ?, updated_by = ? WHERE id = ?`, status, now, by, id); err != nil {
		log.Printf("requests status update: %v", err)
		http.Error(w, "requests not writable", http.StatusInternalServerError)
		return
	}

	// Item-gallery orders: finalizing takes the ordered quantity off an in_stock item's shelf, floored at
	// zero. A zero-row UPDATE (item deleted, or ready_to_print) is not an error: the public order endpoint's
	// stock check is best-effort, and an oversell resolves here, at review time.
	if req.Kind == "item" && req.ItemID != "" {
		if _, err := tx.Exec(`UPDATE items
		  SET stock_qty = CASE WHEN stock_qty > ? THEN stock_qty - ? ELSE 0 END,
		      updated_at = ?, updated_by = ?
		  WHERE id = ? AND stock_mode = ?`,
			req.Quantity, req.Quantity, now, by, req.ItemID, stockModeInStock); err != nil {
			log.Printf("requests status decrement stock: %v", err)
			http.Error(w, "requests not writable", http.StatusInternalServerError)
			return
		}
	}

	// The sale ledger row outlives the request (handleDelete never touches item_sales). A plain INSERT: a
	// duplicate request_id is a bug to surface, not to swallow, and rolls the whole finalize back.
	if req.Kind == "item" {
		if _, err := tx.Exec(`INSERT INTO item_sales
		  (request_id, item_id, item_name, option_name, quantity, revenue_cents, sold_at, recorded_by)
		  VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			req.ID, req.ItemID, req.ItemName, req.OptionName, req.Quantity, req.PriceCents, now, by); err != nil {
			log.Printf("requests status record sale: %v", err)
			http.Error(w, "requests not writable", http.StatusInternalServerError)
			return
		}
	}

	if err := tx.Commit(); err != nil {
		log.Printf("requests status commit: %v", err)
		http.Error(w, "requests not writable", http.StatusInternalServerError)
		return
	}

	req.Status = status
	req.FinalizedAt = now
	req.UpdatedBy = by

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(req)
}

// handleDelete removes the row first, then the stored file; a missing file is not an error.
// If the row DELETE fails the request still exists, so its file must too; file-first would destroy data the
// caller was told was not deleted. Row-first leaves at worst an orphan, which sweepOrphanedUploads reclaims.
func (qr *quoteRequests) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	req, found, err := qr.load(id)
	if err != nil {
		log.Printf("requests load: %v", err)
		http.Error(w, "requests unavailable", http.StatusInternalServerError)
		return
	}
	if !found {
		writeAPIError(w, http.StatusNotFound, "request not found")
		return
	}

	if _, err := qr.db.Exec(`DELETE FROM quote_requests WHERE id = ?`, id); err != nil {
		log.Printf("requests delete: %v", err)
		http.Error(w, "requests not writable", http.StatusInternalServerError)
		return
	}

	// Best-effort: the row is already gone; a failure leaves only an orphan for sweepOrphanedUploads.
	if req.GcodePath != "" {
		if err := os.Remove(req.GcodePath); err != nil && !os.IsNotExist(err) {
			log.Printf("requests delete gcode file: %v", err)
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteNew bulk-deletes every "new" request, for the public-request flood (publicMaxOpenRequests);
// viewed and finalized are left alone. Same row-then-file ordering as handleDelete: rows and paths are read
// and removed in ONE transaction, files best-effort after commit, so a failure leaves only an orphan.
func (qr *quoteRequests) handleDeleteNew(w http.ResponseWriter, r *http.Request) {
	tx, err := qr.db.Begin()
	if err != nil {
		log.Printf("requests delete-new begin tx: %v", err)
		http.Error(w, "requests not writable", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	rows, err := tx.Query(`SELECT gcode_path FROM quote_requests WHERE status = 'new' AND gcode_path != ''`)
	if err != nil {
		log.Printf("requests delete-new query files: %v", err)
		http.Error(w, "requests not writable", http.StatusInternalServerError)
		return
	}
	var gcodePaths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			rows.Close()
			log.Printf("requests delete-new scan file: %v", err)
			http.Error(w, "requests not writable", http.StatusInternalServerError)
			return
		}
		gcodePaths = append(gcodePaths, path)
	}
	closeErr := rows.Err()
	rows.Close()
	if closeErr != nil {
		log.Printf("requests delete-new read files: %v", closeErr)
		http.Error(w, "requests not writable", http.StatusInternalServerError)
		return
	}

	res, err := tx.Exec(`DELETE FROM quote_requests WHERE status = 'new'`)
	if err != nil {
		log.Printf("requests delete-new: %v", err)
		http.Error(w, "requests not writable", http.StatusInternalServerError)
		return
	}
	deleted, err := res.RowsAffected()
	if err != nil {
		log.Printf("requests delete-new rows affected: %v", err)
		http.Error(w, "requests not writable", http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(); err != nil {
		log.Printf("requests delete-new commit: %v", err)
		http.Error(w, "requests not writable", http.StatusInternalServerError)
		return
	}

	// Best-effort, as in handleDelete: a failure leaves only orphans for sweepOrphanedUploads.
	for _, path := range gcodePaths {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			log.Printf("requests delete-new gcode file: %v", err)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		DeletedCount int64 `json:"deletedCount"`
	}{DeletedCount: deleted})
}

// errRequestAlreadyInvoiced is linkRequestToInvoice's loser-of-a-race result: another conversion linked the
// request between prepareRequestConversion's check and this transaction.
var errRequestAlreadyInvoiced = errors.New("request already invoiced")

// requestInvoiceLinkLive reports whether the request already points at an invoice that still counts: one that
// exists and is not canceled. A deleted or canceled invoice frees the request for a new conversion.
func requestInvoiceLinkLive(invoiceID, invoiceStatus string) bool {
	return invoiceID != "" && invoiceStatus != "" && invoiceStatus != "canceled"
}

// prepareRequestConversion runs before the invoice POST's validation for a body that names a requestId. It
// returns an HTTP status and message when the conversion must be refused (unknown request, already invoiced,
// no print time yet) and otherwise adjusts in.Lines in place:
//   - A line's item link is dropped when the item was deleted, and its option when the item's groups no longer
//     contain that summary, so a request for a changed item still converts.
//   - Every item link is dropped when the request was already finalized: finalize recorded that sale in
//     item_sales and took the stock, and a linked line would count it again when the invoice is sent. An
//     unfinalized request converts with its links, and linkRequestToInvoice finalizes it WITHOUT the sale
//     record, so sending the invoice is the one place the sale and the stock are counted.
//
// A non-nil error means a lookup failed (500).
func prepareRequestConversion(db *sql.DB, in *invoiceInput) (int, string, error) {
	req, found, err := loadRequestRow(db, in.RequestID)
	if err != nil {
		return 0, "", err
	}
	if !found {
		return http.StatusUnprocessableEntity, "requestId: unknown request", nil
	}
	if requestInvoiceLinkLive(req.InvoiceID, req.InvoiceStatus) {
		return http.StatusConflict, "this request was already converted to invoice " + req.InvoiceNumber + "; open that invoice instead", nil
	}
	if req.NeedsPrintTime {
		return http.StatusConflict, "this request needs a print time before it can be invoiced; enter the print time first", nil
	}
	for i := range in.Lines {
		l := &in.Lines[i]
		if l.ItemID == "" {
			continue
		}
		if req.Status == "finalized" {
			l.ItemID, l.OptionName = "", ""
			continue
		}
		var groupsJSON string
		err := db.QueryRow(`SELECT option_groups_json FROM items WHERE id = ?`, l.ItemID).Scan(&groupsJSON)
		if errors.Is(err, sql.ErrNoRows) {
			l.ItemID, l.OptionName = "", ""
			continue
		}
		if err != nil {
			return 0, "", err
		}
		if l.OptionName != "" {
			var groups []ItemOptionGroup
			if err := json.Unmarshal([]byte(groupsJSON), &groups); err != nil {
				return 0, "", err
			}
			if _, ok := optionSummaryDelta(groups, l.OptionName); !ok {
				l.OptionName = ""
			}
		}
	}
	return 0, "", nil
}

// loadRequestRow reads one request outside a quoteRequests receiver, for the invoice handlers.
func loadRequestRow(db *sql.DB, id string) (*QuoteRequest, bool, error) {
	return (&quoteRequests{db: db}).load(id)
}

// linkRequestToInvoice runs inside the invoice-insert transaction: it points the request at the new invoice
// and finalizes it, so Convert to invoice leaves the request "Invoiced" in one step. It writes no item_sales
// row and takes no stock; see prepareRequestConversion. The WHERE refuses a request another conversion linked
// in the meantime.
func linkRequestToInvoice(tx *sql.Tx, requestID, invoiceID, by, now string) error {
	res, err := tx.Exec(`
		UPDATE quote_requests
		SET invoice_id = ?, updated_by = ?,
		    finalized_at = CASE WHEN status = 'finalized' THEN finalized_at ELSE ? END,
		    status = 'finalized'
		WHERE id = ? AND (invoice_id = '' OR NOT EXISTS (SELECT 1 FROM invoices i WHERE i.id = quote_requests.invoice_id AND i.status != 'canceled'))`,
		invoiceID, by, now, requestID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return errRequestAlreadyInvoiced
	}
	return nil
}

// requestPrintTimeMaxBytes bounds the POST body: one number.
const requestPrintTimeMaxBytes = 4 * 1024

type requestPrintTimeInput struct {
	TimeSeconds int `json:"timeSeconds"`
}

// handlePrintTime implements POST /api/requests/{id}/print-time: the owner supplies the print time a public
// estimate was submitted without. The server prices the request with the same engine call the public submit
// uses (buildPublicQuoteInputs + ComputeQuote) against the current config, stores time and price, and clears
// the needs-print-time mark.
func (qr *quoteRequests) handlePrintTime(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	in, ok := decodeJSONBody[requestPrintTimeInput](w, r, requestPrintTimeMaxBytes)
	if !ok {
		return
	}
	if in.TimeSeconds < 60 || in.TimeSeconds > maxTimeSeconds {
		writeAPIError(w, http.StatusUnprocessableEntity, fmt.Sprintf("timeSeconds: must be 60-%d", maxTimeSeconds))
		return
	}
	req, found, err := qr.load(id)
	if err != nil {
		log.Printf("requests load: %v", err)
		http.Error(w, "requests unavailable", http.StatusInternalServerError)
		return
	}
	if !found {
		writeAPIError(w, http.StatusNotFound, "request not found")
		return
	}
	if !req.NeedsPrintTime || req.Kind == "item" {
		writeAPIError(w, http.StatusConflict, "this request doesn't need a print time")
		return
	}

	cfg := qr.store.Get()
	var material *Material
	for i := range cfg.Materials {
		if cfg.Materials[i].Name == req.Material {
			material = &cfg.Materials[i]
			break
		}
	}
	if material == nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "material: this material is no longer in your settings; add it back or use Open in new quote")
		return
	}
	qi, opt := buildPublicQuoteInputs(cfg, *material, req.Quantity, req.WeightG, in.TimeSeconds)
	bd := ComputeQuote(qi, opt)
	if !bd.Valid || bd.Pricing == nil || !bd.Pricing.Valid {
		writeAPIError(w, http.StatusUnprocessableEntity, "price: couldn't price this request with the current settings; use Open in new quote")
		return
	}
	priceCents := int(bd.Pricing.Revenue)

	by := identityFor(r).username
	if _, err := qr.db.Exec(`UPDATE quote_requests SET time_seconds = ?, price_cents = ?, needs_print_time = 0, updated_by = ? WHERE id = ? AND needs_print_time = 1`,
		in.TimeSeconds, priceCents, by, id); err != nil {
		log.Printf("requests print time: %v", err)
		http.Error(w, "requests not writable", http.StatusInternalServerError)
		return
	}
	req, _, err = qr.load(id)
	if err != nil {
		log.Printf("requests load: %v", err)
		http.Error(w, "requests unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(req)
}
