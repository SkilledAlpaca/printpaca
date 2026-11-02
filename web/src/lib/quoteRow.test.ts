import { describe, expect, it } from "vitest";
import { displayPriceCents, quoteRowStats, breakdownHint, printBlockerHint } from "./quoteRow";
import type { SavedQuote } from "./api";
import type { CostBreakdown, FieldWarning, PricingResult } from "./engine/index";

function makeQuote(overrides: Partial<SavedQuote> = {}): SavedQuote {
  return {
    id: "q1",
    savedAt: "2024-01-01T00:00:00.000Z",
    label: "Benchy",
    perUnitTotal: 400,
    batchTotal: 1600,
    inputs: {},
    status: "draft",
    statusLog: [],
    partCount: 1,
    ...overrides,
  };
}

describe("displayPriceCents", () => {
  it("prefers resolvedPrice when finite", () => {
    const q = makeQuote({ resolvedPrice: 1000, recommendedPrice: 900 });
    expect(displayPriceCents(q)).toBe(1000);
  });

  it("falls back to recommendedPrice when resolvedPrice is absent", () => {
    const q = makeQuote({ recommendedPrice: 900 });
    expect(displayPriceCents(q)).toBe(900);
  });

  it("falls back to recommendedPrice when resolvedPrice is NaN", () => {
    const q = makeQuote({ resolvedPrice: NaN, recommendedPrice: 900 });
    expect(displayPriceCents(q)).toBe(900);
  });

  it("falls back to recommendedPrice when resolvedPrice is non-finite (Infinity)", () => {
    const q = makeQuote({ resolvedPrice: Number("1e999"), recommendedPrice: 900 });
    expect(displayPriceCents(q)).toBe(900);
  });

  it("returns null when neither price is present", () => {
    expect(displayPriceCents(makeQuote())).toBeNull();
  });

  it("returns null when both prices are non-finite", () => {
    const q = makeQuote({ resolvedPrice: NaN, recommendedPrice: Number("1e999") });
    expect(displayPriceCents(q)).toBeNull();
  });
});

describe("quoteRowStats", () => {
  it("computes margin from price and perUnitTotal", () => {
    const q = makeQuote({ resolvedPrice: 1000, perUnitTotal: 400, batchTotal: 1600 });
    const stats = quoteRowStats(q);
    expect(stats.priceCents).toBe(1000);
    expect(stats.marginPct).toBe(60);
  });

  it("gives null margin when there is no price", () => {
    const q = makeQuote({ perUnitTotal: 400, batchTotal: 1600 });
    const stats = quoteRowStats(q);
    expect(stats.priceCents).toBeNull();
    expect(stats.marginPct).toBeNull();
  });

  it("gives null margin when price is zero", () => {
    const q = makeQuote({ resolvedPrice: 0, perUnitTotal: 400, batchTotal: 1600 });
    const stats = quoteRowStats(q);
    expect(stats.priceCents).toBe(0);
    expect(stats.marginPct).toBeNull();
  });

  it("gives null margin when price is negative", () => {
    const q = makeQuote({ resolvedPrice: -100, perUnitTotal: 400, batchTotal: 1600 });
    const stats = quoteRowStats(q);
    expect(stats.marginPct).toBeNull();
  });

  it("gives null margin when perUnitTotal is not finite", () => {
    const q = makeQuote({ resolvedPrice: 1000, perUnitTotal: NaN, batchTotal: 1600 });
    const stats = quoteRowStats(q);
    expect(stats.priceCents).toBe(1000);
    expect(stats.marginPct).toBeNull();
  });
});

function makeWarning(overrides: Partial<FieldWarning> = {}): FieldWarning {
  return { field: "watts", code: "nan", message: "watts is not a finite number", severity: "error", ...overrides };
}

function makeBreakdown(overrides: Partial<CostBreakdown> = {}): CostBreakdown {
  return {
    perUnit: [],
    perUnitTotal: 0,
    quantity: 1,
    batchTotal: 0,
    warnings: [],
    valid: true,
    ...overrides,
  };
}

function makePricing(overrides: Partial<PricingResult> = {}): PricingResult {
  return {
    strategy: "margin",
    recommendedPrice: 0,
    resolvedPrice: 0,
    revenue: 0,
    fees: 0,
    feeLines: [],
    tax: 0,
    shippingCharge: 0,
    shippingCost: 0,
    netProfit: 0,
    achievedMarginPct: 0,
    warnings: [],
    valid: true,
    ...overrides,
  };
}

describe("breakdownHint", () => {
  it("falls back to the generic weight/time copy when there are no errors", () => {
    expect(breakdownHint(makeBreakdown())).toBe("Enter model weight and print time above to see costs.");
  });

  it("falls back to the generic copy when weightG is the errored field", () => {
    const bd = makeBreakdown({ valid: false, warnings: [makeWarning({ field: "weightG", code: "required", message: "weightG is required" })] });
    expect(breakdownHint(bd)).toBe("Enter model weight and print time above to see costs.");
  });

  it("falls back to the generic copy when timeHours or timeMinutes is among the errors", () => {
    const bd = makeBreakdown({
      valid: false,
      warnings: [
        makeWarning({ field: "watts", code: "nan", message: "watts is not a finite number" }),
        makeWarning({ field: "timeMinutes", code: "required", message: "timeMinutes is required" }),
      ],
    });
    expect(breakdownHint(bd)).toBe("Enter model weight and print time above to see costs.");
  });

  it("names the actual field for a non-weight/time error", () => {
    const bd = makeBreakdown({ valid: false, warnings: [makeWarning({ field: "watts", code: "nan", message: "watts is not a finite number" })] });
    expect(breakdownHint(bd)).toBe("Enter a valid printer power.");
  });

  it("ignores non-error-severity warnings", () => {
    const bd = makeBreakdown({ warnings: [makeWarning({ field: "watts", severity: "warning" })] });
    expect(breakdownHint(bd)).toBe("Enter model weight and print time above to see costs.");
  });
});

describe("printBlockerHint", () => {
  it("returns the breakdown hint when the breakdown itself is invalid, regardless of selling", () => {
    const bd = makeBreakdown({ valid: false, warnings: [makeWarning({ field: "watts" })] });
    expect(printBlockerHint(bd, false)).toBe("Enter a valid printer power.");
    expect(printBlockerHint(bd, true)).toBe("Enter a valid printer power.");
  });

  it("allows printing when the breakdown is valid and selling is off", () => {
    expect(printBlockerHint(makeBreakdown(), false)).toBe("");
  });

  it("allows printing when the breakdown and pricing are both valid", () => {
    const bd = makeBreakdown({ pricing: makePricing({ valid: true }) });
    expect(printBlockerHint(bd, true)).toBe("");
  });

  it("names the pricing field in error when pricing is invalid", () => {
    const bd = makeBreakdown({
      pricing: makePricing({ valid: false, warnings: [makeWarning({ field: "marginPct", code: "below-min", message: "marginPct must be ≥ 0", bound: 0 })] }),
    });
    expect(printBlockerHint(bd, true)).toBe("Margin must be at least 0.");
  });

  it("falls back to a generic pricing message when pricing is invalid with no error warning", () => {
    const bd = makeBreakdown({ pricing: makePricing({ valid: false, warnings: [] }) });
    expect(printBlockerHint(bd, true)).toBe("Set a valid selling price to print a client quote.");
  });

  it("falls back to a generic pricing message when selling is on but pricing is missing entirely", () => {
    expect(printBlockerHint(makeBreakdown(), true)).toBe("Set a valid selling price to print a client quote.");
  });
});
