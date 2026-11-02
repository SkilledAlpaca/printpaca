package main

import (
	"bytes"
	"cmp"
	"fmt"
	"html/template"
	"math"
	"strconv"
	"strings"
	"time"
)

// Outbound document email bodies: server-composed HTML with inline styles and a table layout. No PDF
// pipeline (heavy dependency, no buyer-visible gain over this plus the print view). Invoice and quote
// emails share one template so the two document types can't drift.

// mailLineItem is one pre-formatted line-items row; html/template has no numeric formatting.
type mailLineItem struct {
	Description string
	Quantity    string
	UnitPrice   string
	Amount      string
}

// mailDocument is the template's whole input: pre-formatted strings, so it holds no formatting logic.
type mailDocument struct {
	Kind         string // "Invoice" or "Quote"
	Number       string // invoice number, or the quote's reference (never its internal id); empty omits it
	PartyLabel   string // "Billed to" for invoices, "Prepared for" for quotes
	BusinessName string
	LogoURL      string
	ClientName   string
	Lines        []mailLineItem
	Subtotal     string
	Tax          string
	HasTax       bool
	Total        string
	// CollapseTotal renders one "Total" row when there's no tax and Subtotal == Total. Quotes set it.
	CollapseTotal       bool
	DateLabel           string // "Due date" or "Valid until"
	DateValue           string
	PaymentInstructions string
	TermsText           string
	Notes               string
	// TaxNote says whether the price includes tax; empty renders nothing.
	TaxNote string
	// PayPalLink is the PayPal.me link for the open balance, or "" for no pay block (see paypalPayLink).
	// BalanceDue is the formatted open balance, shown when the link carries no amount. Invoices only.
	PayPalLink string
	BalanceDue string
	// PayPalHasAmount is true when PayPalLink already carries the amount.
	PayPalHasAmount bool
}

// groupThousands adds commas every three digits ("1234567" -> "1,234,567"); en-US only, like formatMoney.
func groupThousands(digits string) string {
	n := len(digits)
	if n <= 3 {
		return digits
	}
	var b strings.Builder
	lead := n % 3
	if lead == 0 {
		lead = 3
	}
	b.WriteString(digits[:lead])
	for i := lead; i < n; i += 3 {
		b.WriteByte(',')
		b.WriteString(digits[i : i+3])
	}
	return b.String()
}

// formatCurrency renders cents as "<sign><symbol><value>" for every email, including the owner
// notification. It must match the engine's formatMoney byte-for-byte: no space after the symbol,
// cfg.Currency.Decimals digits, whole part comma-grouped at 4+ digits, and half-up rounding
// (math.Floor(x+0.5)) rather than FormatFloat's half-to-even. Both run testdata/money-format.json.
func formatCurrency(cfg *Config, cents int) string {
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	decimals := max(cfg.Currency.Decimals, 0)
	scale := math.Pow(10, float64(decimals))
	dollars := float64(cents) / 100
	rounded := math.Floor(dollars*scale+0.5) / scale
	formatted := strconv.FormatFloat(rounded, 'f', decimals, 64)
	whole, frac, hasFrac := strings.Cut(formatted, ".")
	value := groupThousands(whole)
	if hasFrac {
		value += "." + frac
	}
	return sign + cfg.Currency.Symbol + value
}

// formatQuantity drops the decimal point for whole quantities ("3", not "3.000000").
func formatQuantity(q float64) string {
	return strconv.FormatFloat(q, 'f', -1, 64)
}

// documentEmailTmpl is compiled once; html/template auto-escapes client-supplied strings.
var documentEmailTmpl = template.Must(template.New("documentEmail").Parse(`<!doctype html>
<html lang="en-US">
<body style="margin:0;padding:24px;background:#f4f4f5;font-family:Arial,Helvetica,sans-serif;color:#18181b;">
  <table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:640px;margin:0 auto;background:#ffffff;border:1px solid #e4e4e7;border-radius:8px;overflow:hidden;">
    <tr>
      <td style="padding:24px 32px;border-bottom:1px solid #e4e4e7;">
        {{if .LogoURL}}<img src="{{.LogoURL}}" alt="{{.BusinessName}}" style="max-height:48px;display:block;margin-bottom:12px;">{{end}}
        <div style="font-size:20px;font-weight:bold;">{{.BusinessName}}</div>
        <div style="font-size:14px;color:#71717a;margin-top:4px;">{{.Kind}} {{.Number}}</div>
      </td>
    </tr>
    <tr>
      <td style="padding:24px 32px;">
        <table role="presentation" width="100%" cellpadding="0" cellspacing="0">
          <tr>
            <td style="vertical-align:top;">
              <div style="font-size:12px;text-transform:uppercase;color:#71717a;margin-bottom:4px;">{{.PartyLabel}}</div>
              <div style="font-size:14px;">{{.ClientName}}</div>
            </td>
            <td style="vertical-align:top;text-align:right;">
              <div style="font-size:12px;text-transform:uppercase;color:#71717a;margin-bottom:4px;">{{.DateLabel}}</div>
              <div style="font-size:14px;">{{.DateValue}}</div>
            </td>
          </tr>
        </table>
      </td>
    </tr>
    <tr>
      <td style="padding:0 32px;">
        <table role="presentation" width="100%" cellpadding="8" cellspacing="0" style="border-collapse:collapse;font-size:14px;">
          <tr style="border-bottom:2px solid #e4e4e7;">
            <th align="left" style="padding:8px;">Description</th>
            <th align="right" style="padding:8px;">Qty</th>
            <th align="right" style="padding:8px;">Unit price</th>
            <th align="right" style="padding:8px;">Amount</th>
          </tr>
          {{range .Lines}}
          <tr style="border-bottom:1px solid #f4f4f5;">
            <td style="padding:8px;">{{.Description}}</td>
            <td align="right" style="padding:8px;">{{.Quantity}}</td>
            <td align="right" style="padding:8px;">{{.UnitPrice}}</td>
            <td align="right" style="padding:8px;">{{.Amount}}</td>
          </tr>
          {{end}}
        </table>
      </td>
    </tr>
    <tr>
      <td style="padding:16px 32px;">
        <table role="presentation" width="100%" cellpadding="4" cellspacing="0" style="font-size:14px;">
          {{if and .CollapseTotal (not .HasTax) (eq .Subtotal .Total)}}
          <tr>
            <td></td>
            <td align="right" style="font-weight:bold;padding-top:8px;border-top:1px solid #e4e4e7;">Total</td>
            <td align="right" width="120" style="font-weight:bold;padding-top:8px;border-top:1px solid #e4e4e7;">{{.Total}}</td>
          </tr>
          {{else}}
          <tr>
            <td></td>
            <td align="right" style="color:#71717a;">Subtotal</td>
            <td align="right" width="120">{{.Subtotal}}</td>
          </tr>
          {{if .HasTax}}
          <tr>
            <td></td>
            <td align="right" style="color:#71717a;">Tax</td>
            <td align="right" width="120">{{.Tax}}</td>
          </tr>
          {{end}}
          <tr>
            <td></td>
            <td align="right" style="font-weight:bold;padding-top:8px;border-top:1px solid #e4e4e7;">Total</td>
            <td align="right" width="120" style="font-weight:bold;padding-top:8px;border-top:1px solid #e4e4e7;">{{.Total}}</td>
          </tr>
          {{end}}
        </table>
      </td>
    </tr>
    {{if .TaxNote}}
    <tr>
      <td style="padding:0 32px 16px;font-size:13px;color:#3f3f46;">{{.TaxNote}}</td>
    </tr>
    {{end}}
    {{if .Notes}}
    <tr>
      <td style="padding:0 32px 16px;font-size:13px;color:#3f3f46;white-space:pre-wrap;">{{.Notes}}</td>
    </tr>
    {{end}}
    {{if .PayPalLink}}
    <tr>
      <td style="padding:0 32px 16px;">
        <div style="font-size:12px;text-transform:uppercase;color:#71717a;margin-bottom:4px;">Pay with PayPal</div>
        <div style="font-size:13px;">{{if not .PayPalHasAmount}}Send {{.BalanceDue}} to <a href="{{.PayPalLink}}">{{.PayPalLink}}</a>{{else}}<a href="{{.PayPalLink}}">{{.PayPalLink}}</a> ({{.BalanceDue}} due){{end}}</div>
      </td>
    </tr>
    {{end}}
    {{if .PaymentInstructions}}
    <tr>
      <td style="padding:0 32px 16px;">
        <div style="font-size:12px;text-transform:uppercase;color:#71717a;margin-bottom:4px;">Payment instructions</div>
        <div style="font-size:13px;white-space:pre-wrap;">{{.PaymentInstructions}}</div>
      </td>
    </tr>
    {{end}}
    {{if .TermsText}}
    <tr>
      <td style="padding:0 32px 24px;font-size:12px;color:#a1a1aa;white-space:pre-wrap;">{{.TermsText}}</td>
    </tr>
    {{end}}
  </table>
</body>
</html>
`))

// renderDocumentText renders the plain-text part from the same mailDocument as the HTML, so they match.
func renderDocumentText(doc mailDocument) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n%s\n\n", doc.BusinessName, strings.TrimSpace(doc.Kind+" "+doc.Number))
	fmt.Fprintf(&b, "%s: %s\n", doc.PartyLabel, doc.ClientName)
	fmt.Fprintf(&b, "%s: %s\n\n", doc.DateLabel, doc.DateValue)
	for _, l := range doc.Lines {
		fmt.Fprintf(&b, "%s  x%s  %s each  %s\n", l.Description, l.Quantity, l.UnitPrice, l.Amount)
	}
	collapseTotal := doc.CollapseTotal && !doc.HasTax && doc.Subtotal == doc.Total
	if !collapseTotal {
		fmt.Fprintf(&b, "\nSubtotal: %s\n", doc.Subtotal)
		if doc.HasTax {
			fmt.Fprintf(&b, "Tax: %s\n", doc.Tax)
		}
		fmt.Fprintf(&b, "Total: %s\n", doc.Total)
	} else {
		fmt.Fprintf(&b, "\nTotal: %s\n", doc.Total)
	}
	if doc.TaxNote != "" {
		fmt.Fprintf(&b, "%s\n", doc.TaxNote)
	}
	if doc.Notes != "" {
		fmt.Fprintf(&b, "\n%s\n", doc.Notes)
	}
	if doc.PayPalLink != "" {
		if doc.PayPalHasAmount {
			fmt.Fprintf(&b, "\nPay with PayPal (%s due):\n%s\n", doc.BalanceDue, doc.PayPalLink)
		} else {
			fmt.Fprintf(&b, "\nPay with PayPal: send %s to\n%s\n", doc.BalanceDue, doc.PayPalLink)
		}
	}
	if doc.PaymentInstructions != "" {
		fmt.Fprintf(&b, "\nPayment instructions:\n%s\n", doc.PaymentInstructions)
	}
	if doc.TermsText != "" {
		fmt.Fprintf(&b, "\n%s\n", doc.TermsText)
	}
	return b.String()
}

// renderDocumentEmail executes the static template. Execute can only fail on a build-time field
// type mismatch, so it panics rather than returning an error.
func renderDocumentEmail(doc mailDocument) string {
	var buf bytes.Buffer
	if err := documentEmailTmpl.Execute(&buf, doc); err != nil {
		panic(fmt.Sprintf("renderDocumentEmail: %v", err))
	}
	return buf.String()
}

// renderInvoiceEmail builds subject/text/html for handleSend. Seller identity and payment terms: cfg.Quote.
func renderInvoiceEmail(cfg *Config, inv *Invoice) (subject, textBody, htmlBody string) {
	doc := mailDocument{
		Kind:                "Invoice",
		PartyLabel:          "Billed to",
		Number:              inv.Number,
		BusinessName:        cfg.Quote.BusinessName,
		LogoURL:             cfg.Quote.LogoURL,
		ClientName:          inv.ClientName,
		Subtotal:            formatCurrency(cfg, inv.SubtotalCents),
		Tax:                 formatCurrency(cfg, inv.TaxCents),
		HasTax:              inv.TaxCents != 0,
		Total:               formatCurrency(cfg, inv.TotalCents),
		DateLabel:           "Due date",
		DateValue:           formatMailDate(inv.DueDate),
		PaymentInstructions: cfg.Quote.PaymentInstructions,
		TermsText:           cfg.Quote.TermsText,
		Notes:               inv.Notes,
	}
	if link := paypalPayLink(cfg.Invoicing.PayPalMe, cfg.Currency.Code, cfg.Currency.Decimals, inv.Status, inv.BalanceCents); link != "" {
		doc.PayPalLink = link
		doc.BalanceDue = formatCurrency(cfg, inv.BalanceCents)
		doc.PayPalHasAmount = cfg.Currency.Code != ""
	}
	for _, l := range inv.Lines {
		doc.Lines = append(doc.Lines, mailLineItem{
			Description: l.Description,
			Quantity:    formatQuantity(l.Quantity),
			UnitPrice:   formatCurrency(cfg, l.UnitPriceCents),
			Amount:      formatCurrency(cfg, l.AmountCents),
		})
	}

	subject = documentSubject("Invoice "+inv.Number, "Invoice", cfg.Quote.BusinessName)
	return subject, renderDocumentText(doc), renderDocumentEmail(doc)
}

// documentSubject is "<title> from <business>", or just fallback when the business name is empty.
func documentSubject(title, fallback, business string) string {
	if strings.TrimSpace(business) == "" {
		return strings.TrimSpace(title)
	}
	return fmt.Sprintf("%s from %s", title, business)
}

// formatMailDate renders a YYYY-MM-DD date as "Jan 2, 2006"; anything else is returned unchanged.
func formatMailDate(s string) string {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return s
	}
	return t.Format("Jan 2, 2006")
}

// renderQuoteEmail builds subject/text/html for handleQuoteSend; the caller computes validUntil (pure).
func renderQuoteEmail(cfg *Config, q quoteSendInput, lineAmountCents, totalCents int, validUntil time.Time) (subject, textBody, htmlBody string) {
	var taxNote string
	if q.TaxIncluded != nil {
		if *q.TaxIncluded {
			taxNote = "Prices include tax."
		} else {
			taxNote = "Prices exclude sales tax, which is added at invoicing."
		}
	}

	doc := mailDocument{
		Kind:                "Quote",
		PartyLabel:          "Prepared for",
		Number:              strings.TrimSpace(q.Reference),
		BusinessName:        cfg.Quote.BusinessName,
		LogoURL:             cfg.Quote.LogoURL,
		ClientName:          q.ClientName,
		Subtotal:            formatCurrency(cfg, totalCents),
		HasTax:              false,
		Total:               formatCurrency(cfg, totalCents),
		CollapseTotal:       true,
		TaxNote:             taxNote,
		DateLabel:           "Valid until",
		DateValue:           validUntil.Format("Jan 2, 2006"),
		PaymentInstructions: cfg.Quote.PaymentInstructions,
		TermsText:           cfg.Quote.TermsText,
		Lines: []mailLineItem{{
			// One rule for every row: the customer sees a part label, never the quote's own label (the owner's
			// internal note). A flat single-part quote has no part label, so it gets the neutral description.
			Description: defaultQuotePartLabel,
			Quantity:    formatQuantity(q.Quantity),
			UnitPrice:   formatCurrency(cfg, q.UnitPriceCents),
			Amount:      formatCurrency(cfg, lineAmountCents),
		}},
	}
	// A multi-part quote sends one row per part; lineAmountCents is then their sum.
	if len(q.Lines) > 0 {
		doc.Lines = make([]mailLineItem, 0, len(q.Lines)+1)
		for _, l := range q.Lines {
			doc.Lines = append(doc.Lines, mailLineItem{
				Description: cmp.Or(strings.TrimSpace(l.Label), defaultQuotePartLabel),
				Quantity:    formatQuantity(l.Quantity),
				UnitPrice:   formatCurrency(cfg, l.UnitPriceCents),
				Amount:      formatCurrency(cfg, roundHalfUp(l.Quantity*float64(l.UnitPriceCents))),
			})
		}
	}
	if q.ShippingCents > 0 {
		doc.Lines = append(doc.Lines, mailLineItem{
			Description: "Shipping",
			Quantity:    "1",
			UnitPrice:   formatCurrency(cfg, q.ShippingCents),
			Amount:      formatCurrency(cfg, q.ShippingCents),
		})
	}

	if ref := strings.TrimSpace(q.Reference); ref != "" {
		subject = documentSubject("Quote "+ref, "Your quote", cfg.Quote.BusinessName)
	} else {
		subject = documentSubject("Your quote", "Your quote", cfg.Quote.BusinessName)
	}
	return subject, renderDocumentText(doc), renderDocumentEmail(doc)
}

// defaultQuotePartLabel is the emailed row description when a quote line has no part label of its own.
const defaultQuotePartLabel = "3D printed part"
