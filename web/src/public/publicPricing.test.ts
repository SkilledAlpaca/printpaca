import { describe, expect, it } from "vitest";
import { clampQuantity, isValidQuantity, MAX_ITEM_ORDER_QTY, perUnitCents, rateLimitMessage } from "./publicPricing";

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
    expect(result.message).toBe("Max 100 per order.");
  });

  it("names the stock when stock is the limit", () => {
    expect(clampQuantity(9, 3, 1, "stock")).toEqual({ value: 3, message: "Only 3 in stock." });
  });

  it("clamps a value below the default min (1) up to 1, with no message", () => {
    expect(clampQuantity(0, 100)).toEqual({ value: 1, message: null });
  });

  it("respects a custom min", () => {
    expect(clampQuantity(0, 100, 0)).toEqual({ value: 0, message: null });
  });
});

describe("isValidQuantity", () => {
  it("accepts whole numbers in range and rejects everything else", () => {
    expect(isValidQuantity(1, 100)).toBe(true);
    expect(isValidQuantity(100, 100)).toBe(true);
    for (const bad of [null, 0, -3, 2.5, 101, Number.NaN]) expect(isValidQuantity(bad, 100)).toBe(false);
  });
});

describe("rateLimitMessage", () => {
  it("offers the shop's email and phone", () => {
    expect(rateLimitMessage({ email: "shop@example.com", phone: "555-0100" })).toBe(
      "We've had a lot of requests from your connection. Try again later or email shop@example.com or call 555-0100.",
    );
  });
  it("uses whichever contact exists, or none", () => {
    expect(rateLimitMessage({ email: "", phone: "555-0100" })).toContain("or call 555-0100.");
    expect(rateLimitMessage({ email: "", phone: "" })).toBe("We've had a lot of requests from your connection. Try again later.");
  });
});

describe("MAX_ITEM_ORDER_QTY", () => {
  it("is the 10000 per-order ceiling the clamp message names", () => {
    expect(MAX_ITEM_ORDER_QTY).toBe(10000);
    expect(clampQuantity(10001, MAX_ITEM_ORDER_QTY)).toEqual({ value: 10000, message: "Max 10000 per order." });
    expect(isValidQuantity(10000, MAX_ITEM_ORDER_QTY)).toBe(true);
  });
});
