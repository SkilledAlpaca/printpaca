# Architecture

An orientation map for contributors: what printpaca is made of, and where to look for each piece. For configuration and deployment, see [Configuration](configuration.md); for the pricing formulas, see [formulas.md](formulas.md).

## Overview

printpaca is a single-business (one shop, optionally several employee logins), self-hosted 3D-print cost and quote calculator. It ships as one Go binary that embeds the built Svelte SPA, so running it is `docker run` against one small image: no frontend server, no external database, and every setting has a built-in default. Data lives in one SQLite file.

```
web/        Svelte 5 SPA: admin app + public quote page, one Vite build
server/     Go server: HTTP, SQLite, config, auth, REST handlers
e2e/        Playwright end-to-end tests, run against a real built binary
docs/       User and contributor documentation
scripts/    Build and CI helper scripts (for example, the image size gate)
config/     Tracked reference config: config.example.yaml, config.schema.json
packaging/  Unraid CA template
testdata/   Pricing golden vectors, the money-format fixture, and
            uploads/ parser fixtures (see testdata/uploads/README.md)
```

## Components

### web/src/lib/engine/

A framework-free TypeScript module with no runtime dependencies, covering three roles:

- **Shared quote and pricing wire types**: `QuoteInputs`, `CostBreakdown`, `PricingResult`, and others (`quote.ts`), which the admin editor and the public quote page build requests and render responses against, plus the config wire types that mirror `server/config.go` (`types.ts`).
- **Browser-side upload parsers**: `gcode.ts`, `stl.ts`, and `threeMf.ts` parse a dropped G-code, STL, or 3MF file entirely in the browser, so the server never parses a model file; the public page uploads the file only so you can inspect it later. The `testdata/uploads/` fixtures pin them.
- **Small preview helpers**: `invoice.ts`'s `invoiceTotals` (mirrored server-side by `computeTotals` in `server/invoices.go`, for the Invoices tab's live preview) and the Filament and Electricity tabs' math (`filament.ts`, `electricity.ts`).

Quote pricing lives in `server/pricing.go`, not in this module. `server/pricing_test.go` pins it against `testdata/quote-vectors.json`, and the frozen golden-vector suite in `server/pricing_compat_test.go` pins it too. For rounding rules, see [formulas.md](formulas.md).

### web/

A Svelte 5 (runes) single-page app. Vite builds two entry points into one `dist/`:

- `index.html`, the authenticated admin app (quotes, clients, invoices, settings, dashboard), served at `/admin/`
- `public.html`, the anonymous front door (`/`) and public quote page (`/order`)

The Go binary embeds that `dist/` at build time (`//go:embed all:embedded/dist` in `server/static.go`).

The admin app is an installable Progressive Web App with a hand-rolled service worker (`web/public/sw.js`, no Workbox) that precaches the app shell. `api/*` and `healthz` are never cached, and the worker's scope is `/admin/`, so it never controls the front door or the shop. Registration runs only in a secure context (HTTPS or `localhost`), so it silently no-ops on a plain-HTTP LAN deployment.

### server/

A single Go `main` package. It owns HTTP routing (a path router on the main port in front of the admin and public route tables), the SQLite connection (`modernc.org/sqlite`, one open connection, no pool), config loading, merging, and validation, session-based auth middleware, and a REST handler per resource type. It also serves the embedded SPA and the JSON API.

## Data and persistence

Storage is one SQLite file at `$CONFIG_DIR/printpaca.db`. The schema is one baseline migration (`PRAGMA user_version` 1) plus any later append-only migrations (`server/db.go`): each runs once, in order, and existing migrations are never edited or removed. The baseline stamps `PRAGMA application_id`; on open, printpaca refuses a non-empty database without it, which covers a foreign SQLite file and a database from a pre-release build (to start fresh, delete the `printpaca.db*` files from the config directory; `config.yaml` and `uploads/` can stay). A database whose `user_version` is newer than the binary is refused too.

- **Quotes**: server-authoritative, per-record REST (`server/quotestore.go`). The server recomputes and owns every pricing output column (totals, recommended and resolved price, margin) on every write; the client supplies only `inputs` and `options`. `inputs` is always an order, `{parts, pricing}` with 1 to 20 parts (a single-part quote is a one-part order), and `POST /api/quote/compute` answers an order breakdown for the same shape. The editor's form state (version 1) is stored as an opaque snapshot beside the inputs, so a saved quote reopens as it was left; a stored form state with any other version is reported as unusable on restore. The live preview is server-computed too, through the stateless, authenticated `POST /api/quote/compute`; nothing in the browser prices a quote.
- **Clients** and **invoices** (with payments): standard CRUD records. An invoice converts directly from a saved quote. An invoice line can link to a gallery item (`item_id`, `option_name`, and `stock_taken` on `invoice_lines`); Top items counts those lines live while their invoice is sent or paid.
- **Items**: the item gallery's catalog (`items` table): CRUD, image uploads, two stock modes, and a public flag gating visibility on the public listener.
- **quote_requests**: the inbox for public quote-page submissions and anonymous item orders (`kind='item'`).
- **item_sales**: an append-only ledger with one row per finalized item order, written in the same transaction as the finalize (`server/requests.go`). It has no foreign key to `quote_requests`, so deleting a request doesn't remove its sale. Top items (`GET /api/dashboard/top-items`) combines it with the linked invoice lines.
- **Users** (`users` table): the admin identity plus optional employee accounts with per-tab grants (`server/users.go`, `server/identity.go`), gated by `users.multiUser`. Password hashes and grants live only in this table, never in `config.yaml`. Records carry `created_by` and `updated_by` attribution columns.

Configuration is YAML merged over built-in defaults (`server/config.go`), and the Settings UI writes sparse overrides back to `config.yaml`.

## Listeners and routing

The server runs one main listener and, optionally, a second customer-only listener (`server/main.go`, `server/router.go`):

- **Main port** (`PORT`): one hostname for everything. A top-level router (`newMainRouter`) dispatches by path:
  - `/admin/*` goes to the admin chain with the `/admin` prefix stripped, so the REST API, the admin SPA, and every admin check (Host allowlist, Cloudflare Access verification and tunnel guard, demo write guard, CSRF, login session) see the same paths the chain itself uses: `/admin/api/quotes` arrives as `/api/quotes`. `/admin` redirects to `/admin/`.
  - `GET /healthz` is the readiness probe. It runs outside the admin auth and tunnel chain, behind the public chain and the Host allowlist, so tunneled monitors and an Access application scoped to `/admin` get an answer.
  - The admin app registers its service worker at `/admin/sw.js` with scope `/admin/`. The public pages register none, so `/sw.js` is a `404`.
  - The router's own answers (the `/admin` redirect, the `/api` `404`, path-cleaning redirects, `/healthz`) go through the public chain: security headers, access log, and the Host allowlist.
  - `/api/public/*` and everything outside `/admin/` and `/api/` go to the public routes behind the same Host allowlist. Any other `/api/*` is a JSON `404`; the admin API lives under `/admin/api/`.
- **Public** (`PUBLIC_PORT`, optional): anonymous and isolated (`server/public.go`); it never serves `/admin/`. It serves the front door (`/`), the shop (`/order`), the item gallery, a display-only config payload, three rate-limited anonymous POST endpoints across two per-IP budgets (quote preview; quote-request and item-order submission, which share a budget), `GET /custom.css`, item images, and `GET /healthz`. Every price, preview or submission, is computed server-side through `server/pricing.go`; the visitor's browser never receives the cost model and never supplies a price.

Both listeners mount the same public routes over one shared set of rate limiters, one upload semaphore, and one notification queue (`publicShared`), so a visitor gets one budget whichever port they use. `site.shopOnMainPort` turns the public routes off on `PORT`.

The embedded SPA is one Vite build with two entries and a relative base: `index.html` is served at `/admin/` and `public.html` at `/` and `/order`. The admin SPA calls its API with relative URLs (`api/...`), so it works under any prefix. `server/static.go` serves the admin entry; `server/public.go` serves the public entry from an allowlist (`public-manifest.json`). For the admin app's security model, see [Authentication](configuration.md#authentication), and for the routing table, see [Routing and hostnames](configuration.md#routing-and-hostnames).

## Security posture

printpaca is secure by default: a generated admin password on first start, a `Host` allowlist against DNS rebinding, a CSRF guard on mutating requests, and rate-limited logins. In multi-user mode, `requirePermission` (`server/identity.go`) enforces each employee's grants at the API layer, on REST handlers and the Spoolman and Moonraker proxies alike. The image runs as a non-root user on `scratch` (no shell, no package manager). The optional integrations are proxied through narrow, read-only routes so the browser needs no CORS configuration.

## Build, test, deploy

The two-entry Vite build is embedded into a multi-stage, multi-arch `scratch` Docker image (about 16 MiB uncompressed). GitHub Actions (`.github/workflows/ci.yml`) is the pipeline.

Test layers, fastest to slowest:

- Vitest unit tests (`web/src`)
- Go unit tests (`cd server && go test ./... -race`), including the golden-vector pricing suite
- Playwright end-to-end tests (`e2e/`) against a real built binary

See the README's [Development](../README.md#development) section for the commands.
