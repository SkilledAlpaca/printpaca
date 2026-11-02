import { describe, it, expect } from "vitest";
import {
  computeSpoolPrice,
  computeRemainingLength,
  weightFromLength,
  estimateWeightFromVolume,
  DEFAULT_SOLIDITY_PCT,
} from "./filament";

describe("computeSpoolPrice", () => {
  it("matches reference math ($19.99 / 1 kg)", () => {
    const r = computeSpoolPrice(19.99, 1);
    expect(r.perKg).toBeCloseTo(19.99, 10);
    expect(r.perGram).toBeCloseTo(0.01999, 10);
    expect(r.valid).toBe(true);
  });
  it("zero spool weight is invalid, not Infinity", () => {
    const r = computeSpoolPrice(19.99, 0);
    expect(r.valid).toBe(false);
    expect(r.perKg).toBe(0);
  });
});

describe("computeRemainingLength", () => {
  it("300 g of 1.75 mm PLA (1.24 g/cm³) ≈ 100.6 m", () => {
    // vol = 300/1.24 = 241.935 cm³; area = π·0.875² = 2.4053 mm²; length ≈ 100.6 m
    const r = computeRemainingLength(300, 1.75, 1.24);
    expect(r.lengthM).toBeCloseTo(100.6, 1);
    expect(r.valid).toBe(true);
  });
  it("zero diameter or density is invalid", () => {
    expect(computeRemainingLength(300, 0, 1.24).valid).toBe(false);
    expect(computeRemainingLength(300, 1.75, 0).valid).toBe(false);
  });
});

describe("weightFromLength", () => {
  it("round-trips with computeRemainingLength", () => {
    // Start with 300g, convert to length, then back to weight
    const r = computeRemainingLength(300, 1.75, 1.24);
    expect(r.valid).toBe(true);
    const g = weightFromLength(r.lengthM, 1.75, 1.24);
    expect(g).toBeCloseTo(300, 10);
  });
});

describe("estimateWeightFromVolume", () => {
  it("volume x density x solidity/100", () => {
    expect(estimateWeightFromVolume(10, 1.24, 30)).toBeCloseTo(3.72, 10);
  });
  it("100% solidity treats the mesh as fully solid plastic", () => {
    expect(estimateWeightFromVolume(10, 1.24, 100)).toBeCloseTo(12.4, 10);
  });
  it("default solidity constant is 30", () => {
    expect(DEFAULT_SOLIDITY_PCT).toBe(30);
  });
});

describe("validation bounds", () => {
  it("grams: min 0 (negative value fails validation)", () => {
    const r = computeRemainingLength(-1, 1.75, 1.24);
    expect(r.valid).toBe(false);
    expect(r.warnings.length).toBeGreaterThan(0);
  });
  it("diameterMm: min 0.1 (value 0.05 fails validation)", () => {
    const r = computeRemainingLength(300, 0.05, 1.24);
    expect(r.valid).toBe(false);
    expect(r.warnings.length).toBeGreaterThan(0);
  });
  it("diameterMm: max 5 (value 5.5 fails validation)", () => {
    const r = computeRemainingLength(300, 5.5, 1.24);
    expect(r.valid).toBe(false);
    expect(r.warnings.length).toBeGreaterThan(0);
  });
  it("density: min 0.1 (value 0.05 fails validation)", () => {
    const r = computeRemainingLength(300, 1.75, 0.05);
    expect(r.valid).toBe(false);
    expect(r.warnings.length).toBeGreaterThan(0);
  });
  it("density: max 25 (value 25.5 fails validation)", () => {
    const r = computeRemainingLength(300, 1.75, 25.5);
    expect(r.valid).toBe(false);
    expect(r.warnings.length).toBeGreaterThan(0);
  });
  it("all bounds satisfied: 300g, 1.75mm, 1.24 g/cm³ is valid", () => {
    const r = computeRemainingLength(300, 1.75, 1.24);
    expect(r.valid).toBe(true);
    expect(r.warnings.length).toBe(0);
  });
});
