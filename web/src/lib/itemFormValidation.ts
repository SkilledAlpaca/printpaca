/** Minimal shape of ItemsTab's ItemForm needed for validation, kept plain (no Svelte import) so the module
 * stays independently testable. */
interface ItemFormOption {
  name: string;
  delta: number | null;
}
interface ItemFormGroup {
  label: string;
  options: ItemFormOption[];
}
export interface ItemFormShape {
  price: number | null;
  stockMode: "ready_to_print" | "in_stock";
  stockQty: number | null;
  groups: ItemFormGroup[];
}

/** Limits mirrored from server/items.go (maxOptionGroups, maxOptionsPerGrp). */
export const MAX_OPTION_GROUPS = 5;
export const MAX_OPTIONS_PER_GROUP = 20;

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
  f.groups.forEach((g, gi) => {
    const where = g.label.trim() ? `Option group ${gi + 1} (${g.label.trim()})` : `Option group ${gi + 1}`;
    if (g.label.trim() === "") errors.push(`${where}: enter a label.`);
    else if (f.groups.some((h, hi) => hi < gi && h.label.trim().toLowerCase() === g.label.trim().toLowerCase())) {
      errors.push(`${where}: another group already uses this label.`);
    }
    if (g.options.length === 0) errors.push(`${where}: add at least one choice.`);
    const seen = new Set<string>();
    g.options.forEach((o, i) => {
      const name = o.name.trim();
      const oWhere = `${where}, choice ${i + 1}${name ? ` (${name})` : ""}`;
      if (name === "") errors.push(`${oWhere}: enter a name.`);
      else if (seen.has(name.toLowerCase())) errors.push(`${oWhere}: another choice in this group has the same name.`);
      seen.add(name.toLowerCase());
      if (o.delta === null || outOfRange(o.delta, DELTA_MIN, DELTA_MAX)) {
        errors.push(`${oWhere}: enter a valid price adjustment.`);
      }
    });
  });
  return errors;
}
