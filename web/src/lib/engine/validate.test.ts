import { describe, it, expect } from "vitest";
import { validateField, hasErrors } from "./validate";

describe("validateField", () => {
  it("accepts a value inside bounds", () => {
    expect(validateField("weightG", 50, { min: 0 })).toEqual([]);
  });
  it("flags required missing values", () => {
    const w = validateField("weightG", undefined, { required: true, min: 0 });
    expect(w).toHaveLength(1);
    expect(w[0]).toMatchObject({ field: "weightG", code: "required", severity: "error" });
  });
  it("returns empty for optional missing values", () => {
    expect(validateField("shippingCharge", null, { min: 0 })).toEqual([]);
  });
  it("flags NaN", () => {
    expect(validateField("watts", NaN, { min: 0 })[0]?.code).toBe("nan");
  });
  it("flags below-min and above-max", () => {
    expect(validateField("marginPct", -1, { min: 0, max: 99.9 })[0]?.code).toBe("below-min");
    expect(validateField("marginPct", 100, { min: 0, max: 99.9 })[0]?.code).toBe("above-max");
  });
  it("reports the violated rule's threshold on `bound`, so a UI can build its own copy without parsing `message`", () => {
    expect(validateField("marginPct", -1, { min: 0, max: 99.9 })[0]?.bound).toBe(0);
    expect(validateField("marginPct", 100, { min: 0, max: 99.9 })[0]?.bound).toBe(99.9);
  });
  it("flags non-integers when integer rule set", () => {
    expect(validateField("quantity", 1.5, { min: 1, integer: true })[0]?.code).toBe("not-integer");
  });
  it("respects downgraded severity", () => {
    const w = validateField("failureRatePct", 60, { min: 0, max: 50, severity: "warning" });
    expect(w[0]?.severity).toBe("warning");
  });
  it("rejects ±Infinity with nan code", () => {
    expect(validateField("q", Infinity, { min: 0 })[0]?.code).toBe("nan");
    expect(validateField("q", -Infinity, { integer: true })[0]?.code).toBe("nan");
  });
  it("accumulates multiple simultaneous violations", () => {
    expect(validateField("q", 1.5, { min: 2, max: 1, integer: true })).toHaveLength(3);
  });
});

describe("hasErrors", () => {
  it("is true only when an error-severity warning exists", () => {
    expect(hasErrors([{ field: "x", code: "nan", message: "", severity: "warning" }])).toBe(false);
    expect(hasErrors([{ field: "x", code: "nan", message: "", severity: "error" }])).toBe(true);
  });
  it("returns false for empty warnings array", () => {
    expect(hasErrors([])).toBe(false);
  });
});
