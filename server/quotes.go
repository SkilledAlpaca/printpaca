package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// This file implements POST /api/quotes/send. Unlike an invoice send it loads no stored quote: it composes
// the email from the narrow quoteSendInput in the body, validated like the public quote-request form.
// Nothing is persisted: the quote's client-owned statusLog is its send record.

// quoteSendMaxBytes is 64 KiB: up to maxQuoteSendLines lines with 500-char labels that may be \uXXXX-escaped.
const quoteSendMaxBytes = 64 * 1024

// maxQuoteSendLines bounds quoteSendInput.Lines: one row per part of a multi-part order.
const maxQuoteSendLines = maxOrderParts

// quoteSendInput is the display data a quote's send needs, narrower than the full SavedQuote.
// The caller extracts it with convertQuote.ts's helpers, so the endpoint never parses the engine snapshot.
type quoteSendInput struct {
	ID string `json:"id"`
	// Reference is the owner-entered quote number or name shown to the customer; empty shows "your quote".
	Reference      string  `json:"reference"`
	Label          string  `json:"label"`
	ClientName     string  `json:"clientName"`
	Quantity       float64 `json:"quantity"`
	UnitPriceCents int     `json:"unitPriceCents"`
	ShippingCents  int     `json:"shippingCents"`
	// Lines, when non-empty, replaces the flat Label/Quantity/UnitPriceCents line (then ignored,
	// unvalidated). The email renders one row per line, then Shipping.
	Lines []quoteSendLine `json:"lines,omitempty"`
	// SavedAt is the quote's own savedAt (RFC3339); validUntil is measured from it, not the server clock,
	// so re-sending an old quote doesn't reset its validity window.
	SavedAt string `json:"savedAt"`

	// TaxIncluded says whether the quoted price already includes tax (the invoice adds sales tax if not).
	// A *bool so absent (no tax note) differs from false.
	TaxIncluded *bool `json:"taxIncluded,omitempty"`
}

// quoteSendLine is one emailed row of a multi-part quote.
type quoteSendLine struct {
	Label          string  `json:"label"`
	Quantity       float64 `json:"quantity"`
	UnitPriceCents int     `json:"unitPriceCents"`
}

// quoteSendRequest is the full POST /api/quotes/send body.
type quoteSendRequest struct {
	Quote quoteSendInput `json:"quote"`
	To    string         `json:"to"`
}

// validateQuoteSendRequest trims fields in place and returns one message per problem (untrusted input).
func validateQuoteSendRequest(in *quoteSendRequest) []string {
	in.Quote.ID = strings.TrimSpace(in.Quote.ID)
	in.Quote.Label = strings.TrimSpace(in.Quote.Label)
	in.Quote.ClientName = strings.TrimSpace(in.Quote.ClientName)
	in.Quote.SavedAt = strings.TrimSpace(in.Quote.SavedAt)
	in.To = strings.TrimSpace(in.To)

	var problems []string
	if in.Quote.ID == "" {
		problems = append(problems, "quote.id: required")
	} else if len(in.Quote.ID) > 100 {
		problems = append(problems, "quote.id: max 100 characters")
	}
	if in.Quote.Label == "" {
		problems = append(problems, "quote.label: required")
	} else if len(in.Quote.Label) > 500 {
		problems = append(problems, "quote.label: max 500 characters")
	}
	if len(in.Quote.ClientName) > 200 {
		problems = append(problems, "quote.clientName: max 200 characters")
	}
	if len(in.Quote.Lines) > maxQuoteSendLines {
		problems = append(problems, fmt.Sprintf("quote.lines: max %d lines", maxQuoteSendLines))
	}
	if len(in.Quote.Lines) == 0 {
		if in.Quote.Quantity <= 0 || in.Quote.Quantity > 100000 {
			problems = append(problems, "quote.quantity: must be > 0 and <= 100000")
		}
		if in.Quote.UnitPriceCents < 0 || in.Quote.UnitPriceCents > 100_000_000 {
			problems = append(problems, "quote.unitPriceCents: must be 0-100000000")
		}
	}
	for i := range in.Quote.Lines {
		l := &in.Quote.Lines[i]
		l.Label = strings.TrimSpace(l.Label)
		if l.Label == "" {
			problems = append(problems, fmt.Sprintf("quote.lines[%d].label: required", i))
		} else if len(l.Label) > 500 {
			problems = append(problems, fmt.Sprintf("quote.lines[%d].label: max 500 characters", i))
		}
		if l.Quantity <= 0 || l.Quantity > 100000 {
			problems = append(problems, fmt.Sprintf("quote.lines[%d].quantity: must be > 0 and <= 100000", i))
		}
		if l.UnitPriceCents < 0 || l.UnitPriceCents > 100_000_000 {
			problems = append(problems, fmt.Sprintf("quote.lines[%d].unitPriceCents: must be 0-100000000", i))
		}
	}
	if in.Quote.ShippingCents < 0 || in.Quote.ShippingCents > 100_000_000 {
		problems = append(problems, "quote.shippingCents: must be 0-100000000")
	}
	if in.To == "" {
		problems = append(problems, "to: required")
	} else if len(in.To) > 200 {
		problems = append(problems, "to: max 200 characters")
	} else if !strings.Contains(in.To, "@") {
		problems = append(problems, "to: enter a valid email address")
	}
	if in.Quote.SavedAt == "" {
		problems = append(problems, "quote.savedAt: required")
	} else if _, err := time.Parse(time.RFC3339, in.Quote.SavedAt); err != nil {
		problems = append(problems, "quote.savedAt: must be RFC3339")
	}
	return problems
}

// quoteSendResponse is the success body; sentAt is the server clock, so the caller's statusLog entry matches.
type quoteSendResponse struct {
	Ok     bool   `json:"ok"`
	SentAt string `json:"sentAt"`
}

// handleQuoteSend implements POST /api/quotes/send: 409 if SMTP isn't configured, 422 for validation problems
// (including a `to` that isn't a known client), 502 with the SMTP error on send failure.
// Nothing is written to the database; db is only read (clientEmailExists).
func handleQuoteSend(db *sql.DB, store *ConfigStore, dial smtpDialer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := store.Get() // one snapshot for this request

		in, ok := decodeJSONBody[quoteSendRequest](w, r, quoteSendMaxBytes)
		if !ok {
			return
		}

		if problems := validateQuoteSendRequest(&in); len(problems) > 0 {
			writeAPIError(w, http.StatusUnprocessableEntity, problems...)
			return
		}

		if cfg.Smtp.Host == "" {
			writeAPIError(w, http.StatusConflict, "email isn't configured; set up SMTP in Settings")
			return
		}

		// Recipient restriction: a quote has no client record to fall back on, so `to` must match a client on
		// file. This stops the endpoint emailing arbitrary third parties through the shop's SMTP relay.
		known, err := clientEmailExists(db, in.To)
		if err != nil {
			log.Printf("quotes send: recipient lookup: %v", err)
			http.Error(w, "quotes send unavailable", http.StatusInternalServerError)
			return
		}
		if !known {
			writeAPIError(w, http.StatusUnprocessableEntity, "recipient is not a client on file")
			return
		}

		// lineAmountCents sums the item rows (each rounded half-up on its own, like the rows the recipient
		// sees).
		lineAmountCents := 0
		if len(in.Quote.Lines) > 0 {
			for _, l := range in.Quote.Lines {
				lineAmountCents += roundHalfUp(l.Quantity * float64(l.UnitPriceCents))
			}
		} else {
			lineAmountCents = roundHalfUp(in.Quote.Quantity * float64(in.Quote.UnitPriceCents))
		}
		totalCents := lineAmountCents + in.Quote.ShippingCents
		// savedAt was validated as RFC3339 above, so the error is unreachable.
		savedAt, _ := time.Parse(time.RFC3339, in.Quote.SavedAt)
		validUntil := savedAt.UTC().AddDate(0, 0, cfg.Quote.ValidDays)

		subject, textBody, htmlBody := renderQuoteEmail(cfg, in.Quote, lineAmountCents, totalCents, validUntil)
		if err := sendMailWith(cfg, dial, in.To, subject, textBody, htmlBody); err != nil {
			log.Printf("quotes send: %v", err)
			writeAPIError(w, http.StatusBadGateway, err.Error())
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(quoteSendResponse{
			Ok:     true,
			SentAt: time.Now().UTC().Format(time.RFC3339),
		})
	}
}
