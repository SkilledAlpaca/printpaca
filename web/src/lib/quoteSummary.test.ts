import { describe, it, expect } from "vitest";
import type { OrderBreakdown } from "./engine/index";
import { quoteSummaryStats } from "./quoteSummary";

// The mapper only reads these fields; `bd` builds a one-part order from single-breakdown fields.
function bd(over: Record<string, unknown>): OrderBreakdown {
  const { valid = true, perUnitTotal = 1243, batchTotal = 1243, quantity = 1, pricing, perUnit } = over as Record<string, any>;
  const resolved = pricing?.resolvedPrice ?? 0;
  return {
    parts: [{ label: "Part 1", breakdown: { valid, perUnitTotal, batchTotal, quantity, perUnit: perUnit ?? [] }, recommendedPrice: resolved, resolvedPrice: resolved, itemRevenue: resolved * quantity }],
    batchTotal,
    itemRevenue: resolved * quantity,
    pricing,
    warnings: [],
    valid,
  } as unknown as OrderBreakdown;
}

const pricing = { valid: true, resolvedPrice: 2499, revenue: 4998, netProfit: 1256, achievedMarginPct: 50.3 };

describe("quoteSummaryStats", () => {
  it("always exposes batch cost when the breakdown is valid", () => {
    const s = quoteSummaryStats(bd({}), false);
    expect(s.costCents).toBe(1243);
    expect(s.pricingEnabled).toBe(false);
    expect(s.priceCents).toBeNull();
    expect(s.profitCents).toBeNull();
    expect(s.marginPct).toBeNull();
  });

  // batchTotal is the sum of the batch-rounded cost lines; perUnitTotal is round(batchTotal / quantity).
  it("at quantity 1, the displayed cost matches batchTotal, which equals perUnitTotal", () => {
    const s = quoteSummaryStats(bd({ perUnitTotal: 537, batchTotal: 537, quantity: 1 }), false);
    expect(s.costCents).toBe(537);
  });

  it("at quantity > 1, the displayed cost matches batchTotal, same field the breakdown's own Batch total row reads", () => {
    const s = quoteSummaryStats(bd({ perUnitTotal: 149, batchTotal: 596, quantity: 4 }), false);
    expect(s.costCents).toBe(596);
  });

  it("nulls the cost when the breakdown is invalid", () => {
    expect(quoteSummaryStats(bd({ valid: false }), false).costCents).toBeNull();
  });

  it("exposes batch price, profit, and margin when pricing is valid", () => {
    const s = quoteSummaryStats(bd({ quantity: 2, pricing }), true);
    expect(s.pricingEnabled).toBe(true);
    expect(s.priceCents).toBe(4998); // per-unit resolvedPrice times quantity
    expect(s.profitCents).toBe(1256);
    expect(s.marginPct).toBeCloseTo(50.3);
  });

  it("nulls the margin when revenue is zero, since a 0% margin would hide the loss", () => {
    const s = quoteSummaryStats(bd({ pricing: { ...pricing, revenue: 0, resolvedPrice: 0, netProfit: -1243, achievedMarginPct: 0 } }), true);
    expect(s.profitCents).toBe(-1243);
    expect(s.marginPct).toBeNull();
  });

  it("nulls pricing stats when pricing is invalid but keeps pricingEnabled", () => {
    const s = quoteSummaryStats(bd({ pricing: { ...pricing, valid: false } }), true);
    expect(s.pricingEnabled).toBe(true);
    expect(s.priceCents).toBeNull();
    expect(s.profitCents).toBeNull();
  });

  it("ignores pricing entirely when selling is disabled", () => {
    const s = quoteSummaryStats(bd({ pricing }), false);
    expect(s.pricingEnabled).toBe(false);
    expect(s.priceCents).toBeNull();
  });

  it("falls back to quantity 1 for display when quantity is not finite", () => {
    expect(quoteSummaryStats(bd({ valid: false, quantity: NaN }), false).quantity).toBe(1);
  });

  // Pins a 127g/7h30m PLA case that once disagreed by a cent ($5.36 vs $5.37): displayed cost equals the
  // sum of the rounded per-line amounts. The fixture is that case's known-correct output.
  it("displayed cost matches the sum of per-line amounts, not a separately rounded total", () => {
    const perUnit = [{ amount: 100 }, { amount: 5 }, { amount: 13 }, { amount: 6 }, { amount: 413 }, { amount: 0 }];
    const breakdown = bd({ perUnit, perUnitTotal: 537, batchTotal: 537, quantity: 1 });

    const displayedBreakdownSum = perUnit.reduce((sum, line) => sum + line.amount, 0);
    expect(displayedBreakdownSum).toBe(breakdown.parts[0]!.breakdown.perUnitTotal);

    const s = quoteSummaryStats(breakdown, false);
    expect(s.costCents).toBe(displayedBreakdownSum);
  });

  // With two or more parts resolvedPrice is an order total; the bar reads itemRevenue so it doesn't jump.
  it("reads the price from itemRevenue, never pricing.resolvedPrice, for a multi-part order", () => {
    const order = {
      parts: [
        { label: "A", breakdown: { valid: true, quantity: 2, batchTotal: 200, perUnitTotal: 100 }, itemRevenue: 600 },
        { label: "B", breakdown: { valid: true, quantity: 1, batchTotal: 300, perUnitTotal: 300 }, itemRevenue: 700 },
      ],
      batchTotal: 500,
      itemRevenue: 1300,
      pricing: { valid: true, resolvedPrice: 1300, netProfit: 400, achievedMarginPct: 30 },
      warnings: [],
      valid: true,
    } as unknown as OrderBreakdown;
    const s = quoteSummaryStats(order, true);
    expect(s.costCents).toBe(500);
    expect(s.priceCents).toBe(1300);
    expect(s.quantity).toBe(1);
  });

  it("nulls the cost when any part is invalid", () => {
    const order = bd({});
    order.parts.push({ ...order.parts[0]!, breakdown: { ...order.parts[0]!.breakdown, valid: false } });
    expect(quoteSummaryStats(order, false).costCents).toBeNull();
  });
});
