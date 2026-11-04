package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

// pricing.go is the sole quote pricing implementation (web/src/lib/engine/quote.ts holds only the request and
// response types), pinned by testdata/quote-vectors.json. roundCents is not roundHalfUp. Each cost line is
// extended to the batch in float and rounded once; the per-unit figure is round(batch / qty), and perUnitTotal
// is round(batchTotal / qty). Cost validity cascades one way into pricing.

// F is a JSON-decodable float64 for always-present fields that can carry NaN (as `null`, which is what a
// browser's JSON.stringify(NaN) emits); OptF is for fields that can be absent. An omitted F key validates as 0 (so fieldRule.required is OptF-only);
// fine, as 0 equals omission for every F field. An explicit `null` decodes to NaN and errors.
type F float64

func (f *F) UnmarshalJSON(b []byte) error {
	// "null" is accepted too: a browser's JSON.stringify(NaN) emits null, so a live blank required field
	// arrives as null. Otherwise Unmarshal("null", &v) is a no-op on a float64 and the blank would price as
	// a real zero.
	if string(b) == "null" {
		*f = F(math.NaN())
		return nil
	}
	var v float64
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*f = F(v)
	return nil
}

// OptF is an optional numeric input distinguishing three JSON states: key absent -> Present=false (only
// `required` fires); `null` -> Present=true, Value=NaN (always a "nan" error); a number -> Present=true.
// A *float64 can't tell absent from null; a non-pointer json.Unmarshaler struct avoids the null shortcut.
type OptF struct {
	Value   float64
	Present bool
}

func (o *OptF) UnmarshalJSON(b []byte) error {
	o.Present = true
	if string(b) == "null" {
		o.Value = math.NaN()
		return nil
	}
	return json.Unmarshal(b, &o.Value)
}

// MarshalJSON writes the number, or null for NaN (what a browser's JSON.stringify(NaN) emits and
// UnmarshalJSON reads back as NaN). An absent value has no JSON form; the OptF fields tag `omitzero` so the
// encoder never asks for one, and marshaling one directly is a bug.
func (o OptF) MarshalJSON() ([]byte, error) {
	if !o.Present {
		return nil, errors.New("OptF: absent value has no JSON form; tag the field omitzero")
	}
	if math.IsNaN(o.Value) {
		return []byte("null"), nil
	}
	return json.Marshal(o.Value)
}

// --- money rounding ----------------------------------------------------

// roundCents rounds half away from zero, 1e-9 epsilon fixing IEEE754 ties (1.005*100 is
// 100.49999999999999); non-finite rounds to 0. int64(x) beyond int64's range is implementation-defined
// (amd64: MinInt64), so a huge positive price would go negative; it saturates at +-maxCents.
func roundCents(v float64) int64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	sign := 1.0
	if v < 0 {
		sign = -1.0
	}
	mag := math.Floor(math.Abs(v) + 0.5 + 1e-9)
	if mag >= maxCentsMagnitude {
		return int64(sign) * maxCents
	}
	return int64(sign * mag)
}

// ceilCents rounds up to the next whole cent. An absolute 1e-9 plus a relative 1e-12 epsilon keeps a float that
// is an exact cent amount plus IEEE754 noise (200.00000000000003, 223999.9999999998 for 224 / (1 - 0.999)) at
// that cent. It prices the recommended
// price: rounding up keeps the realized margin or markup at or above the target. Non-finite is 0, and the
// result saturates like roundCents.
func ceilCents(v float64) int64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	c := math.Ceil(v - 1e-9 - math.Abs(v)*1e-12)
	if c >= maxCentsMagnitude {
		return maxCents
	}
	if c <= -maxCentsMagnitude {
		return -maxCents
	}
	return int64(c)
}

// maxCents/maxCentsMagnitude bound roundCents' output; maxCentsMagnitude is exactly 2^63, since
// float64(math.MaxInt64) rounds up to it, which makes the guard exact.
const (
	maxCents                  = int64(math.MaxInt64)
	maxCentsMagnitude float64 = 1 << 63
)

// toCents converts a float amount to integer cents via roundCents.
func toCents(amount float64) int64 {
	return roundCents(amount * 100)
}

// addCents/subCents/mulCents are saturating int64 money arithmetic: equal to the bare operators in range,
// clamped to +-maxCents instead of wrapping, so a huge positive total can't turn negative on a Valid quote.
// The input maxima bound inputs, not their products and sums.
func addCents(a, b int64) int64 {
	sum := a + b
	// Overflow iff both addends share a sign and the sum does not.
	if a > 0 && b > 0 && sum < 0 {
		return maxCents
	}
	if a < 0 && b < 0 && sum >= 0 {
		return -maxCents
	}
	// math.MinInt64 is the one value outside the +-maxCents band; normalizing it keeps the clamp sticky.
	if sum == math.MinInt64 {
		return -maxCents
	}
	return sum
}

func subCents(a, b int64) int64 {
	// -math.MinInt64 isn't representable, so negating b first would wrap. No caller produces it, but name it.
	if b == math.MinInt64 {
		return maxCents
	}
	return addCents(a, -b)
}

func mulCents(a, b int64) int64 {
	if a == 0 || b == 0 {
		return 0
	}
	negative := (a < 0) != (b < 0)
	saturated := maxCents
	if negative {
		saturated = -maxCents
	}
	// MinInt64 x -1 wraps to MinInt64, which the division check below would read as exact; excluded as in
	// subCents.
	if a == math.MinInt64 || b == math.MinInt64 {
		return saturated
	}
	p := a * b
	if p/b != a {
		return saturated
	}
	return p
}

// jsRound ports JS's Math.round (floor(x+0.5): ties go toward +Infinity). Display-only, for round2.
func jsRound(n float64) float64 {
	return math.Floor(n + 0.5)
}

// round2 rounds to two decimals for the formula/inputs strings on each LineItem; never used for cents.
func round2(n float64) float64 {
	return jsRound(n*100) / 100
}

// formatf renders a number as JS's string coercion would (50, 1.3333333333333333). Display-only, best effort.
func formatf[T ~float64](v T) string {
	return strconv.FormatFloat(float64(v), 'g', -1, 64)
}

// --- electricity (effectiveRate) ---------------------------------------

// effectiveRate resolves the electricity rate. Flat mode returns the flat rate (0 if absent); TOU mode blends
// rates by offPeakFrac clamped to [0, 1]. Go's math.Max/Min propagate NaN like JS's, so a NaN fraction
// poisons the rate.
func effectiveRate(e EnergyInputs) float64 {
	if e.Mode == "flat" {
		if !e.FlatRate.Present {
			return 0
		}
		return e.FlatRate.Value
	}
	frac := 0.0
	if e.OffPeakFrac.Present {
		frac = e.OffPeakFrac.Value
	}
	clamped := math.Min(1, math.Max(0, frac))
	peak := 0.0
	if e.PeakRate.Present {
		peak = e.PeakRate.Value
	}
	offPeak := 0.0
	if e.OffPeakRate.Present {
		offPeak = e.OffPeakRate.Value
	}
	return clamped*offPeak + (1-clamped)*peak
}

// --- field validation ----------------------------------------------------

type FieldWarning struct {
	Field    string   `json:"field"`
	Code     string   `json:"code"`
	Message  string   `json:"message"`
	Severity string   `json:"severity"`
	Bound    *float64 `json:"bound,omitempty"`
}

type fieldRule struct {
	min *float64
	max *float64
	// required is meaningful only for an OptF (validateOpt), where it fires when the JSON key was absent.
	required bool
	integer  bool
}

//go:fix inline
func fmin(v float64) *float64 { return new(v) }

// formatBound renders a bound as validate.ts's template literal does, so warning copy agrees verbatim:
// plain decimal where Go's %v prints 1e9 as "1e+09". Every bound is below JS's 1e21 exponential threshold.
func formatBound(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// validateF validates an always-present F: non-finite is always a "nan"
// error, then min/max/integer checks. The copy names the bound and direction (`must be >= min`), since raw
// consumers (export, logs, API clients) see this text.
func validateF(field string, value F, rule fieldRule) []FieldWarning {
	v := float64(value)
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return []FieldWarning{{Field: field, Code: "nan", Message: field + " is not a finite number", Severity: "error"}}
	}
	warnings := []FieldWarning{}
	if rule.min != nil && v < *rule.min {
		warnings = append(warnings, FieldWarning{Field: field, Code: "below-min", Message: field + " must be ≥ " + formatBound(*rule.min), Severity: "error", Bound: rule.min})
	}
	if rule.max != nil && v > *rule.max {
		warnings = append(warnings, FieldWarning{Field: field, Code: "above-max", Message: field + " must be ≤ " + formatBound(*rule.max), Severity: "error", Bound: rule.max})
	}
	if rule.integer && v != math.Trunc(v) {
		warnings = append(warnings, FieldWarning{Field: field, Code: "not-integer", Message: field + " must be a whole number", Severity: "error"})
	}
	return warnings
}

// missingTimeMessage is the copy for a print time that was left blank. A blank field reaches the server as
// null (what a browser's JSON.stringify(NaN) emits), which F decodes to NaN, so "missing" is the only
// non-finite state a print time can arrive in over the wire.
const missingTimeMessage = "Enter the print time"

// validateTime is validateF for the print-time halves: a blank (null/NaN) time is a "required" error reading
// "Enter the print time" instead of the generic non-finite "nan" copy; Inf and every range check are
// unchanged.
func validateTime(field string, value F, rule fieldRule) []FieldWarning {
	if math.IsNaN(float64(value)) {
		return []FieldWarning{{Field: field, Code: "required", Message: missingTimeMessage, Severity: "error"}}
	}
	return validateF(field, value, rule)
}

// validateEnum guards a string field that must be one of a set: an unknown value would silently take the
// default branch (a "Margin" quote priced by markup yet labeled "Priced by Margin"). Code "invalid-enum"
// is outside validate.ts's FieldCode union; format.ts falls back to the raw message.
func validateEnum(field, value string, allowed ...string) []FieldWarning {
	if slices.Contains(allowed, value) {
		return []FieldWarning{}
	}
	return []FieldWarning{{
		Field:    field,
		Code:     "invalid-enum",
		Message:  field + " must be one of: " + strings.Join(allowed, ", "),
		Severity: "error",
	}}
}

// validateOpt validates an optional OptF: absent -> only `required` fires; present ->
// validateF, so a present NaN always fires "nan".
func validateOpt(field string, value OptF, rule fieldRule) []FieldWarning {
	if !value.Present {
		if rule.required {
			return []FieldWarning{{Field: field, Code: "required", Message: field + " is required", Severity: "error"}}
		}
		return []FieldWarning{}
	}
	return validateF(field, F(value.Value), rule)
}

func hasErrors(warnings []FieldWarning) bool {
	for _, w := range warnings {
		if w.Severity == "error" {
			return true
		}
	}
	return false
}

func pickWarnings(warnings []FieldWarning, fields ...string) []FieldWarning {
	out := []FieldWarning{}
	for _, w := range warnings {
		if slices.Contains(fields, w.Field) {
			out = append(out, w)
		}
	}
	return out
}

// --- quote computation -----------------------------------------------

type EnergyInputs struct {
	Mode        string `json:"mode"`
	FlatRate    OptF   `json:"flatRate,omitzero"`
	PeakRate    OptF   `json:"peakRate,omitzero"`
	OffPeakRate OptF   `json:"offPeakRate,omitzero"`
	OffPeakFrac OptF   `json:"offPeakFrac,omitzero"`
	// Schedule mode carries a snapshot of the tariff, the time zone it is read in, and the print's resolved
	// start as wall-clock "YYYY-MM-DDTHH:MM" in that zone, so a saved quote reprices identically after the
	// shop's settings change.
	Schedule *EnergySchedule `json:"schedule,omitempty"`
	Timezone string          `json:"timezone,omitempty"`
	Start    string          `json:"start,omitempty"`
}

// PricingFee* is the fee-schedule input shape of PricingInputs.FeeSchedule. It doesn't reuse config.go's
// types: those use plain float64 (no NaN-capable decoding) and represent a named saved schedule.
type PricingFeeFixed struct {
	Amount F      `json:"amount"`
	Label  string `json:"label"`
}

type PricingFeePercent struct {
	Pct   F      `json:"pct"`
	Base  string `json:"base"`
	Label string `json:"label"`
}

type PricingFeeSchedule struct {
	Source  *string             `json:"source,omitempty"`
	Fixed   []PricingFeeFixed   `json:"fixed"`
	Percent []PricingFeePercent `json:"percent"`
}

type PricingInputs struct {
	Strategy          string              `json:"strategy"`
	MarginPct         OptF                `json:"marginPct,omitzero"`
	MarkupMultiplier  OptF                `json:"markupMultiplier,omitzero"`
	FeeSchedule       *PricingFeeSchedule `json:"feeSchedule,omitempty"`
	FeeScheduleName   *string             `json:"feeScheduleName,omitempty"`
	TaxRatePct        F                   `json:"taxRatePct"`
	ShippingCharge    F                   `json:"shippingCharge"`
	ShippingCost      F                   `json:"shippingCost"`
	SalePriceOverride OptF                `json:"salePriceOverride,omitzero"`
}

type FeeLine struct {
	Label  string `json:"label"`
	Amount int64  `json:"amount"`
}

type PricingResult struct {
	Strategy          string         `json:"strategy"`
	RecommendedPrice  int64          `json:"recommendedPrice"`
	ResolvedPrice     int64          `json:"resolvedPrice"`
	Revenue           int64          `json:"revenue"`
	Fees              int64          `json:"fees"`
	FeeLines          []FeeLine      `json:"feeLines"`
	Tax               int64          `json:"tax"`
	ShippingCharge    int64          `json:"shippingCharge"`
	ShippingCost      int64          `json:"shippingCost"`
	NetProfit         int64          `json:"netProfit"`
	AchievedMarginPct float64        `json:"achievedMarginPct"`
	FeeScheduleName   *string        `json:"feeScheduleName,omitempty"`
	Warnings          []FieldWarning `json:"warnings"`
	Valid             bool           `json:"valid"`
}

// HardwareLine is one attached hardware entry's resolved values on a quote (QuoteInputs.Hardware), not a live
// reference into the config catalog.
type HardwareLine struct {
	Name          string `json:"name"`
	UnitCost      F      `json:"unitCost"`
	QtyPerUnit    F      `json:"qtyPerUnit"`
	LostOnFailure bool   `json:"lostOnFailure"`
}

type QuoteInputs struct {
	WeightG          F            `json:"weightG"`
	PricePerKg       F            `json:"pricePerKg"`
	WastePct         F            `json:"wastePct"`
	TimeHours        F            `json:"timeHours"`
	TimeMinutes      F            `json:"timeMinutes"`
	Watts            F            `json:"watts"`
	Energy           EnergyInputs `json:"energy"`
	FailureRatePct   F            `json:"failureRatePct"`
	PrepMinutes      F            `json:"prepMinutes"`
	LaborRate        F            `json:"laborRate"`
	MachineCost      F            `json:"machineCost"`
	MachineLifeHours F            `json:"machineLifeHours"`
	MonthlyFixed     F            `json:"monthlyFixed"`
	MonthlyHours     F            `json:"monthlyHours"`
	Quantity         F            `json:"quantity"`
	// Hardware is optional; absent or empty, ComputeQuote must produce byte-identical output to a quote without the field.
	Hardware []HardwareLine `json:"hardware,omitempty"`
	Pricing  *PricingInputs `json:"pricing,omitempty"`
}

type EngineOptions struct {
	LaborPerUnit bool `json:"laborPerUnit"`
}

var DefaultEngineOptions = EngineOptions{LaborPerUnit: false}

type LineItem struct {
	ID       string         `json:"id"`
	Label    string         `json:"label"`
	Amount   int64          `json:"amount"`
	Formula  string         `json:"formula"`
	Inputs   map[string]any `json:"inputs"`
	Warnings []FieldWarning `json:"warnings"`
	// Periods is the electricity line's split by named tariff period (schedule mode only).
	Periods []EnergyPeriod `json:"periods,omitempty"`
}

type CostBreakdown struct {
	PerUnit      []LineItem     `json:"perUnit"`
	PerUnitTotal int64          `json:"perUnitTotal"`
	Quantity     float64        `json:"quantity"`
	BatchTotal   int64          `json:"batchTotal"`
	Pricing      *PricingResult `json:"pricing,omitempty"`
	Warnings     []FieldWarning `json:"warnings"`
	Valid        bool           `json:"valid"`
}

// maxPrinterWatts caps a printer's plausible power draw; web/src/lib/engine/electricity.ts mirrors it.
const maxPrinterWatts = 5000

// Plausibility maxima turn an absurd magnitude into a named validation error, not a silent wrong number
// (1e200 x 1e200 overflows to +Inf, which roundCents maps to a $0.00 line). They bound inputs, not products;
// the real guarantee is that every money product and sum saturates (roundCents, addCents/subCents/mulCents).
const (
	maxMoneyAmount   = 1e9
	maxWeightG       = 100000
	maxQuantity      = 100000
	maxHoursInput    = 100000
	maxMinutesInput  = maxHoursInput * 60
	maxPercentAmount = 100
)

// maxMarginPct is the single ceiling on a margin, shared by ComputePricing, config.go's validation, and web
// settingsRules.ts (hardcodes 99.9; config_test.go pins it). Relaxing one layer alone would let marginPct 99.95
// save clean and make every quote unpriceable. 99.9 keeps 1/(1 - margin) well conditioned.
const maxMarginPct = 99.9

// maxMarkupMultiplier is the single ceiling on a markup, shared like maxMarginPct (also
// config.schema.json and web settingsRules.ts, which hardcodes 1000). Without one, 1e15 at quantity 1000
// wrapped revenue negative with Valid true. 1000 bounds absurdity; it is inclusive.
const maxMarkupMultiplier = 1000

// maxFailureRatePct is the single ceiling on failureRatePct, shared with config.go, config.schema.json, and
// web settingsRules.ts. The failure buffer is base x p / (1 - p), which grows without bound as p approaches 1;
// 90 keeps it finite (9x the base) and rejects the meaningless 100.
const maxFailureRatePct = 90

// ComputeQuote computes six cost lines in pinned order (material, electricity, depreciation, failure, labor,
// overhead), one more per Hardware entry (strictly additive). Each line is extended to the batch in float and
// rounded once; batchTotal is the sum of those batch amounts, a line's per-unit amount is round(batch / qty),
// and perUnitTotal is round(batchTotal / qty). A nested PricingResult is computed if Pricing is set.
func ComputeQuote(inputs QuoteInputs, options EngineOptions) CostBreakdown {
	warnings := []FieldWarning{}
	warnings = append(warnings, validateF("weightG", inputs.WeightG, fieldRule{min: fmin(0), max: fmin(maxWeightG)})...)
	warnings = append(warnings, validateF("pricePerKg", inputs.PricePerKg, fieldRule{min: fmin(0), max: new(maxMoneyAmount)})...)
	warnings = append(warnings, validateF("wastePct", inputs.WastePct, fieldRule{min: fmin(0), max: fmin(100)})...)
	warnings = append(warnings, validateTime("timeHours", inputs.TimeHours, fieldRule{min: fmin(0), max: fmin(maxHoursInput)})...)
	warnings = append(warnings, validateTime("timeMinutes", inputs.TimeMinutes, fieldRule{min: fmin(0), max: fmin(59)})...)
	warnings = append(warnings, validateF("watts", inputs.Watts, fieldRule{min: fmin(0), max: fmin(maxPrinterWatts)})...)
	warnings = append(warnings, validateEnum("mode", inputs.Energy.Mode, "flat", "tou", "schedule")...)
	scheduleLoc := time.UTC
	switch inputs.Energy.Mode {
	case "flat":
		warnings = append(warnings, validateOpt("flatRate", inputs.Energy.FlatRate, fieldRule{min: fmin(0), required: true})...)
	case "schedule":
		var sw []FieldWarning
		scheduleLoc, sw = scheduleInputWarnings(inputs.Energy)
		warnings = append(warnings, sw...)
	default:
		warnings = append(warnings, validateOpt("peakRate", inputs.Energy.PeakRate, fieldRule{min: fmin(0), required: true})...)
		warnings = append(warnings, validateOpt("offPeakRate", inputs.Energy.OffPeakRate, fieldRule{min: fmin(0), required: true})...)
		warnings = append(warnings, validateOpt("offPeakFrac", inputs.Energy.OffPeakFrac, fieldRule{min: fmin(0), max: fmin(1), required: true})...)
	}
	warnings = append(warnings, validateF("failureRatePct", inputs.FailureRatePct, fieldRule{min: fmin(0), max: fmin(maxFailureRatePct)})...)
	warnings = append(warnings, validateF("prepMinutes", inputs.PrepMinutes, fieldRule{min: fmin(0), max: fmin(maxMinutesInput)})...)
	warnings = append(warnings, validateF("laborRate", inputs.LaborRate, fieldRule{min: fmin(0), max: new(maxMoneyAmount)})...)
	warnings = append(warnings, validateF("machineCost", inputs.MachineCost, fieldRule{min: fmin(0), max: new(maxMoneyAmount)})...)
	warnings = append(warnings, validateF("machineLifeHours", inputs.MachineLifeHours, fieldRule{min: fmin(0), max: fmin(maxHoursInput)})...)
	warnings = append(warnings, validateF("monthlyFixed", inputs.MonthlyFixed, fieldRule{min: fmin(0), max: new(maxMoneyAmount)})...)
	warnings = append(warnings, validateF("monthlyHours", inputs.MonthlyHours, fieldRule{min: fmin(0), max: fmin(maxHoursInput)})...)
	warnings = append(warnings, validateF("quantity", inputs.Quantity, fieldRule{min: fmin(1), max: fmin(maxQuantity), integer: true})...)
	// Hardware length needs its own bound: one legal entry reaches ~1e16 cents, so ~930 maxed entries (under
	// quotesMaxBytes) overflowed perUnitTotal with Valid true. config.go caps the catalog, but compute
	// routes take resolved lines. Reuses maxConfigCollectionSize; reported on the bare field "hardware".
	if len(inputs.Hardware) > maxConfigCollectionSize {
		warnings = append(warnings, FieldWarning{
			Field:    "hardware",
			Code:     "above-max",
			Message:  "hardware must be ≤ " + formatBound(maxConfigCollectionSize) + " entries",
			Severity: "error",
			Bound:    fmin(maxConfigCollectionSize),
		})
	}
	for i, hw := range inputs.Hardware {
		warnings = append(warnings, validateF(fmt.Sprintf("hardware[%d].unitCost", i), hw.UnitCost, fieldRule{min: fmin(0), max: new(maxMoneyAmount)})...)
		warnings = append(warnings, validateF(fmt.Sprintf("hardware[%d].qtyPerUnit", i), hw.QtyPerUnit, fieldRule{min: fmin(0), max: fmin(maxQuantity)})...)
	}

	hours := float64(inputs.TimeHours) + float64(inputs.TimeMinutes)/60
	rate := effectiveRate(inputs.Energy)
	qtyRaw := float64(inputs.Quantity)
	qty := 1.0
	if qtyRaw >= 1 && !math.IsInf(qtyRaw, 0) {
		qty = qtyRaw
	}
	// Schedule mode: the batch runs back to back from the start, so electricity is priced for the whole span
	// and the per-unit figure is its share. batchElectricity is the exact batch cost.
	var periods []EnergyPeriod
	scheduleOK := false
	scheduleBatch := 0.0
	if inputs.Energy.Mode == "schedule" && inputs.Energy.Schedule != nil {
		kW := float64(inputs.Watts) / 1000
		periods, scheduleBatch, scheduleOK = scheduleEnergy(*inputs.Energy.Schedule, scheduleLoc, inputs.Energy.Start, hours*qty, kW)
		if scheduleOK && hours*qty > 0 && kW > 0 {
			rate = scheduleBatch / (hours * qty * kW)
		}
	}

	material := (float64(inputs.WeightG) * (1 + float64(inputs.WastePct)/100) * float64(inputs.PricePerKg)) / 1000
	electricity := hours * (float64(inputs.Watts) / 1000) * rate
	if inputs.Energy.Mode == "schedule" {
		electricity = 0
		if scheduleOK {
			electricity = scheduleBatch / qty
		}
	}
	depreciation := 0.0
	if float64(inputs.MachineLifeHours) > 0 {
		depreciation = hours * (float64(inputs.MachineCost) / float64(inputs.MachineLifeHours))
	}
	overhead := 0.0
	if float64(inputs.MonthlyHours) > 0 {
		overhead = hours * (float64(inputs.MonthlyFixed) / float64(inputs.MonthlyHours))
	}
	laborMultiplier := 1.0
	if options.LaborPerUnit {
		laborMultiplier = qty
	}
	laborBatch := float64(inputs.LaborRate) * (float64(inputs.PrepMinutes) / 60) * laborMultiplier

	// hardwareLostSum counts only LostOnFailure entries, which join the failure base. It is 0 without
	// hardware.
	var hardwareLostSum float64
	for _, hw := range inputs.Hardware {
		amt := float64(hw.UnitCost) * float64(hw.QtyPerUnit)
		if hw.LostOnFailure {
			hardwareLostSum += amt
		}
	}

	consumables := material + electricity + depreciation
	failureBase := consumables + hardwareLostSum
	// A failure buffer is a cost reserve, never a credit: a negative base must not become a negative line.
	// Only an invalid input makes it negative, so this never fires on a valid quote; it matches
	// pricing_compat_test.go's frozen reference. Floored on the base; NaN still poisons.
	if failureBase < 0 {
		failureBase = 0
	}
	// Expected wasted attempts per good part at independent failure rate p is p / (1 - p), not p. p >= 1 has no
	// finite expectation; it is a validation error already, so the line is 0 rather than +Inf.
	failureP := float64(inputs.FailureRatePct) / 100
	failure := 0.0
	if failureP < 1 {
		failure = failureBase * failureP / (1 - failureP)
	}
	// The base floor closes one route to a credit; a negative failureRatePct is the other (validation errors
	// don't short-circuit line computation). Both frozen references in pricing_compat_test.go floor
	// failureCost at 0, and docs/formulas.md calls it "a cost reserve, not a credit". NaN still poisons.
	if failure < 0 {
		failure = 0
	}

	materialFormula := formatf(inputs.WeightG) + " g × " + formatf(inputs.PricePerKg) + "/kg ÷ 1000"
	if float64(inputs.WastePct) > 0 {
		materialFormula = formatf(inputs.WeightG) + " g × (1 + " + formatf(inputs.WastePct) + "%) × " + formatf(inputs.PricePerKg) + "/kg ÷ 1000"
	}

	depreciationFormula := "lifespan not set"
	if float64(inputs.MachineLifeHours) > 0 {
		depreciationFormula = formatf(round2(hours)) + " h × " + formatf(inputs.MachineCost) + " ÷ " + formatf(inputs.MachineLifeHours) + " h"
	}

	overheadFormula := "monthly hours not set"
	if float64(inputs.MonthlyHours) > 0 {
		overheadFormula = formatf(round2(hours)) + " h × " + formatf(inputs.MonthlyFixed) + " ÷ " + formatf(inputs.MonthlyHours) + " h/mo"
	}

	laborLabel := "Prep labor (batch, amortized)"
	if options.LaborPerUnit {
		laborLabel = "Prep labor (per unit)"
	}

	failureFields := []string{
		"failureRatePct", "weightG", "pricePerKg", "wastePct", "watts", "mode", "flatRate", "peakRate",
		"offPeakRate", "offPeakFrac", "schedule", "printStart", "timeHours", "timeMinutes", "machineCost", "machineLifeHours",
	}
	// The failure line's warnings scope in the hardware that feeds failureBase: only LostOnFailure entries.
	for i, hw := range inputs.Hardware {
		if hw.LostOnFailure {
			failureFields = append(failureFields, fmt.Sprintf("hardware[%d].unitCost", i), fmt.Sprintf("hardware[%d].qtyPerUnit", i))
		}
	}

	// Each line is extended to the batch in float and rounded once, so a sub-cent per-unit cost still totals
	// correctly at volume; the per-unit amount shown is round(batch / qty). batchTotal sums the batch amounts.
	var batchTotal int64
	lineAmount := func(batchFloat float64) int64 {
		b := toCents(batchFloat)
		batchTotal = addCents(batchTotal, b)
		if b == maxCents || b == -maxCents {
			// A saturated batch carries no information; derive the per-unit amount from the float.
			return toCents(batchFloat / qty)
		}
		return divRoundCents(b, qty)
	}
	perUnit := []LineItem{
		{
			ID: "material", Label: "Material", Amount: lineAmount(material * qty), Formula: materialFormula,
			Inputs:   map[string]any{"weightG": float64(inputs.WeightG), "pricePerKg": float64(inputs.PricePerKg), "wastePct": float64(inputs.WastePct)},
			Warnings: pickWarnings(warnings, "weightG", "pricePerKg", "wastePct"),
		},
		{
			ID: "electricity", Label: "Electricity", Amount: lineAmount(electricityBatch(inputs.Energy.Mode, electricity, qty, scheduleBatch)),
			Formula:  electricityFormula(inputs, hours, rate, periods),
			Inputs:   electricityInputs(inputs, hours, rate),
			Warnings: pickWarnings(warnings, "watts", "mode", "flatRate", "peakRate", "offPeakRate", "offPeakFrac", "schedule", "printStart", "timeHours", "timeMinutes"),
			Periods:  periods,
		},
		{
			ID: "depreciation", Label: "Depreciation", Amount: lineAmount(depreciation * qty), Formula: depreciationFormula,
			Inputs:   map[string]any{"hours": round2(hours), "machineCost": float64(inputs.MachineCost), "machineLifeHours": float64(inputs.MachineLifeHours)},
			Warnings: pickWarnings(warnings, "machineCost", "machineLifeHours", "timeHours", "timeMinutes"),
		},
		{
			ID: "failure", Label: "Failure buffer", Amount: lineAmount(failure * qty),
			Formula:  formatf(inputs.FailureRatePct) + "% ÷ (1 − " + formatf(inputs.FailureRatePct) + "%) × " + formatf(round2(failureBase)) + " (consumables)",
			Inputs:   map[string]any{"failureRatePct": float64(inputs.FailureRatePct), "base": round2(failureBase), "appliesTo": "consumables"},
			Warnings: pickWarnings(warnings, failureFields...),
		},
		{
			ID: "labor", Label: laborLabel, Amount: lineAmount(laborBatch),
			Formula:  laborFormula(inputs, options, qty),
			Inputs:   map[string]any{"laborRate": float64(inputs.LaborRate), "prepMinutes": float64(inputs.PrepMinutes), "quantity": qty},
			Warnings: pickWarnings(warnings, "laborRate", "prepMinutes", "quantity"),
		},
		{
			ID: "overhead", Label: "Overhead", Amount: lineAmount(overhead * qty), Formula: overheadFormula,
			Inputs:   map[string]any{"hours": round2(hours), "monthlyFixed": float64(inputs.MonthlyFixed), "monthlyHours": float64(inputs.MonthlyHours)},
			Warnings: pickWarnings(warnings, "monthlyFixed", "monthlyHours", "timeHours", "timeMinutes"),
		},
	}

	// One line per attached hardware entry, after the six pinned lines, so the breakdown names which
	// hardware: each is unitCost x qtyPerUnit, never re-multiplied by the failure rate (LostOnFailure cost
	// enters failureBase).
	for i, hw := range inputs.Hardware {
		amt := float64(hw.UnitCost) * float64(hw.QtyPerUnit)
		perUnit = append(perUnit, LineItem{
			ID: fmt.Sprintf("hardware-%d", i), Label: hw.Name,
			Amount:  lineAmount(amt * qty),
			Formula: formatf(hw.UnitCost) + " × " + formatf(hw.QtyPerUnit) + " qty/unit",
			Inputs: map[string]any{
				"unitCost": float64(hw.UnitCost), "qtyPerUnit": float64(hw.QtyPerUnit), "lostOnFailure": hw.LostOnFailure,
			},
			Warnings: pickWarnings(warnings, fmt.Sprintf("hardware[%d].unitCost", i), fmt.Sprintf("hardware[%d].qtyPerUnit", i)),
		})
	}

	// Both totals saturate rather than wrap (addCents/roundCents): a bare `+=` or int64() would turn a huge
	// positive cost negative, then price and persist it.
	perUnitTotal := divRoundCents(batchTotal, qty)
	if batchTotal == maxCents || batchTotal == -maxCents {
		perUnitTotal = 0
		for _, l := range perUnit {
			perUnitTotal = addCents(perUnitTotal, l.Amount)
		}
	}

	breakdown := CostBreakdown{
		PerUnit:      perUnit,
		PerUnitTotal: perUnitTotal,
		Quantity:     qty,
		BatchTotal:   batchTotal,
		Warnings:     warnings,
		Valid:        !hasErrors(warnings),
	}
	// breakdown.Valid reflects cost-input validity only (the one-way cascade); computePricing folds it into
	// its own.
	if inputs.Pricing != nil {
		pricing := ComputePricing(*inputs.Pricing, breakdown)
		breakdown.Pricing = &pricing
	}
	return breakdown
}

// divRoundCents is round(cents / qty), half away from zero. For a whole quantity it divides in integers, because
// float64 holds only 53 bits and a batch near the int64 range would lose cents converting back and forth.
func divRoundCents(cents int64, qty float64) int64 {
	if n := int64(qty); float64(n) == qty && n >= 1 && n <= maxQuantity {
		q, r := cents/n, cents%n
		if r < 0 {
			if -2*r >= n {
				q--
			}
		} else if 2*r >= n {
			q++
		}
		return q
	}
	return roundCents(float64(cents) / qty)
}

func laborFormula(inputs QuoteInputs, options EngineOptions, qty float64) string {
	f := formatf(inputs.LaborRate) + "/h × " + formatf(inputs.PrepMinutes) + " min ÷ 60"
	if !options.LaborPerUnit {
		f += " ÷ " + formatf(qty)
	}
	return f
}

// ComputePricing applies margin/markup recommendedPrice, resolvedPrice (override or recommended),
// revenue = itemRevenue + shippingCharge, fee lines, price-inclusive tax on item revenue only, netProfit, and
// achievedMarginPct. It is a one-line adapter over computePricingLines, shared with ComputeOrder.
func ComputePricing(p PricingInputs, bd CostBreakdown) PricingResult {
	result, _ := computePricingLines(p, []pricingLine{newPricingLine(bd, p.SalePriceOverride)}, "")
	return result
}

// newPricingLine builds a part's pricing input from its cost breakdown. unitCost is the unrounded per-unit cost
// in cents (batchTotal / qty), so a sub-cent unit cost still prices; it falls back to perUnitTotal for a
// breakdown with no batch.
func newPricingLine(bd CostBreakdown, override OptF) pricingLine {
	unit := float64(bd.PerUnitTotal)
	if bd.BatchTotal != 0 && bd.Quantity >= 1 {
		unit = float64(bd.BatchTotal) / bd.Quantity
	}
	return pricingLine{
		perUnitTotal: bd.PerUnitTotal,
		unitCost:     unit,
		quantity:     bd.Quantity,
		batchTotal:   bd.BatchTotal,
		override:     override,
		costValid:    bd.Valid,
	}
}

// pricingLine is one priced unit of an order for computePricingLines: a part's cost basis plus its own
// sale-price override. The order-level p.SalePriceOverride is ignored; overrides travel per line.
type pricingLine struct {
	perUnitTotal int64
	unitCost     float64 // unrounded per-unit cost in cents; the pricing basis
	quantity     float64
	batchTotal   int64
	override     OptF
	costValid    bool
}

// pricingLineResult is one line's priced outcome, in input order.
type pricingLineResult struct {
	recommended int64
	resolved    int64
	itemRevenue int64
}

// computePricingLines is the post-validation body shared by ComputePricing (one line) and ComputeOrder
// (one per part): order-level inputs apply once, itemRevenue sums price x quantity, and fees, tax,
// shipping, netProfit run once on totals. Warning order is pinned so one line matches ComputePricing.
func computePricingLines(p PricingInputs, lines []pricingLine, prefixKind string) (PricingResult, []pricingLineResult) {
	linePrefix := func(i int) string {
		if prefixKind == "" {
			return ""
		}
		return fmt.Sprintf("%s[%d].", prefixKind, i)
	}
	warnings := []FieldWarning{}

	// Strategy is an enum and the branch below treats anything but "margin" as markup; validating first
	// makes an unrecognized value ("Margin", "") a named error, not a silently markup-priced Valid quote.
	warnings = append(warnings, validateEnum("strategy", p.Strategy, "margin", "markup")...)

	if p.Strategy == "margin" {
		warnings = append(warnings, validateOpt("marginPct", p.MarginPct, fieldRule{min: fmin(0), max: new(maxMarginPct), required: true})...)
	} else {
		warnings = append(warnings, validateOpt("markupMultiplier", p.MarkupMultiplier, fieldRule{min: fmin(0), max: fmin(maxMarkupMultiplier), required: true})...)
	}

	warnings = append(warnings, validateF("taxRatePct", p.TaxRatePct, fieldRule{min: fmin(0), max: fmin(maxPercentAmount)})...)
	warnings = append(warnings, validateF("shippingCharge", p.ShippingCharge, fieldRule{min: fmin(0), max: new(maxMoneyAmount)})...)
	warnings = append(warnings, validateF("shippingCost", p.ShippingCost, fieldRule{min: fmin(0), max: new(maxMoneyAmount)})...)
	for i, l := range lines {
		warnings = append(warnings, validateOpt(linePrefix(i)+"salePriceOverride", l.override, fieldRule{min: fmin(0), max: new(maxMoneyAmount)})...)
	}

	var fixedFees []PricingFeeFixed
	var percentFees []PricingFeePercent
	if p.FeeSchedule != nil {
		fixedFees = p.FeeSchedule.Fixed
		percentFees = p.FeeSchedule.Percent
	}
	for i, f := range fixedFees {
		warnings = append(warnings, validateF(fmt.Sprintf("feeSchedule.fixed[%d].amount", i), f.Amount, fieldRule{min: fmin(0), max: new(maxMoneyAmount)})...)
	}
	for i, f := range percentFees {
		warnings = append(warnings, validateF(fmt.Sprintf("feeSchedule.percent[%d].pct", i), f.Pct, fieldRule{min: fmin(0), max: fmin(maxPercentAmount)})...)
		// PricingFeePercent.Base is wire contract, but the loop below computes every percent fee on revenue;
		// without this check a client schedule declaring base "profit" or "subtotal" would silently use
		// revenue. config.go makes the same check for config.yaml schedules.
		warnings = append(warnings, validateEnum(fmt.Sprintf("feeSchedule.percent[%d].base", i), f.Base, "revenue")...)
	}

	costValid := true
	for i, l := range lines {
		if l.unitCost <= 0 {
			warnings = append(warnings, FieldWarning{Field: linePrefix(i) + "perUnitTotal", Code: "below-min", Message: "cost basis must be positive to price", Severity: "error"})
		}
		if !l.costValid {
			costValid = false
		}
	}

	valid := !hasErrors(warnings) && costValid

	results := make([]pricingLineResult, len(lines))
	var itemRevenue, recommendedRevenue, batchTotal int64
	for i, l := range lines {
		var recommendedPrice int64
		if valid && l.unitCost > 0 {
			// Margin and markup apply to the pre-tax price; the tax-inclusive price grosses that up by
			// taxRatePct. Fees stay a deduction from the result.
			var exTax float64
			if p.Strategy == "margin" {
				exTax = l.unitCost / (1 - p.MarginPct.Value/100)
			} else {
				exTax = l.unitCost * p.MarkupMultiplier.Value
			}
			// Round up, not to nearest: a price rounded down realizes less than the target margin (29.9% for 30%).
			recommendedPrice = ceilCents(exTax * (1 + float64(p.TaxRatePct)/100))
		}

		resolvedPrice := recommendedPrice
		if l.override.Present {
			resolvedPrice = toCents(l.override.Value)
		}

		// l.quantity is fractional only for an (invalid) non-integer quantity, so the cast is exact for what is
		// asserted. mulCents/addCents saturate: resolved price x quantity is a product the per-field maxima
		// don't bound.
		lineRevenue := mulCents(resolvedPrice, int64(math.Round(l.quantity)))
		results[i] = pricingLineResult{recommended: recommendedPrice, resolved: resolvedPrice, itemRevenue: lineRevenue}
		itemRevenue = addCents(itemRevenue, lineRevenue)
		recommendedRevenue = addCents(recommendedRevenue, mulCents(recommendedPrice, int64(math.Round(l.quantity))))
		batchTotal = addCents(batchTotal, l.batchTotal)
	}

	shippingCharge := toCents(float64(p.ShippingCharge))
	shippingCost := toCents(float64(p.ShippingCost))
	revenue := addCents(itemRevenue, shippingCharge)

	feeLines := []FeeLine{}
	for _, f := range fixedFees {
		feeLines = append(feeLines, FeeLine{Label: f.Label, Amount: toCents(float64(f.Amount))})
	}
	for _, f := range percentFees {
		feeLines = append(feeLines, FeeLine{Label: f.Label, Amount: roundCents(float64(revenue) * (float64(f.Pct) / 100))})
	}
	var fees int64
	for _, f := range feeLines {
		fees = addCents(fees, f.Amount)
	}

	// Tax is price-inclusive (VAT-style), as every other layer assumes (docs/formulas.md, the "Tax (included
	// in price)" label, convertQuote.ts forcing taxBp to 0): tax in gross p at rate r is p x r / (100 + r),
	// not p x r / 100 (r=20 on $120 remits $20). On a recommended price (pre-tax price x (1 + r/100)) that
	// equals the pre-tax price x r / 100. Shipping is excluded; the divisor hits 0 only at r = -100.
	tax := roundCents(float64(itemRevenue) * float64(p.TaxRatePct) / (100 + float64(p.TaxRatePct)))
	// Same deductions in order, via saturating subtraction so a clamped revenue or batchTotal can't flip the
	// sign.
	netProfit := revenue
	for _, deduction := range []int64{batchTotal, shippingCost, fees, tax} {
		netProfit = subCents(netProfit, deduction)
	}

	// Margin and markup are measured before tax and fees, so fees or a low override can still push profit
	// negative on a valid quote; say so instead of leaving a silent loss.
	if valid && netProfit < 0 {
		warnings = append(warnings, FieldWarning{
			Field:    "netProfit",
			Code:     "net-profit-negative",
			Message:  "netProfit is negative: this price does not cover costs, fees, and tax",
			Severity: "warning",
		})
	}

	achievedMarginPct := 0.0
	if revenue > 0 {
		achievedMarginPct = (float64(netProfit) / float64(revenue)) * 100
	}

	// One line: the per-unit prices. Two or more: sums of price x quantity, comparable across differing
	// quantities.
	var recommendedOut, resolvedOut int64
	switch len(lines) {
	case 0:
	case 1:
		recommendedOut, resolvedOut = results[0].recommended, results[0].resolved
	default:
		recommendedOut, resolvedOut = recommendedRevenue, itemRevenue
	}

	return PricingResult{
		Strategy:          p.Strategy,
		RecommendedPrice:  recommendedOut,
		ResolvedPrice:     resolvedOut,
		Revenue:           revenue,
		Fees:              fees,
		FeeLines:          feeLines,
		Tax:               tax,
		ShippingCharge:    shippingCharge,
		ShippingCost:      shippingCost,
		NetProfit:         netProfit,
		AchievedMarginPct: achievedMarginPct,
		FeeScheduleName:   p.FeeScheduleName,
		Warnings:          warnings,
		Valid:             valid,
	}, results
}
