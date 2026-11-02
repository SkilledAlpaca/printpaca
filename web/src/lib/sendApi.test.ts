import { describe, it, expect } from "vitest";
import type { OrderBreakdown } from "./engine/index";
import type { SavedQuote } from "./api";
import { buildQuoteSendPayload, reasonForStatus } from "./sendApi";

/** A formState snapshot with one part, the shape every saved quote carries. */
function snap(quantity: unknown, order: Record<string, unknown> = {}) {
  return { version: 1, parts: [{ quantity }], order };
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

describe("reasonForStatus", () => {
  it("classifies 409 as unconfigured", () => {
    expect(reasonForStatus(409)).toBe("unconfigured");
  });
  it("classifies 422 as invalid", () => {
    expect(reasonForStatus(422)).toBe("invalid");
  });
  it("classifies 502 as smtp-error", () => {
    expect(reasonForStatus(502)).toBe("smtp-error");
  });
  it("classifies anything else as unknown", () => {
    expect(reasonForStatus(500)).toBe("unknown");
    expect(reasonForStatus(400)).toBe("unknown");
  });
});

describe("buildQuoteSendPayload", () => {
  it("prefers the saved resolved per-unit price, already in cents", () => {
    const payload = buildQuoteSendPayload(saved({ resolvedPrice: 2100, recommendedPrice: 2499 }));
    expect(payload).toEqual({
      id: "q1",
      reference: "",
      label: "benchy for Sam",
      clientName: "",
      quantity: 5,
      unitPriceCents: 2100,
      shippingCents: 0,
      savedAt: "2026-08-21T00:00:00.000Z",
      taxIncluded: false,
    });
  });

  it("marks taxIncluded only when the snapshot priced with tax baked in", () => {
    expect(
      buildQuoteSendPayload(saved({ resolvedPrice: 2100, inputs: snap(5, { sellingEnabled: true, taxRatePct: 8.25 }) }))
        ?.taxIncluded,
    ).toBe(true);
    expect(
      buildQuoteSendPayload(saved({ resolvedPrice: 2100, inputs: snap(5, { sellingEnabled: false, taxRatePct: 8.25 }) }))
        ?.taxIncluded,
    ).toBe(false);
  });

  it("falls back to the recommended per-unit price", () => {
    expect(buildQuoteSendPayload(saved({ recommendedPrice: 2499 }))?.unitPriceCents).toBe(2499);
  });

  it("returns null for cost-only quotes rather than falling back to batch cost", () => {
    expect(buildQuoteSendPayload(saved({}))).toBeNull();
  });

  it("never emits a negative or fractional unit price", () => {
    expect(buildQuoteSendPayload(saved({ resolvedPrice: 2100.4 }))?.unitPriceCents).toBe(2100);
    expect(buildQuoteSendPayload(saved({ resolvedPrice: -5 }))?.unitPriceCents).toBe(0);
  });

  it("carries the linked client name when present", () => {
    expect(buildQuoteSendPayload(saved({ resolvedPrice: 2100, clientName: "Sam" }))?.clientName).toBe("Sam");
  });

  it("defaults clientName to empty string when absent", () => {
    expect(buildQuoteSendPayload(saved({ resolvedPrice: 2100, clientName: undefined }))?.clientName).toBe("");
  });

  it("converts shippingCharge dollars to shippingCents, rounded", () => {
    const payload = buildQuoteSendPayload(
      saved({ resolvedPrice: 2100, inputs: snap(5, { shippingCharge: 4.995 }) }),
    );
    expect(payload?.shippingCents).toBe(500);
  });

  it("omits shipping when the snapshot has none", () => {
    expect(buildQuoteSendPayload(saved({ resolvedPrice: 2100 }))?.shippingCents).toBe(0);
  });

  it("converts shippingCharge with the engine's toCents, matching invoiceLinesFromQuote's own conversion", () => {
    // buildQuoteSendPayload must use toCents, not Math.round(v * 100): 1.005 * 100 is 100.49999999999999,
    // so the emailed quote and the invoice built from it could state different shipping amounts.
    const payload = buildQuoteSendPayload(saved({ resolvedPrice: 2100, inputs: snap(5, { shippingCharge: 1.005 }) }));
    expect(payload?.shippingCents).toBe(101);
  });

  it("carries the quote's own savedAt", () => {
    const payload = buildQuoteSendPayload(saved({ resolvedPrice: 2100, savedAt: "2026-01-05T08:00:00.000Z" }));
    expect(payload?.savedAt).toBe("2026-01-05T08:00:00.000Z");
  });
});

describe("buildQuoteSendPayload: multi-part lines", () => {
  const order = {
    parts: [
      { label: "Body", breakdown: { quantity: 4 }, resolvedPrice: 1500, itemRevenue: 6000 },
      { label: "Lid", breakdown: { quantity: 1 }, resolvedPrice: 800.4, itemRevenue: 800 },
    ],
    itemRevenue: 6800,
    pricing: { valid: true },
    batchTotal: 0,
    warnings: [],
    valid: true,
  } as unknown as OrderBreakdown;
  const multi = saved({ label: "Shelf order", partCount: 2, resolvedPrice: 6800, inputs: { version: 1, parts: [{}, {}], order: { shippingCharge: 8 } } });

  it("sends one line per part with the part label only", () => {
    const p = buildQuoteSendPayload(multi, order)!;
    expect(p.lines).toEqual([
      { label: "Body", quantity: 4, unitPriceCents: 1500 },
      { label: "Lid", quantity: 1, unitPriceCents: 800 },
    ]);
    expect(p.label).toBe("Shelf order");
    expect(p.shippingCents).toBe(800);
    expect(p.quantity).toBe(1);
  });

  it("cannot build a multi-part payload without the order answer", () => {
    expect(buildQuoteSendPayload(multi, null)).toBeNull();
  });

  it("leaves lines off for a one-part quote", () => {
    expect(buildQuoteSendPayload(saved({ resolvedPrice: 100 }))!.lines).toBeUndefined();
  });
});
