import { describe, expect, it } from "vitest";
import { dollarsToCents, itemTotalCents, itemUnitCents, nextSortOrders } from "./itemMath";

describe("itemMath", () => {
  it("computes unit and total in cents", () => {
    expect(itemUnitCents(1500, 250)).toBe(1750);
    expect(itemUnitCents(1500, -250)).toBe(1250);
    expect(itemTotalCents(1500, 250, 3)).toBe(5250);
    expect(itemTotalCents(1500, 0, 1)).toBe(1500);
  });
  it("converts dollars once, rounding half away from zero", () => {
    expect(dollarsToCents(19.99)).toBe(1999);
    expect(dollarsToCents(0.1 + 0.2)).toBe(30);
  });
  it("matches the engine's toCents on a negative tie", () => {
    // -0.005 dollars is a half-cent tie; toCents rounds half away from zero, to -1 cent.
    expect(dollarsToCents(-0.005)).toBe(-1);
  });

  describe("nextSortOrders", () => {
    it("swaps two distinct values regardless of direction", () => {
      expect(nextSortOrders(0, 1, true)).toEqual([1, 0]);
      expect(nextSortOrders(1, 0, false)).toEqual([0, 1]);
    });
    it("breaks a tie by moving `a` below `b` when moving down", () => {
      // a keeps its value and b is nudged one step below it, so a now sorts after b.
      const [aNext, bNext] = nextSortOrders(0, 0, true);
      expect(aNext).toBeGreaterThan(bNext);
    });
    it("breaks a tie by moving `a` above `b` when moving up", () => {
      const [aNext, bNext] = nextSortOrders(0, 0, false);
      expect(aNext).toBeLessThan(bNext);
    });
  });
});
