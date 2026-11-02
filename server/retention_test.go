package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// backdateFinalized sets quote_requests.finalized_at directly, to simulate a finalize daysAgo days ago without waiting out requestGcodeRetentionGraceDays.
func backdateFinalized(t *testing.T, db *sql.DB, id string, daysAgo int) {
	t.Helper()
	ts := time.Now().UTC().Add(-time.Duration(daysAgo) * 24 * time.Hour).Format(time.RFC3339)
	if _, err := db.Exec(`UPDATE quote_requests SET finalized_at = ? WHERE id = ?`, ts, id); err != nil {
		t.Fatalf("backdate finalized_at: %v", err)
	}
}

// A finalized request past the grace period has its file deleted, gcode_path cleared, and gcode_pruned_at stamped, while gcode_filename/gcode_bytes survive.
func TestPruneFinalizedGcodeRemovesPastGrace(t *testing.T) {
	dir := t.TempDir()
	adminMux, publicMux, db, _ := newRequestsTestSetup(t, dir)
	id := seedQuoteRequest(t, publicMux, "PLA")
	getRequest(t, adminMux, id)                    // new -> viewed
	setRequestStatus(t, adminMux, id, "finalized") // viewed -> finalized, sets finalized_at
	backdateFinalized(t, db, id, requestGcodeRetentionGraceDays+1)

	var gcodePath, gcodeFilename string
	var gcodeBytes int
	if err := db.QueryRow(`SELECT gcode_path, gcode_filename, gcode_bytes FROM quote_requests WHERE id = ?`, id).Scan(&gcodePath, &gcodeFilename, &gcodeBytes); err != nil {
		t.Fatalf("read before prune: %v", err)
	}
	if _, err := os.Stat(gcodePath); err != nil {
		t.Fatalf("file missing before prune: %v", err)
	}

	pruned, err := pruneFinalizedGcode(db)
	if err != nil {
		t.Fatalf("pruneFinalizedGcode: %v", err)
	}
	if pruned != 1 {
		t.Fatalf("pruned = %d, want 1", pruned)
	}

	if _, err := os.Stat(gcodePath); !os.IsNotExist(err) {
		t.Fatalf("file still present after prune (err = %v)", err)
	}

	var req QuoteRequest
	rec := getRequest(t, adminMux, id)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET request after prune: %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.GcodePath != "" {
		t.Errorf("gcodePath = %q, want empty after prune", req.GcodePath)
	}
	if req.GcodePrunedAt == "" {
		t.Errorf("gcodePrunedAt is empty, want a timestamp after prune")
	}
	if req.GcodeFilename != gcodeFilename {
		t.Errorf("gcodeFilename = %q, want %q (metadata must survive pruning)", req.GcodeFilename, gcodeFilename)
	}
	if req.GcodeBytes != gcodeBytes {
		t.Errorf("gcodeBytes = %d, want %d (metadata must survive pruning)", req.GcodeBytes, gcodeBytes)
	}
	if req.Status != "finalized" {
		t.Errorf("status = %q, want finalized (pruning must not touch status)", req.Status)
	}
}

// TestPruneFinalizedGcodeKeepsWithinGrace: a finalized request still inside
// its grace period keeps its file untouched.
func TestPruneFinalizedGcodeKeepsWithinGrace(t *testing.T) {
	dir := t.TempDir()
	adminMux, publicMux, db, _ := newRequestsTestSetup(t, dir)
	id := seedQuoteRequest(t, publicMux, "PLA")
	getRequest(t, adminMux, id)
	setRequestStatus(t, adminMux, id, "finalized") // finalized_at = now, well within grace

	var gcodePath string
	if err := db.QueryRow(`SELECT gcode_path FROM quote_requests WHERE id = ?`, id).Scan(&gcodePath); err != nil {
		t.Fatalf("read gcode_path: %v", err)
	}

	pruned, err := pruneFinalizedGcode(db)
	if err != nil {
		t.Fatalf("pruneFinalizedGcode: %v", err)
	}
	if pruned != 0 {
		t.Fatalf("pruned = %d, want 0 (still within grace period)", pruned)
	}
	if _, err := os.Stat(gcodePath); err != nil {
		t.Fatalf("file should still exist within grace period: %v", err)
	}

	var gcodePrunedAt string
	if err := db.QueryRow(`SELECT gcode_pruned_at FROM quote_requests WHERE id = ?`, id).Scan(&gcodePrunedAt); err != nil {
		t.Fatalf("read gcode_pruned_at: %v", err)
	}
	if gcodePrunedAt != "" {
		t.Errorf("gcode_pruned_at = %q, want empty (not pruned)", gcodePrunedAt)
	}
}

// seedFinalizedUpload submits a public request carrying filename/content, marks it viewed then finalized via adminMux, and backdates finalized_at by daysAgo; shared setup for the grace tests.
func seedFinalizedUpload(t *testing.T, adminMux, publicMux *http.ServeMux, db *sql.DB, filename string, content []byte, daysAgo int) (id, gcodePath string) {
	t.Helper()
	rec := postPublicQuoteRequest(t, publicMux, publicQuoteRequestFields("PLA"), filename, content)
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed submission: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal seed response: %v", err)
	}
	id = resp.ID

	getRequest(t, adminMux, id)                    // new -> viewed
	setRequestStatus(t, adminMux, id, "finalized") // viewed -> finalized
	backdateFinalized(t, db, id, daysAgo)

	if err := db.QueryRow(`SELECT gcode_path FROM quote_requests WHERE id = ?`, id).Scan(&gcodePath); err != nil {
		t.Fatalf("read gcode_path: %v", err)
	}
	return id, gcodePath
}

// Model uploads get a longer finite grace: an uppercase .STL (also exercising the case-insensitive match) at 31 days is past the .gcode grace (requestGcodeRetentionGraceDays, 30)
// but inside the model grace (requestModelRetentionGraceDays, 180), so it survives; the admin delete path (handleDelete) still removes it at any age.
func TestPruneFinalizedModelFileSurvivesPast30DayGcodeGraceButWithin180DayModelGrace(t *testing.T) {
	dir := t.TempDir()
	adminMux, publicMux, db, _ := newRequestsTestSetup(t, dir)

	id, gcodePath := seedFinalizedUpload(t, adminMux, publicMux, db, "model.STL", []byte("solid test\n"), requestGcodeRetentionGraceDays+1)

	pruned, err := pruneFinalizedGcode(db)
	if err != nil {
		t.Fatalf("pruneFinalizedGcode: %v", err)
	}
	if pruned != 0 {
		t.Fatalf("pruned = %d, want 0 (31 days is past the .gcode grace period but within the model grace period)", pruned)
	}
	if _, err := os.Stat(gcodePath); err != nil {
		t.Fatalf("STL file should survive at 31 days: %v", err)
	}

	var gotPrunedAt string
	if err := db.QueryRow(`SELECT gcode_pruned_at FROM quote_requests WHERE id = ?`, id).Scan(&gotPrunedAt); err != nil {
		t.Fatalf("read gcode_pruned_at: %v", err)
	}
	if gotPrunedAt != "" {
		t.Errorf("gcode_pruned_at = %q, want empty (not pruned)", gotPrunedAt)
	}
}

// Past requestModelRetentionGraceDays (180) a model upload IS auto-pruned; without that, the 2 GiB uploads cap (publicUploadsCapBytes) could wedge shut once full of undeletable model files.
func TestPruneFinalizedModelFilePrunedPast180DayGrace(t *testing.T) {
	dir := t.TempDir()
	adminMux, publicMux, db, _ := newRequestsTestSetup(t, dir)

	id, gcodePath := seedFinalizedUpload(t, adminMux, publicMux, db, "model.stl", []byte("solid test\n"), requestModelRetentionGraceDays+1)

	pruned, err := pruneFinalizedGcode(db)
	if err != nil {
		t.Fatalf("pruneFinalizedGcode: %v", err)
	}
	if pruned != 1 {
		t.Fatalf("pruned = %d, want 1 (181 days is past the model grace period)", pruned)
	}
	if _, err := os.Stat(gcodePath); !os.IsNotExist(err) {
		t.Fatalf("STL file should be removed past 181 days (err = %v)", err)
	}

	var gotPath, gotPrunedAt string
	if err := db.QueryRow(`SELECT gcode_path, gcode_pruned_at FROM quote_requests WHERE id = ?`, id).Scan(&gotPath, &gotPrunedAt); err != nil {
		t.Fatalf("read after prune: %v", err)
	}
	if gotPath != "" {
		t.Errorf("gcode_path = %q, want cleared after prune", gotPath)
	}
	if gotPrunedAt == "" {
		t.Error("gcode_pruned_at is empty, want a timestamp after prune")
	}
}

// Uppercase ".GCODE" is still real slicer output, not a model file, and must be pruned like the lowercase case.
func TestPruneFinalizedGcodeRemovesUppercaseGcodeExtension(t *testing.T) {
	dir := t.TempDir()
	adminMux, publicMux, db, _ := newRequestsTestSetup(t, dir)

	rec := postPublicQuoteRequest(t, publicMux, publicQuoteRequestFields("PLA"), "MODEL.GCODE", []byte("G1 X1 Y1\n"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed submission: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal seed response: %v", err)
	}
	id := resp.ID

	getRequest(t, adminMux, id)
	setRequestStatus(t, adminMux, id, "finalized")
	backdateFinalized(t, db, id, requestGcodeRetentionGraceDays+1)

	pruned, err := pruneFinalizedGcode(db)
	if err != nil {
		t.Fatalf("pruneFinalizedGcode: %v", err)
	}
	if pruned != 1 {
		t.Fatalf("pruned = %d, want 1 (uppercase .GCODE must match case-insensitively)", pruned)
	}
}

// TestPruneFinalizedGcodeKeepsOpenRequests: "new"/"viewed" requests are never
// eligible, no matter how old, since the visitor's file may still be needed
// to produce the quote that would finalize the request.
func TestPruneFinalizedGcodeKeepsOpenRequests(t *testing.T) {
	dir := t.TempDir()
	adminMux, publicMux, db, _ := newRequestsTestSetup(t, dir)
	newID := seedQuoteRequest(t, publicMux, "PLA") // left "new"
	viewedID := seedQuoteRequest(t, publicMux, "PLA")
	getRequest(t, adminMux, viewedID) // new -> viewed

	// Backdate created_at on both far past the grace period: age alone must
	// never make an open request eligible.
	old := time.Now().UTC().Add(-time.Duration(requestGcodeRetentionGraceDays+365) * 24 * time.Hour).Format(time.RFC3339)
	for _, id := range []string{newID, viewedID} {
		if _, err := db.Exec(`UPDATE quote_requests SET created_at = ? WHERE id = ?`, old, id); err != nil {
			t.Fatalf("backdate created_at: %v", err)
		}
	}

	var pathNew, pathViewed string
	db.QueryRow(`SELECT gcode_path FROM quote_requests WHERE id = ?`, newID).Scan(&pathNew)
	db.QueryRow(`SELECT gcode_path FROM quote_requests WHERE id = ?`, viewedID).Scan(&pathViewed)

	pruned, err := pruneFinalizedGcode(db)
	if err != nil {
		t.Fatalf("pruneFinalizedGcode: %v", err)
	}
	if pruned != 0 {
		t.Fatalf("pruned = %d, want 0 (no finalized requests)", pruned)
	}
	for _, p := range []string{pathNew, pathViewed} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("open request's file should survive: %v", err)
		}
	}
}

// TestGcodeDownloadReturns410AfterPrune: once a file has been pruned, the
// admin download endpoint must report 410 Gone with an explanation, not the
// 404 it uses for "no such request"/"no file was ever uploaded".
func TestGcodeDownloadReturns410AfterPrune(t *testing.T) {
	dir := t.TempDir()
	adminMux, publicMux, db, _ := newRequestsTestSetup(t, dir)
	id := seedQuoteRequest(t, publicMux, "PLA")
	getRequest(t, adminMux, id)
	setRequestStatus(t, adminMux, id, "finalized")
	backdateFinalized(t, db, id, requestGcodeRetentionGraceDays+1)

	if _, err := pruneFinalizedGcode(db); err != nil {
		t.Fatalf("pruneFinalizedGcode: %v", err)
	}

	rec := serveReq(adminMux, httptest.NewRequest("GET", "/api/requests/"+id+"/gcode", nil))
	if rec.Code != http.StatusGone {
		t.Fatalf("status = %d, want 410, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "pruned") {
		t.Errorf("410 body should explain the file was pruned, got %s", rec.Body.String())
	}
}

// TestRunRetentionLoopStopsCleanlyOnContextCancel exercises requirement (d):
// the loop must run its startup pass and then shut down cleanly when its
// context is canceled, without leaking the goroutine.
func TestRunRetentionLoopStopsCleanlyOnContextCancel(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)

	ctx, cancel := context.WithCancel(context.Background())
	done := runRetentionLoop(ctx, db, dir)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runRetentionLoop did not stop within 5s of context cancellation")
	}
}

// ---------------------------------------------------------------------------
// pruneFinalizedGcode: file merely missing vs. actually pruned
// ---------------------------------------------------------------------------

// A merely ABSENT file (db-only restore, moved CONFIG_DIR, empty-mounted volume) must never count as a successful prune: the row stays eligible for retry (gcode_pruned_at unset, gcode_path untouched),
// or GET .../gcode would lie with a permanent 410 "intentionally deleted".
func TestPruneFinalizedGcodeSkipsWhenFileMissing(t *testing.T) {
	dir := t.TempDir()
	adminMux, publicMux, db, _ := newRequestsTestSetup(t, dir)
	id := seedQuoteRequest(t, publicMux, "PLA")
	getRequest(t, adminMux, id)
	setRequestStatus(t, adminMux, id, "finalized")
	backdateFinalized(t, db, id, requestGcodeRetentionGraceDays+1)

	var gcodePath string
	if err := db.QueryRow(`SELECT gcode_path FROM quote_requests WHERE id = ?`, id).Scan(&gcodePath); err != nil {
		t.Fatalf("read gcode_path: %v", err)
	}
	// Simulate the file being gone for a reason OTHER than
	// pruneFinalizedGcode itself having removed it.
	if err := os.Remove(gcodePath); err != nil {
		t.Fatalf("simulate missing file: %v", err)
	}

	var buf bytes.Buffer
	restore := redirectLog(t, &buf)
	defer restore()

	pruned, err := pruneFinalizedGcode(db)
	if err != nil {
		t.Fatalf("pruneFinalizedGcode: %v", err)
	}
	if pruned != 0 {
		t.Fatalf("pruned = %d, want 0 (a missing file is not a successful prune)", pruned)
	}
	if !strings.Contains(buf.String(), "missing") || !strings.Contains(buf.String(), gcodePath) {
		t.Errorf("log output = %q, want a warning naming the missing path %q", buf.String(), gcodePath)
	}

	var gotPath, gotPrunedAt string
	if err := db.QueryRow(`SELECT gcode_path, gcode_pruned_at FROM quote_requests WHERE id = ?`, id).Scan(&gotPath, &gotPrunedAt); err != nil {
		t.Fatalf("read after prune attempt: %v", err)
	}
	if gotPath != gcodePath {
		t.Errorf("gcode_path = %q, want unchanged %q (must not clear the association when the file is merely missing)", gotPath, gcodePath)
	}
	if gotPrunedAt != "" {
		t.Errorf("gcode_pruned_at = %q, want empty (row must remain eligible for a later retry)", gotPrunedAt)
	}
}

// Positive-path counterpart to the missing-file test, guarding against a fix that stops stamping real prunes; an explicit "both branches" pairing.
func TestPruneFinalizedGcodeRealDeleteStillStampsPruned(t *testing.T) {
	dir := t.TempDir()
	adminMux, publicMux, db, _ := newRequestsTestSetup(t, dir)
	id := seedQuoteRequest(t, publicMux, "PLA")
	getRequest(t, adminMux, id)
	setRequestStatus(t, adminMux, id, "finalized")
	backdateFinalized(t, db, id, requestGcodeRetentionGraceDays+1)

	pruned, err := pruneFinalizedGcode(db)
	if err != nil {
		t.Fatalf("pruneFinalizedGcode: %v", err)
	}
	if pruned != 1 {
		t.Fatalf("pruned = %d, want 1 (the file genuinely existed and was removed)", pruned)
	}

	var gotPath, gotPrunedAt string
	if err := db.QueryRow(`SELECT gcode_path, gcode_pruned_at FROM quote_requests WHERE id = ?`, id).Scan(&gotPath, &gotPrunedAt); err != nil {
		t.Fatalf("read after prune: %v", err)
	}
	if gotPath != "" {
		t.Errorf("gcode_path = %q, want cleared after a real prune", gotPath)
	}
	if gotPrunedAt == "" {
		t.Error("gcode_pruned_at is empty, want a timestamp after a real prune")
	}
}

// ---------------------------------------------------------------------------
// sweepOrphanedUploads
// ---------------------------------------------------------------------------

// A gcode.gz with no matching quote_requests.gcode_path row, older than orphanSweepGrace, is removed and counted.
func TestSweepOrphanedUploadsRemovesOldUnreferencedFile(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)

	uploadsDir := filepath.Join(dir, "uploads")
	if err := os.MkdirAll(uploadsDir, 0o755); err != nil {
		t.Fatalf("mkdir uploads: %v", err)
	}
	orphan := filepath.Join(uploadsDir, "orphan-1.gcode.gz")
	if err := os.WriteFile(orphan, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed orphan file: %v", err)
	}
	old := time.Now().Add(-orphanSweepGrace - time.Hour)
	if err := os.Chtimes(orphan, old, old); err != nil {
		t.Fatalf("backdate mtime: %v", err)
	}

	// Item images live in uploads/items/, which sweepOrphanedUploads never descends into (it walks only the flat uploads/ dir for *.gcode.gz); a retention pass must leave this old file alone.
	itemImagesDir := filepath.Join(uploadsDir, "items")
	if err := os.MkdirAll(itemImagesDir, 0o755); err != nil {
		t.Fatalf("mkdir uploads/items: %v", err)
	}
	itemImage := filepath.Join(itemImagesDir, "0123456789abcdef.png")
	if err := os.WriteFile(itemImage, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed item image file: %v", err)
	}
	if err := os.Chtimes(itemImage, old, old); err != nil {
		t.Fatalf("backdate item image mtime: %v", err)
	}

	removed, err := sweepOrphanedUploads(db, dir)
	if err != nil {
		t.Fatalf("sweepOrphanedUploads: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, statErr := os.Stat(orphan); !os.IsNotExist(statErr) {
		t.Fatalf("orphan file still present after sweep (err = %v)", statErr)
	}
	if _, statErr := os.Stat(itemImage); statErr != nil {
		t.Fatalf("item image should survive a retention pass: %v", statErr)
	}
}

// An unreferenced file younger than orphanSweepGrace is left alone: it may be mid-upload (written by writeGcodePart, INSERT not yet committed).
func TestSweepOrphanedUploadsKeepsRecentFile(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)

	uploadsDir := filepath.Join(dir, "uploads")
	if err := os.MkdirAll(uploadsDir, 0o755); err != nil {
		t.Fatalf("mkdir uploads: %v", err)
	}
	recent := filepath.Join(uploadsDir, "in-flight.gcode.gz")
	if err := os.WriteFile(recent, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed recent file: %v", err)
	}

	removed, err := sweepOrphanedUploads(db, dir)
	if err != nil {
		t.Fatalf("sweepOrphanedUploads: %v", err)
	}
	if removed != 0 {
		t.Fatalf("removed = %d, want 0 (file is within the grace window)", removed)
	}
	if _, statErr := os.Stat(recent); statErr != nil {
		t.Fatalf("recent file should survive: %v", statErr)
	}
}

// TestSweepOrphanedUploadsKeepsReferencedFile: a file whose basename matches
// a live quote_requests.gcode_path is never removed, no matter how old.
func TestSweepOrphanedUploadsKeepsReferencedFile(t *testing.T) {
	dir := t.TempDir()
	_, publicMux, db, _ := newRequestsTestSetup(t, dir)
	id := seedQuoteRequest(t, publicMux, "PLA")

	var gcodePath string
	if err := db.QueryRow(`SELECT gcode_path FROM quote_requests WHERE id = ?`, id).Scan(&gcodePath); err != nil {
		t.Fatalf("read gcode_path: %v", err)
	}
	old := time.Now().Add(-orphanSweepGrace - time.Hour)
	if err := os.Chtimes(gcodePath, old, old); err != nil {
		t.Fatalf("backdate mtime: %v", err)
	}

	removed, err := sweepOrphanedUploads(db, dir)
	if err != nil {
		t.Fatalf("sweepOrphanedUploads: %v", err)
	}
	if removed != 0 {
		t.Fatalf("removed = %d, want 0 (file is referenced by a live row)", removed)
	}
	if _, statErr := os.Stat(gcodePath); statErr != nil {
		t.Fatalf("referenced file should survive: %v", statErr)
	}
}

// ---------------------------------------------------------------------------
// notifyQueue.stop
// ---------------------------------------------------------------------------

// TestNotifyQueueStopDrainsQueuedJobs pins that on shutdown, jobs
// still sitting in the channel must be drained (not simply discarded) within
// the budget stop is given.
func TestNotifyQueueStopDrainsQueuedJobs(t *testing.T) {
	var processed atomic.Int32
	nq := newNotifyQueueWithWorker(func(cfg *Config, req QuoteRequest) {
		processed.Add(1)
	})
	cfg := &Config{}
	for i := range 5 {
		nq.enqueue(cfg, QuoteRequest{ID: fmt.Sprintf("job-%d", i)})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	nq.stop(ctx)

	if got := processed.Load(); got != 5 {
		t.Fatalf("processed = %d, want 5 (stop must drain everything already queued)", got)
	}
}

// If the shutdown budget expires before the worker drains everything, the count still queued is logged so the loss is named.
func TestNotifyQueueStopLogsRemainingOnBudgetExpiry(t *testing.T) {
	blockCh := make(chan struct{})
	nq := newNotifyQueueWithWorker(func(cfg *Config, req QuoteRequest) {
		<-blockCh // never returns before the test unblocks it
	})
	// stop's budget expires WITHOUT waiting for the worker (the scenario under test), so unblock the still-running worker on cleanup to avoid leaking it.
	t.Cleanup(func() {
		close(blockCh) // let the stuck job (and anything queued behind it) finish
		<-nq.done      // wait for run() to fully exit
	})

	cfg := &Config{}
	nq.enqueue(cfg, QuoteRequest{ID: "stuck"})    // occupies the worker forever (until unblocked)
	nq.enqueue(cfg, QuoteRequest{ID: "queued-1"}) // never reached within the budget

	var buf bytes.Buffer
	restore := redirectLog(t, &buf)
	defer restore()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	nq.stop(ctx)

	if !strings.Contains(buf.String(), "shutdown budget expired") {
		t.Errorf("log output = %q, want a line naming the expired shutdown budget", buf.String())
	}
}

// A handler still running past the shared shutdown budget calls enqueue after stop closed ch; that must log a drop and return, not panic on a send to a closed channel.
func TestNotifyQueueEnqueueAfterStopDoesNotPanic(t *testing.T) {
	nq := newNotifyQueueWithWorker(func(cfg *Config, req QuoteRequest) {})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	nq.stop(ctx)

	var buf bytes.Buffer
	restore := redirectLog(t, &buf)
	defer restore()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("enqueue after stop panicked: %v", r)
		}
	}()
	nq.enqueue(&Config{}, QuoteRequest{ID: "late"})

	if !strings.Contains(buf.String(), "late") {
		t.Errorf("log output = %q, want a line naming the dropped request", buf.String())
	}
}

// Races enqueue against stop itself (a submitting goroutine vs main's shutdown goroutine closing the channel after Shutdown hit DeadlineExceeded). Meaningful mainly under -race.
func TestNotifyQueueConcurrentEnqueueAndStopDoesNotPanic(t *testing.T) {
	nq := newNotifyQueueWithWorker(func(cfg *Config, req QuoteRequest) {})

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("enqueue panicked racing stop: %v", r)
				}
			}()
			nq.enqueue(&Config{}, QuoteRequest{ID: fmt.Sprintf("racer-%d", i)})
		}(i)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	nq.stop(ctx)

	wg.Wait()
}
