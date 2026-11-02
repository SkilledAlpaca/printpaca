import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, it, expect } from "vitest";
import { invoiceTotals, roundHalfUpCents, type InvoiceLineInput } from "./invoice";

// The vector table is testdata/invoice-totals.json, shared with server/invoices_test.go so the Go and
// TypeScript totals can't drift.
interface Vector {
  name: string;
  lines: InvoiceLineInput[];
  invoiceDiscountCents: number;
  taxBp: number;
  lineAmounts: number[];
  subtotalCents: number;
  taxCents: number;
  totalCents: number;
}
const vectors: Vector[] = JSON.parse(
  readFileSync(resolve(__dirname, "../../../../testdata/invoice-totals.json"), "utf-8"),
);

describe("invoiceTotals", () => {
  it("has vectors", () => {
    expect(vectors.length).toBeGreaterThan(0);
  });

  for (const v of vectors) {
    it(v.name, () => {
      const r = invoiceTotals(v.lines, v.invoiceDiscountCents, v.taxBp);
      expect(r.lineAmounts).toEqual(v.lineAmounts);
      expect(r.subtotalCents).toBe(v.subtotalCents);
      expect(r.taxCents).toBe(v.taxCents);
      expect(r.totalCents).toBe(v.totalCents);
    });
  }
});

describe("roundHalfUpCents", () => {
  it("rounds a tie up for the non-negative inputs invoiceTotals passes", () => {
    expect(roundHalfUpCents(0.5)).toBe(1);
    expect(roundHalfUpCents(1.5)).toBe(2);
  });
  it("rounds a negative tie toward positive infinity", () => {
    expect(roundHalfUpCents(-0.5)).toBe(0);
  });
  it("returns 0 for non-finite input", () => {
    expect(roundHalfUpCents(NaN)).toBe(0);
    expect(roundHalfUpCents(Infinity)).toBe(0);
    expect(roundHalfUpCents(-Infinity)).toBe(0);
  });
});

describe("roundHalfUpCents epsilon", () => {
  it("rounds IEEE 754 ties that land just under .5 up (1.005 x 100)", () => {
    expect(1.005 * 100).toBeLessThan(100.5);
    expect(roundHalfUpCents(1.005 * 100)).toBe(101);
    expect(roundHalfUpCents(2.675 * 100)).toBe(268);
    expect(roundHalfUpCents(100.4)).toBe(100);
  });
});
