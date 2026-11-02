package main

import (
	"encoding/json"
	"fmt"
	"math"
	"testing"
)

// referenceQuoteConsumables is an independent reference implementation: do not "fix" it to match pricing.go; it uses plain float64 and only toCents from pricing.go.
// Grids: 144 cases (4 weights x 4 times x 3 failure rates x 3 machine configs) under DefaultEngineOptions, exact cents per line and perUnitTotal within 3c; 3 margin/fee cases (30% margin, Etsy-US schedule) within 5c. Later tests are additive.
// KNOWN GAP, deferred: wastePct has no grid dimension (the reference has no waste concept); TestBoundaryVectors and the wastePct fixture vectors pin it, and closing it needs a SECOND independent reference.

const fixedMargin = 0.3

// refInputs holds referenceQuote's inputs. salePrice/shipping are
// pointers so an absent value can be distinguished from an explicit 0.
type refInputs struct {
	weight       float64
	pricePerKg   float64
	timeH        float64
	timeM        float64
	watts        float64
	rate         float64
	failurePct   float64
	prepMin      float64
	laborRate    float64
	machineCost  float64
	machineLife  float64
	monthlyFixed float64
	monthlyHours float64
	salePrice    *float64
	shipping     *float64
}

// refResult holds referenceQuote's result. revenue/fees/netProfit are
// only meaningful when salePrice or shipping was set on the input.
type refResult struct {
	matCost, elecCost, deprCost, laborCost, overheadCost float64
	subtotal, failureCost, total, recommended            float64
	revenue, fees, netProfit                             float64
}

// refToQuoteInputs builds the engine's QuoteInputs from a refInputs,
// always wastePct 0, quantity 1, flat-rate energy mode.
func refToQuoteInputs(r refInputs) QuoteInputs {
	return QuoteInputs{
		WeightG:          F(r.weight),
		PricePerKg:       F(r.pricePerKg),
		WastePct:         F(0),
		TimeHours:        F(r.timeH),
		TimeMinutes:      F(r.timeM),
		Watts:            F(r.watts),
		Energy:           EnergyInputs{Mode: "flat", FlatRate: OptF{Value: r.rate, Present: true}},
		FailureRatePct:   F(r.failurePct),
		PrepMinutes:      F(r.prepMin),
		LaborRate:        F(r.laborRate),
		MachineCost:      F(r.machineCost),
		MachineLifeHours: F(r.machineLife),
		MonthlyFixed:     F(r.monthlyFixed),
		MonthlyHours:     F(r.monthlyHours),
		Quantity:         F(1),
	}
}

func lineAmount(t *testing.T, bd CostBreakdown, id string) int64 {
	t.Helper()
	for _, l := range bd.PerUnit {
		if l.ID == id {
			return l.Amount
		}
	}
	t.Fatalf("no perUnit line item with id %q", id)
	return 0
}

// The independent reference, written from docs/formulas.md's "consumables" rule. Failure applies only to material + electricity + depreciation, floored at zero (never a credit):
// failureCost = (matCost + elecCost + deprCost) x p/(1-p) with p = failurePct/100 (expected wasted attempts per good part); total = matCost + elecCost + deprCost + laborCost + overheadCost + failureCost.
func referenceQuoteConsumables(i refInputs) refResult {
	totalHours := i.timeH + i.timeM/60
	matCost := i.weight * (i.pricePerKg / 1000)
	elecCost := totalHours * (i.watts / 1000) * i.rate
	hourlyDepr := 0.0
	if i.machineLife > 0 {
		hourlyDepr = i.machineCost / i.machineLife
	}
	deprCost := totalHours * hourlyDepr
	laborCost := i.laborRate * (i.prepMin / 60)
	overheadRate := 0.0
	if i.monthlyHours > 0 {
		overheadRate = i.monthlyFixed / i.monthlyHours
	}
	overheadCost := totalHours * overheadRate

	consumables := matCost + elecCost + deprCost
	p := i.failurePct / 100
	failureCost := consumables * p / (1 - p)
	if failureCost < 0 {
		failureCost = 0
	}
	subtotal := consumables + laborCost + overheadCost
	total := subtotal + failureCost

	recommended := 0.0
	if total > 0 {
		recommended = total / (1 - fixedMargin)
	}

	result := refResult{
		matCost: matCost, elecCost: elecCost, deprCost: deprCost,
		laborCost: laborCost, overheadCost: overheadCost,
		subtotal: subtotal, failureCost: failureCost, total: total,
		recommended: recommended,
	}

	if i.salePrice != nil || i.shipping != nil {
		// The frozen reference uses a || fallback (salePrice 0 falls back to recommended); our engine treats an explicit salePriceOverride of 0 as $0, so no case uses salePrice 0.
		sale := 0.0
		if i.salePrice != nil {
			sale = *i.salePrice
		}
		resolvedSale := sale
		if resolvedSale == 0 {
			resolvedSale = recommended
		}
		shipping := 0.0
		if i.shipping != nil {
			shipping = *i.shipping
		}
		revenue := resolvedSale + shipping
		fees := 0.2 + revenue*0.065 + revenue*0.03 + 0.25
		result.revenue = revenue
		result.fees = fees
		result.netProfit = revenue - total - fees
	}
	return result
}

// The 144-case grid (4 weights x 4 times x 3 failure rates x 3 machine configs) under DefaultEngineOptions against referenceQuoteConsumables: exact cents per line, perUnitTotal within 3c.
func TestPricingCompatCostGridConsumables(t *testing.T) {
	weights := []float64{0, 12.5, 50, 342.7}
	times := [][2]float64{{0, 0}, {1, 20}, {10, 0}, {3, 47}}
	failures := []float64{0, 5, 8}
	machines := [][2]float64{{400, 4000}, {0, 0}, {1200, 4000}}

	for _, weight := range weights {
		for _, tm := range times {
			timeH, timeM := tm[0], tm[1]
			for _, failurePct := range failures {
				for _, m := range machines {
					machineCost, machineLife := m[0], m[1]
					weight, timeH, timeM, failurePct, machineCost, machineLife := weight, timeH, timeM, failurePct, machineCost, machineLife
					name := fmt.Sprintf("w=%v_t=%vh%vm_f=%v%%_m=%v-%v", weight, timeH, timeM, failurePct, machineCost, machineLife)
					t.Run(name, func(t *testing.T) {
						ref := refInputs{
							weight: weight, pricePerKg: 22, timeH: timeH, timeM: timeM,
							watts: 250, rate: 0.15, failurePct: failurePct, prepMin: 5, laborRate: 12,
							machineCost: machineCost, machineLife: machineLife,
							monthlyFixed: 250, monthlyHours: 120,
						}
						expected := referenceQuoteConsumables(ref)
						bd := ComputeQuote(refToQuoteInputs(ref), DefaultEngineOptions)

						for _, l := range []struct {
							id   string
							want float64
						}{
							{"material", expected.matCost},
							{"electricity", expected.elecCost},
							{"depreciation", expected.deprCost},
							{"labor", expected.laborCost},
							{"overhead", expected.overheadCost},
							{"failure", expected.failureCost},
						} {
							if got, want := lineAmount(t, bd, l.id), toCents(l.want); got != want {
								t.Errorf("%s = %d, want %d", l.id, got, want)
							}
						}

						wantTotal := toCents(expected.total)
						if diff := bd.PerUnitTotal - wantTotal; diff < -3 || diff > 3 {
							t.Errorf("perUnitTotal = %d, want within 3 cents of %d", bd.PerUnitTotal, wantTotal)
						}
						if !bd.Valid {
							t.Errorf("valid = false, want true")
						}
					})
				}
			}
		}
	}
}

// Pins the negative-failure-base floor against an independent expectation: a failure buffer is a cost reserve, never a credit, so a negative base (from invalid input) floors to zero.
// The frozen reference floored its failureCost while pricing.go once did not, and the non-negative grid could never expose it.
func TestPricingCompatNegativeFailureBaseFloored(t *testing.T) {
	// weight -1000 g at $22/kg makes matCost -$22.00, so consumables is
	// negative and the pre-floor buffer would be a credit.
	ref := refInputs{
		weight: -1000, pricePerKg: 22, timeH: 1, timeM: 0,
		watts: 250, rate: 0.15, failurePct: 8, prepMin: 5, laborRate: 12,
		machineCost: 400, machineLife: 4000, monthlyFixed: 250, monthlyHours: 120,
	}
	expected := referenceQuoteConsumables(ref)
	if expected.failureCost != 0 {
		t.Fatalf("test setup: reference failureCost = %v, want 0 (the floor must be what is under test)", expected.failureCost)
	}
	bd := ComputeQuote(refToQuoteInputs(ref), DefaultEngineOptions)
	if got := lineAmount(t, bd, "failure"); got != 0 {
		t.Errorf("failure = %d, want 0 (a negative failure base floors to zero, never a credit)", got)
	}
	if bd.Valid {
		t.Error("valid = true, want false (a negative weightG is still an input error)")
	}
	if !hasWarning(bd.Warnings, "weightG", "below-min") {
		t.Errorf("no weightG/below-min warning; warnings = %+v", bd.Warnings)
	}
}

// The SECOND route to a negative failure line: a negative failureRatePct over a positive base. It is a below-min validation error, but pricing.go floors the cost as well as the base,
// so the engine and the reference agree (-21c would otherwise appear as a credit), as docs/formulas.md's no-negative rule states.
func TestPricingCompatNegativeFailureRateFloored(t *testing.T) {
	// hardwareCompatBaseInputs: consumables $2.10; only the RATE is negative, which distinguishes this from the negative-base case.
	inputs := hardwareCompatBaseInputs()
	inputs.FailureRatePct = F(-10)
	bd := ComputeQuote(inputs, DefaultEngineOptions)

	if got := lineAmount(t, bd, "failure"); got != 0 {
		t.Errorf("failure = %d, want 0 (a cost reserve is never a credit, whichever side of the product is negative)", got)
	}
	if bd.Valid {
		t.Error("valid = true, want false (a negative failureRatePct is still an input error)")
	}
	if !hasWarning(bd.Warnings, "failureRatePct", "below-min") {
		t.Errorf("no failureRatePct/below-min warning; warnings = %+v", bd.Warnings)
	}
	// The reference's expectation, asserted the same way the
	// negative-base test above does: both independent implementations
	// return 0 failure cost on this input.
	ref := refInputs{
		weight: 100, pricePerKg: 20, timeH: 1, timeM: 0, watts: 1000, rate: 0.10,
		failurePct: -10, prepMin: 60, laborRate: 10,
	}
	refCost := referenceQuoteConsumables(ref).failureCost
	if refCost != 0 {
		t.Fatalf("test setup: reference failureCost = %v, want 0", refCost)
	}
	// Everything else is untouched: 200 + 10 + 0 + 0 + 1000 + 0.
	if bd.PerUnitTotal != 1210 {
		t.Errorf("perUnitTotal = %d, want 1210", bd.PerUnitTotal)
	}
}

// Gives batch labor amortization (laborRate x prepMinutes / 60 / quantity) and batchTotal an independent expectation from docs/formulas.md;
// the references have no quantity concept (the grids use quantity 1).
func TestPricingCompatBatchLaborAmortization(t *testing.T) {
	const laborRate, prepMinutes = 12.0, 50.0
	laborBatchDollars := laborRate * (prepMinutes / 60) // $10.00 of prep for the whole batch

	for _, qty := range []float64{1, 2, 3, 4, 7, 250} {
		for _, perUnit := range []bool{false, true} {
			qty, perUnit := qty, perUnit
			t.Run(fmt.Sprintf("qty=%v_laborPerUnit=%v", qty, perUnit), func(t *testing.T) {
				// Batch mode spreads one batch's prep across the quantity;
				// per-unit mode charges the full prep to each unit.
				wantLaborBatch := toCents(laborBatchDollars)
				if perUnit {
					wantLaborBatch = toCents(laborBatchDollars * qty)
				}
				wantLabor := roundCents(float64(wantLaborBatch) / qty)

				inputs := hardwareCompatBaseInputs()
				inputs.PrepMinutes = F(prepMinutes)
				inputs.LaborRate = F(laborRate)
				inputs.Quantity = F(qty)
				bd := ComputeQuote(inputs, EngineOptions{LaborPerUnit: perUnit})

				if !bd.Valid {
					t.Fatalf("valid = false, want true; warnings = %+v", bd.Warnings)
				}
				if got := lineAmount(t, bd, "labor"); got != wantLabor {
					t.Errorf("labor = %d, want %d", got, wantLabor)
				}
				// Each cost line is extended to the batch in float and rounded once: 200 material +
				// 10 electricity + 0 depreciation + 23.33 failure (210 x 0.1 / 0.9) + 0 overhead per unit.
				wantBatch := toCents(2.00*qty) + toCents(0.10*qty) + roundCents(qty*210.0*0.1/0.9) + wantLaborBatch
				if bd.BatchTotal != wantBatch {
					t.Errorf("batchTotal = %d, want %d (each line rounded once at batch)", bd.BatchTotal, wantBatch)
				}
				if want := roundCents(float64(wantBatch) / qty); bd.PerUnitTotal != want {
					t.Errorf("perUnitTotal = %d, want %d (round(batchTotal / qty))", bd.PerUnitTotal, want)
				}
			})
		}
	}
}

// Hardware component: frozen vectors added 2026-09-15 with hand-derived expected cents (no independent reference exists). Do not adjust an expected value to follow a pricing.go change without re-deriving it from docs/formulas.md.
// Shared base: material 200c (100 g x $20/kg), electricity 10c (1 h x 1000 W x $0.10/kWh), depreciation 0, labor 1000c ($10/h x 60 min, batch, qty 1), overhead 0, consumables $2.10, failureRatePct 10%.
// Catalog entries: magnet unitCost $1.00 x qtyPerUnit 2, lostOnFailure -> $2.00/unit; hook $0.50 x 1, not lost -> $0.50/unit.
func hardwareCompatBaseInputs() QuoteInputs {
	return QuoteInputs{
		WeightG: F(100), PricePerKg: F(20), WastePct: F(0),
		TimeHours: F(1), TimeMinutes: F(0), Watts: F(1000),
		Energy:         EnergyInputs{Mode: "flat", FlatRate: OptF{Value: 0.10, Present: true}},
		FailureRatePct: F(10), PrepMinutes: F(60), LaborRate: F(10),
		MachineCost: F(0), MachineLifeHours: F(0),
		MonthlyFixed: F(0), MonthlyHours: F(0), Quantity: F(1),
	}
}

func TestPricingCompatHardware(t *testing.T) {
	magnet := HardwareLine{Name: "Magnet", UnitCost: F(1.00), QtyPerUnit: F(2), LostOnFailure: true}
	hook := HardwareLine{Name: "Hook", UnitCost: F(0.50), QtyPerUnit: F(1), LostOnFailure: false}
	bracket := HardwareLine{Name: "Bracket", UnitCost: F(0.25), QtyPerUnit: F(3), LostOnFailure: false}

	consumablesOpts := DefaultEngineOptions // laborPerUnit false

	cases := []struct {
		name        string
		hardware    []HardwareLine
		options     EngineOptions
		wantHwCents []int64 // one entry per attached hardware line, in order
		wantFailure int64
		wantPerUnit int64
		wantBatch   int64
	}{
		{
			// lostOnFailure=false only: the hook's cost never joins the
			// failure base under "consumables" mode, so failure stays at
			// consumables-only: $2.10 x 10% / 90% = $0.2333 (23c).
			name: "lostOnFailure-false-only", hardware: []HardwareLine{hook}, options: consumablesOpts,
			wantHwCents: []int64{50}, wantFailure: 23, wantPerUnit: 1283, wantBatch: 1283,
		},
		{
			// lostOnFailure=true only: the magnet's $2.00 joins the failure
			// base: ($2.10 + $2.00) x 10% / 90% = $0.4556 (46c).
			name: "lostOnFailure-true-only", hardware: []HardwareLine{magnet}, options: consumablesOpts,
			wantHwCents: []int64{200}, wantFailure: 46, wantPerUnit: 1456, wantBatch: 1456,
		},
		{
			// Mixed entries in "consumables" mode: only the magnet ($2.00, lostOnFailure) joins the failure base, the hook ($0.50) is added after failure. failureBase = $2.10 + $2.00 = $4.10; failure = 46c.
			name: "mixed-consumables", hardware: []HardwareLine{magnet, hook}, options: consumablesOpts,
			wantHwCents: []int64{200, 50}, wantFailure: 46, wantPerUnit: 1506, wantBatch: 1506,
		},
		{
			// qtyPerUnit > 1 on a lostOnFailure=false entry (0.25 * 3 =
			// $0.75), "consumables" mode: never joins the failure base,
			// failure stays at the plain consumables-only $0.2333 (23c).
			name: "qty-per-unit-gt-1", hardware: []HardwareLine{bracket}, options: consumablesOpts,
			wantHwCents: []int64{75}, wantFailure: 23, wantPerUnit: 1308, wantBatch: 1308,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inputs := hardwareCompatBaseInputs()
			inputs.Hardware = c.hardware
			bd := ComputeQuote(inputs, c.options)

			if !bd.Valid {
				t.Fatalf("valid = false, want true")
			}
			if got, want := lineAmount(t, bd, "material"), int64(200); got != want {
				t.Errorf("material = %d, want %d", got, want)
			}
			if got, want := lineAmount(t, bd, "electricity"), int64(10); got != want {
				t.Errorf("electricity = %d, want %d", got, want)
			}
			if got, want := lineAmount(t, bd, "depreciation"), int64(0); got != want {
				t.Errorf("depreciation = %d, want %d", got, want)
			}
			if got, want := lineAmount(t, bd, "labor"), int64(1000); got != want {
				t.Errorf("labor = %d, want %d", got, want)
			}
			if got, want := lineAmount(t, bd, "overhead"), int64(0); got != want {
				t.Errorf("overhead = %d, want %d", got, want)
			}
			if got, want := lineAmount(t, bd, "failure"), c.wantFailure; got != want {
				t.Errorf("failure = %d, want %d", got, want)
			}
			if len(bd.PerUnit) != 6+len(c.hardware) {
				t.Fatalf("perUnit line count = %d, want %d", len(bd.PerUnit), 6+len(c.hardware))
			}
			for i, want := range c.wantHwCents {
				id := fmt.Sprintf("hardware-%d", i)
				if got := lineAmount(t, bd, id); got != want {
					t.Errorf("%s = %d, want %d", id, got, want)
				}
				if label := bd.PerUnit[6+i].Label; label != c.hardware[i].Name {
					t.Errorf("%s label = %q, want %q", id, label, c.hardware[i].Name)
				}
			}
			if bd.PerUnitTotal != c.wantPerUnit {
				t.Errorf("perUnitTotal = %d, want %d", bd.PerUnitTotal, c.wantPerUnit)
			}
			if bd.BatchTotal != c.wantBatch {
				t.Errorf("batchTotal = %d, want %d", bd.BatchTotal, c.wantBatch)
			}
		})
	}
}

// An explicit empty []HardwareLine{} and a nil Hardware field give byte-identical CostBreakdown output, matching the six-line baseline:
// $2.10 consumables x 10% / 90% = 23c failure, perUnitTotal 1233c ("absent/empty hardware = bit-identical to today").
func TestPricingCompatHardwareZeroEntryEqualsAbsent(t *testing.T) {
	absent := hardwareCompatBaseInputs()
	absent.Hardware = nil

	empty := hardwareCompatBaseInputs()
	empty.Hardware = []HardwareLine{}

	bdAbsent := ComputeQuote(absent, DefaultEngineOptions)
	bdEmpty := ComputeQuote(empty, DefaultEngineOptions)

	absentJSON, err := json.Marshal(bdAbsent)
	if err != nil {
		t.Fatalf("marshal absent: %v", err)
	}
	emptyJSON, err := json.Marshal(bdEmpty)
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	if string(absentJSON) != string(emptyJSON) {
		t.Errorf("nil Hardware and []HardwareLine{} produced different output:\n absent: %s\n empty:  %s", absentJSON, emptyJSON)
	}

	if len(bdAbsent.PerUnit) != 6 {
		t.Fatalf("perUnit line count = %d, want 6 (no hardware lines)", len(bdAbsent.PerUnit))
	}
	if got, want := lineAmount(t, bdAbsent, "failure"), int64(23); got != want {
		t.Errorf("failure = %d, want %d", got, want)
	}
	if bdAbsent.PerUnitTotal != 1233 {
		t.Errorf("perUnitTotal = %d, want 1233", bdAbsent.PerUnitTotal)
	}
	if bdAbsent.BatchTotal != 1233 {
		t.Errorf("batchTotal = %d, want 1233", bdAbsent.BatchTotal)
	}
}

// Hardware interactions the vectors above leave open (they omit PricingInputs, use Quantity 1 and laborPerUnit false, and attach only well-formed entries).
// Expected values derive from hardwareCompatBaseInputs: 200c material, 10c electricity, 0c depreciation, 1000c batch labor at qty 1, 0c overhead, 10% failure, $2.10 consumables.

// TestPricingCompatHardwareWithPricing pins recommendedPrice/revenue/
// netProfit over a perUnitTotal that INCLUDES a hardware line -- the whole
// pricing block was outside the hardware vectors entirely.
func TestPricingCompatHardwareWithPricing(t *testing.T) {
	magnet := HardwareLine{Name: "Magnet", UnitCost: F(1.00), QtyPerUnit: F(2), LostOnFailure: true}

	inputs := hardwareCompatBaseInputs()
	inputs.Hardware = []HardwareLine{magnet}
	inputs.Pricing = &PricingInputs{
		Strategy:   "margin",
		MarginPct:  OptF{Value: 30, Present: true},
		TaxRatePct: F(0), ShippingCharge: F(0), ShippingCost: F(0),
	}
	bd := ComputeQuote(inputs, DefaultEngineOptions)

	// failureBase = $2.10 consumables + $2.00 magnet (lostOnFailure) = $4.10
	// -> failure $0.4556 (46c); perUnitTotal = 200 + 10 + 0 + 46 + 1000 + 0 +
	// 200 = 1456c, the same figure TestPricingCompatHardware pins.
	if bd.PerUnitTotal != 1456 {
		t.Fatalf("perUnitTotal = %d, want 1456", bd.PerUnitTotal)
	}
	p := bd.Pricing
	if p == nil {
		t.Fatal("pricing result is nil")
	}
	if !p.Valid {
		t.Fatalf("pricing valid = false, want true; warnings = %+v", p.Warnings)
	}
	// 1456 / (1 - 0.30) = 2080c, and with no override the
	// resolved price is the recommended one.
	if p.RecommendedPrice != 2080 || p.ResolvedPrice != 2080 || p.Revenue != 2080 {
		t.Errorf("recommended/resolved/revenue = %d/%d/%d, want 2080/2080/2080", p.RecommendedPrice, p.ResolvedPrice, p.Revenue)
	}
	// netProfit = revenue 2080 - batchTotal 1456 - 0 shipping - 0 fees - 0 tax.
	if p.NetProfit != 624 {
		t.Errorf("netProfit = %d, want 624", p.NetProfit)
	}
	if want := 624.0 / 2080.0 * 100; math.Abs(p.AchievedMarginPct-want) > 1e-9 {
		t.Errorf("achievedMarginPct = %v, want %v", p.AchievedMarginPct, want)
	}
}

// Hardware at quantity > 1 (batchTotal over a hardware-bearing perUnitTotal).
func TestPricingCompatHardwareQuantityAndLaborPerUnit(t *testing.T) {
	magnet := HardwareLine{Name: "Magnet", UnitCost: F(1.00), QtyPerUnit: F(2), LostOnFailure: true}

	t.Run("quantity-4-batch-labor", func(t *testing.T) {
		// Each line is extended to the batch in float and rounded once: material 800, electricity 40, failure 182
		// ($4.10 x 10% / 90% x 4 = 182.2c), labor 1000 ($10.00 batch), hardware 800 -> batchTotal 2822c.
		// Per-unit amounts are round(batch / 4): labor 250, failure 46 (45.5), hardware 200; perUnitTotal = round(2822 / 4) = 706c.
		inputs := hardwareCompatBaseInputs()
		inputs.Quantity = F(4)
		inputs.Hardware = []HardwareLine{magnet}
		bd := ComputeQuote(inputs, DefaultEngineOptions)
		if !bd.Valid {
			t.Fatalf("valid = false; warnings = %+v", bd.Warnings)
		}
		if got := lineAmount(t, bd, "labor"); got != 250 {
			t.Errorf("labor = %d, want 250", got)
		}
		if got := lineAmount(t, bd, "failure"); got != 46 {
			t.Errorf("failure = %d, want 46", got)
		}
		if got := lineAmount(t, bd, "hardware-0"); got != 200 {
			t.Errorf("hardware-0 = %d, want 200", got)
		}
		if bd.PerUnitTotal != 706 || bd.BatchTotal != 2822 {
			t.Errorf("perUnitTotal/batchTotal = %d/%d, want 706/2822", bd.PerUnitTotal, bd.BatchTotal)
		}
	})
}

// Per-entry hardware warnings and their SCOPING into the failure line: an entry's warnings go on the failure line only when it feeds the failure base (only lostOnFailure ones).
func TestPricingCompatHardwareInvalidEntries(t *testing.T) {
	// Entry 0 is lost on failure and doubly invalid (negative unitCost, NaN qtyPerUnit); entry 1 is not lost and has a negative unitCost, so under "consumables" its warnings stay off the failure line.
	bad := HardwareLine{Name: "Bad", UnitCost: F(-1), QtyPerUnit: F(math.NaN()), LostOnFailure: true}
	badFree := HardwareLine{Name: "Bad free", UnitCost: F(-2), QtyPerUnit: F(1), LostOnFailure: false}

	inputs := hardwareCompatBaseInputs()
	inputs.Hardware = []HardwareLine{bad, badFree}
	bd := ComputeQuote(inputs, DefaultEngineOptions)

	if bd.Valid {
		t.Error("valid = true, want false")
	}
	for _, w := range []struct{ field, code string }{
		{"hardware[0].unitCost", "below-min"},
		{"hardware[0].qtyPerUnit", "nan"},
		{"hardware[1].unitCost", "below-min"},
	} {
		if !hasWarning(bd.Warnings, w.field, w.code) {
			t.Errorf("no %s/%s warning; warnings = %+v", w.field, w.code, bd.Warnings)
		}
	}

	var failureLine LineItem
	for _, l := range bd.PerUnit {
		if l.ID == "failure" {
			failureLine = l
		}
	}
	if !hasWarning(failureLine.Warnings, "hardware[0].unitCost", "below-min") ||
		!hasWarning(failureLine.Warnings, "hardware[0].qtyPerUnit", "nan") {
		t.Errorf("failure line is missing the lostOnFailure entry's warnings: %+v", failureLine.Warnings)
	}
	if hasWarning(failureLine.Warnings, "hardware[1].unitCost", "below-min") {
		t.Errorf("failure line carries a NON-lostOnFailure entry's warning on the failure line: %+v", failureLine.Warnings)
	}

	// Amounts: entry 0 is -1 × NaN = NaN -> 0c, and poisons the failure base
	// (NaN), so the failure line is 0c too. Entry 1 is -2 × 1 = -$2.00
	// (-200c). perUnitTotal = 200 + 10 + 0 + 0 + 1000 + 0 + 0 + (-200) = 1010c.
	if got := lineAmount(t, bd, "hardware-0"); got != 0 {
		t.Errorf("hardware-0 = %d, want 0", got)
	}
	if got := lineAmount(t, bd, "hardware-1"); got != -200 {
		t.Errorf("hardware-1 = %d, want -200", got)
	}
	if got := lineAmount(t, bd, "failure"); got != 0 {
		t.Errorf("failure = %d, want 0 (a NaN base poisons the line)", got)
	}
	if bd.PerUnitTotal != 1010 {
		t.Errorf("perUnitTotal = %d, want 1010", bd.PerUnitTotal)
	}

}

// wastePct feeds failureBase through the material term, so an invalid wastePct must scope onto the failure line's warnings, not just the material line's.
func TestPricingCompatInvalidWastePctScopesOntoFailureLine(t *testing.T) {
	inputs := hardwareCompatBaseInputs()
	inputs.WastePct = F(150) // above the 100 max

	bd := ComputeQuote(inputs, DefaultEngineOptions)
	if bd.Valid {
		t.Error("valid = true, want false")
	}
	if !hasWarning(bd.Warnings, "wastePct", "above-max") {
		t.Errorf("no wastePct/above-max warning; warnings = %+v", bd.Warnings)
	}

	var materialLine, failureLine LineItem
	for _, l := range bd.PerUnit {
		switch l.ID {
		case "material":
			materialLine = l
		case "failure":
			failureLine = l
		}
	}
	if !hasWarning(materialLine.Warnings, "wastePct", "above-max") {
		t.Errorf("material line is missing wastePct/above-max: %+v", materialLine.Warnings)
	}
	if !hasWarning(failureLine.Warnings, "wastePct", "above-max") {
		t.Errorf("failure line is missing wastePct/above-max: %+v", failureLine.Warnings)
	}
}

// A zero-quantity attached entry still produces its own perUnit LINE (0c) and a zero term in the failure base: attached-but-unused, not absent.
func TestPricingCompatHardwareZeroQtyPerUnit(t *testing.T) {
	zero := HardwareLine{Name: "Magnet", UnitCost: F(1.00), QtyPerUnit: F(0), LostOnFailure: true}

	inputs := hardwareCompatBaseInputs()
	inputs.Hardware = []HardwareLine{zero}
	bd := ComputeQuote(inputs, DefaultEngineOptions)

	if !bd.Valid {
		t.Fatalf("valid = false, want true (0 is a legitimate qtyPerUnit); warnings = %+v", bd.Warnings)
	}
	if len(bd.PerUnit) != 7 {
		t.Fatalf("perUnit line count = %d, want 7 (six pinned lines plus the attached entry)", len(bd.PerUnit))
	}
	if got := lineAmount(t, bd, "hardware-0"); got != 0 {
		t.Errorf("hardware-0 = %d, want 0", got)
	}
	// failureBase = $2.10 consumables + $0.00 = $2.10 -> $0.2333 (23c), the
	// same as the no-hardware baseline; perUnitTotal = 1233c likewise.
	if got := lineAmount(t, bd, "failure"); got != 23 {
		t.Errorf("failure = %d, want 23", got)
	}
	if bd.PerUnitTotal != 1233 {
		t.Errorf("perUnitTotal = %d, want 1233", bd.PerUnitTotal)
	}
}

// TestPricingCompatMarginAndFees covers "compat: pricing matches reference (30% margin, Etsy-US fees)".
func TestPricingCompatMarginAndFees(t *testing.T) {
	etsyUs := &PricingFeeSchedule{
		Fixed: []PricingFeeFixed{
			{Amount: F(0.2), Label: "listing"},
			{Amount: F(0.25), Label: "payment fixed"},
		},
		Percent: []PricingFeePercent{
			{Pct: F(6.5), Base: "revenue", Label: "transaction"},
			{Pct: F(3.0), Base: "revenue", Label: "payment"},
		},
	}
	feeScheduleName := "etsy-us"

	sale15, shipping45 := 15.0, 4.5
	shipping5 := 5.0
	sale8, shipping0 := 8.0, 0.0

	cases := []refInputs{
		{
			weight: 50, pricePerKg: 20, timeH: 1, timeM: 20, watts: 250, rate: 0.15,
			failurePct: 5, prepMin: 5, laborRate: 12, machineCost: 400, machineLife: 4000,
			monthlyFixed: 0, monthlyHours: 100, salePrice: &sale15, shipping: &shipping45,
		},
		{
			weight: 120, pricePerKg: 25, timeH: 6, timeM: 0, watts: 250, rate: 0.15,
			failurePct: 8, prepMin: 8, laborRate: 18, machineCost: 1200, machineLife: 4000,
			monthlyFixed: 250, monthlyHours: 120, salePrice: nil, shipping: &shipping5,
		},
		{
			weight: 30, pricePerKg: 18, timeH: 0, timeM: 45, watts: 180, rate: 0.15,
			failurePct: 3, prepMin: 0, laborRate: 0, machineCost: 300, machineLife: 5000,
			monthlyFixed: 0, monthlyHours: 50, salePrice: &sale8, shipping: &shipping0,
		},
	}

	for n, ref := range cases {
		t.Run(fmt.Sprintf("pricing_case_%d", n), func(t *testing.T) {
			expected := referenceQuoteConsumables(ref)

			var salePriceOverride OptF
			if ref.salePrice != nil {
				salePriceOverride = OptF{Value: *ref.salePrice, Present: true}
			}
			shippingCharge := 0.0
			if ref.shipping != nil {
				shippingCharge = *ref.shipping
			}

			qi := refToQuoteInputs(ref)
			qi.Pricing = &PricingInputs{
				Strategy:          "margin",
				MarginPct:         OptF{Value: 30, Present: true},
				TaxRatePct:        F(0),
				ShippingCharge:    F(shippingCharge),
				ShippingCost:      F(0),
				FeeSchedule:       etsyUs,
				FeeScheduleName:   &feeScheduleName,
				SalePriceOverride: salePriceOverride,
			}

			bd := ComputeQuote(qi, DefaultEngineOptions)
			p := bd.Pricing
			if p == nil {
				t.Fatal("pricing result is nil")
			}

			within := func(label string, got, want, tol int64) {
				t.Helper()
				if diff := got - want; diff < -tol || diff > tol {
					t.Errorf("%s = %d, want within %d cents of %d", label, got, tol, want)
				}
			}
			within("recommendedPrice", p.RecommendedPrice, toCents(expected.recommended), 5)
			within("revenue", p.Revenue, toCents(expected.revenue), 5)
			within("fees", p.Fees, toCents(expected.fees), 5)
			within("netProfit", p.NetProfit, toCents(expected.netProfit), 5)
		})
	}
}

// Gives TOU energy cross-implementation coverage without editing the frozen references (which carry a single `rate`): compute effectiveRate's blend
// (clamp(frac,0,1)*offPeak + (1-clamp)*peak) with plain arithmetic and feed it to referenceQuoteConsumables as a flat rate. The TOU quote must equal both that reference and pricing.go's flat quote; the frac cases straddle the clamp.
func TestPricingCompatTouBlendAgainstFlatEquivalent(t *testing.T) {
	for _, c := range []struct {
		peak, offPeak, frac float64
	}{
		{0.30, 0.10, 0},
		{0.30, 0.10, 0.25},
		{0.30, 0.10, 0.5},
		{0.30, 0.10, 1},
		{0.30, 0.10, 1.5},  // clamps to 1: all off-peak
		{0.30, 0.10, -0.5}, // clamps to 0: all peak
		{0.18, 0.42, 0.75}, // off-peak dearer than peak: the blend still just weights
	} {
		t.Run(fmt.Sprintf("peak=%v_off=%v_frac=%v", c.peak, c.offPeak, c.frac), func(t *testing.T) {
			clamped := c.frac
			if clamped < 0 {
				clamped = 0
			}
			if clamped > 1 {
				clamped = 1
			}
			blended := clamped*c.offPeak + (1-clamped)*c.peak

			ref := refInputs{
				weight: 342.7, pricePerKg: 22, timeH: 3, timeM: 47,
				watts: 250, rate: blended, failurePct: 8, prepMin: 5, laborRate: 12,
				machineCost: 1200, machineLife: 4000, monthlyFixed: 250, monthlyHours: 120,
			}
			expected := referenceQuoteConsumables(ref)

			tou := refToQuoteInputs(ref)
			tou.Energy = EnergyInputs{
				Mode:        "tou",
				PeakRate:    OptF{Value: c.peak, Present: true},
				OffPeakRate: OptF{Value: c.offPeak, Present: true},
				OffPeakFrac: OptF{Value: c.frac, Present: true},
			}
			bd := ComputeQuote(tou, DefaultEngineOptions)

			// An out-of-range frac is still a named input error even though
			// the clamp keeps the arithmetic sane -- same discipline as
			// quantity's below-min clamp.
			wantValid := c.frac >= 0 && c.frac <= 1
			if bd.Valid != wantValid {
				t.Errorf("valid = %v, want %v; warnings = %+v", bd.Valid, wantValid, bd.Warnings)
			}
			if got, want := lineAmount(t, bd, "electricity"), toCents(expected.elecCost); got != want {
				t.Errorf("electricity = %d, want %d (blended rate %v)", got, want, blended)
			}
			if got, want := lineAmount(t, bd, "failure"), toCents(expected.failureCost); got != want {
				t.Errorf("failure = %d, want %d", got, want)
			}

			// ... and identical to the flat-mode quote at the same rate, so a
			// blend that is right about the number but wrong about which
			// branch it takes cannot pass.
			flatBd := ComputeQuote(refToQuoteInputs(ref), DefaultEngineOptions)
			if bd.PerUnitTotal != flatBd.PerUnitTotal {
				t.Errorf("tou perUnitTotal = %d, flat-at-blended-rate = %d", bd.PerUnitTotal, flatBd.PerUnitTotal)
			}
		})
	}
}

// An explicitly empty fee schedule must be byte-identical to none: no phantom 0c line, and feeLines must not flip from [] to null.
func TestPricingCompatEmptyFeeScheduleEqualsAbsent(t *testing.T) {
	withSchedule := func(s *PricingFeeSchedule) CostBreakdown {
		inputs := hardwareCompatBaseInputs()
		inputs.Pricing = &PricingInputs{
			Strategy:         "markup",
			MarkupMultiplier: OptF{Value: 2, Present: true},
			TaxRatePct:       F(0), ShippingCharge: F(0), ShippingCost: F(0),
			FeeSchedule: s,
		}
		return ComputeQuote(inputs, DefaultEngineOptions)
	}

	absent := withSchedule(nil)
	empty := withSchedule(&PricingFeeSchedule{Fixed: []PricingFeeFixed{}, Percent: []PricingFeePercent{}})

	absentJSON, err := json.Marshal(absent)
	if err != nil {
		t.Fatalf("marshal absent: %v", err)
	}
	emptyJSON, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	if string(absentJSON) != string(emptyJSON) {
		t.Errorf("an explicitly empty feeSchedule diverged from an absent one:\n absent: %s\n empty:  %s", absentJSON, emptyJSON)
	}
	if p := absent.Pricing; p == nil || !p.Valid || p.Fees != 0 || len(p.FeeLines) != 0 {
		t.Errorf("baseline pricing = %+v, want valid with 0 fees and no fee lines", p)
	}
}
