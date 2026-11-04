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

/** The per-order item quantity ceiling; mirrors maxItemOrderQty in server/items.go. */
export const MAX_ITEM_ORDER_QTY = 10000;

/** Why a quantity is capped: a fixed per-order policy limit, or the stock on hand. */
export type QuantityLimit = "policy" | "stock";

/** Clamps a quantity input to `max` (real stock or policy limit) in the form state itself, since the `max`
 * attribute's native affordances are easy to miss. `min` defaults to 1. The message names the limit
 * ("Max 100 per order.", "Only 3 in stock."). */
export function clampQuantity(raw: number, max: number, min = 1, limit: QuantityLimit = "policy"): QuantityClampResult {
  if (raw > max) {
    return { value: max, message: limit === "stock" ? `Only ${max} in stock.` : `Max ${max} per order.` };
  }
  if (raw < min) return { value: min, message: null };
  return { value: raw, message: null };
}

/** Whether `quantity` is a whole number from 1 to `max`. A total is shown only for a valid quantity, so a
 * negative or fractional entry never renders a price. */
export function isValidQuantity(quantity: number | null, max: number): quantity is number {
  return quantity !== null && Number.isInteger(quantity) && quantity >= 1 && quantity <= max;
}

/** The 429 copy: no jargon, and the shop's own contact details when it has published them. */
export function rateLimitMessage(branding: { email: string; phone: string }): string {
  const contact = [branding.email ? `email ${branding.email}` : "", branding.phone ? `call ${branding.phone}` : ""].filter(
    (s) => s !== "",
  );
  const tail = contact.length > 0 ? ` or ${contact.join(" or ")}` : "";
  return `We've had a lot of requests from your connection. Try again later${tail}.`;
}
