import { describe, expect, it } from "vitest";
import { splitHash, syncDetail } from "./hashRoute";

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
