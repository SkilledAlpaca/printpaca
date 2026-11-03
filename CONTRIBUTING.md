# Contributing to printpaca

This project follows the [Code of Conduct](CODE_OF_CONDUCT.md). Report vulnerabilities privately; see [SECURITY.md](SECURITY.md).

## Dev setup

- Node 26 (npm workspace: `web`)
- Go 1.26.9 or later (the floor declared in `server/go.mod`)

```bash
npm install
npx playwright install --with-deps chromium   # one-time: browser binary npm run e2e needs
```

## Test commands

Run these before opening a PR: they're the same gates CI runs.

```bash
npx vitest run                       # the full vitest suite
npm run check -w web                 # svelte-check (0 errors required)
npm run e2e                          # the Playwright suite (real Go binary on :8099)
cd server && test -z "$(gofmt -l .)" && go vet ./... && go test ./... -race -count=1
```

`npm run build` builds `web` only, into the gitignored `web/dist`; it never touches `server/embedded/dist`, so `git status --porcelain` stays empty. The tracked `server/embedded/dist` placeholder is replaced only inside the Docker build (the Dockerfile's web-build stage); `go build` and `go test` in `server` work standalone against the placeholder.

To regenerate the documentation screenshots after a UI change, run `node --experimental-strip-types scripts/screenshot-docs.ts`.

## House rules

1. **Cost or pricing math changes need unit tests and a `docs/formulas.md` update in the same PR.** Update the formula table, and the design decisions table if you change pinned behavior; `docs/formulas.md` must stay accurate. If a change affects the golden-vector suite (`server/pricing_compat_test.go`), the suite must stay green or the change must be documented as intentional.

2. **No personal data, and no hard-coded fees or prices without a `source` annotation.** Nothing under version control may contain real names, addresses, tokens, or other personal data, because this project is meant to be self-hosted by strangers. A hard-coded monetary assumption (a fee schedule, a default price) needs a `source` field (see `feeSchedules.*.source` in `server/defaults.yaml`) citing where the number came from and when you last checked it.

3. **Update `THIRD_PARTY_NOTICES.md` when a runtime dependency changes.** Adding, removing, or upgrading a Go module in `server`'s build graph, or an npm package whose code or data is bundled into `web/dist` (not a build-only devDependency such as Vite or TypeScript), needs a matching update in the same PR. The file explains how to re-derive the list and verify license text.

4. **The database baseline is fixed.** `migrations[0]` in `server/db.go` is the complete baseline schema at `user_version` 1, and `testdata/schema-v1.sql` is the `sqlite_master` dump `TestBaselineMatchesSchemaFixture` compares it against. A new schema change appends a migration after the baseline and never edits the baseline or the fixture; add a test for the new migration alongside it.

## Pull requests

Open an issue first for anything larger than a small fix, so we can agree on the approach. Keep each PR to one change, and fill in the PR template checklist.

## Scope boundaries

`server/*.go` and `web/src` implement the calculator itself, and changes there come with tests. Config, docs, and CI live outside that boundary and can be updated independently.
