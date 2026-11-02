// Pure helpers shared by PublicQuotePage.svelte and ItemOrderPanel.svelte's quantity/price display.

/** Rounds a total to a per-unit price for display only ("$7.95 · $1.59 each"); never sent to the server,
 * which prices authoritatively. Returns null when quantity <= 0. */
export function perUnitCents(totalCents: number, quantity: number): number | null {
  if (!Number.isFinite(quantity) || quantity <= 0) return null;
  return Math.round(totalCents / quantity);
}

export interface QuantityClampResult {
  value: number;
  /** Set only when `raw` was clamped down to `max`, so callers can show it as a one-shot notice. */
  message: string | null;
}

/** Clamps a quantity input to `max` (real stock or policy limit) in the form state itself, since the `max`
 * attribute's native affordances are easy to miss. `min` defaults to 1. */
export function clampQuantity(raw: number, max: number, min = 1): QuantityClampResult {
  if (raw > max) {
    return { value: max, message: `Only ${max} available — quantity reduced to ${max}.` };
  }
  if (raw < min) return { value: min, message: null };
  return { value: raw, message: null };
}
