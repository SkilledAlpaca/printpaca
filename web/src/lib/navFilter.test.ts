import { describe, expect, it } from "vitest";
import { brandTab, fallbackIfDisabled, filterNavSections, firstNavTab, resolveHashTab, type NavSection } from "./navFilter";

type Tab = "quote" | "dashboard" | "quotes" | "requests" | "filament" | "settings" | "guide";

const SECTIONS: NavSection<Tab>[] = [
  { label: "Sell", items: ["quote", "quotes", "requests"] },
  { label: "Shop", items: ["filament"] },
  { label: null, items: ["settings"] },
];

const HASH_TABS: Partial<Record<string, Tab>> = {
  "#/quote": "quote",
  "#/quotes": "quotes",
  "#/requests": "requests",
  "#/filament": "filament",
  "#/settings": "settings",
  "#/guide": "guide",
};

describe("filterNavSections", () => {
  it("passes every section through unchanged when nothing is disabled", () => {
    expect(filterNavSections(SECTIONS, new Set())).toEqual(SECTIONS);
  });

  it("drops a disabled item from its section", () => {
    const result = filterNavSections(SECTIONS, new Set(["quotes"]));
    expect(result[0]).toEqual({ label: "Sell", items: ["quote", "requests"] });
  });

  it("drops a section's header along with it once every item in it is disabled", () => {
    const result = filterNavSections(SECTIONS, new Set(["filament"]));
    expect(result.map((s) => s.label)).toEqual(["Sell", null]);
  });

  it("drops every section that empties out, keeping the rest", () => {
    const result = filterNavSections(SECTIONS, new Set(["quote", "quotes", "requests", "filament"]));
    expect(result).toEqual([{ label: null, items: ["settings"] }]);
  });
});

describe("resolveHashTab", () => {
  it("resolves a known, enabled hash to its tab", () => {
    expect(resolveHashTab("#/filament", HASH_TABS, new Set())).toBe("filament");
  });

  it("returns null for an unrecognized hash", () => {
    expect(resolveHashTab("#/bogus", HASH_TABS, new Set())).toBeNull();
  });

  it("returns null for a hash naming a disabled tab, same as an unrecognized one", () => {
    expect(resolveHashTab("#/filament", HASH_TABS, new Set(["filament"]))).toBeNull();
  });

  it("settings and quote are never disabled in practice, but stay resolvable even if listed", () => {
    expect(resolveHashTab("#/settings", HASH_TABS, new Set())).toBe("settings");
  });
});

describe("fallbackIfDisabled", () => {
  it("returns the tab unchanged when it isn't disabled", () => {
    expect(fallbackIfDisabled("filament", new Set(["requests"]), "quote")).toBe("filament");
  });

  it("falls back once the current tab becomes disabled", () => {
    expect(fallbackIfDisabled("filament", new Set(["filament"]), "quote")).toBe("quote");
  });
});

describe("firstNavTab", () => {
  it("returns the first item of the first section", () => {
    expect(firstNavTab(SECTIONS)).toBe("quote");
  });

  it("skips an emptied-out leading section (already dropped by filterNavSections)", () => {
    const filtered = filterNavSections(SECTIONS, new Set(["quote", "quotes", "requests"]));
    expect(firstNavTab(filtered)).toBe("filament");
  });

  it("returns null once every section has emptied out", () => {
    expect(firstNavTab([])).toBeNull();
  });
});

describe("brandTab (D22)", () => {
  const WITH_DASHBOARD: NavSection<Tab>[] = [
    { label: "Sell", items: ["quote", "dashboard", "quotes", "requests"] },
    { label: "Shop", items: ["filament"] },
    { label: null, items: ["settings"] },
  ];
  it("prefers the Dashboard when the nav has it, wherever it sits", () => {
    expect(brandTab(WITH_DASHBOARD, "dashboard")).toBe("dashboard");
  });

  it("falls back to the first nav tab when the Dashboard is hidden or not granted", () => {
    const filtered = filterNavSections(WITH_DASHBOARD, new Set(["dashboard"]));
    expect(brandTab(filtered, "dashboard")).toBe("quote");
    const quotesOnly = filterNavSections(WITH_DASHBOARD, new Set(["dashboard", "quote", "requests", "filament", "settings"]));
    expect(brandTab(quotesOnly, "dashboard")).toBe("quotes");
  });

  it("returns null once every section has emptied out", () => {
    expect(brandTab([], "dashboard")).toBeNull();
  });
});
