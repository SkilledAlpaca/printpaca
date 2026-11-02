import type { CostBreakdown } from "./engine/index";
import type { SavedQuote } from "./api";
import { friendlyMessage, formatDateTime } from "./format";

// weight and time start blank on every fresh quote, so an error on either gets the generic "fill these in"
// copy; any other error names its actual field so the hint never dead-ends.
const BREAKDOWN_FALLBACK_FIELDS = new Set(["weightG", "timeHours", "timeMinutes"]);
const BREAKDOWN_DEFAULT_HINT = "Enter model weight and print time above to see costs.";

/** One-line reason a cost breakdown isn't valid yet; the single source for BreakdownTable's empty state and
 * the disabled-action hints in QuoteTab and HistoryPanel. */
export function breakdownHint(breakdown: CostBreakdown): string {
  const errorWarnings = breakdown.warnings.filter((w) => w.severity === "error");
  const firstError = errorWarnings[0];
  if (!firstError || errorWarnings.some((w) => BREAKDOWN_FALLBACK_FIELDS.has(w.field))) {
    return BREAKDOWN_DEFAULT_HINT;
  }
  return friendlyMessage(firstError);
}

/** One-line reason QuoteTab's print buttons are disabled, or "". Mirrors the buttons' gating exactly (valid
 * breakdown, and a valid price only when selling) so the hint can't disagree; QuoteTab derives canPrint
 * from this being "". */
export function printBlockerHint(breakdown: CostBreakdown, sellingEnabled: boolean): string {
  if (!breakdown.valid) return breakdownHint(breakdown);
  if (sellingEnabled && breakdown.pricing?.valid !== true) {
    const pricingError = (breakdown.pricing?.warnings ?? []).find((w) => w.severity === "error");
    return pricingError ? friendlyMessage(pricingError) : "Set a valid selling price to print a client quote.";
  }
  return "";
}

/** A saved quote's savedAt (ISO 8601) as a locale string, or "" if unparseable. Uses dateStyle/timeStyle
 * because the default toLocaleString shows seconds nobody reads in the ledger's widest column. */
export function formatSavedAt(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "" : formatDateTime(iso);
}

/** The per-unit price for list rows (cents): resolvedPrice (honors an override) if finite, else
 * recommendedPrice if finite, else null for a cost-only save. Dependency-free so display and sort code can
 * import it without convertQuote.ts. */
export function displayPriceCents(q: SavedQuote): number | null {
  if (typeof q.resolvedPrice === "number" && Number.isFinite(q.resolvedPrice)) return q.resolvedPrice;
  if (typeof q.recommendedPrice === "number" && Number.isFinite(q.recommendedPrice)) return q.recommendedPrice;
  return null;
}

/** Price and margin for one saved-quote row. `marginPct` is the percent of the per-unit price that isn't
 * per-unit cost; null when there's no positive price or `perUnitTotal` is non-finite (which would poison
 * the subtraction into NaN). The batch cost isn't included; read `q.batchTotal` for that. */
export function quoteRowStats(q: SavedQuote): { priceCents: number | null; marginPct: number | null } {
  const priceCents = displayPriceCents(q);
  const marginPct =
    priceCents !== null && priceCents > 0 && Number.isFinite(q.perUnitTotal)
      ? ((priceCents - q.perUnitTotal) / priceCents) * 100
      : null;
  return { priceCents, marginPct };
}
