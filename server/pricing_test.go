package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Asserts ComputeQuote/ComputePricing against testdata/quote-vectors.json (also consumed by pricing_order_test.go and pricing_compat_test.go): every cent value, valid flag, line order/ids, feeLines and PricingResult field, but not the display-only LineItem.Formula/Inputs (where a NaN is the string "__NaN__") or warning text. A NaN in a vector's inputs is JSON null.
// The first 52 vectors carry no derivation; later ones are HAND-DERIVED (a vector copied from the implementation proves nothing), carrying a "derivation" string (ignored by the decoder) and omitting display-only fields.
// Both kinds are fixed once landed: expected outputs change only with a deliberate, separately justified formula change.

type quoteVector struct {
	Name     string         `json:"name"`
	Inputs   QuoteInputs    `json:"inputs"`
	Options  *EngineOptions `json:"options"`
	Expected struct {
		CostBreakdown CostBreakdown  `json:"costBreakdown"`
		PricingResult *PricingResult `json:"pricingResult"`
	} `json:"expected"`
}

func loadQuoteVectors(t *testing.T) []quoteVector {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "testdata", "quote-vectors.json"))
	if err != nil {
		t.Fatalf("reading shared quote-vectors fixture: %v", err)
	}
	var vectors []quoteVector
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatalf("parsing shared quote-vectors fixture: %v", err)
	}
	if len(vectors) == 0 {
		t.Fatal("shared quote-vectors fixture is empty")
	}
	return vectors
}

func TestComputeQuoteMatchesSharedFixture(t *testing.T) {
	for _, v := range loadQuoteVectors(t) {
		t.Run(v.Name, func(t *testing.T) {
			options := DefaultEngineOptions
			if v.Options != nil {
				options = *v.Options
			}
			got := ComputeQuote(v.Inputs, options)
			want := v.Expected.CostBreakdown
			assertCostBreakdown(t, v.Name, got, want)

			if (got.Pricing == nil) != (v.Expected.PricingResult == nil) {
				t.Fatalf("%s: pricing presence mismatch: got nil=%v, want nil=%v", v.Name, got.Pricing == nil, v.Expected.PricingResult == nil)
			}
			if got.Pricing != nil {
				assertPricingResult(t, v.Name, *got.Pricing, *v.Expected.PricingResult)
			}
		})
	}
}

func assertCostBreakdown(t *testing.T, name string, got, want CostBreakdown) {
	t.Helper()
	if len(got.PerUnit) != len(want.PerUnit) {
		t.Fatalf("%s: perUnit line count = %d, want %d", name, len(got.PerUnit), len(want.PerUnit))
	}
	for i := range want.PerUnit {
		g, w := got.PerUnit[i], want.PerUnit[i]
		if g.ID != w.ID {
			t.Errorf("%s: perUnit[%d].id = %q, want %q (line order must be pinned)", name, i, g.ID, w.ID)
		}
		if g.Amount != w.Amount {
			t.Errorf("%s: perUnit[%d] (%s).amount = %d, want %d", name, i, w.ID, g.Amount, w.Amount)
		}
	}
	if got.PerUnitTotal != want.PerUnitTotal {
		t.Errorf("%s: perUnitTotal = %d, want %d", name, got.PerUnitTotal, want.PerUnitTotal)
	}
	if got.BatchTotal != want.BatchTotal {
		t.Errorf("%s: batchTotal = %d, want %d", name, got.BatchTotal, want.BatchTotal)
	}
	if got.Quantity != want.Quantity {
		t.Errorf("%s: quantity = %v, want %v", name, got.Quantity, want.Quantity)
	}
	if got.Valid != want.Valid {
		t.Errorf("%s: cost valid = %v, want %v", name, got.Valid, want.Valid)
	}
}

func assertPricingResult(t *testing.T, name string, got, want PricingResult) {
	t.Helper()
	if got.Strategy != want.Strategy {
		t.Errorf("%s: pricing.strategy = %q, want %q", name, got.Strategy, want.Strategy)
	}
	if got.Valid != want.Valid {
		t.Errorf("%s: pricing.valid = %v, want %v", name, got.Valid, want.Valid)
	}
	if got.RecommendedPrice != want.RecommendedPrice {
		t.Errorf("%s: pricing.recommendedPrice = %d, want %d", name, got.RecommendedPrice, want.RecommendedPrice)
	}
	if got.ResolvedPrice != want.ResolvedPrice {
		t.Errorf("%s: pricing.resolvedPrice = %d, want %d", name, got.ResolvedPrice, want.ResolvedPrice)
	}
	if got.Revenue != want.Revenue {
		t.Errorf("%s: pricing.revenue = %d, want %d", name, got.Revenue, want.Revenue)
	}
	if got.Fees != want.Fees {
		t.Errorf("%s: pricing.fees = %d, want %d", name, got.Fees, want.Fees)
	}
	if len(got.FeeLines) != len(want.FeeLines) {
		t.Fatalf("%s: feeLines count = %d, want %d", name, len(got.FeeLines), len(want.FeeLines))
	}
	for i := range want.FeeLines {
		g, w := got.FeeLines[i], want.FeeLines[i]
		if g.Label != w.Label || g.Amount != w.Amount {
			t.Errorf("%s: feeLines[%d] = %+v, want %+v", name, i, g, w)
		}
	}
	if got.Tax != want.Tax {
		t.Errorf("%s: pricing.tax = %d, want %d", name, got.Tax, want.Tax)
	}
	if got.ShippingCharge != want.ShippingCharge {
		t.Errorf("%s: pricing.shippingCharge = %d, want %d", name, got.ShippingCharge, want.ShippingCharge)
	}
	if got.ShippingCost != want.ShippingCost {
		t.Errorf("%s: pricing.shippingCost = %d, want %d", name, got.ShippingCost, want.ShippingCost)
	}
	if got.NetProfit != want.NetProfit {
		t.Errorf("%s: pricing.netProfit = %d, want %d", name, got.NetProfit, want.NetProfit)
	}
	if math.Abs(got.AchievedMarginPct-want.AchievedMarginPct) > 1e-9 {
		t.Errorf("%s: pricing.achievedMarginPct = %v, want %v", name, got.AchievedMarginPct, want.AchievedMarginPct)
	}
	gotName, wantName := "", ""
	if got.FeeScheduleName != nil {
		gotName = *got.FeeScheduleName
	}
	if want.FeeScheduleName != nil {
		wantName = *want.FeeScheduleName
	}
	if gotName != wantName {
		t.Errorf("%s: pricing.feeScheduleName = %q, want %q", name, gotName, wantName)
	}
}

// Pins roundCents' half-away-from-zero tie rule with the 1e-9 epsilon, and non-finite input mapping to 0 (not part of the shared fixture).
func TestRoundCentsAndToCents(t *testing.T) {
	cases := []struct {
		name string
		in   float64
		want int64
	}{
		{"exact", 100, 100},
		{"half-up-positive", 100.5, 101},
		{"half-away-negative", -100.5, -101},
		{"epsilon-tie-underflow", 100.49999999999999, 101}, // the epsilon pushes this IEEE754-adjacent-to-.5 value over the tie
		{"nan", math.NaN(), 0},
		{"pos-inf", math.Inf(1), 0},
		{"neg-inf", math.Inf(-1), 0},
		{"zero", 0, 0},
		{"neg-zero-input", math.Copysign(0, -1), 0},
		// Magnitude saturation (see roundCents' MAGNITUDE note): int64(1e19) is implementation-defined and yields a large NEGATIVE on amd64; the sign must survive.
		{"saturates-positive", 1e19, math.MaxInt64},
		{"saturates-negative", -1e19, -math.MaxInt64},
		{"saturates-at-2pow63", 9223372036854775808.0, math.MaxInt64},
		{"just-below-2pow63-does-not-saturate", 9223372036854775000.0, 9223372036854774784},
	}
	for _, c := range cases {
		if got := roundCents(c.in); got != c.want {
			t.Errorf("roundCents(%v) [%s] = %d, want %d", c.in, c.name, got, c.want)
		}
	}
	if got := toCents(1.005); got != 101 {
		t.Errorf("toCents(1.005) = %d, want 101 (classic IEEE754 tie-underflow case)", got)
	}
}

// markupPricing is a markup pricing block with no tax or shipping.
func markupPricing(multiplier float64) *PricingInputs {
	return &PricingInputs{
		Strategy:         "markup",
		MarkupMultiplier: OptF{Value: multiplier, Present: true},
		TaxRatePct:       F(0), ShippingCharge: F(0), ShippingCost: F(0),
	}
}

// boundaryBaseInputs is a rounding-unambiguous base: material 200c (100 g x $20/kg), electricity 10c (1 h x 1000 W x $0.10/kWh), depreciation 0, failure 0 (failureRatePct 0 unless a case sets it),
// labor 1000c ($10/h x 60 min, batch, qty 1), overhead 0; perUnitTotal $12.10 (1210c).
func boundaryBaseInputs() QuoteInputs {
	return QuoteInputs{
		WeightG: F(100), PricePerKg: F(20), WastePct: F(0),
		TimeHours: F(1), TimeMinutes: F(0), Watts: F(1000),
		Energy:         EnergyInputs{Mode: "flat", FlatRate: OptF{Value: 0.10, Present: true}},
		FailureRatePct: F(0), PrepMinutes: F(60), LaborRate: F(10),
		MachineCost: F(0), MachineLifeHours: F(0),
		MonthlyFixed: F(0), MonthlyHours: F(0), Quantity: F(1),
	}
}

func warningFor(warnings []FieldWarning, field, code string) (FieldWarning, bool) {
	for _, w := range warnings {
		if w.Field == field && w.Code == code {
			return w, true
		}
	}
	return FieldWarning{}, false
}

func hasWarning(warnings []FieldWarning, field, code string) bool {
	_, ok := warningFor(warnings, field, code)
	return ok
}

// Pins the below-min/above-max warning COPY, which the fixture comparison excludes (Go once emitted "weightG must be below min" for a value below the minimum). The admin UI rebuilds copy from FieldWarning.Bound,
// but export, logs and API clients read this string. The expected text is the operator-facing phrasing character for character, including plain-decimal bounds (never Go's 1e+09).
func TestValidateFieldMessagesNameTheBound(t *testing.T) {
	inputs := boundaryBaseInputs()
	inputs.WeightG = F(-1)
	inputs.WastePct = F(150)
	inputs.PricePerKg = F(2e9) // above maxMoneyAmount, the largest bound in this file
	bd := ComputeQuote(inputs, DefaultEngineOptions)

	cases := []struct{ field, code, want string }{
		{"weightG", "below-min", "weightG must be ≥ 0"},
		{"wastePct", "above-max", "wastePct must be ≤ 100"},
		{"pricePerKg", "above-max", "pricePerKg must be ≤ 1000000000"},
	}
	for _, c := range cases {
		w, ok := warningFor(bd.Warnings, c.field, c.code)
		if !ok {
			t.Errorf("no %s/%s warning; warnings = %+v", c.field, c.code, bd.Warnings)
			continue
		}
		if w.Message != c.want {
			t.Errorf("%s/%s message = %q, want %q", c.field, c.code, w.Message, c.want)
		}
	}
}

// A Strategy that is not exactly "margin" once priced by the MARKUP formula while echoing the unrecognized name back. Both legitimate values keep their behavior; anything else is an error.
func TestStrategyAndEnergyModeAreValidatedEnums(t *testing.T) {
	price := func(strategy string) PricingResult {
		t.Helper()
		inputs := boundaryBaseInputs()
		inputs.Pricing = &PricingInputs{
			Strategy:         strategy,
			MarginPct:        OptF{Value: 30, Present: true},
			MarkupMultiplier: OptF{Value: 2.5, Present: true},
			TaxRatePct:       F(0), ShippingCharge: F(0), ShippingCost: F(0),
		}
		bd := ComputeQuote(inputs, DefaultEngineOptions)
		if bd.Pricing == nil {
			t.Fatal("pricing result is nil")
		}
		return *bd.Pricing
	}

	// The two legitimate values are unchanged: margin on a 1210c cost basis
	// at 30% is 1210/0.7 = 1728.57... -> 1729c; markup is 1210 * 2.5 = 3025c.
	if p := price("margin"); !p.Valid || p.RecommendedPrice != 1729 {
		t.Errorf("margin: valid=%v recommendedPrice=%d, want true/1729", p.Valid, p.RecommendedPrice)
	}
	if p := price("markup"); !p.Valid || p.RecommendedPrice != 3025 {
		t.Errorf("markup: valid=%v recommendedPrice=%d, want true/3025", p.Valid, p.RecommendedPrice)
	}

	for _, bogus := range []string{"Margin", "MARKUP", "", "cost-plus"} {
		p := price(bogus)
		if p.Valid {
			t.Errorf("strategy %q: valid = true, want false (an unrecognized strategy must not price silently)", bogus)
		}
		if !hasWarning(p.Warnings, "strategy", "invalid-enum") {
			t.Errorf("strategy %q: no strategy/invalid-enum warning; warnings = %+v", bogus, p.Warnings)
		}
		if p.RecommendedPrice != 0 {
			t.Errorf("strategy %q: recommendedPrice = %d, want 0", bogus, p.RecommendedPrice)
		}
	}

	// energy.mode has the same shape: anything not "flat" took the TOU
	// branch. Both legitimate values stay valid; an unrecognized one is a
	// named error rather than a silent reinterpretation.
	flat := boundaryBaseInputs()
	if bd := ComputeQuote(flat, DefaultEngineOptions); !bd.Valid {
		t.Errorf("energy.mode flat: valid = false, want true; warnings = %+v", bd.Warnings)
	}
	tou := boundaryBaseInputs()
	tou.Energy = EnergyInputs{
		Mode:        "tou",
		PeakRate:    OptF{Value: 0.20, Present: true},
		OffPeakRate: OptF{Value: 0.05, Present: true},
		OffPeakFrac: OptF{Value: 0.5, Present: true},
	}
	if bd := ComputeQuote(tou, DefaultEngineOptions); !bd.Valid {
		t.Errorf("energy.mode tou: valid = false, want true; warnings = %+v", bd.Warnings)
	}
	bogus := boundaryBaseInputs()
	bogus.Energy.Mode = "TOU"
	bd := ComputeQuote(bogus, DefaultEngineOptions)
	if bd.Valid {
		t.Error("energy.mode \"TOU\": valid = true, want false")
	}
	if !hasWarning(bd.Warnings, "mode", "invalid-enum") {
		t.Errorf("energy.mode \"TOU\": no mode/invalid-enum warning; warnings = %+v", bd.Warnings)
	}
}

// A client-supplied fee schedule with a percent base other than "revenue" must be rejected, not silently computed on revenue: decodeQuoteInputs accepts Base, but only config.yaml schedules were checked.
func TestFeePercentBaseMustBeRevenue(t *testing.T) {
	withBase := func(base string) PricingResult {
		t.Helper()
		inputs := boundaryBaseInputs()
		inputs.Pricing = markupPricing(2)
		inputs.Pricing.FeeSchedule = &PricingFeeSchedule{
			Percent: []PricingFeePercent{{Pct: F(10), Base: base, Label: "pct"}},
		}
		bd := ComputeQuote(inputs, DefaultEngineOptions)
		return *bd.Pricing
	}

	// revenue is unchanged: 1210 * 2 = 2420c revenue, 10% = 242c of fees.
	ok := withBase("revenue")
	if !ok.Valid || ok.Fees != 242 {
		t.Errorf("base revenue: valid=%v fees=%d, want true/242", ok.Valid, ok.Fees)
	}
	for _, bad := range []string{"profit", "subtotal", ""} {
		p := withBase(bad)
		if p.Valid {
			t.Errorf("base %q: valid = true, want false", bad)
		}
		if !hasWarning(p.Warnings, "feeSchedule.percent[0].base", "invalid-enum") {
			t.Errorf("base %q: no feeSchedule.percent[0].base/invalid-enum warning; warnings = %+v", bad, p.Warnings)
		}
	}
}

// Fee-schedule validation errors must name the entry (as hardware[i].unitCost does), so a three-entry schedule's second fixed fee is distinguishable from its third percent fee.
func TestFeeScheduleWarningsAreIndexed(t *testing.T) {
	inputs := boundaryBaseInputs()
	inputs.Pricing = markupPricing(2)
	inputs.Pricing.FeeSchedule = &PricingFeeSchedule{
		Fixed: []PricingFeeFixed{
			{Amount: F(1), Label: "good fixed"},
			{Amount: F(-1), Label: "bad fixed"}, // index 1: below-min
		},
		Percent: []PricingFeePercent{
			{Pct: F(5), Base: "revenue", Label: "good pct"},
			{Pct: F(5), Base: "profit", Label: "bad pct"}, // index 1: invalid-enum
		},
	}
	p := *ComputeQuote(inputs, DefaultEngineOptions).Pricing
	if p.Valid {
		t.Error("valid = true, want false")
	}
	for _, w := range []struct{ field, code string }{
		{"feeSchedule.fixed[1].amount", "below-min"},
		{"feeSchedule.percent[1].base", "invalid-enum"},
	} {
		if !hasWarning(p.Warnings, w.field, w.code) {
			t.Errorf("no %s/%s warning; warnings = %+v", w.field, w.code, p.Warnings)
		}
	}
	// The good entries at index 0 must not carry a warning under the bad
	// entries' indices or under the bare "feeSchedule" name.
	if hasWarning(p.Warnings, "feeSchedule", "below-min") || hasWarning(p.Warnings, "feeSchedule", "invalid-enum") {
		t.Errorf("warnings carry the unindexed \"feeSchedule\" field name: %+v", p.Warnings)
	}

	// Two bad entries of the SAME kind at different indices must not produce byte-identical warnings; the operator could not tell which line to fix.
	twoBad := boundaryBaseInputs()
	twoBad.Pricing = markupPricing(2)
	twoBad.Pricing.FeeSchedule = &PricingFeeSchedule{
		Fixed: []PricingFeeFixed{
			{Amount: F(1), Label: "good fixed"},
			{Amount: F(-1), Label: "bad fixed 1"}, // index 1: below-min
			{Amount: F(-2), Label: "bad fixed 2"}, // index 2: below-min
		},
	}
	p2 := *ComputeQuote(twoBad, DefaultEngineOptions).Pricing
	w1, ok1 := warningFor(p2.Warnings, "feeSchedule.fixed[1].amount", "below-min")
	w2, ok2 := warningFor(p2.Warnings, "feeSchedule.fixed[2].amount", "below-min")
	if !ok1 || !ok2 {
		t.Fatalf("missing indexed warnings for the two bad fixed fees; warnings = %+v", p2.Warnings)
	}
	if w1 == w2 {
		t.Errorf("the two bad fixed-fee warnings are byte-identical: %+v", w1)
	}
}

// Pins the F type's semantics: an F-typed key ABSENT from the request body decodes to 0 and validates as a legitimate 0 (a float64 has no presence state),
// so `required: true` on validateF would be inert and an absent taxRatePct must not be reported "required". An explicit `null` still errors, since F's UnmarshalJSON maps it to NaN.
func TestAbsentFTypedKeyMeansZeroNotRequired(t *testing.T) {
	const body = `{
		"weightG": 100, "pricePerKg": 20, "wastePct": 0,
		"timeHours": 1, "timeMinutes": 0, "watts": 1000,
		"energy": {"mode": "flat", "flatRate": 0.10},
		"failureRatePct": 0, "prepMinutes": 60, "laborRate": 10,
		"machineCost": 0, "machineLifeHours": 0,
		"monthlyFixed": 0, "monthlyHours": 0, "quantity": 1,
		"pricing": {"strategy": "markup", "markupMultiplier": 2, "shippingCharge": 0, "shippingCost": 0}
	}`
	var inputs QuoteInputs
	if err := json.Unmarshal([]byte(body), &inputs); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	bd := ComputeQuote(inputs, DefaultEngineOptions)
	p := bd.Pricing
	if p == nil {
		t.Fatal("pricing result is nil")
	}
	if !p.Valid {
		t.Errorf("valid = false, want true; warnings = %+v", p.Warnings)
	}
	if hasWarning(p.Warnings, "taxRatePct", "required") {
		t.Error("an absent taxRatePct reported a \"required\" error that validateF cannot actually enforce")
	}
	if p.Tax != 0 {
		t.Errorf("tax = %d, want 0 (an absent taxRatePct means 0)", p.Tax)
	}

	// The explicit-null case, for contrast: still a hard error.
	var nulled QuoteInputs
	if err := json.Unmarshal([]byte(strings.Replace(body, `"shippingCharge": 0`, `"shippingCharge": null`, 1)), &nulled); err != nil {
		t.Fatalf("unmarshal null: %v", err)
	}
	nullBd := ComputeQuote(nulled, DefaultEngineOptions)
	if nullBd.Pricing.Valid {
		t.Error("an explicit null shippingCharge priced as valid; want a nan error")
	}
	if !hasWarning(nullBd.Pricing.Warnings, "shippingCharge", "nan") {
		t.Errorf("no shippingCharge/nan warning; warnings = %+v", nullBd.Pricing.Warnings)
	}
}

// An absurd magnitude must be a NAMED validation error, not a silently wrong number. Before the plausibility maxima, salePriceOverride 1e17 became 1e19 cents and overflowed int64 into a NEGATIVE
// resolvedPrice persisted with Valid true (and convertible to an invoice); weightG 1e200 x pricePerKg 1e200 overflowed to +Inf, which roundCents maps to a $0.00 material line on a "valid" quote.
func TestOutOfRangeMagnitudesAreNamedErrors(t *testing.T) {
	// markupMultiplier had no ceiling at any layer: 1e17 saturated recommendedPrice to MaxInt64 and then wrapped revenue NEGATIVE (-1000 at quantity 1000) with Valid true and no warnings.
	for _, tc := range []struct {
		name     string
		quantity float64 // 0 keeps the base quantity
		pricing  *PricingInputs
		field    string
	}{
		{"salePriceOverride", 0, func() *PricingInputs {
			p := markupPricing(2)
			p.SalePriceOverride = OptF{Value: 1e17, Present: true}
			return p
		}(), "salePriceOverride"},
		{"markupMultiplier", 1000, markupPricing(1e17), "markupMultiplier"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inputs := boundaryBaseInputs()
			if tc.quantity != 0 {
				inputs.Quantity = F(tc.quantity)
			}
			inputs.Pricing = tc.pricing
			p := ComputeQuote(inputs, DefaultEngineOptions).Pricing
			if p.Valid {
				t.Error("valid = true, want false")
			}
			if !hasWarning(p.Warnings, tc.field, "above-max") {
				t.Errorf("no %s/above-max warning; warnings = %+v", tc.field, p.Warnings)
			}
			if p.ResolvedPrice < 0 || p.Revenue < 0 {
				t.Errorf("resolvedPrice = %d, revenue = %d; an out-of-range positive amount must never wrap to a negative one", p.ResolvedPrice, p.Revenue)
			}
		})
	}

	t.Run("weightG-and-pricePerKg", func(t *testing.T) {
		inputs := boundaryBaseInputs()
		inputs.WeightG = F(1e200)
		inputs.PricePerKg = F(1e200)
		bd := ComputeQuote(inputs, DefaultEngineOptions)
		if bd.Valid {
			t.Error("valid = true, want false (a $0.00 material line from an overflowed +Inf must not read as a valid quote)")
		}
		if !hasWarning(bd.Warnings, "weightG", "above-max") {
			t.Errorf("no weightG/above-max warning; warnings = %+v", bd.Warnings)
		}
		if !hasWarning(bd.Warnings, "pricePerKg", "above-max") {
			t.Errorf("no pricePerKg/above-max warning; warnings = %+v", bd.Warnings)
		}
	})
}

// Inclusive-boundary cases the shared fixture leaves unpinned; each expected value is derived from docs/formulas.md against boundaryBaseInputs, not copied from output.
func TestBoundaryVectors(t *testing.T) {
	// Inclusive maxima accept the boundary value and reject one past it. Expected amounts are derived from boundaryBaseInputs'
	// lines: wastePct 100 doubles material (100 g x 2 x $20/kg = 400c); failureRatePct 100 makes the buffer the whole $2.10 consumables;
	// timeMinutes 59 gives 1.98333 h x 1 kW x $0.10 = 20c (60 belongs in timeHours); maxPrinterWatts 5000 is 1 h x 5 kW x $0.10 = 50c.
	inclusive := []struct {
		name        string
		mutate      func(*QuoteInputs)
		wantLine    string // line id to check, "" for none
		wantAmount  int64
		wantPerUnit int64  // 0 to skip
		rejectField string // non-empty: the case must be invalid with an above-max warning on this field
	}{
		{"wastePct-100-inclusive-max", func(q *QuoteInputs) { q.WastePct = F(100) }, "material", 400, 1410, ""},                          // 400+10+0+0+1000+0
		{"failureRatePct-90-inclusive-max", func(q *QuoteInputs) { q.FailureRatePct = F(maxFailureRatePct) }, "failure", 1890, 3100, ""}, // 200+10+0+1890 (210 x 0.9 / 0.1)+1000+0
		{"failureRatePct-90.1-rejected", func(q *QuoteInputs) { q.FailureRatePct = F(90.1) }, "", 0, 0, "failureRatePct"},
		{"failureRatePct-100-rejected", func(q *QuoteInputs) { q.FailureRatePct = F(100) }, "", 0, 0, "failureRatePct"},
		{"timeMinutes-59-inclusive-max", func(q *QuoteInputs) { q.TimeMinutes = F(59) }, "electricity", 20, 0, ""},
		{"timeMinutes-60-rejected", func(q *QuoteInputs) { q.TimeMinutes = F(60) }, "", 0, 0, "timeMinutes"},
		{"watts-5000-inclusive-max", func(q *QuoteInputs) { q.Watts = F(maxPrinterWatts) }, "electricity", 50, 0, ""},
		{"watts-5001-rejected", func(q *QuoteInputs) { q.Watts = F(maxPrinterWatts + 1) }, "", 0, 0, "watts"},
	}
	for _, tc := range inclusive {
		t.Run(tc.name, func(t *testing.T) {
			inputs := boundaryBaseInputs()
			tc.mutate(&inputs)
			bd := ComputeQuote(inputs, DefaultEngineOptions)
			if tc.rejectField != "" {
				if bd.Valid {
					t.Error("valid = true, want false")
				}
				if !hasWarning(bd.Warnings, tc.rejectField, "above-max") {
					t.Errorf("no %s/above-max warning; warnings = %+v", tc.rejectField, bd.Warnings)
				}
				return
			}
			if !bd.Valid {
				t.Errorf("valid = false, want true; warnings = %+v", bd.Warnings)
			}
			if got := lineAmount(t, bd, tc.wantLine); got != tc.wantAmount {
				t.Errorf("%s = %d, want %d", tc.wantLine, got, tc.wantAmount)
			}
			if tc.wantPerUnit != 0 && bd.PerUnitTotal != tc.wantPerUnit {
				t.Errorf("perUnitTotal = %d, want %d", bd.PerUnitTotal, tc.wantPerUnit)
			}
		})
	}

	t.Run("wastePct-raises-material-and-failure-together", func(t *testing.T) {
		// The (1 + wastePct/100) factor feeds the material line AND, through `consumables`, the failure base, which the reference grid cannot see (it forces wastePct 0).
		// At failureRatePct 50 the buffer equals the base (p / (1 - p) = 1): wastePct 0 -> consumables $2.10, failure 210c; wastePct 100 -> consumables $4.10, failure 410c.
		low := boundaryBaseInputs()
		low.FailureRatePct = F(50)
		lowBd := ComputeQuote(low, DefaultEngineOptions)

		high := boundaryBaseInputs()
		high.FailureRatePct = F(50)
		high.WastePct = F(100)
		highBd := ComputeQuote(high, DefaultEngineOptions)

		if got := lineAmount(t, lowBd, "material"); got != 200 {
			t.Errorf("wastePct 0 material = %d, want 200", got)
		}
		if got := lineAmount(t, lowBd, "failure"); got != 210 {
			t.Errorf("wastePct 0 failure = %d, want 210", got)
		}
		if got := lineAmount(t, highBd, "material"); got != 400 {
			t.Errorf("wastePct 100 material = %d, want 400", got)
		}
		if got := lineAmount(t, highBd, "failure"); got != 410 {
			t.Errorf("wastePct 100 failure = %d, want 410", got)
		}
	})

	t.Run("quantity-zero-clamps-to-one-and-warns", func(t *testing.T) {
		// pricing.go clamps qty below 1 to 1 for the math, but the below-min warning still fires. docs/formulas.md names four consumers of the clamped quantity (laborPerUnit split, batchTotal, CostBreakdown.Quantity, itemRevenue);
		// the explicit salePriceOverride below makes the fourth observable, since without it recommendedPrice on an invalid breakdown is 0 and a regression to a raw 0 quantity would be invisible.
		inputs := boundaryBaseInputs()
		inputs.Quantity = F(0)
		inputs.Pricing = &PricingInputs{
			Strategy:   "margin",
			MarginPct:  OptF{Value: 30, Present: true},
			TaxRatePct: F(0), ShippingCharge: F(0), ShippingCost: F(0),
			SalePriceOverride: OptF{Value: 5, Present: true},
		}
		bd := ComputeQuote(inputs, DefaultEngineOptions)
		if bd.Valid {
			t.Error("valid = true, want false")
		}
		if !hasWarning(bd.Warnings, "quantity", "below-min") {
			t.Errorf("no quantity/below-min warning; warnings = %+v", bd.Warnings)
		}
		if bd.Quantity != 1 {
			t.Errorf("quantity = %v, want 1 (clamped)", bd.Quantity)
		}
		if bd.PerUnitTotal != 1210 || bd.BatchTotal != 1210 {
			t.Errorf("perUnitTotal/batchTotal = %d/%d, want 1210/1210", bd.PerUnitTotal, bd.BatchTotal)
		}
		p := bd.Pricing
		if p == nil {
			t.Fatal("pricing result is nil")
		}
		// salePriceOverride $5.00 resolves on presence: 500c. itemRevenue is
		// resolvedPrice × the CLAMPED quantity (1), not the raw input (0).
		if p.ResolvedPrice != 500 {
			t.Errorf("resolvedPrice = %d, want 500", p.ResolvedPrice)
		}
		if p.Revenue != p.ResolvedPrice*1 {
			t.Errorf("revenue = %d, want %d (resolvedPrice × the clamped quantity, not × the raw 0)", p.Revenue, p.ResolvedPrice*1)
		}
	})

	t.Run("quantity-below-one-fractional-warns-twice", func(t *testing.T) {
		inputs := boundaryBaseInputs()
		inputs.Quantity = F(0.5)
		bd := ComputeQuote(inputs, DefaultEngineOptions)
		if !hasWarning(bd.Warnings, "quantity", "below-min") || !hasWarning(bd.Warnings, "quantity", "not-integer") {
			t.Errorf("want both below-min and not-integer on quantity; warnings = %+v", bd.Warnings)
		}
		if bd.Quantity != 1 {
			t.Errorf("quantity = %v, want 1 (clamped)", bd.Quantity)
		}
	})

	t.Run("markupMultiplier-zero-prices-zero-and-stays-valid", func(t *testing.T) {
		// A 0 multiplier is legitimate per the min-0 rule and yields a $0.00 recommended price with valid == true; pinned so the behavior is a decision, not an accident.
		inputs := boundaryBaseInputs()
		inputs.Pricing = markupPricing(0)
		p := *ComputeQuote(inputs, DefaultEngineOptions).Pricing
		if !p.Valid {
			t.Errorf("valid = false, want true; warnings = %+v", p.Warnings)
		}
		if p.RecommendedPrice != 0 || p.ResolvedPrice != 0 || p.Revenue != 0 {
			t.Errorf("recommended/resolved/revenue = %d/%d/%d, want 0/0/0", p.RecommendedPrice, p.ResolvedPrice, p.Revenue)
		}
		if p.NetProfit != -1210 { // revenue 0 - batchTotal 1210
			t.Errorf("netProfit = %d, want -1210", p.NetProfit)
		}
		if p.AchievedMarginPct != 0 {
			t.Errorf("achievedMarginPct = %v, want 0 (revenue is not > 0)", p.AchievedMarginPct)
		}
	})

	t.Run("negative-perUnitTotal-cannot-be-priced", func(t *testing.T) {
		// The < 0 counterpart of pricing-allzero-cost-invalid, reachable from a negative weightG: material = -1000 g x $20/kg / 1000 = -2000c, failure floored to 0, so perUnitTotal = -2000 + 10 + 1000 = -990.
		inputs := boundaryBaseInputs()
		inputs.WeightG = F(-1000)
		inputs.Pricing = &PricingInputs{
			Strategy:   "margin",
			MarginPct:  OptF{Value: 30, Present: true},
			TaxRatePct: F(0), ShippingCharge: F(0), ShippingCost: F(0),
		}
		bd := ComputeQuote(inputs, DefaultEngineOptions)
		if bd.PerUnitTotal != -990 {
			t.Errorf("perUnitTotal = %d, want -990", bd.PerUnitTotal)
		}
		p := *bd.Pricing
		if p.Valid {
			t.Error("pricing valid = true, want false")
		}
		if !hasWarning(p.Warnings, "perUnitTotal", "below-min") {
			t.Errorf("no perUnitTotal/below-min warning; warnings = %+v", p.Warnings)
		}
		if p.RecommendedPrice != 0 {
			t.Errorf("recommendedPrice = %d, want 0", p.RecommendedPrice)
		}
	})

	t.Run("feeSchedule-mixed-with-nan-fixed-amount", func(t *testing.T) {
		// pricing-feeSchedule-nan-pct-invalid covers a NaN pct; this is the
		// NaN *fixed* amount, alongside a healthy fixed line and a percent
		// line, so the bad entry is not the only thing in the schedule.
		inputs := boundaryBaseInputs()
		inputs.Pricing = markupPricing(2)
		inputs.Pricing.FeeSchedule = &PricingFeeSchedule{
			Fixed: []PricingFeeFixed{
				{Amount: F(math.NaN()), Label: "bad fixed"},
				{Amount: F(0.5), Label: "good fixed"},
			},
			Percent: []PricingFeePercent{{Pct: F(3), Base: "revenue", Label: "pct"}},
		}
		p := *ComputeQuote(inputs, DefaultEngineOptions).Pricing
		if p.Valid {
			t.Error("valid = true, want false")
		}
		if !hasWarning(p.Warnings, "feeSchedule.fixed[0].amount", "nan") {
			t.Errorf("no feeSchedule.fixed[0].amount/nan warning; warnings = %+v", p.Warnings)
		}
		if len(p.FeeLines) != 3 {
			t.Fatalf("feeLines count = %d, want 3 (the bad line still appears, at 0c)", len(p.FeeLines))
		}
		if p.FeeLines[0].Amount != 0 {
			t.Errorf("NaN fixed fee line = %d, want 0", p.FeeLines[0].Amount)
		}
	})
}

// The invariant behind every plausibility maximum: a breakdown reporting Valid may be wrong about the MAGNITUDE of an absurd quote (it saturates at maxCents) but never about the DIRECTION.
// A negative total on a valid quote was persisted to batch_total_cents/net_profit_cents and could be invoiced. Rejecting the input with a NAMED field warning is also acceptable.
func assertTotalsCannotWrap(t *testing.T, label string, bd CostBreakdown) {
	t.Helper()
	if !bd.Valid {
		if len(bd.Warnings) == 0 {
			t.Errorf("%s: valid = false with an EMPTY warnings slice -- an invalid quote must name the field that made it invalid", label)
		}
		return
	}
	if bd.PerUnitTotal < 0 {
		t.Errorf("%s: perUnitTotal = %d with valid = true; a positive cost must never wrap to a negative total", label, bd.PerUnitTotal)
	}
	if bd.BatchTotal < 0 {
		t.Errorf("%s: batchTotal = %d with valid = true; a positive cost must never wrap to a negative total", label, bd.BatchTotal)
	}
	// qty >= 1 on any valid breakdown, so the batch can never be cheaper
	// than one unit. This is the assertion that actually catches a wrap: a
	// wrapped batchTotal is enormously *smaller* than perUnitTotal.
	if bd.BatchTotal < bd.PerUnitTotal {
		t.Errorf("%s: batchTotal = %d < perUnitTotal = %d with valid = true", label, bd.BatchTotal, bd.PerUnitTotal)
	}
	if p := bd.Pricing; p != nil && p.Valid {
		if p.Revenue < 0 {
			t.Errorf("%s: revenue = %d with pricing valid = true", label, p.Revenue)
		}
		if p.ResolvedPrice < 0 || p.RecommendedPrice < 0 {
			t.Errorf("%s: recommended/resolved price = %d/%d with pricing valid = true", label, p.RecommendedPrice, p.ResolvedPrice)
		}
	}
}

// The per-field maxima bound each input but not the PRODUCTS and SUMS of several, and a money product computed with bare `+`, `*` and int64() would wrap.
// The repro: monthlyFixed 1e9 / monthlyHours 1 x timeHours 1e5 = 1e16 cents of overhead on ONE line; x quantity 1e5 = 1e21 cents of batchTotal, which wrapped to -9223372036854775808.
// Not asserted: that the maxima keep products in range (they do not, and no monthlyHours bound fixes it). Saturation is the guarantee (see roundCents' MAGNITUDE note and addCents/mulCents).
func TestMaximalInputsCannotOverflowTotals(t *testing.T) {
	withMarginPricing := func(in QuoteInputs) QuoteInputs {
		in.Pricing = &PricingInputs{
			Strategy:   "margin",
			MarginPct:  OptF{Value: 30, Present: true},
			TaxRatePct: F(maxPercentAmount), ShippingCharge: F(maxMoneyAmount), ShippingCost: F(maxMoneyAmount),
			FeeSchedule: &PricingFeeSchedule{
				Fixed:   []PricingFeeFixed{{Amount: F(maxMoneyAmount), Label: "fixed"}},
				Percent: []PricingFeePercent{{Pct: F(maxPercentAmount), Base: "revenue", Label: "pct"}},
			},
		}
		return in
	}

	// Each mutator pushes ONE cost input to its own inclusive maximum (or,
	// for the two denominators, to the smallest positive value that still
	// means "set" -- which is where the division blows up, not at the max).
	maxima := []struct {
		name   string
		mutate func(*QuoteInputs)
	}{
		{"weightG", func(q *QuoteInputs) { q.WeightG = F(maxWeightG) }},
		{"pricePerKg", func(q *QuoteInputs) { q.PricePerKg = F(maxMoneyAmount) }},
		{"wastePct", func(q *QuoteInputs) { q.WastePct = F(100) }},
		{"timeHours", func(q *QuoteInputs) { q.TimeHours = F(maxHoursInput) }},
		{"timeMinutes", func(q *QuoteInputs) { q.TimeMinutes = F(59) }},
		{"watts", func(q *QuoteInputs) { q.Watts = F(maxPrinterWatts) }},
		{"failureRatePct", func(q *QuoteInputs) { q.FailureRatePct = F(maxPercentAmount) }},
		{"prepMinutes", func(q *QuoteInputs) { q.PrepMinutes = F(maxMinutesInput) }},
		{"laborRate", func(q *QuoteInputs) { q.LaborRate = F(maxMoneyAmount) }},
		{"machineCost-over-minimal-life", func(q *QuoteInputs) {
			q.MachineCost = F(maxMoneyAmount)
			q.MachineLifeHours = F(1)
		}},
		{"monthlyFixed-over-minimal-hours", func(q *QuoteInputs) {
			q.MonthlyFixed = F(maxMoneyAmount)
			q.MonthlyHours = F(1)
		}},
		{"quantity", func(q *QuoteInputs) { q.Quantity = F(maxQuantity) }},
		{"hardware-at-catalog-limit", func(q *QuoteInputs) {
			for range maxConfigCollectionSize {
				q.Hardware = append(q.Hardware, HardwareLine{
					Name: "max", UnitCost: F(maxMoneyAmount), QtyPerUnit: F(maxQuantity), LostOnFailure: true,
				})
			}
		}},
	}

	for _, m := range maxima {
		t.Run("one-at-a-time/"+m.name, func(t *testing.T) {
			inputs := boundaryBaseInputs()
			m.mutate(&inputs)
			assertTotalsCannotWrap(t, m.name, ComputeQuote(inputs, DefaultEngineOptions))
			assertTotalsCannotWrap(t, m.name+"/priced", ComputeQuote(withMarginPricing(inputs), DefaultEngineOptions))
		})
	}

	t.Run("all-at-once", func(t *testing.T) {
		inputs := boundaryBaseInputs()
		for _, m := range maxima {
			m.mutate(&inputs)
		}
		assertTotalsCannotWrap(t, "all", ComputeQuote(inputs, DefaultEngineOptions))
		assertTotalsCannotWrap(t, "all/priced", ComputeQuote(withMarginPricing(inputs), DefaultEngineOptions))
	})

	t.Run("documented-repro-stays-valid-and-non-negative", func(t *testing.T) {
		// This exact input combination, asserted as a concrete pin rather
		// than only through the shared invariant: every field is in range,
		// so the breakdown IS valid, and the totals must saturate upward.
		inputs := boundaryBaseInputs()
		inputs.MonthlyFixed = F(maxMoneyAmount)
		inputs.MonthlyHours = F(1)
		inputs.TimeHours = F(maxHoursInput)
		inputs.Quantity = F(maxQuantity)
		inputs.Pricing = &PricingInputs{
			Strategy:   "margin",
			MarginPct:  OptF{Value: 30, Present: true},
			TaxRatePct: F(0), ShippingCharge: F(0), ShippingCost: F(0),
		}
		bd := ComputeQuote(inputs, DefaultEngineOptions)
		if !bd.Valid {
			t.Fatalf("valid = false; every input here is at or below its own documented maximum. warnings = %+v", bd.Warnings)
		}
		assertTotalsCannotWrap(t, "repro", bd)
		// overhead = 1e5 h × $1e9/h = 1e14 dollars = 1e16 cents, so the batch
		// product is ~1e21 and must land on the clamp, not on a wrapped value.
		if bd.BatchTotal != maxCents {
			t.Errorf("batchTotal = %d, want %d (saturated, not wrapped)", bd.BatchTotal, maxCents)
		}
		if bd.Pricing.NetProfit > 0 {
			t.Errorf("netProfit = %d; a saturated revenue minus a saturated batch cost must not read as profit", bd.Pricing.NetProfit)
		}
		if bd.Pricing.AchievedMarginPct < -100 || bd.Pricing.AchievedMarginPct > 100 {
			t.Errorf("achievedMarginPct = %v, outside [-100,100]", bd.Pricing.AchievedMarginPct)
		}
	})
}

// addCents/subCents/mulCents are exact for everything that fits and clamped (never wrapped) otherwise; the exact half guarantees the golden vectors did not move.
func TestSaturatingCentsArithmetic(t *testing.T) {
	const min64 = math.MinInt64
	for _, c := range []struct {
		name string
		got  int64
		want int64
	}{
		{"add-exact", addCents(1451, 4), 1455},
		{"add-negative-exact", addCents(-990, 10), -980},
		{"add-both-negative-exact", addCents(-990, -10), -1000},
		{"add-saturates-positive", addCents(maxCents, 1), maxCents},
		{"add-saturates-negative", addCents(-maxCents, -2), -maxCents},
		{"add-at-limit-is-exact", addCents(maxCents-1, 1), maxCents},
		{"sub-exact", subCents(2073, 1451), 622},
		{"sub-into-negative-is-exact", subCents(0, 1210), -1210},
		{"sub-saturates-positive", subCents(maxCents, -1), maxCents},
		{"sub-saturates-negative", subCents(-maxCents, 1), -maxCents},
		{"sub-min-int64-saturates", subCents(0, min64), maxCents},
		{"mul-exact", mulCents(701, 4), 2804},
		{"mul-by-zero", mulCents(maxCents, 0), 0},
		{"mul-negative-exact", mulCents(-500, 3), -1500},
		{"mul-saturates-positive", mulCents(1<<40, 1<<40), maxCents},
		{"mul-saturates-negative", mulCents(-(1 << 40), 1<<40), -maxCents},
		{"mul-min-int64-saturates", mulCents(min64, -1), maxCents},
	} {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
}

// Pins the array-length bound at both layers. Hardware entries were bounded per entry (one legal line reaches ~1e16 cents) but not in length: 930 maxed entries (~51 KB, inside the 64 KiB body cap) summed perUnitTotal to -9146744073709551616 with valid true.
func TestHardwareArrayLengthIsBounded(t *testing.T) {
	maxedHardware := func(n int) []HardwareLine {
		out := make([]HardwareLine, 0, n)
		for i := range n {
			out = append(out, HardwareLine{
				Name: fmt.Sprintf("hw-%d", i), UnitCost: F(maxMoneyAmount), QtyPerUnit: F(maxQuantity),
			})
		}
		return out
	}

	t.Run("at-the-limit-is-accepted", func(t *testing.T) {
		inputs := boundaryBaseInputs()
		inputs.Hardware = maxedHardware(maxConfigCollectionSize)
		bd := ComputeQuote(inputs, DefaultEngineOptions)
		if !bd.Valid {
			t.Errorf("valid = false at exactly %d entries, want true (the bound is inclusive); warnings = %+v", maxConfigCollectionSize, bd.Warnings)
		}
		assertTotalsCannotWrap(t, "at-limit", bd)
	})

	t.Run("above-the-limit-is-a-named-error", func(t *testing.T) {
		inputs := boundaryBaseInputs()
		inputs.Hardware = maxedHardware(maxConfigCollectionSize + 1)
		bd := ComputeQuote(inputs, DefaultEngineOptions)
		if bd.Valid {
			t.Error("valid = true, want false")
		}
		w, ok := warningFor(bd.Warnings, "hardware", "above-max")
		if !ok {
			t.Fatalf("no hardware/above-max warning; warnings = %+v", bd.Warnings)
		}
		if !strings.Contains(w.Message, "200") {
			t.Errorf("message %q does not name the bound", w.Message)
		}
		if w.Bound == nil || *w.Bound != maxConfigCollectionSize {
			t.Errorf("Bound = %v, want %d (format.ts rebuilds the operator-facing copy from it)", w.Bound, maxConfigCollectionSize)
		}
	})

	// The handler-level half: the wire route, with a body that really does
	// fit inside quotesMaxBytes, must not answer 200 with a negative total.
	t.Run("compute-handler-rejects-950-maxed-entries", func(t *testing.T) {
		// Deliberately the most compact legal encoding of a maxed entry, so
		// the body stays inside quotesMaxBytes: the whole point of this case
		// is that the 64 KiB cap is NOT what stops it.
		entry := `{"name":"h","unitCost":1e9,"qtyPerUnit":100000}`
		hw := strings.Repeat(entry+",", 949) + entry
		inputs := strings.Replace(validQuoteInputsJSON, `"quantity":1}`, `"quantity":1,"hardware":[`+hw+"]}", 1)
		if len(inputs) >= quotesMaxBytes {
			t.Fatalf("test setup: body is %d bytes, at or over the %d cap -- the point is that it fits", len(inputs), quotesMaxBytes)
		}

		mux := newTestMux(t, t.TempDir())
		rec := postCompute(t, mux, `{"inputs":`+inputs+`,"options":{}}`)
		if rec.Code >= 400 && rec.Code < 500 {
			return // a named 4xx is the other acceptable answer
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 or a 4xx; body = %s", rec.Code, rec.Body.String())
		}
		var ob OrderBreakdown
		if err := json.Unmarshal(rec.Body.Bytes(), &ob); err != nil {
			t.Fatalf("unmarshal breakdown: %v", err)
		}
		bd := ob.Parts[0].Breakdown
		if bd.Valid {
			t.Errorf("valid = true for %d hardware entries, want false", 950)
		}
		if !hasWarning(bd.Warnings, "hardware", "above-max") {
			t.Errorf("no hardware/above-max warning in the response; warnings = %+v", bd.Warnings)
		}
		if bd.PerUnitTotal < 0 || bd.BatchTotal < 0 {
			t.Errorf("perUnitTotal/batchTotal = %d/%d; neither may be negative", bd.PerUnitTotal, bd.BatchTotal)
		}
	})
}

// OptF marshals so the demo seed can build engine inputs directly: a number as a number, NaN as null (which
// decodes back to NaN), and an absent field is omitted by its omitzero tag.
func TestOptFMarshalRoundTrips(t *testing.T) {
	type holder struct {
		A OptF `json:"a,omitzero"`
		B OptF `json:"b,omitzero"`
		C OptF `json:"c,omitzero"`
	}
	raw, err := json.Marshal(holder{A: OptF{Value: 1.5, Present: true}, B: OptF{Value: math.NaN(), Present: true}})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"a":1.5,"b":null}` {
		t.Fatalf("marshaled = %s, want {\"a\":1.5,\"b\":null}", raw)
	}
	var back holder
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if !back.A.Present || back.A.Value != 1.5 || !back.B.Present || !math.IsNaN(back.B.Value) || back.C.Present {
		t.Fatalf("round trip = %+v", back)
	}
	if _, err := json.Marshal(OptF{}); err == nil {
		t.Fatal("marshaling an absent OptF directly should fail")
	}
}

// A valid quote whose net profit is negative (margin is measured before fees and tax, or the override is
// low) stays Valid but carries a warning-severity net-profit-negative field warning. Values derive from the
// pricing-negative-net-profit-valid fixture vector: price 1681c, net -44c.
func TestNetProfitNegativeWarning(t *testing.T) {
	inputs := boundaryBaseInputs()
	inputs.Pricing = &PricingInputs{
		Strategy: "margin", MarginPct: OptF{Value: 10, Present: true}, TaxRatePct: F(25),
		FeeSchedule: &PricingFeeSchedule{
			Fixed:   []PricingFeeFixed{{Amount: 0.2, Label: "listing"}},
			Percent: []PricingFeePercent{{Pct: 6.5, Base: "revenue", Label: "transaction"}, {Pct: 3, Base: "revenue", Label: "payment"}},
		},
	}
	p := ComputeQuote(inputs, DefaultEngineOptions).Pricing
	if p.NetProfit != -44 || !p.Valid {
		t.Fatalf("netProfit/valid = %d/%v, want -44/true", p.NetProfit, p.Valid)
	}
	w, ok := warningFor(p.Warnings, "netProfit", "net-profit-negative")
	if !ok || w.Severity != "warning" {
		t.Fatalf("want a warning-severity netProfit/net-profit-negative warning, got %+v", p.Warnings)
	}

	inputs.Pricing.MarginPct = OptF{Value: 25, Present: true}
	p = ComputeQuote(inputs, DefaultEngineOptions).Pricing
	if p.NetProfit < 0 || hasWarning(p.Warnings, "netProfit", "net-profit-negative") {
		t.Errorf("positive net profit %d must not warn: %+v", p.NetProfit, p.Warnings)
	}
}

// The tax carved out of the grossed-up recommended price equals the pre-tax price x t / 100, so the margin
// the operator picked is what remains before fees.
func TestRecommendedPriceTaxCarveOut(t *testing.T) {
	for _, rate := range []float64{5, 8.25, 20, 25, 100} {
		inputs := boundaryBaseInputs() // cost 1210c
		inputs.Pricing = &PricingInputs{Strategy: "margin", MarginPct: OptF{Value: 20, Present: true}, TaxRatePct: F(rate)}
		p := ComputeQuote(inputs, DefaultEngineOptions).Pricing
		exTax := 1210 / 0.8
		if want := roundCents(exTax * (1 + rate/100)); p.RecommendedPrice != want {
			t.Errorf("rate %v: recommended = %d, want %d", rate, p.RecommendedPrice, want)
		}
		wantTax := exTax * rate / 100
		if d := math.Abs(float64(p.Tax) - wantTax); d > 1 {
			t.Errorf("rate %v: tax = %d, want about %.2f (pre-tax price x t / 100)", rate, p.Tax, wantTax)
		}
	}
}

// +Inf (like zero, negative, and NaN) is clamped to 1 for the math; it is also rejected as not finite.
func TestQuantityInfinityIsClamped(t *testing.T) {
	inputs := boundaryBaseInputs()
	inputs.Quantity = F(math.Inf(1))
	bd := ComputeQuote(inputs, DefaultEngineOptions)
	if bd.Quantity != 1 || bd.BatchTotal != 1210 || bd.PerUnitTotal != 1210 {
		t.Errorf("quantity/batch/perUnit = %v/%d/%d, want 1/1210/1210", bd.Quantity, bd.BatchTotal, bd.PerUnitTotal)
	}
	if bd.Valid || !hasWarning(bd.Warnings, "quantity", "nan") {
		t.Errorf("want invalid with quantity/nan, got valid=%v %+v", bd.Valid, bd.Warnings)
	}
}

// divRoundCents rounds half away from zero in integers for a whole quantity, exactly even above 2^53.
func TestDivRoundCents(t *testing.T) {
	cases := []struct {
		cents int64
		qty   float64
		want  int64
	}{
		{4000, 10000, 0}, {5000, 10000, 1}, {182, 4, 46}, {181, 4, 45}, {-182, 4, -46}, {-181, 4, -45},
		{2000000000000001210, 1, 2000000000000001210}, {1210, 2.5, 484}, {7, 3, 2},
	}
	for _, c := range cases {
		if got := divRoundCents(c.cents, c.qty); got != c.want {
			t.Errorf("divRoundCents(%d, %v) = %d, want %d", c.cents, c.qty, got, c.want)
		}
	}
}

// Batch labor of $1.00 at quantity 3 is 100c, not 3 x 33c: each line is rounded once at the batch.
func TestBatchLaborRoundsOnceAtBatch(t *testing.T) {
	inputs := boundaryBaseInputs()
	inputs.WeightG, inputs.TimeHours = F(0), F(0)
	inputs.PrepMinutes, inputs.LaborRate, inputs.Quantity = F(6), F(10), F(3) // $1.00 per batch
	bd := ComputeQuote(inputs, DefaultEngineOptions)
	if bd.BatchTotal != 100 {
		t.Errorf("batchTotal = %d, want 100", bd.BatchTotal)
	}
	if got := lineAmount(t, bd, "labor"); got != 33 {
		t.Errorf("per-unit labor = %d, want 33 (round(100 / 3))", got)
	}
}
