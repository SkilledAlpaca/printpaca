package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/smtp"
	"strings"
	"testing"
)

func TestBuildRequestNotification(t *testing.T) {
	cfg := &Config{Currency: Currency{Symbol: "$", Decimals: 2}}

	cases := []struct {
		name       string
		req        QuoteRequest
		wantSubj   string
		wantLines  []string
		wantAbsent []string
	}{
		{
			name: "with note",
			req: QuoteRequest{
				ID: "req-1", Name: "Ada Lovelace", Email: "ada@example.com",
				Material: "PLA", Quantity: 3, WeightG: 42.5, TimeSeconds: 3725,
				PriceCents: 1234, Note: "please rush",
			},
			wantSubj: "New estimate request from Ada Lovelace",
			wantLines: []string{
				"Name: Ada Lovelace",
				"Email: ada@example.com",
				"Material: PLA",
				"Quantity: 3",
				"Weight: 42.50 g",
				"Print time: 1 h 2 min",
				"Price: $12.34",
				"Note: please rush",
				"Request ID: req-1",
			},
		},
		{
			name: "without note",
			req: QuoteRequest{
				ID: "req-2", Name: "Bob", Email: "bob@example.com",
				Material: "PETG", Quantity: 1, WeightG: 10, TimeSeconds: 60,
				PriceCents: 500, Note: "",
			},
			wantSubj: "New estimate request from Bob",
			wantLines: []string{
				"Name: Bob",
				"Print time: 0 h 1 min",
				"Price: $5.00",
				"Request ID: req-2",
			},
			wantAbsent: []string{"Note:"},
		},
		{
			name: "CRLF in name cannot inject headers into the subject",
			req: QuoteRequest{
				ID: "req-4", Name: "Eve\r\nBcc: victim@example.com", Email: "eve@example.com",
				Material: "PLA", Quantity: 1, WeightG: 1, TimeSeconds: 0,
				PriceCents: 100,
			},
			wantSubj:  "New estimate request from Eve  Bcc: victim@example.com",
			wantLines: []string{"Price: $1.00"},
		},
		{
			name: "sub-100-cent price",
			req: QuoteRequest{
				ID: "req-3", Name: "Cy", Email: "cy@example.com",
				Material: "PLA", Quantity: 1, WeightG: 1, TimeSeconds: 0,
				PriceCents: 45,
			},
			wantSubj:  "New estimate request from Cy",
			wantLines: []string{"Price: $0.45"},
		},
		{
			// Pins the shared formatCurrency's thousands grouping on a 4+ digit amount (the owner notification once formatted its price by hand).
			name: "four-digit price is thousands-grouped",
			req: QuoteRequest{
				ID: "req-5", Name: "Dana", Email: "dana@example.com",
				Material: "PLA", Quantity: 1, WeightG: 1, TimeSeconds: 0,
				PriceCents: 123456789,
			},
			wantSubj:  "New estimate request from Dana",
			wantLines: []string{"Price: $1,234,567.89"},
		},
		{
			name: "item order with option",
			req: QuoteRequest{
				ID: "req-6", Kind: "item", Name: "Fay", Email: "fay@example.com",
				ItemID: "item-1", ItemName: "Benchy", OptionName: "Glow",
				Quantity: 2, PriceCents: 3500, Note: "gift wrap",
			},
			wantSubj: "New order for Benchy from Fay",
			wantLines: []string{
				"Name: Fay",
				"Email: fay@example.com",
				"Item: Benchy",
				"Option: Glow",
				"Quantity: 2",
				"Price: $35.00",
				"Note: gift wrap",
				"Request ID: req-6",
			},
			wantAbsent: []string{"Material:", "Weight:", "Print time:"},
		},
		{
			name: "item order without an option",
			req: QuoteRequest{
				ID: "req-7", Kind: "item", Name: "Gus", Email: "gus@example.com",
				ItemID: "item-2", ItemName: "Widget", OptionName: "",
				Quantity: 1, PriceCents: 500,
			},
			wantSubj: "New order for Widget from Gus",
			wantLines: []string{
				"Item: Widget",
				"Quantity: 1",
				"Price: $5.00",
				"Request ID: req-7",
			},
			wantAbsent: []string{"Option:", "Material:", "Weight:", "Print time:", "Note:"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			subject, body := buildRequestNotification(cfg, tc.req)
			if subject != tc.wantSubj {
				t.Errorf("subject = %q, want %q", subject, tc.wantSubj)
			}
			for _, line := range tc.wantLines {
				if !strings.Contains(body, line) {
					t.Errorf("body missing %q; body:\n%s", line, body)
				}
			}
			for _, absent := range tc.wantAbsent {
				if strings.Contains(body, absent) {
					t.Errorf("body unexpectedly contains %q; body:\n%s", absent, body)
				}
			}
		})
	}
}

func TestSendRequestNotificationHostEmptySkipsDial(t *testing.T) {
	cfg := &Config{} // Smtp.Host == ""
	dialed := false
	dial := func(addr string) (*smtp.Client, error) {
		dialed = true
		return nil, errors.New("dial should not have been called")
	}

	sendRequestNotification(cfg, QuoteRequest{ID: "req-1", Name: "Ada"}, dial)

	if dialed {
		t.Error("dial was called despite empty smtp.host")
	}
}

// The startup warning for the case sendMailWith silently falls through unauthenticated: smtp.host and smtp.username set, SMTP_PASSWORD unset (see warnIfSmtpAuthMisconfigured).
func TestWarnIfSmtpAuthMisconfigured(t *testing.T) {
	cases := []struct {
		name        string
		host        string
		username    string
		password    string
		wantWarning bool
	}{
		{"host+username, no password: warns", "smtp.example.com", "shop", "", true},
		{"host+username, password set: silent", "smtp.example.com", "shop", "hunter2", false},
		{"no username: silent (unauthenticated is the deliberate default)", "smtp.example.com", "", "", false},
		{"smtp not configured at all: silent", "", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SMTP_PASSWORD", tc.password)
			cfg := &Config{Smtp: Smtp{Host: tc.host, Username: tc.username}}

			var buf bytes.Buffer
			defer redirectLog(t, &buf)()
			warnIfSmtpAuthMisconfigured(cfg)

			gotWarning := strings.Contains(buf.String(), "SMTP_PASSWORD")
			if gotWarning != tc.wantWarning {
				t.Errorf("warning logged = %v, want %v; log output: %q", gotWarning, tc.wantWarning, buf.String())
			}
		})
	}
}

func TestSendRequestNotificationDialsWhenHostSet(t *testing.T) {
	cfg := &Config{Smtp: Smtp{Host: "smtp.example.com", Port: 587, From: "quotes@example.com", To: "owner@example.com"}}
	var dialedAddr string
	dial := func(addr string) (*smtp.Client, error) {
		dialedAddr = addr
		return nil, errors.New("pretend dial failure")
	}

	sendRequestNotification(cfg, QuoteRequest{ID: "req-1", Name: "Ada"}, dial)

	if dialedAddr != "smtp.example.com:587" {
		t.Errorf("dialed addr = %q, want %q", dialedAddr, "smtp.example.com:587")
	}
}

// ---------------------------------------------------------------------------
// sendMailWith
// ---------------------------------------------------------------------------

// sendMailWith never wraps or rewords a failure, so err.Error() is exactly what the dialer produced; that lets the smtp test endpoint and an invoice/quote send's 502 body show the real cause.
func TestSendMailWithErrorPropagatesVerbatim(t *testing.T) {
	cfg := testSmtpConfig(t, "")
	sentinel := errors.New("connection refused by fake dialer")
	dial := func(addr string) (*smtp.Client, error) { return nil, sentinel }

	err := sendMailWith(cfg, dial, "client@example.com", "subj", "text", "")
	if err == nil || err.Error() != sentinel.Error() {
		t.Fatalf("err = %v, want exactly %q", err, sentinel.Error())
	}
}

// Happy path over the fake: no STARTTLS advertised (none attempted), no username (no AUTH), MAIL/RCPT/DATA/QUIT succeed, and the sent envelope and body match.
func TestSendMailNoSTARTTLSFullConversation(t *testing.T) {
	cfg := testSmtpConfig(t, "") // no username: AUTH must not be attempted
	fake := &fakeSMTP{noSTARTTLS: true}

	err := sendMailWith(cfg, fake.dialer(t), "client@example.com", "Test subject", "plain body", "")
	if err != nil {
		t.Fatalf("sendMailWith: %v", err)
	}

	got := fake.snapshot()
	if got.sawSTARTTLS {
		t.Error("STARTTLS was performed despite not being advertised")
	}
	if got.authPlain != "" {
		t.Errorf("AUTH was attempted with no username configured: %q", got.authPlain)
	}
	if got.mailFrom != "FROM:<quotes@example.com>" {
		t.Errorf("mailFrom = %q, want FROM:<quotes@example.com>", got.mailFrom)
	}
	if len(got.rcptTo) != 1 || got.rcptTo[0] != "TO:<client@example.com>" {
		t.Errorf("rcptTo = %v, want [TO:<client@example.com>]", got.rcptTo)
	}
	if !strings.Contains(got.dataBody, "Test subject") || !strings.Contains(got.dataBody, "plain body") {
		t.Errorf("dataBody missing subject/body: %q", got.dataBody)
	}
}

// STARTTLS is attempted whenever advertised. The fake advertises but closes instead of upgrading, which only happens if the client tried; a client ignoring the advertisement would get normal replies and err == nil.
// The error must be non-nil and propagated unwrapped.
func TestSendMailAttemptsSTARTTLSWhenAdvertised(t *testing.T) {
	cfg := testSmtpConfig(t, "")
	fake := &fakeSMTP{breakSTARTTLS: true}

	err := sendMailWith(cfg, fake.dialer(t), "client@example.com", "subj", "text", "")
	if err == nil {
		t.Fatal("sendMailWith returned nil, want a TLS handshake error (STARTTLS was advertised)")
	}
	// Never sent MAIL/RCPT: the handshake failure must abort before the
	// envelope, not paper over it and continue in plaintext.
	got := fake.snapshot()
	if got.mailFrom != "" {
		t.Errorf("mailFrom = %q, want empty (send must abort on STARTTLS failure)", got.mailFrom)
	}
}

// With cfg.Smtp.RequireTLS, a server not advertising STARTTLS must abort with a clear error instead of falling through to plaintext (the default, covered above).
func TestSendMailRequireTLSFailsWhenNoSTARTTLS(t *testing.T) {
	cfg := testSmtpConfig(t, "")
	cfg.Smtp.RequireTLS = true
	fake := &fakeSMTP{noSTARTTLS: true}

	err := sendMailWith(cfg, fake.dialer(t), "client@example.com", "s", "b", "")
	if err == nil {
		t.Fatal("sendMailWith returned nil, want an error (requireTLS set, server didn't advertise STARTTLS)")
	}
	if !strings.Contains(err.Error(), "requireTLS") || !strings.Contains(err.Error(), "STARTTLS") {
		t.Errorf("err = %q, want a message naming requireTLS and STARTTLS so the cause is actionable", err.Error())
	}

	// Must abort before the envelope, not paper over the missing TLS and
	// continue in plaintext.
	got := fake.snapshot()
	if got.mailFrom != "" {
		t.Errorf("mailFrom = %q, want empty (send must abort before MAIL when requireTLS fails)", got.mailFrom)
	}
}

// requireTLS must not affect the advertised-STARTTLS path. A real handshake over the fake deadlocks (net.Pipe is unbuffered and neither side is reading during the TLS 1.3 flight exchange),
// so this reuses breakSTARTTLS (advertise, then close) for a fast I/O failure. RequireTLS=true must attempt STARTTLS and never reach the "no STARTTLS advertised" branch;
// the error is the raw handshake failure, not the requireTLS-specific message.
func TestSendMailRequireTLSDoesNotAffectAdvertisedSTARTTLSPath(t *testing.T) {
	cfg := testSmtpConfig(t, "")
	cfg.Smtp.RequireTLS = true
	fake := &fakeSMTP{breakSTARTTLS: true}

	err := sendMailWith(cfg, fake.dialer(t), "client@example.com", "s", "b", "")
	if err == nil {
		t.Fatal("sendMailWith returned nil, want a TLS handshake error (STARTTLS was advertised, then broken)")
	}
	if strings.Contains(err.Error(), "requireTLS") {
		t.Errorf("err = %q, should be the raw STARTTLS handshake error -- requireTLS's own branch must not fire when STARTTLS WAS advertised", err.Error())
	}
}

// Control: with RequireTLS false (the default) a no-STARTTLS server behaves as before; redundant with TestSendMailNoSTARTTLSFullConversation but explicit and searchable.
func TestSendMailRequireTLSOffUnaffectedByMissingSTARTTLS(t *testing.T) {
	cfg := testSmtpConfig(t, "")
	// cfg.Smtp.RequireTLS left false (zero value).
	fake := &fakeSMTP{noSTARTTLS: true}

	if err := sendMailWith(cfg, fake.dialer(t), "client@example.com", "s", "b", ""); err != nil {
		t.Fatalf("sendMailWith: %v", err)
	}
}

// AUTH is attempted only when both cfg.Smtp.Username and SMTP_PASSWORD are set, with the exact credentials PLAIN auth encodes.
func TestSendMailAuthSentOnlyWhenUsernameAndPasswordConfigured(t *testing.T) {
	cases := []struct {
		name        string
		username    string
		password    string
		wantAttempt bool
	}{
		{"neither set", "", "", false},
		{"username without password", "alice", "", false},
		{"both set", "alice", "hunter2", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SMTP_PASSWORD", tc.password)
			cfg := testSmtpConfig(t, tc.username)
			// net/smtp's PlainAuth refuses credentials over plaintext to a non-localhost host; that is production behavior being exercised, so the fixture host must satisfy it.
			cfg.Smtp.Host = "localhost"
			fake := &fakeSMTP{noSTARTTLS: true}

			if err := sendMailWith(cfg, fake.dialer(t), "client@example.com", "s", "b", ""); err != nil {
				t.Fatalf("sendMailWith: %v", err)
			}

			got := fake.snapshot()
			attempted := got.authPlain != ""
			if attempted != tc.wantAttempt {
				t.Fatalf("AUTH attempted = %v, want %v (authPlain=%q)", attempted, tc.wantAttempt, got.authPlain)
			}
			if tc.wantAttempt {
				want := "\x00" + tc.username + "\x00" + tc.password
				if got.authPlain != want {
					t.Errorf("authPlain = %q, want %q", got.authPlain, want)
				}
			}
		})
	}
}

// TestSendMailAuthFailurePropagatesVerbatim: a 535 from the server aborts
// the send with the server's own response text intact, not a generic
// "auth failed" message.
func TestSendMailAuthFailurePropagatesVerbatim(t *testing.T) {
	t.Setenv("SMTP_PASSWORD", "hunter2")
	cfg := testSmtpConfig(t, "alice")
	cfg.Smtp.Host = "localhost" // see TestSendMailAuthSentOnlyWhenUsernameAndPasswordConfigured
	fake := &fakeSMTP{noSTARTTLS: true, rejectAuth: true}

	err := sendMailWith(cfg, fake.dialer(t), "client@example.com", "s", "b", "")
	if err == nil {
		t.Fatal("sendMailWith returned nil, want the server's 535 to propagate")
	}
	if !strings.Contains(err.Error(), "535") || !strings.Contains(err.Error(), "authentication failed") {
		t.Errorf("err = %q, want it to contain the server's verbatim 535 response", err.Error())
	}
}

// TestSendMailRcptFailurePropagatesVerbatim: an SMTP-level recipient
// rejection (550) is what a caller's status line should show, not a
// generic "invoices not writable" message.
func TestSendMailRcptFailurePropagatesVerbatim(t *testing.T) {
	cfg := testSmtpConfig(t, "")
	fake := &fakeSMTP{noSTARTTLS: true, rejectRcptTo: "nobody"}

	err := sendMailWith(cfg, fake.dialer(t), "nobody@example.com", "s", "b", "")
	if err == nil {
		t.Fatal("sendMailWith returned nil, want the server's 550 to propagate")
	}
	if !strings.Contains(err.Error(), "550") {
		t.Errorf("err = %q, want it to contain the server's verbatim 550 response", err.Error())
	}
}

// A label reaching `to`, `subject`, or cfg.Smtp.From with an embedded CRLF must not add a second header line (e.g. a forged Bcc) to the message net/smtp's Data() writer sends verbatim.
func TestBuildMimeMessageSanitizesHeaderInjection(t *testing.T) {
	evil := "victim@example.com\r\nBcc: evil@example.com"

	cases := []struct {
		name    string
		to      string
		subject string
		from    string
	}{
		{"injection via to", evil, "Subject line", "quotes@example.com"},
		{"injection via subject", "victim@example.com", "Hello\r\nBcc: evil@example.com", "quotes@example.com"},
		{"injection via cfg.Smtp.From", "victim@example.com", "Hello", evil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testSmtpConfig(t, "")
			cfg.Smtp.From = tc.from
			msg := string(buildMimeMessage(cfg, tc.to, tc.subject, "body", ""))

			if strings.Contains(msg, "\r\nBcc:") {
				t.Fatalf("message contains an injected Bcc header line; got:\n%s", msg)
			}
			lines := strings.Split(msg, "\r\n")
			headerLines := 0
			for _, line := range lines {
				if strings.HasPrefix(line, "To:") || strings.HasPrefix(line, "Subject:") || strings.HasPrefix(line, "From:") {
					headerLines++
				}
			}
			if headerLines != 3 {
				t.Fatalf("expected exactly 3 header lines (To/Subject/From), got %d; message:\n%s", headerLines, msg)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// buildMimeMessage
// ---------------------------------------------------------------------------

func TestBuildMimeMessagePlainTextShape(t *testing.T) {
	cfg := testSmtpConfig(t, "")
	msg := string(buildMimeMessage(cfg, "client@example.com", "Hello", "plain text body", ""))

	for _, want := range []string{
		// encodeAddressHeader routes bare addresses through
		// net/mail.Address.String(), which always emits the angle-addr form
		// even without a display name.
		"From: <quotes@example.com>\r\n",
		"To: <client@example.com>\r\n",
		"Subject: Hello\r\n",
		"Content-Type: text/plain; charset=utf-8\r\n",
		"plain text body",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q; got:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "multipart") {
		t.Errorf("plain-text message unexpectedly mentions multipart; got:\n%s", msg)
	}
}

func TestBuildMimeMessageMultipartAlternativeShape(t *testing.T) {
	cfg := testSmtpConfig(t, "")
	msg := string(buildMimeMessage(cfg, "client@example.com", "Invoice", "plain fallback", "<b>html body</b>"))

	for _, want := range []string{
		"Content-Type: multipart/alternative; boundary=",
		"Content-Type: text/plain; charset=utf-8",
		"plain fallback",
		"Content-Type: text/html; charset=utf-8",
		"<b>html body</b>",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q; got:\n%s", want, msg)
		}
	}
}

// A non-ASCII subject must never appear as raw UTF-8 in a header; it must be an RFC 2047 encoded-word a receiving MUA can decode.
func TestBuildMimeMessageEncodesNonASCIISubject(t *testing.T) {
	cfg := testSmtpConfig(t, "")
	subject := "Facture pour Café Déluxe"
	msg := string(buildMimeMessage(cfg, "client@example.com", subject, "body", ""))

	lines := strings.Split(msg, "\r\n")
	var subjectLine string
	for _, line := range lines {
		if strings.HasPrefix(line, "Subject:") {
			subjectLine = line
			break
		}
	}
	if subjectLine == "" {
		t.Fatal("no Subject header line found")
	}
	if strings.Contains(subjectLine, "Café") {
		t.Errorf("Subject header contains raw non-ASCII text, want an encoded-word: %q", subjectLine)
	}
	if !strings.Contains(subjectLine, "=?utf-8?") && !strings.Contains(subjectLine, "=?UTF-8?") {
		t.Fatalf("Subject header is not an RFC 2047 encoded-word: %q", subjectLine)
	}

	// And it must decode back to the original text.
	encoded := strings.TrimPrefix(subjectLine, "Subject: ")
	dec := new(mime.WordDecoder)
	decoded, err := dec.DecodeHeader(encoded)
	if err != nil {
		t.Fatalf("DecodeHeader(%q): %v", encoded, err)
	}
	if decoded != subject {
		t.Errorf("decoded subject = %q, want %q", decoded, subject)
	}
}

// For a From of the form `Name <addr>` with a non-ASCII Name, the angle-addr (which MAIL FROM relies on) must survive verbatim and only the display name is RFC 2047-encoded, unlike Subject.
func TestBuildMimeMessageEncodesFromDisplayNameOnly(t *testing.T) {
	cfg := testSmtpConfig(t, "")
	cfg.Smtp.From = "Bäckerei Müller <shop@example.com>"
	msg := string(buildMimeMessage(cfg, "client@example.com", "Hello", "body", ""))

	lines := strings.Split(msg, "\r\n")
	var fromLine string
	for _, line := range lines {
		if strings.HasPrefix(line, "From:") {
			fromLine = line
			break
		}
	}
	if fromLine == "" {
		t.Fatal("no From header line found")
	}
	if !strings.HasSuffix(fromLine, "<shop@example.com>") {
		t.Errorf("From header angle-addr not intact: %q", fromLine)
	}
	if strings.Contains(fromLine, "Bäckerei") || strings.Contains(fromLine, "Müller") {
		t.Errorf("From header contains raw non-ASCII display name, want an encoded-word: %q", fromLine)
	}
	if !strings.Contains(fromLine, "=?utf-8?") && !strings.Contains(fromLine, "=?UTF-8?") {
		t.Fatalf("From header display name is not an RFC 2047 encoded-word: %q", fromLine)
	}
}

// RFC 5322 hard-caps a line at 998 bytes; free-text payment instructions or terms (2000-char caps in config.go) could exceed it as one unbroken line, so the quoted-printable output must soft-wrap.
func TestBuildMimeMessageBodyLinesStayUnder998Bytes(t *testing.T) {
	long := strings.Repeat("a", 5000)
	msg := buildMimeMessage(testSmtpConfig(t, ""), "client@example.com", "subj", long, "")

	for line := range strings.SplitSeq(string(msg), "\r\n") {
		if len(line) > 998 {
			t.Fatalf("line of %d bytes exceeds the 998-byte RFC 5322 limit: %q...", len(line), line[:80])
		}
	}
}

// ---------------------------------------------------------------------------
// POST /api/smtp/test
// ---------------------------------------------------------------------------

func TestHandleSmtpTestUnconfiguredIs409(t *testing.T) {
	cfg, err := LoadConfig(t.TempDir()) // Smtp.Host == "" by default
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	store := NewConfigStore(cfg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/smtp/test", strings.NewReader("{}"))
	handleSmtpTest(store, dialSMTP)(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleSmtpTestSuccessReturnsOk(t *testing.T) {
	cfg := testSmtpConfig(t, "")
	store := NewConfigStore(cfg)
	fake := &fakeSMTP{noSTARTTLS: true}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/smtp/test", strings.NewReader("{}"))
	handleSmtpTest(store, fake.dialer(t))(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var resp smtpTestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Ok || resp.Error != "" {
		t.Errorf("resp = %+v, want ok=true, error=\"\"", resp)
	}
	if fake.snapshot().rcptTo[0] != "TO:<owner@example.com>" {
		t.Errorf("test email did not go to configured smtp.to: %+v", fake.snapshot())
	}
}

func TestHandleSmtpTestFailureReturnsErrorVerbatim(t *testing.T) {
	cfg := testSmtpConfig(t, "")
	store := NewConfigStore(cfg)
	fake := &fakeSMTP{noSTARTTLS: true, rejectRcptTo: "owner"}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/smtp/test", strings.NewReader("{}"))
	handleSmtpTest(store, fake.dialer(t))(rec, req)

	// ok/error, not an HTTP error status: a failed test send is an
	// expected, displayable outcome.
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var resp smtpTestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Ok {
		t.Fatal("resp.Ok = true, want false")
	}
	if !strings.Contains(resp.Error, "550") {
		t.Errorf("resp.Error = %q, want the server's verbatim 550 response", resp.Error)
	}
}
