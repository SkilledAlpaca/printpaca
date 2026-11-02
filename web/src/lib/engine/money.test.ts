import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, it, expect } from "vitest";
import { toCents, roundCents, formatMoney } from "./money";

describe("toCents", () => {
  it("rounds half away from zero", () => {
    expect(toCents(1.005)).toBe(101);
    expect(toCents(-1.005)).toBe(-101);
    expect(toCents(2.674999)).toBe(267);
    expect(toCents(0)).toBe(0);
  });
  it("returns 0 for non-finite input", () => {
    expect(toCents(NaN)).toBe(0);
    expect(toCents(Infinity)).toBe(0);
  });
  it("guards -0 return", () => {
    expect(Object.is(toCents(-0.004), 0)).toBe(true);
  });
});

// Golden vectors for the tie cases every money-rounding call shares: 1.005*100 stores as 100.49999999999999
// (an underflowing tie that plain floor(x+0.5) rounds wrong without the epsilon) and 2.675*100 lands
// exactly on 267.5; plus their negatives.
describe("roundCents (canonical half-away-from-zero, with epsilon)", () => {
  it("rounds the two classic IEEE754 money gotchas up at the tie", () => {
    expect(roundCents(1.005 * 100)).toBe(101);
    expect(roundCents(2.675 * 100)).toBe(268);
  });
  it("rounds their negatives away from zero, mirroring the positive case", () => {
    expect(roundCents(-1.005 * 100)).toBe(-101);
    expect(roundCents(-2.675 * 100)).toBe(-268);
  });
  it("rounds an exact, non-underflowing tie away from zero", () => {
    expect(roundCents(0.5)).toBe(1);
    expect(roundCents(-0.5)).toBe(-1);
  });
  it("rounds ordinary non-tie values to the nearer integer", () => {
    expect(roundCents(267.4999)).toBe(267);
    expect(roundCents(-267.4999)).toBe(-267);
  });
  it("never returns -0", () => {
    expect(Object.is(roundCents(-0.004), 0)).toBe(true);
    expect(Object.is(roundCents(0), 0)).toBe(true);
  });
  it("returns 0 for non-finite input", () => {
    expect(roundCents(NaN)).toBe(0);
    expect(roundCents(Infinity)).toBe(0);
    expect(roundCents(-Infinity)).toBe(0);
  });
  it("stays inert (falls back to plain half-away-from-zero) past the epsilon's effective range", () => {
    // Past ~4.5e15 the 1e-9 epsilon underflows against the value, so this only checks nothing throws.
    expect(roundCents(1e16)).toBe(1e16);
  });
});

describe("formatMoney", () => {
  const usd = { symbol: "$", decimals: 2, code: "" };
  it("formats cents with symbol and decimals", () => {
    expect(formatMoney(1234, usd)).toBe("$12.34");
    expect(formatMoney(0, usd)).toBe("$0.00");
  });
  it("formats negatives with sign before symbol", () => {
    expect(formatMoney(-50, usd)).toBe("-$0.50");
  });
  it("respects configured decimals", () => {
    expect(formatMoney(1234, { symbol: "¥", decimals: 0, code: "" })).toBe("¥12");
  });
  it("groups integer digits with commas at 4+ digits", () => {
    expect(formatMoney(100000, usd)).toBe("$1,000.00");
    expect(formatMoney(123456789, usd)).toBe("$1,234,567.89");
    expect(formatMoney(-123456789, usd)).toBe("-$1,234,567.89");
  });
  it("groups with decimals set to 0", () => {
    expect(formatMoney(123456700, { symbol: "$", decimals: 0, code: "" })).toBe("$1,234,567");
    expect(formatMoney(999900, { symbol: "$", decimals: 0, code: "" })).toBe("$9,999");
  });
});

// Golden vectors shared with server/mail_templates_test.go via testdata/money-format.json, so the Go
// email formatter and formatMoney can't drift apart.
interface MoneyFormatFixture {
  cents: number;
  decimals: number;
  symbol: string;
  expected: string;
}

const fixturePath = resolve(__dirname, "../../../../testdata/money-format.json");
const fixtures: MoneyFormatFixture[] = JSON.parse(readFileSync(fixturePath, "utf-8"));

describe("formatMoney matches the shared money-format fixture", () => {
  it("has at least one fixture case", () => {
    expect(fixtures.length).toBeGreaterThan(0);
  });

  for (const c of fixtures) {
    it(`formatMoney(${c.cents}, {symbol: ${JSON.stringify(c.symbol)}, decimals: ${c.decimals}}) === ${JSON.stringify(c.expected)}`, () => {
      expect(formatMoney(c.cents, { symbol: c.symbol, decimals: c.decimals, code: "" })).toBe(c.expected);
    });
  }
});
