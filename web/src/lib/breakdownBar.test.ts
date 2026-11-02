import { describe, expect, it } from "vitest";
import { computeBreakdownSegments } from "./breakdownBar";
import type { CostBreakdown, FieldWarning, LineItem } from "./engine/index";

function line(overrides: Partial<LineItem> = {}): LineItem {
  return {
    id: "material",
    label: "Material",
    amount: 100,
    formula: "",
    inputs: {},
    warnings: [],
    ...overrides,
  };
}

function warning(overrides: Partial<FieldWarning> = {}): FieldWarning {
  return { field: "weightG", code: "below-min", message: "bad", severity: "error", ...overrides };
}

function breakdown(perUnit: LineItem[], perUnitTotal: number): CostBreakdown {
  return { perUnit, perUnitTotal, quantity: 1, batchTotal: perUnitTotal, warnings: [], valid: true };
}

describe("computeBreakdownSegments", () => {
  it("computes proportions that sum to 100 within rounding", () => {
    const lines = [
      line({ id: "material", amount: 300 }),
      line({ id: "electricity", amount: 200 }),
      line({ id: "labor", amount: 500 }),
    ];
    const segments = computeBreakdownSegments(breakdown(lines, 1000));
    expect(segments).toHaveLength(3);
    const total = segments.reduce((sum, s) => sum + s.percent, 0);
    expect(total).toBeCloseTo(100, 9);
    expect(segments.find((s) => s.id === "material")?.percent).toBeCloseTo(30, 9);
  });

  it("skips zero-amount lines", () => {
    const lines = [
      line({ id: "material", amount: 100 }),
      line({ id: "electricity", amount: 0 }),
      line({ id: "depreciation", amount: 100 }),
    ];
    const segments = computeBreakdownSegments(breakdown(lines, 200));
    expect(segments.map((s) => s.id)).toEqual(["material", "depreciation"]);
  });

  it("returns no segments when any line has an error warning", () => {
    const lines = [
      line({ id: "material", amount: 100 }),
      line({ id: "electricity", amount: 100, warnings: [warning()] }),
    ];
    expect(computeBreakdownSegments(breakdown(lines, 200))).toEqual([]);
  });

  it("still produces segments when a line only has non-error (warning severity) warnings", () => {
    const lines = [line({ id: "material", amount: 100, warnings: [warning({ severity: "warning" })] })];
    expect(computeBreakdownSegments(breakdown(lines, 100))).toHaveLength(1);
  });

  it("returns no segments when perUnitTotal is zero", () => {
    const lines = [line({ id: "material", amount: 0 })];
    expect(computeBreakdownSegments(breakdown(lines, 0))).toEqual([]);
  });

  it("returns no segments for an empty breakdown", () => {
    expect(computeBreakdownSegments(breakdown([], 0))).toEqual([]);
  });
});
