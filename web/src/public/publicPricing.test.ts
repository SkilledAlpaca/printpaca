import { describe, expect, it } from "vitest";
import { DEFAULT_MAX_ORDER_QTY, isValidQuantity, normalizedQuantityText, overCapMessage, perUnitCents, telHref } from "./publicPricing";

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

describe("overCapMessage", () => {
  it("is empty within the cap, or for a blank entry", () => {
    expect(overCapMessage(100, 100)).toBe("");
    expect(overCapMessage(null, 100)).toBe("");
  });

  it("names the policy cap, with a thousands separator", () => {
    expect(overCapMessage(101, 100)).toBe("Max 100 per order.");
    expect(overCapMessage(10001, 10000)).toBe("Max 10,000 per order.");
  });

  it("names the stock when stock is the limit", () => {
    expect(overCapMessage(9, 3, "stock")).toBe("Only 3 in stock.");
  });
});

describe("normalizedQuantityText", () => {
  it("rewrites scientific notation to a plain integer", () => {
    expect(normalizedQuantityText("1e3")).toBe("1000");
    expect(normalizedQuantityText(" 2E2 ")).toBe("200");
    expect(normalizedQuantityText("1.5e1")).toBe("15");
  });

  it("leaves plain, fractional, and non-numeric text alone", () => {
    for (const raw of ["12", "2.5", "1.5e0", "abc", "", "1e-1"]) expect(normalizedQuantityText(raw)).toBeNull();
  });
});

describe("isValidQuantity", () => {
  it("accepts whole numbers in range and rejects everything else", () => {
    expect(isValidQuantity(1, 100)).toBe(true);
    expect(isValidQuantity(100, 100)).toBe(true);
    for (const bad of [null, 0, -3, 2.5, 101, Number.NaN]) expect(isValidQuantity(bad, 100)).toBe(false);
  });
});

describe("telHref", () => {
  it("keeps digits and a leading plus", () => {
    expect(telHref("(555) 010-0142")).toBe("tel:5550100142");
    expect(telHref("+1 555-010-0142")).toBe("tel:+15550100142");
  });
});

describe("DEFAULT_MAX_ORDER_QTY", () => {
  it("is the shop default of 100", () => {
    expect(DEFAULT_MAX_ORDER_QTY).toBe(100);
  });
});
