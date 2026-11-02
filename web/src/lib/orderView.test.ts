import { describe, expect, it } from "vitest";
import type { OrderBreakdown } from "./engine/index";
import { costValid, orderLines, orderPrintBlockerHint, partBreakdown, EMPTY_ORDER, otherPricingWarnings, partPricingWarning } from "./orderView";

function part(label: string, valid: boolean, over: Record<string, unknown> = {}) {
  return {
    label,
    breakdown: { valid, quantity: 2, perUnitTotal: 100, batchTotal: 200, perUnit: [], warnings: valid ? [] : [{ field: "weightG", code: "required", message: "x", severity: "error" }] },
    recommendedPrice: 150,
    resolvedPrice: 150,
    itemRevenue: 300,
    ...over,
  };
}

function order(parts: ReturnType<typeof part>[], pricing?: Record<string, unknown>): OrderBreakdown {
  return { parts, batchTotal: 0, itemRevenue: 0, pricing, warnings: [], valid: parts.every((p) => p.breakdown.valid) && (pricing?.valid ?? true) } as unknown as OrderBreakdown;
}

describe("costValid", () => {
  it("is false before any answer", () => {
    expect(costValid(EMPTY_ORDER)).toBe(false);
  });
  it("ignores pricing validity (a costed draft with a bad margin still saves)", () => {
    expect(costValid(order([part("A", true)], { valid: false, warnings: [] }))).toBe(true);
  });
  it("needs every part valid", () => {
    expect(costValid(order([part("A", true), part("B", false)]))).toBe(false);
  });
});

describe("partBreakdown", () => {
  it("returns a placeholder for an index with no answer yet", () => {
    expect(partBreakdown(EMPTY_ORDER, 2).valid).toBe(false);
  });
});

describe("orderPrintBlockerHint", () => {
  it("names the part with the problem when there are several", () => {
    const hint = orderPrintBlockerHint(order([part("A", true), part("B", false)]), ["A", "B"], false);
    expect(hint.startsWith("B: ")).toBe(true);
  });
  it("does not name the part for a single part", () => {
    const hint = orderPrintBlockerHint(order([part("A", false)]), ["A"], false);
    expect(hint.startsWith("A:")).toBe(false);
  });
  it("blocks on an unusable price only while selling", () => {
    const o = order([part("A", true)], { valid: false, warnings: [{ field: "marginPct", code: "above-max", message: "m", severity: "error", bound: 99.9 }] });
    expect(orderPrintBlockerHint(o, ["A"], true)).toContain("Margin");
    expect(orderPrintBlockerHint(o, ["A"], false)).toBe("");
  });
  it("allows printing a valid priced order", () => {
    expect(orderPrintBlockerHint(order([part("A", true)], { valid: true, warnings: [] }), ["A"], true)).toBe("");
  });
});

describe("orderLines", () => {
  it("is empty without a pricing block", () => {
    expect(orderLines(order([part("A", true)]))).toEqual([]);
  });
  it("uses per-part resolved prices and line totals", () => {
    const o = order([part("A", true), part("B", true, { resolvedPrice: 75, itemRevenue: 150 })], { valid: true, warnings: [] });
    expect(orderLines(o)).toEqual([
      { label: "A", quantity: 2, unitPriceCents: 150, lineTotalCents: 300 },
      { label: "B", quantity: 2, unitPriceCents: 75, lineTotalCents: 150 },
    ]);
  });
});

describe("pricing warnings", () => {
  const ws = [
    { field: "parts[1].salePriceOverride", code: "below-min", message: "m", severity: "error", bound: 0 },
    { field: "feeSchedule", code: "x", message: "bad fee", severity: "error" },
  ] as never;
  it("finds the active part's override warning only", () => {
    expect(partPricingWarning(ws, 1, "salePriceOverride")).toContain("Sale price override");
    expect(partPricingWarning(ws, 0, "salePriceOverride")).toBe("");
  });
  it("leaves per-part override warnings out of the generic list", () => {
    expect(otherPricingWarnings(ws, new Set()).map((w) => w.field)).toEqual(["feeSchedule"]);
  });
});
