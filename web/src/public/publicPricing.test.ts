import { describe, expect, it } from "vitest";
import { clampQuantity, perUnitCents } from "./publicPricing";

describe("perUnitCents", () => {
  it("divides an integer total evenly", () => {
    expect(perUnitCents(1000, 4)).toBe(250);
  });

  it("rounds a non-exact division to the nearest cent", () => {
    expect(perUnitCents(1000, 3)).toBe(333);
  });

  it("returns null for a zero or negative quantity, never divides by zero", () => {
    expect(perUnitCents(1000, 0)).toBeNull();
    expect(perUnitCents(1000, -1)).toBeNull();
  });
});

describe("clampQuantity", () => {
  it("passes through a value already within range, with no message", () => {
    expect(clampQuantity(5, 100)).toEqual({ value: 5, message: null });
  });

  it("clamps a value above max down to max, with a named message", () => {
    const result = clampQuantity(500, 100);
    expect(result.value).toBe(100);
    expect(result.message).toContain("100");
  });

  it("clamps a value below the default min (1) up to 1, with no message", () => {
    expect(clampQuantity(0, 100)).toEqual({ value: 1, message: null });
  });

  it("respects a custom min", () => {
    expect(clampQuantity(0, 100, 0)).toEqual({ value: 0, message: null });
  });
});
