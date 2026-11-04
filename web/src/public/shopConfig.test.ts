import { describe, expect, it } from "vitest";
import type { PublicConfig } from "./api";
import { composeTitle, parsePublicConfig, readEmbeddedConfig, shopTitleOf } from "./shopConfig";

const base = {
  branding: { businessName: "Alpaca Prints", logoUrl: "", email: "", phone: "", address: "" },
  staffUrl: "",
  introText: "Hi",
  currency: { symbol: "$", position: "before", decimals: 2 },
  materials: [],
  filamentDiameterMm: 1.75,
  maxUploadBytes: 1,
  demoMode: false,
} as unknown as PublicConfig;

const withShop = (pageTitle: string | undefined, businessName = "Alpaca Prints"): PublicConfig =>
  ({ ...base, branding: { ...base.branding, businessName }, shop: pageTitle === undefined ? undefined : { pageTitle } }) as PublicConfig;

const docWith = (text: string | null) => ({
  getElementById: (id: string) => (id === "shop-config" && text !== null ? ({ textContent: text } as HTMLElement) : null),
});

describe("composeTitle", () => {
  it("is the shop title alone on the gallery", () => {
    expect(composeTitle("Alpaca Prints", null)).toBe("Alpaca Prints");
  });
  it("puts the page first on inner pages", () => {
    expect(composeTitle("Alpaca Prints", "Articulated Dragon")).toBe("Articulated Dragon · Alpaca Prints");
    expect(composeTitle("Alpaca Prints", "Closed")).toBe("Closed · Alpaca Prints");
  });
});

describe("shopTitleOf", () => {
  it("prefers the served title, then the business name, then Shop", () => {
    expect(shopTitleOf(withShop("Custom tab"))).toBe("Custom tab");
    expect(shopTitleOf(withShop("  "))).toBe("Alpaca Prints");
    expect(shopTitleOf(withShop(undefined))).toBe("Alpaca Prints");
    expect(shopTitleOf(withShop("", ""))).toBe("Shop");
  });
});

describe("readEmbeddedConfig", () => {
  it("parses the embedded block", () => {
    const cfg = readEmbeddedConfig(docWith(JSON.stringify(base)));
    expect(cfg?.branding.businessName).toBe("Alpaca Prints");
  });
  it("round-trips the escaped form the server writes", () => {
    const hostile = { ...base, branding: { ...base.branding, businessName: "</script><b>&" } };
    const escaped = JSON.stringify(hostile).replace(/</g, "\\u003c").replace(/>/g, "\\u003e").replace(/&/g, "\\u0026");
    expect(escaped).not.toMatch(/[<>&]/);
    expect(readEmbeddedConfig(docWith(escaped))?.branding.businessName).toBe("</script><b>&");
  });
  it("is null when the block is missing, empty, or not JSON, so the page shows a loading state", () => {
    expect(readEmbeddedConfig(docWith(null))).toBeNull();
    expect(readEmbeddedConfig(docWith(""))).toBeNull();
    expect(readEmbeddedConfig(docWith("{not json"))).toBeNull();
  });
  it("is null for JSON that is not a usable config", () => {
    expect(readEmbeddedConfig(docWith("null"))).toBeNull();
    expect(readEmbeddedConfig(docWith("[]"))).toBeNull();
    expect(readEmbeddedConfig(docWith("{}"))).toBeNull();
    expect(readEmbeddedConfig(docWith(JSON.stringify({ ...base, materials: "x" })))).toBeNull();
  });
});

describe("parsePublicConfig", () => {
  it("requires branding, materials and currency", () => {
    expect(parsePublicConfig(base)).toBe(base);
    expect(parsePublicConfig({ ...base, branding: null })).toBeNull();
    expect(parsePublicConfig({ ...base, currency: undefined })).toBeNull();
    expect(parsePublicConfig("x")).toBeNull();
  });
});
