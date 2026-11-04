import { beforeEach, describe, expect, it } from "vitest";
import type { EffectiveConfig, OrderInputs } from "./engine/index";
import {
  captureQuoteBaseline,
  pinStoredRates,
  quote,
  quoteClient,
  quoteEngineOptions,
  quoteIsDirty,
  quoteMeta,
  quoteSession,
  resetQuote,
  storedRatesDiffer,
  toOrderInputs,
  useCurrentRates,
} from "./state.svelte";
import { prefill } from "./prefill.svelte";
import { quoteIdFromHash, quoteRouteBase } from "./hashRoute";

function cfgWith(touPeak: number, laborPerUnit = false): EffectiveConfig {
  return {
    currency: { symbol: "$", decimals: 2, code: "" },
    demoMode: false,
    timezone: "UTC",
    printers: [{ id: "pr1", name: "P", watts: 120, purchaseCost: 300, lifespanHours: 3000, filamentDiameterMm: 1.75 }],
    materials: [{ id: "m1", name: "PLA", pricePerKg: 22, wastePct: 3, density: 1.24 }],
    presets: {},
    hardware: [],
    feeSchedules: {},
    defaults: { printer: "pr1", material: "m1", quantity: 1, failureRatePct: 5, preset: "none" },
    energy: {
      mode: "tou",
      flatRate: 0.15,
      tou: { peakRate: touPeak, offPeakRate: 0.1, defaultOffPeakFrac: 0.7 },
      schedule: { defaultStartTime: "22:00", holidays: [], seasons: [] },
    },
    pricing: {
      strategy: "margin",
      marginPct: 30,
      markupMultiplier: 2.5,
      taxRatePct: 0,
      defaultShippingCharge: 0,
      defaultShippingCost: 0,
      defaultFeeSchedule: "none",
      laborPerUnit,
    },
  } as unknown as EffectiveConfig;
}

/** What the server would hand back for a quote saved under `cfg`: the engine bag the editor sent. */
function storedFor(cfg: EffectiveConfig) {
  return { inputs: toOrderInputs(cfg) as OrderInputs, options: quoteEngineOptions(cfg) };
}

describe("resetQuote", () => {
  const cfg = cfgWith(0.3);

  beforeEach(() => resetQuote(cfg));

  it("reseeds parts, order, label, client, meta and the bound quote, so nothing carries over", () => {
    quote.parts[0]!.weightG = 55;
    quote.parts[0]!.timeHours = 2;
    quote.parts[0]!.quantity = 9;
    quote.order.sellingEnabled = true;
    quote.order.shippingCharge = 12;
    quoteClient.id = "c1";
    quoteClient.name = "Casey";
    quoteMeta.project = "Lamp";
    quoteMeta.reference = "Q-9";
    quoteSession.loadedId = "abc";
    quoteSession.version = "2026-01-01T00:00:00.000Z";
    quoteSession.label = "Old quote";
    quoteSession.conflict = true;
    prefill.byPart["x"] = { source: "gcode", label: "a.gcode", snapshot: { weightG: 1, timeHours: 1, timeMinutes: 1, printerId: "", materialId: "" } };

    resetQuote(cfg);

    expect(quote.parts).toHaveLength(1);
    expect(quote.parts[0]).toMatchObject({ label: "Part 1", weightG: null, timeHours: null, timeMinutes: null, quantity: 1, printerId: "pr1", materialId: "m1" });
    expect(quote.order.sellingEnabled).toBe(false);
    expect(quote.order.shippingCharge).toBe(0);
    expect(quote.activePartId).toBe(quote.parts[0]!.id);
    expect(quoteClient).toEqual({ id: "", name: "" });
    expect(quoteMeta).toEqual({ project: "", reference: "" });
    expect(quoteSession).toMatchObject({ loadedId: null, version: "", label: "", pin: null, conflict: false });
    expect(prefill.byPart).toEqual({});
  });

  it("leaves a clean editor: the blank form is the baseline", () => {
    quote.parts[0]!.weightG = 10;
    expect(quoteIsDirty()).toBe(true);
    resetQuote(cfg);
    expect(quoteIsDirty()).toBe(false);
  });
});

describe("dirty tracking", () => {
  const cfg = cfgWith(0.3);
  beforeEach(() => resetQuote(cfg));

  it("counts a label edit, a client edit and a form edit as unsaved, and a capture as clean", () => {
    expect(quoteIsDirty()).toBe(false);
    quoteSession.label = "Benchy";
    expect(quoteIsDirty()).toBe(true);
    captureQuoteBaseline();
    expect(quoteIsDirty()).toBe(false);
    quoteClient.name = "Sam";
    expect(quoteIsDirty()).toBe(true);
    captureQuoteBaseline();
    quote.parts[0]!.weightG = 3;
    expect(quoteIsDirty()).toBe(true);
  });
});

describe("pinned stored rates", () => {
  beforeEach(() => resetQuote(cfgWith(0.3)));

  it("prices a reopened quote from its stored rates after the shop's rates change", () => {
    const saved = storedFor(cfgWith(0.3));
    const changed = cfgWith(0.9);
    pinStoredRates(saved);

    const sent = toOrderInputs(changed).parts[0]!.energy;
    expect(sent).toMatchObject({ mode: "tou", peakRate: 0.3 });
    expect(storedRatesDiffer(changed)).toBe(true);
    expect(storedRatesDiffer(cfgWith(0.3))).toBe(false);
  });

  it("uses the current rates once the user picks Use current rates", () => {
    pinStoredRates(storedFor(cfgWith(0.3)));
    const changed = cfgWith(0.9);
    useCurrentRates();
    expect(toOrderInputs(changed).parts[0]!.energy).toMatchObject({ peakRate: 0.9 });
    expect(storedRatesDiffer(changed)).toBe(false);
  });

  it("lapses when the user edits an energy field, and returns if they put it back", () => {
    pinStoredRates(storedFor(cfgWith(0.3)));
    const changed = cfgWith(0.9);
    quote.parts[0]!.offPeakFrac = 0.4;
    expect(toOrderInputs(changed).parts[0]!.energy).toMatchObject({ peakRate: 0.9, offPeakFrac: 0.4 });
    expect(storedRatesDiffer(changed)).toBe(false);
    // Put back, the form equals the saved one again, so the saved rates apply again.
    quote.parts[0]!.offPeakFrac = 0.7;
    expect(toOrderInputs(changed).parts[0]!.energy).toMatchObject({ peakRate: 0.3 });
  });

  it("keeps the stored labor option too, and drops it with the pin", () => {
    pinStoredRates(storedFor(cfgWith(0.3, true)));
    expect(quoteEngineOptions(cfgWith(0.3, false)).laborPerUnit).toBe(true);
    useCurrentRates();
    expect(quoteEngineOptions(cfgWith(0.3, false)).laborPerUnit).toBe(false);
  });

  it("pins nothing when the stored bag doesn't match the parts", () => {
    pinStoredRates({ inputs: { parts: [] } as unknown as OrderInputs, options: { laborPerUnit: false } as never });
    expect(quoteSession.pin).toBeNull();
    pinStoredRates(null);
    expect(quoteSession.pin).toBeNull();
  });

  it("does not apply to an imported bag passed in explicitly", () => {
    pinStoredRates(storedFor(cfgWith(0.3)));
    const src = { parts: quote.parts.map((p) => ({ ...p })), order: { ...quote.order } };
    expect(toOrderInputs(cfgWith(0.9), src).parts[0]!.energy).toMatchObject({ peakRate: 0.9 });
  });
});

describe("editor URL", () => {
  it("reads the saved quote id from #/quote/<id> and nothing from other routes", () => {
    expect(quoteIdFromHash("#/quote/abc123")).toBe("abc123");
    expect(quoteIdFromHash("#/quote/a%20b")).toBe("a b");
    expect(quoteIdFromHash("#/quote")).toBeNull();
    expect(quoteIdFromHash("#/quotes")).toBeNull();
    expect(quoteIdFromHash("#/invoices#abc")).toBeNull();
  });

  it("maps #/quote/<id> to the tab's own hash", () => {
    expect(quoteRouteBase("#/quote/abc")).toBe("#/quote");
    expect(quoteRouteBase("#/quote")).toBe("#/quote");
    expect(quoteRouteBase("#/quotes")).toBe("#/quotes");
  });
});
