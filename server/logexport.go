package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"sync"
	"time"
)

// GET /api/logs/errors: the Settings page's Diagnostics export of recent error-looking lines from this
// process's log output. printpaca writes no log files (stderr goes to Docker), so installLogTee mirrors
// the stream into a bounded in-memory ring and this handler filters, redacts, and serves it as text.

// logRingMaxBytes bounds appLogRing at ~4 MiB. A live diagnostic aid, not durable: a restart clears it.
const logRingMaxBytes = 4 * 1024 * 1024

// logRingBuffer is a bounded, goroutine-safe, line-granular ring. Once buffered bytes exceed maxBytes
// the oldest lines are evicted, so a chatty error loop can't leak memory.
type logRingBuffer struct {
	mu       sync.Mutex
	lines    [][]byte // oldest first
	size     int      // sum of len(lines[i]) currently held
	maxBytes int
}

func newLogRingBuffer(maxBytes int) *logRingBuffer {
	return &logRingBuffer{maxBytes: maxBytes}
}

// Write implements io.Writer for the log tee. Each call is stored as one entry even if it contains
// embedded newlines (multi-line banners), so the ring's byte accounting matches what the caller
// considered one log statement. It never fails and blocks only on its own mutex.
func (r *logRingBuffer) Write(p []byte) (int, error) {
	line := append([]byte(nil), p...) // copy: p's backing array may be reused by the caller
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, line)
	r.size += len(line)
	// The len > 1 guard keeps a single oversized line rather than evicting the buffer to empty.
	for r.size > r.maxBytes && len(r.lines) > 1 {
		r.size -= len(r.lines[0])
		r.lines = r.lines[1:]
	}
	return len(p), nil
}

// snapshot returns a copy of every buffered line, oldest first, taken under the lock so a Write can't race.
func (r *logRingBuffer) snapshot() [][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]byte, len(r.lines))
	copy(out, r.lines)
	return out
}

// appLogRing is the process-wide ring, installed by installLogTee from main(). Tests build their own
// *logRingBuffer; installLogTee is never called under `go test`.
var appLogRing = newLogRingBuffer(logRingMaxBytes)

// processStart approximates process start for the export's "uptime" line.
var processStart = time.Now()

// installLogTee tees the standard logger to out (os.Stderr in production, so the visible stream is
// unchanged) and ring. main() calls it first so early startup lines are captured.
func installLogTee(ring *logRingBuffer, out io.Writer) {
	log.SetOutput(io.MultiWriter(out, ring))
}

// isErrorLine (via errorLineWordPattern and errorLineAccessPattern) is a text heuristic: logging is
// unleveled, so there is no severity field. It can miss real problems and catch benign lines using
// these words; that is accepted over adding a level to every call site.
//
// Keywords come from the codebase's own format strings (rejected, invalid, missing, full, warning)
// and from common Go/SQLite error text (cannot, unable, refused, denied, timeout, exceeded, locked,
// error, panic). adminAuthDisabledWarning matches ("WARNING"); the informational demoModeWarning does not.
//
// The access-log alternative matches the line's 4xx/5xx status field instead of vocabulary: the
// public listener logs every status, and ordinary 200s must not match. The anchor tolerates the
// standard logger's LstdFlags prefix (optionally with microseconds), since production lines start
// with a timestamp; it stays anchored so a quoted path containing "access ..." can't match.
//
// The two patterns are tried in order, word pattern first, because most matches hit a keyword and
// the access-log pattern is pricier. This is equivalent to one combined alternation.
var (
	errorLineWordPattern = regexp.MustCompile(
		`(?i:\b(?:error|errors|fail(?:ed|ure|s)?|refused|denied|rejected|invalid|panic|cannot|unable|missing|full|warning|timeout|exceeded|locked)\b)`,
	)
	errorLineAccessPattern = regexp.MustCompile(
		`^(?:\d{4}/\d{2}/\d{2} )?(?:\d{2}:\d{2}:\d{2}(?:\.\d{1,6})? )?access \S+ "[^"]*" "[^"]*" [45]\d\d `,
	)
)

// isErrorLine reports whether line matches the heuristic above, trying the cheap word pattern first.
func isErrorLine(line []byte) bool {
	return errorLineWordPattern.Match(line) || errorLineAccessPattern.Match(line)
}

// --- redaction ---
// The ring stays raw; every rule below applies only to the copy of matched lines written to the response.

// ipv4Pattern matches a dotted-quad with each octet bounded to 0-255, so version strings don't match.
var ipv4Pattern = regexp.MustCompile(
	`\b(?:(?:25[0-5]|2[0-4]\d|1?\d?\d)\.){3}(?:25[0-5]|2[0-4]\d|1?\d?\d)\b`,
)

// ipv6Pattern covers the full 8-group form, "::" compressed forms, and a leading "::". It is not
// RFC 4291-exact (zone IDs and embedded IPv4 tails aren't handled; clientIP never produces them) and
// leaves timestamps ("23:00:00") and durations ("250ms") alone.
var ipv6Pattern = regexp.MustCompile(
	`\b(?:[0-9A-Fa-f]{1,4}:){7}[0-9A-Fa-f]{1,4}\b` +
		`|\b(?:[0-9A-Fa-f]{1,4}:){1,7}:(?:[0-9A-Fa-f]{1,4}(?::[0-9A-Fa-f]{1,4}){0,6})?` +
		`|::(?:[0-9A-Fa-f]{1,4}:){0,6}[0-9A-Fa-f]{1,4}`,
)

// emailPattern is a simple practical matcher, enough for client emails interpolated into error messages.
var emailPattern = regexp.MustCompile(
	`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b`,
)

// secretRunPattern matches a long run of [A-Za-z0-9+/=_-]: tokens, keys, JWTs and hashes, without
// recognizing each shape. 24 sits below the 32-hex object ids and above anything incidental in prose.
// Accepted cost: those 32-char object ids are redacted too; the operator can cross-reference stderr.
var secretRunPattern = regexp.MustCompile(`[A-Za-z0-9+/_=-]{24,}`)

// credentialKeyPattern redacts a credential-ish key and the rest of its line ("Authorization: Bearer
// ...", "apiKey=...", "token: ..."), regardless of value shape (a short password is still a secret).
// printpaca never logs these, but %v-interpolated library errors might, so this stays defensive.
var credentialKeyPattern = regexp.MustCompile(
	`(?i)\b(authorization|api[_-]?key|apikey|token|secret|password|passwd)\b\s*[:=]\s*.*`,
)

// bearerPattern catches a bare "Bearer <token>" not preceded by a recognized key.
var bearerPattern = regexp.MustCompile(`(?i)\bbearer\s+\S+`)

// redactor applies every rule to one export, with a per-export pseudonym map so the same IP or email
// gets the same [ip-N]/[email-N] tag across lines. handleLogsErrors builds a fresh one per request, so
// tags carry no meaning between exports.
type redactor struct {
	ips    map[string]string
	emails map[string]string
}

func newRedactor() *redactor {
	return &redactor{ips: map[string]string{}, emails: map[string]string{}}
}

// tag returns value's pseudonym in m, minting the next "[prefix-N]" (1-based, first-seen order) if new.
func (rd *redactor) tag(m map[string]string, prefix, value string) string {
	if t, ok := m[value]; ok {
		return t
	}
	t := fmt.Sprintf("[%s-%d]", prefix, len(m)+1)
	m[value] = t
	return t
}

// redact applies, in order: credential pairs, bare bearer, long runs, IPv6, IPv4, then emails.
// credentialKeyPattern must consume a whole "Authorization: Bearer <token>" before secretRunPattern
// fragments it, and IPv6 runs before IPv4 so a dotted-quad isn't carved out of an IPv4-in-IPv6 form.
func (rd *redactor) redact(line string) string {
	line = credentialKeyPattern.ReplaceAllString(line, "${1}: [redacted]")
	line = bearerPattern.ReplaceAllString(line, "[redacted]")
	line = secretRunPattern.ReplaceAllStringFunc(line, func(string) string { return "[redacted]" })
	line = ipv6Pattern.ReplaceAllStringFunc(line, func(m string) string { return rd.tag(rd.ips, "ip", m) })
	line = ipv4Pattern.ReplaceAllStringFunc(line, func(m string) string { return rd.tag(rd.ips, "ip", m) })
	line = emailPattern.ReplaceAllStringFunc(line, func(m string) string { return rd.tag(rd.emails, "email", m) })
	return line
}

// handleLogsErrors implements GET /api/logs/errors on the admin mux, so it goes through the full admin chain.
// It is a GET, so demoWriteGuard ignores it; it needs its own demo check: the demo serves nothing here,
// since the buffer may hold another visitor's rate-limit or auth-rejection lines. The rejection uses
// the writeAuthError JSON shape.
func handleLogsErrors(ring *logRingBuffer, demoMode bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if demoMode {
			writeAuthError(w, http.StatusForbidden, demoReadOnlyMessage)
			return
		}

		lines := ring.snapshot()
		rd := newRedactor()
		matched := make([][]byte, 0, len(lines))
		for _, line := range lines {
			if isErrorLine(line) {
				matched = append(matched, []byte(rd.redact(string(line))))
			}
		}

		now := time.Now()
		filename := fmt.Sprintf("printpaca-errors-%s.txt", now.Format("2006-01-02"))
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
		w.Header().Set("Cache-Control", "no-store")
		fmt.Fprintf(w, "# printpaca error log export\n")
		fmt.Fprintf(w, "# version: %s\n", version)
		fmt.Fprintf(w, "# uptime: %s\n", now.Sub(processStart).Round(time.Second))
		fmt.Fprintf(w, "# lines: %d buffered, %d matched\n", len(lines), len(matched))
		fmt.Fprintf(w, "#\n")
		for _, line := range matched {
			w.Write(line)
			if len(line) == 0 || line[len(line)-1] != '\n' {
				w.Write([]byte("\n"))
			}
		}
	}
}
