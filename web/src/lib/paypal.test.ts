import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { paypalPayLink } from "./paypal";

interface Row {
  name: string;
  handle: string;
  code: string;
  decimals: number;
  status: string;
  balanceCents: number;
  expected: string;
}

const rows: Row[] = JSON.parse(
  readFileSync(resolve(__dirname, "../../../testdata/paypal-link.json"), "utf8"),
);

describe("paypalPayLink matches the shared paypal-link fixture", () => {
  it("has rows", () => {
    expect(rows.length).toBeGreaterThan(0);
  });
  for (const r of rows) {
    it(r.name, () => {
      const got = paypalPayLink(
        r.handle,
        { symbol: "$", decimals: r.decimals, code: r.code },
        { status: r.status, balanceCents: r.balanceCents },
      );
      expect(got).toBe(r.expected);
    });
  }
});
