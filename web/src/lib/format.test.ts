import { describe, it, expect } from "vitest";
import { moneyAmount, moneyPrecise, friendlyMessage, fieldLabel, formatDate, formatDateTime, presetDisplayName } from "./format";
import type { FieldWarning } from "./engine/index";
import type { ServerConfig } from "./settings";
import { settingsWarnings } from "./settingsRules";

// EffectiveConfig is only read for .currency here; a minimal stand-in keeps the test independent.
const cfg = { currency: { symbol: "$", decimals: 2, code: "" } } as never;

describe("moneyAmount", () => {
  it("routes dollar amounts through toCents' half-up-with-epsilon rounding", () => {
    expect(moneyAmount(0.375, cfg)).toBe("$0.38");
    expect(moneyAmount(19.99, cfg)).toBe("$19.99");
    expect(moneyAmount(1.005, cfg)).toBe("$1.01"); // raw toFixed(2) would give "1.00"
  });
});

describe("moneyPrecise", () => {
  it("rounds an exact half up despite binary representation", () => {
      // 0.25 * 0.15 is stored as 0.037499999999999998; toFixed(3) alone gives "0.037", so use the epsilon.
    expect(moneyPrecise(0.25 * 0.15, cfg, 3)).toBe("$0.038");
  });

  it("formats per-gram sub-cent prices at 3 decimals", () => {
    expect(moneyPrecise(19.99 / 1000, cfg, 3)).toBe("$0.020");
  });

  it("respects the requested precision", () => {
    expect(moneyPrecise(0.8999999999999999, cfg, 2)).toBe("$0.90");
  });

  it("never renders a negative sign for values that round to zero", () => {
    expect(moneyPrecise(-0.0004, cfg, 3)).toBe("$0.000");
    expect(moneyPrecise(-0.5, cfg, 2)).toBe("-$0.50");
  });
});

const warn = (field: string, code: FieldWarning["code"], message: string, bound?: number): FieldWarning => ({
  field,
  code,
  message,
  severity: "error",
  bound,
});

describe("friendlyMessage", () => {
  it("turns nan into an actionable instruction", () => {
    expect(friendlyMessage(warn("weightG", "nan", "weightG is not a finite number"))).toBe(
      "Enter a valid model weight.",
    );
    expect(friendlyMessage(warn("flatRate", "nan", "flatRate is not a finite number"))).toBe(
      "Enter a valid electricity rate.",
    );
  });

  it("states required fields plainly", () => {
    expect(friendlyMessage(warn("weightG", "required", "weightG is required"))).toBe(
      "Model weight is required.",
    );
  });

  it("rewrites bound violations in words, reading the bound from the structured field", () => {
    expect(friendlyMessage(warn("marginPct", "below-min", "marginPct must be ≥ 0", 0))).toBe(
      "Margin must be at least 0.",
    );
    expect(friendlyMessage(warn("marginPct", "above-max", "marginPct must be ≤ 99", 99))).toBe(
      "Margin must be at most 99.",
    );
  });

  it("falls back to the label-swapped engine message for a below-min/above-max warning with no bound (e.g. settingsRules.ts's own hand-built warnings)", () => {
    expect(friendlyMessage(warn("marginPct", "below-min", "marginPct must be ≥ 0"))).toBe(
      "Margin must be ≥ 0",
    );
  });

  it("keeps whole-number violations in domain language", () => {
    expect(friendlyMessage(warn("quantity", "not-integer", "quantity must be a whole number"))).toBe(
      "Quantity must be a whole number.",
    );
  });

  it("falls back to the label-swapped engine message for unparsable bounds", () => {
    expect(friendlyMessage(warn("feeSchedule", "below-min", "feeSchedule has a negative fee"))).toBe(
      "Fee schedule has a negative fee",
    );
  });
});

// Every `field` settingsRules.ts can emit must resolve to a FIELD_LABELS entry: fieldLabel falls back to
// the raw field string, so a real label means `fieldLabel(w.field) !== w.field`. The config is invalid
// in every validated field, so a new field with no label fails here.
function allInvalidConfig(): ServerConfig {
  return {
    currency: { symbol: "$", decimals: -1, code: "usd" },
    pricing: {
      strategy: "margin",
      marginPct: -1,
      markupMultiplier: -1,
      taxRatePct: 0,
      defaultShippingCharge: -1,
      defaultShippingCost: 0,
      defaultFeeSchedule: "none",
      laborPerUnit: false,
    },
    feeSchedules: {},
    materials: [{ id: "", name: "PLA", density: 0, pricePerKg: 20, wastePct: -1, public: false }],
    printers: [{ id: "", name: "Printer", watts: 250, purchaseCost: 400, lifespanHours: 4000, filamentDiameterMm: 0 }],
    hardware: [{ name: "", unitCost: -1, lostOnFailure: true }],
    energy: { mode: "flat", flatRate: 0.15, tou: { peakRate: 0.2, offPeakRate: 0.1, defaultOffPeakFrac: 0.5 }, schedule: { defaultStartTime: "22:00", holidays: [], seasons: [] } },
    presets: {
      "side-hustle": { failureRatePct: 5, prepMinutes: 10, laborRate: 20, monthlyFixed: 50, monthlyHours: 40 },
    },
    defaults: { preset: "side-hustle", printer: "p1", material: "pla", failureRatePct: 5, quantity: 0 },
    quote: {
      businessName: "",
      logoUrl: "",
      address: "",
      email: "",
      phone: "",
      taxId: "",
      paymentInstructions: "x".repeat(2001),
      termsText: "x".repeat(2001),
      validDays: 30,
      referencePrefix: "",
    },
    invoicing: { numberPrefix: "", salesTaxPct: -1, dueDays: -1, paymentMethods: [""], paypalMe: "bad/handle" },
    publicQuotes: { printer: "p1", preset: "side-hustle", introText: "x".repeat(501) },
    customCss: "x".repeat(32 * 1024 + 1),
    timezone: "Mars/Phobos",
    smtp: { host: "smtp.example.com", port: 0, username: "", from: "not-an-email", to: "not-an-email" },
    integrationsUrls: { spoolmanUrl: "", moonrakerUrl: "", fluiddUrl: "" },
    ui: { disabledTabs: [] },
    site: { shopOnMainPort: true, staffLink: true, adminUrl: "" },
    shop: { maxOrderQty: 100 },
    users: { multiUser: false, accessAdminEmail: "" },
  };
}

describe("FIELD_LABELS covers every settingsRules.ts field id", () => {
  it("resolves a friendly label for every warning settingsWarnings() can produce", () => {
    const warnings = settingsWarnings(allInvalidConfig());
    expect(warnings.length).toBeGreaterThan(0);
    for (const w of warnings) {
      expect(fieldLabel(w.field), `no friendly label for field "${w.field}"`).not.toBe(w.field);
    }
  });
});

describe("formatDate and formatDateTime", () => {
  it("formats a plain date in en-US without a UTC day shift", () => {
    expect(formatDate("2026-11-02")).toBe("Nov 2, 2026");
  });
  it("formats an RFC 3339 timestamp's date", () => {
    expect(formatDate("2026-11-02T12:00:00Z")).toMatch(/^Nov [12], 2026$/);
  });
  it("formats a timestamp with date and time", () => {
    expect(formatDateTime("2026-11-02T12:00:00Z")).toMatch(/^Nov [12], 2026, \d{1,2}:\d{2}\s?[AP]M$/);
  });
  it("returns unparseable input unchanged", () => {
    expect(formatDate("soon")).toBe("soon");
    expect(formatDateTime("soon")).toBe("soon");
  });
});

describe("presetDisplayName", () => {
  it("turns a kebab-case key into a sentence-case name", () => {
    expect(presetDisplayName("side-hustle")).toBe("Side hustle");
    expect(presetDisplayName("small-shop")).toBe("Small shop");
    expect(presetDisplayName("Custom")).toBe("Custom");
  });
});

describe("friendlyMessage: blank print time", () => {
  it("reads 'Enter the print time.' for either half", () => {
    for (const field of ["timeHours", "timeMinutes"]) {
      expect(friendlyMessage({ field, code: "required", message: "Enter the print time", severity: "error" })).toBe("Enter the print time.");
    }
  });
  it("keeps the generic copy for other required fields", () => {
    expect(friendlyMessage({ field: "weightG", code: "required", message: "x", severity: "error" })).toBe("Model weight is required.");
  });
});
