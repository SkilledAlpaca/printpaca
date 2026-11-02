-- index invoice_lines_invoice on invoice_lines
CREATE INDEX invoice_lines_invoice ON invoice_lines(invoice_id)
-- index invoice_lines_item on invoice_lines
CREATE INDEX invoice_lines_item ON invoice_lines(item_id) WHERE item_id != ''
-- index invoice_sends_invoice on invoice_sends
CREATE INDEX invoice_sends_invoice ON invoice_sends(invoice_id)
-- index invoices_status on invoices
CREATE INDEX invoices_status ON invoices(status)
-- index invoices_year_seq on invoices
CREATE UNIQUE INDEX invoices_year_seq ON invoices(number_year, number_seq)
-- index item_sales_sold_at on item_sales
CREATE INDEX item_sales_sold_at ON item_sales(sold_at)
-- index payments_invoice on payments
CREATE INDEX payments_invoice ON payments(invoice_id)
-- index payments_paid_date on payments
CREATE INDEX payments_paid_date ON payments(paid_date)
-- index quote_requests_status on quote_requests
CREATE INDEX quote_requests_status ON quote_requests(status)
-- index quotes_saved_at on quotes
CREATE INDEX quotes_saved_at ON quotes(saved_at)
-- index quotes_status on quotes
CREATE INDEX quotes_status ON quotes(status)
-- index users_email_unique on users
CREATE UNIQUE INDEX users_email_unique ON users (lower(email)) WHERE email != ''
-- table app_state on app_state
CREATE TABLE app_state (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)
-- table clients on clients
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
	)
-- table invoice_lines on invoice_lines
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
	)
-- table invoice_sends on invoice_sends
CREATE TABLE invoice_sends (
	  id           TEXT PRIMARY KEY,
	  invoice_id   TEXT NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
	  sent_at      TEXT NOT NULL,
	  channel      TEXT NOT NULL,
	  recipient    TEXT NOT NULL DEFAULT ''
	)
-- table invoices on invoices
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
	)
-- table item_sales on item_sales
CREATE TABLE item_sales (
	  request_id    TEXT PRIMARY KEY,
	  item_id       TEXT NOT NULL,
	  item_name     TEXT NOT NULL,
	  option_name   TEXT NOT NULL DEFAULT '',
	  quantity      INTEGER NOT NULL,
	  revenue_cents INTEGER NOT NULL,
	  sold_at       TEXT NOT NULL,
	  recorded_by   TEXT NOT NULL DEFAULT ''
	)
-- table items on items
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
	)
-- table payments on payments
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
	)
-- table quote_requests on quote_requests
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
	)
-- table quotes on quotes
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
	)
-- table users on users
CREATE TABLE users (
	  id            TEXT PRIMARY KEY,
	  username      TEXT NOT NULL UNIQUE COLLATE NOCASE,
	  password_hash TEXT NOT NULL,
	  permissions   TEXT NOT NULL DEFAULT '[]',
	  disabled      INTEGER NOT NULL DEFAULT 0,
	  created_at    TEXT NOT NULL,
	  updated_at    TEXT NOT NULL,
	  email         TEXT NOT NULL DEFAULT ''
	)
