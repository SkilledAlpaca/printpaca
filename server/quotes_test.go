package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newQuoteSendMux wires only POST /api/quotes/send against a fresh store, with dial swapped for the given smtpDialer, and seeds one client with email "client@example.com"
// (the happy-path recipient) so the recipient restriction (handleQuoteSend's clientEmailExists check) needs no per-test client seeding.
func newQuoteSendMux(t *testing.T, dial smtpDialer, configure func(cfg *Config)) *http.ServeMux {
	t.Helper()
	dir := t.TempDir()
	db := openTestDB(t, dir)
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(`INSERT INTO clients (id, name, email, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		newID(), "Beta Print", "client@example.com", now, now); err != nil {
		t.Fatalf("seed client: %v", err)
	}

	cfg := loadTestConfig(t, dir)
	if configure != nil {
		configure(cfg)
	}
	store := NewConfigStore(cfg)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/quotes/send", handleQuoteSend(db, store, dial))
	return mux
}

func postQuoteSend(t *testing.T, mux *http.ServeMux, body string) *httptest.ResponseRecorder {
	return serveReq(mux, httptest.NewRequest("POST", "/api/quotes/send", strings.NewReader(body)))
}

const validQuoteBody = `{"quote":{"id":"q-1","label":"PLA Widget","clientName":"Beta Print","quantity":2,"unitPriceCents":500,"shippingCents":300,"savedAt":"2026-08-01T12:00:00Z"},"to":"client@example.com"}`

func TestQuoteSendUnconfiguredSmtpIs409(t *testing.T) {
	mux := newQuoteSendMux(t, dialSMTP, nil) // Smtp.Host == "" by default
	rec := postQuoteSend(t, mux, validQuoteBody)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body: %s", rec.Code, rec.Body.String())
	}
}

func TestQuoteSendSuccess(t *testing.T) {
	fake := &fakeSMTP{noSTARTTLS: true}
	mux := newQuoteSendMux(t, fake.dialer(t), func(cfg *Config) {
		cfg.Smtp = Smtp{Host: "localhost", Port: 587, From: "quotes@example.com", To: "owner@example.com"}
	})

	rec := postQuoteSend(t, mux, validQuoteBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var resp quoteSendResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Ok || resp.SentAt == "" {
		t.Errorf("resp = %+v, want ok=true and a non-empty sentAt", resp)
	}

	got := fake.snapshot()
	if len(got.rcptTo) != 1 || got.rcptTo[0] != "TO:<client@example.com>" {
		t.Errorf("rcptTo = %v, want client@example.com", got.rcptTo)
	}
	if !strings.Contains(got.dataBody, "3D printed part") || !strings.Contains(got.dataBody, "Shipping") {
		t.Errorf("dataBody missing line items: %q", got.dataBody)
	}
	// validUntil is measured from the posted quote.savedAt
	// ("2026-08-01T12:00:00Z") plus the default cfg.Quote.ValidDays (14),
	// not from the server's clock at send time.
	if !strings.Contains(got.dataBody, "Aug 15, 2026") {
		t.Errorf("dataBody missing valid-until date computed from savedAt: %q", got.dataBody)
	}
}

func TestQuoteSendFailureHidesDetailFromNonAdmin(t *testing.T) {
	fake := &fakeSMTP{noSTARTTLS: true, rejectRcptTo: "client"}
	mux := newQuoteSendMux(t, fake.dialer(t), func(cfg *Config) {
		cfg.Smtp = Smtp{Host: "localhost", Port: 587, From: "quotes@example.com", To: "owner@example.com"}
	})
	req := httptest.NewRequest("POST", "/api/quotes/send", strings.NewReader(validQuoteBody))
	req = req.WithContext(withIdentity(req.Context(), identity{username: "flo"}))
	rec := serveReq(mux, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body: %s", rec.Code, rec.Body.String())
	}
	var body apiErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Errors) != 1 || body.Errors[0] != sendFailureNonAdminMessage {
		t.Fatalf("errors = %v, want the fixed non-admin message", body.Errors)
	}
}

func TestQuoteSendFailurePropagatesVerbatim(t *testing.T) {
	fake := &fakeSMTP{noSTARTTLS: true, rejectRcptTo: "client"}
	mux := newQuoteSendMux(t, fake.dialer(t), func(cfg *Config) {
		cfg.Smtp = Smtp{Host: "localhost", Port: 587, From: "quotes@example.com", To: "owner@example.com"}
	})

	rec := postQuoteSend(t, mux, validQuoteBody)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body: %s", rec.Code, rec.Body.String())
	}
	var body apiErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Errors) != 1 || !strings.Contains(body.Errors[0], "550") {
		t.Fatalf("errors = %v, want the server's verbatim 550 response", body.Errors)
	}
}

func TestQuoteSendValidation(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"missing quote id", `{"quote":{"label":"L","quantity":1,"unitPriceCents":100},"to":"a@example.com"}`},
		{"missing label", `{"quote":{"id":"q-1","quantity":1,"unitPriceCents":100},"to":"a@example.com"}`},
		{"zero quantity", `{"quote":{"id":"q-1","label":"L","quantity":0,"unitPriceCents":100},"to":"a@example.com"}`},
		{"negative unitPriceCents", `{"quote":{"id":"q-1","label":"L","quantity":1,"unitPriceCents":-1},"to":"a@example.com"}`},
		{"negative shippingCents", `{"quote":{"id":"q-1","label":"L","quantity":1,"unitPriceCents":100,"shippingCents":-1},"to":"a@example.com"}`},
		{"missing to", `{"quote":{"id":"q-1","label":"L","quantity":1,"unitPriceCents":100}}`},
		{"to without @", `{"quote":{"id":"q-1","label":"L","quantity":1,"unitPriceCents":100},"to":"not-an-email"}`},
		{"unknown field", `{"quote":{"id":"q-1","label":"L","quantity":1,"unitPriceCents":100},"to":"a@example.com","extra":true}`},
		{"missing savedAt", `{"quote":{"id":"q-1","label":"L","quantity":1,"unitPriceCents":100},"to":"a@example.com"}`},
		{"savedAt not RFC3339", `{"quote":{"id":"q-1","label":"L","quantity":1,"unitPriceCents":100,"savedAt":"2026-08-01"},"to":"a@example.com"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeSMTP{noSTARTTLS: true}
			mux := newQuoteSendMux(t, fake.dialer(t), func(cfg *Config) {
				cfg.Smtp = Smtp{Host: "localhost", Port: 587, From: "quotes@example.com", To: "owner@example.com"}
			})
			rec := postQuoteSend(t, mux, tc.body)
			if rec.Code != http.StatusUnprocessableEntity && rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 422 or 400; body: %s", rec.Code, rec.Body.String())
			}
			if len(fake.snapshot().rcptTo) != 0 {
				t.Error("an invalid request should never reach the SMTP conversation")
			}
		})
	}
}

func TestQuoteSendTaxIncluded(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantStatus int
		wantNote   string // substring expected/absent in the sent email body
	}{
		{
			name:       "absent renders neither note",
			body:       validQuoteBody,
			wantStatus: http.StatusOK,
			wantNote:   "",
		},
		{
			name:       "true renders tax-included note",
			body:       `{"quote":{"id":"q-1","label":"PLA Widget","clientName":"Beta Print","quantity":2,"unitPriceCents":500,"shippingCents":300,"savedAt":"2026-08-01T12:00:00Z","taxIncluded":true},"to":"client@example.com"}`,
			wantStatus: http.StatusOK,
			wantNote:   "Prices include tax.",
		},
		{
			name:       "false renders tax-excluded note",
			body:       `{"quote":{"id":"q-1","label":"PLA Widget","clientName":"Beta Print","quantity":2,"unitPriceCents":500,"shippingCents":300,"savedAt":"2026-08-01T12:00:00Z","taxIncluded":false},"to":"client@example.com"}`,
			wantStatus: http.StatusOK,
			wantNote:   "Prices exclude sales tax, which is added at invoicing.",
		},
		{
			name:       "non-boolean is rejected",
			body:       `{"quote":{"id":"q-1","label":"PLA Widget","clientName":"Beta Print","quantity":2,"unitPriceCents":500,"shippingCents":300,"savedAt":"2026-08-01T12:00:00Z","taxIncluded":"yes"},"to":"client@example.com"}`,
			wantStatus: http.StatusBadRequest,
			wantNote:   "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeSMTP{noSTARTTLS: true}
			mux := newQuoteSendMux(t, fake.dialer(t), func(cfg *Config) {
				cfg.Smtp = Smtp{Host: "localhost", Port: 587, From: "quotes@example.com", To: "owner@example.com"}
			})
			rec := postQuoteSend(t, mux, tc.body)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus != http.StatusOK {
				return
			}
			got := fake.snapshot()
			if tc.wantNote == "" {
				for _, note := range []string{"Prices include tax.", "Prices exclude sales tax"} {
					if strings.Contains(got.dataBody, note) {
						t.Errorf("dataBody contains %q, want neither note when taxIncluded is absent", note)
					}
				}
				return
			}
			if !strings.Contains(got.dataBody, tc.wantNote) {
				t.Errorf("dataBody missing %q: %q", tc.wantNote, got.dataBody)
			}
		})
	}
}

// `to` must match some client's email on file (case-insensitively) though the request is otherwise well-formed; TestQuoteSendSuccess is the allowed counterpart.
func TestQuoteSendUnknownRecipientIs422(t *testing.T) {
	fake := &fakeSMTP{noSTARTTLS: true}
	mux := newQuoteSendMux(t, fake.dialer(t), func(cfg *Config) {
		cfg.Smtp = Smtp{Host: "localhost", Port: 587, From: "quotes@example.com", To: "owner@example.com"}
	})

	body := `{"quote":{"id":"q-1","label":"PLA Widget","clientName":"Stranger","quantity":2,"unitPriceCents":500,"shippingCents":300,"savedAt":"2026-08-01T12:00:00Z"},"to":"stranger@example.com"}`
	rec := postQuoteSend(t, mux, body)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body: %s", rec.Code, rec.Body.String())
	}
	var body2 apiErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body2); err != nil {
		t.Fatalf("unmarshal error body: %v", err)
	}
	if len(body2.Errors) != 1 || !strings.Contains(body2.Errors[0], "recipient is not a client on file") {
		t.Fatalf("errors = %v, want recipient-not-a-client message", body2.Errors)
	}
	if len(fake.snapshot().rcptTo) != 0 {
		t.Error("an unknown recipient should never reach the SMTP conversation")
	}
}

func TestQuoteSendOversizedBodyIs413(t *testing.T) {
	fake := &fakeSMTP{noSTARTTLS: true}
	mux := newQuoteSendMux(t, fake.dialer(t), func(cfg *Config) {
		cfg.Smtp = Smtp{Host: "localhost", Port: 587, From: "quotes@example.com", To: "owner@example.com"}
	})
	body := `{"quote":{"id":"q-1","label":"` + strings.Repeat("x", quoteSendMaxBytes+1) + `"},"to":"a@example.com"}`
	rec := postQuoteSend(t, mux, body)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
}
