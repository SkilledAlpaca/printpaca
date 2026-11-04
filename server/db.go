package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// appID is stored in the SQLite header (PRAGMA application_id) by the baseline migration. openDB refuses a
// non-empty database that lacks it, so a file from some other program, or from a pre-release printpaca build,
// never gets migrated or queried. The value is "PPA1" in ASCII.
const appID = 0x50504131

// migrations holds the SQL taking the schema from version i to i+1; index i is applied at user_version i.
// Index 0 is the baseline: the complete schema in one step. Append, never edit or remove, an entry
// after it; every later schema change is a new migration.
var migrations = []string{
	// 0 -> 1: the baseline.
	//
	// Timestamps are server-set RFC3339 UTC strings, not SQLite datetime, so Go and DB agree. Money is
	// integer cents (quantity is the one float). created_by/updated_by hold the acting user's name ('' when
	// unattributed); quote_requests has only updated_by because customers create those through the public
	// path.
	`
	PRAGMA application_id = 1347436849; -- appID

	-- app_state holds internal markers.
	CREATE TABLE app_state (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);

	-- quotes: one row per saved quote. inputs is the stored {parts, pricing} bag; the output columns are
	-- recomputed by the server (for 2 or more parts they describe the order as one unit,
	-- computeQuoteOutputs); part_count is the number of parts in inputs.
	CREATE TABLE quotes (
	  id                       TEXT PRIMARY KEY,
	  saved_at                 TEXT NOT NULL,
	  label                    TEXT NOT NULL DEFAULT '',
	  client_id                TEXT,
	  client_name              TEXT NOT NULL DEFAULT '',
	  project                  TEXT NOT NULL DEFAULT '',
	  reference                TEXT NOT NULL DEFAULT '',
	  status                   TEXT NOT NULL DEFAULT 'draft',
	  status_log               TEXT NOT NULL DEFAULT '[]',
	  invoiced_as              TEXT,
	  inputs                   TEXT NOT NULL,
	  quantity                 REAL NOT NULL,
	  per_unit_total_cents     INTEGER NOT NULL,
	  batch_total_cents        INTEGER NOT NULL,
	  recommended_price_cents  INTEGER,
	  resolved_price_cents     INTEGER,
	  net_profit_cents         INTEGER,
	  achieved_margin_pct      REAL,
	  valid                    INTEGER NOT NULL,
	  created_by               TEXT NOT NULL DEFAULT '',
	  updated_by               TEXT NOT NULL DEFAULT '',
	  part_count               INTEGER NOT NULL DEFAULT 1
	);
	CREATE INDEX quotes_saved_at ON quotes(saved_at);
	CREATE INDEX quotes_status ON quotes(status);

	CREATE TABLE clients (
		id               TEXT PRIMARY KEY,
		name             TEXT NOT NULL,
		email            TEXT NOT NULL DEFAULT '',
		phone            TEXT NOT NULL DEFAULT '',
		billing_address  TEXT NOT NULL DEFAULT '',
		shipping_address TEXT NOT NULL DEFAULT '',
		notes            TEXT NOT NULL DEFAULT '',
		created_at       TEXT NOT NULL,
		updated_at       TEXT NOT NULL,
		created_by       TEXT NOT NULL DEFAULT '',
		updated_by       TEXT NOT NULL DEFAULT ''
	);

	-- invoices: numbers are MAX(number_seq)+1 per year in the insert transaction, never reused.
	-- invoice_lines, payments, and invoice_sends cascade on invoice delete (draft-only).
	CREATE TABLE invoices (
	  id            TEXT PRIMARY KEY,
	  number        TEXT NOT NULL UNIQUE,
	  number_year   INTEGER NOT NULL,
	  number_seq    INTEGER NOT NULL,
	  status        TEXT NOT NULL DEFAULT 'draft',
	  client_id     TEXT NOT NULL DEFAULT '',
	  client_name   TEXT NOT NULL,
	  client_email  TEXT NOT NULL DEFAULT '',
	  client_billing_address  TEXT NOT NULL DEFAULT '',
	  client_shipping_address TEXT NOT NULL DEFAULT '',
	  issue_date    TEXT NOT NULL DEFAULT '',
	  due_date      TEXT NOT NULL DEFAULT '',
	  tax_bp        INTEGER NOT NULL DEFAULT 0,
	  discount_cents INTEGER NOT NULL DEFAULT 0,
	  notes         TEXT NOT NULL DEFAULT '',
	  subtotal_cents INTEGER NOT NULL DEFAULT 0,
	  tax_cents     INTEGER NOT NULL DEFAULT 0,
	  total_cents   INTEGER NOT NULL DEFAULT 0,
	  created_at    TEXT NOT NULL,
	  updated_at    TEXT NOT NULL,
	  created_by    TEXT NOT NULL DEFAULT '',
	  updated_by    TEXT NOT NULL DEFAULT ''
	);
	CREATE UNIQUE INDEX invoices_year_seq ON invoices(number_year, number_seq);
	CREATE INDEX invoices_status ON invoices(status);

	-- invoice_lines: item_id/option_name tie a line to a gallery item so Top items can count counter sales; no
	-- foreign key, because the item can be deleted and the line must survive it. stock_taken is the units the
	-- send actually took off the shelf (after the floor at zero), so a cancel restores exactly that many and
	-- never more. kind is 'item' or 'shipping'; tax exemption keys off it, not the description text.
	CREATE TABLE invoice_lines (
	  id              TEXT PRIMARY KEY,
	  invoice_id      TEXT NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
	  position        INTEGER NOT NULL,
	  description     TEXT NOT NULL,
	  quantity        REAL NOT NULL DEFAULT 1,
	  unit_price_cents INTEGER NOT NULL DEFAULT 0,
	  discount_cents  INTEGER NOT NULL DEFAULT 0,
	  quote_id        TEXT NOT NULL DEFAULT '',
	  item_id         TEXT NOT NULL DEFAULT '',
	  option_name     TEXT NOT NULL DEFAULT '',
	  stock_taken     INTEGER NOT NULL DEFAULT 0,
	  kind            TEXT NOT NULL DEFAULT 'item'
	);
	CREATE INDEX invoice_lines_invoice ON invoice_lines(invoice_id);
	CREATE INDEX invoice_lines_item ON invoice_lines(item_id) WHERE item_id != '';

	-- payments: many per invoice. paid_date is "YYYY-MM-DD".
	CREATE TABLE payments (
	  id           TEXT PRIMARY KEY,
	  invoice_id   TEXT NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
	  paid_date    TEXT NOT NULL,
	  amount_cents INTEGER NOT NULL,
	  method       TEXT NOT NULL DEFAULT '',
	  note         TEXT NOT NULL DEFAULT '',
	  created_at   TEXT NOT NULL,
	  reference    TEXT NOT NULL DEFAULT '',
	  created_by   TEXT NOT NULL DEFAULT '',
	  updated_by   TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX payments_invoice ON payments(invoice_id);
	CREATE INDEX payments_paid_date ON payments(paid_date);

	-- invoice_sends: one row per delivery event, channel 'email' (SMTP) or 'manual' (PATCH to sent).
	-- sent_at is RFC3339 UTC.
	CREATE TABLE invoice_sends (
	  id           TEXT PRIMARY KEY,
	  invoice_id   TEXT NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
	  sent_at      TEXT NOT NULL,
	  channel      TEXT NOT NULL,
	  recipient    TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX invoice_sends_invoice ON invoice_sends(invoice_id);

	-- quote_requests (public page). status goes new -> viewed -> finalized (requests.go); weight_g,
	-- time_seconds, and price_cents are display data only. gcode_path is the gzipped file under uploads/.
	-- finalized_at is the retention clock; gcode_pruned_at distinguishes "never uploaded" from "pruned".
	-- shipping_cents is the shipping amount the public quote showed the visitor, so converting the request to an
	-- invoice can add the Shipping line they saw. tax_inclusive records whether the price included tax when the
	-- visitor submitted it, so conversion uses that basis even if pricing.taxRatePct changes later.
	CREATE TABLE quote_requests (
	  id             TEXT PRIMARY KEY,
	  status         TEXT NOT NULL DEFAULT 'new',
	  name           TEXT NOT NULL,
	  email          TEXT NOT NULL,
	  note           TEXT NOT NULL DEFAULT '',
	  material       TEXT NOT NULL,
	  quantity       INTEGER NOT NULL,
	  weight_g       REAL NOT NULL DEFAULT 0,
	  time_seconds   INTEGER NOT NULL DEFAULT 0,
	  price_cents    INTEGER NOT NULL DEFAULT 0,
	  gcode_filename TEXT NOT NULL DEFAULT '',
	  gcode_path     TEXT NOT NULL DEFAULT '',
	  gcode_bytes    INTEGER NOT NULL DEFAULT 0,
	  created_at     TEXT NOT NULL,
	  finalized_at   TEXT NOT NULL DEFAULT '',
	  gcode_pruned_at TEXT NOT NULL DEFAULT '',
	  kind           TEXT NOT NULL DEFAULT 'quote',
	  item_id        TEXT NOT NULL DEFAULT '',
	  item_name      TEXT NOT NULL DEFAULT '',
	  option_name    TEXT NOT NULL DEFAULT '',
	  updated_by     TEXT NOT NULL DEFAULT '',
	  shipping_cents INTEGER NOT NULL DEFAULT 0,
	  tax_inclusive  INTEGER NOT NULL DEFAULT 0 CHECK (tax_inclusive IN (0, 1))
	);
	CREATE INDEX quote_requests_status ON quote_requests(status);

	CREATE TABLE items (
	  id           TEXT PRIMARY KEY,
	  name         TEXT NOT NULL,
	  description  TEXT NOT NULL DEFAULT '',
	  price_cents  INTEGER NOT NULL,
	  option_label TEXT NOT NULL DEFAULT '',
	  options_json TEXT NOT NULL DEFAULT '[]',
	  image_file   TEXT NOT NULL DEFAULT '',
	  public       INTEGER NOT NULL DEFAULT 0,
	  stock_mode   TEXT NOT NULL DEFAULT 'ready_to_print',
	  stock_qty    INTEGER NOT NULL DEFAULT 0,
	  sort_order   INTEGER NOT NULL DEFAULT 0,
	  created_at   TEXT NOT NULL,
	  updated_at   TEXT NOT NULL,
	  allow_backorder INTEGER NOT NULL DEFAULT 0,
	  created_by   TEXT NOT NULL DEFAULT '',
	  updated_by   TEXT NOT NULL DEFAULT ''
	);

	-- users: employee accounts, hashes, and per-tab grants. email is optional (Cloudflare Access sign-in),
	-- stored lowercased, '' meaning none; the partial unique index makes it case-insensitively unique
	-- without constraining users that have none.
	CREATE TABLE users (
	  id            TEXT PRIMARY KEY,
	  username      TEXT NOT NULL UNIQUE COLLATE NOCASE,
	  password_hash TEXT NOT NULL,
	  permissions   TEXT NOT NULL DEFAULT '[]',
	  disabled      INTEGER NOT NULL DEFAULT 0,
	  created_at    TEXT NOT NULL,
	  updated_at    TEXT NOT NULL,
	  email         TEXT NOT NULL DEFAULT ''
	);
	CREATE UNIQUE INDEX users_email_unique ON users (lower(email)) WHERE email != '';

	-- item_sales: one append-only row per finalized gallery order, written by the finalize transaction. It
	-- exists apart from quote_requests because deleting a request (the Requests tab's tidy-up) would otherwise
	-- delete the only record of the sale; request_id therefore has no foreign key. revenue_cents is the order
	-- total stored in quote_requests.price_cents, and sold_at is the RFC3339 UTC finalized_at, so string
	-- comparison orders it.
	CREATE TABLE item_sales (
	  request_id    TEXT PRIMARY KEY,
	  item_id       TEXT NOT NULL,
	  item_name     TEXT NOT NULL,
	  option_name   TEXT NOT NULL DEFAULT '',
	  quantity      INTEGER NOT NULL,
	  revenue_cents INTEGER NOT NULL,
	  sold_at       TEXT NOT NULL,
	  recorded_by   TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX item_sales_sold_at ON item_sales(sold_at);
	`,

	// 1 -> 2: option groups. items.option_groups_json holds the groups (server/items.go's ItemOptionGroup).
	// Each item with a legacy option_label + options_json becomes one required group with generated ids and
	// no option photos. option_label and options_json stay in the table but nothing reads or writes them.
	`
	ALTER TABLE items ADD COLUMN option_groups_json TEXT NOT NULL DEFAULT '[]';

	UPDATE items SET option_groups_json = json_array(json_object(
	  'id', 'g_' || lower(hex(randomblob(4))),
	  'label', option_label,
	  'required', json('true'),
	  'options', json((SELECT json_group_array(json_object(
	      'id', 'o_' || lower(hex(randomblob(4))),
	      'name', json_extract(value, '$.name'),
	      'deltaCents', json_extract(value, '$.deltaCents'),
	      'imageFile', ''))
	    FROM json_each(items.options_json)))
	))
	WHERE option_label != '' AND json_array_length(options_json) > 0;

	-- Stored option names change shape with the groups: a v1.0.5 order or invoice line recorded the bare
	-- option ("Red"), and the new code reads "Color: Red" (itemoptions.go optionSummaryDelta). Rewrite each
	-- row linked to an item that was just converted, when its option_name is one of that item's legacy
	-- option names. This lives in migration 2 (unreleased) so an upgrade is one consistent step.
	UPDATE invoice_lines SET option_name = (SELECT i.option_label || ': ' || invoice_lines.option_name FROM items i WHERE i.id = invoice_lines.item_id)
	WHERE option_name != '' AND item_id != '' AND EXISTS (SELECT 1 FROM items i, json_each(i.options_json) o
	  WHERE i.id = invoice_lines.item_id AND i.option_label != '' AND json_extract(o.value, '$.name') = invoice_lines.option_name);
	UPDATE quote_requests SET option_name = (SELECT i.option_label || ': ' || quote_requests.option_name FROM items i WHERE i.id = quote_requests.item_id)
	WHERE option_name != '' AND item_id != '' AND EXISTS (SELECT 1 FROM items i, json_each(i.options_json) o
	  WHERE i.id = quote_requests.item_id AND i.option_label != '' AND json_extract(o.value, '$.name') = quote_requests.option_name);
	UPDATE item_sales SET option_name = (SELECT i.option_label || ': ' || item_sales.option_name FROM items i WHERE i.id = item_sales.item_id)
	WHERE option_name != '' AND item_id != '' AND EXISTS (SELECT 1 FROM items i, json_each(i.options_json) o
	  WHERE i.id = item_sales.item_id AND i.option_label != '' AND json_extract(o.value, '$.name') = item_sales.option_name);
	`,

	// 2 -> 3: optimistic concurrency for items. items.version starts at 1 and the trigger bumps it on
	// every update, whichever code path wrote the row, so a stale editor save (PUT with the loaded version)
	// is detectable. recursive_triggers is off, so the trigger's own UPDATE does not re-fire it.
	`
	ALTER TABLE items ADD COLUMN version INTEGER NOT NULL DEFAULT 1;
	CREATE TRIGGER items_bump_version AFTER UPDATE ON items
	WHEN NEW.version = OLD.version
	BEGIN
	  UPDATE items SET version = OLD.version + 1 WHERE id = NEW.id;
	END;
	`,
}

// openDB opens the SQLite database in configDir, creating it on first run, and runs pending migrations.
func openDB(configDir string) (*sql.DB, error) {
	path := filepath.Join(configDir, "printpaca.db")
	// Check the application_id on a connection that leaves the journal mode alone: journal_mode(WAL) would
	// rewrite a foreign file's header and create -wal/-shm files before the refusal below.
	const common = "_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=temp_store(2)"
	probe, err := sql.Open("sqlite", fmt.Sprintf("file:%s?%s", path, common))
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	probe.SetMaxOpenConns(1)
	err = checkApplicationID(probe)
	probe.Close()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	// temp_store(2) keeps SQLite's temp b-trees in memory: the FROM scratch image has no /tmp, /var/tmp, or
	// /usr/tmp, so a spill to disk would fail with SQLITE_CANTOPEN.
	dsn := fmt.Sprintf("file:%s?%s&_pragma=journal_mode(WAL)", path, common)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	// One connection sidesteps SQLITE_BUSY: a single-user LAN app gains nothing from a pool. Don't raise it.
	db.SetMaxOpenConns(1)

	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// checkApplicationID refuses a non-empty database that this build didn't create. That covers a foreign SQLite
// file and any database from a pre-release printpaca build.
// A brand-new file (no tables, user_version 0) passes; the baseline migration then stamps it.
func checkApplicationID(db *sql.DB) error {
	var id, version, objects int
	if err := db.QueryRow("PRAGMA application_id").Scan(&id); err != nil {
		return fmt.Errorf("read application_id: %w", err)
	}
	if id == appID {
		return nil
	}
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master").Scan(&objects); err != nil {
		return fmt.Errorf("read sqlite_master: %w", err)
	}
	if version == 0 && objects == 0 {
		return nil
	}
	return errors.New("this database was created by a pre-release build of printpaca or by another program (unrecognized application_id); to start fresh, stop printpaca, delete printpaca.db, printpaca.db-wal, and printpaca.db-shm from the config directory, and start it again. config.yaml and uploads are kept")
}

// migrate applies every migration at or past PRAGMA user_version, each in its own transaction.
// A failed migration rolls back and the error propagates, so the server won't start half-migrated.
func migrate(db *sql.DB) error {
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}
	// A user_version ahead of every known migration means a newer release opened this database; refuse to
	// start rather than run stale queries against an unknown schema.
	if version > len(migrations) {
		return fmt.Errorf("database schema version %d is newer than this binary knows (%d): run a newer printpaca binary against this database", version, len(migrations))
	}
	for i := version; i < len(migrations); i++ {
		if err := runMigration(db, i); err != nil {
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
	}
	return nil
}

func runMigration(db *sql.DB, i int) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(migrations[i]); err != nil {
		return err
	}
	// PRAGMA user_version takes no bound parameters; i+1 is our own counter, so formatting it is safe.
	// The write lands on the header page and commits or rolls back with the transaction.
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
		return fmt.Errorf("set user_version: %w", err)
	}
	return tx.Commit()
}

// newID returns a fresh primary key: 16 crypto/rand bytes as lowercase hex.
func newID() string {
	b := make([]byte, 16)
	rand.Read(b) // never fails since Go 1.24: an entropy failure crashes the process instead
	return hex.EncodeToString(b)
}

// boolToInt binds a bool to an INTEGER 0/1 column.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
