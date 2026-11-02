import type { Cents, CostBreakdown } from "./engine/index";

export interface BreakdownSegment {
  id: string;
  label: string;
  amount: Cents;
  percent: number;
}

/** Stacked-bar segments: one per positive line, sized by share of perUnitTotal. Returns [] if any line is
 * invalid, so the bar never shows partly-broken numbers. */
export function computeBreakdownSegments(breakdown: CostBreakdown): BreakdownSegment[] {
  const anyLineInvalid = breakdown.perUnit.some((line) => line.warnings.some((w) => w.severity === "error"));
  if (anyLineInvalid || breakdown.perUnitTotal <= 0) return [];

  return breakdown.perUnit
    .filter((line) => line.amount > 0)
    .map((line) => ({
      id: line.id,
      label: line.label,
      amount: line.amount,
      percent: (line.amount / breakdown.perUnitTotal) * 100,
    }));
}
