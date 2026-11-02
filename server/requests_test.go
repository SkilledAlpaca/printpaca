package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// seedQuoteRequest submits one public quote request through publicMux and
// returns its assigned id, so admin API tests exercise real rows produced by
// the same code path production uses.
func seedQuoteRequest(t *testing.T, publicMux *http.ServeMux, material string) string {
	t.Helper()
	rec := postPublicQuoteRequest(t, publicMux, publicQuoteRequestFields(material), "model.gcode", []byte("G1 X1 Y1\n"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed submission: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal seed response: %v", err)
	}
	return resp.ID
}

func getRequest(t *testing.T, mux *http.ServeMux, id string) *httptest.ResponseRecorder {
	return serveReq(mux, httptest.NewRequest("GET", "/api/requests/"+id, nil))
}

func setRequestStatus(t *testing.T, mux *http.ServeMux, id, status string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	body := fmt.Sprintf(`{"status":%q}`, status)
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/requests/"+id+"/status", strings.NewReader(body)))
	return rec
}

func deleteRequest(t *testing.T, mux *http.ServeMux, id string) *httptest.ResponseRecorder {
	return serveReq(mux, httptest.NewRequest("DELETE", "/api/requests/"+id, nil))
}

func deleteNewRequests(t *testing.T, mux *http.ServeMux) *httptest.ResponseRecorder {
	return serveReq(mux, httptest.NewRequest("DELETE", "/api/requests/new", nil))
}

func TestRequestsListOrderNewestFirst(t *testing.T) {
	dir := t.TempDir()
	adminMux, publicMux, _, _ := newRequestsTestSetup(t, dir)

	first := seedQuoteRequest(t, publicMux, "PLA")
	second := seedQuoteRequest(t, publicMux, "PLA")
	third := seedQuoteRequest(t, publicMux, "PLA")

	rec := serveReq(adminMux, httptest.NewRequest("GET", "/api/requests", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Requests []QuoteRequest `json:"requests"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Requests) != 3 {
		t.Fatalf("requests len = %d, want 3", len(body.Requests))
	}
	// created_at has second resolution, so ties are broken by rowid (insert
	// order): newest first means third, second, first.
	got := []string{body.Requests[0].ID, body.Requests[1].ID, body.Requests[2].ID}
	want := []string{third, second, first}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

// TestRequestsListPaginationLimit pins ?limit=: the response is capped at
// limit rows, in the exact same order as the unpaginated list, and
// X-Total-Count reports every row that exists, not just what was returned.
func TestRequestsListPaginationLimit(t *testing.T) {
	dir := t.TempDir()
	adminMux, publicMux, _, _ := newRequestsTestSetup(t, dir)

	seedQuoteRequest(t, publicMux, "PLA")
	second := seedQuoteRequest(t, publicMux, "PLA")
	third := seedQuoteRequest(t, publicMux, "PLA")

	rec := serveReq(adminMux, httptest.NewRequest("GET", "/api/requests?limit=2", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if got, want := rec.Header().Get("X-Total-Count"), "3"; got != want {
		t.Errorf("X-Total-Count = %q, want %q", got, want)
	}
	var body struct {
		Requests []QuoteRequest `json:"requests"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Requests) != 2 {
		t.Fatalf("requests len = %d, want 2", len(body.Requests))
	}
	if body.Requests[0].ID != third || body.Requests[1].ID != second {
		t.Fatalf("order = [%s %s], want [%s %s] (newest-first order must be preserved under limit)", body.Requests[0].ID, body.Requests[1].ID, third, second)
	}
}

// TestRequestsListPaginationOffset pins ?offset=: skips the first offset
// rows in the existing order and still sets X-Total-Count, since offset
// alone opts a request into pagination just as limit does.
func TestRequestsListPaginationOffset(t *testing.T) {
	dir := t.TempDir()
	adminMux, publicMux, _, _ := newRequestsTestSetup(t, dir)

	first := seedQuoteRequest(t, publicMux, "PLA")
	second := seedQuoteRequest(t, publicMux, "PLA")
	seedQuoteRequest(t, publicMux, "PLA")

	rec := serveReq(adminMux, httptest.NewRequest("GET", "/api/requests?offset=1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if got, want := rec.Header().Get("X-Total-Count"), "3"; got != want {
		t.Errorf("X-Total-Count = %q, want %q", got, want)
	}
	var body struct {
		Requests []QuoteRequest `json:"requests"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Requests) != 2 {
		t.Fatalf("requests len = %d, want 2 (3 total minus the 1 skipped by offset)", len(body.Requests))
	}
	if body.Requests[0].ID != second || body.Requests[1].ID != first {
		t.Fatalf("order = [%s %s], want [%s %s]", body.Requests[0].ID, body.Requests[1].ID, second, first)
	}
}

// With neither ?limit= nor ?offset= the response is every row with no X-Total-Count header, keeping the web client (which sends neither) unaffected.
func TestRequestsListAbsentPaginationParamsReturnsAllUnchanged(t *testing.T) {
	dir := t.TempDir()
	adminMux, publicMux, _, _ := newRequestsTestSetup(t, dir)
	seedQuoteRequest(t, publicMux, "PLA")
	seedQuoteRequest(t, publicMux, "PLA")

	rec := serveReq(adminMux, httptest.NewRequest("GET", "/api/requests", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Total-Count"); got != "" {
		t.Errorf("X-Total-Count = %q, want unset when pagination isn't used", got)
	}
	var body struct {
		Requests []QuoteRequest `json:"requests"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Requests) != 2 {
		t.Fatalf("requests len = %d, want 2", len(body.Requests))
	}
}

// TestRequestsListInvalidPaginationParamsIs400 pins validation: a
// non-integer or out-of-range limit/offset is rejected with 400, not
// silently clamped or ignored.
func TestRequestsListInvalidPaginationParamsIs400(t *testing.T) {
	dir := t.TempDir()
	adminMux, _, _, _ := newRequestsTestSetup(t, dir)

	cases := []string{
		"/api/requests?limit=0",
		"/api/requests?limit=-1",
		"/api/requests?limit=abc",
		"/api/requests?limit=501",
		"/api/requests?offset=-1",
		"/api/requests?offset=abc",
	}
	for _, path := range cases {
		t.Run(path, func(t *testing.T) {
			rec := serveReq(adminMux, httptest.NewRequest("GET", path, nil))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestRequestsGetMarksNewAsViewed(t *testing.T) {
	dir := t.TempDir()
	adminMux, publicMux, _, _ := newRequestsTestSetup(t, dir)
	id := seedQuoteRequest(t, publicMux, "PLA")

	rec := getRequest(t, adminMux, id)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	var req QuoteRequest
	if err := json.Unmarshal(rec.Body.Bytes(), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.Status != "viewed" {
		t.Fatalf("status after first GET = %q, want viewed", req.Status)
	}

	// A second GET does not change an already-viewed request.
	rec = getRequest(t, adminMux, id)
	if err := json.Unmarshal(rec.Body.Bytes(), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.Status != "viewed" {
		t.Fatalf("status after second GET = %q, want viewed", req.Status)
	}
}

// In demo mode a GET of a "new" request (anyone can browse the seeded inbox) must not flip it to "viewed": that write belongs to demoWriteGuard's read-only contract even though it rides on a GET.
// The row is seeded through a non-demo publicMux (demo mode inserts no row) so only the admin GET is in demo mode.
func TestRequestsGetSkipsViewedWriteInDemoMode(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	cfg := newPublicTestConfig(t, dir)
	store := NewConfigStore(cfg)

	adminMux := routesMux(db, store, newAuthState("", nil), dir, true) // demoMode true
	publicMux, _ := buildPublicMux(store, db, dir, false, false)       // seed with demoMode false

	id := seedQuoteRequest(t, publicMux, "PLA")

	rec := getRequest(t, adminMux, id)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	var req QuoteRequest
	if err := json.Unmarshal(rec.Body.Bytes(), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.Status != "new" {
		t.Fatalf("status in demo-mode GET response = %q, want new (unchanged)", req.Status)
	}

	// Second GET: same result, and the row itself was never written.
	rec = getRequest(t, adminMux, id)
	if err := json.Unmarshal(rec.Body.Bytes(), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.Status != "new" {
		t.Fatalf("status after second demo-mode GET = %q, want new", req.Status)
	}

	var dbStatus string
	if err := db.QueryRow(`SELECT status FROM quote_requests WHERE id = ?`, id).Scan(&dbStatus); err != nil {
		t.Fatalf("query status: %v", err)
	}
	if dbStatus != "new" {
		t.Fatalf("db status = %q, want new (no write in demo mode)", dbStatus)
	}
}

func TestRequestsGetUnknownIs404(t *testing.T) {
	dir := t.TempDir()
	adminMux, _, _, _ := newRequestsTestSetup(t, dir)
	rec := getRequest(t, adminMux, "nonexistent")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestRequestsGcodeDownloadRoundTrips(t *testing.T) {
	dir := t.TempDir()
	adminMux, publicMux, _, _ := newRequestsTestSetup(t, dir)

	content := []byte("G1 X1 Y1\nG1 X2 Y2\n")
	rec := postPublicQuoteRequest(t, publicMux, publicQuoteRequestFields("PLA"), "part.gcode", content)
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var seeded struct {
		ID string `json:"id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &seeded)

	rec = serveReq(adminMux, httptest.NewRequest("GET", "/api/requests/"+seeded.ID+"/gcode", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.Bytes(); !bytes.Equal(got, content) {
		t.Fatalf("downloaded content = %q, want %q", got, content)
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") || !strings.Contains(cd, "part.gcode") {
		t.Errorf("Content-Disposition = %q, want attachment with part.gcode", cd)
	}
}

// Public STL/3MF drops upload through the same "gcode" multipart field (no new field, no schema change, no extension allowlist), so this covers that path beside the .gcode case.
// Content is a minimal ASCII STL triangle, proving only the filename and byte count matter, not content.
func TestRequestsStlUploadDownloadRoundTrips(t *testing.T) {
	dir := t.TempDir()
	adminMux, publicMux, db, _ := newRequestsTestSetup(t, dir)

	content := []byte("solid part\n facet normal 0 0 0\n outer loop\n  vertex 0 0 0\n  vertex 1 0 0\n  vertex 0 1 0\n endloop\n endfacet\nendsolid part\n")
	rec := postPublicQuoteRequest(t, publicMux, publicQuoteRequestFields("PLA"), "part.stl", content)
	if rec.Code != http.StatusCreated {
		t.Fatalf("submit: status = %d, want 201, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	var gcodeFilename string
	var gcodeBytes int
	if err := db.QueryRow(`SELECT gcode_filename, gcode_bytes FROM quote_requests WHERE id = ?`, resp.ID).
		Scan(&gcodeFilename, &gcodeBytes); err != nil {
		t.Fatalf("query row: %v", err)
	}
	if gcodeFilename != "part.stl" {
		t.Errorf("gcode_filename = %q, want part.stl", gcodeFilename)
	}
	if gcodeBytes != len(content) {
		t.Errorf("gcode_bytes = %d, want %d", gcodeBytes, len(content))
	}

	rec = serveReq(adminMux, httptest.NewRequest("GET", "/api/requests/"+resp.ID+"/gcode", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("download: status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.Bytes(); !bytes.Equal(got, content) {
		t.Fatalf("downloaded content = %q, want %q", got, content)
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") || !strings.Contains(cd, "part.stl") {
		t.Errorf("Content-Disposition = %q, want attachment with part.stl", cd)
	}
}

// The download must carry the exact decompressed Content-Length up front (from the gzip ISIZE trailer) so clients can detect a response truncated mid-stream.
func TestRequestsGcodeDownloadSetsContentLength(t *testing.T) {
	dir := t.TempDir()
	adminMux, publicMux, _, _ := newRequestsTestSetup(t, dir)

	content := []byte("G1 X1 Y1\nG1 X2 Y2\nG1 X3 Y3\nG1 X4 Y4\n")
	id := func() string {
		rec := postPublicQuoteRequest(t, publicMux, publicQuoteRequestFields("PLA"), "part.gcode", content)
		if rec.Code != http.StatusCreated {
			t.Fatalf("seed: status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var resp struct {
			ID string `json:"id"`
		}
		json.Unmarshal(rec.Body.Bytes(), &resp)
		return resp.ID
	}()

	rec := serveReq(adminMux, httptest.NewRequest("GET", "/api/requests/"+id+"/gcode", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	want := strconv.Itoa(len(content))
	if got := rec.Header().Get("Content-Length"); got != want {
		t.Errorf("Content-Length = %q, want %q", got, want)
	}
	if got := rec.Body.Bytes(); !bytes.Equal(got, content) {
		t.Fatalf("downloaded content = %q, want %q", got, content)
	}
}

// A truncated or corrupt .gcode.gz must fail before Content-Length is set from the ISIZE trailer; otherwise the 500 declares a length from garbage bytes and hangs the client.
func TestRequestsGcodeDownloadCorruptFileNoContentLength(t *testing.T) {
	dir := t.TempDir()
	adminMux, publicMux, db, _ := newRequestsTestSetup(t, dir)

	content := []byte("G1 X1 Y1\nG1 X2 Y2\nG1 X3 Y3\nG1 X4 Y4\n")
	rec := postPublicQuoteRequest(t, publicMux, publicQuoteRequestFields("PLA"), "part.gcode", content)
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var seeded struct {
		ID string `json:"id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &seeded)

	var gcodePath string
	if err := db.QueryRow(`SELECT gcode_path FROM quote_requests WHERE id = ?`, seeded.ID).Scan(&gcodePath); err != nil {
		t.Fatalf("query gcode_path: %v", err)
	}

	// Corrupt the stored file: keep the last 4 bytes (the gzip ISIZE
	// trailer, which the handler reads even on a corrupt stream) but
	// truncate/garble everything before it so gzip.NewReader fails.
	stored, err := os.ReadFile(gcodePath)
	if err != nil {
		t.Fatalf("read stored file: %v", err)
	}
	if len(stored) < 4 {
		t.Fatalf("stored file too small to corrupt meaningfully: %d bytes", len(stored))
	}
	corrupt := append([]byte("not a gzip stream at all"), stored[len(stored)-4:]...)
	if err := os.WriteFile(gcodePath, corrupt, 0o644); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}

	rec = serveReq(adminMux, httptest.NewRequest("GET", "/api/requests/"+seeded.ID+"/gcode", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body = %s", rec.Code, rec.Body.String())
	}
	if cl := rec.Header().Get("Content-Length"); cl != "" {
		t.Errorf("Content-Length = %q, want unset (must not be derived from a corrupt file's trailer bytes)", cl)
	}
}

func TestRequestsGcodeDownloadUnknownIs404(t *testing.T) {
	dir := t.TempDir()
	adminMux, _, _, _ := newRequestsTestSetup(t, dir)
	rec := serveReq(adminMux, httptest.NewRequest("GET", "/api/requests/nonexistent/gcode", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestRequestsStatusViewedToFinalized(t *testing.T) {
	dir := t.TempDir()
	adminMux, publicMux, _, _ := newRequestsTestSetup(t, dir)
	id := seedQuoteRequest(t, publicMux, "PLA")

	getRequest(t, adminMux, id) // new -> viewed

	rec := setRequestStatus(t, adminMux, id, "finalized")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	var req QuoteRequest
	if err := json.Unmarshal(rec.Body.Bytes(), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.Status != "finalized" {
		t.Fatalf("status = %q, want finalized", req.Status)
	}
}

func TestRequestsStatusNewToFinalizedIs409(t *testing.T) {
	dir := t.TempDir()
	adminMux, publicMux, _, _ := newRequestsTestSetup(t, dir)
	id := seedQuoteRequest(t, publicMux, "PLA") // still "new": never GET'd

	rec := setRequestStatus(t, adminMux, id, "finalized")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409, body = %s", rec.Code, rec.Body.String())
	}
}

func TestRequestsDeleteRemovesFile(t *testing.T) {
	dir := t.TempDir()
	adminMux, publicMux, db, _ := newRequestsTestSetup(t, dir)
	id := seedQuoteRequest(t, publicMux, "PLA")

	var gcodePath string
	if err := db.QueryRow(`SELECT gcode_path FROM quote_requests WHERE id = ?`, id).Scan(&gcodePath); err != nil {
		t.Fatalf("query gcode_path: %v", err)
	}
	if _, err := os.Stat(gcodePath); err != nil {
		t.Fatalf("stored file missing before delete: %v", err)
	}

	rec := deleteRequest(t, adminMux, id)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body = %s", rec.Code, rec.Body.String())
	}

	if _, err := os.Stat(gcodePath); !os.IsNotExist(err) {
		t.Fatalf("stored file still present after delete (err = %v)", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM quote_requests WHERE id = ?`, id).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("row still present after delete")
	}

	// A second delete is a 404, not a 500 or a panic.
	rec = deleteRequest(t, adminMux, id)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("second delete status = %d, want 404", rec.Code)
	}
}

// insertItemOrderRequest inserts a row shaped like the public item-order INSERT directly at status, bypassing "new" and the HTTP path, so stock-decrement tests can start at "viewed" without a GET.
func insertItemOrderRequest(t *testing.T, db *sql.DB, itemID, itemName string, quantity int, status string) string {
	t.Helper()
	id := newID()
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(`INSERT INTO quote_requests
	  (id, status, kind, name, email, note, material, quantity, weight_g, time_seconds,
	   price_cents, gcode_filename, gcode_path, gcode_bytes, created_at,
	   item_id, item_name, option_name)
	  VALUES (?, ?, 'item', 'Buyer', 'buyer@example.com', '', '', ?, 0, 0, 0, '', '', 0, ?, ?, ?, '')`,
		id, status, quantity, now, itemID, itemName)
	if err != nil {
		t.Fatalf("insert item order request: %v", err)
	}
	return id
}

// itemStock reads the current stock_mode/stock_qty for one item row directly,
// so tests can assert what finalizing an order did (or didn't) to it without
// going back through the admin items API.
func itemStock(t *testing.T, db *sql.DB, itemID string) (mode string, qty int) {
	t.Helper()
	if err := db.QueryRow(`SELECT stock_mode, stock_qty FROM items WHERE id = ?`, itemID).Scan(&mode, &qty); err != nil {
		t.Fatalf("query item stock: %v", err)
	}
	return mode, qty
}

// Stock-decrement-on-finalize: a kind 'item' finalize against an in_stock item decrements stock_qty by the quantity, floored at zero;
// a ready_to_print item row, a kind 'quote' finalize, and finalizing against a since-deleted item (zero-row UPDATE, not an error) are unaffected.
func TestFinalizeItemOrderDecrementsStock(t *testing.T) {
	dir := t.TempDir()
	adminMux, publicMux, db, _ := newRequestsTestSetup(t, dir)

	// in_stock item, qty 5.
	rec := postItem(t, adminMux, `{"name":"Benchy","priceCents":1500,"public":true,`+
		`"stockMode":"in_stock","stockQty":5}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST in_stock item want 201, got %d %s", rec.Code, rec.Body.String())
	}
	var inStock Item
	if err := json.Unmarshal(rec.Body.Bytes(), &inStock); err != nil {
		t.Fatalf("unmarshal in_stock item: %v", err)
	}

	// Order quantity 2, seeded at "viewed" (the transition this endpoint
	// requires).
	id := insertItemOrderRequest(t, db, inStock.ID, inStock.Name, 2, "viewed")
	rec = setRequestStatus(t, adminMux, id, "finalized")
	if rec.Code != http.StatusOK {
		t.Fatalf("finalize want 200, got %d %s", rec.Code, rec.Body.String())
	}
	if _, qty := itemStock(t, db, inStock.ID); qty != 3 {
		t.Fatalf("stock_qty after first finalize = %d, want 3", qty)
	}

	// A second order for 9 (more than the 3 remaining) floors at zero
	// rather than going negative.
	id = insertItemOrderRequest(t, db, inStock.ID, inStock.Name, 9, "viewed")
	rec = setRequestStatus(t, adminMux, id, "finalized")
	if rec.Code != http.StatusOK {
		t.Fatalf("finalize want 200, got %d %s", rec.Code, rec.Body.String())
	}
	if _, qty := itemStock(t, db, inStock.ID); qty != 0 {
		t.Fatalf("stock_qty after second finalize = %d, want 0 (floored)", qty)
	}

	// ready_to_print item: finalize changes nothing. stockQty is set to 7 (not 0) so deleting the UPDATE's "AND stock_mode = ?" guard would fail this assertion instead of passing via the CASE's ELSE 0.
	rec = postItem(t, adminMux, `{"name":"Widget","priceCents":500,"public":true,`+
		`"stockMode":"ready_to_print","stockQty":7}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST ready_to_print item want 201, got %d %s", rec.Code, rec.Body.String())
	}
	var readyToPrint Item
	if err := json.Unmarshal(rec.Body.Bytes(), &readyToPrint); err != nil {
		t.Fatalf("unmarshal ready_to_print item: %v", err)
	}
	id = insertItemOrderRequest(t, db, readyToPrint.ID, readyToPrint.Name, 4, "viewed")
	rec = setRequestStatus(t, adminMux, id, "finalized")
	if rec.Code != http.StatusOK {
		t.Fatalf("finalize want 200, got %d %s", rec.Code, rec.Body.String())
	}
	mode, qty := itemStock(t, db, readyToPrint.ID)
	if mode != stockModeReadyToPrint || qty != 7 {
		t.Fatalf("ready_to_print item stock = {%q %d}, want unchanged {%q 7}", mode, qty, stockModeReadyToPrint)
	}

	// kind 'quote' request: finalize path unchanged (no item touched).
	quoteID := seedQuoteRequest(t, publicMux, "PLA")
	getRequest(t, adminMux, quoteID) // new -> viewed
	rec = setRequestStatus(t, adminMux, quoteID, "finalized")
	if rec.Code != http.StatusOK {
		t.Fatalf("finalize quote request want 200, got %d %s", rec.Code, rec.Body.String())
	}

	// Deleted item (item_id points nowhere): finalize still succeeds, no
	// error, and no row is created.
	id = insertItemOrderRequest(t, db, "nonexistent-item-id", "Ghost", 1, "viewed")
	rec = setRequestStatus(t, adminMux, id, "finalized")
	if rec.Code != http.StatusOK {
		t.Fatalf("finalize deleted-item order want 200, got %d %s", rec.Code, rec.Body.String())
	}
}

// GET /api/requests/storage must report {"usedBytes":...,"capBytes":...} (the web client's exact names), usedBytes must reflect visitor uploads,
// and bytes under uploads/items (admin item images, a separate budget) must not count.
func TestRequestsStorageEndpoint(t *testing.T) {
	dir := t.TempDir()
	adminMux, publicMux, _, _ := newRequestsTestSetup(t, dir)

	content := []byte("G1 X1 Y1\nG1 X2 Y2\n")
	seedQuoteRequest(t, publicMux, "PLA") // uses "model.gcode" / 9 bytes, see seedQuoteRequest
	rec := postPublicQuoteRequest(t, publicMux, publicQuoteRequestFields("PLA"), "part.gcode", content)
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// An admin-side item-gallery image under uploads/items must not count
	// toward this endpoint's usedBytes.
	itemsDir := filepath.Join(dir, "uploads", "items")
	if err := os.MkdirAll(itemsDir, 0o755); err != nil {
		t.Fatalf("mkdir uploads/items: %v", err)
	}
	if err := os.WriteFile(filepath.Join(itemsDir, "gallery.jpg"), []byte("a much longer item-gallery image payload than either gcode upload"), 0o644); err != nil {
		t.Fatalf("write item image: %v", err)
	}

	wantUsed, err := dirSize(filepath.Join(dir, "uploads"))
	if err != nil {
		t.Fatalf("dirSize: %v", err)
	}

	rec = serveReq(adminMux, httptest.NewRequest("GET", "/api/requests/storage", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}

	var body struct {
		UsedBytes int64 `json:"usedBytes"`
		CapBytes  int64 `json:"capBytes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.CapBytes != publicUploadsCapBytes {
		t.Errorf("capBytes = %d, want %d", body.CapBytes, publicUploadsCapBytes)
	}
	if body.UsedBytes != wantUsed {
		t.Errorf("usedBytes = %d, want %d (dirSize over uploads/, excluding uploads/items)", body.UsedBytes, wantUsed)
	}
	if body.UsedBytes <= 0 {
		t.Fatalf("usedBytes = %d, want > 0 (two real gcode uploads were seeded)", body.UsedBytes)
	}
}

// ---------------------------------------------------------------------------
// DELETE /api/requests/new (bulk-delete new requests)
// ---------------------------------------------------------------------------

// The bulk-delete endpoint removes every "new" row and its stored file, leaves "viewed" and "finalized" rows and files untouched, and reports how many rows it removed.
func TestRequestsDeleteNewRemovesOnlyStatusNewRowsAndFiles(t *testing.T) {
	dir := t.TempDir()
	adminMux, publicMux, db, _ := newRequestsTestSetup(t, dir)

	// Two "new" requests: one with a stored file, one without; a fileless submission is the flood scenario, so the endpoint must not assume every row has a file to remove.
	newWithFile := seedQuoteRequest(t, publicMux, "PLA")
	fileless := postPublicQuoteRequest(t, publicMux, publicQuoteRequestFields("PLA"), "", nil)
	if fileless.Code != http.StatusCreated {
		t.Fatalf("seed fileless: status = %d, body = %s", fileless.Code, fileless.Body.String())
	}
	var filelessResp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(fileless.Body.Bytes(), &filelessResp); err != nil {
		t.Fatalf("unmarshal fileless seed response: %v", err)
	}
	newFileless := filelessResp.ID

	// A "viewed" request (GET flips new -> viewed) with a stored file must
	// survive the purge untouched, file included.
	viewedID := seedQuoteRequest(t, publicMux, "PLA")
	if rec := getRequest(t, adminMux, viewedID); rec.Code != http.StatusOK {
		t.Fatalf("mark viewed: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// A "finalized" request (viewed -> finalized) with a stored file must
	// also survive untouched.
	finalizedID := seedQuoteRequest(t, publicMux, "PLA")
	if rec := getRequest(t, adminMux, finalizedID); rec.Code != http.StatusOK {
		t.Fatalf("mark viewed: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec := setRequestStatus(t, adminMux, finalizedID, "finalized"); rec.Code != http.StatusOK {
		t.Fatalf("finalize: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var viewedPath, finalizedPath, newWithFilePath string
	if err := db.QueryRow(`SELECT gcode_path FROM quote_requests WHERE id = ?`, viewedID).Scan(&viewedPath); err != nil {
		t.Fatalf("query viewed gcode_path: %v", err)
	}
	if err := db.QueryRow(`SELECT gcode_path FROM quote_requests WHERE id = ?`, finalizedID).Scan(&finalizedPath); err != nil {
		t.Fatalf("query finalized gcode_path: %v", err)
	}
	if err := db.QueryRow(`SELECT gcode_path FROM quote_requests WHERE id = ?`, newWithFile).Scan(&newWithFilePath); err != nil {
		t.Fatalf("query new gcode_path: %v", err)
	}
	for _, p := range []string{viewedPath, finalizedPath, newWithFilePath} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("stored file missing before purge (%s): %v", p, err)
		}
	}

	rec := deleteNewRequests(t, adminMux)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		DeletedCount int64 `json:"deletedCount"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.DeletedCount != 2 {
		t.Fatalf("deletedCount = %d, want 2", body.DeletedCount)
	}

	// The two "new" rows, and the one file among them, are gone.
	for _, id := range []string{newWithFile, newFileless} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM quote_requests WHERE id = ?`, id).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", id, err)
		}
		if count != 0 {
			t.Fatalf("row %s still present after purge", id)
		}
	}
	if _, err := os.Stat(newWithFilePath); !os.IsNotExist(err) {
		t.Fatalf("new request's stored file still present after purge (err = %v)", err)
	}

	// The "viewed" and "finalized" rows and their files are untouched.
	for _, tc := range []struct{ id, path string }{{viewedID, viewedPath}, {finalizedID, finalizedPath}} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM quote_requests WHERE id = ?`, tc.id).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", tc.id, err)
		}
		if count != 1 {
			t.Fatalf("row %s missing after purge, want untouched", tc.id)
		}
		if _, err := os.Stat(tc.path); err != nil {
			t.Fatalf("stored file for %s missing after purge, want untouched: %v", tc.id, err)
		}
	}

	// A second purge with nothing left "new" reports 0, not an error.
	rec = deleteNewRequests(t, adminMux)
	if rec.Code != http.StatusOK {
		t.Fatalf("second purge status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	body.DeletedCount = -1
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.DeletedCount != 0 {
		t.Fatalf("second purge deletedCount = %d, want 0", body.DeletedCount)
	}
}

// demoWriteGuard covers this endpoint like every unsafe /api/ write (not in demoWriteGuardExemptPaths): a demo DELETE gets the guard's 403 before the handler and the row survives.
// The guard only runs inside auth.middleware, so wrap the mux as TestDemoWriteGuardBlocksWriteBeforeCSRFCheck does; calling registerRoutes's mux directly would bypass it and pass for the wrong reason.
func TestRequestsDeleteNewIsBlockedInDemoMode(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	cfg := newPublicTestConfig(t, dir)
	store := NewConfigStore(cfg)

	auth := newAuthState("secret", nil)
	auth.demoMode = true
	rawMux := routesMux(db, store, auth, dir, true) // demoMode true
	adminMux := auth.middleware(rawMux)
	publicMux, _ := buildPublicMux(store, db, dir, false, false) // seed with demoMode false

	id := seedQuoteRequest(t, publicMux, "PLA")

	req := httptest.NewRequest("DELETE", "/api/requests/new", nil)
	req.Host = "localhost"
	rec := serveReq(adminMux, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body = %s", rec.Code, rec.Body.String())
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM quote_requests WHERE id = ?`, id).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("row count = %d, want 1 (demo mode must block the delete)", count)
	}
}

// Attribution (identity.go): quote_requests has no created_by (the public listener has no identity and writes ""); updated_by moves only on handleGet's new->viewed and handleStatus's viewed->finalized.

func TestPublicRequestCreationLeavesUpdatedByEmpty(t *testing.T) {
	dir := t.TempDir()
	_, publicMux, db, _ := newRequestsTestSetup(t, dir)
	id := seedQuoteRequest(t, publicMux, "PLA")

	var updatedBy string
	if err := db.QueryRow(`SELECT updated_by FROM quote_requests WHERE id = ?`, id).Scan(&updatedBy); err != nil {
		t.Fatalf("query updated_by: %v", err)
	}
	if updatedBy != "" {
		t.Fatalf("updated_by after public creation = %q, want empty (the public listener has no identity to stamp)", updatedBy)
	}
}

func TestRequestsGetAndStatusStampUpdatedByFromActingIdentity(t *testing.T) {
	dir := t.TempDir()
	adminMux, publicMux, db, _ := newRequestsTestSetup(t, dir)
	if _, _, err := createUser(db, "flo", "password123", []string{"requests"}, ""); err != nil {
		t.Fatalf("createUser flo: %v", err)
	}
	if _, _, err := createUser(db, "bob", "password123", []string{"requests"}, ""); err != nil {
		t.Fatalf("createUser bob: %v", err)
	}
	id := seedQuoteRequest(t, publicMux, "PLA")

	getReq := withCtxIdentity(httptest.NewRequest("GET", "/api/requests/"+id, nil), identity{username: "flo"})
	getRec := serveReq(adminMux, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET: status = %d, body %s", getRec.Code, getRec.Body.String())
	}
	var viewed QuoteRequest
	if err := json.Unmarshal(getRec.Body.Bytes(), &viewed); err != nil {
		t.Fatalf("unmarshal GET: %v", err)
	}
	if viewed.Status != "viewed" || viewed.UpdatedBy != "flo" {
		t.Fatalf("after GET: status=%q updatedBy=%q, want viewed/%q", viewed.Status, viewed.UpdatedBy, "flo")
	}

	statusReq := withCtxIdentity(httptest.NewRequest("POST", "/api/requests/"+id+"/status", strings.NewReader(`{"status":"finalized"}`)), identity{username: "bob"})
	statusRec := serveReq(adminMux, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("status: status = %d, body %s", statusRec.Code, statusRec.Body.String())
	}
	var finalized QuoteRequest
	if err := json.Unmarshal(statusRec.Body.Bytes(), &finalized); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	if finalized.Status != "finalized" || finalized.UpdatedBy != "bob" {
		t.Fatalf("after finalize: status=%q updatedBy=%q, want finalized/%q", finalized.Status, finalized.UpdatedBy, "bob")
	}
}
