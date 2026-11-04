import { afterEach, describe, expect, it, vi } from "vitest";
import { cardClass, cloneConfig, configsEqual, errorSection, loadSettings, nextKey, saveSettings, type ServerConfig } from "./settings";
import { DEFAULT_SHOP_CONFIG } from "./shop";

function baseConfig(): ServerConfig {
  return {
    currency: { symbol: "$", decimals: 2, code: "" },
    pricing: {
      strategy: "margin",
      marginPct: 30,
      markupMultiplier: 1.5,
      taxRatePct: 0,
      defaultShippingCharge: 0,
      defaultShippingCost: 0,
      defaultFeeSchedule: "none",
      laborPerUnit: false,
    },
    feeSchedules: {},
    materials: [{ id: "pla", name: "PLA", density: 1.24, pricePerKg: 20, wastePct: 5, public: false }],
    printers: [{ id: "p1", name: "Printer", watts: 250, purchaseCost: 400, lifespanHours: 4000, filamentDiameterMm: 1.75 }],
    hardware: [],
    energy: { mode: "flat", flatRate: 0.15, tou: { peakRate: 0.2, offPeakRate: 0.1, defaultOffPeakFrac: 0.5 }, schedule: { defaultStartTime: "22:00", holidays: [], seasons: [] } },
    presets: {
      "side-hustle": { failureRatePct: 5, prepMinutes: 10, laborRate: 20, monthlyFixed: 50, monthlyHours: 40 },
    },
    defaults: { preset: "side-hustle", printer: "p1", material: "pla", failureRatePct: 5, quantity: 1 },
    quote: { businessName: "", logoUrl: "", address: "", email: "", phone: "", taxId: "", paymentInstructions: "", termsText: "", validDays: 30, referencePrefix: "" },
    invoicing: { numberPrefix: "INV", salesTaxPct: 0, dueDays: 30, paymentMethods: ["cash", "bank transfer"], paypalMe: "" },
    publicQuotes: { printer: "p1", preset: "side-hustle", introText: "" },
    customCss: "",
    timezone: "UTC",
    smtp: { host: "", port: 587, username: "", from: "", to: "" },
    integrationsUrls: { spoolmanUrl: "", moonrakerUrl: "", fluiddUrl: "" },
    ui: { disabledTabs: [] },
    site: { shopOnMainPort: true, staffLink: true, adminUrl: "" },
    shop: structuredClone(DEFAULT_SHOP_CONFIG),
    users: { multiUser: false, accessAdminEmail: "" },
  };
}

describe("cloneConfig / configsEqual", () => {
  it("clone is deeply equal but not the same reference", () => {
    const c = baseConfig();
    const cloned = cloneConfig(c);
    expect(cloned).toEqual(c);
    expect(cloned).not.toBe(c);
    expect(cloned.materials).not.toBe(c.materials);
  });

  it("mutating the clone does not affect the original", () => {
    const c = baseConfig();
    const cloned = cloneConfig(c);
    cloned.currency.symbol = "€";
    expect(c.currency.symbol).toBe("$");
  });

  it("configsEqual: true for equal configs, false after an edit", () => {
    const a = baseConfig();
    const b = cloneConfig(a);
    expect(configsEqual(a, b)).toBe(true);
    b.currency.symbol = "€";
    expect(configsEqual(a, b)).toBe(false);
  });
});

describe("errorSection", () => {
  it.each([
    ["currency.decimals: must be 0-4", "currency"],
    ["pricing.marginPct: 150 out of range [0,100)", "pricing"],
    ["pricing.defaultFeeSchedule: \"foo\" not in feeSchedules", "pricing"],
    ["feeSchedules.direct.percent[0].base: must be \"revenue\", got \"x\"", "feeSchedules"],
    ["feeSchedules.direct: built-in entries cannot be removed", "feeSchedules"],
    ["materials: at least one material required", "materials"],
    ["materials[0]: missing or duplicate id \"\"", "materials"],
    ["materials[0] (pla): density must be > 0", "materials"],
    ["printers[0] (p1): filamentDiameterMm must be > 0", "printers"],
    ["energy.mode: must be flat or tou", "energy"],
    ["presets.side-hustle: built-in entries cannot be removed", "presets"],
    ["defaults.material: \"x\" not in materials", "defaults"],
    ["quote.validDays: must be >= 0", "quote"],
    ["invoicing.dueDays: 400 out of range [0,365]", "invoicing"],
    ["publicQuotes.printer: \"x\" not in printers", "publicQuotes"],
    ["shop.maxOrderQty: must be a whole number from 1 to 10000", "shop"],
    ["smtp.port: 70000 out of range [1,65535]", "smtp"],
    ["ui.disabledTabs[0]: \"nope\" is not a valid tab id", "ui"],
    ["users.multiUser: requires ADMIN_PASSWORD to be set -- enable authentication before turning on multi-user mode", "users"],
  ])("%s -> %s", (msg, expected) => {
    expect(errorSection(msg)).toBe(expected);
  });

  it("unknown prefixes fall back to general", () => {
    expect(errorSection("config: required")).toBe("general");
    expect(errorSection("json: unknown field \"bogus\"")).toBe("general");
  });
});

describe("nextKey", () => {
  it("returns the base when unused", () => {
    expect(nextKey([], "new-material")).toBe("new-material");
    expect(nextKey(["other"], "new-material")).toBe("new-material");
  });

  it("appends -2, -3, ... past the first collision", () => {
    expect(nextKey(["new-material"], "new-material")).toBe("new-material-2");
    expect(nextKey(["new-material", "new-material-2"], "new-material")).toBe("new-material-3");
  });

  it("skips only the exact taken slots, not a used -3 with -2 free", () => {
    expect(nextKey(["new-material", "new-material-3"], "new-material")).toBe("new-material-2");
  });
});

describe("loadSettings", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("returns the parsed payload on a 200", async () => {
    const payload = { config: baseConfig(), builtin: baseConfig() };
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({ ok: true, json: async () => payload } as unknown as Response),
    );
    expect(await loadSettings()).toEqual(payload);
  });

  it("returns null on a non-200", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: false, json: async () => ({}) } as unknown as Response));
    expect(await loadSettings()).toBeNull();
  });

  it("returns null on a network error", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("network down")));
    expect(await loadSettings()).toBeNull();
  });
});

describe("saveSettings", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("ok:true on 200", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: true, json: async () => ({}) } as unknown as Response));
    const result = await saveSettings(baseConfig());
    expect(result).toEqual({ ok: true });
  });

  it("ok:false with server-provided errors on 422", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: false,
        json: async () => ({ errors: ["pricing.marginPct: 150 out of range [0,100)"] }),
      } as unknown as Response),
    );
    const result = await saveSettings(baseConfig());
    expect(result).toEqual({ ok: false, errors: ["pricing.marginPct: 150 out of range [0,100)"] });
  });

  it("ok:false with server-provided errors on 507", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: false,
        json: async () => ({ errors: ["writing config.yaml: disk full"] }),
      } as unknown as Response),
    );
    const result = await saveSettings(baseConfig());
    expect(result).toEqual({ ok: false, errors: ["writing config.yaml: disk full"] });
  });

  it("a failed fetch maps to a generic unreachable-server error", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("network down")));
    const result = await saveSettings(baseConfig());
    expect(result).toEqual({ ok: false, errors: ["Couldn't reach the server."] });
  });

  it("a non-JSON body maps to a generic unreachable-server error", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: true,
        json: async () => {
          throw new Error("not json");
        },
      } as unknown as Response),
    );
    const result = await saveSettings(baseConfig());
    expect(result).toEqual({ ok: false, errors: ["Couldn't reach the server."] });
  });
});

describe("cardClass", () => {
  it("adds the danger marker only for an invalid card", () => {
    expect(cardClass(false)).toBe("card");
    expect(cardClass(true)).toBe("card danger-border");
  });
});
