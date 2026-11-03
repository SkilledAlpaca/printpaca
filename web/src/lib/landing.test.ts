import { describe, expect, it } from "vitest";
import { hasShopActivity } from "./landing";

describe("hasShopActivity", () => {
  it("is false only on a fresh install", () => {
    expect(hasShopActivity({ quotes: 0, requests: 0, invoices: 0 })).toBe(false);
  });
  it("is true once any quote, request, or invoice exists", () => {
    expect(hasShopActivity({ quotes: 1, requests: 0, invoices: 0 })).toBe(true);
    expect(hasShopActivity({ quotes: 0, requests: 1, invoices: 0 })).toBe(true);
    expect(hasShopActivity({ quotes: 0, requests: 0, invoices: 1 })).toBe(true);
  });
});
