import { describe, it, expect } from "vitest";
import { invoiceTotals } from "./engine/index";
import type { OrderBreakdown } from "./engine/index";
import type { SavedQuote } from "./api";
import {
  convertClientName,
  extractQuantity,
  extractShippingCharge,
  extractTaxRatePct,
  invoiceLineFromQuote,
  invoiceLinesFromQuote,
  quoteHasPrice,
  convertTaxBp,
  convertPreview,
  applyConvertStatus,
} from "./convertQuote";

/** A formState snapshot with one part, the shape every saved quote carries. */
function snap(quantity?: unknown, order: Record<string, unknown> = {}) {
  return { version: 1, parts: [quantity === undefined ? {} : { quantity }], order };
}

function saved(over: Partial<SavedQuote>): SavedQuote {
  return {
    id: "q1",
    savedAt: "2026-08-21T00:00:00.000Z",
    label: "benchy for Sam",
    perUnitTotal: 1243,
    batchTotal: 6215,
    inputs: snap(5),
    status: "draft",
    statusLog: [],
    partCount: 1,
    ...over,
  };
}

describe("extractQuantity", () => {
  it("reads a positive finite quantity from the snapshot", () => {
    expect(extractQuantity(snap(5))).toBe(5);
  });
  it("falls back to 1 for missing, non-numeric, or non-positive values", () => {
    expect(extractQuantity(undefined)).toBe(1);
    expect(extractQuantity({})).toBe(1);
    expect(extractQuantity(snap("5"))).toBe(1);
    expect(extractQuantity(snap(0))).toBe(1);
    expect(extractQuantity(snap(NaN))).toBe(1);
  });
});

describe("invoiceLineFromQuote", () => {
  it("prefers the saved resolved per-unit price, already in cents", () => {
    const line = invoiceLineFromQuote(saved({ resolvedPrice: 2100, recommendedPrice: 2499 }));
    expect(line).toEqual({
      description: "benchy for Sam",
      quantity: 5,
      unitPriceCents: 2100,
      discountCents: 0,
      quoteId: "q1",
    });
  });

  it("falls back to the recommended per-unit price", () => {
    expect(invoiceLineFromQuote(saved({ recommendedPrice: 2499 })).unitPriceCents).toBe(2499);
  });

  it("falls back to batch cost divided by quantity for cost-only quotes", () => {
    expect(invoiceLineFromQuote(saved({})).unitPriceCents).toBe(1243);
  });

  it("never emits a negative or fractional unit price", () => {
    expect(invoiceLineFromQuote(saved({ resolvedPrice: 2100.4 })).unitPriceCents).toBe(2100);
    expect(invoiceLineFromQuote(saved({ resolvedPrice: -5 })).unitPriceCents).toBe(0);
  });

  it("falls through to recommendedPrice when resolvedPrice is a non-numeric string", () => {
    const line = invoiceLineFromQuote(
      saved({ resolvedPrice: "abc" as unknown as number, recommendedPrice: 2499 }),
    );
    expect(line.unitPriceCents).toBe(2499);
  });

  it("falls through to recommendedPrice when resolvedPrice is NaN", () => {
    const line = invoiceLineFromQuote(saved({ resolvedPrice: NaN, recommendedPrice: 2499 }));
    expect(line.unitPriceCents).toBe(2499);
  });

  it("falls through to recommendedPrice when resolvedPrice is Infinity", () => {
    const line = invoiceLineFromQuote(saved({ resolvedPrice: Infinity, recommendedPrice: 2499 }));
    expect(line.unitPriceCents).toBe(2499);
  });

  it("falls through to the batch fallback when both resolvedPrice and recommendedPrice are unusable", () => {
    const line = invoiceLineFromQuote(
      saved({ resolvedPrice: "abc" as unknown as number, recommendedPrice: NaN }),
    );
    expect(line.unitPriceCents).toBe(1243);
  });
});

describe("extractShippingCharge", () => {
  it("reads a positive finite shipping charge in dollars from the snapshot", () => {
    expect(extractShippingCharge(snap(undefined, { shippingCharge: 8.5 }))).toBe(8.5);
  });

  it("returns 0 for missing, absent, non-positive, non-numeric, or non-finite values", () => {
    expect(extractShippingCharge(undefined)).toBe(0);
    expect(extractShippingCharge({})).toBe(0);
    expect(extractShippingCharge(snap(undefined, { shippingCharge: 0 }))).toBe(0);
    expect(extractShippingCharge(snap(undefined, { shippingCharge: -5 }))).toBe(0);
    expect(extractShippingCharge(snap(undefined, { shippingCharge: "8.5" }))).toBe(0);
    expect(extractShippingCharge(snap(undefined, { shippingCharge: NaN }))).toBe(0);
  });
});

describe("invoiceLinesFromQuote", () => {
  it("adds a Shipping line when the quote's snapshot charged shipping", () => {
    const lines = invoiceLinesFromQuote(saved({ inputs: snap(5, { shippingCharge: 8.5 }) }));
    expect(lines).toHaveLength(2);
    expect(lines[1]).toEqual({
      description: "Shipping",
      kind: "shipping",
      quantity: 1,
      unitPriceCents: 850,
      discountCents: 0,
      quoteId: "q1",
    });
  });

  it("returns exactly one line when the quote charged no shipping", () => {
    const lines = invoiceLinesFromQuote(saved({}));
    expect(lines).toHaveLength(1);
  });

  it("rounds shipping with the engine's toCents, not a plain Math.round of the float cents", () => {
    // 19.995 * 100 === 1999.4999999999998 in IEEE754; toCents' epsilon correction lands on 2000c, not 1999c.
    const lines = invoiceLinesFromQuote(saved({ inputs: snap(5, { shippingCharge: 19.995 }) }));
    expect(lines[1]).toEqual({
      description: "Shipping",
      kind: "shipping",
      quantity: 1,
      unitPriceCents: 2000,
      discountCents: 0,
      quoteId: "q1",
    });
  });
});

describe("quoteHasPrice", () => {
  it("is true when resolvedPrice is a finite number", () => {
    expect(quoteHasPrice(saved({ resolvedPrice: 2100 }))).toBe(true);
  });

  it("is true when recommendedPrice is a finite number", () => {
    expect(quoteHasPrice(saved({ recommendedPrice: 2499 }))).toBe(true);
  });

  it("is false for a cost-only quote with neither price set", () => {
    expect(quoteHasPrice(saved({}))).toBe(false);
  });

  it("is false when the only price present is non-finite", () => {
    expect(quoteHasPrice(saved({ resolvedPrice: NaN }))).toBe(false);
  });
});

describe("extractTaxRatePct", () => {
  it("reads taxRatePct when sellingEnabled is true", () => {
    expect(extractTaxRatePct(snap(undefined, { sellingEnabled: true, taxRatePct: 8 }))).toBe(8);
  });

  it("returns 0 when sellingEnabled is false, even with a taxRatePct present", () => {
    expect(extractTaxRatePct(snap(undefined, { sellingEnabled: false, taxRatePct: 8 }))).toBe(0);
  });

  it("returns 0 when sellingEnabled is absent", () => {
    expect(extractTaxRatePct(snap(undefined, { taxRatePct: 8 }))).toBe(0);
  });

  it("returns 0 for missing, absent, or non-finite values", () => {
    expect(extractTaxRatePct(undefined)).toBe(0);
    expect(extractTaxRatePct({})).toBe(0);
    expect(extractTaxRatePct(snap(undefined, { sellingEnabled: true }))).toBe(0);
    expect(extractTaxRatePct(snap(undefined, { sellingEnabled: true, taxRatePct: NaN }))).toBe(0);
  });

  it("returns 0 for a corrupt snapshot with a string taxRatePct", () => {
    expect(extractTaxRatePct(snap(undefined, { sellingEnabled: true, taxRatePct: "8" }))).toBe(0);
  });
});

describe("convertTaxBp", () => {
  it("is 0 for a priced quote with a positive tax rate and selling enabled", () => {
    const q = saved({ resolvedPrice: 2100, inputs: snap(undefined, { sellingEnabled: true, taxRatePct: 8 }) });
    expect(convertTaxBp(q)).toBe(0);
  });

  it("is null for a priced quote with taxRatePct 0", () => {
    const q = saved({ resolvedPrice: 2100, inputs: snap(undefined, { sellingEnabled: true, taxRatePct: 0 }) });
    expect(convertTaxBp(q)).toBeNull();
  });

  it("is null for a cost-only quote (no prices)", () => {
    const q = saved({ inputs: snap(undefined, { sellingEnabled: true, taxRatePct: 8 }) });
    expect(quoteHasPrice(q)).toBe(false);
    expect(convertTaxBp(q)).toBeNull();
  });

  it("is null when sellingEnabled is false, even for a priced quote with a stale taxRatePct", () => {
    const q = saved({ resolvedPrice: 2100, inputs: snap(undefined, { sellingEnabled: false, taxRatePct: 8 }) });
    expect(convertTaxBp(q)).toBeNull();
  });
});

describe("convertPreview", () => {
  it("carries taxBp 0 through totals when the quoted price already includes tax", () => {
    const q = saved({
      resolvedPrice: 1250,
      inputs: snap(2, { sellingEnabled: true, taxRatePct: 8 }),
    });
    const preview = convertPreview(q, 8);
    expect(preview.taxBp).toBe(0);
    expect(preview.subtotalCents).toBe(2500);
    expect(preview.taxCents).toBe(0);
    expect(preview.totalCents).toBe(2500);
  });

  it("falls back to the default sales tax when the quote carries no tax-inclusive price", () => {
    const q = saved({
      resolvedPrice: 1250,
      inputs: snap(2, { sellingEnabled: false, taxRatePct: 8 }),
    });
    const preview = convertPreview(q, 8);
    expect(preview.taxBp).toBe(800);
    expect(preview.subtotalCents).toBe(2500);
    expect(preview.taxCents).toBe(200);
    expect(preview.totalCents).toBe(2700);
  });

  it("taxes only the product line, never the Shipping line, when falling back to the default sales tax", () => {
    // Same fallback (sellingEnabled false, so the shop default tax applies) plus shipping: subtotal
    // 2 * 1250c = 2500c taxed at 8% -> 200c; shipping (1000c) is untaxed.
    const q = saved({
      resolvedPrice: 1250,
      inputs: snap(2, { sellingEnabled: false, taxRatePct: 8, shippingCharge: 10 }),
    });
    const preview = convertPreview(q, 8);
    expect(preview.taxBp).toBe(800);
    expect(preview.lines).toHaveLength(2);
    expect(preview.lines[1]).toMatchObject({ description: "Shipping", unitPriceCents: 1000 });
    expect(preview.subtotalCents).toBe(3500); // 2500 product + 1000 shipping
    expect(preview.taxCents).toBe(200); // round(2500 * 8%), shipping excluded
    expect(preview.totalCents).toBe(3700); // product 2500 + tax 200 + shipping 1000
  });

  // convertPreview must use the shared invoiceTotals (engine/invoice.ts), not a duplicate: pin
  // byte-identical totals by computing invoiceTotals independently from the same lines/taxBp.
  it("produces totals byte-identical to invoiceTotals(lines, 0, taxBp) for the same quote", () => {
    const q = saved({
      resolvedPrice: 1250,
      inputs: snap(2, { sellingEnabled: false, taxRatePct: 8, shippingCharge: 10 }),
    });
    const preview = convertPreview(q, 8);
    const independent = invoiceTotals(preview.lines, 0, preview.taxBp);
    expect(preview.subtotalCents).toBe(independent.subtotalCents);
    expect(preview.taxCents).toBe(independent.taxCents);
    expect(preview.totalCents).toBe(independent.totalCents);
  });
});

describe("applyConvertStatus", () => {
  it("sets a draft quote to accepted with a log entry", () => {
    const q = saved({ status: "draft", statusLog: [] });
    const next = applyConvertStatus(q, "2026-08-22T00:00:00.000Z");
    expect(next.status).toBe("accepted");
    expect(next.statusLog).toEqual([{ status: "accepted", at: "2026-08-22T00:00:00.000Z" }]);
  });

  it("sets a sent quote to accepted with a log entry", () => {
    const q = saved({ status: "sent", statusLog: [{ status: "sent", at: "2026-08-21T00:00:00.000Z" }] });
    const next = applyConvertStatus(q, "2026-08-22T00:00:00.000Z");
    expect(next.status).toBe("accepted");
    expect(next.statusLog).toEqual([
      { status: "sent", at: "2026-08-21T00:00:00.000Z" },
      { status: "accepted", at: "2026-08-22T00:00:00.000Z" },
    ]);
  });

  it("leaves an already-accepted quote unchanged (no duplicate log entry on re-invoice)", () => {
    const q = saved({
      status: "accepted",
      statusLog: [{ status: "accepted", at: "2026-08-20T00:00:00.000Z" }],
    });
    const next = applyConvertStatus(q, "2026-08-22T00:00:00.000Z");
    expect(next).toBe(q);
    expect(next.statusLog).toEqual([{ status: "accepted", at: "2026-08-20T00:00:00.000Z" }]);
  });

  it("still accepts a declined quote (any non-accepted stored status transitions)", () => {
    const q = saved({ status: "declined", statusLog: [{ status: "declined", at: "2026-08-20T00:00:00.000Z" }] });
    const next = applyConvertStatus(q, "2026-08-22T00:00:00.000Z");
    expect(next.status).toBe("accepted");
    expect(next.statusLog).toEqual([
      { status: "declined", at: "2026-08-20T00:00:00.000Z" },
      { status: "accepted", at: "2026-08-22T00:00:00.000Z" },
    ]);
  });

  it("does not mutate the input quote", () => {
    const q = saved({ status: "draft", statusLog: [] });
    applyConvertStatus(q, "2026-08-22T00:00:00.000Z");
    expect(q.status).toBe("draft");
    expect(q.statusLog).toEqual([]);
  });
});

describe("convertClientName", () => {
  const q = { id: "q", savedAt: "", label: "x", perUnitTotal: 0, batchTotal: 0, inputs: {} } as SavedQuote;
  it("prefers the chosen client's name", () => {
    expect(convertClientName("Acme", { ...q, clientName: "Walk Sam" })).toBe("Acme");
  });
  it("carries the quote's walk-in name", () => {
    expect(convertClientName(undefined, { ...q, clientName: "  Walk Sam " })).toBe("Walk Sam");
  });
  it("falls back to Walk-in when empty", () => {
    expect(convertClientName(undefined, q)).toBe("Walk-in");
    expect(convertClientName("", { ...q, clientName: "  " })).toBe("Walk-in");
  });
});

// Multi-part orders convert to one invoice line per part, priced from the server's per-part prices,
// plus the Shipping line.
describe("multi-part conversion", () => {
  const order = {
    parts: [
      { label: "Body", breakdown: { quantity: 4 }, resolvedPrice: 1500, itemRevenue: 6000 },
      { label: "Lid", breakdown: { quantity: 1 }, resolvedPrice: 800, itemRevenue: 800 },
      { label: "Spacers", breakdown: { quantity: 20 }, resolvedPrice: 75, itemRevenue: 1500 },
    ],
    batchTotal: 5000,
    itemRevenue: 8300,
    pricing: { valid: true, resolvedPrice: 8300 },
    warnings: [],
    valid: true,
  } as unknown as OrderBreakdown;

  const multi = saved({
    label: "Shelf order",
    partCount: 3,
    resolvedPrice: 8300,
    inputs: { version: 1, parts: [{ quantity: 4 }, { quantity: 1 }, { quantity: 20 }], order: { sellingEnabled: true, taxRatePct: 7, shippingCharge: 8 } },
  });

  it("produces one line per part plus Shipping, summing to the quote's revenue", () => {
    const lines = invoiceLinesFromQuote(multi, order);
    expect(lines.map((l) => [l.description, l.quantity, l.unitPriceCents])).toEqual([
      ["Shelf order - Body", 4, 1500],
      ["Shelf order - Lid", 1, 800],
      ["Shelf order - Spacers", 20, 75],
      ["Shipping", 1, 800],
    ]);
    expect(lines.every((l) => l.quoteId === "q1")).toBe(true);
    const totals = invoiceTotals(lines, 0, 0);
    expect(totals.totalCents).toBe(8300 + 800);
  });

  it("produces no lines without the order answer, so convert cannot invoice a made-up price", () => {
    expect(invoiceLinesFromQuote(multi, null)).toEqual([]);
    expect(invoiceLinesFromQuote(multi, undefined)).toEqual([]);
    expect(convertPreview(multi, 0, null).lines).toEqual([]);
  });

  it("reads tax and shipping from the order block, and treats a multi-part quantity as 1", () => {
    expect(extractQuantity(multi.inputs)).toBe(1);
    expect(extractShippingCharge(multi.inputs)).toBe(8);
    expect(extractTaxRatePct(multi.inputs)).toBe(7);
    expect(convertTaxBp(multi)).toBe(0);
  });

  it("a one-part quote converts to the single product line", () => {
    const one = saved({
      partCount: 1,
      resolvedPrice: 2100,
      inputs: { version: 1, parts: [{ quantity: 5 }], order: { sellingEnabled: true, shippingCharge: 0 } },
    });
    expect(extractQuantity(one.inputs)).toBe(5);
    expect(invoiceLinesFromQuote(one)).toEqual([
      { description: "benchy for Sam", quantity: 5, unitPriceCents: 2100, discountCents: 0, quoteId: "q1" },
    ]);
  });
});
