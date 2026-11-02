package main

import (
	"context"
	"database/sql"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Retention: quote_requests rows are never auto-deleted (the durable record); only the upload is pruned,
// after a grace from finalized_at. ".gcode" (regenerable) gets 30 days; .stl/.3mf (the visitor's only copy)
// gets 180, finite so publicUploadsCapBytes drains. Open requests keep their files.
const requestGcodeRetentionGraceDays = 30

// requestModelRetentionGraceDays is the grace period for uploads that aren't G-code (see the file header).
const requestModelRetentionGraceDays = 180

// requestGcodeRetentionGrace is requestGcodeRetentionGraceDays as a Duration.
const requestGcodeRetentionGrace = requestGcodeRetentionGraceDays * 24 * time.Hour

// requestModelRetentionGrace is requestModelRetentionGraceDays as a Duration.
const requestModelRetentionGrace = requestModelRetentionGraceDays * 24 * time.Hour

// requestGcodeRetentionInterval is how often the loop re-scans after its startup pass.
const requestGcodeRetentionInterval = 24 * time.Hour

// pruneFinalizedGcode deletes finalized requests' uploads past their grace and stamps gcode_pruned_at, so
// GET /api/requests/{id}/gcode can tell "never uploaded" from "pruned". No row is ever deleted; a per-file
// failure is logged and skipped. The status filter uses the quote_requests_status index.
func pruneFinalizedGcode(db *sql.DB) (pruned int, err error) {
	gcodeCutoff := time.Now().UTC().Add(-requestGcodeRetentionGrace).Format(time.RFC3339)
	modelCutoff := time.Now().UTC().Add(-requestModelRetentionGrace).Format(time.RFC3339)

	// One query with two ORed branches: a row matches exactly one, so it reads as "which grace period
	// applies".
	rows, err := db.Query(`
		SELECT id, gcode_path FROM quote_requests
		WHERE status = 'finalized'
		  AND gcode_path != ''
		  AND gcode_pruned_at = ''
		  AND finalized_at != ''
		  AND (
		    (LOWER(gcode_filename) LIKE '%.gcode' AND finalized_at <= ?)
		    OR
		    (LOWER(gcode_filename) NOT LIKE '%.gcode' AND finalized_at <= ?)
		  )`, gcodeCutoff, modelCutoff)
	if err != nil {
		return 0, err
	}
	type candidate struct{ id, path string }
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if scanErr := rows.Scan(&c.id, &c.path); scanErr != nil {
			rows.Close()
			return 0, scanErr
		}
		candidates = append(candidates, c)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		rows.Close()
		return 0, rowsErr
	}
	rows.Close()

	now := time.Now().UTC().Format(time.RFC3339)
	for _, c := range candidates {
		// Check existence before touching the database: a missing uploads directory (restore order, moved
		// CONFIG_DIR) must not look like "we deleted it", or every request would be marked pruned on restart.
		// Leave the row so it retries when the path returns. A stat-then-update-then-remove race is accepted.
		if _, statErr := os.Stat(c.path); statErr != nil {
			if os.IsNotExist(statErr) {
				log.Printf("retention: gcode file missing for request %s (expected at %s): skipping prune this pass, will retry once the file/volume is available", c.id, c.path)
				continue
			}
			log.Printf("retention: stat %s (request %s): %v", c.path, c.id, statErr)
			if err == nil {
				err = statErr
			}
			continue
		}

		// Crash safety: mark the row pruned before removing the file. The reverse order can leave a row
		// pointing at a deleted file with gcode_pruned_at unset, retried forever. Update first leaves at worst
		// an orphan file, which sweepOrphanedUploads reclaims.
		if _, execErr := db.Exec(`UPDATE quote_requests SET gcode_path = '', gcode_pruned_at = ? WHERE id = ?`, now, c.id); execErr != nil {
			log.Printf("retention: mark request %s pruned: %v", c.id, execErr)
			if err == nil {
				err = execErr
			}
			continue
		}
		if rmErr := os.Remove(c.path); rmErr != nil && !os.IsNotExist(rmErr) {
			// The row is already committed as pruned; a failed disk delete leaves an orphan for
			// sweepOrphanedUploads. Still logged and folded into the returned err.
			log.Printf("retention: remove %s (request %s): %v", c.path, c.id, rmErr)
			if err == nil {
				err = rmErr
			}
		}
		log.Printf("retention: pruned gcode for request %s (finalized past its grace period)", c.id)
		pruned++
	}
	return pruned, err
}

// orphanSweepGrace is how old an unreferenced uploads/*.gcode.gz must be before removal.
// It keeps a mid-upload file (writeGcodePart ran, INSERT not yet committed) from being taken for an orphan;
// an hour far exceeds publicUploadReadDeadline (5 minutes).
const orphanSweepGrace = time.Hour

// sweepOrphanedUploads removes uploads/*.gcode.gz older than orphanSweepGrace with no gcode_path row.
// Orphans come from crashes before the INSERT, handleDelete's failed os.Remove, and pruneFinalizedGcode's
// row-first ordering; they count toward publicUploadsCapBytes. A per-file failure is logged and skipped.
func sweepOrphanedUploads(db *sql.DB, configDir string) (removed int, err error) {
	uploadsDir := filepath.Join(configDir, "uploads")
	entries, readErr := os.ReadDir(uploadsDir)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return 0, nil // nothing has ever been uploaded
		}
		return 0, readErr
	}

	rows, queryErr := db.Query(`SELECT gcode_path FROM quote_requests WHERE gcode_path != ''`)
	if queryErr != nil {
		return 0, queryErr
	}
	referenced := map[string]struct{}{}
	for rows.Next() {
		var path string
		if scanErr := rows.Scan(&path); scanErr != nil {
			rows.Close()
			return 0, scanErr
		}
		referenced[filepath.Base(path)] = struct{}{}
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		rows.Close()
		return 0, rowsErr
	}
	rows.Close()

	cutoff := time.Now().Add(-orphanSweepGrace)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), gcodeUploadSuffix) {
			continue
		}
		if _, ok := referenced[entry.Name()]; ok {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			if os.IsNotExist(infoErr) {
				continue // removed between ReadDir and Info; not an orphan we need to chase
			}
			log.Printf("retention: stat orphan candidate %s: %v", entry.Name(), infoErr)
			if err == nil {
				err = infoErr
			}
			continue
		}
		if info.ModTime().After(cutoff) {
			continue // possibly still mid-upload; reconsidered next pass
		}
		path := filepath.Join(uploadsDir, entry.Name())
		if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
			log.Printf("retention: remove orphaned upload %s: %v", path, rmErr)
			if err == nil {
				err = rmErr
			}
			continue
		}
		log.Printf("retention: removed orphaned upload %s (no matching quote_requests row, older than %s)", path, orphanSweepGrace)
		removed++
	}
	return removed, err
}

// runRetentionPass runs every retention job once; each job's error is logged so one can't block another.
func runRetentionPass(db *sql.DB, configDir string) {
	if _, err := pruneFinalizedGcode(db); err != nil {
		log.Printf("retention: prune pass: %v", err)
	}
	if _, err := sweepOrphanedUploads(db, configDir); err != nil {
		log.Printf("retention: orphan sweep: %v", err)
	}
}

// runRetentionLoop runs a pass immediately, then every requestGcodeRetentionInterval until ctx is canceled.
// The returned channel closes once the loop exits, so main can wait before closing the database.
func runRetentionLoop(ctx context.Context, db *sql.DB, configDir string) (done <-chan struct{}) {
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		runRetentionPass(db, configDir)
		ticker := time.NewTicker(requestGcodeRetentionInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runRetentionPass(db, configDir)
			}
		}
	}()
	return doneCh
}
