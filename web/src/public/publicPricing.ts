// Pure helpers shared by PublicQuotePage.svelte and ItemOrderPanel.svelte's quantity/price display.

/** Rounds a total to a per-unit price for display only ("$7.95 · $1.59 each"); never sent to the server,
 * which prices authoritatively. Returns null when quantity <= 0. */
export function perUnitCents(totalCents: number, quantity: number): number | null {
  if (!Number.isFinite(quantity) || quantity <= 0) return null;
  return Math.round(totalCents / quantity);
}

/** Fallback for a server that predates shop.maxOrderQty; mirrors defaultShopMaxOrderQty in server/config.go. */
export const DEFAULT_MAX_ORDER_QTY = 100;

/** Why a quantity is capped: a fixed per-order policy limit, or the stock on hand. */
export type QuantityLimit = "policy" | "stock";

/** The message for a quantity above its cap, or "" when it is within it. Names the limit ("Max 1,000 per
 * order.", "Only 3 in stock.") with a thousands separator. The page shows it instead of a total; it never
 * rewrites what the visitor typed. */
export function overCapMessage(quantity: number | null, max: number, limit: QuantityLimit = "policy"): string {
  if (quantity === null || !Number.isFinite(quantity) || quantity <= max) return "";
  const n = max.toLocaleString("en-US");
  return limit === "stock" ? `Only ${n} in stock.` : `Max ${n} per order.`;
}

/** The canonical text for a quantity typed in scientific notation ("1e3" -> "1000"), or null when the text
 * is already plain or isn't a whole number. A number input keeps "1e3" on screen while pricing 1000. */
export function normalizedQuantityText(raw: string): string | null {
  const t = raw.trim();
  if (!/^[+-]?(\d+\.?\d*|\.\d+)e[+-]?\d+$/i.test(t)) return null;
  const n = Number(t);
  return Number.isSafeInteger(n) ? String(n) : null;
}

/** Whether `quantity` is a whole number from 1 to `max`. A total is shown only for a valid quantity, so a
 * negative or fractional entry never renders a price. */
export function isValidQuantity(quantity: number | null, max: number): quantity is number {
  return quantity !== null && Number.isInteger(quantity) && quantity >= 1 && quantity <= max;
}

/** A phone number as a tel: target: digits and a leading plus only. */
export function telHref(phone: string): string {
  return `tel:${phone.replace(/[^\d+]/g, "")}`;
}

/** The pieces of an item's unit price, for "$22.00 + Large $4.00" under the total. */
export interface PriceGroup {
  id: string;
  required: boolean;
  options: { id: string; name: string; deltaCents: number }[];
}

/** The unit price written out: the base, then each chosen option that changes it ("$22.00 + Large $4.00",
 * "$22.00 − Small $1.00"). Empty when no chosen option changes the price, so there is nothing to itemize. */
export function unitBreakdown(baseCents: number, groups: PriceGroup[], selected: Record<string, string>, fmt: (cents: number) => string): string {
  const parts: string[] = [];
  for (const g of groups) {
    const o = g.options.find((x) => x.id === selected[g.id]);
    if (!o || o.deltaCents === 0) continue;
    parts.push(`${o.deltaCents > 0 ? "+" : "−"} ${o.name} ${fmt(Math.abs(o.deltaCents))}`);
  }
  return parts.length === 0 ? "" : `${fmt(baseCents)} ${parts.join(" ")}`;
}

/** The lowest unit price still open while required groups are unanswered, or null when the price is settled:
 * no required group is unanswered, or every unanswered one prices all its choices the same. Answered groups
 * count at their chosen delta, and an unanswered required group at its cheapest choice. */
export function fromUnitCents(baseCents: number, groups: PriceGroup[], selected: Record<string, string>): number | null {
  let cents = baseCents;
  let open = false;
  for (const g of groups) {
    const chosen = g.options.find((x) => x.id === selected[g.id]);
    if (chosen) {
      cents += chosen.deltaCents;
    } else if (g.required && g.options.length > 0) {
      const deltas = g.options.map((o) => o.deltaCents);
      cents += Math.min(...deltas);
      if (new Set(deltas).size > 1) open = true;
    }
  }
  return open ? cents : null;
}
