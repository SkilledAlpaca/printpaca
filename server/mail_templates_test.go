package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These assert key fields are present in the rendered emails rather than pinning the HTML byte-for-byte, so markup can evolve.

func testDocConfig() *Config {
	return &Config{
		Currency: Currency{Symbol: "$", Decimals: 2},
		Quote: QuoteBranding{
			BusinessName:        "Acme Prints",
			LogoURL:             "https://example.com/logo.png",
			PaymentInstructions: "Pay via bank transfer to acct 12345",
			TermsText:           "All sales final",
			ValidDays:           14,
		},
	}
}

func TestRenderInvoiceEmailContainsKeyFields(t *testing.T) {
	cfg := testDocConfig()
	inv := &Invoice{
		InvoiceSummary: InvoiceSummary{
			Number:        "INV-2026-0001",
			ClientName:    "Beta Print <script>",
			DueDate:       "2026-02-01",
			Notes:         "Thanks for your business",
			SubtotalCents: 10000,
			TaxCents:      825,
			TotalCents:    10825,
		},
		Lines: []InvoiceLine{
			{Description: "Widget", Quantity: 2, UnitPriceCents: 5000, AmountCents: 10000},
		},
	}

	subject, textBody, htmlBody := renderInvoiceEmail(cfg, inv)

	if !strings.Contains(subject, "INV-2026-0001") || !strings.Contains(subject, "Acme Prints") {
		t.Errorf("subject = %q, want it to mention the invoice number and business name", subject)
	}

	for _, want := range []string{
		"Acme Prints",              // seller identity
		"Beta Print",               // client block
		"Widget",                   // line item description
		"$100.00",                  // line amount (10000c)
		"$8.25",                    // tax
		"$108.25",                  // total
		"Feb 1, 2026",              // due date
		"Pay via bank transfer",    // payment instructions
		"All sales final",          // terms
		"Thanks for your business", // notes
	} {
		if !strings.Contains(htmlBody, want) {
			t.Errorf("html body missing %q", want)
		}
		if !strings.Contains(textBody, want) {
			t.Errorf("text body missing %q", want)
		}
	}

	// html/template must have escaped the client name: no raw <script> tag.
	if strings.Contains(htmlBody, "<script>") {
		t.Error("html body contains an unescaped <script> tag (client name injection)")
	}
	if !strings.Contains(htmlBody, "&lt;script&gt;") {
		t.Error("html body does not show the client name's <script> escaped")
	}

	// A zero-tax invoice must omit the Tax row entirely (HasTax gate).
	inv.TaxCents = 0
	_, _, htmlNoTax := renderInvoiceEmail(cfg, inv)
	if strings.Contains(htmlNoTax, ">Tax<") {
		t.Error("html body shows a Tax row for a zero-tax invoice")
	}

	// A zero-tax invoice keeps both the Subtotal and Total rows: invoice
	// emails don't collapse them even when the two figures are equal, unlike
	// quotes (renderQuoteEmail leaves CollapseTotal at its zero value here).
	if !strings.Contains(htmlNoTax, ">Subtotal<") {
		t.Error("html body dropped the Subtotal row for a zero-tax invoice; invoice emails must keep both rows")
	}
}

func TestRenderQuoteEmailContainsKeyFields(t *testing.T) {
	cfg := testDocConfig()
	q := quoteSendInput{
		ID:             "quote-1",
		Label:          "PLA Widget x10",
		ClientName:     "Gamma Corp",
		Quantity:       10,
		UnitPriceCents: 500,
		ShippingCents:  1200,
	}
	validUntil := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	subject, textBody, htmlBody := renderQuoteEmail(cfg, q, 5000, 6200, validUntil)

	if !strings.Contains(subject, "Acme Prints") {
		t.Errorf("subject = %q, want it to mention the business name", subject)
	}

	for _, want := range []string{
		"Acme Prints",
		"Gamma Corp",
		"3D printed part",
		"Shipping",
		"$50.00", // line amount
		"$12.00", // shipping
		"$62.00", // total
		"Mar 1, 2026",
		"Pay via bank transfer",
		"All sales final",
	} {
		if !strings.Contains(htmlBody, want) {
			t.Errorf("html body missing %q", want)
		}
		if !strings.Contains(textBody, want) {
			t.Errorf("text body missing %q", want)
		}
	}

	// No shipping: the Shipping line item must not appear at all.
	q.ShippingCents = 0
	_, _, htmlNoShip := renderQuoteEmail(cfg, q, 5000, 5000, validUntil)
	if strings.Contains(htmlNoShip, "Shipping") {
		t.Error("html body shows a Shipping line with ShippingCents == 0")
	}

	// A quote's Subtotal and Total are always the same figure with no tax
	// line, so the email must render a single Total row, not the redundant
	// Subtotal+Total pair.
	if strings.Contains(htmlBody, ">Subtotal<") {
		t.Error("html body shows a redundant Subtotal row for a quote (Subtotal == Total)")
	}
	if strings.Contains(textBody, "Subtotal:") {
		t.Error("text body shows a redundant Subtotal line for a quote (Subtotal == Total)")
	}
	if !strings.Contains(htmlBody, ">Total<") || !strings.Contains(textBody, "Total:") {
		t.Error("body is missing the collapsed Total row/line")
	}
}

func TestRenderQuoteEmailTaxNote(t *testing.T) {
	cfg := testDocConfig()
	q := quoteSendInput{
		ID:             "quote-1",
		Label:          "PLA Widget x10",
		ClientName:     "Gamma Corp",
		Quantity:       10,
		UnitPriceCents: 500,
	}
	validUntil := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	// Absent: neither note renders (backward compatible default).
	_, textAbsent, htmlAbsent := renderQuoteEmail(cfg, q, 5000, 5000, validUntil)
	for _, unwanted := range []string{"Prices include tax.", "Prices exclude sales tax"} {
		if strings.Contains(htmlAbsent, unwanted) || strings.Contains(textAbsent, unwanted) {
			t.Errorf("body contains %q with TaxIncluded absent, want neither note", unwanted)
		}
	}

	included := true
	q.TaxIncluded = &included
	_, textIncluded, htmlIncluded := renderQuoteEmail(cfg, q, 5000, 5000, validUntil)
	if !strings.Contains(htmlIncluded, "Prices include tax.") || !strings.Contains(textIncluded, "Prices include tax.") {
		t.Error("body missing the tax-included note when TaxIncluded=true")
	}

	excluded := false
	q.TaxIncluded = &excluded
	_, textExcluded, htmlExcluded := renderQuoteEmail(cfg, q, 5000, 5000, validUntil)
	want := "Prices exclude sales tax, which is added at invoicing."
	if !strings.Contains(htmlExcluded, want) || !strings.Contains(textExcluded, want) {
		t.Error("body missing the tax-excluded note when TaxIncluded=false")
	}
}

func TestFormatCurrencyNegative(t *testing.T) {
	cfg := &Config{Currency: Currency{Symbol: "$", Decimals: 2}}
	got := formatCurrency(cfg, -500)
	if got != "-$5.00" {
		t.Errorf("formatCurrency(-500) = %q, want %q", got, "-$5.00")
	}
}

func TestFormatCurrencyGroupsThousands(t *testing.T) {
	cfg := &Config{Currency: Currency{Symbol: "$", Decimals: 2}}
	cases := map[int]string{
		999:        "$9.99",
		100000:     "$1,000.00",
		123456789:  "$1,234,567.89",
		-123456700: "-$1,234,567.00",
	}
	for cents, want := range cases {
		if got := formatCurrency(cfg, cents); got != want {
			t.Errorf("formatCurrency(%d) = %q, want %q", cents, got, want)
		}
	}
}

// formatCurrency must read cfg.Currency.Decimals instead of hard-coding two digits (decimals 0 or 3/4), as the web UI and print view do through formatMoney.
func TestFormatCurrencyHonorsDecimals(t *testing.T) {
	cases := []struct {
		cents    int
		decimals int
		symbol   string
		want     string
	}{
		{1234, 0, "¥", "¥12"},
		{123456700, 0, "$", "$1,234,567"},
		{5, 4, "€", "€0.0500"},
		{-1, 4, "€", "-€0.0100"},
	}
	for _, c := range cases {
		cfg := &Config{Currency: Currency{Symbol: c.symbol, Decimals: c.decimals}}
		if got := formatCurrency(cfg, c.cents); got != c.want {
			t.Errorf("formatCurrency(%d, decimals=%d) = %q, want %q", c.cents, c.decimals, got, c.want)
		}
	}
}

// moneyFormatFixture is one row of testdata/money-format.json, exercised identically by this test and the web engine's money tests,
// so formatCurrency and formatMoney can't drift apart.
type moneyFormatFixture struct {
	Cents    int    `json:"cents"`
	Decimals int    `json:"decimals"`
	Symbol   string `json:"symbol"`
	Expected string `json:"expected"`
}

func loadMoneyFormatFixtures(t *testing.T) []moneyFormatFixture {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "testdata", "money-format.json"))
	if err != nil {
		t.Fatalf("reading shared money-format fixture: %v", err)
	}
	var cases []moneyFormatFixture
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatalf("parsing shared money-format fixture: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("shared money-format fixture is empty")
	}
	return cases
}

func TestFormatCurrencyMatchesSharedFixture(t *testing.T) {
	for _, c := range loadMoneyFormatFixtures(t) {
		cfg := &Config{Currency: Currency{Symbol: c.Symbol, Decimals: c.Decimals}}
		if got := formatCurrency(cfg, c.Cents); got != c.Expected {
			t.Errorf("formatCurrency(cents=%d, decimals=%d, symbol=%q) = %q, want %q (shared fixture)",
				c.Cents, c.Decimals, c.Symbol, got, c.Expected)
		}
	}
}

// The comma grouping applied by formatCurrency must reach both the HTML and text bodies for a line at or above $1,000.
func TestRenderInvoiceEmailThousandsSeparator(t *testing.T) {
	cfg := testDocConfig()
	inv := &Invoice{
		InvoiceSummary: InvoiceSummary{
			Number:        "INV-2026-0002",
			ClientName:    "Delta Print",
			DueDate:       "2026-02-01",
			SubtotalCents: 123456700,
			TaxCents:      0,
			TotalCents:    123456700,
		},
		Lines: []InvoiceLine{
			{Description: "Bulk order", Quantity: 1, UnitPriceCents: 123456700, AmountCents: 123456700},
		},
	}

	_, textBody, htmlBody := renderInvoiceEmail(cfg, inv)

	want := "$1,234,567.00"
	if !strings.Contains(htmlBody, want) {
		t.Errorf("html body missing thousands-separated amount %q", want)
	}
	if !strings.Contains(textBody, want) {
		t.Errorf("text body missing thousands-separated amount %q", want)
	}
}

func TestFormatQuantityDropsTrailingZeros(t *testing.T) {
	cases := map[float64]string{3: "3", 0.5: "0.5", 1.25: "1.25"}
	for q, want := range cases {
		if got := formatQuantity(q); got != want {
			t.Errorf("formatQuantity(%v) = %q, want %q", q, got, want)
		}
	}
}

func TestRenderInvoiceEmailPayPalLink(t *testing.T) {
	newInv := func(status string, balance int) *Invoice {
		return &Invoice{InvoiceSummary: InvoiceSummary{
			Number: "INV-2026-0002", ClientName: "Beta Print", DueDate: "2026-02-01",
			Status: status, TotalCents: 4250, BalanceCents: balance,
			SubtotalCents: 4250,
		}}
	}

	t.Run("with currency code the link carries the amount in both parts", func(t *testing.T) {
		cfg := testDocConfig()
		cfg.Invoicing.PayPalMe = "yourshop"
		cfg.Currency.Code = "USD"
		_, text, html := renderInvoiceEmail(cfg, newInv("sent", 4250))
		const link = "https://paypal.me/yourshop/42.50USD"
		for name, body := range map[string]string{"text": text, "html": html} {
			if !strings.Contains(body, link) {
				t.Errorf("%s part missing %q", name, link)
			}
		}
	})

	t.Run("without currency code the link has no amount and the text names it", func(t *testing.T) {
		cfg := testDocConfig()
		cfg.Invoicing.PayPalMe = "yourshop"
		_, text, html := renderInvoiceEmail(cfg, newInv("sent", 4250))
		for name, body := range map[string]string{"text": text, "html": html} {
			if !strings.Contains(body, "https://paypal.me/yourshop") {
				t.Errorf("%s part missing the bare link", name)
			}
			if !strings.Contains(body, "Send $42.50") && !strings.Contains(body, "send $42.50") {
				t.Errorf("%s part does not tell the payer to send $42.50", name)
			}
		}
	})

	for _, c := range []struct {
		name    string
		handle  string
		status  string
		balance int
	}{
		{"no handle", "", "sent", 4250},
		{"canceled", "yourshop", "canceled", 4250},
		{"paid", "yourshop", "paid", 0},
	} {
		t.Run("no block: "+c.name, func(t *testing.T) {
			cfg := testDocConfig()
			cfg.Invoicing.PayPalMe = c.handle
			_, text, html := renderInvoiceEmail(cfg, newInv(c.status, c.balance))
			if strings.Contains(text, "paypal.me") || strings.Contains(html, "paypal.me") || strings.Contains(html, "Pay with PayPal") {
				t.Error("pay block rendered, want none")
			}
		})
	}
}

// The quote's own label is the owner's internal note and is never emailed: a flat quote shows the neutral
// description, a multi-part quote shows each part label, and a blank part label falls back to the same text.
func TestRenderQuoteEmailLabelRule(t *testing.T) {
	cfg := testDocConfig()
	validUntil := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	flat := quoteSendInput{Label: "INTERNAL NOTE", Quantity: 1, UnitPriceCents: 500}
	_, _, html := renderQuoteEmail(cfg, flat, 500, 500, validUntil)
	if strings.Contains(html, "INTERNAL NOTE") || !strings.Contains(html, "3D printed part") {
		t.Error("flat quote must show the neutral description, not the quote label")
	}

	multi := quoteSendInput{Label: "INTERNAL NOTE", Lines: []quoteSendLine{
		{Label: "Bracket", Quantity: 1, UnitPriceCents: 500},
		{Label: "  ", Quantity: 2, UnitPriceCents: 300},
	}}
	_, _, html = renderQuoteEmail(cfg, multi, 1100, 1100, validUntil)
	if strings.Contains(html, "INTERNAL NOTE") || !strings.Contains(html, "Bracket") || !strings.Contains(html, "3D printed part") {
		t.Error("multi-part quote must show part labels, with the neutral text for a blank one")
	}
}
