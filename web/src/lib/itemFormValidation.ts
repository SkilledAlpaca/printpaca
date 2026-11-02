/** Minimal shape of ItemsTab's ItemForm needed for validation, kept plain (no Svelte import) so the module
 * stays independently testable. */
interface ItemFormOption {
  name: string;
  delta: number | null;
}
export interface ItemFormShape {
  price: number | null;
  stockMode: "ready_to_print" | "in_stock";
  stockQty: number | null;
  options: ItemFormOption[];
}

// Mirror the min/max attributes on ItemsTab.svelte's LabeledNumber fields; named constants so the two places
// can't drift. Price and stock share one range; option deltas get their own (a delta may be negative).
const AMOUNT_MIN = 0;
const AMOUNT_MAX = 1000000;
const DELTA_MIN = -1000000;
const DELTA_MAX = 1000000;

/** True when v is unusable as an amount: non-finite (including absurd finite values like 1e308) or outside
 * [min, max]. A non-finite value would pass through roundCents (non-finite maps to 0) and silently save
 * $0.00; an out-of-range one typed past the max attribute isn't blocked by native validation when set
 * programmatically. */
function outOfRange(v: number, min: number, max: number): boolean {
  return !Number.isFinite(v) || v < min || v > max;
}

/** Every "enter a valid amount" problem that toInput's `?? 0` fallbacks would hide: null, non-finite and
 * out-of-range. Returns [] when ready to save. */
export function validateItemForm(f: ItemFormShape): string[] {
  const errors: string[] = [];
  if (f.price === null || outOfRange(f.price, AMOUNT_MIN, AMOUNT_MAX)) {
    errors.push("Price: enter a valid amount.");
  }
  if (f.stockMode === "in_stock" && (f.stockQty === null || outOfRange(f.stockQty, AMOUNT_MIN, AMOUNT_MAX))) {
    errors.push("Quantity in stock: enter a valid amount.");
  }
  f.options.forEach((o, i) => {
    if (o.delta === null || outOfRange(o.delta, DELTA_MIN, DELTA_MAX)) {
      errors.push(`Option ${i + 1}${o.name ? ` (${o.name})` : ""}: enter a valid price adjustment.`);
    }
  });
  return errors;
}
