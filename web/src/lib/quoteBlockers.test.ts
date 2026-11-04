import { describe, expect, it } from "vitest";
import type { FieldWarning, OrderBreakdown } from "./engine/index";
import { quoteBlockers, needSentence } from "./quoteBlockers";

function w(field: string, code: FieldWarning["code"] = "required", severity: FieldWarning["severity"] = "error"): FieldWarning {
  return { field, code, message: "x", severity };
}

function order(partWarnings: FieldWarning[][], pricingWarnings: FieldWarning[] = []): OrderBreakdown {
  return {
    parts: partWarnings.map((warnings) => ({ label: "", breakdown: { warnings, valid: warnings.length === 0 }, recommendedPrice: 0, resolvedPrice: 0, itemRevenue: 0 })),
    pricing: { warnings: pricingWarnings, valid: false },
    warnings: [],
    valid: false,
  } as unknown as OrderBreakdown;
}

describe("quoteBlockers", () => {
  it("is empty when nothing specific is known", () => {
    expect(quoteBlockers(order([[]]), ["Part 1"])).toEqual([]);
  });

  it("merges the two print-time halves into one blocker", () => {
    const b = quoteBlockers(order([[w("timeHours"), w("timeMinutes")]]), ["Part 1"]);
    expect(b).toHaveLength(1);
    expect(b[0]).toMatchObject({ label: "Print time", partIndex: 0, missing: true, inputLabel: "Print time" });
  });

  it("names the part only when the order has several", () => {
    const b = quoteBlockers(order([[], [w("timeHours", "nan")]]), ["Part 1", "Lid"]);
    expect(b.map((x) => x.label)).toEqual(["Print time (Lid)"]);
    expect(b[0]?.partIndex).toBe(1);
  });

  it("separates blank fields from rejected values and skips advisory warnings", () => {
    const b = quoteBlockers(order([[w("weightG", "below-min"), w("wastePct", "above-max", "warning"), w("pricePerKg", "nan")]]), ["Part 1"]);
    expect(b.map((x) => [x.label, x.missing])).toEqual([["Model weight", false], ["Filament price", true]]);
  });

  it("includes pricing-block inputs but not fields with no input", () => {
    const b = quoteBlockers(order([[]], [w("marginPct", "above-max"), w("feeSchedule", "nan"), w("parts[0].salePriceOverride", "below-min")]), ["Part 1"]);
    expect(b.map((x) => x.label)).toEqual(["Margin"]);
    expect(b[0]?.partIndex).toBeNull();
  });
});

describe("needSentence", () => {
  const b = (label: string, field: string) => ({ label, field });
  it("is empty with nothing missing", () => {
    expect(needSentence([])).toBe("");
  });
  it("names a single field", () => {
    expect(needSentence([b("Print time", "timeHours")])).toBe("Price needs a print time");
  });
  it("names every field, print time first", () => {
    expect(needSentence([b("Model weight", "weightG"), b("Print time", "timeHours")])).toBe("Price needs a print time and model weight");
    expect(needSentence([b("Model weight", "weightG"), b("Filament price", "pricePerKg"), b("Print time", "timeMinutes")])).toBe(
      "Price needs a print time, model weight, and filament price",
    );
  });
  it("keeps the part name of a multi-part label", () => {
    expect(needSentence([b("Print time (Lid)", "timeHours")])).toBe("Price needs a print time (Lid)");
  });
});
