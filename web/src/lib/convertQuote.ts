import { invoiceTotals, toCents, type OrderBreakdown } from "./engine/index";
import type { SavedQuote } from "./api";
import { pctToBp, type InvoiceLineInput } from "./invoicesApi";
import { displayPriceCents } from "./quoteRow";
import { applyTransition } from "./quoteStatus";

/** The saved snapshot is a {version, parts, order} form state. Flattens it: order-level keys come from
 * `order`; `quantity` is the sole part's for one part, 1 for multi-part (stored as one unit;
 * server/quotestore.go computeQuoteOutputs). Null for anything else. */
function flatView(inputs: unknown): Record<string, unknown> | null {
  if (!inputs || typeof inputs !== "object") return null;
  const rec = inputs as Record<string, unknown>;
  if (rec.version !== 1 || !Array.isArray(rec.parts)) return null;
  const order = (rec.order && typeof rec.order === "object" ? rec.order : {}) as Record<string, unknown>;
  const first = (rec.parts[0] && typeof rec.parts[0] === "object" ? rec.parts[0] : {}) as Record<string, unknown>;
  return { ...order, quantity: rec.parts.length === 1 ? first.quantity : 1 };
}

export function extractQuantity(inputs: unknown): number {
  const view = flatView(inputs);
  if (view && "quantity" in view) {
    const q = view.quantity;
    if (typeof q === "number" && Number.isFinite(q) && q > 0) return q;
  }
  return 1;
}

/** The single invoice line for a saved quote; money fields are already cents. Price ladder: resolvedPrice,
 * then recommendedPrice, then batch cost per unit. The cost fallback is only safe behind a quoteHasPrice()
 * guard; any new call site must check it first or it would invoice bare cost as a price. */
export function invoiceLineFromQuote(q: SavedQuote): InvoiceLineInput {
  const quantity = extractQuantity(q.inputs);
  const perUnit = displayPriceCents(q) ?? q.batchTotal / quantity;
  return {
    description: q.label,
    quantity,
    unitPriceCents: Math.max(0, Math.round(perUnit)),
    discountCents: 0,
    quoteId: q.id,
  };
}

/** Batch-level shipping the customer pays, in dollars, from the untyped snapshot; 0 when absent/unusable. */
export function extractShippingCharge(inputs: unknown): number {
  const view = flatView(inputs);
  if (view && "shippingCharge" in view) {
    const s = view.shippingCharge;
    if (typeof s === "number" && Number.isFinite(s) && s > 0) return s;
  }
  return 0;
}

/** The description of the Shipping line a conversion adds. Display text only: the line's kind ("shipping")
 * keeps it out of the taxed subtotal (tax applies to item revenue only). Matches server/invoices.go's
 * quoteConversionShippingDescription. */
export const SHIPPING_LINE_DESCRIPTION = "Shipping";

/** True for a multi-part order (2 or more parts). */
export function isMultiPart(q: SavedQuote): boolean {
  return q.partCount >= 2;
}

/** One invoice line per part: "<quote label> - <part label>", the part's quantity and resolved per-unit
 * price. Needs the server's OrderBreakdown (the saved quote lacks per-part prices); without it, no lines. */
function orderInvoiceLines(q: SavedQuote, order: OrderBreakdown | null | undefined): InvoiceLineInput[] {
  if (!order || !order.pricing) return [];
  return order.parts.map((p) => ({
    description: `${q.label} - ${p.label}`,
    quantity: p.breakdown.quantity,
    unitPriceCents: Math.max(0, Math.round(p.resolvedPrice)),
    discountCents: 0,
    quoteId: q.id,
  }));
}

/** All invoice lines: the product line plus a Shipping line when shipping was charged, once per batch. */
export function invoiceLinesFromQuote(q: SavedQuote, order?: OrderBreakdown | null): InvoiceLineInput[] {
  const multi = isMultiPart(q);
  const lines = multi ? orderInvoiceLines(q, order) : [invoiceLineFromQuote(q)];
  // No part lines (order answer missing or loading) means nothing to invoice; never a Shipping-only invoice.
  if (multi && lines.length === 0) return [];
  const shipping = extractShippingCharge(q.inputs);
  if (shipping > 0) {
    lines.push({
      description: SHIPPING_LINE_DESCRIPTION,
      kind: "shipping",
      quantity: 1,
      unitPriceCents: toCents(shipping),
      discountCents: 0,
      quoteId: q.id,
    });
  }
  return lines;
}

/** True when the quote has a customer-facing price (resolvedPrice or recommendedPrice present and finite),
 * not a cost-only save. */
export function quoteHasPrice(q: SavedQuote): boolean {
  return displayPriceCents(q) !== null;
}

/** The tax rate (percent) baked into the snapshot at save time. Non-zero only when sellingEnabled is true,
 * since taxRatePct can be stale from a prior selling state. */
export function extractTaxRatePct(inputs: unknown): number {
  const rec = flatView(inputs);
  if (rec) {
    if (rec.sellingEnabled === true) {
      const t = rec.taxRatePct;
      if (typeof t === "number" && Number.isFinite(t) && t > 0) return t;
    }
  }
  return 0;
}

/** 0 when the quoted price already includes tax (so the invoice doesn't add it again), else null meaning
 * "apply the server's default sales tax". */
export function convertTaxBp(q: SavedQuote): number | null {
  if (quoteHasPrice(q) && extractTaxRatePct(q.inputs) > 0) return 0;
  return null;
}

/** Marks a converted quote `accepted` with a log entry unless it already is (re-invoicing adds no duplicate
 * entry). Checks stored status only, so an expired-for-display quote still transitions. Pure; the caller
 * supplies `at` and merges `invoicedAs` afterwards. */
export function applyConvertStatus(q: SavedQuote, at: string): SavedQuote {
  if (q.status === "accepted") return q;
  return applyTransition(q, "accepted", at);
}

/** Live totals preview via the shared invoiceTotals (mirrored by server/invoices.go's computeTotals).
 * invoiceDiscountCents is 0 at conversion, so its clamps never engage. */
export function convertPreview(
  q: SavedQuote,
  defaultSalesTaxPct: number,
  order?: OrderBreakdown | null,
): { lines: InvoiceLineInput[]; taxBp: number; subtotalCents: number; taxCents: number; totalCents: number } {
  const lines = invoiceLinesFromQuote(q, order);
  const taxBp = convertTaxBp(q) ?? pctToBp(defaultSalesTaxPct);
  const totals = invoiceTotals(lines, 0, taxBp);
  return { lines, taxBp, subtotalCents: totals.subtotalCents, taxCents: totals.taxCents, totalCents: totals.totalCents };
}

/** The chosen client's name, else the quote's customer name, else "Walk-in". */
export function convertClientName(chosenName: string | undefined, q: SavedQuote): string {
  return chosenName || q.clientName?.trim() || "Walk-in";
}
