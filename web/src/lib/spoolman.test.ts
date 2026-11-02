import { describe, expect, it } from "vitest";
import {
  isLowStock,
  isValidSpool,
  spoolLabel,
  spoolOverrideFrom,
  spoolPricePerKg,
  type SpoolDto,
} from "./spoolman";

function spool(overrides: Partial<SpoolDto> = {}): SpoolDto {
  return {
    id: 7,
    remaining_weight: 850,
    filament: { name: "PLA Basic", material: "PLA", density: 1.24, weight: 1000, price: 20 },
    ...overrides,
  };
}

describe("spoolLabel", () => {
  it("joins vendor, name, and id when all present", () => {
    const s = spool({ filament: { ...spool().filament, vendor: { name: "Prusament" } } });
    expect(spoolLabel(s)).toBe("Prusament PLA Basic #7");
  });

  it("falls back to material when name is missing", () => {
    const s = spool({ filament: { density: 1.24, material: "PETG" } });
    expect(spoolLabel(s)).toBe("PETG #7");
  });

  it("never empty: id alone when vendor, name, and material are all missing", () => {
    const s = spool({ filament: { density: 1.24 } });
    expect(spoolLabel(s)).toBe("#7");
  });
});

describe("spoolPricePerKg", () => {
  it("derives $/kg from filament price and weight (weight is grams) when the spool has no own price", () => {
    expect(spoolPricePerKg(spool({ filament: { density: 1.24, price: 20, weight: 1000 } }))).toBe(20);
    expect(spoolPricePerKg(spool({ filament: { density: 1.24, price: 10, weight: 500 } }))).toBe(20);
  });

  it("spool-level price/initial_weight win over the filament preset", () => {
    const s = spool({
      price: 30,
      initial_weight: 1000,
      filament: { density: 1.24, price: 20, weight: 1000 },
    });
    expect(spoolPricePerKg(s)).toBe(30);
  });

  it("falls back to filament values when the spool doesn't record its own price/weight", () => {
    const s = spool({ filament: { density: 1.24, price: 20, weight: 1000 } });
    expect(spoolPricePerKg(s)).toBe(20);
  });

  it("null when neither spool nor filament has a price", () => {
    expect(spoolPricePerKg(spool({ filament: { density: 1.24, weight: 1000 } }))).toBeNull();
  });

  it("null when neither spool nor filament has a weight (denominator missing)", () => {
    expect(spoolPricePerKg(spool({ filament: { density: 1.24, price: 20 } }))).toBeNull();
  });

  it("null when price is zero (falsy, not just missing)", () => {
    expect(spoolPricePerKg(spool({ filament: { density: 1.24, price: 0, weight: 1000 } }))).toBeNull();
  });

  it("null when weight is zero", () => {
    expect(spoolPricePerKg(spool({ filament: { density: 1.24, price: 20, weight: 0 } }))).toBeNull();
  });
});

describe("spoolOverrideFrom", () => {
  it("composes label, cent-rounded derived price, density, remaining weight, and spool id", () => {
    const s = spool();
    const o = spoolOverrideFrom(s);
    expect(o).toEqual({ label: "PLA Basic #7", pricePerKg: 20, density: 1.24, remainingG: 850, spoolId: 7 });
  });

  it("rounds pricePerKg to cents", () => {
    const s = spool({ filament: { density: 1.24, price: 10, weight: 3000 } }); // 3.3333.. $/kg
    expect(spoolOverrideFrom(s).pricePerKg).toBe(3.33);
  });

  it("pricePerKg is null (never 0) when the spool has no usable price", () => {
    const s = spool({ filament: { density: 1.24, weight: 1000 } });
    expect(spoolOverrideFrom(s).pricePerKg).toBeNull();
  });

  it("remainingG is null, not 0, when Spoolman omits remaining_weight", () => {
    const s = spool({ remaining_weight: undefined });
    expect(spoolOverrideFrom(s).remainingG).toBeNull();
  });

  it("remainingG is 0 when Spoolman reports a confirmed-empty spool", () => {
    const s = spool({ remaining_weight: 0 });
    expect(spoolOverrideFrom(s).remainingG).toBe(0);
  });
});

describe("isLowStock", () => {
  it("true when remaining is strictly less than needed", () => {
    expect(isLowStock(100, 150)).toBe(true);
  });

  it("false when remaining equals needed (exact fit is not low)", () => {
    expect(isLowStock(150, 150)).toBe(false);
  });

  it("false when remaining exceeds needed", () => {
    expect(isLowStock(500, 150)).toBe(false);
  });

  it("false when nothing is needed yet (weight not entered)", () => {
    expect(isLowStock(0, 0)).toBe(false);
  });

  it("false when remaining is unknown (null), even if a lot is needed", () => {
    expect(isLowStock(null, 500)).toBe(false);
  });
});

describe("isValidSpool", () => {
  it("accepts a well-formed spool", () => {
    expect(isValidSpool(spool())).toBe(true);
  });

  it("rejects null, non-objects, and elements missing id/filament without throwing", () => {
    const fixtures: unknown[] = [null, undefined, 42, "spool", {}, { id: 7 }, { filament: {} }, { id: "7", filament: {} }];
    for (const f of fixtures) {
      expect(() => isValidSpool(f)).not.toThrow();
      expect(isValidSpool(f)).toBe(false);
    }
  });

  it("filters a mixed-shape array down to only the valid elements", () => {
    const good = spool({ id: 1 });
    const mixed: unknown[] = [good, null, { notASpool: true }, 42, spool({ id: 2 })];
    const kept = mixed.filter(isValidSpool);
    expect(kept.map((s) => s.id)).toEqual([1, 2]);
  });
});
