package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"
)

// pricing_order.go adds multi-part orders on pricing.go: 1 to maxOrderParts parts, each a QuoteInputs
// sans pricing. Math is computePricingLines, shared with ComputePricing, so one part equals a single quote.

const (
	maxOrderParts     = 20
	maxPartLabelChars = 120
)

// errTooManyParts lets handlers answer 422 (well-formed but out of policy) instead of 400.
var errTooManyParts = errors.New("too many parts")

// OrderPricingInputs is PricingInputs minus salePriceOverride, which is per part.
type OrderPricingInputs struct {
	Strategy         string              `json:"strategy"`
	MarginPct        OptF                `json:"marginPct,omitzero"`
	MarkupMultiplier OptF                `json:"markupMultiplier,omitzero"`
	FeeSchedule      *PricingFeeSchedule `json:"feeSchedule,omitempty"`
	FeeScheduleName  *string             `json:"feeScheduleName,omitempty"`
	TaxRatePct       F                   `json:"taxRatePct"`
	ShippingCharge   F                   `json:"shippingCharge"`
	ShippingCost     F                   `json:"shippingCost"`
}

func (o OrderPricingInputs) pricingInputs() PricingInputs {
	return PricingInputs{
		Strategy: o.Strategy, MarginPct: o.MarginPct, MarkupMultiplier: o.MarkupMultiplier,
		FeeSchedule: o.FeeSchedule, FeeScheduleName: o.FeeScheduleName,
		TaxRatePct: o.TaxRatePct, ShippingCharge: o.ShippingCharge, ShippingCost: o.ShippingCost,
	}
}

// OrderPartInputs is one part: a flattened QuoteInputs plus label and per-part salePriceOverride.
// A part must not carry its own "pricing" block; decodeOrderInputs rejects one.
type OrderPartInputs struct {
	Label             string `json:"label"`
	SalePriceOverride OptF   `json:"salePriceOverride,omitzero"`
	QuoteInputs
}

// OrderInputs is the multi-part inputs bag; without Pricing the order is costed only.
type OrderInputs struct {
	Parts   []OrderPartInputs   `json:"parts"`
	Pricing *OrderPricingInputs `json:"pricing,omitempty"`
}

// OrderPart is one part's computed result.
type OrderPart struct {
	Label            string        `json:"label"`
	Breakdown        CostBreakdown `json:"breakdown"`
	RecommendedPrice int64         `json:"recommendedPrice"`
	ResolvedPrice    int64         `json:"resolvedPrice"`
	ItemRevenue      int64         `json:"itemRevenue"`
}

// OrderBreakdown is ComputeOrder's result.
// Warnings collects every part's cost warnings prefixed "parts[i].".
type OrderBreakdown struct {
	Parts      []OrderPart `json:"parts"`
	BatchTotal int64       `json:"batchTotal"`
	// ItemRevenue is Σ resolved×qty over the parts (0 without pricing).
	ItemRevenue int64          `json:"itemRevenue"`
	Pricing     *PricingResult `json:"pricing,omitempty"`
	Warnings    []FieldWarning `json:"warnings"`
	Valid       bool           `json:"valid"`
}

// ComputeOrder costs each part with ComputeQuote, then prices via computePricingLines.
func ComputeOrder(in OrderInputs, opts EngineOptions) OrderBreakdown {
	out := OrderBreakdown{Parts: make([]OrderPart, len(in.Parts)), Warnings: []FieldWarning{}}
	lines := make([]pricingLine, len(in.Parts))
	allValid := true
	for i, part := range in.Parts {
		qi := part.QuoteInputs
		qi.Pricing = nil
		bd := ComputeQuote(qi, opts)
		out.Parts[i] = OrderPart{Label: part.Label, Breakdown: bd}
		out.BatchTotal = addCents(out.BatchTotal, bd.BatchTotal)
		for _, w := range bd.Warnings {
			w.Field = fmt.Sprintf("parts[%d].%s", i, w.Field)
			out.Warnings = append(out.Warnings, w)
		}
		if !bd.Valid {
			allValid = false
		}
		lines[i] = newPricingLine(bd, part.SalePriceOverride)
	}
	out.Valid = allValid
	if in.Pricing != nil {
		result, per := computePricingLines(in.Pricing.pricingInputs(), lines, "parts")
		for i := range per {
			out.Parts[i].RecommendedPrice = per[i].recommended
			out.Parts[i].ResolvedPrice = per[i].resolved
			out.Parts[i].ItemRevenue = per[i].itemRevenue
		}
		for i := range per {
			out.ItemRevenue = addCents(out.ItemRevenue, per[i].itemRevenue)
		}
		out.Pricing = &result
		out.Valid = result.Valid
	}
	return out
}

// decodeQuoteInputs strictly decodes the quotes.inputs bag: {parts, pricing}, 1 to maxOrderParts parts.
func decodeQuoteInputs(raw json.RawMessage) (*OrderInputs, error) {
	var oi OrderInputs
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&oi); err != nil {
		return nil, err
	}
	if len(oi.Parts) == 0 {
		return nil, errors.New("parts: at least 1 part required")
	}
	if len(oi.Parts) > maxOrderParts {
		return nil, fmt.Errorf("parts: at most %d parts (got %d): %w", maxOrderParts, len(oi.Parts), errTooManyParts)
	}
	for i, p := range oi.Parts {
		if p.Pricing != nil {
			return nil, fmt.Errorf("parts[%d].pricing: not allowed; pricing is order-level", i)
		}
		if utf8.RuneCountInString(p.Label) > maxPartLabelChars {
			return nil, fmt.Errorf("parts[%d].label: max %d characters", i, maxPartLabelChars)
		}
	}
	return &oi, nil
}

// sanitizeOrderForJSON applies sanitizeBreakdownForJSON to every part.
func sanitizeOrderForJSON(ob *OrderBreakdown) {
	for i := range ob.Parts {
		sanitizeBreakdownForJSON(&ob.Parts[i].Breakdown)
	}
}
