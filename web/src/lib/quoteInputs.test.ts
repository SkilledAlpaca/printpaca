import { describe, expect, it } from "vitest";
import type { EnergyConfig, PresetConfig, PrinterConfig } from "./engine/index";
import { buildQuoteInputs, energyInputsFor } from "./quoteInputs";

const FLAT: EnergyConfig = { mode: "flat", flatRate: 0.15, tou: { peakRate: 0.3, offPeakRate: 0.1, defaultOffPeakFrac: 0.7 } };
const TOU: EnergyConfig = { mode: "tou", flatRate: 0.15, tou: { peakRate: 0.3, offPeakRate: 0.1, defaultOffPeakFrac: 0.7 } };

describe("energyInputsFor", () => {
  it("maps a flat config using cfg's own flat rate by default", () => {
    expect(energyInputsFor(FLAT)).toEqual({ mode: "flat", flatRate: 0.15 });
  });

  it("maps a tou config using cfg's own default off-peak fraction", () => {
    expect(energyInputsFor(TOU)).toEqual({ mode: "tou", peakRate: 0.3, offPeakRate: 0.1, offPeakFrac: 0.7 });
  });

  it("substitutes a caller-supplied off-peak fraction (the admin form's live override)", () => {
    expect(energyInputsFor(TOU, 0.5)).toEqual({ mode: "tou", peakRate: 0.3, offPeakRate: 0.1, offPeakFrac: 0.5 });
  });

  it("substitutes a caller-supplied flat rate (the admin form's live override)", () => {
    expect(energyInputsFor(FLAT, undefined, 0.22)).toEqual({ mode: "flat", flatRate: 0.22 });
  });
});

const PRINTER: PrinterConfig = { id: "p1", name: "Printer", watts: 250, purchaseCost: 400, lifespanHours: 4000, filamentDiameterMm: 1.75 };
const PRESET: PresetConfig = { failureRatePct: 5, prepMinutes: 5, laborRate: 12, monthlyFixed: 0, monthlyHours: 100 };
const ENERGY = { mode: "flat" as const, flatRate: 0.15 };

describe("buildQuoteInputs", () => {
  const base = {
    weightG: 100,
    pricePerKg: 20,
    wastePct: 5,
    timeSeconds: 3600,
    printer: PRINTER,
    preset: PRESET,
    energy: ENERGY,
    quantity: 2,
    pricing: undefined,
  };

  it("splits timeSeconds into hours/minutes and pulls watts/machineCost/machineLifeHours off the printer", () => {
    const inputs = buildQuoteInputs(base);
    expect(inputs.timeHours).toBe(1);
    expect(inputs.timeMinutes).toBe(0);
    expect(inputs.watts).toBe(250);
    expect(inputs.machineCost).toBe(400);
    expect(inputs.machineLifeHours).toBe(4000);
  });

  it("pulls failureRatePct/prepMinutes/laborRate/monthlyFixed/monthlyHours off the preset", () => {
    const inputs = buildQuoteInputs(base);
    expect(inputs.failureRatePct).toBe(5);
    expect(inputs.prepMinutes).toBe(5);
    expect(inputs.laborRate).toBe(12);
    expect(inputs.monthlyFixed).toBe(0);
    expect(inputs.monthlyHours).toBe(100);
  });

  it("passes weightG/pricePerKg/wastePct/quantity through unchanged when non-null", () => {
    const inputs = buildQuoteInputs(base);
    expect(inputs.weightG).toBe(100);
    expect(inputs.pricePerKg).toBe(20);
    expect(inputs.wastePct).toBe(5);
    expect(inputs.quantity).toBe(2);
  });

  it("turns a null weightG/pricePerKg/wastePct/quantity into NaN, never a silent 0", () => {
    const inputs = buildQuoteInputs({ ...base, weightG: null, pricePerKg: null, wastePct: null, quantity: null });
    expect(inputs.weightG).toBeNaN();
    expect(inputs.pricePerKg).toBeNaN();
    expect(inputs.wastePct).toBeNaN();
    expect(inputs.quantity).toBeNaN();
  });

  it("turns a null timeSeconds into NaN hours/minutes rather than splitting", () => {
    const inputs = buildQuoteInputs({ ...base, timeSeconds: null });
    expect(inputs.timeHours).toBeNaN();
    expect(inputs.timeMinutes).toBeNaN();
  });

  it("carries the energy input and optional pricing straight through", () => {
    const pricing = { strategy: "margin" as const, marginPct: 30, markupMultiplier: 2.5, taxRatePct: 0, shippingCharge: 0, shippingCost: 0 };
    const inputs = buildQuoteInputs({ ...base, pricing });
    expect(inputs.energy).toBe(ENERGY);
    expect(inputs.pricing).toBe(pricing);
  });
});
