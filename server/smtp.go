package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"os"
	"strings"
	"time"
)

// Outbound SMTP for the app: owner new-request notifications (fire-and-forget), sendMailWith for invoice
// and quote sends, and the test-email endpoint. All share opportunistic STARTTLS (mandatory with
// smtp.requireTLS), then PLAIN auth only when a username is configured and SMTP_PASSWORD is set.

// smtpDialTimeout bounds the initial TCP connect.
const smtpDialTimeout = 10 * time.Second

// smtpSessionDeadline bounds the conversation; keep it under main.go's 60s WriteTimeout and web's 45s abort.
const smtpSessionDeadline = 30 * time.Second

// smtpDialer opens an SMTP client to addr; tests inject a stub to avoid the network.
type smtpDialer func(addr string) (*smtp.Client, error)

// dialSMTP is the production smtpDialer. STARTTLS happens later, once the server's extensions are known.
func dialSMTP(addr string) (*smtp.Client, error) {
	conn, err := net.DialTimeout("tcp", addr, smtpDialTimeout)
	if err != nil {
		return nil, err
	}
	// Bounds dial through QUIT; see smtpSessionDeadline.
	if err := conn.SetDeadline(time.Now().Add(smtpSessionDeadline)); err != nil {
		conn.Close()
		return nil, err
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		conn.Close()
		return nil, err
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return client, nil
}

// sanitizeHeader strips CR and LF. A visitor name reaches Subject with only a length cap, so this
// prevents header injection.
func sanitizeHeader(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' {
			return ' '
		}
		return r
	}, s)
}

// encodeHeaderWord RFC 2047-encodes s for a header value; a no-op for plain ASCII, so call sites need
// no check. Callers run sanitizeHeader first.
func encodeHeaderWord(s string) string {
	return mime.QEncoding.Encode("utf-8", s)
}

// encodeAddressHeader formats a From/To value. QEncoding the whole "Name <addr>" would encode the
// brackets and @, so it parses with net/mail and re-emits via Address.String(), which encodes only a
// non-ASCII display name. An unparsable value (config only requires "@") falls back to the sanitized value.
func encodeAddressHeader(value string) string {
	sanitized := sanitizeHeader(value)
	addr, err := mail.ParseAddress(sanitized)
	if err != nil {
		return sanitized
	}
	return addr.String()
}

// buildRequestNotification renders the owner's plain-text email for a new public request: a quote
// request (Kind != "item"; "" counts as quote) or a gallery item order. Pure, no I/O.
func buildRequestNotification(cfg *Config, req QuoteRequest) (subject, body string) {
	priceCents := max(req.PriceCents, 0)
	price := formatCurrency(cfg, priceCents)

	var b bytes.Buffer
	fmt.Fprintf(&b, "Name: %s\n", req.Name)
	fmt.Fprintf(&b, "Email: %s\n", req.Email)

	if req.Kind == "item" {
		subject = fmt.Sprintf("New order for %s from %s", sanitizeHeader(req.ItemName), sanitizeHeader(req.Name))
		fmt.Fprintf(&b, "Item: %s\n", req.ItemName)
		if req.OptionName != "" {
			fmt.Fprintf(&b, "Option: %s\n", req.OptionName)
		}
		fmt.Fprintf(&b, "Quantity: %d\n", req.Quantity)
		fmt.Fprintf(&b, "Price: %s\n", price)
	} else {
		subject = fmt.Sprintf("New estimate request from %s", sanitizeHeader(req.Name))
		hours := req.TimeSeconds / 3600
		minutes := (req.TimeSeconds % 3600) / 60
		fmt.Fprintf(&b, "Material: %s\n", req.Material)
		fmt.Fprintf(&b, "Quantity: %d\n", req.Quantity)
		fmt.Fprintf(&b, "Weight: %.2f g\n", req.WeightG)
		fmt.Fprintf(&b, "Print time: %d h %d min\n", hours, minutes)
		fmt.Fprintf(&b, "Price: %s\n", price)
	}

	if req.Note != "" {
		fmt.Fprintf(&b, "\nNote: %s\n", req.Note)
	}
	fmt.Fprintf(&b, "\nRequest ID: %s\n", req.ID)

	return subject, b.String()
}

// buildMimeMessage assembles RFC 5322 headers plus body: single text/plain, or multipart/alternative
// text+html when htmlBody is set. Subject uses encodeHeaderWord and From/To use encodeAddressHeader.
// Bodies are quoted-printable (normalizes \n to \r\n, lines under 76 bytes) so they survive 7-bit
// relays. bytes.Buffer never fails, so writer errors are ignored.
func buildMimeMessage(cfg *Config, to, subject, textBody, htmlBody string) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\n", encodeAddressHeader(cfg.Smtp.From))
	fmt.Fprintf(&b, "To: %s\r\n", encodeAddressHeader(to))
	fmt.Fprintf(&b, "Subject: %s\r\n", encodeHeaderWord(sanitizeHeader(subject)))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")

	if htmlBody == "" {
		b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
		b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
		b.WriteString("\r\n")
		writeQuotedPrintable(&b, textBody)
		return b.Bytes()
	}

	mw := multipart.NewWriter(&b)
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%s\r\n\r\n", mw.Boundary())
	if pw, err := mw.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {"text/plain; charset=utf-8"},
		"Content-Transfer-Encoding": {"quoted-printable"},
	}); err == nil {
		writeQuotedPrintable(pw, textBody)
	}
	if pw, err := mw.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {"text/html; charset=utf-8"},
		"Content-Transfer-Encoding": {"quoted-printable"},
	}); err == nil {
		writeQuotedPrintable(pw, htmlBody)
	}
	mw.Close()
	return b.Bytes()
}

// writeQuotedPrintable ignores errors: every caller writes into a bytes.Buffer-backed writer.
func writeQuotedPrintable(w io.Writer, s string) {
	qw := quotedprintable.NewWriter(w)
	qw.Write([]byte(s))
	qw.Close()
}

// sendMailWith runs one SMTP conversation: dial, STARTTLS (mandatory with RequireTLS), optional PLAIN
// auth, MAIL/RCPT/DATA/QUIT. Callers must check cfg.Smtp.Host != "" first. Errors are returned
// unwrapped so callers can show the real cause verbatim. dial is stubbed in tests.
func sendMailWith(cfg *Config, dial smtpDialer, to, subject, textBody, htmlBody string) error {
	addr := fmt.Sprintf("%s:%d", cfg.Smtp.Host, cfg.Smtp.Port)

	c, err := dial(addr)
	if err != nil {
		return err
	}
	defer c.Close()

	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: cfg.Smtp.Host}); err != nil {
			return err
		}
	} else if cfg.Smtp.RequireTLS {
		// requireTLS fails closed: a server not advertising STARTTLS is a silent downgrade to plaintext, which
		// a hostile path could induce by stripping the advertisement. Implicit-TLS ports (465) aren't supported.
		return fmt.Errorf("smtp.requireTLS is set but %s did not advertise STARTTLS; refusing to send in plaintext", cfg.Smtp.Host)
	}

	if cfg.Smtp.Username != "" {
		if password := os.Getenv("SMTP_PASSWORD"); password != "" {
			auth := smtp.PlainAuth("", cfg.Smtp.Username, password, cfg.Smtp.Host)
			if err := c.Auth(auth); err != nil {
				return err
			}
		}
	}

	if err := c.Mail(cfg.Smtp.From); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}

	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(buildMimeMessage(cfg, to, subject, textBody, htmlBody)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// warnIfSmtpAuthMisconfigured logs a startup warning when smtp.host and smtp.username are set but
// SMTP_PASSWORD is empty. Otherwise sendMailWith attempts an unauthenticated send and the user sees only
// the relay's raw "530 Authentication Required". Called at startup and after settings saves.
func warnIfSmtpAuthMisconfigured(cfg *Config) {
	if cfg.Smtp.Host == "" || cfg.Smtp.Username == "" {
		return
	}
	if os.Getenv("SMTP_PASSWORD") == "" {
		log.Printf("printpaca: WARNING: smtp.username is set (%q) but the SMTP_PASSWORD environment variable is empty. Outgoing mail will be attempted without authentication, which most SMTP relays reject. Set SMTP_PASSWORD and restart the container, or clear smtp.username on the Settings page to send without auth intentionally.", cfg.Smtp.Username)
	}
}

// notifyNewRequest runs via `go` after a submission's insert succeeds; it never errors, failures are logged.
func notifyNewRequest(cfg *Config, req QuoteRequest) {
	sendRequestNotification(cfg, req, dialSMTP)
}

// sendRequestNotification composes and sends the owner notification. Split out so tests can inject a
// dialer and assert the empty-Host guard short-circuits before dialing.
func sendRequestNotification(cfg *Config, req QuoteRequest, dial smtpDialer) {
	if cfg.Smtp.Host == "" {
		return // notifications disabled; deliberately silent, no log spam
	}
	subject, body := buildRequestNotification(cfg, req)
	if err := sendMailWith(cfg, dial, cfg.Smtp.To, subject, body, ""); err != nil {
		log.Printf("smtp notify: %v", err)
	}
}

// smtpTestResponse is handleSmtpTest's 200 body; Error is empty on success, else err.Error() verbatim.
type smtpTestResponse struct {
	Ok    bool   `json:"ok"`
	Error string `json:"error"`
}

// handleSmtpTest implements POST /api/smtp/test: sends a fixed message to smtp.to synchronously, so
// the response is the result. 409 when SMTP isn't configured; otherwise 200 with ok/error, since a
// failed send is an expected, displayable outcome.
func handleSmtpTest(store *ConfigStore, dial smtpDialer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := store.Get() // one snapshot for this request
		if cfg.Smtp.Host == "" {
			writeAPIError(w, http.StatusConflict, "email isn't configured; set up SMTP in Settings")
			return
		}

		// The body is documented as {}: discard a capped amount so a client sending one isn't rejected.
		io.Copy(io.Discard, io.LimitReader(r.Body, 4096))

		err := sendMailWith(cfg, dial, cfg.Smtp.To,
			"printpaca test email",
			"This is a test email from printpaca. If it arrived, SMTP delivery works.",
			"")

		resp := smtpTestResponse{Ok: err == nil}
		if err != nil {
			resp.Error = err.Error()
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}
