package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestOpenDBRunsMigrations(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)

	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if version != len(migrations) {
		t.Fatalf("user_version = %d, want %d", version, len(migrations))
	}

	for _, table := range []string{"quotes", "app_state"} {
		var name string
		err := db.QueryRow("SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&name)
		if err != nil {
			t.Fatalf("table %s missing: %v", table, err)
		}
	}
}

// temp_store must be 2 (MEMORY) so SQLite never needs a filesystem temp directory; the scratch image has none.
func TestOpenDBSetsTempStoreMemory(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)

	var tempStore int
	if err := db.QueryRow("PRAGMA temp_store").Scan(&tempStore); err != nil {
		t.Fatalf("read temp_store: %v", err)
	}
	if tempStore != 2 {
		t.Errorf("temp_store = %d, want 2 (MEMORY)", tempStore)
	}
}

func TestOpenDBReopenIsANoOpAndKeepsData(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	if _, err := db.Exec(`
		INSERT INTO quotes (id, saved_at, label, client_name, project, reference, status, status_log, inputs, quantity, per_unit_total_cents, batch_total_cents, valid)
		VALUES ('a', '2026-01-01T00:00:00Z', '', '', '', '', 'draft', '[]', '{}', 1, 0, 0, 0)`); err != nil {
		t.Fatalf("seed quote: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	db2 := openTestDB(t, dir)

	var count int
	if err := db2.QueryRow(`SELECT COUNT(*) FROM quotes`).Scan(&count); err != nil {
		t.Fatalf("count quotes: %v", err)
	}
	if count != 1 {
		t.Fatalf("quotes count = %d, want 1 (reopen must not rerun migrations or lose data)", count)
	}
}

// Downgrade guard: a database whose user_version is past every known migration must refuse to open.
func TestOpenDBRefusesNewerSchema(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	future := len(migrations) + 1
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", future)); err != nil {
		t.Fatalf("bump user_version: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	_, err := openDB(dir)
	if err == nil {
		t.Fatal("openDB should refuse a database newer than this binary's known migrations")
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("%d", future)) {
		t.Errorf("error %q should name the database's version %d", err.Error(), future)
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("%d", len(migrations))) {
		t.Errorf("error %q should name this binary's known migration count %d", err.Error(), len(migrations))
	}
}

var (
	schemaSpace  = regexp.MustCompile(`\s+`)
	schemaPunct  = regexp.MustCompile(`\s*([(),])\s*`)
	schemaHeader = regexp.MustCompile(`^-- (\S+) (\S+) on (\S+)$`)
)

// normalizeSchemaSQL collapses whitespace so two CREATE statements compare equal when they differ only in
// layout.
func normalizeSchemaSQL(sqlText string) string {
	return strings.TrimSpace(schemaPunct.ReplaceAllString(schemaSpace.ReplaceAllString(sqlText, " "), "$1"))
}

// parseSchemaDump reads the "-- type name on table" / SQL pairs of testdata/schema-v1.sql into a map keyed by
// the header line.
func parseSchemaDump(t *testing.T, dump string) map[string]string {
	t.Helper()
	out := map[string]string{}
	var key string
	var body []string
	flush := func() {
		if key != "" {
			out[key] = normalizeSchemaSQL(strings.Join(body, "\n"))
		}
		body = nil
	}
	for _, line := range strings.Split(dump, "\n") {
		if m := schemaHeader.FindStringSubmatch(line); m != nil {
			flush()
			key = m[1] + " " + m[2] + " on " + m[3]
			continue
		}
		body = append(body, line)
	}
	flush()
	return out
}

// TestBaselineMatchesSchemaFixture compares the baseline migration's sqlite_master (tables with column
// definitions, defaults and constraints, plus every index, whitespace-normalized) with testdata/schema-v1.sql,
// dumped from the baseline. Column order is part of the comparison. Migrations after the baseline never touch
// the baseline or this fixture.
func TestBaselineMatchesSchemaFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "schema-v1.sql"))
	if err != nil {
		t.Fatal(err)
	}
	want := parseSchemaDump(t, string(raw))
	if len(want) < 20 {
		t.Fatalf("fixture parsed to %d objects, want at least 20", len(want))
	}

	// Only the baseline, so a migration appended later doesn't break the comparison.
	bdb, err := openRawSQLite(filepath.Join(t.TempDir(), "baseline.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bdb.Close() })
	bdb.SetMaxOpenConns(1)
	if err := runMigration(bdb, 0); err != nil {
		t.Fatalf("baseline migration: %v", err)
	}
	rows, err := bdb.Query(`SELECT type, name, tbl_name, sql FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var typ, name, tbl, sqlText string
		if err := rows.Scan(&typ, &name, &tbl, &sqlText); err != nil {
			t.Fatal(err)
		}
		got[typ+" "+name+" on "+tbl] = normalizeSchemaSQL(sqlText)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	for key, w := range want {
		g, ok := got[key]
		if !ok {
			t.Errorf("baseline is missing %s", key)
			continue
		}
		if g != w {
			t.Errorf("%s differs\n baseline: %s\n fixture:  %s", key, g, w)
		}
	}
	for key := range got {
		if _, ok := want[key]; !ok {
			t.Errorf("baseline has %s, which the fixture does not", key)
		}
	}
}

func TestBaselineSetsApplicationID(t *testing.T) {
	db := openTestDB(t, t.TempDir())
	var id int
	if err := db.QueryRow("PRAGMA application_id").Scan(&id); err != nil {
		t.Fatal(err)
	}
	if id != appID {
		t.Fatalf("application_id = %#x, want %#x", id, appID)
	}
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != len(migrations) {
		t.Fatalf("user_version = %d, want %d (every migration applied)", version, len(migrations))
	}
}

// A non-empty database without printpaca's application_id is refused, whether it is a foreign SQLite file or a
// database from a pre-release printpaca build (the earlier application_id, a nonzero user_version).
func TestOpenDBRefusesForeignDatabase(t *testing.T) {
	for name, setup := range map[string]string{
		"foreign file": `CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT)`,
		"pre-release":  `CREATE TABLE quotes (id TEXT PRIMARY KEY, saved_at TEXT NOT NULL, data TEXT NOT NULL); PRAGMA application_id = 1347440705; PRAGMA user_version = 7`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			raw, err := openRawSQLite(filepath.Join(dir, "printpaca.db"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := raw.Exec(setup); err != nil {
				t.Fatal(err)
			}
			if err := raw.Close(); err != nil {
				t.Fatal(err)
			}
			_, err = openDB(dir)
			if err == nil {
				t.Fatal("openDB should refuse a non-empty database without printpaca's application_id")
			}
			if !strings.Contains(err.Error(), "application_id") || !strings.Contains(err.Error(), "delete printpaca.db") {
				t.Errorf("error %q should say the application_id is missing and how to start fresh", err.Error())
			}
		})
	}
}

// Refusing a foreign database must leave it untouched: no switch to WAL (header bytes 18 and 19 stay 1, the
// rollback-journal value) and no -wal or -shm side files.
func TestOpenDBRefusalLeavesForeignFileUntouched(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "printpaca.db")
	raw, err := openRawSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	header := func() []byte {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(b) < 100 {
			t.Fatalf("database is %d bytes, want at least a 100-byte header", len(b))
		}
		return b[:100]
	}
	if h := header(); h[18] != 1 || h[19] != 1 {
		t.Fatalf("fixture header bytes 18/19 = %d/%d, want 1/1 (rollback journal)", h[18], h[19])
	}

	if _, err := openDB(dir); err == nil {
		t.Fatal("openDB should refuse a foreign database")
	}

	if h := header(); h[18] != 1 || h[19] != 1 {
		t.Errorf("header bytes 18/19 = %d/%d after refusal, want 1/1 (the file must not be switched to WAL)", h[18], h[19])
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(path + suffix); !os.IsNotExist(err) {
			t.Errorf("%s exists after refusal (stat err = %v), want absent", path+suffix, err)
		}
	}
}

// An existing zero-byte or table-less file is a fresh database, not a foreign one.
func TestOpenDBAcceptsEmptyFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "printpaca.db"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	db := openTestDB(t, dir)
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'quotes'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("quotes table count = %d (%v), want 1", n, err)
	}
}

// The partial unique index keeps users without an email unconstrained and the rest case-insensitively unique.
func TestSchemaUsersEmailUniqueness(t *testing.T) {
	db := openTestDB(t, t.TempDir())
	if _, err := db.Exec(`INSERT INTO users (id, username, password_hash, created_at, updated_at) VALUES ('u1','alice','h','t','t'), ('u2','bob','h','t','t'), ('u3','carol','h','t','t')`); err != nil {
		t.Fatalf("users without an email must not collide: %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users WHERE email = '' AND permissions = '[]' AND disabled = 0`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("defaults: n=%d err=%v, want 3 users with email '', permissions '[]', disabled 0", n, err)
	}
	if _, err := db.Exec(`UPDATE users SET email = 'A@Example.com' WHERE id = 'u1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE users SET email = 'a@example.com' WHERE id = 'u2'`); err == nil {
		t.Fatal("case-insensitive duplicate email should violate the unique index")
	}
	if _, err := db.Exec(`INSERT INTO users (id, username, password_hash, created_at, updated_at) VALUES ('u4','ALICE','h','t','t')`); err == nil {
		t.Fatal("usernames are unique case-insensitively")
	}
}

// Deleting an invoice removes its lines, payments, and sends: foreign_keys is on and the references cascade.
func TestSchemaInvoiceChildrenCascade(t *testing.T) {
	db := openTestDB(t, t.TempDir())
	for _, q := range []string{
		`INSERT INTO invoices (id, number, number_year, number_seq, client_name, created_at, updated_at) VALUES ('i1','INV-1',2026,1,'c','t','t')`,
		`INSERT INTO invoice_lines (id, invoice_id, position, description) VALUES ('l1','i1',0,'d')`,
		`INSERT INTO payments (id, invoice_id, paid_date, amount_cents, created_at) VALUES ('p1','i1','2026-01-01',100,'t')`,
		`INSERT INTO invoice_sends (id, invoice_id, sent_at, channel) VALUES ('s1','i1','t','email')`,
		`DELETE FROM invoices WHERE id = 'i1'`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for _, table := range []string{"invoice_lines", "payments", "invoice_sends"} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s rows after invoice delete = %d (%v), want 0", table, n, err)
		}
	}
}

// Attribution columns default to ” on every table that has them; quote_requests has only updated_by.
func TestSchemaAttributionColumns(t *testing.T) {
	db := openTestDB(t, t.TempDir())
	tableColumns := map[string][]string{
		"quotes":         {"created_by", "updated_by"},
		"invoices":       {"created_by", "updated_by"},
		"payments":       {"created_by", "updated_by"},
		"clients":        {"created_by", "updated_by"},
		"items":          {"created_by", "updated_by"},
		"quote_requests": {"updated_by"},
	}
	for table, cols := range tableColumns {
		for _, col := range cols {
			var v string
			if err := db.QueryRow(fmt.Sprintf(`SELECT COALESCE(MIN(%s), '') FROM %s`, col, table)).Scan(&v); err != nil {
				t.Errorf("%s.%s: %v", table, col, err)
			}
		}
	}
	if rows, err := db.Query(`SELECT created_by FROM quote_requests`); err == nil {
		rows.Close()
		t.Error("quote_requests.created_by should not exist: customers create requests through the public path")
	}
}

// A quote row inserted without part_count reads back as one part.
func TestSchemaPartCountDefaultsToOne(t *testing.T) {
	db := openTestDB(t, t.TempDir())
	if _, err := db.Exec(`INSERT INTO quotes (id, saved_at, label, client_name, project, reference, status, status_log, inputs, quantity, per_unit_total_cents, batch_total_cents, valid)
		VALUES ('q1', '2026-01-01T00:00:00Z', 'L', '', '', '', 'draft', '[]', '{}', 1, 0, 0, 0)`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT part_count FROM quotes WHERE id = 'q1'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("part_count = %d (%v), want 1", n, err)
	}
}

// seedV105OptionRows builds a version-1 database in dir shaped like v1.0.5: item "it" with option_label
// "Color" and bare option names, plus one row per table that stored the bare name. It returns nothing; the
// caller opens dir with openDB, which runs migration 2.
func seedV105OptionRows(t *testing.T, dir string) {
	t.Helper()
	raw, err := openRawSQLite(filepath.Join(dir, "printpaca.db"))
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	if err := runMigration(raw, 0); err != nil {
		t.Fatal(err)
	}
	stmts := []string{
		`INSERT INTO items (id, name, price_cents, option_label, options_json, created_at, updated_at) VALUES
		  ('it', 'Clip', 1000, 'Color', '[{"name":"Red","deltaCents":0},{"name":"Blue","deltaCents":50}]', 'x', 'x'),
		  ('plain', 'Plain', 500, '', '[]', 'x', 'x')`,
		`INSERT INTO invoices (id, number, number_year, number_seq, client_name, created_at, updated_at) VALUES ('inv', '2026-0001', 2026, 1, 'Walk-in', 'x', 'x')`,
		`INSERT INTO invoice_lines (id, invoice_id, position, description, quantity, unit_price_cents, item_id, option_name) VALUES
		  ('l1', 'inv', 0, 'Clip', 1, 1000, 'it', 'Red'),
		  ('l2', 'inv', 1, 'Plain', 1, 500, 'plain', ''),
		  ('l3', 'inv', 2, 'Clip', 1, 1000, 'it', '')`,
		`INSERT INTO quote_requests (id, name, email, material, quantity, created_at, kind, item_id, item_name, option_name)
		  VALUES ('r1', 'A', 'a@example.com', 'PLA', 1, 'x', 'item', 'it', 'Clip', 'Blue')`,
		`INSERT INTO item_sales (request_id, item_id, item_name, option_name, quantity, revenue_cents, sold_at)
		  VALUES ('r1', 'it', 'Clip', 'Blue', 1, 1050, 'x'), ('r2', 'gone', 'Old', 'Red', 1, 100, 'x')`,
	}
	for _, s := range stmts {
		if _, err := raw.Exec(s); err != nil {
			t.Fatalf("seed: %v\n%s", err, s)
		}
	}
	raw.Close()
}

// Migration 2 rewrites bare option names stored by v1.0.5 into "Label: Name" in all three tables, and leaves
// rows that are empty or linked to an item that no longer exists alone.
func TestMigrationRewritesBareOptionNames(t *testing.T) {
	dir := t.TempDir()
	seedV105OptionRows(t, dir)
	db := openTestDB(t, dir)
	one := func(q string) string {
		var v string
		if err := db.QueryRow(q).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	for q, want := range map[string]string{
		`SELECT option_name FROM invoice_lines WHERE id = 'l1'`:      "Color: Red",
		`SELECT option_name FROM invoice_lines WHERE id = 'l2'`:      "",
		`SELECT option_name FROM invoice_lines WHERE id = 'l3'`:      "",
		`SELECT option_name FROM quote_requests WHERE id = 'r1'`:     "Color: Blue",
		`SELECT option_name FROM item_sales WHERE request_id = 'r1'`: "Color: Blue",
		`SELECT option_name FROM item_sales WHERE request_id = 'r2'`: "Red",
	} {
		if got := one(q); got != want {
			t.Errorf("%s = %q, want %q", q, got, want)
		}
	}
}

// A draft invoice carrying a pre-upgrade line must still save: linksValid parses the migrated name.
func TestMigratedDraftInvoiceSaves(t *testing.T) {
	dir := t.TempDir()
	seedV105OptionRows(t, dir)
	mux := newTestMux(t, dir)
	rec := getInvoice(t, mux, "inv")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET: %d %s", rec.Code, rec.Body.String())
	}
	var inv Invoice
	if err := json.Unmarshal(rec.Body.Bytes(), &inv); err != nil {
		t.Fatal(err)
	}
	body := `{"clientName":"Walk-in","lines":[{"description":"Clip","quantity":1,"unitPriceCents":1000,"itemId":"it","optionName":"Color: Red"}]}`
	if rec := putInvoice(t, mux, "inv", body); rec.Code != http.StatusOK {
		t.Fatalf("PUT migrated line: %d %s", rec.Code, rec.Body.String())
	}
	// The line as stored after migration, sent back unchanged.
	if len(inv.Lines) == 0 || inv.Lines[0].OptionName != "Color: Red" {
		t.Fatalf("stored lines = %+v", inv.Lines)
	}
}

// The v1.1 migrations give every existing invoice version 1 and every existing request no invoice link and no
// needs-print-time mark.
func TestMigrationAddsInvoiceVersionAndRequestLink(t *testing.T) {
	dir := t.TempDir()
	seedV105OptionRows(t, dir)
	db := openTestDB(t, dir)
	var version int
	if err := db.QueryRow(`SELECT version FROM invoices WHERE id = 'inv'`).Scan(&version); err != nil || version != 1 {
		t.Errorf("invoice version = %d (err %v), want 1", version, err)
	}
	var invoiceID string
	var needs int
	if err := db.QueryRow(`SELECT invoice_id, needs_print_time FROM quote_requests WHERE id = 'r1'`).Scan(&invoiceID, &needs); err != nil || invoiceID != "" || needs != 0 {
		t.Errorf("request invoice_id %q needs_print_time %d (err %v), want '' and 0", invoiceID, needs, err)
	}
}
