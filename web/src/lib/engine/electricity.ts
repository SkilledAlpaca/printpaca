import { validateField, hasErrors, type FieldWarning } from "./validate";
import type { EnergySchedule } from "./schedule";

export interface ElectricityInputs {
  watts: number;
  hours: number;
  rate: number;
}

export interface ElectricityResult {
  total: number;
  perHour: number;
  per24h: number;
  warnings: FieldWarning[];
  valid: boolean;
}

export interface EnergyInputs {
  mode: "flat" | "tou" | "schedule";
  flatRate?: number;
  peakRate?: number;
  offPeakRate?: number;
  offPeakFrac?: number;
  /** Schedule mode: a snapshot of the tariff, the zone it is read in, and the print's start as wall-clock
   * "YYYY-MM-DDTHH:MM" in that zone. The server prices from these, so a saved quote reprices identically. */
  schedule?: EnergySchedule;
  timezone?: string;
  start?: string;
}

/** Mirrors server/pricing.go's maxPrinterWatts so a value accepted here is never rejected by the server. */
const MAX_PRINTER_WATTS = 10000;

export function effectiveRate(e: EnergyInputs): number {
  if (e.mode === "flat") return e.flatRate ?? 0;
  // Schedule mode has no single rate: the cost depends on the start and duration (schedule.ts).
  if (e.mode === "schedule") return 0;
  const frac = Math.min(1, Math.max(0, e.offPeakFrac ?? 0));
  return frac * (e.offPeakRate ?? 0) + (1 - frac) * (e.peakRate ?? 0);
}

export function computeElectricity(i: ElectricityInputs): ElectricityResult {
  const warnings: FieldWarning[] = [
    ...validateField("watts", i.watts, { min: 0, max: MAX_PRINTER_WATTS }),
    ...validateField("hours", i.hours, { min: 0 }),
    ...validateField("rate", i.rate, { min: 0 }),
  ];
  const kw = i.watts / 1000;
  const perHour = kw * i.rate;
  return { total: kw * i.hours * i.rate, perHour, per24h: perHour * 24, warnings, valid: !hasErrors(warnings) };
}
