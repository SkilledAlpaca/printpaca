import { describe, it, expect } from "vitest";
import { computeElectricity, effectiveRate } from "./electricity";

describe("computeElectricity", () => {
  it("matches reference math (250W, 10h, $0.15)", () => {
    const r = computeElectricity({ watts: 250, hours: 10, rate: 0.15 });
    expect(r.total).toBeCloseTo(0.375, 10);
    expect(r.perHour).toBeCloseTo(0.0375, 10);
    expect(r.per24h).toBeCloseTo(0.9, 10);
    expect(r.valid).toBe(true);
  });
  it("zero anything → zero cost, still valid", () => {
    const r = computeElectricity({ watts: 0, hours: 5, rate: 0.2 });
    expect(r.total).toBe(0);
    expect(r.valid).toBe(true);
  });
  it("negative input → invalid with warning", () => {
    const r = computeElectricity({ watts: -5, hours: 1, rate: 0.15 });
    expect(r.valid).toBe(false);
    expect(r.warnings[0]?.field).toBe("watts");
  });
  it("watts above 10000 → warning with code above-max", () => {
    const r = computeElectricity({ watts: 10001, hours: 1, rate: 0.15 });
    expect(r.valid).toBe(false);
    expect(r.warnings.some((w) => w.code === "above-max")).toBe(true);
  });
  it("NaN rate → warning with code nan", () => {
    const r = computeElectricity({ watts: 100, hours: 1, rate: NaN });
    expect(r.valid).toBe(false);
    expect(r.warnings.some((w) => w.code === "nan")).toBe(true);
  });
  it("per24h === perHour * 24 exactly", () => {
    const r = computeElectricity({ watts: 1, hours: 1, rate: 0.07 });
    expect(r.per24h).toBe(r.perHour * 24);
  });
});

describe("effectiveRate", () => {
  it("flat mode returns flatRate", () => {
    expect(effectiveRate({ mode: "flat", flatRate: 0.15 })).toBe(0.15);
  });
  it("tou mode blends peak and off-peak", () => {
    // 70% off-peak at 0.10 + 30% peak at 0.30 = 0.16
    expect(
      effectiveRate({ mode: "tou", peakRate: 0.3, offPeakRate: 0.1, offPeakFrac: 0.7 }),
    ).toBeCloseTo(0.16, 10);
  });
  it("tou mode clamps out-of-range fractions", () => {
    // offPeakFrac -1 clamps to 0
    expect(effectiveRate({ mode: "tou", peakRate: 0.3, offPeakRate: 0.1, offPeakFrac: -1 })).toBeCloseTo(0.3, 10);
    // offPeakFrac 1.5 clamps to 1
    expect(effectiveRate({ mode: "tou", peakRate: 0.3, offPeakRate: 0.1, offPeakFrac: 1.5 })).toBeCloseTo(0.1, 10);
  });
  it("tou mode boundary fractions", () => {
    expect(effectiveRate({ mode: "tou", peakRate: 0.3, offPeakRate: 0.1, offPeakFrac: 0 })).toBe(0.3);
    expect(effectiveRate({ mode: "tou", peakRate: 0.3, offPeakRate: 0.1, offPeakFrac: 1 })).toBe(0.1);
  });
});
