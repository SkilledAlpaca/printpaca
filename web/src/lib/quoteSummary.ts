import type { OrderBreakdown } from "./engine/index";
import { costValid, orderQuantity } from "./orderView";

export interface QuoteSummaryStats {
  /** Batch quantity for display; 1 for a multi-part order or an unusable input. */
  quantity: number;
  /** Order production cost in cents (sum of the parts' batch totals); null when any part is invalid. */
  costCents: number | null;
  /** Customer item price in cents (sum of resolved price x quantity); null unless pricing is on and valid. */
  priceCents: number | null;
  profitCents: number | null;
  marginPct: number | null;
  /** Whether the customer group renders stats rather than the enable-pricing CTA. */
  pricingEnabled: boolean;
}

/** Projection of an OrderBreakdown into the summary bar's headline numbers. order.pricing exists only when
 * selling is on, but sellingEnabled is taken explicitly so the bar never shows stale pricing from before
 * the toggle flipped. The price is itemRevenue, never pricing.resolvedPrice: with 2+ parts the latter is
 * an order total and with one part a per-unit price, so reading it would make the bar jump when a second
 * part is added. */
export function quoteSummaryStats(order: OrderBreakdown, sellingEnabled: boolean): QuoteSummaryStats {
  const p = sellingEnabled ? order.pricing : undefined;
  const pricingValid = p?.valid === true;
  return {
    quantity: orderQuantity(order),
    costCents: costValid(order) ? order.batchTotal : null,
    priceCents: pricingValid ? order.itemRevenue : null,
    profitCents: pricingValid ? p.netProfit : null,
    // No revenue means no meaningful margin (the engine reports 0%); show "-" rather than a misleading 0%.
    marginPct: pricingValid && p.revenue > 0 ? p.achievedMarginPct : null,
    pricingEnabled: sellingEnabled && p !== undefined,
  };
}
