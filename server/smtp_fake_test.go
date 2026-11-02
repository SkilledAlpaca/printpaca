package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"io"
	"math/big"
	"net"
	"net/smtp"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"
)

// An in-process SMTP peer built from the standard library over net.Pipe (no dependency, no real socket), because sendMailWith's TLS/auth/DATA behavior can't be exercised by a dialer that never returns a working client.
// Shared by smtp_test.go, invoices_test.go and quotes_test.go.

// fakeSMTP configures a fakeSMTPServer's behavior and records what the client sent. The zero value advertises STARTTLS, completes the TLS handshake, and accepts everything.
type fakeSMTP struct {
	noSTARTTLS    bool   // don't advertise STARTTLS at all
	breakSTARTTLS bool   // advertise STARTTLS, but close the conn instead of completing the handshake (simulates a broken/misconfigured TLS peer)
	rejectAuth    bool   // 535 instead of 235 for AUTH
	rejectRcptTo  string // RCPT TO containing this substring gets 550 instead of 250

	mu          sync.Mutex
	sawSTARTTLS bool
	authPlain   string // decoded "\x00user\x00pass" payload, once seen
	mailFrom    string
	rcptTo      []string
	dataBody    string
}

// snapshot returns a copy of the recorded fields for assertions, guarded by
// the same mutex the serving goroutine writes through.
func (f *fakeSMTP) snapshot() fakeSMTP {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fakeSMTP{
		sawSTARTTLS: f.sawSTARTTLS,
		authPlain:   f.authPlain,
		mailFrom:    f.mailFrom,
		rcptTo:      append([]string(nil), f.rcptTo...),
		dataBody:    f.dataBody,
	}
}

// dialer returns an smtpDialer wired to this fake: each call opens a net.Pipe, serves one end, and returns the *smtp.Client over the other. The server name is the host half of addr,
// because net/smtp's PlainAuth checks it against isLocalhost and the constructed host, so a placeholder would break every AUTH test.
func (f *fakeSMTP) dialer(t *testing.T) smtpDialer {
	t.Helper()
	return func(addr string) (*smtp.Client, error) {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		clientConn, serverConn := net.Pipe()
		go f.serve(t, serverConn)
		return smtp.NewClient(clientConn, host)
	}
}

// selfSignedTLSConfig generates a throwaway in-memory ECDSA cert for the STARTTLS handshake; the client trusts nothing here, so serve() relies on a client config that skips verification.
func selfSignedTLSConfig(t *testing.T) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test.invalid"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"test.invalid"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	return &tls.Config{Certificates: []tls.Certificate{cert}}
}

// serve implements the server half of the conversation over conn (one end
// of a net.Pipe), a minimal-but-real SMTP dialog: greeting, EHLO
// capabilities, optional STARTTLS, AUTH PLAIN, MAIL/RCPT/DATA/QUIT.
func (f *fakeSMTP) serve(t *testing.T, conn net.Conn) {
	tc := textproto.NewConn(conn)
	defer tc.Close()

	if err := tc.PrintfLine("220 test.invalid ESMTP"); err != nil {
		return
	}

	for {
		line, err := tc.ReadLine()
		if err != nil {
			return
		}
		verb, arg, _ := strings.Cut(line, " ")
		verb = strings.ToUpper(verb)

		switch verb {
		case "EHLO", "HELO":
			if f.noSTARTTLS {
				tc.PrintfLine("250 test.invalid")
				continue
			}
			tc.PrintfLine("250-test.invalid")
			tc.PrintfLine("250 STARTTLS")

		case "STARTTLS":
			if f.breakSTARTTLS {
				// Advertise support but misbehave: close instead of upgrading, so the client's handshake fails and sendMailWith gets a real TLS error to propagate verbatim.
				tc.PrintfLine("220 Ready to start TLS")
				conn.Close()
				return
			}
			tc.PrintfLine("220 Ready to start TLS")
			tlsConn := tls.Server(conn, selfSignedTLSConfig(t))
			if err := tlsConn.Handshake(); err != nil {
				return
			}
			f.mu.Lock()
			f.sawSTARTTLS = true
			f.mu.Unlock()
			conn = tlsConn
			tc = textproto.NewConn(conn)

		case "AUTH":
			// net/smtp sends "AUTH PLAIN <base64>" as a single line for
			// PlainAuth (no server challenge round-trip).
			_, b64, _ := strings.Cut(arg, " ")
			decoded, _ := base64.StdEncoding.DecodeString(b64)
			f.mu.Lock()
			f.authPlain = string(decoded)
			f.mu.Unlock()
			if f.rejectAuth {
				tc.PrintfLine("535 authentication failed")
			} else {
				tc.PrintfLine("235 authentication successful")
			}

		case "MAIL":
			f.mu.Lock()
			f.mailFrom = arg
			f.mu.Unlock()
			tc.PrintfLine("250 OK")

		case "RCPT":
			if f.rejectRcptTo != "" && strings.Contains(arg, f.rejectRcptTo) {
				tc.PrintfLine("550 no such user")
				continue
			}
			f.mu.Lock()
			f.rcptTo = append(f.rcptTo, arg)
			f.mu.Unlock()
			tc.PrintfLine("250 OK")

		case "DATA":
			tc.PrintfLine("354 send message, end with .")
			dr := tc.DotReader()
			body, _ := io.ReadAll(dr)
			f.mu.Lock()
			f.dataBody = string(body)
			f.mu.Unlock()
			tc.PrintfLine("250 OK: queued")

		case "QUIT":
			tc.PrintfLine("221 bye")
			return

		default:
			tc.PrintfLine("500 unrecognized command")
		}
	}
}

// testSmtpConfig returns a Config with a non-empty Smtp block, the shared fixture for the "configured" branch of every send path; host/port are never dialed (the fake dialer ignores addr).
func testSmtpConfig(t *testing.T, username string) *Config {
	t.Helper()
	cfg := loadTestConfig(t, t.TempDir())
	cfg.Smtp = Smtp{
		Host:     "smtp.example.com",
		Port:     587,
		Username: username,
		From:     "quotes@example.com",
		To:       "owner@example.com",
	}
	return cfg
}
