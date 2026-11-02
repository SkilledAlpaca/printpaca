package main

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// pricing_order_test.go pins ComputeOrder. Load-bearing invariant: for every vector in testdata/quote-vectors.json a one-quote order
// must match ComputeQuote with pricing. The multi-part vectors are HAND-DERIVED (arithmetic per vector), never copied from the implementation.

// orderPart builds a zero-overhead part: only the material line costs
// anything, so perUnitTotal = round(weightG x pricePerKg / 1000 x 100) cents.
func orderPart(label string, weightG, pricePerKg, quantity float64) OrderPartInputs {
	return OrderPartInputs{
		Label: label,
		QuoteInputs: QuoteInputs{
			WeightG: F(weightG), PricePerKg: F(pricePerKg), Quantity: F(quantity),
			Energy: EnergyInputs{Mode: "flat", FlatRate: OptF{Value: 0, Present: true}},
		},
	}
}

func TestComputeOrderSinglePartMatchesComputeQuote(t *testing.T) {
	for _, v := range loadQuoteVectors(t) {
		t.Run(v.Name, func(t *testing.T) {
			opts := DefaultEngineOptions
			if v.Options != nil {
				opts = *v.Options
			}
			want := ComputeQuote(v.Inputs, opts)

			part := OrderPartInputs{QuoteInputs: v.Inputs}
			part.Pricing = nil
			in := OrderInputs{Parts: []OrderPartInputs{part}}
			if p := v.Inputs.Pricing; p != nil {
				part.SalePriceOverride = p.SalePriceOverride
				in.Parts[0] = part
				in.Pricing = &OrderPricingInputs{
					Strategy: p.Strategy, MarginPct: p.MarginPct, MarkupMultiplier: p.MarkupMultiplier,
					FeeSchedule: p.FeeSchedule, FeeScheduleName: p.FeeScheduleName,
					TaxRatePct: p.TaxRatePct, ShippingCharge: p.ShippingCharge, ShippingCost: p.ShippingCost,
				}
			}
			// The cost breakdown of the lone part is ComputeQuote without pricing.
			bare := v.Inputs
			bare.Pricing = nil
			assertCostBreakdown(t, v.Name, ComputeOrder(in, opts).Parts[0].Breakdown, ComputeQuote(bare, opts))

			got := ComputeOrder(in, opts)
			if len(got.Parts) != 1 {
				t.Fatalf("parts = %d, want 1", len(got.Parts))
			}
			if got.BatchTotal != want.BatchTotal {
				t.Errorf("batchTotal = %d, want %d", got.BatchTotal, want.BatchTotal)
			}
			if (got.Pricing == nil) != (want.Pricing == nil) {
				t.Fatalf("pricing presence: got nil=%v want nil=%v", got.Pricing == nil, want.Pricing == nil)
			}
			if want.Pricing == nil {
				if got.Valid != want.Valid {
					t.Errorf("valid = %v, want %v", got.Valid, want.Valid)
				}
				return
			}
			assertPricingResult(t, v.Name, *got.Pricing, *want.Pricing)
			if got.Valid != want.Pricing.Valid {
				t.Errorf("valid = %v, want %v", got.Valid, want.Pricing.Valid)
			}
			// Warnings: same fields and codes, per-part ones carrying the index.
			if len(got.Pricing.Warnings) != len(want.Pricing.Warnings) {
				t.Fatalf("warning count = %d, want %d", len(got.Pricing.Warnings), len(want.Pricing.Warnings))
			}
			for i, w := range want.Pricing.Warnings {
				g := got.Pricing.Warnings[i]
				field := w.Field
				if field == "salePriceOverride" || field == "perUnitTotal" {
					field = "parts[0]." + field
				}
				if g.Field != field || g.Code != w.Code {
					t.Errorf("warning[%d] = %s/%s, want %s/%s", i, g.Field, g.Code, field, w.Code)
				}
			}
			pr := got.Parts[0]
			if pr.RecommendedPrice != want.Pricing.RecommendedPrice || pr.ResolvedPrice != want.Pricing.ResolvedPrice {
				t.Errorf("part prices = %d/%d, want %d/%d", pr.RecommendedPrice, pr.ResolvedPrice, want.Pricing.RecommendedPrice, want.Pricing.ResolvedPrice)
			}
		})
	}
}

func TestComputeOrderMultiPartVectors(t *testing.T) {
	// Material-only parts (watts 0): A 100 g @ $20/kg = 200 c/unit; B 250 g @ $40/kg = 1000; C 50 g @ $30/kg = 150.
	a := func(q float64) OrderPartInputs { return orderPart("A", 100, 20, q) }
	b := func(q float64) OrderPartInputs { return orderPart("B", 250, 40, q) }
	c := func(q float64) OrderPartInputs { return orderPart("C", 50, 30, q) }
	over := func(p OrderPartInputs, dollars float64) OrderPartInputs {
		p.SalePriceOverride = OptF{Value: dollars, Present: true}
		return p
	}
	feeSchedule := &PricingFeeSchedule{
		Fixed:   []PricingFeeFixed{{Amount: 0.30, Label: "Listing"}},
		Percent: []PricingFeePercent{{Pct: 3, Base: "revenue", Label: "Payment"}},
	}

	t.Run("three parts, markup, override, fixed+percent fees, tax, shipping", func(t *testing.T) {
		// qty A=3, B=2, C=10; markup x2 grossed up by the 10% tax (x1.1): A 440, B 2200, C 330; C overridden to $0.50.
		// itemRevenue = 440x3 + 2200x2 + 50x10 = 6220; with $5 shipping revenue = 6720; batchTotal = 4100; shippingCost 300.
		// Fees once, not per part: 30 + round(6720 x 3%) = 232. Tax 10% inclusive on item revenue only: round(6220 x 10/110) = 565.
		// netProfit = 6720 - 4100 - 300 - 232 - 565 = 1523; order recommendedPrice 9020 differs from itemRevenue because C is overridden.
		in := OrderInputs{
			Parts: []OrderPartInputs{a(3), b(2), over(c(10), 0.50)},
			Pricing: &OrderPricingInputs{
				Strategy: "markup", MarkupMultiplier: OptF{Value: 2, Present: true},
				FeeSchedule: feeSchedule, TaxRatePct: 10, ShippingCharge: 5, ShippingCost: 3,
			},
		}
		got := ComputeOrder(in, DefaultEngineOptions)
		checkOrder(t, got, orderWant{
			valid: true, batch: 4100, itemRev: 6220, recRev: 9020, revenue: 6720, fees: 232, tax: 565, net: 1523,
			perUnit: []int64{200, 1000, 150}, partBatch: []int64{600, 2000, 1500},
			rec: []int64{440, 2200, 330}, res: []int64{440, 2200, 50}, itemRevs: []int64{1320, 4400, 500},
		})
		if len(got.Pricing.FeeLines) != 2 || got.Pricing.FeeLines[0].Amount != 30 || got.Pricing.FeeLines[1].Amount != 202 {
			t.Errorf("feeLines = %+v, want 30 then 202", got.Pricing.FeeLines)
		}
		if want := 1523.0 / 6720 * 100; math.Abs(got.Pricing.AchievedMarginPct-want) > 1e-9 {
			t.Errorf("achievedMarginPct = %v, want %v", got.Pricing.AchievedMarginPct, want)
		}
	})

	t.Run("two parts, margin, tax, fees, shipping (margin before tax)", func(t *testing.T) {
		// A x3 (200c), B x2 (1000c); margin 20% on the pre-tax price, then grossed up by 10% tax (x1.1):
		//   A 200 / 0.8 = 250 -> 275; B 1000 / 0.8 = 1250 -> 1375. itemRevenue = 825 + 2750 = 3575; $5 shipping -> revenue 4075.
		//   Fees once: 30 + round(4075 x 3%) = 30 + 122 = 152. Tax round(3575 x 10 / 110) = 325. batchTotal 2600; shippingCost 300.
		//   netProfit = 4075 - 2600 - 300 - 152 - 325 = 698.
		in := OrderInputs{
			Parts: []OrderPartInputs{a(3), b(2)},
			Pricing: &OrderPricingInputs{
				Strategy: "margin", MarginPct: OptF{Value: 20, Present: true},
				FeeSchedule: feeSchedule, TaxRatePct: 10, ShippingCharge: 5, ShippingCost: 3,
			},
		}
		got := ComputeOrder(in, DefaultEngineOptions)
		checkOrder(t, got, orderWant{
			valid: true, batch: 2600, itemRev: 3575, recRev: 3575, revenue: 4075, fees: 152, tax: 325, net: 698,
			perUnit: []int64{200, 1000}, partBatch: []int64{600, 2000},
			rec: []int64{275, 1375}, res: []int64{275, 1375}, itemRevs: []int64{825, 2750},
		})
		if hasWarning(got.Pricing.Warnings, "netProfit", "net-profit-negative") {
			t.Errorf("positive net profit must not warn: %+v", got.Pricing.Warnings)
		}
	})

	t.Run("two parts, thin margin and costly shipping, negative net profit warns", func(t *testing.T) {
		// Margin 5%, tax 10%: A 200 / 0.95 x 1.1 = 231.58 -> 232; B 1000 / 0.95 x 1.1 = 1157.89 -> 1158.
		//   itemRevenue = 696 + 2316 = 3012; $5 shipping -> revenue 3512. Fees: 30 + round(3512 x 3%) = 30 + 105 = 135.
		//   Tax round(3012 x 10 / 110) = 274. batchTotal 2600; shippingCost $8.
		//   netProfit = 3512 - 2600 - 800 - 135 - 274 = -297: valid, with a net-profit-negative warning.
		in := OrderInputs{
			Parts: []OrderPartInputs{a(3), b(2)},
			Pricing: &OrderPricingInputs{
				Strategy: "margin", MarginPct: OptF{Value: 5, Present: true},
				FeeSchedule: feeSchedule, TaxRatePct: 10, ShippingCharge: 5, ShippingCost: 8,
			},
		}
		got := ComputeOrder(in, DefaultEngineOptions)
		checkOrder(t, got, orderWant{
			valid: true, batch: 2600, itemRev: 3012, recRev: 3012, revenue: 3512, fees: 135, tax: 274, net: -297,
			perUnit: []int64{200, 1000}, partBatch: []int64{600, 2000},
			rec: []int64{232, 1158}, res: []int64{232, 1158}, itemRevs: []int64{696, 2316},
		})
		if !hasWarning(got.Pricing.Warnings, "netProfit", "net-profit-negative") {
			t.Errorf("want a net-profit-negative warning, got %+v", got.Pricing.Warnings)
		}
	})

	t.Run("two parts, margin, no fees or tax", func(t *testing.T) {
		// A x3 (200c), B x2 (1000c), margin 50%: price = cost / 0.5.
		//   recommended A 400, B 2000; itemRevenue = 1200 + 4000 = 5200 = revenue.
		//   batchTotal = 600 + 2000 = 2600; netProfit = 5200 - 2600 = 2600 (50%).
		in := OrderInputs{
			Parts:   []OrderPartInputs{a(3), b(2)},
			Pricing: &OrderPricingInputs{Strategy: "margin", MarginPct: OptF{Value: 50, Present: true}},
		}
		got := ComputeOrder(in, DefaultEngineOptions)
		checkOrder(t, got, orderWant{
			valid: true, batch: 2600, itemRev: 5200, recRev: 5200, revenue: 5200, net: 2600,
			perUnit: []int64{200, 1000}, partBatch: []int64{600, 2000},
			rec: []int64{400, 2000}, res: []int64{400, 2000}, itemRevs: []int64{1200, 4000},
		})
		if got.Pricing.AchievedMarginPct != 50 {
			t.Errorf("achievedMarginPct = %v, want 50", got.Pricing.AchievedMarginPct)
		}
	})

	t.Run("tax rounds once on the order, not per part", func(t *testing.T) {
		// A x1 and A x1 at markup x1.5 grossed up by the 7% tax (x1.07): 321c each, itemRevenue 642. Tax once on the order at 7% inclusive: round(642 x 7/107) = 42.
		// batch 400; netProfit = 642 - 400 - 42 = 200.
		in := OrderInputs{
			Parts:   []OrderPartInputs{a(1), a(1)},
			Pricing: &OrderPricingInputs{Strategy: "markup", MarkupMultiplier: OptF{Value: 1.5, Present: true}, TaxRatePct: 7},
		}
		got := ComputeOrder(in, DefaultEngineOptions)
		checkOrder(t, got, orderWant{
			valid: true, batch: 400, itemRev: 642, recRev: 642, revenue: 642, tax: 42, net: 200,
			perUnit: []int64{200, 200}, partBatch: []int64{200, 200},
			rec: []int64{321, 321}, res: []int64{321, 321}, itemRevs: []int64{321, 321},
		})
	})

	t.Run("prep labor amortizes per part over that part's quantity", func(t *testing.T) {
		// Each part carries 60 min prep at $12/h = $12.00 batch labor. A x4: 1200/4 = 300c per unit, perUnit 500. B x1: 1200c per unit, perUnit 2200.
		// batch = 500x4 + 2200x1 = 4200.
		pa, pb := a(4), b(1)
		for _, p := range []*OrderPartInputs{&pa, &pb} {
			p.PrepMinutes, p.LaborRate = 60, 12
		}
		got := ComputeOrder(OrderInputs{Parts: []OrderPartInputs{pa, pb}}, DefaultEngineOptions)
		if got.Pricing != nil {
			t.Fatal("pricing must be nil when the order has no pricing block")
		}
		if got.Parts[0].Breakdown.PerUnitTotal != 500 || got.Parts[1].Breakdown.PerUnitTotal != 2200 {
			t.Errorf("perUnit = %d/%d, want 500/2200", got.Parts[0].Breakdown.PerUnitTotal, got.Parts[1].Breakdown.PerUnitTotal)
		}
		if got.BatchTotal != 4200 || !got.Valid {
			t.Errorf("batchTotal=%d valid=%v, want 4200 true", got.BatchTotal, got.Valid)
		}
	})

	t.Run("one invalid part invalidates the order and names its index", func(t *testing.T) {
		// B has a negative weight (below-min on parts[1].weightG). The order is
		// invalid, no part is priced (recommended 0), and the zero-cost part C
		// (weight 0) also reports parts[2].perUnitTotal.
		bad := b(2)
		bad.WeightG = -1
		zero := orderPart("C", 0, 0, 1)
		in := OrderInputs{
			Parts:   []OrderPartInputs{a(3), bad, zero},
			Pricing: &OrderPricingInputs{Strategy: "markup", MarkupMultiplier: OptF{Value: 2, Present: true}},
		}
		got := ComputeOrder(in, DefaultEngineOptions)
		if got.Valid || got.Pricing == nil || got.Pricing.Valid {
			t.Fatalf("valid = %v pricing.valid = %v, want both false", got.Valid, got.Pricing != nil && got.Pricing.Valid)
		}
		for i, p := range got.Parts {
			if p.RecommendedPrice != 0 || p.ResolvedPrice != 0 || p.ItemRevenue != 0 {
				t.Errorf("part %d priced on an invalid order: %+v", i, p)
			}
		}
		if !hasWarning(got.Warnings, "parts[1].weightG", "below-min") {
			t.Errorf("order warnings missing parts[1].weightG below-min: %+v", got.Warnings)
		}
		if !hasWarning(got.Pricing.Warnings, "parts[2].perUnitTotal", "below-min") {
			t.Errorf("pricing warnings missing parts[2].perUnitTotal: %+v", got.Pricing.Warnings)
		}
		if got.Parts[0].Breakdown.Valid != true || got.Parts[1].Breakdown.Valid != false {
			t.Errorf("part validity = %v/%v, want true/false", got.Parts[0].Breakdown.Valid, got.Parts[1].Breakdown.Valid)
		}
	})

	t.Run("invalid per-part override is named by index", func(t *testing.T) {
		bad := over(b(1), -2)
		in := OrderInputs{
			Parts:   []OrderPartInputs{a(1), bad},
			Pricing: &OrderPricingInputs{Strategy: "markup", MarkupMultiplier: OptF{Value: 2, Present: true}},
		}
		got := ComputeOrder(in, DefaultEngineOptions)
		if got.Valid || !hasWarning(got.Pricing.Warnings, "parts[1].salePriceOverride", "below-min") {
			t.Fatalf("want invalid with parts[1].salePriceOverride below-min, got valid=%v %+v", got.Valid, got.Pricing.Warnings)
		}
	})
}

type orderWant struct {
	valid                                           bool
	batch, itemRev, recRev, revenue, fees, tax, net int64
	perUnit, partBatch, rec, res, itemRevs          []int64
}

func checkOrder(t *testing.T, got OrderBreakdown, w orderWant) {
	t.Helper()
	if got.Valid != w.valid || got.Pricing == nil || got.Pricing.Valid != w.valid {
		t.Fatalf("valid = %v (pricing %+v), want %v; warnings %+v", got.Valid, got.Pricing, w.valid, got.Pricing.Warnings)
	}
	if got.BatchTotal != w.batch {
		t.Errorf("batchTotal = %d, want %d", got.BatchTotal, w.batch)
	}
	pr := got.Pricing
	if pr.Revenue != w.revenue || pr.Fees != w.fees || pr.Tax != w.tax || pr.NetProfit != w.net {
		t.Errorf("revenue/fees/tax/net = %d/%d/%d/%d, want %d/%d/%d/%d", pr.Revenue, pr.Fees, pr.Tax, pr.NetProfit, w.revenue, w.fees, w.tax, w.net)
	}
	if len(got.Parts) >= 2 && (pr.RecommendedPrice != w.recRev || pr.ResolvedPrice != w.itemRev) {
		t.Errorf("order recommended/resolved = %d/%d, want %d/%d", pr.RecommendedPrice, pr.ResolvedPrice, w.recRev, w.itemRev)
	}
	if got.ItemRevenue != w.itemRev {
		t.Errorf("OrderBreakdown.itemRevenue = %d, want %d", got.ItemRevenue, w.itemRev)
	}
	for i, p := range got.Parts {
		if p.Breakdown.PerUnitTotal != w.perUnit[i] || p.Breakdown.BatchTotal != w.partBatch[i] {
			t.Errorf("part %d perUnit/batch = %d/%d, want %d/%d", i, p.Breakdown.PerUnitTotal, p.Breakdown.BatchTotal, w.perUnit[i], w.partBatch[i])
		}
		if p.RecommendedPrice != w.rec[i] || p.ResolvedPrice != w.res[i] || p.ItemRevenue != w.itemRevs[i] {
			t.Errorf("part %d rec/res/itemRev = %d/%d/%d, want %d/%d/%d", i, p.RecommendedPrice, p.ResolvedPrice, p.ItemRevenue, w.rec[i], w.res[i], w.itemRevs[i])
		}
	}
}

func TestDecodeQuoteInputs(t *testing.T) {
	part := `{"label":"A","weightG":100,"pricePerKg":20,"quantity":1,"energy":{"mode":"flat","flatRate":0}}`
	cases := []struct {
		name, raw string
		order     bool
		errSub    string
		tooMany   bool
	}{
		{"order", `{"parts":[` + part + `]}`, true, "", false},
		{"flat single-part bag", `{"weightG":1,"energy":{"mode":"flat","flatRate":0}}`, false, "unknown field", false},
		{"no parts key", `{}`, false, "at least 1", false},
		{"order unknown field", `{"parts":[` + part + `],"bogus":2}`, true, "unknown field", false},
		{"part unknown field", `{"parts":[{"bogus":1}]}`, true, "unknown field", false},
		{"empty parts", `{"parts":[]}`, true, "at least 1", false},
		{"null parts", `{"parts":null}`, true, "at least 1", false},
		{"part pricing", `{"parts":[{"pricing":{"strategy":"margin"}}]}`, true, "parts[0].pricing", false},
		{"long label", `{"parts":[{"label":"` + strings.Repeat("x", 121) + `"}]}`, true, "parts[0].label", false},
		{"max label", `{"parts":[{"label":"` + strings.Repeat("é", 120) + `"}]}`, true, "", false},
		{"21 parts", `{"parts":[` + strings.TrimSuffix(strings.Repeat(part+",", 21), ",") + `]}`, true, "at most 20", true},
		{"20 parts", `{"parts":[` + strings.TrimSuffix(strings.Repeat(part+",", 20), ",") + `]}`, true, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			order, err := decodeQuoteInputs(json.RawMessage(tc.raw))
			if tc.errSub != "" {
				if err == nil || !strings.Contains(err.Error(), tc.errSub) {
					t.Fatalf("err = %v, want containing %q", err, tc.errSub)
				}
				if errors.Is(err, errTooManyParts) != tc.tooMany {
					t.Fatalf("errTooManyParts = %v, want %v", errors.Is(err, errTooManyParts), tc.tooMany)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if order == nil || len(order.Parts) == 0 {
				t.Fatalf("order = %+v, want at least one part", order)
			}
		})
	}
}

// --- HTTP / storage ---------------------------------------------------------

// orderPartJSON is a material-only part (see orderPart): perUnit = 200c.
func orderPartJSON(label string, qty int) string {
	return `{"label":"` + label + `","weightG":100,"pricePerKg":20,"wastePct":0,"timeHours":0,"timeMinutes":0,"watts":0,` +
		`"energy":{"mode":"flat","flatRate":0},"failureRatePct":0,"prepMinutes":0,"laborRate":0,"machineCost":0,` +
		`"machineLifeHours":0,"monthlyFixed":0,"monthlyHours":0,"quantity":` + itoa(qty) + `}`
}

func httptestGet(t *testing.T, mux *http.ServeMux, path string) *httptest.ResponseRecorder {
	return serveReq(mux, httptest.NewRequest("GET", path, nil))
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func orderBody(parts ...string) string {
	return `{"label":"Order","status":"draft","statusLog":[],"inputs":{"parts":[` + strings.Join(parts, ",") + `],` +
		`"pricing":{"strategy":"markup","markupMultiplier":2,"taxRatePct":0,"shippingCharge":0,"shippingCost":0}},"options":{}}`
}

func TestQuotesOrderStorageSemantics(t *testing.T) {
	mux := newTestMux(t, t.TempDir())

	// Two parts, qty 3 and 2 at 200c/unit, markup x2: item revenue
	// 400x3 + 400x2 = 2000; batch = 600 + 400 = 1000; net = 1000.
	rec := postQuote(t, mux, orderBody(orderPartJSON("A", 3), orderPartJSON("B", 2)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST = %d %s", rec.Code, rec.Body.String())
	}
	q := mustUnmarshalQuote(t, rec)
	if q.PartCount != 2 || q.Quantity != 1 || q.PerUnitTotalCents != 1000 || q.BatchTotalCents != 1000 {
		t.Errorf("columns = parts %d qty %v perUnit %d batch %d, want 2/1/1000/1000", q.PartCount, q.Quantity, q.PerUnitTotalCents, q.BatchTotalCents)
	}
	if q.RecommendedPriceCents == nil || *q.RecommendedPriceCents != 2000 || q.ResolvedPriceCents == nil || *q.ResolvedPriceCents != 2000 {
		t.Errorf("recommended/resolved = %v/%v, want 2000/2000", q.RecommendedPriceCents, q.ResolvedPriceCents)
	}
	if q.NetProfitCents == nil || *q.NetProfitCents != 1000 || !q.Valid {
		t.Errorf("net/valid = %v/%v, want 1000/true", q.NetProfitCents, q.Valid)
	}

	// GET round-trips the inputs bag verbatim and partCount.
	get := httptestGet(t, mux, "/api/quotes/"+q.ID)
	got := mustUnmarshalQuote(t, get)
	if got.PartCount != 2 || string(got.Inputs) != string(q.Inputs) {
		t.Errorf("GET partCount=%d inputs match=%v", got.PartCount, string(got.Inputs) == string(q.Inputs))
	}

	// PUT down to one part: single-part semantics (qty 3, per-unit 200, price 400).
	put := putQuote(t, mux, q.ID, orderBody(orderPartJSON("A", 3)))
	if put.Code != http.StatusOK {
		t.Fatalf("PUT = %d %s", put.Code, put.Body.String())
	}
	one := mustUnmarshalQuote(t, put)
	if one.PartCount != 1 || one.Quantity != 3 || one.PerUnitTotalCents != 200 || one.BatchTotalCents != 600 ||
		one.ResolvedPriceCents == nil || *one.ResolvedPriceCents != 400 {
		t.Errorf("one-part order columns = %+v", one)
	}

	// quoteBodyFor's bag is a one-part order: part_count 1.
	single := mustUnmarshalQuote(t, postQuote(t, mux, quoteBodyFor("")))
	if single.PartCount != 1 {
		t.Errorf("one-part partCount = %d, want 1", single.PartCount)
	}
}

func TestQuotesOrderPartLimit(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	twenty := make([]string, 20)
	for i := range twenty {
		twenty[i] = orderPartJSON("P", 1)
	}
	if rec := postQuote(t, mux, orderBody(twenty...)); rec.Code != http.StatusCreated {
		t.Fatalf("20 parts = %d %s, want 201", rec.Code, rec.Body.String())
	}
	if rec := postQuote(t, mux, orderBody(append(twenty, orderPartJSON("P", 1))...)); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("21 parts = %d %s, want 422", rec.Code, rec.Body.String())
	}
	if rec := postCompute(t, mux, `{"inputs":{"parts":[`+strings.Join(append(twenty, orderPartJSON("P", 1)), ",")+`]},"options":{}}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("compute 21 parts = %d, want 422", rec.Code)
	}
	if rec := postQuote(t, mux, orderBody()); rec.Code != http.StatusBadRequest {
		t.Fatalf("0 parts = %d, want 400", rec.Code)
	}
}

func TestQuoteComputeAnswersOrderBreakdown(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	rec := postCompute(t, mux, `{"inputs":{"parts":[`+orderPartJSON("A", 3)+`,`+orderPartJSON("B", 2)+`],`+
		`"pricing":{"strategy":"markup","markupMultiplier":2}},"options":{}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("order compute = %d %s", rec.Code, rec.Body.String())
	}
	var ob OrderBreakdown
	if err := json.Unmarshal(rec.Body.Bytes(), &ob); err != nil {
		t.Fatal(err)
	}
	if len(ob.Parts) != 2 || ob.BatchTotal != 1000 || ob.Pricing == nil || ob.Pricing.ResolvedPrice != 2000 || ob.Parts[1].Label != "B" {
		t.Errorf("order breakdown = %+v", ob)
	}
	// A NaN input must still marshal (sanitized) inside a part.
	nan := strings.Replace(orderPartJSON("A", 1), `"weightG":100`, `"weightG":null`, 1)
	if rec := postCompute(t, mux, `{"inputs":{"parts":[`+nan+`]},"options":{}}`); rec.Code != http.StatusOK {
		t.Fatalf("order compute with NaN = %d %s", rec.Code, rec.Body.String())
	}
	// The flat single-part shape is gone: it is an unknown-field 400, not a CostBreakdown.
	flat := postCompute(t, mux, `{"inputs":{"weightG":100,"pricePerKg":20,"quantity":1},"options":{}}`)
	if flat.Code != http.StatusBadRequest {
		t.Fatalf("flat compute = %d %s, want 400", flat.Code, flat.Body.String())
	}
}

func TestQuotesBodyCapFitsTwentyHeavyParts(t *testing.T) {
	// Every part carries 100 hardware lines; 20 such parts must
	// fit under quotesMaxBytes (256 KiB).
	hw := strings.TrimSuffix(strings.Repeat(`{"name":"Heat-set insert M3","unitCost":0.1,"qtyPerUnit":1,"lostOnFailure":false},`, 100), ",")
	part := strings.Replace(orderPartJSON("P", 1), `"quantity":1`, `"quantity":1,"hardware":[`+hw+`]`, 1)
	parts := make([]string, 20)
	for i := range parts {
		parts[i] = part
	}
	body := orderBody(parts...)
	if len(body) <= 64*1024 || len(body) >= quotesMaxBytes {
		t.Fatalf("test setup: body is %d bytes, want between 64 KiB and %d", len(body), quotesMaxBytes)
	}
	mux := newTestMux(t, t.TempDir())
	if rec := postQuote(t, mux, body); rec.Code != http.StatusCreated {
		t.Fatalf("POST = %d %s", rec.Code, rec.Body.String())
	}
}

func TestQuoteSendLines(t *testing.T) {
	fake := &fakeSMTP{noSTARTTLS: true}
	mux := newQuoteSendMux(t, fake.dialer(t), func(cfg *Config) {
		cfg.Smtp = Smtp{Host: "localhost", Port: 587, From: "quotes@example.com", To: "owner@example.com"}
	})
	// Rows: 3 x $4.00 = $12.00, 2 x $10.00 = $20.00; shipping $5.00.
	body := `{"quote":{"id":"q-1","label":"Bracket order","clientName":"Beta Print","shippingCents":500,` +
		`"lines":[{"label":"Bracket body","quantity":3,"unitPriceCents":400},{"label":"Bracket lid","quantity":2,"unitPriceCents":1000}],` +
		`"savedAt":"2026-08-01T12:00:00Z"},"to":"client@example.com"}`
	rec := postQuoteSend(t, mux, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", rec.Code, rec.Body.String())
	}
	got := fake.snapshot().dataBody
	for _, want := range []string{"Bracket body", "Bracket lid", "$12.00", "$20.00", "Shipping", "$37.00"} {
		if !strings.Contains(got, want) {
			t.Errorf("dataBody missing %q: %q", want, got)
		}
	}

	// Validation: too many lines, bad line fields; flat fields not required with lines.
	line := `{"label":"x","quantity":1,"unitPriceCents":1}`
	tooMany := `{"quote":{"id":"q","label":"L","lines":[` + strings.TrimSuffix(strings.Repeat(line+",", 21), ",") + `],"savedAt":"2026-08-01T12:00:00Z"},"to":"client@example.com"}`
	if rec := postQuoteSend(t, mux, tooMany); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("21 lines = %d, want 422", rec.Code)
	}
	badLine := `{"quote":{"id":"q","label":"L","lines":[{"label":"","quantity":0,"unitPriceCents":-1}],"savedAt":"2026-08-01T12:00:00Z"},"to":"client@example.com"}`
	rec = postQuoteSend(t, mux, badLine)
	if rec.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(rec.Body.String(), "lines[0].label") || !strings.Contains(rec.Body.String(), "lines[0].quantity") || !strings.Contains(rec.Body.String(), "lines[0].unitPriceCents") {
		t.Errorf("bad line = %d %s", rec.Code, rec.Body.String())
	}
}

func TestExportIncludesPartCount(t *testing.T) {
	mux, _ := newExportTestSetup(t)
	if rec := postQuote(t, mux, orderBody(orderPartJSON("A", 3), orderPartJSON("B", 2), orderPartJSON("C", 1))); rec.Code != http.StatusCreated {
		t.Fatalf("seed order: %d %s", rec.Code, rec.Body.String())
	}
	rec := getExport(t, mux)
	if rec.Code != http.StatusOK {
		t.Fatalf("export: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"partCount": 3`) && !strings.Contains(rec.Body.String(), `"partCount":3`) {
		t.Errorf("export lacks partCount 3: %s", rec.Body.String())
	}
	var resp exportPayload
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Quotes) != 1 || resp.Quotes[0].PartCount != 3 {
		t.Fatalf("quotes = %+v, want one with partCount 3", resp.Quotes)
	}
}
