import { beforeEach, describe, it, expect } from "vitest";
import type { EffectiveConfig, HardwareConfig } from "./engine/index";
import {
  addPart,
  defaultOrderForm,
  defaultPartState,
  duplicatePart,
  effectiveConfig,
  parseFormState,
  nextPartLabel,
  pruneMissingHardware,
  quote,
  reconcileWithConfig,
  removePart,
  restoreFormState,
  setActivePart,
  startOnePartQuote,
  timePair,
  toFormState,
  toOrderInputs,
} from "./state.svelte";
import { demoLocal } from "./demoLocal.svelte";

function config(overrides: Partial<EffectiveConfig> = {}): EffectiveConfig {
  return {
    currency: { symbol: "$", decimals: 2, code: "" },
    demoMode: false,
    materials: [],
    ...overrides,
  } as EffectiveConfig;
}

describe("timePair", () => {
  it("keeps both halves NaN when both are blank, so the engine flags missing time", () => {
    expect(timePair(null, null)).toEqual({ hours: NaN, minutes: NaN });
  });

  it("defaults minutes to 0 when only hours is entered", () => {
    expect(timePair(2, null)).toEqual({ hours: 2, minutes: 0 });
  });

  it("defaults hours to 0 when only minutes is entered", () => {
    expect(timePair(null, 45)).toEqual({ hours: 0, minutes: 45 });
  });

  it("passes both values through unchanged when neither is blank", () => {
    expect(timePair(1, 30)).toEqual({ hours: 1, minutes: 30 });
  });
});

describe("toOrderInputs: hardware resolution", () => {
  function hardwareCfg(hardware: HardwareConfig[]): EffectiveConfig {
    return {
      ...config(),
      energy: { mode: "flat", flatRate: 0.15, tou: { peakRate: 0.3, offPeakRate: 0.1, defaultOffPeakFrac: 0.7 } },
      hardware,
    } as EffectiveConfig;
  }

  beforeEach(() => {
    quote.parts[0]!.hardware = [];
  });

  it("resolves a row's catalog reference into the engine's full HardwareLine shape", () => {
    quote.parts[0]!.hardware = [{ name: "magnet", qtyPerUnit: 3 }];
    const cfg = hardwareCfg([{ name: "magnet", unitCost: 1.5, lostOnFailure: true }]);
    const inputs = toOrderInputs(cfg).parts[0]!;
    expect(inputs.hardware).toEqual([{ name: "magnet", unitCost: 1.5, qtyPerUnit: 3, lostOnFailure: true }]);
  });

  it("drops a row whose name no longer matches any catalog entry (renamed/removed since attached)", () => {
    quote.parts[0]!.hardware = [{ name: "no-longer-in-catalog", qtyPerUnit: 1 }];
    const cfg = hardwareCfg([{ name: "magnet", unitCost: 1.5, lostOnFailure: true }]);
    const inputs = toOrderInputs(cfg).parts[0]!;
    expect(inputs.hardware).toBeUndefined();
  });

  it("leaves inputs.hardware unset for an empty quote.parts[0]!.hardware array (byte-identical to absent)", () => {
    quote.parts[0]!.hardware = [];
    const cfg = hardwareCfg([{ name: "magnet", unitCost: 1.5, lostOnFailure: true }]);
    const inputs = toOrderInputs(cfg).parts[0]!;
    expect(inputs.hardware).toBeUndefined();
  });

  it("passes a blanked (null) qtyPerUnit through as NaN, never a silent 0", () => {
    quote.parts[0]!.hardware = [{ name: "magnet", qtyPerUnit: null }];
    const cfg = hardwareCfg([{ name: "magnet", unitCost: 1.5, lostOnFailure: false }]);
    const inputs = toOrderInputs(cfg).parts[0]!;
    expect(inputs.hardware).toHaveLength(1);
    expect(Number.isNaN(inputs.hardware![0]!.qtyPerUnit)).toBe(true);
  });
});

// salePriceOverride resolves on PRESENCE, not truthiness (docs/formulas.md): an explicit 0 is a giveaway.
// This pins the form mapping, so changing toPartInputs' `!== null` check to `||` fails here with Go green.
describe("toOrderInputs: salePriceOverride presence vs. truthiness", () => {
  function pricingCfg(): EffectiveConfig {
    return {
      ...config(),
      energy: { mode: "flat", flatRate: 0.15, tou: { peakRate: 0.3, offPeakRate: 0.1, defaultOffPeakFrac: 0.7 } },
      feeSchedules: {},
    } as EffectiveConfig;
  }

  beforeEach(() => {
    quote.parts[0]!.hardware = [];
    quote.order.sellingEnabled = true;
    quote.order.feeScheduleName = "none";
    quote.parts[0]!.salePriceOverride = null;
  });

  it("keeps an explicit 0 as a real 0 -- Object.hasOwn, not truthiness, is what must hold", () => {
    quote.parts[0]!.salePriceOverride = 0;
    const inputs = toOrderInputs(pricingCfg());
    expect(Object.hasOwn(inputs.parts[0]!, "salePriceOverride")).toBe(true);
    expect(inputs.parts[0]!.salePriceOverride).toBe(0);
  });

  it("resolves a blank (null) override to no override at all", () => {
    quote.parts[0]!.salePriceOverride = null;
    const inputs = toOrderInputs(pricingCfg());
    expect(inputs.parts[0]!.salePriceOverride).toBeUndefined();
  });
});

// pruneMissingHardware is HistoryPanel's restore() cleanup for deleted catalog entries, and the primary
// path: it prunes quote.parts[0]!.hardware itself, unlike toQuoteInputs, which only drops the row from
// the compute bag (defense in depth).
describe("pruneMissingHardware", () => {
  it("removes a row whose catalog entry was deleted, leaving it out of `kept`", () => {
    const hardware = [{ name: "magnet", qtyPerUnit: 3 }];
    const catalog = [{ name: "insert" }]; // "magnet" no longer exists
    const { kept, missing } = pruneMissingHardware(hardware, catalog);
    expect(kept).toEqual([]);
    expect(missing).toEqual([{ name: "magnet", qtyPerUnit: 3 }]);
  });

  it("keeps every row that still matches a catalog entry untouched", () => {
    const hardware = [{ name: "magnet", qtyPerUnit: 3 }];
    const catalog = [{ name: "magnet" }];
    const { kept, missing } = pruneMissingHardware(hardware, catalog);
    expect(kept).toEqual(hardware);
    expect(missing).toEqual([]);
  });

  it("prunes only the deleted rows out of a mixed set, preserving the rest", () => {
    const hardware = [
      { name: "magnet", qtyPerUnit: 3 },
      { name: "deleted-insert", qtyPerUnit: 1 },
      { name: "clip", qtyPerUnit: 2 },
    ];
    const catalog = [{ name: "magnet" }, { name: "clip" }];
    const { kept, missing } = pruneMissingHardware(hardware, catalog);
    expect(kept).toEqual([
      { name: "magnet", qtyPerUnit: 3 },
      { name: "clip", qtyPerUnit: 2 },
    ]);
    expect(missing).toEqual([{ name: "deleted-insert", qtyPerUnit: 1 }]);
  });

  it("is a no-op (kept === the original array) when nothing is missing", () => {
    const hardware = [{ name: "magnet", qtyPerUnit: 3 }];
    const { kept, missing } = pruneMissingHardware(hardware, [{ name: "magnet" }]);
    // Identity, not just equality: a no-op prune must not reassign a new array on every restore().
    expect(kept).toBe(hardware);
    expect(missing).toEqual([]);
  });
});

describe("effectiveConfig", () => {
  beforeEach(() => {
    demoLocal.currency = null;
  });

  it("passes null through", () => {
    expect(effectiveConfig(null)).toBeNull();
  });

  it("passes a non-demo config through with the same object identity", () => {
    const cfg = config({ demoMode: false });
    demoLocal.currency = "eur"; // even with a preset chosen, non-demo is untouched
    expect(effectiveConfig(cfg)).toBe(cfg);
  });

  it("passes a demo config through unchanged when no preset is chosen", () => {
    const cfg = config({ demoMode: true });
    expect(effectiveConfig(cfg)).toBe(cfg);
  });

  it("overrides currency (symbol + decimals only) when demoMode is true and a preset is chosen", () => {
    const cfg = config({ demoMode: true, currency: { symbol: "$", decimals: 2, code: "" } });
    demoLocal.currency = "jpy";
    const result = effectiveConfig(cfg);
    expect(result).not.toBe(cfg);
    expect(result?.currency).toEqual({ symbol: "¥", decimals: 0, code: "" });
    expect(result?.materials).toBe(cfg.materials);
  });
});

function resetQuote() {
  const first = { ...defaultPartState("Part 1"), id: "p1" };
  quote.parts = [first];
  quote.activePartId = "p1";
  Object.assign(quote.order, defaultOrderForm());
}

describe("formState migration", () => {
  it("rejects a formState that is not version 1", () => {
    expect(parseFormState({ weightG: 50, quantity: 3, sellingEnabled: true, marginPct: 40 })).toBeNull();
    expect(parseFormState({})).toBeNull();
    expect(parseFormState({ version: 99, parts: [{}] })).toBeNull();
  });

  it("ignores keys outside the allowlist (no __proto__ injection)", () => {
    const m = parseFormState(JSON.parse('{"version": 1, "parts": [{"weightG": 1, "__proto__": {"polluted": true}, "evil": 1}], "order": {}}'))!;
    expect((m.parts[0] as unknown as Record<string, unknown>).evil).toBeUndefined();
    expect(({} as Record<string, unknown>).polluted).toBeUndefined();
  });

  it("fills a part missing hardware with an empty list", () => {
    expect(parseFormState({ version: 1, parts: [{ weightG: 1 }], order: {} })!.parts[0]!.hardware).toEqual([]);
  });

  it("reads a stored form state as is, filling missing keys from defaults", () => {
    const m = parseFormState({ version: 1, parts: [{ label: "A", weightG: 5 }, { label: "B", weightG: 6, quantity: 2 }], order: { marginPct: 10 } })!;
    expect(m.parts.map((p) => p.label)).toEqual(["A", "B"]);
    expect(m.parts[1]!.quantity).toBe(2);
    expect(m.parts[0]!.quantity).toBe(1);
    expect(m.order.marginPct).toBe(10);
    expect(m.order.strategy).toBe("margin");
  });

  it("rejects unusable values", () => {
    expect(parseFormState(null)).toBeNull();
    expect(parseFormState("x")).toBeNull();
    expect(parseFormState([])).toBeNull();
    expect(parseFormState({ version: 1, parts: [] })).toBeNull();
    expect(parseFormState({ version: 1, parts: [3] })).toBeNull();
  });

  const seedCfg = {
    defaults: { printer: "pr1", material: "m1", quantity: 2, failureRatePct: 4, preset: "none" },
    printers: [{ id: "pr1", name: "P", watts: 111, purchaseCost: 1, lifespanHours: 2, filamentDiameterMm: 1.75 }],
    materials: [{ id: "m1", name: "M", pricePerKg: 33, wastePct: 3, density: 1.2 }],
    presets: {},
    energy: { flatRate: 0.2, tou: { defaultOffPeakFrac: 0.5 } },
    pricing: { strategy: "markup", marginPct: 11, markupMultiplier: 3, taxRatePct: 5, defaultShippingCharge: 7, defaultShippingCost: 2, defaultFeeSchedule: "none" },
  } as unknown as EffectiveConfig;

  it("seeds the base from config defaults so partial or empty rows do not restore hard-coded constants", () => {
    const empty = parseFormState({ version: 1, parts: [{}] }, seedCfg)!;
    expect(empty.parts[0]).toMatchObject({ printerId: "pr1", materialId: "m1", watts: 111, pricePerKg: 33, quantity: 2 });
    expect(empty.order).toMatchObject({ strategy: "markup", marginPct: 11, taxRatePct: 5, shippingCharge: 7, shippingCost: 2 });
    const v2 = parseFormState({ version: 1, parts: [{ weightG: 5 }], order: { marginPct: 40 } }, seedCfg)!;
    expect(v2.parts[0]).toMatchObject({ printerId: "pr1", weightG: 5 });
    expect(v2.order).toMatchObject({ marginPct: 40, strategy: "markup" });
  });

  it("stored keys still win over the seeded base", () => {
    const m = parseFormState({ version: 1, parts: [{ printerId: "other", watts: 9 }], order: {} }, seedCfg)!;
    expect(m.parts[0]).toMatchObject({ printerId: "other", watts: 9, materialId: "m1" });
  });

  it("does not alias the input's nested hardware or spool objects", () => {
    const input = {
      version: 1,
      parts: [{ hardware: [{ name: "magnet", qtyPerUnit: 1 }], spoolOverride: { label: "s", pricePerKg: 1, density: 1, remainingG: 2 } }],
      order: {},
    };
    const m = parseFormState(input)!;
    expect(m.parts[0]!.hardware[0]).not.toBe(input.parts[0]!.hardware[0]);
    expect(m.parts[0]!.spoolOverride).not.toBe(input.parts[0]!.spoolOverride);
    m.parts[0]!.hardware[0]!.qtyPerUnit = 99;
    expect(input.parts[0]!.hardware[0]!.qtyPerUnit).toBe(1);
  });
});

describe("quote state: restore, parts, snapshot", () => {
  beforeEach(resetQuote);

  const cfg = {
    printers: [{ id: "pr1", name: "P", watts: 100, purchaseCost: 1, lifespanHours: 2, filamentDiameterMm: 1.75 }],
    materials: [{ id: "m1", name: "M", pricePerKg: 25, wastePct: 3, density: 1.2 }],
    feeSchedules: { etsy: {} },
    hardware: [{ name: "magnet", unitCost: 1, lostOnFailure: false }],
    presets: {},
  } as unknown as EffectiveConfig;

  it("restores a formState with fresh ids and the first part active", () => {
    const fs = { version: 1, parts: [{ label: "A", weightG: 5 }, { label: "B", weightG: 6 }], order: { sellingEnabled: true } };
    expect(restoreFormState(fs)).toBe(true);
    expect(quote.parts.map((p) => p.label)).toEqual(["A", "B"]);
    expect(quote.parts[0]!.id).not.toBe(quote.parts[1]!.id);
    expect(quote.activePartId).toBe(quote.parts[0]!.id);
    expect(quote.order.sellingEnabled).toBe(true);
  });

  it("leaves the editor untouched when the stored value is unusable", () => {
    quote.parts[0]!.weightG = 9;
    expect(restoreFormState({ version: 1, parts: [] })).toBe(false);
    expect(quote.parts[0]!.weightG).toBe(9);
  });

  it("toFormState drops ids and round-trips through restore", () => {
    restoreFormState({ version: 1, parts: [{ label: "A", weightG: 5 }, { label: "B", weightG: 6 }], order: {} });
    const fs = toFormState();
    expect(fs.version).toBe(1);
    expect(fs.parts.every((p) => !("id" in p))).toBe(true);
    const before = JSON.stringify(fs);
    restoreFormState(fs);
    expect(JSON.stringify(toFormState())).toBe(before);
  });

  it("addPart copies printer/material/rates from the active part but not weight, time, or quantity", () => {
    const a = quote.parts[0]!;
    a.printerId = "pr1";
    a.materialId = "m1";
    a.weightG = 50;
    a.quantity = 4;
    a.laborRate = 99;
    const b = addPart(cfg);
    expect(quote.parts).toHaveLength(2);
    expect(b.label).toBe("Part 2");
    expect(b).toMatchObject({ printerId: "pr1", materialId: "m1", laborRate: 99, weightG: null, quantity: 1 });
    expect(b.id).not.toBe(a.id);
  });

  it("addPart resets a spool-priced active part's price to the material price", () => {
    const a = quote.parts[0]!;
    a.materialId = "m1";
    a.pricePerKg = 80;
    a.spoolOverride = { label: "s", pricePerKg: 80, density: 1.1, remainingG: 10, spoolId: 1 };
    const b = addPart(cfg);
    expect(b.pricePerKg).toBe(25);
    expect(b.spoolOverride).toBeNull();
  });

  it("addPart stops at 20 parts", () => {
    for (let i = 0; i < 25; i++) addPart(cfg);
    expect(quote.parts).toHaveLength(20);
  });

  it("duplicatePart inserts a copy after the original", () => {
    quote.parts[0]!.weightG = 12;
    const copy = duplicatePart("p1")!;
    expect(quote.parts.map((p) => p.id)).toEqual(["p1", copy.id]);
    expect(copy.weightG).toBe(12);
    expect(copy.label).toBe("Part 1 (copy)");
  });

  it("removePart keeps at least one part and re-points the active part", () => {
    addPart(cfg);
    const second = quote.parts[1]!.id;
    setActivePart(second);
    removePart(second);
    expect(quote.parts).toHaveLength(1);
    expect(quote.activePartId).toBe("p1");
    removePart("p1");
    expect(quote.parts).toHaveLength(1);
  });

  it("nextPartLabel fills the first unused Part N", () => {
    expect(nextPartLabel([{ label: "Part 1" }, { label: "Part 3" }])).toBe("Part 2");
  });

  it("startOnePartQuote replaces all parts with one fresh part and resets the order block", () => {
    addPart(cfg);
    quote.order.shippingCharge = 7;
    const seedCfg = {
      ...cfg,
      defaults: { printer: "pr1", material: "m1", quantity: 2, failureRatePct: 4, preset: "none" },
      energy: { flatRate: 0.2, tou: { defaultOffPeakFrac: 0.5 } },
      pricing: { strategy: "margin", marginPct: 30, markupMultiplier: 2.5, taxRatePct: 0, defaultShippingCharge: 0, defaultShippingCost: 0, defaultFeeSchedule: "none" },
    } as unknown as EffectiveConfig;
    const part = startOnePartQuote(seedCfg);
    expect(quote.parts).toHaveLength(1);
    expect(quote.activePartId).toBe(part.id);
    expect(part).toMatchObject({ printerId: "pr1", materialId: "m1", quantity: 2, weightG: null });
    expect(quote.order.shippingCharge).toBe(0);
    expect(quote.order.sellingEnabled).toBe(false);
  });

  it("reconcileWithConfig falls back per part and prefixes notes when there are several parts", () => {
    restoreFormState({
      version: 1,
      parts: [
        { label: "A", printerId: "gone", materialId: "m1" },
        { label: "B", printerId: "pr1", materialId: "m1", hardware: [{ name: "deleted", qtyPerUnit: 1 }] },
      ],
      order: { feeScheduleName: "missing" },
    });
    const notes = reconcileWithConfig(cfg);
    expect(notes).toEqual([
      'A: Printer "gone" no longer exists, so the first printer was selected instead.',
      'B: Hardware "deleted" no longer exists, so it was removed from this quote.',
      'Fee schedule "missing" no longer exists, so "None" was selected instead.',
    ]);
    expect(quote.parts[0]!.printerId).toBe("pr1");
    expect(quote.parts[1]!.hardware).toEqual([]);
    expect(quote.order.feeScheduleName).toBe("none");
  });
});

describe("toOrderInputs: order shape", () => {
  beforeEach(resetQuote);
  const cfg = {
    currency: { symbol: "$", decimals: 2, code: "" },
    energy: { mode: "flat", flatRate: 0.15, tou: { peakRate: 0.3, offPeakRate: 0.1, defaultOffPeakFrac: 0.7 } },
    feeSchedules: { etsy: { fixed: [], percent: [] } },
    hardware: [],
  } as unknown as EffectiveConfig;

  it("sends one entry per part with labels, and no pricing block while selling is off", () => {
    restoreFormState({ version: 1, parts: [{ label: "A" }, { label: "  " }, { label: "C" }], order: {} });
    const o = toOrderInputs(cfg);
    expect(o.parts.map((p) => p.label)).toEqual(["A", "Part 2", "C"]);
    expect(o.pricing).toBeUndefined();
    expect(o.parts.every((p) => !("pricing" in p))).toBe(true);
  });

  it("puts fees, tax, and shipping on the order and the override on the part", () => {
    restoreFormState({
      version: 1,
      parts: [{ label: "A", salePriceOverride: 0 }, { label: "B" }],
      order: { sellingEnabled: true, taxRatePct: 7, shippingCharge: 5, feeScheduleName: "etsy" },
    });
    const o = toOrderInputs(cfg);
    expect(o.pricing).toMatchObject({ taxRatePct: 7, shippingCharge: 5, feeScheduleName: "etsy" });
    expect(o.pricing!.feeSchedule).toBeDefined();
    expect(o.parts[0]!.salePriceOverride).toBe(0);
    expect("salePriceOverride" in o.parts[1]!).toBe(false);
  });

  it("omits overrides when selling is off", () => {
    restoreFormState({ version: 1, parts: [{ label: "A", salePriceOverride: 3 }], order: { sellingEnabled: false } });
    expect(toOrderInputs(cfg).parts[0]!.salePriceOverride).toBeUndefined();
  });
});
