import type { EnergyConfig, EnergyInputs, PresetConfig, PrinterConfig, QuoteInputs } from "./engine/index";
import { splitSeconds } from "./gcodeTime";

/** cfg.energy to the engine's EnergyInputs. offPeakFrac/flatRate default to cfg's values (RequestsTab's
 * verification replay and PublicQuotePage have no per-quote override); the admin Quote tab's
 * state.svelte.ts toPartInputs passes its live form values instead. */
export function energyInputsFor(
  energy: EnergyConfig,
  offPeakFrac: number = energy.tou.defaultOffPeakFrac,
  flatRate: number = energy.flatRate,
  schedule?: { timezone: string; start: string },
): EnergyInputs {
  if (energy.mode === "schedule") {
    // A snapshot of the tariff travels with the quote, so a saved quote reprices identically after the shop's
    // schedule changes. An omitted start is sent as missing and the server asks for it.
    const out: EnergyInputs = { mode: "schedule", schedule: JSON.parse(JSON.stringify(energy.schedule)) as EnergyInputs["schedule"], timezone: schedule?.timezone ?? "UTC" };
    if (schedule?.start) out.start = schedule.start;
    return out;
  }
  return energy.mode === "tou"
    ? { mode: "tou", peakRate: energy.tou.peakRate, offPeakRate: energy.tou.offPeakRate, offPeakFrac }
    : { mode: "flat", flatRate };
}

/** null becomes NaN so the validator flags it, never a silent 0 (same as state.svelte.ts's num()). */
function num(v: number | null): number {
  return v === null ? NaN : v;
}

/** The printer + preset + material + energy + pricing to QuoteInputs mapping shared by RequestsTab's
 * verifyRequest and PublicQuotePage's live estimate; the two must compute identical quotes or verification
 * reports false mismatches. Not used by the admin Quote tab's toPartInputs, whose fields come from
 * independently editable form state. */
export interface QuoteInputBag {
  weightG: number | null;
  pricePerKg: number | null;
  wastePct: number | null;
  timeSeconds: number | null;
  printer: PrinterConfig;
  preset: PresetConfig;
  energy: EnergyInputs;
  quantity: number | null;
  pricing: QuoteInputs["pricing"];
}

export function buildQuoteInputs(bag: QuoteInputBag): QuoteInputs {
  const t = bag.timeSeconds === null ? { hours: NaN, minutes: NaN } : splitSeconds(bag.timeSeconds);
  return {
    weightG: num(bag.weightG),
    pricePerKg: num(bag.pricePerKg),
    wastePct: num(bag.wastePct),
    timeHours: t.hours,
    timeMinutes: t.minutes,
    watts: bag.printer.watts,
    energy: bag.energy,
    failureRatePct: bag.preset.failureRatePct,
    prepMinutes: bag.preset.prepMinutes,
    laborRate: bag.preset.laborRate,
    machineCost: bag.printer.purchaseCost,
    machineLifeHours: bag.printer.lifespanHours,
    monthlyFixed: bag.preset.monthlyFixed,
    monthlyHours: bag.preset.monthlyHours,
    quantity: num(bag.quantity),
    pricing: bag.pricing,
  };
}
