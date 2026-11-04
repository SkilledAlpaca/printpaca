import { afterEach, describe, expect, it, vi } from "vitest";
import { fetchTopItems, optionsLine, sourceSplit, attentionTiles } from "./dashboardApi";

describe("optionsLine", () => {
  it("joins options as name and units with a middle dot", () => {
    expect(optionsLine([{ name: "Red", units: 4 }, { name: "Blue", units: 2 }])).toBe("Red 4 · Blue 2");
  });

  it("is empty when there are no options", () => {
    expect(optionsLine([])).toBe("");
  });
});

describe("sourceSplit", () => {
  it("names both sources", () => {
    expect(sourceSplit(9, 5)).toBe("9 shop orders · 5 in person");
  });
});

describe("fetchTopItems", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("requests the chosen window and returns the body", async () => {
    const body = { days: 90, totalUnits: 0, galleryUnits: 0, inPersonUnits: 0, totalRevenueCents: 0, items: [] };
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, json: async () => body });
    vi.stubGlobal("fetch", fetchMock);
    expect(await fetchTopItems(90)).toEqual(body);
    expect(fetchMock.mock.calls[0]?.[0]).toBe("api/dashboard/top-items?days=90");
  });

  it("returns null on a non-OK response", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: false }));
    expect(await fetchTopItems(30)).toBeNull();
  });

  it("returns null on a network failure", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("offline")));
    expect(await fetchTopItems(30)).toBeNull();
  });
});

describe("attentionTiles (D25)", () => {
  const counts = { newRequests: 4, printTimeRequests: 2, overdueInvoices: 1, draftInvoices: 3, sentQuotes: 5 };

  it("lists the five tiles in D25's order with their counts", () => {
    expect(attentionTiles(counts, () => true).map((t) => [t.label, t.value])).toEqual([
      ["New requests", 4],
      ["Overdue invoices", 1],
      ["Draft invoices", 3],
      ["Needs print time", 2],
      ["Quotes awaiting reply", 5],
    ]);
  });

  it("leaves out a tile whose page the user can't open", () => {
    const labels = attentionTiles(counts, (tab) => tab === "invoices").map((t) => t.label);
    expect(labels).toEqual(["Overdue invoices", "Draft invoices"]);
  });
});
