import { describe, expect, it } from "vitest";
import { matchesQuoteFilter } from "./quoteFilter";

const NOW = Date.parse("2026-11-03T12:00:00Z");
const quote = (over: Record<string, unknown> = {}) => ({
  label: "Lamp shade pair",
  clientName: "Casey Ng",
  status: "draft" as const,
  savedAt: "2026-11-01T12:00:00Z",
  invoicedAs: undefined as string | undefined,
  ...over,
});

describe("matchesQuoteFilter", () => {
  it("matches the label and the client name, case-insensitively", () => {
    expect(matchesQuoteFilter(quote(), "lamp", "all", 30, NOW)).toBe(true);
    expect(matchesQuoteFilter(quote(), "CASEY", "all", 30, NOW)).toBe(true);
    expect(matchesQuoteFilter(quote(), "robin", "all", 30, NOW)).toBe(false);
  });
  it("treats blank text as no text filter and a missing client as empty", () => {
    expect(matchesQuoteFilter(quote({ clientName: undefined }), "  ", "all", 30, NOW)).toBe(true);
    expect(matchesQuoteFilter(quote({ clientName: undefined }), "casey", "all", 30, NOW)).toBe(false);
  });
  it("filters by the displayed status, so an old draft counts as expired", () => {
    expect(matchesQuoteFilter(quote(), "", "draft", 30, NOW)).toBe(true);
    expect(matchesQuoteFilter(quote({ savedAt: "2026-08-01T00:00:00Z" }), "", "expired", 30, NOW)).toBe(true);
    expect(matchesQuoteFilter(quote({ savedAt: "2026-08-01T00:00:00Z" }), "", "draft", 30, NOW)).toBe(false);
    expect(matchesQuoteFilter(quote({ status: "accepted" }), "", "accepted", 30, NOW)).toBe(true);
  });
  it("lists converted quotes under Invoiced whatever their status", () => {
    expect(matchesQuoteFilter(quote({ invoicedAs: "inv1", status: "accepted" }), "", "invoiced", 30, NOW)).toBe(true);
    expect(matchesQuoteFilter(quote(), "", "invoiced", 30, NOW)).toBe(false);
  });
  it("composes text and status", () => {
    expect(matchesQuoteFilter(quote(), "casey", "sent", 30, NOW)).toBe(false);
    expect(matchesQuoteFilter(quote(), "casey", "draft", 30, NOW)).toBe(true);
  });
});
