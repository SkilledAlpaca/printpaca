import { describe, expect, it } from "vitest";
import { filterNavSections, type NavSection } from "./navFilter";
import {
  canConfigureShop,
  canEditQuotes,
  canReadSettings,
  extractConfigUser,
  GRANTABLE_TAB_KEYS,
  hasTabAccess,
  isGrantableTab,
  ungrantedTabs,
  type ConfigUser,
} from "./permissions";

const admin: ConfigUser = { username: "admin", isAdmin: true, permissions: [], signedInVia: "password" };
const employee = (permissions: string[]): ConfigUser => ({ username: "alice", isAdmin: false, permissions, signedInVia: "password" });

describe("isGrantableTab", () => {
  it("is true for exactly the ten permission keys", () => {
    for (const key of GRANTABLE_TAB_KEYS) expect(isGrantableTab(key)).toBe(true);
  });

  it("is false for guide and settings, the two non-grantable App.svelte tabs", () => {
    expect(isGrantableTab("guide")).toBe(false);
    expect(isGrantableTab("settings")).toBe(false);
  });

  it("is false for an unrecognized id", () => {
    expect(isGrantableTab("bogus")).toBe(false);
  });
});

describe("hasTabAccess", () => {
  it("grants the admin identity every tab", () => {
    for (const key of GRANTABLE_TAB_KEYS) expect(hasTabAccess(key, admin)).toBe(true);
  });

  it("grants everything when there is no user info (null user means the config has not loaded)", () => {
    expect(hasTabAccess("invoices", null)).toBe(true);
    expect(hasTabAccess("invoices", undefined)).toBe(true);
  });

  it("grants guide and settings to an employee regardless of permissions", () => {
    const emp = employee([]);
    expect(hasTabAccess("guide", emp)).toBe(true);
    expect(hasTabAccess("settings", emp)).toBe(true);
  });

  it("grants a permission key only when it's in the employee's list", () => {
    const emp = employee(["quotes", "invoices"]);
    expect(hasTabAccess("quotes", emp)).toBe(true);
    expect(hasTabAccess("invoices", emp)).toBe(true);
    expect(hasTabAccess("clients", emp)).toBe(false);
  });

  it("denies every grantable tab to an employee with no permissions", () => {
    const emp = employee([]);
    for (const key of GRANTABLE_TAB_KEYS) expect(hasTabAccess(key, emp)).toBe(false);
  });
});

describe("ungrantedTabs", () => {
  it("is empty for the admin identity", () => {
    expect(ungrantedTabs(GRANTABLE_TAB_KEYS, admin)).toEqual(new Set());
  });

  it("is empty when there is no user info", () => {
    expect(ungrantedTabs(GRANTABLE_TAB_KEYS, null)).toEqual(new Set());
  });

  it("names every grantable tab the employee lacks, leaving guide/settings out entirely", () => {
    const ids = [...GRANTABLE_TAB_KEYS, "guide", "settings"] as const;
    const emp = employee(["quote", "quotes"]);
    const result = ungrantedTabs(ids, emp);
    expect(result.has("guide")).toBe(false);
    expect(result.has("settings")).toBe(false);
    expect(result.has("quote")).toBe(false);
    expect(result.has("quotes")).toBe(false);
    expect(result.has("clients")).toBe(true);
    expect(result.has("invoices")).toBe(true);
  });

  // The union App.svelte computes (ungrantedTabs merged with ui.disabledTabs) before rendering the sidebar.
  it("combines with disabledTabs to filter a nav section list", () => {
    const sections: NavSection<string>[] = [
      { label: "Sell", items: ["quote", "quotes", "invoices"] },
      { label: "Shop", items: ["filament", "printers"] },
      { label: null, items: ["settings"] },
    ];
    const emp = employee(["quote", "printers"]);
    const hidden = new Set([...ungrantedTabs(GRANTABLE_TAB_KEYS, emp), "printers" /* also config-disabled */]);
    const result = filterNavSections(sections, hidden);
    expect(result).toEqual([{ label: "Sell", items: ["quote"] }, { label: null, items: ["settings"] }]);
  });
});

// Mirrors server/main.go's GET /api/settings gate.
describe("canReadSettings", () => {
  it("grants the admin identity", () => {
    expect(canReadSettings(admin)).toBe(true);
  });

  it("grants everything when there is no user info (null user means the config has not loaded)", () => {
    expect(canReadSettings(null)).toBe(true);
    expect(canReadSettings(undefined)).toBe(true);
  });

  it("grants an employee holding any one of filament/electricity/printers", () => {
    expect(canReadSettings(employee(["filament"]))).toBe(true);
    expect(canReadSettings(employee(["electricity"]))).toBe(true);
    expect(canReadSettings(employee(["printers"]))).toBe(true);
  });

  // "invoices" and every other grantable tab is not one of the three GET /api/settings grants.
  it("denies an employee holding only invoices (or any other non-{filament,electricity,printers} grant)", () => {
    expect(canReadSettings(employee(["invoices"]))).toBe(false);
    expect(canReadSettings(employee(["quotes", "clients", "dashboard", "items", "requests", "quote"]))).toBe(false);
  });

  it("denies an employee with no permissions at all", () => {
    expect(canReadSettings(employee([]))).toBe(false);
  });
});

describe("extractConfigUser", () => {
  it("returns the config's user", () => {
    const user: ConfigUser = { username: "alice", isAdmin: false, permissions: ["quotes"], signedInVia: "password" };
    expect(extractConfigUser({ user })).toBe(user);
  });

  it("returns null while the config hasn't loaded", () => {
    expect(extractConfigUser(null)).toBeNull();
    expect(extractConfigUser(undefined)).toBeNull();
  });
});

describe("canEditQuotes", () => {
  it("needs the quote grant, not the quotes list grant", () => {
    expect(canEditQuotes(admin)).toBe(true);
    expect(canEditQuotes(null)).toBe(true);
    expect(canEditQuotes(employee(["quotes"]))).toBe(false);
    expect(canEditQuotes(employee(["quote", "quotes"]))).toBe(true);
  });
});

describe("canConfigureShop", () => {
  it("is true for an admin or an unloaded user and false for an employee", () => {
    expect(canConfigureShop(admin)).toBe(true);
    expect(canConfigureShop(undefined)).toBe(true);
    expect(canConfigureShop(employee(["quote", "filament"]))).toBe(false);
  });
});
