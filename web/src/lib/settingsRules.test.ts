import { describe, expect, it } from "vitest";
import { hasErrors } from "./engine/index";
import type { ServerConfig } from "./settings";
import { settingsWarnings, CUSTOM_CSS_MAX_BYTES } from "./settingsRules";

// Mirrors settings.test.ts's baseConfig: satisfies every rule, so each test breaks only one field.
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
    materials: [
      { id: "pla", name: "PLA", density: 1.24, pricePerKg: 20, wastePct: 5, public: false },
      { id: "petg", name: "PETG", density: 1.27, pricePerKg: 25, wastePct: 5, public: false },
    ],
    printers: [
      { id: "p1", name: "Printer", watts: 250, purchaseCost: 400, lifespanHours: 4000, filamentDiameterMm: 1.75 },
    ],
    hardware: [{ name: "magnet", unitCost: 0.15, lostOnFailure: true }],
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
    users: { multiUser: false, accessAdminEmail: "" },
  };
}

function warningsFor(config: ServerConfig, f: string) {
  return settingsWarnings(config).filter((w) => w.field === f);
}

function setPath(obj: unknown, path: string, value: unknown): void {
  const keys = path.split(".");
  let o = obj as Record<string, unknown>;
  for (const k of keys.slice(0, -1)) o = o[k] as Record<string, unknown>;
  o[keys[keys.length - 1]!] = value;
}

/** Each value, set on a fresh base config, leaves `field` with no warnings. */
function expectValid(field: string, values: unknown[]): void {
  for (const v of values) {
    const c = baseConfig();
    setPath(c, field, v);
    expect(warningsFor(c, field)).toEqual([]);
  }
}

/** Each value, set on a fresh base config, gives `field` at least one error. */
function expectInvalid(field: string, values: unknown[]): void {
  for (const v of values) {
    const c = baseConfig();
    setPath(c, field, v);
    expect(hasErrors(warningsFor(c, field))).toBe(true);
  }
}

describe("settingsWarnings: base config", () => {
  it("has no warnings", () => {
    expect(settingsWarnings(baseConfig())).toEqual([]);
  });
});

describe("currency.decimals", () => {
  it("passes at the bounds", () => {
    expectValid("currency.decimals", [0, 4]);
  });
  it("fails below min, above max, and non-integer", () => {
    expectInvalid("currency.decimals", [-1, 5, 2.5]);
  });
});

describe("pricing.marginPct", () => {
  it("passes at 0 and just under 100", () => {
    expectValid("pricing.marginPct", [0, 99.9]);
  });
  it("fails below 0", () => {
    expectInvalid("pricing.marginPct", [-0.1]);
  });
  // 99.9 is the ceiling shared with server/config.go and server/pricing.go (maxMarginPct); above it the form
  // must fail inline, naming the bound, not save and make every quote unpriceable.
  it("fails just above the shared 99.9 ceiling", () => {
    const c = baseConfig();
    c.pricing.marginPct = 99.95;
    const warnings = warningsFor(c, "pricing.marginPct");
    expect(hasErrors(warnings)).toBe(true);
    expect(warnings.some((w) => w.code === "above-max" && w.message.includes("99.9"))).toBe(true);
  });
  it("fails at exactly 100", () => {
    const c = baseConfig();
    c.pricing.marginPct = 100;
    const warnings = warningsFor(c, "pricing.marginPct");
    expect(hasErrors(warnings)).toBe(true);
    expect(warnings.some((w) => w.code === "above-max" && w.message.includes("99.9"))).toBe(true);
  });
  it("reports exactly one warning above the ceiling, not a duplicate pair", () => {
    const c = baseConfig();
    c.pricing.marginPct = 150;
    expect(warningsFor(c, "pricing.marginPct")).toHaveLength(1);
  });
});

describe("pricing.markupMultiplier", () => {
  it("passes at 0", () => {
    expectValid("pricing.markupMultiplier", [0]);
  });
  it("fails below 0", () => {
    expectInvalid("pricing.markupMultiplier", [-1]);
  });
  // 10000 is the ceiling shared with server/config.go and server/pricing.go (maxMarkupMultiplier); 1e6 once
  // priced a $14.51 part at $14.5 M. The bound is inclusive, so the boundary is the interesting case.
  it("passes at the shared 10000 ceiling", () => {
    expectValid("pricing.markupMultiplier", [10000]);
  });
  it("fails just above the shared 10000 ceiling, naming the bound", () => {
    const c = baseConfig();
    c.pricing.markupMultiplier = 10000.1;
    const warnings = warningsFor(c, "pricing.markupMultiplier");
    expect(hasErrors(warnings)).toBe(true);
    expect(warnings.some((w) => w.code === "above-max" && w.message.includes("10000"))).toBe(true);
  });
  it("fails at magnitudes that would wrap revenue", () => {
    for (const mult of [1e6, 1e15, 1e17]) {
      const c = baseConfig();
      c.pricing.markupMultiplier = mult;
      expect(hasErrors(warningsFor(c, "pricing.markupMultiplier"))).toBe(true);
    }
  });
});

// A negative pricing.taxRatePct would shave the price, since tax is price-inclusive (p x r / (100 + r)).
// The bounds mirror server/config.go and server/pricing.go.
describe("pricing.taxRatePct", () => {
  it("passes across the accepted range", () => {
    for (const rate of [0, 20, 100]) {
      const c = baseConfig();
      c.pricing.taxRatePct = rate;
      expect(warningsFor(c, "pricing.taxRatePct")).toEqual([]);
    }
  });
  it("fails below 0, naming the bound", () => {
    const c = baseConfig();
    c.pricing.taxRatePct = -5;
    const warnings = warningsFor(c, "pricing.taxRatePct");
    expect(hasErrors(warnings)).toBe(true);
    expect(warnings.some((w) => w.code === "below-min" && w.message.includes("0"))).toBe(true);
  });
  it("fails above 100", () => {
    expectInvalid("pricing.taxRatePct", [100.1]);
  });
});

// failureRatePct has one ceiling, 90, shared with server/config.go and server/pricing.go: the failure buffer is
// base x p / (1 - p), which is unbounded as p approaches 1.
describe("failureRatePct ceiling", () => {
  it("passes across the accepted range", () => {
    expectValid("defaults.failureRatePct", [0, 5, 90]);
    expectValid("presets.side-hustle.failureRatePct", [0, 90]);
  });
  it("fails above 90 for the default and for a preset", () => {
    expectInvalid("defaults.failureRatePct", [90.1, 100]);
    expectInvalid("presets.side-hustle.failureRatePct", [90.1, 100]);
  });
});

describe("defaults.quantity", () => {
  it("passes at 1", () => {
    expectValid("defaults.quantity", [1]);
  });
  it("fails below 1 and on a non-integer", () => {
    expectInvalid("defaults.quantity", [0, 1.5]);
  });
});

describe("invoicing.numberPrefix", () => {
  it("passes for 1-10 alphanumeric characters", () => {
    expectValid("invoicing.numberPrefix", ["A", "INV2026ABC" /* exactly 10 characters */]);
  });
  it("fails when empty, too long, or containing non-alphanumeric characters", () => {
    expectInvalid("invoicing.numberPrefix", ["", "12345678901" /* 11 chars, over the max */, "INV-2026"]);
  });
});

describe("invoicing.salesTaxPct", () => {
  it("passes at the bounds", () => {
    expectValid("invoicing.salesTaxPct", [0, 30, 100]);
  });
  it("fails outside the bounds", () => {
    expectInvalid("invoicing.salesTaxPct", [-1, 100.1]);
  });
});

describe("invoicing.dueDays", () => {
  it("passes at the bounds", () => {
    expectValid("invoicing.dueDays", [0, 365, 10000]);
  });
  it("fails outside the bounds and on a non-integer", () => {
    expectInvalid("invoicing.dueDays", [-1, 10001, 30.5]);
  });
});

describe("invoicing.paymentMethods", () => {
  it("passes with 1-20 nonempty entries under 50 characters", () => {
    const c = baseConfig();
    c.invoicing.paymentMethods = ["cash"];
    expect(settingsWarnings(c).filter((w) => w.field.startsWith("invoicing.paymentMethods"))).toEqual([]);
  });
  it("fails with 0 entries and with 21 entries", () => {
    const c = baseConfig();
    c.invoicing.paymentMethods = [];
    expect(hasErrors(warningsFor(c, "invoicing.paymentMethods"))).toBe(true);
    expect(warningsFor(c, "invoicing.paymentMethods").some((w) => w.message === "must have 1-20 entries (found 0)")).toBe(true);
    c.invoicing.paymentMethods = Array.from({ length: 21 }, (_, i) => `method-${i}`);
    expect(hasErrors(warningsFor(c, "invoicing.paymentMethods"))).toBe(true);
    expect(warningsFor(c, "invoicing.paymentMethods").some((w) => w.message === "must have 1-20 entries (found 21)")).toBe(true);
  });
  it("fails a blank entry (after trimming) and an over-length entry", () => {
    const c = baseConfig();
    c.invoicing.paymentMethods = ["   ", "x".repeat(51)];
    expect(hasErrors(warningsFor(c, "invoicing.paymentMethods[0]"))).toBe(true);
    expect(hasErrors(warningsFor(c, "invoicing.paymentMethods[1]"))).toBe(true);
  });
});

describe("customCss", () => {
  // Mirrors server/config.go's customCssMaxBytes for the top-level customCss field.
  it("passes at the byte cap", () => {
    expectValid("customCss", ["x".repeat(CUSTOM_CSS_MAX_BYTES)]);
  });
  it("fails one byte over the cap", () => {
    expectInvalid("customCss", ["x".repeat(CUSTOM_CSS_MAX_BYTES + 1)]);
  });
});

describe("timezone", () => {
  it("passes for IANA names and an empty value (the server treats it as UTC)", () => {
    expectValid("timezone", ["UTC", "America/Los_Angeles", "Europe/Berlin", ""]);
  });
  it("fails for unknown names and Local", () => {
    expectInvalid("timezone", ["Mars/Phobos", "not a zone", "Local", "america/new_york", "utc"]);
  });
});

describe("pricing.defaultShippingCharge", () => {
  it("passes at 0 and above, fails below 0", () => {
    expectValid("pricing.defaultShippingCharge", [0, 4.5]);
    expectInvalid("pricing.defaultShippingCharge", [-1]);
  });
});

describe("materials[i].id", () => {
  it("passes with unique, nonempty ids", () => {
    expect(warningsFor(baseConfig(), "materials[0].id")).toEqual([]);
  });
  it("fails on an empty id", () => {
    const c = baseConfig();
    c.materials[0]!.id = "";
    expect(hasErrors(warningsFor(c, "materials[0].id"))).toBe(true);
  });
  it("flags every row sharing a duplicate id, including non-adjacent positions", () => {
    const c = baseConfig();
    c.materials = [
      { id: "pla", name: "PLA", density: 1.24, pricePerKg: 20, wastePct: 5, public: false },
      { id: "petg", name: "PETG", density: 1.27, pricePerKg: 25, wastePct: 5, public: false },
      { id: "pla", name: "PLA again", density: 1.24, pricePerKg: 20, wastePct: 5, public: false },
    ];
    expect(hasErrors(warningsFor(c, "materials[0].id"))).toBe(true);
    expect(hasErrors(warningsFor(c, "materials[1].id"))).toBe(false);
    expect(hasErrors(warningsFor(c, "materials[2].id"))).toBe(true);
    expect(warningsFor(c, "materials[0].id").some((w) => w.message === 'must be unique ("pla" is used twice)')).toBe(true);
  });
});

describe("materials[i].density", () => {
  it("passes for a positive value", () => {
    expect(warningsFor(baseConfig(), "materials[0].density")).toEqual([]);
  });
  it("fails at exactly 0 (inclusive min: 0 can't express this bound) and below", () => {
    const c = baseConfig();
    c.materials[0]!.density = 0;
    let warnings = warningsFor(c, "materials[0].density");
    expect(hasErrors(warnings)).toBe(true);
    expect(warnings.some((w) => w.message === "must be greater than 0")).toBe(true);
    c.materials[0]!.density = -1;
    expect(hasErrors(warningsFor(c, "materials[0].density"))).toBe(true);
  });
  it("treats a null density (emptied number input) as required, not as 0", () => {
    const c = baseConfig();
    (c.materials[0] as unknown as { density: number | null }).density = null;
    const warnings = warningsFor(c, "materials[0].density");
    expect(hasErrors(warnings)).toBe(true);
    expect(warnings.some((w) => w.code === "required")).toBe(true);
  });
});

describe("materials[i].wastePct", () => {
  it("passes at the bounds", () => {
    const c = baseConfig();
    c.materials[0]!.wastePct = 0;
    expect(warningsFor(c, "materials[0].wastePct")).toEqual([]);
    c.materials[0]!.wastePct = 100;
    expect(warningsFor(c, "materials[0].wastePct")).toEqual([]);
  });
  it("fails outside the bounds", () => {
    const c = baseConfig();
    c.materials[0]!.wastePct = -1;
    expect(hasErrors(warningsFor(c, "materials[0].wastePct"))).toBe(true);
    c.materials[0]!.wastePct = 100.1;
    expect(hasErrors(warningsFor(c, "materials[0].wastePct"))).toBe(true);
  });
});

describe("printers[i].id", () => {
  it("passes with unique, nonempty ids", () => {
    expect(warningsFor(baseConfig(), "printers[0].id")).toEqual([]);
  });
  it("flags duplicates across positions 0 and 2", () => {
    const c = baseConfig();
    c.printers = [
      { id: "p1", name: "A", watts: 250, purchaseCost: 400, lifespanHours: 4000, filamentDiameterMm: 1.75 },
      { id: "p2", name: "B", watts: 250, purchaseCost: 400, lifespanHours: 4000, filamentDiameterMm: 1.75 },
      { id: "p1", name: "C", watts: 250, purchaseCost: 400, lifespanHours: 4000, filamentDiameterMm: 1.75 },
    ];
    expect(hasErrors(warningsFor(c, "printers[0].id"))).toBe(true);
    expect(hasErrors(warningsFor(c, "printers[1].id"))).toBe(false);
    expect(hasErrors(warningsFor(c, "printers[2].id"))).toBe(true);
  });
});

describe("printers[i].filamentDiameterMm", () => {
  it("passes for a positive value", () => {
    expect(warningsFor(baseConfig(), "printers[0].filamentDiameterMm")).toEqual([]);
  });
  it("fails at exactly 0", () => {
    const c = baseConfig();
    c.printers[0]!.filamentDiameterMm = 0;
    const warnings = warningsFor(c, "printers[0].filamentDiameterMm");
    expect(hasErrors(warnings)).toBe(true);
    expect(warnings.some((w) => w.message === "must be greater than 0")).toBe(true);
  });
});

describe("hardware[i].name", () => {
  it("passes with a unique, nonempty name", () => {
    expect(warningsFor(baseConfig(), "hardware[0].name")).toEqual([]);
  });
  it("fails on an empty name", () => {
    const c = baseConfig();
    c.hardware[0]!.name = "";
    expect(hasErrors(warningsFor(c, "hardware[0].name"))).toBe(true);
  });
  it("flags every row sharing a duplicate name, including non-adjacent positions", () => {
    const c = baseConfig();
    c.hardware = [
      { name: "magnet", unitCost: 0.15, lostOnFailure: true },
      { name: "hook", unitCost: 0.08, lostOnFailure: false },
      { name: "magnet", unitCost: 0.2, lostOnFailure: true },
    ];
    expect(hasErrors(warningsFor(c, "hardware[0].name"))).toBe(true);
    expect(hasErrors(warningsFor(c, "hardware[1].name"))).toBe(false);
    expect(hasErrors(warningsFor(c, "hardware[2].name"))).toBe(true);
    expect(warningsFor(c, "hardware[0].name").some((w) => w.message === 'must be unique ("magnet" is used twice)')).toBe(true);
  });
});

describe("hardware[i].unitCost", () => {
  it("passes at 0 and above", () => {
    const c = baseConfig();
    c.hardware[0]!.unitCost = 0;
    expect(warningsFor(c, "hardware[0].unitCost")).toEqual([]);
  });
  it("fails below 0", () => {
    const c = baseConfig();
    c.hardware[0]!.unitCost = -0.01;
    expect(hasErrors(warningsFor(c, "hardware[0].unitCost"))).toBe(true);
  });
  it("treats a null unitCost (emptied number input) as required, not as 0", () => {
    const c = baseConfig();
    (c.hardware[0] as unknown as { unitCost: number | null }).unitCost = null;
    const warnings = warningsFor(c, "hardware[0].unitCost");
    expect(hasErrors(warnings)).toBe(true);
    expect(warnings.some((w) => w.code === "required")).toBe(true);
  });
});

describe("smtp gating", () => {
  it("reports nothing when host is empty, even with an out-of-range port and bad addresses", () => {
    const c = baseConfig();
    c.smtp = { host: "", port: 0, username: "", from: "not-an-email", to: "not-an-email" };
    expect(settingsWarnings(c).filter((w) => w.field.startsWith("smtp."))).toEqual([]);
  });
  it("validates port/from/to once host is set", () => {
    const c = baseConfig();
    c.smtp = { host: "smtp.example.com", port: 0, username: "", from: "not-an-email", to: "not-an-email" };
    expect(hasErrors(warningsFor(c, "smtp.port"))).toBe(true);
    expect(hasErrors(warningsFor(c, "smtp.from"))).toBe(true);
    expect(hasErrors(warningsFor(c, "smtp.to"))).toBe(true);
  });
  it("passes a valid port and addresses once host is set", () => {
    const c = baseConfig();
    c.smtp = { host: "smtp.example.com", port: 587, username: "", from: "a@example.com", to: "b@example.com" };
    expect(settingsWarnings(c).filter((w) => w.field.startsWith("smtp."))).toEqual([]);
  });
  it("fails port outside [1,65535] and on a non-integer", () => {
    const c = baseConfig();
    c.smtp = { host: "smtp.example.com", port: 65536, username: "", from: "a@example.com", to: "b@example.com" };
    expect(hasErrors(warningsFor(c, "smtp.port"))).toBe(true);
    c.smtp.port = 587.5;
    expect(hasErrors(warningsFor(c, "smtp.port"))).toBe(true);
  });
});

describe("null handling for numeric fields", () => {
  it("treats a null numeric field (emptied number input) as required, not as 0", () => {
    const c = baseConfig();
    // bind:value on <input type="number"> yields null when emptied; a required field's null must read
    // as "required", not as 0 passing a min:0 rule.
    (c.defaults as unknown as { quantity: number | null }).quantity = null;
    const warnings = warningsFor(c, "defaults.quantity");
    expect(hasErrors(warnings)).toBe(true);
    expect(warnings.some((w) => w.code === "required")).toBe(true);
  });

  it("reports only `required` for a null marginPct, never a bound warning too", () => {
    const c = baseConfig();
    (c.pricing as unknown as { marginPct: number | null }).marginPct = null;
    const warnings = warningsFor(c, "pricing.marginPct");
    expect(warnings).toHaveLength(1);
    expect(warnings[0]!.code).toBe("required");
  });
});

describe("invoicing.paypalMe and currency.code", () => {
  it("accept empty and well-formed values", () => {
    const c = baseConfig();
    expect(warningsFor(c, "invoicing.paypalMe")).toEqual([]);
    expect(warningsFor(c, "currency.code")).toEqual([]);
    c.invoicing.paypalMe = "yourshop";
    c.currency.code = "USD";
    expect(warningsFor(c, "invoicing.paypalMe")).toEqual([]);
    expect(warningsFor(c, "currency.code")).toEqual([]);
  });
  it("reject a URL, a slash, and an over-long handle", () => {
    const c = baseConfig();
    for (const bad of ["https://paypal.me/yourshop", "your/shop", "a".repeat(21), "bad handle"]) {
      c.invoicing.paypalMe = bad;
      expect(hasErrors(warningsFor(c, "invoicing.paypalMe"))).toBe(true);
    }
  });
  it("reject lowercase and wrong-length currency codes", () => {
    const c = baseConfig();
    for (const bad of ["usd", "US", "USDX", "U5D"]) {
      c.currency.code = bad;
      expect(hasErrors(warningsFor(c, "currency.code"))).toBe(true);
    }
  });
});
