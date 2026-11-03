// Types and constants for the quote/pricing domain. server/pricing.go is the sole pricing implementation;
// QuoteInputs and CostBreakdown are the compute request and response shapes.

import type { Cents } from "./money";
import type { EnergyInputs } from "./electricity";
import type { EnergyPeriod } from "./schedule";
import type { FieldWarning } from "./validate";
import type { FeeSchedule } from "./types";

interface PricingInputs {
  strategy: "margin" | "markup";
  marginPct: number;
  markupMultiplier: number;
  feeSchedule?: FeeSchedule;
  feeScheduleName?: string;
  taxRatePct: number;
  shippingCharge: number; // customer pays you
  shippingCost: number;   // postage + packaging you pay
  /** Resolved on presence, not truthiness: only an absent key uses the recommended price. An explicit 0 is
   * a deliberate giveaway; a negative flows through as negative revenue and marks the quote invalid.
   * Implementation: server/pricing.go ComputePricing. */
  salePriceOverride?: number;
}

interface FeeLine { label: string; amount: Cents; }

export interface PricingResult {
  strategy: "margin" | "markup";
  recommendedPrice: Cents;
  resolvedPrice: Cents;
  revenue: Cents;
  fees: Cents;
  feeLines: FeeLine[];
  tax: Cents;
  shippingCharge: Cents;
  shippingCost: Cents;
  netProfit: Cents;
  achievedMarginPct: number;
  feeScheduleName?: string;
  warnings: FieldWarning[];
  valid: boolean;
}

// One attached hardware entry's resolved values, not a live reference into the config catalog, so a saved
// quote keeps its original hardware pricing.
interface HardwareLine {
  name: string;
  unitCost: number;
  qtyPerUnit: number;
  lostOnFailure: boolean;
}

export interface QuoteInputs {
  weightG: number;
  pricePerKg: number;
  wastePct: number;
  timeHours: number;
  timeMinutes: number;
  watts: number;
  energy: EnergyInputs;
  failureRatePct: number;
  prepMinutes: number;
  laborRate: number;
  machineCost: number;
  machineLifeHours: number;
  monthlyFixed: number;
  monthlyHours: number;
  quantity: number;
  hardware?: HardwareLine[];
  pricing?: PricingInputs;
}

export interface EngineOptions {
  laborPerUnit: boolean;
}

export const defaultEngineOptions: EngineOptions = {
  laborPerUnit: false,
};

export interface LineItem {
  id: string;
  label: string;
  amount: Cents;
  formula: string;
  inputs: Record<string, number | string>;
  warnings: FieldWarning[];
  /** The electricity line's split by named tariff period, schedule mode only. */
  periods?: EnergyPeriod[];
}

export interface CostBreakdown {
  perUnit: LineItem[];
  perUnitTotal: Cents;
  quantity: number;
  /** Sum of the cost lines, each extended to the batch in float and rounded once; perUnitTotal is round(batchTotal / quantity). */
  batchTotal: Cents;
  pricing?: PricingResult;
  warnings: FieldWarning[];
  valid: boolean;
}

// Orders: a quote is 1 to MAX_ORDER_PARTS parts (server/pricing_order.go). The `inputs` bag is OrderInputs
// ({parts, pricing}) and compute answers an OrderBreakdown; a one-part order prices exactly like the single
// quote it equals. QuoteInputs above is the engine's per-part shape.

export const MAX_ORDER_PARTS = 20;
export const MAX_PART_LABEL_CHARS = 120;

/** One part: a single-part QuoteInputs without its own `pricing`, plus a label and an optional per-unit
 * override (same presence rule as PricingInputs.salePriceOverride). */
export interface PartInputs extends Omit<QuoteInputs, "pricing"> {
  label: string;
  salePriceOverride?: number;
}

type OrderPricingInputs = Omit<PricingInputs, "salePriceOverride">;

export interface OrderInputs {
  parts: PartInputs[];
  pricing?: OrderPricingInputs;
}

export interface OrderPart {
  label: string;
  breakdown: CostBreakdown;
  recommendedPrice: Cents;
  resolvedPrice: Cents;
  itemRevenue: Cents;
}

export interface OrderBreakdown {
  parts: OrderPart[];
  batchTotal: Cents;
  /** Sum of resolved x quantity over the parts; 0 without a pricing block. Read this, not
   * pricing.resolvedPrice, for item revenue. */
  itemRevenue: Cents;
  pricing?: PricingResult;
  warnings: FieldWarning[];
  valid: boolean;
}
