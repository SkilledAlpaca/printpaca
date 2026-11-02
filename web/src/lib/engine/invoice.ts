// Client-side preview of server/invoices.go's computeTotals; the server is authoritative for what is stored.

/** Half plus a 1e-9 epsilon. The epsilon corrects IEEE 754 ties that land just under .5 (1.005 * 100 is
 * 100.49999999999999). Keep the literal identical to halfUpBias in server/invoices.go. */
const HALF_UP_BIAS = 0.500000001;

/** Half-up rounding, identical to server/invoices.go's roundHalfUp. Module-only: not re-exported from
 * index.ts. */
export function roundHalfUpCents(x: number): number {
  if (!Number.isFinite(x)) return 0;
  return Math.floor(x + HALF_UP_BIAS);
}

export type InvoiceLineKind = "item" | "shipping";

export interface InvoiceLineInput {
  quantity: number;
  unitPriceCents: number;
  discountCents: number;
  /** "shipping" lines are exempt from tax; absent or "item" is taxed. */
  kind?: InvoiceLineKind;
}

export interface InvoiceTotals {
  lineAmounts: number[];
  subtotalCents: number;
  taxCents: number;
  totalCents: number;
}

/** Mirrors server/invoices.go's computeTotals exactly:
 * lineAmount = round(quantity * unitPriceCents) - lineDiscountCents; subtotal = sum(lineAmounts);
 * taxable = max(0, sum(lineAmounts of lines whose kind is not "shipping") - invoiceDiscountCents); tax = round(taxable*taxBp/10000);
 * total = max(0, subtotal - invoiceDiscountCents) + tax. */
export function invoiceTotals(
  lines: InvoiceLineInput[],
  invoiceDiscountCents: number,
  taxBp: number,
): InvoiceTotals {
  const lineAmounts = lines.map(
    (l) => roundHalfUpCents(l.quantity * l.unitPriceCents) - l.discountCents,
  );
  const subtotalCents = lineAmounts.reduce((sum, a) => sum + a, 0);
  const taxableSubtotalCents = lines.reduce(
    (sum, l, i) => (l.kind === "shipping" ? sum : sum + lineAmounts[i]!),
    0,
  );
  const taxable = Math.max(0, taxableSubtotalCents - invoiceDiscountCents);
  const taxCents = roundHalfUpCents((taxable * taxBp) / 10000);
  const totalCents = Math.max(0, subtotalCents - invoiceDiscountCents) + taxCents;
  return { lineAmounts, subtotalCents, taxCents, totalCents };
}
