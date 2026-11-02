import { validateField, hasErrors, type FieldWarning } from "./validate";

export interface SpoolPriceResult {
  perKg: number;
  perGram: number;
  warnings: FieldWarning[];
  valid: boolean;
}

export function computeSpoolPrice(spoolPrice: number, spoolWeightKg: number): SpoolPriceResult {
  const warnings: FieldWarning[] = [
    ...validateField("spoolPrice", spoolPrice, { min: 0 }),
    ...validateField("spoolWeightKg", spoolWeightKg, { min: 0.001 }),
  ];
  const valid = !hasErrors(warnings) && spoolWeightKg > 0;
  const perKg = valid ? spoolPrice / spoolWeightKg : 0;
  return { perKg, perGram: perKg / 1000, warnings, valid };
}

export interface LengthResult {
  lengthM: number;
  warnings: FieldWarning[];
  valid: boolean;
}

function crossSectionMm2(diameterMm: number): number {
  return Math.PI * (diameterMm / 2) ** 2;
}

export function computeRemainingLength(
  grams: number,
  diameterMm: number,
  density: number,
): LengthResult {
  const warnings: FieldWarning[] = [
    ...validateField("grams", grams, { min: 0 }),
    ...validateField("diameterMm", diameterMm, { min: 0.1, max: 5 }),
    ...validateField("density", density, { min: 0.1, max: 25 }),
  ];
  const valid = !hasErrors(warnings) && diameterMm > 0 && density > 0;
  if (!valid) return { lengthM: 0, warnings, valid };
  const volumeMm3 = (grams / density) * 1000;
  const lengthM = volumeMm3 / crossSectionMm2(diameterMm) / 1000;
  return { lengthM, warnings, valid };
}

/** Default solidity for geometry-only weight estimates (STL / unsliced 3MF): a naive stand-in for typical
 * infill plus shells. */
/** Unrounded grams for a filament length; inverse of computeRemainingLength. Module-only: not re-exported from index.ts. */
export function weightFromLength(lengthM: number, diameterMm: number, density: number): number {
  const volumeMm3 = lengthM * 1000 * crossSectionMm2(diameterMm);
  return (volumeMm3 / 1000) * density;
}

export const DEFAULT_SOLIDITY_PCT = 30;

const MIN_SOLIDITY_PCT = 5;
const MAX_SOLIDITY_PCT = 100;

export function estimateWeightFromVolume(volumeCm3: number, density: number, solidityPct: number): number {
  return volumeCm3 * density * (solidityPct / 100);
}

/** Clamps a cleared or non-finite solidity input to the bounds/default, so the estimate never becomes 0 g
 * or renders "null%". */
export function clampSolidityPct(input: number | null): number {
  const v = input ?? DEFAULT_SOLIDITY_PCT;
  if (!Number.isFinite(v)) return DEFAULT_SOLIDITY_PCT;
  return Math.min(MAX_SOLIDITY_PCT, Math.max(MIN_SOLIDITY_PCT, v));
}
