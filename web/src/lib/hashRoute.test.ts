import { describe, expect, it, vi } from "vitest";
import { splitHash, syncDetail, invoiceHash, routeBase, settingsSectionFromHash } from "./hashRoute";

describe("splitHash", () => {
  it("returns the whole hash as base with no fragment when there's no second #", () => {
    expect(splitHash("#/invoices")).toEqual({ base: "#/invoices", fragment: null });
  });

  it("splits a detail-id fragment off a tab hash", () => {
    expect(splitHash("#/invoices#7a1c")).toEqual({ base: "#/invoices", fragment: "7a1c" });
  });

  it("splits a guide anchor fragment", () => {
    expect(splitHash("#/guide#margin-vs-markup")).toEqual({ base: "#/guide", fragment: "margin-vs-markup" });
  });

  it("treats a bare '#' as base with no fragment (no second # to find)", () => {
    expect(splitHash("#")).toEqual({ base: "#", fragment: null });
  });

  it("treats an empty hash as base with no fragment", () => {
    expect(splitHash("")).toEqual({ base: "", fragment: null });
  });
});

describe("syncDetail", () => {
  const INVOICES = "#/invoices";

  it("ignores a hashchange for a different tab", () => {
    let opened: string | null = null;
    let closed = false;
    syncDetail("#/clients#1", INVOICES, null, false, (id) => (opened = id), () => (closed = true));
    expect(opened).toBeNull();
    expect(closed).toBe(false);
  });

  it("opens the id named by a matching hash", () => {
    let opened: string | null = null;
    syncDetail(`${INVOICES}#7a1c`, INVOICES, null, false, (id) => (opened = id), () => {});
    expect(opened).toBe("7a1c");
  });

  it("does not re-open when the fragment already matches currentId", () => {
    let opened: string | null = null;
    syncDetail(`${INVOICES}#7a1c`, INVOICES, "7a1c", true, (id) => (opened = id), () => {});
    expect(opened).toBeNull();
  });

  it("closes the detail view when the hash goes bare while a record is open", () => {
    let closed = false;
    syncDetail(INVOICES, INVOICES, "7a1c", true, () => {}, () => (closed = true));
    expect(closed).toBe(true);
  });

  it("closes the detail view when the hash goes bare while composing a new (idless) record", () => {
    let closed = false;
    syncDetail(INVOICES, INVOICES, null, true, () => {}, () => (closed = true));
    expect(closed).toBe(true);
  });

  it("does nothing when the hash goes bare and no detail view is open", () => {
    let closed = false;
    syncDetail(INVOICES, INVOICES, null, false, () => {}, () => (closed = true));
    expect(closed).toBe(false);
  });
});

describe("invoice routes", () => {
  it("reads #/invoices/<id> and the older #/invoices#<id> alike", () => {
    expect(splitHash("#/invoices/7a1c")).toEqual({ base: "#/invoices", fragment: "7a1c" });
    expect(splitHash("#/invoices#7a1c")).toEqual({ base: "#/invoices", fragment: "7a1c" });
    expect(splitHash("#/invoices")).toEqual({ base: "#/invoices", fragment: null });
  });
  it("builds the new address", () => {
    expect(invoiceHash("7a1c")).toBe("#/invoices/7a1c");
  });
  it("syncDetail opens and closes with the new form", () => {
    const open = vi.fn();
    const close = vi.fn();
    syncDetail("#/invoices/9", "#/invoices", null, false, open, close);
    expect(open).toHaveBeenCalledWith("9");
    syncDetail("#/invoices", "#/invoices", "9", true, open, close);
    expect(close).toHaveBeenCalled();
  });
});

describe("routeBase and settingsSectionFromHash (A16)", () => {
  it("maps #/new to the New quote route", () => {
    expect(routeBase("#/new")).toBe("#/quote");
  });
  it("maps #/settings/<section> to Settings", () => {
    expect(routeBase("#/settings/storefront")).toBe("#/settings");
    expect(settingsSectionFromHash("#/settings/Storefront")).toBe("storefront");
  });
  it("leaves other routes alone", () => {
    expect(routeBase("#/quotes")).toBe("#/quotes");
    expect(routeBase("#/quote/abc")).toBe("#/quote");
    expect(settingsSectionFromHash("#/settings")).toBeNull();
  });
});
