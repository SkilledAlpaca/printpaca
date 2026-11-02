import type { CostBreakdown, FieldWarning, OrderBreakdown, OrderPart } from "./engine/index";
import { breakdownHint } from "./quoteRow";
import { friendlyMessage } from "./format";

// Pure helpers over the server's OrderBreakdown, the Quote tab's single price object (a one-part quote is a
// one-part order). Two validity notions must not be confused (server/pricing_order.go): OrderBreakdown.valid
// and PricingResult.valid include the order pricing block, each part's CostBreakdown.valid is cost-only. "Can
// this be saved" is cost validity (a costed-but-unpriced draft must keep saving); "can this be printed or
// sent" is order validity.

const EMPTY_BREAKDOWN: CostBreakdown = {
  perUnit: [],
  perUnitTotal: 0,
  quantity: 1,
  batchTotal: 0,
  warnings: [],
  valid: false,
};

/** What the editor shows before the first server answer lands. */
export const EMPTY_ORDER: OrderBreakdown = { parts: [], batchTotal: 0, itemRevenue: 0, warnings: [], valid: false };

/** Part i's cost breakdown, or an empty placeholder while the order has no answer for that index. */
export function partBreakdown(order: OrderBreakdown, index: number): CostBreakdown {
  return order.parts[index]?.breakdown ?? EMPTY_BREAKDOWN;
}

/** Cost-only validity: an answer exists and every part's breakdown is valid; pricing is not consulted. */
export function costValid(order: OrderBreakdown): boolean {
  return order.parts.length > 0 && order.parts.every((p) => p.breakdown.valid);
}

/** The warning for `field` of part `index` among the order pricing warnings (server/pricing_order.go names
 * them "parts[i].<field>"). */
export function partPricingWarning(pricingWarnings: FieldWarning[], index: number, field: string): string {
  const w = pricingWarnings.find((x) => x.field === `parts[${index}].${field}`);
  return w ? friendlyMessage(w) : "";
}

/** Order pricing warnings no visible input renders: all but the order inputs' own fields and the per-part
 * override. */
export function otherPricingWarnings(pricingWarnings: FieldWarning[], inputFields: ReadonlySet<string>): FieldWarning[] {
  return pricingWarnings.filter((w) => !inputFields.has(w.field) && !/^parts\[\d+\]\.salePriceOverride$/.test(w.field));
}

/** One-line reason a cost breakdown isn't valid yet, naming the part when there are several. */
export function orderCostHint(order: OrderBreakdown, labels: readonly string[]): string {
  const i = order.parts.findIndex((p) => !p.breakdown.valid);
  if (i === -1) return breakdownHint(partBreakdown(order, 0));
  const hint = breakdownHint(order.parts[i]!.breakdown);
  return order.parts.length > 1 ? `${labels[i] ?? `Part ${i + 1}`}: ${hint}` : hint;
}

/** One-line reason printing is blocked, or "". Mirrors quoteRow.ts's printBlockerHint: invalid costs first,
 * then (only while selling) an unusable order price. */
export function orderPrintBlockerHint(order: OrderBreakdown, labels: readonly string[], sellingEnabled: boolean): string {
  if (!costValid(order)) return orderCostHint(order, labels);
  if (sellingEnabled && order.pricing?.valid !== true) {
    const pricingError = (order.pricing?.warnings ?? []).find((w) => w.severity === "error");
    return pricingError ? friendlyMessage(pricingError) : "Set a valid selling price to print a client quote.";
  }
  return "";
}

/** One line of a customer-facing document: part label, units, per-unit price and extended total, in cents. */
export interface OrderLine {
  label: string;
  quantity: number;
  unitPriceCents: number;
  lineTotalCents: number;
}

/** One priced line per part from the server's per-part resolved price (which honors overrides); empty
 * without a pricing block. Never reads pricing.recommendedPrice/resolvedPrice (order totals at 2+ parts). */
export function orderLines(order: OrderBreakdown): OrderLine[] {
  if (!order.pricing) return [];
  return order.parts.map((p: OrderPart) => ({
    label: p.label,
    quantity: p.breakdown.quantity,
    unitPriceCents: p.resolvedPrice,
    lineTotalCents: p.itemRevenue,
  }));
}

export function orderQuantity(order: OrderBreakdown): number {
  const q = order.parts.length === 1 ? order.parts[0]!.breakdown.quantity : 1;
  return Number.isFinite(q) && q >= 1 ? q : 1;
}
