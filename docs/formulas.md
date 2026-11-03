# Formulas and assumptions

`server/pricing.go` is the sole quote pricing implementation. `web/src/lib/engine` holds the shared quote types and constants, plus client-side helpers that never price a quote: the invoice totals preview (`invoice.ts`), cent rounding (`money.ts`), the Electricity and Filament calculators, and the G-code, STL, and 3MF parsers (see [architecture.md](architecture.md)). This document describes `server/pricing.go`'s behavior.

All money is computed as floats within a line, then rounded to integer cents half-away-from-zero. Each cost line is extended to the whole batch in float (`unit amount × quantity`; prep labor is already a batch amount) and rounded **once**, so a sub-cent per-unit cost still totals correctly at volume: 0.2 g of $20/kg filament is 0.4¢ per unit and exactly $40.00 over 10,000 units. `batchTotal` is the sum of the rounded batch lines.

The per-unit amount shown for a line is `round(batch line ÷ quantity)`, and `perUnitTotal` is `round(batchTotal ÷ quantity)`. Because each figure is rounded on its own, the displayed per-unit lines can differ from `perUnitTotal` by a cent or two; `batchTotal` is the authoritative figure. Pricing uses the unrounded unit cost (`batchTotal ÷ quantity`), not `perUnitTotal`. The unit price stays cent-rounded because invoices extend that price by the quantity. Invoices extend a price, never a cost, so cost lines don't need per-unit cent rounding.

A whole-number quantity divides in integer arithmetic, so a batch near the `int64` range doesn't lose cents to float conversion.

The one place a quantity is rounded separately is `itemRevenue` (the quantity-scaled revenue line under [Pricing](#pricing)), which uses Go's `math.Round` on the quantity. A quantity must be a whole number >= 1 to validate, so a fractional quantity (2.5, for example) is reachable only on an already-invalid quote: `batchTotal` extends each cost line by 2.5, while `itemRevenue` multiplies `resolvedPrice` by `round(2.5) = 3`, and the displayed numbers won't agree with each other.

## Cost per unit

| Line | Formula |
|---|---|
| material | weight_g × (1 + wastePct ÷ 100) × pricePerKg ÷ 1000 (wastePct defaults to 0) |
| electricity | Flat: hours × watts ÷ 1000 × rate. Simple time-of-use (`tou`): the same with rate = offPeakFrac·offPeakRate + (1−offPeakFrac)·peakRate, with offPeakFrac clamped to [0, 1] before blending. In simple mode, `offPeakFrac` is your estimate of the share of the print that runs off peak; printpaca doesn't derive it from a start time, and public quotes use `energy.tou.defaultOffPeakFrac`. Time-of-use schedule (`schedule`): watts ÷ 1000 × Σ (hours in each segment × that segment's rate), where the print's start time decides which tariff periods it falls in. See [Time-of-use schedule](#time-of-use-schedule). |
| depreciation | hours × machineCost ÷ lifespanHours (0 when lifespan unset) |
| failure buffer | failure base × p ÷ (1 − p), where p = failureRatePct ÷ 100; floored at 0 (see below for what's in the base) |
| prep labor | laborRate × prepMinutes ÷ 60, per batch, amortized ÷ quantity (laborPerUnit flips this) |
| overhead | hours × monthlyFixed ÷ monthlyHours (0 when monthly hours unset) |
| hardware (one line per attached entry) | unitCost × qtyPerUnit, per attached catalog entry (**admin quotes only**; see [Hardware scope](#hardware-scope)) |

```
batch line   = round(unit amount × qty)           (labor: round(batch labor))
batchTotal   = Σ batch lines
per-unit line = round(batch line ÷ qty)
perUnitTotal = round(batchTotal ÷ qty)
```

The failure buffer is `p ÷ (1 − p)` times the base, not `p` times the base. If each attempt fails independently with probability p, you expect p ÷ (1 − p) wasted attempts for every good part: 0.25 at p = 20%, 1.0 at p = 50%. `failureRatePct` therefore has a maximum of 90 (a buffer of 9× the base); at 100% no part would ever finish, so the value is rejected.

**A quantity below 1 (zero, negative, NaN, or not finite) is clamped to 1**, not rejected. The clamped value feeds `laborPerUnit`'s per-batch and per-unit split, `batchTotal`, `CostBreakdown.Quantity`, and `itemRevenue`, and the quote raises its own below-1 field warning, so a clamped quote is always flagged invalid even though every line computes.

The failure rate applies to the failure base: material + electricity + depreciation, plus only the attached hardware entries with `lostOnFailure: true`. A failed print doesn't re-incur your rent or (typically) your prep labor, and doesn't waste a hardware piece attached after a successful print (for example, glued on afterward). A piece consumed during printing (for example, a magnet placed mid-print) is wasted with the material, so its cost joins the base. The failure line's formula text and its `appliesTo` input name this basis as `consumables`.

A hardware line's displayed amount is always its plain `unitCost × qtyPerUnit`, never re-multiplied by the failure rate. An entry that joins the failure base enlarges the *failure buffer* line, not its own line. Absent or empty hardware contributes exactly 0 to every sum, byte-identical to a quote with no hardware component.

Two consequences of the base are not visible from the failure-buffer row alone:

- **`wastePct` raises the failure buffer too.** The `(1 + wastePct ÷ 100)` factor is part of the material line, which is part of the failure base under both options. An invalid `wastePct` (outside `[0, 100]`) therefore attaches its warning to the failure line as well as the material line, so `BreakdownTable` blanks both rows to "-".
- **The failure buffer never goes negative.** It is a cost reserve, not a credit: a negative base floors to 0. A negative base comes only from an input that is already a validation error (a negative weight, filament price, machine cost, or hardware unit cost), so the floor never changes a valid quote; it keeps an invalid one from displaying a refund.

### Hardware scope

The hardware catalog is **admin-quote-only**: the operator's internal cost basis for physical add-ons attached by hand on the Quote tab. The public quote page, the public preview endpoint, and the Requests verification replay build their inputs with no hardware field and price accordingly. A public visitor can't select hardware or know which add-ons a part needs, so applying the catalog to every public quote would charge visitors for parts their model may not use. `server/public_test.go` pins the scope in both public paths.

### Time-of-use schedule

Energy mode `schedule` prices electricity from a tariff and a start time, not from a share estimate. The `flat` and `tou` modes are unchanged.

The tariff has seasons (each a set of months), and each season has a weekday and a weekend schedule. A day type has a base rate with a name, plus up to 8 non-overlapping windows. Each window has a name, a start, an end, and a rate. Hours that no window covers use the base rate. Times are wall-clock times in the instance time zone (the top-level `timezone`), written `HH:MM`, and a window's end can be `24:00`. Saturdays, Sundays, and the dates listed in `holidays` use the weekend schedule.

For a part with per-unit time _h_, quantity _q_, and printer power _W_:

1. The print runs continuously from its start for _h_ × _q_ hours. Units print back to back.
2. printpaca splits that span at every window edge, at midnight, and where the season changes. A change of day also changes the day type, so a print that crosses midnight into Saturday switches to the weekend schedule.
3. Each segment costs _W_ ÷ 1000 × segment hours × segment rate. The electricity batch cost is the sum of the segments, and the per-unit cost is the batch cost ÷ _q_.
4. The breakdown lists the hours and cost for each named period. Periods with the same name, such as "Off-peak" in summer and winter, add together.

The hours are real elapsed hours, so a daylight saving change inside the print shortens or lengthens the wall-clock span but not the number of hours billed. A wall-clock time that happens twice resolves to the first occurrence. A wall-clock time that doesn't exist (the skipped hour in spring) resolves to the instant after the gap.

Each part has an optional print start, and the order has one that parts inherit. The start is wall-clock `YYYY-MM-DDTHH:MM` in the instance time zone. The quote saves the resolved start together with a copy of the tariff, so reopening or repricing a saved quote gives the same electricity cost even after you edit the tariff. Public quotes start at the next occurrence of `energy.schedule.defaultStartTime` at the time of the request. Spans longer than 366 days simulate the first 366 days and scale that average for the rest, which only matters for very large batches.

`server/energy_schedule.go` and `web/src/lib/engine/schedule.ts` implement the same algorithm. `testdata/schedule-vectors.json` holds shared test vectors (midnight, a season boundary, daylight saving changes in both directions, a holiday, quantity above 1, and a span past 366 days) that both the Go tests and the Vitest tests read.

## Pricing

- margin:  `exTax = unitCost ÷ (1 − margin%)`
- markup:  `exTax = unitCost × multiplier`
- price (recommended) = `exTax × (1 + tax% ÷ 100)`, which is `exTax` when the tax rate is 0
- revenue = `price × qty + shippingCharge`
- fees    = `Σ fixed + Σ pct% × revenue`  (per configured fee schedule; each line rounded). Every percent fee's `base` must be the literal string `"revenue"`; any other value (for example `"profit"` or `"subtotal"`) is rejected outright with an `invalid-enum` warning, not silently computed on revenue anyway.
- tax     = `price × qty × tax% ÷ (100 + tax%)` (not on shipping)
- net profit = `revenue − batchTotal − shippingCost − fees − tax`
- achieved margin % = `netProfit ÷ revenue × 100`, except when `revenue ≤ 0`, which reports 0% (see below)

`unitCost` is the unrounded per-unit cost, `batchTotal ÷ qty`, in cents.

**What margin is measured against.** The margin (or markup) applies to the **pre-tax price**, before tax is added and before marketplace fees. The tax-inclusive price grosses that amount up by the tax rate, so the tax carved out of it (`price × tax% ÷ (100 + tax%)`) equals `exTax × tax% ÷ 100`, and the pre-tax profit is the margin you chose. Marketplace fees are not solved into the price. They are a deduction from revenue, so net profit, and the achieved margin that follows from it, is lower than the margin you set. With a 25% margin, 25% tax, and a $0.20 listing fee plus 9.5% in percent fees, a $12.10 cost prices at $20.17 and leaves $1.92 net profit, a 9.5% achieved margin.

**Negative net profit.** When a valid quote's net profit is below 0 (fees and tax exceed the margin, a markup below 1, or a low override), the quote stays valid and gets a warning-severity field warning, `net-profit-negative`, on the field `netProfit`. The Price and profit card shows it above the totals.

**Achieved margin with no revenue.** A quote with `revenue ≤ 0` (a $0 override, for example) has no meaningful margin, so the engine reports 0% and the interface shows "n/a" (or "-" in the summary bar) instead of "0%". The net profit beside it still shows the loss, which is the cost of the giveaway.

**Cost basis must be positive to price *recommended* prices.** The unrounded `unitCost` (`batchTotal ÷ quantity`) must be greater than 0. A quote whose rounded `perUnitTotal` is 0 but whose `unitCost` is a fraction of a cent still prices. A zero or negative cost basis marks the quote invalid with a "cost basis must be positive to price" field warning, and `recommendedPrice` is never computed: the margin or markup division is skipped. **`resolvedPrice` is not gated the same way.** When `salePriceOverride` is present it resolves from the override regardless of the cost basis, and flows into `itemRevenue`, `revenue`, `tax`, `netProfit`, and `achievedMarginPct` like any resolved price. The quote stays invalid overall, but the override is honored.

### Tax is price-inclusive

Tax is **price-inclusive** (VAT-style): the quoted price already contains the tax you must remit. The tax line is carved *out of* the price, not added on top, and is subtracted from net profit as money that was never yours.

That is why the divisor is `100 + tax%`, not `100`. At 20% on a $120 gross sale the seller remits **$20** (`120 × 20 ÷ 120`), not $24 (`120 × 20 ÷ 100`). The exclusive method overstates the tax line and understates net profit and achieved margin on every priced quote with a non-zero rate.

Two other places must stay consistent: the Quote tab labels the field "Tax (included in price)", and both conversion paths to an invoice (saved quote to invoice in `web/src/lib/convertQuote.ts`, public request to invoice in `web/src/lib/convertRequest.ts`) set the invoice's `taxBp` to 0 when the quote's price already includes tax, because adding sales tax again would double-charge. When the price includes no tax (`pricing.taxRatePct` is 0), the invoice takes the `invoicing.salesTaxPct` default. See [Request to invoice](#request-to-invoice) for the request path.

### Margin ceiling

`marginPct` has **one** ceiling, 99.9 inclusive, enforced identically in `server/pricing.go` (`maxMarginPct`), `server/config.go` (startup and settings-save validation), and `web/src/lib/settingsRules.ts` (the inline Settings check). A value above it is refused where it was entered, with a message naming the field and the bound.

The bound doesn't protect `1 ÷ (1 − margin%)` mid-quote: `recommendedPrice` is guarded by `valid &&`, so the division never runs for an out-of-range margin. The alignment ensures no value passes config validation and then makes every quote unpriceable.

### Markup ceiling

`markupMultiplier` has the same one-ceiling rule at 1000 inclusive (`maxMarkupMultiplier`), agreed by `server/pricing.go`, `server/config.go`, `web/src/lib/settingsRules.ts`, and `config/config.schema.json`'s `maximum`, and pinned together by `server/config_test.go`. The multiplier is a plain product with no singularity, so the ceiling catches operator mistakes rather than protecting a formula. With the saturating arithmetic in [Input bounds](#input-bounds), it keeps `recommendedPrice` and `revenue` non-negative for any accepted multiplier.

### Sale price override

`salePriceOverride` resolves to `resolvedPrice` (the `price` in the [Pricing](#pricing) formulas) on **presence**, not truthiness; the recommended price applies only when the field is absent. An explicit `0` resolves to $0.00, a deliberate giveaway priced as one. An explicit negative value flows through revenue, tax, and net profit as a negative amount while the quote is marked invalid.

### Input bounds

Every money, quantity, and duration input carries an inclusive plausibility maximum in `server/pricing.go`:

| Field | Maximum | Constant |
|---|---|---|
| `weightG` | 100000 | `maxWeightG` |
| `pricePerKg`, `laborRate`, `machineCost`, `monthlyFixed`, `shippingCharge`, `shippingCost`, `salePriceOverride`, `hardware[i].unitCost`, fee-schedule fixed `amount` | 1,000,000,000 | `maxMoneyAmount` |
| `wastePct`, `taxRatePct`, fee-schedule percent `pct` | 100 | `maxPercentAmount` (100 literal for `wastePct`) |
| `failureRatePct` | 90 | `maxFailureRatePct`, enforced identically in `server/pricing.go`, `server/config.go` (defaults and every preset), `config/config.schema.json`, and `web/src/lib/settingsRules.ts`; see [Cost per unit](#cost-per-unit) |
| `timeHours`, `machineLifeHours`, `monthlyHours` | 100000 | `maxHoursInput` |
| `prepMinutes` | 6,000,000 | `maxMinutesInput` (`maxHoursInput × 60`) |
| `quantity`, `hardware[i].qtyPerUnit` | 100000 | `maxQuantity` |
| `watts` | 5000 | `maxPrinterWatts` |
| Schedule: seasons, windows per day type, holidays | 6, 8, 100 | `maxScheduleSeasons`, `maxScheduleWindows`, `maxScheduleHolidays`; schedule rates are also capped at `maxMoneyAmount` |
| `offPeakFrac` (simple `tou` mode only) | 1 | Rejected above 1 like any bound, and also clamped to [0, 1] inside `effectiveRate` (see [Cost per unit](#cost-per-unit)), so an invalid `offPeakFrac = 1.5` never extrapolates the blended rate past pure off-peak or peak. |
| `markupMultiplier` | 1000 | `maxMarkupMultiplier`, see [Markup ceiling](#markup-ceiling) above |
| `hardware` (array length, admin quotes only) | 200 | `maxConfigCollectionSize`, shared with the `config.yaml` hardware catalog's limit |
| `timeMinutes` | **59** | Semantic, not a plausibility bound: it is the minutes remainder, so 60 or more belongs in `timeHours`. `timeMinutes = 60` blanks the quote with an above-max warning. `splitPublicSeconds` in `server/public.go` converts raw seconds into an hours and minutes pair that never trips it. |

Bounds make an absurd magnitude on one field a named field error. They don't bound every product or sum built from several fields: `perUnitTotal`, `batchTotal`, `itemRevenue`, the fee total, and `netProfit` can exceed `int64` from inputs that are each within their maximum. For example, `monthlyFixed` 1e9 ÷ `monthlyHours` 1 × `timeHours` 1e5 reaches about 1e16 cents on the overhead line. A bare `int64(x)` conversion of an out-of-range float64 is implementation-defined in Go and wraps a huge positive amount into a large negative one on amd64.

**Saturating arithmetic** prevents that wrap. `roundCents` (every line and every price, fee, and tax figure) and the integer-domain `addCents`, `subCents`, and `mulCents` (batch and revenue totals, fee sums, net profit) clamp at ±`maxCents` (`int64`'s maximum magnitude). In-range values, and so every frozen golden vector, are unaffected. A quote can still report `Valid: true` while overstating an absurd magnitude, but it can never report a negative total, revenue, or price from individually legal inputs.

`shippingCharge` (what you bill the customer, part of revenue) and `shippingCost` (postage and packaging you pay, an expense) are tracked separately, so undercharged shipping shows up as a real loss.

## Multi-part quotes

A quote is an order of 1 to 20 parts. Each part is a full single-part quote input (weight, time, printer, material, energy, failure rate, prep, labor, overhead, quantity, hardware) without its own pricing block, plus a label (at most 120 characters) and an optional per-unit `salePriceOverride`. The order carries the pricing inputs once: strategy, margin or markup, fee schedule, tax rate, `shippingCharge`, and `shippingCost`. A single-part quote is a one-part order; every stored quote is an order (`{parts, pricing}`).

For each part _i_ with quantity _q_<sub>i</sub>:

1. The part is costed exactly like a single-part quote ([Cost per unit](#cost-per-unit)), giving `batchTotal`<sub>i</sub> (the sum of its batch-rounded lines) and `perUnitTotal`<sub>i</sub> = `round(batchTotal`<sub>i</sub> ÷ _q_<sub>i</sub>`)`. Prep labor amortizes over that part's own quantity, because each part is its own setup.
2. The order's strategy applies to the part's unit cost (`batchTotal`<sub>i</sub> ÷ _q_<sub>i</sub>) as in [Pricing](#pricing), including the tax gross-up, giving `recommended`<sub>i</sub>. `resolved`<sub>i</sub> is the part's `salePriceOverride` when present (see [Sale price override](#sale-price-override)), otherwise `recommended`<sub>i</sub>.
3. `itemRevenue`<sub>i</sub> = `resolved`<sub>i</sub> × _q_<sub>i</sub>.

For the order:

```
itemRevenue = Σ itemRevenue_i
revenue     = itemRevenue + shippingCharge
fees        = every fixed fee once + Σ round(revenue × pct ÷ 100) over the percent fees
tax         = round(itemRevenue × t ÷ (100 + t))     (price-inclusive, shipping excluded)
batchTotal  = Σ batchTotal_i
netProfit   = revenue − batchTotal − shippingCost − fees − tax
```

`achievedMarginPct` is `netProfit ÷ revenue × 100`, as for a single part. The order is valid only when every part is valid and the order pricing is valid; a part problem is named with its index (`parts[2].weightG`). A one-part order produces exactly the figures of the equivalent single-part quote, pinned by a test over every golden vector.

**Result fields.** The order-level `recommendedPrice` and `resolvedPrice` are the per-unit prices for a one-part order. For two or more parts `recommendedPrice` = Σ `recommended`<sub>i</sub> × qty<sub>i</sub> and `resolvedPrice` = Σ `resolved`<sub>i</sub> × qty<sub>i</sub> (the item revenue), figures comparable across parts of different quantities; they differ when a part carries an override. `OrderBreakdown.itemRevenue` repeats the item revenue explicitly; per-part prices are on each part of the result.

**Storage.** An order of two or more parts is saved as one unit priced at its item revenue: `quantity` = 1, `perUnitTotalCents` = `batchTotalCents` = Σ `batchTotal`<sub>i</sub>, and `recommendedPriceCents` = Σ recommended×qty and `resolvedPriceCents` = `itemRevenue`. Anything that multiplies a quote's price by its quantity (the quotes list, the dashboard, conversion to an invoice) stays correct without knowing about parts. `partCount` records the number of parts.

## Design decisions

Notable design decisions behind printpaca's cost and pricing math, and the reasoning behind each.

| Decision | Why |
|---|---|
| Failure % applies only to material + electricity + depreciation (plus lost-on-failure hardware), not labor or overhead (see [Cost per unit](#cost-per-unit) above) | A failed print doesn't re-incur your rent or (typically) prep labor |
| Reusable hardware catalog (name, unitCost, lostOnFailure) attached per quote as resolved values, not a live config reference | Owner-side add-on costs (magnets, findings, inserts) need per-quote pricing history that survives a later catalog edit, the same way an attached fee schedule does |
| Quantity is first-class in cost and pricing math, with batch labor amortization | Batch quoting is the common selling case |
| Configurable margin OR markup pricing strategy | Open-source, per-user pricing policy |
| Named fee schedules as config data, each with a provenance note | Fees change; users are worldwide |
| Typed validation with per-field warnings; an invalid value renders as "-", never a wrong number | Silent zeros produce confidently wrong quotes |
| Integer cents everywhere | Reproducibility |
| Cost lines extend to the batch in float and round once per batch line | Per-unit cent rounding drops sub-cent costs (0.4¢ per unit becomes $0.00 over 10,000 units); rounding once at the batch bounds the error to half a cent per line |
| Failure buffer is base × p ÷ (1 − p), and `failureRatePct` tops out at 90 | The expected number of wasted attempts per good part is p ÷ (1 − p); 100% has no finite expectation |
| Margin and markup apply to the pre-tax price; tax is added on top; fees are deducted afterward | The operator's chosen margin holds before tax and fees, and a quote that fees push below zero is flagged with `net-profit-negative` instead of looking healthy |
| Full-file streamed G-code scan, explicit parse status, prefers slicer-reported weight, configurable filament diameter | Correctness |
| Optional tax % line, computed **price-inclusive** (VAT-style) | EU/AU audience; the quoted price is what the customer pays, and the tax inside it was never the seller's money |
| Hardware is admin-quote-only, never applied to a public quote | A visitor cannot select add-ons or know which ones a part needs; applying the catalog to everyone would charge for parts their model may not use |
| The failure buffer floors at zero | A failure buffer is a cost reserve, never a credit |
| `salePriceOverride` resolves on presence, so an explicit 0 means $0.00 | A deliberate giveaway must be priced as one, not silently replaced by the recommended price |
| Separate `shippingCharge` (revenue) and `shippingCost` (postage + packaging expense) in net profit | Under-charged shipping is a real loss that a single combined "shipping" field would hide |
| Multi-part quotes: fees apply once to the order, not per part | A marketplace or payment fee is charged on the sale, not on each file; a fixed listing fee per part would overstate cost, and rounding a percent fee per part would drift a cent from the real charge |
| Multi-part quotes: tax is computed once on the order's item revenue | Tax is on the sale, and rounding once matches what the customer is billed; per-part rounding can differ by a cent per part |
| Multi-part quotes: prep labor amortizes per part over that part's quantity | Each part is its own setup, so a part printed once carries its full prep cost and a part printed 20 times spreads it |
| Multi-part quotes: an order of 2+ parts is stored as one unit (quantity 1) priced at its item revenue | Every reader of the stored price columns multiplies price by quantity; one unit at the order's revenue stays correct for the list, the dashboard and invoice conversion without teaching them about parts |
| Multi-part quotes: a one-part order is the single-part quote | One code path (`computePricingLines`) prices both, and a test over every golden vector keeps them identical |

## G-code parsing

`parseGcode` scans the full uploaded file as a stream (no fixed-size head/tail window), looking for slicer-emitted metadata comments in this priority order:

1. **Slicer-reported weight**: the most direct and accurate source when present. Recognized comment formats, matched case-insensitively. A value may be a comma-separated list (multi-extruder output); the entries are summed, and the sum is used only if it's finite and positive:
   - `; total filament weight [g] = <n>` / `; total filament used [g] = <n>`: `:` or `=` separator (PrusaSlicer/OrcaSlicer/Bambu family, higher-ranked, preferred when multiple comments match)
   - `; filament used [g] = <n>`: `=` only (PrusaSlicer/OrcaSlicer family)
   - `; filament weight: <n>`: `:` or `=` separator (generic fallback form)
2. **Slicer-reported length**, when no weight comment is found. Recognized formats: `;Filament used: <n>m` (Cura) and `; filament used [mm] = <n>` (PrusaSlicer/OrcaSlicer). Weight is then derived as:
   ```
   weight_g = length_mm × π × (filamentDiameterMm / 2)² ÷ 1000 × density_g_per_cm3
   ```
   using the filament diameter from the **selected printer profile** (not a hard-coded 1.75 mm) and the density from the **selected material**.
3. **Nothing found**: result status is `"not-found"`, and the UI shows a visible message listing exactly which comment formats were searched for, rather than silently defaulting to zero.

Print time is extracted separately, independent of the weight/length search, also ranked so the most specific match wins when several are present:
- Cura: `;TIME:<seconds>`
- PrusaSlicer: `; estimated printing time (normal mode) = XdXhXmXs` (and other parenthesized-mode variants)
- OrcaSlicer/Bambu: `; total estimated time = ...` (preferred) or `; model printing time = ...` (fallback), matched whether the comment starts the line or appears mid-line

If the slicer also emits its own cost estimate, printpaca captures and displays it labeled as "slicer's own estimate" for the user's comparison; **it is never used in printpaca's own math**, since it reflects whatever pricing assumptions were baked into the slicer profile, not this app's configuration.

The result carries `{ source: "reported-weight" | "derived-from-length" | "none", slicer?: string }` so the UI can tell the user which path actually produced the numbers on screen, instead of presenting a number with no indication of its provenance.

The engine's test suite holds fixtures for Cura, PrusaSlicer, OrcaSlicer/Bambu, and Snapmaker Orca (U1) comment formats.

## Verification

`server/pricing.go`'s cost and pricing math is pinned by a frozen golden-vector suite in `server/pricing_compat_test.go` (vectors frozen 2026-08-15). The suite pins the following, with quantity fixed at 1:

- **Cost-line grid** (144 cases: 4 weights × 4 print-time combinations × 3 failure rates × 3 machine cost/lifespan pairs, including zero weight, zero time, and zero machine cost/lifespan as edge values). Each per-line cost (material, electricity, depreciation, labor, overhead, failure) matches **exactly, to the cent**; the summed per-unit total matches **within 3¢**, a documented rounding-order difference (printpaca rounds and sums each line; the golden vectors sum floats and round once), not test slack.

  The grid runs against an independent reference written from the failure-base rule above.
- **Pricing grid** (3 cases, fixed 30% margin, the single Etsy-US fee schedule): recommended price, revenue, fees, and net profit each match **within 5¢**, same rounding-order rationale as above.

Covered by targeted vectors in the same file rather than by the grid, since the reference implementations have no concept of them: batch labor amortization across quantity, the hardware component (see below), and the negative-failure-base floor.

Boundary margins (near 100%), other fee schedules, inclusive input maxima, and the tax formula are covered by the unit suites instead (`server/pricing_test.go` plus the shared `testdata/quote-vectors.json` fixture), not by this golden-vector grid: any change to core cost/pricing math that breaks the pinned cases fails this suite immediately.

The hardware component has no TypeScript reference to port, so `server/pricing_compat_test.go` pins it with direct expected-cents vectors: one entry with `lostOnFailure: false`, one `true`, mixed entries, `qtyPerUnit` > 1, and a check that an empty hardware array produces byte-identical output to an absent one. The pinned cases above carry no hardware: the 144-case consumables grid is unmodified, and the 3 margin/fee cases run against `referenceQuoteConsumables`.

## Invoice totals

`computeTotals` in `server/invoices.go` is the only code that decides stored invoice totals. `invoiceTotals` in `web/src/lib/engine/invoice.ts` is the client-side preview of it, and both read the shared vector table in `testdata/invoice-totals.json`, so they can't drift.

```
lineAmount      = roundHalfUp(quantity × unitPriceCents) − lineDiscountCents
subtotal        = Σ lineAmount
taxableBase     = max(0, Σ lineAmount over lines whose kind is not "shipping" − invoiceDiscountCents)
tax             = roundHalfUp(taxableBase × taxBp ÷ 10000)
total           = max(0, subtotal − invoiceDiscountCents) + tax
```

- **Rounding.** `roundHalfUp(x)` is `floor(x + 0.500000001)`. The `1e-9` epsilon corrects IEEE 754 ties that land just under .5, such as `1.005 × 100`, which is stored as `100.49999999999999` and would otherwise round down to 100. Go and TypeScript use the same constant, so a preview and a stored total agree. Quantity is the only float on an invoice; every other amount is integer cents or basis points.
- **Line kind.** Each line has a `kind` of `item` (the default) or `shipping`. Only `shipping` lines are exempt from tax; they still count toward the subtotal and the total. The description is free text and never affects tax.
- **Discounts.** A line discount is taken off its own line and can push the line negative. The invoice discount comes off the taxable base (items only) before tax, and off the whole subtotal for the total. Both floors are zero, so a discount larger than the invoice never produces a negative tax or total.
- **Tax rate.** `taxBp` is the rate in basis points, 0 to 3000. A new invoice that doesn't send `taxBp` takes `invoicing.salesTaxPct`. This is sales tax added on top; it is not the same setting as `pricing.taxRatePct`, which is already inside a quoted price.
- **Where `taxBp` is 0.** A quote or public request whose price already included tax converts with `taxBp = 0`, so the invoice doesn't add the tax a second time. See [Request to invoice](#request-to-invoice).

## Payments and balance

- A payment can be recorded only on a `sent` invoice. Its amount is 1 cent or more, and it carries a `paidDate` (`YYYY-MM-DD`).
- `balance = total − Σ payments`. Overpaying is allowed, so the balance can go negative.
- When the payments reach the total (`Σ payments ≥ total`), the invoice flips from `sent` to `paid` in the same transaction. Deleting a payment that leaves a `paid` invoice below its total flips it back to `sent`.
- Marking a `sent` invoice `paid` by hand adds one balancing payment for the open balance, with method `other`, note `marked as paid`, and today's date in the configured time zone. Moving `paid` back to `sent` removes that balancing payment and any other payment with the same method and note.
- Canceling an invoice that has payments is refused with a 409 that asks you to remove the payments first, so a refunded deposit can't keep counting as revenue. Deleting a payment is allowed on `sent` and `paid` invoices, and is refused on drafts and canceled invoices.

## Dashboard

Every figure on the Dashboard is computed on the server from the `payments`, `invoices`, and `quotes` tables.

- **Time zone.** The top-level `timezone` setting (an IANA name, default `UTC`) decides what "today", "this month", and "this year" mean. Stored timestamps stay UTC; payments carry a plain local date. At 8:30 PM on October 31 in Los Angeles, "this month" is October, even though it is already November in UTC. The zone data is embedded in the binary, so the setting works in the scratch image.
- **Revenue.** Revenue is cash basis: the sum of payment amounts bucketed by `paidDate`, not by invoice date or status. "Revenue this month" and "Revenue this year" cover the half-open local calendar month and year. "Revenue by month" shows the last 12 local months, with empty months as zero. Revenue includes the sales tax collected on an invoice and any overpayment, because it sums what was paid.
- **Outstanding.** The sum of `total − paid` over `sent` invoices that still have a positive balance. Drafts, paid, and canceled invoices are excluded.
- **Overdue.** A count of `sent` invoices with a positive balance and a `dueDate` earlier than today in the configured zone.
- **Quote tiles.** "Quotes saved this month" counts `quotes.savedAt` in the local month. "Invoiced share" is the share of those quotes that have been converted. "Average margin" is the unweighted mean of `achievedMarginPct` over all invoiced quotes, taken when each quote was saved, so a $2 quote counts as much as a $2,000 one.

## Top items

The Top items panel ranks gallery items by units over the last 30, 90, or 365 days. It reports **booked sales**, not payments, so its revenue won't match the revenue tiles.

- **Gallery orders** count when the order is finalized. They come from the append-only `item_sales` ledger, so deleting the request later doesn't remove the sale. Revenue is the order total.
- **In-person sales** are invoice lines linked to a gallery item, counted while their invoice is `sent` or `paid` and dated by the invoice's `issueDate`. An unpaid sent invoice counts. The query runs on every load, so canceling an invoice drops its lines, and moving a `paid` invoice back to `sent` keeps them.
- **Line revenue.** An in-person line's revenue is its line amount less its share of the invoice-level discount, before tax:

  ```
  lineRevenue = round((roundHalfUp(quantity × unitPriceCents) − lineDiscountCents)
                      × max(0, subtotal − invoiceDiscount) ÷ subtotal)
  ```

  The share is taken over the whole invoice subtotal, shipping lines included, because that is how the invoice discount reduces the total. When the invoice has no discount or its subtotal isn't positive, the line amount is used as is. Each line is rounded on its own, so the lines of one invoice can differ from the discounted total by a cent or two.

## Weight estimate from an STL or 3MF

A model file carries geometry but no slicer settings, so printpaca estimates the weight from the model's volume. It is a rough stand-in until you slice.

```
volume_cm3 = |Σ signed tetrahedron terms| ÷ 6 ÷ 1000
weight_g   = volume_cm3 × density_g_per_cm3 × solidityPct ÷ 100
```

- **Volume.** The parser sums one signed tetrahedron term per triangle (the divergence theorem), so it needs a closed mesh. STL files are read as binary when the header's triangle count accounts for the whole file length, and as ASCII otherwise. In a 3MF, each `<mesh>` is summed on its own and the absolute value is taken per mesh, so objects with opposite winding can't cancel. Coordinates are scaled to millimeters from the model's `unit` (micron, millimeter, centimeter, meter, inch, or foot; millimeter when unset). Parts stored in other files of the archive (the Production Extension's `p:path` references) are followed, and a part referenced N times counts N times. Item and component transforms (placement, rotation, scale) aren't applied.
- **Density** comes from the selected material, in g/cm³.
- **Solidity** is an operator-adjustable percentage, 30 by default and clamped to 5 to 100. It stands in for infill plus shells; a cleared or invalid value falls back to 30. The result is shown as an estimate, with its inputs. printpaca never estimates print time from volume: the time fields stay blank with a note that the file has no slicer time, and the quote can't be priced until you enter the print time from your slicer. The Price and profit card lists what is still missing, and each item links to its field.
- **Sliced 3MF.** A 3MF that carries slicer metadata (Bambu Studio and OrcaSlicer `slice_info.config`, or an embedded g-code file) uses the slicer's own weight and time instead. The geometry estimate applies only when no such metadata exists.
- **Limits.** Files over the upload cap, or a 3MF model entry over its geometry cap, are reported as too large rather than estimated. An empty or non-closed file reports an empty or malformed result and no weight.

## Request to invoice

Converting a public request into an invoice (the Requests tab's **Convert to invoice**) goes through `web/src/lib/convertRequest.ts`, which mirrors the saved-quote path in `web/src/lib/convertQuote.ts`.

- **Product line.** One line with the request's quantity and `round(priceCents ÷ quantity)` as the unit price. A gallery item order uses the item name and option, and is never linked to the gallery item, because finalizing the order already counted the sale and took the stock.
- **Shipping line.** The public quote page shows the visitor `pricing.defaultShippingCharge` beside the price. That amount is stored on the request at submit (`shippingCents`), so the invoice adds the same `Shipping` line (kind `shipping`, untaxed) the visitor saw, even if the setting changes afterward. A request with no stored shipping adds no line. Gallery item orders carry no shipping.
- **Tax.** A quote request's public price already includes `pricing.taxRatePct` when that setting is above 0, so the invoice gets `taxBp = 0` and doesn't add tax again. The request stores that basis at submit (`taxInclusive`), so a later change to `pricing.taxRatePct` doesn't alter the conversion. This is the same rule as `convertTaxBp` for saved quotes. When `pricing.taxRatePct` was 0 at submit, and for gallery item orders (an item's listed price has no tax in it), the invoice takes the `invoicing.salesTaxPct` default, applied to the product line only.
- **Check.** With `pricing.taxRatePct = 20`, a $12.00 price, and $4.00 shipping, the visitor saw $16.00 and the invoice totals $16.00 with no tax line, not $14.40.
