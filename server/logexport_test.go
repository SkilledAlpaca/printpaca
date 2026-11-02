package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// --- logRingBuffer ---

// Once the running total exceeds maxBytes the oldest lines go first, one at a time, until under the cap; the newest lines always survive.
func TestLogRingBufferEvictsOldestLinesOnceOverCap(t *testing.T) {
	ring := newLogRingBuffer(50) // 5 lines of 10 bytes each fit exactly
	for i := range 20 {
		fmt.Fprintf(ring, "line-%04d\n", i) // 10 bytes each, incl. newline
	}
	lines := ring.snapshot()
	if len(lines) != 5 {
		t.Fatalf("len(lines) = %d, want 5 (50 bytes / 10 bytes-per-line)", len(lines))
	}
	// Oldest surviving line should be line-0015 (the last 5 of 0..19).
	for i, want := 0, 15; i < len(lines); i, want = i+1, want+1 {
		got := strings.TrimSpace(string(lines[i]))
		wantStr := fmt.Sprintf("line-%04d", want)
		if got != wantStr {
			t.Fatalf("lines[%d] = %q, want %q", i, got, wantStr)
		}
	}
}

// Covers the len(r.lines) > 1 guard: a single line larger than maxBytes is kept, so the ring is never left empty.
func TestLogRingBufferNeverEvictsDownToEmpty(t *testing.T) {
	ring := newLogRingBuffer(10)
	big := strings.Repeat("x", 100)
	fmt.Fprintln(ring, big)
	lines := ring.snapshot()
	if len(lines) != 1 {
		t.Fatalf("len(lines) = %d, want 1 (oversized line kept, not evicted)", len(lines))
	}
	if got := strings.TrimSpace(string(lines[0])); got != big {
		t.Fatalf("lines[0] = %q, want the oversized line intact", got)
	}
}

// Run with -race, which is the actual assertion: no data race, and buffered bytes never exceed maxBytes once writers settle.
func TestLogRingBufferConcurrentWritesRace(t *testing.T) {
	ring := newLogRingBuffer(1024)
	const goroutines = 20
	const linesEach = 200

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := range goroutines {
		go func(g int) {
			defer wg.Done()
			for i := range linesEach {
				fmt.Fprintf(ring, "writer-%d line-%d\n", g, i)
				_ = ring.snapshot() // concurrent reader, same as an export mid-write-storm
			}
		}(g)
	}
	wg.Wait()

	lines := ring.snapshot()
	total := 0
	for _, l := range lines {
		total += len(l)
	}
	if total > ring.maxBytes {
		t.Fatalf("buffered bytes = %d, want <= maxBytes %d", total, ring.maxBytes)
	}
	if len(lines) == 0 {
		t.Fatal("ring is empty after concurrent writes, want at least the most recent lines retained")
	}
}

// --- isErrorLine ---

// Uses real formatted-log shapes from this codebase (and realistic stdlib/driver error text) and asserts each is recognized as an error line.
func TestErrorLinePatternMatchesRealErrorLines(t *testing.T) {
	cases := []string{
		// clients.go / items.go / invoices.go / ... "<what>: %v" call sites,
		// with realistic net/database driver error text.
		`clients query: dial tcp 127.0.0.1:5432: connect: connection refused`,
		`db: open printpaca.db: unable to open database file`,
		`quotes update: database is locked`,
		`items insert: context deadline exceeded`,
		// invoices.go: literal "invalid" in the format string itself.
		`invoices: invalid stored issueDate "bad-date": parsing time "bad-date" as "2006-01-02": cannot parse "bad-date" as "2006"`,
		// public.go: literal "invalid" in the format string.
		`public quote-preview: recompute produced an invalid breakdown for stored inputs`,
		// auth.go: literal "rejected".
		`admin listener: rejected request for disallowed Host "evil.example"; set ALLOWED_HOSTS to include it if this host is legitimate (see README's Authentication section)`,
		// public.go: literal "full".
		`public quote-request: uploads storage full (dirSize=123 cap=456)`,
		`public quote-request: notification queue full, dropping notification for request abc123`,
		// retention.go: literal "missing".
		`retention: gcode file missing for request abc123 (expected at /x): skipping prune this pass, will retry once the file/volume is available`,
		// smtp.go: literal "WARNING".
		`printpaca: WARNING: smtp.username is set ("bob") but the SMTP_PASSWORD environment variable is empty. Outgoing mail will be attempted without authentication, which most SMTP relays reject.`,
		// accesslog.go's own line shape, admin listener (401/403/5xx only).
		`access admin "GET" "/api/clients" 401 3ms "203.0.113.5"`,
		`access admin "POST" "/api/clients" 500 12ms "203.0.113.5"`,
		// accesslog.go's own line shape, public listener (logs every status).
		`access public "POST" "/api/quote-request" 429 2ms "203.0.113.9"`,
		`access public "GET" "/api/config" 400 1ms "203.0.113.9"`,
		// The same access-line shape as it actually reaches the ring in
		// production: main never calls log.SetFlags, so LstdFlags prepends
		// "2006/01/02 15:04:05 " to every entry.
		`2026/09/16 17:04:05 access admin "GET" "/api/clients" 401 3ms "203.0.113.5"`,
		`2026/09/16 17:04:05.123456 access public "GET" "/api/config" 400 1ms "203.0.113.9"`,
		// main.go: log.Fatalf-wrapped config load error, realistic yaml text.
		`config: yaml: line 4: found character that cannot start any token`,
		// Go's own panic/recover vocabulary.
		`http: panic serving 203.0.113.9:54321: runtime error: index out of range`,
	}
	for _, line := range cases {
		if !isErrorLine([]byte(line)) {
			t.Errorf("isErrorLine did not match expected error line: %q", line)
		}
	}
}

// Ordinary informational output, including the multi-line startup banners (demoModeWarning, adminAuthDisabledWarning) and public access-log lines, must not be flagged.
func TestErrorLinePatternSkipsNonErrorLines(t *testing.T) {
	cases := []string{
		`printpaca 1.4.0 listening on :8080 (config: /config)`,
		`printpaca: ADMIN_PASSWORD is set; the admin listener requires login`,
		`printpaca: Use this password to sign in: sw0rdfish-example`,
		`shutting down`,
		`printpaca public listener on :9090`,
		`demo seed: 3 clients, 2 quotes, 1 invoices, 0 payments, 4 requests`,
		`public quote-request: queued notification for request a1b2c3d4e5f60718293a4b5c6d7e8f90`,
		// A successful/redirect/not-modified access-log line on either
		// listener must never match -- only the [45]xx alternative should.
		`access public "GET" "/" 200 1ms "203.0.113.9"`,
		`access public "GET" "/item-images/x.jpg" 304 1ms "203.0.113.9"`,
		`2026/09/16 17:04:05 access public "GET" "/" 200 1ms "203.0.113.9"`,
		// demoModeWarning (main.go): informational, no keyword present --
		// one log.Print call, so one buffered entry despite the embedded
		// newlines.
		"printpaca: ================================================================\n" +
			"printpaca: DEMO_MODE is enabled. This instance is a READ-ONLY DEMO.\n" +
			"printpaca: Every admin write is blocked (except login/logout), the public\n" +
			"printpaca: quote submission is simulated -- nothing is persisted -- and the\n" +
			"printpaca: Spoolman/Moonraker integrations serve fake sample data.\n" +
			"printpaca: ================================================================",
	}
	for _, line := range cases {
		if isErrorLine([]byte(line)) {
			t.Errorf("isErrorLine unexpectedly matched non-error line: %q", line)
		}
	}
}

// adminAuthDisabledWarning IS a genuine security warning and contains "WARNING", so an errors export must capture it; checked against the actual constant.
func TestErrorLinePatternMatchesAdminAuthDisabledWarning(t *testing.T) {
	if !isErrorLine([]byte(adminAuthDisabledWarning)) {
		t.Error("isErrorLine did not match adminAuthDisabledWarning; a real startup security warning should be captured")
	}
}

// TestErrorLinePatternSkipsDemoModeWarning is the demoModeWarning half of
// the same check, against the actual constant.
func TestErrorLinePatternSkipsDemoModeWarning(t *testing.T) {
	if isErrorLine([]byte(demoModeWarning)) {
		t.Error("isErrorLine matched demoModeWarning; it is informational, not an error, and should be excluded")
	}
}

// --- redact ---

func TestRedactIPv4(t *testing.T) {
	rd := newRedactor()
	got := rd.redact(`admin listener: rejected request for disallowed Host "203.0.113.5"`)
	want := `admin listener: rejected request for disallowed Host "[ip-1]"`
	if got != want {
		t.Fatalf("redact() = %q, want %q", got, want)
	}
}

func TestRedactIPv6(t *testing.T) {
	rd := newRedactor()
	got := rd.redact(`login attempt from ::1`)
	want := `login attempt from [ip-1]`
	if got != want {
		t.Fatalf("redact() = %q, want %q", got, want)
	}

	rd2 := newRedactor()
	got2 := rd2.redact(`peer 2001:db8::ff00:42:8329 closed the connection`)
	want2 := `peer [ip-1] closed the connection`
	if got2 != want2 {
		t.Fatalf("redact() = %q, want %q", got2, want2)
	}
}

func TestRedactEmail(t *testing.T) {
	rd := newRedactor()
	got := rd.redact(`quotes send: recipient lookup: no client with email jane.doe@example.com`)
	want := `quotes send: recipient lookup: no client with email [email-1]`
	if got != want {
		t.Fatalf("redact() = %q, want %q", got, want)
	}
}

func TestRedactLongHexOrBase64Run(t *testing.T) {
	rd := newRedactor()
	// A 32-lowercase-hex object id (db.go's newID shape).
	got := rd.redact(`public quote-request: queued notification for request a1b2c3d4e5f60718293a4b5c6d7e8f90`)
	want := `public quote-request: queued notification for request [redacted]`
	if got != want {
		t.Fatalf("redact() = %q, want %q", got, want)
	}
}

func TestRedactCredentialKeyAndBearerToken(t *testing.T) {
	rd := newRedactor()
	got := rd.redact(`Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0`)
	want := `Authorization: [redacted]`
	if got != want {
		t.Fatalf("redact() = %q, want %q", got, want)
	}

	rd2 := newRedactor()
	got2 := rd2.redact(`smtp dial: token=abc123def456ghi789jklmno rejected by relay`)
	want2 := `smtp dial: token: [redacted]`
	if got2 != want2 {
		t.Fatalf("redact() = %q, want %q", got2, want2)
	}

	// SMTP_PASSWORD is an env var name, not a "password:"/"password=" credential pair, and must not be redacted as one.
	rd3 := newRedactor()
	line := `printpaca: WARNING: smtp.username is set ("bob") but the SMTP_PASSWORD environment variable is empty.`
	if got3 := rd3.redact(line); got3 != line {
		t.Fatalf("redact() = %q, want unchanged %q (SMTP_PASSWORD is an identifier, not a credential key/value pair)", got3, line)
	}
}

// The same real IP/email gets the same pseudonym within one export (one redactor), distinct values get distinct ones, numbered in first-seen order.
func TestRedactPseudonymStabilityWithinOneExport(t *testing.T) {
	rd := newRedactor()
	l1 := rd.redact(`request from 203.0.113.9 for client jane@example.com`)
	l2 := rd.redact(`second request from 203.0.113.9, different client bob@example.com`)
	l3 := rd.redact(`third request from 203.0.113.42 for client jane@example.com again`)

	if !strings.Contains(l1, "[ip-1]") || !strings.Contains(l1, "[email-1]") {
		t.Fatalf("l1 = %q, want [ip-1] and [email-1]", l1)
	}
	if !strings.Contains(l2, "[ip-1]") {
		t.Fatalf("l2 = %q, want the same [ip-1] as l1 (same address)", l2)
	}
	if !strings.Contains(l2, "[email-2]") {
		t.Fatalf("l2 = %q, want a new [email-2] (different address than l1)", l2)
	}
	if !strings.Contains(l3, "[ip-2]") {
		t.Fatalf("l3 = %q, want a new [ip-2] (different address than l1/l2)", l3)
	}
	if !strings.Contains(l3, "[email-1]") {
		t.Fatalf("l3 = %q, want the same [email-1] as l1 (same address recurring)", l3)
	}

	// A fresh redactor (a new export) must not remember the mapping above.
	rd2 := newRedactor()
	l4 := rd2.redact(`request from 203.0.113.42 for client jane@example.com`)
	if !strings.Contains(l4, "[ip-1]") || !strings.Contains(l4, "[email-1]") {
		t.Fatalf("l4 = %q, want numbering to restart at 1 for a new redactor/export", l4)
	}
}

// TestRedactMixedLine exercises every class together on one line, the same
// way a real formatted error line could plausibly combine several of them.
func TestRedactMixedLine(t *testing.T) {
	rd := newRedactor()
	in := `mixed line ip=192.0.2.1 email=user@example.org token=abc123def456ghi789jklmno secret line`
	got := rd.redact(in)
	want := `mixed line ip=[ip-1] email=[email-1] token: [redacted]`
	if got != want {
		t.Fatalf("redact() = %q, want %q", got, want)
	}
}

// --- GET /api/logs/errors ---

func TestHandleLogsErrorsDemoModeBlocked(t *testing.T) {
	ring := newLogRingBuffer(logRingMaxBytes)
	fmt.Fprintln(ring, "clients query: connection refused")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/logs/errors", nil)
	handleLogsErrors(ring, true)(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 in demo mode", rec.Code)
	}
	var body authErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v (body: %s)", err, rec.Body.String())
	}
	if body.Error != demoReadOnlyMessage {
		t.Fatalf("error = %q, want the guard's usual demoReadOnlyMessage %q", body.Error, demoReadOnlyMessage)
	}
}

// TestHandleLogsErrorsHeaderAndFiltering pins the response shape: text/plain
// attachment, a header with correct buffered/matched counts, and a body
// containing only the matched (and redacted) lines, in original order.
func TestHandleLogsErrorsHeaderAndFiltering(t *testing.T) {
	ring := newLogRingBuffer(logRingMaxBytes)
	fmt.Fprintln(ring, "printpaca 1.4.0 listening on :8080 (config: /config)") // not an error
	fmt.Fprintln(ring, "clients query: connection refused from 203.0.113.5")   // error, has an IP
	fmt.Fprintln(ring, "shutting down")                                        // not an error
	fmt.Fprintln(ring, "items insert: context deadline exceeded")              // error

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/logs/errors", nil)
	handleLogsErrors(ring, false)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("Content-Type = %q, want text/plain", ct)
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") || !strings.Contains(cd, "printpaca-errors-") || !strings.Contains(cd, ".txt") {
		t.Fatalf("Content-Disposition = %q, want an attachment filename shaped like printpaca-errors-<date>.txt", cd)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "lines: 4 buffered, 2 matched") {
		t.Fatalf("body header missing correct counts, got:\n%s", body)
	}
	if strings.Contains(body, "listening on") || strings.Contains(body, "shutting down") {
		t.Fatalf("body contains a non-matched line, got:\n%s", body)
	}
	if !strings.Contains(body, "connection refused from [ip-1]") {
		t.Fatalf("body missing redacted matched error line, got:\n%s", body)
	}
	if !strings.Contains(body, "context deadline exceeded") {
		t.Fatalf("body missing second matched error line, got:\n%s", body)
	}
	// Order: the connection-refused line was buffered before the
	// context-deadline-exceeded line, and must stay in that order.
	if strings.Index(body, "connection refused") > strings.Index(body, "context deadline exceeded") {
		t.Fatalf("matched lines out of order, got:\n%s", body)
	}
}

// Through the real admin chain (adminListenerHandler), an enabled authState with no session must 401 before the handler runs.
func TestLogsErrorsRouteRequiresAuth(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	auth := newAuthState("secret", nil) // enabled, no session presented below
	handler := adminListenerHandler(mux, auth, false)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/logs/errors", nil)
	req.Host = "localhost"
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (no session presented)", rec.Code)
	}
}

// Through registerRoutes with demoMode=true and auth disabled, only this handler's own check can block the GET (demoWriteGuard ignores GETs).
func TestLogsErrorsRouteBlockedInDemoModeThroughRealMux(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	cfg := loadTestConfig(t, dir)
	mux := routesMux(db, NewConfigStore(cfg), newAuthState("", nil), dir, true) // demoMode true, auth disabled

	rec := serveReq(mux, httptest.NewRequest("GET", "/api/logs/errors", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 in demo mode", rec.Code)
	}
}

// After installLogTee everything logged still reaches the original writer unchanged and also lands in the ring.
// Saves and restores log.Writer() around the test so it never leaks into other tests.
func TestInstallLogTeeMirrorsToRingWithoutChangingOriginalStream(t *testing.T) {
	prev := log.Writer()
	prevFlags := log.Flags()
	t.Cleanup(func() {
		log.SetOutput(prev)
		log.SetFlags(prevFlags)
	})
	log.SetFlags(0) // deterministic output for the byte-identical comparison below

	var out bytes.Buffer
	ring := newLogRingBuffer(logRingMaxBytes)
	installLogTee(ring, &out)

	log.Print("hello from the tee test")

	if got, want := out.String(), "hello from the tee test\n"; got != want {
		t.Fatalf("original stream got %q, want byte-identical %q", got, want)
	}
	lines := ring.snapshot()
	if len(lines) != 1 || string(lines[0]) != out.String() {
		t.Fatalf("ring lines = %q, want exactly one line matching the original stream %q", lines, out.String())
	}
}
