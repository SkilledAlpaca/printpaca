import { describe, expect, it } from "vitest";
import { buildQuotesFile, isImportableQuote, parseQuotesFile, sortQuotes } from "./quotesFile";
import type { SavedQuote } from "./api";

function makeQuote(overrides: Partial<SavedQuote> = {}): SavedQuote {
  return {
    id: "q1",
    savedAt: "2024-01-01T00:00:00.000Z",
    label: "Benchy",
    perUnitTotal: 1.5,
    batchTotal: 15,
    inputs: { version: 1, parts: [{}], order: {} },
    status: "draft",
    statusLog: [],
    partCount: 1,
    ...overrides,
  };
}

describe("buildQuotesFile / parseQuotesFile round trip", () => {
  it("round-trips quotes through JSON with no skips", () => {
    const quotes = [makeQuote({ id: "a" }), makeQuote({ id: "b" })];
    const payload = buildQuotesFile(quotes);
    const text = JSON.stringify(payload);
    const result = parseQuotesFile(text);
    expect(result).not.toBeNull();
    expect(result!.quotes).toEqual(quotes);
    expect(result!.skipped).toBe(0);
  });

  it("rejects a bare JSON array of quotes", () => {
    expect(parseQuotesFile(JSON.stringify([makeQuote()]))).toBeNull();
  });

  it("returns null for invalid JSON text", () => {
    expect(parseQuotesFile("not json {{{")).toBeNull();
  });

  it("returns null for a JSON object without a quotes array", () => {
    expect(parseQuotesFile(JSON.stringify({ version: 1 }))).toBeNull();
    expect(parseQuotesFile(JSON.stringify({ version: 1, quotes: "not-an-array" }))).toBeNull();
    expect(parseQuotesFile(JSON.stringify({ quotes: [] }))).toBeNull();
  });

  it("returns null for a top-level JSON string/number/null/boolean", () => {
    expect(parseQuotesFile(JSON.stringify("hello"))).toBeNull();
    expect(parseQuotesFile(JSON.stringify(42))).toBeNull();
    expect(parseQuotesFile(JSON.stringify(null))).toBeNull();
    expect(parseQuotesFile(JSON.stringify(true))).toBeNull();
  });

  it("skips entries missing status, statusLog, or partCount, or with a status outside the four", () => {
    const valid = makeQuote({ id: "valid" });
    const { status: _s, ...noStatus } = makeQuote({ id: "no-status" });
    const { statusLog: _l, ...noLog } = makeQuote({ id: "no-log" });
    const { partCount: _p, ...noCount } = makeQuote({ id: "no-count" });
    const result = parseQuotesFile(
      JSON.stringify({ version: 1, quotes: [valid, noStatus, noLog, noCount, { ...makeQuote(), status: "expired" }] }),
    );
    expect(result).toEqual({ quotes: [valid], skipped: 4 });
  });

  it("round-trips the new status/statusLog fields when present", () => {
    const withStatus = makeQuote({
      id: "s1",
      status: "sent",
      statusLog: [{ status: "draft", at: "2026-01-01T00:00:00.000Z" }, { status: "sent", at: "2026-01-02T00:00:00.000Z" }],
    });
    const payload = buildQuotesFile([withStatus]);
    const result = parseQuotesFile(JSON.stringify(payload));
    expect(result!.quotes).toEqual([withStatus]);
  });

  it("drops and counts invalid entries while keeping valid ones", () => {
    const valid = makeQuote({ id: "valid" });
    const entries = [
      valid,
      null,
      { ...makeQuote(), id: undefined },
      { ...makeQuote(), label: 123 },
      { ...makeQuote(), savedAt: "not-a-date" },
      { ...makeQuote(), perUnitTotal: Number("1e999") },
      { ...makeQuote(), batchTotal: Number("1e999") },
    ];
    const result = parseQuotesFile(JSON.stringify({ version: 1, quotes: entries }));
    expect(result).not.toBeNull();
    expect(result!.quotes).toEqual([valid]);
    expect(result!.skipped).toBe(6);
  });
});

describe("isImportableQuote", () => {
  it("true for a minimal valid quote", () => {
    expect(isImportableQuote(makeQuote())).toBe(true);
  });

  it("true with extra unknown keys allowed", () => {
    expect(isImportableQuote({ ...makeQuote(), extra: "field" })).toBe(true);
  });

  it("false for non-object / null", () => {
    expect(isImportableQuote(null)).toBe(false);
    expect(isImportableQuote("string")).toBe(false);
    expect(isImportableQuote(42)).toBe(false);
  });

  it("false when id is not a string", () => {
    expect(isImportableQuote({ ...makeQuote(), id: 1 })).toBe(false);
  });

  it("false when savedAt is not a string", () => {
    expect(isImportableQuote({ ...makeQuote(), savedAt: 12345 })).toBe(false);
  });

  it("false when savedAt is unparseable", () => {
    expect(isImportableQuote({ ...makeQuote(), savedAt: "not-a-date" })).toBe(false);
  });

  it("false when label is not a string", () => {
    expect(isImportableQuote({ ...makeQuote(), label: 123 })).toBe(false);
  });

  it("false when perUnitTotal is not a finite number", () => {
    expect(isImportableQuote({ ...makeQuote(), perUnitTotal: "1.5" })).toBe(false);
    expect(isImportableQuote({ ...makeQuote(), perUnitTotal: Number("1e999") })).toBe(false);
    expect(isImportableQuote({ ...makeQuote(), perUnitTotal: NaN })).toBe(false);
  });

  it("false when batchTotal is not a finite number", () => {
    expect(isImportableQuote({ ...makeQuote(), batchTotal: "15" })).toBe(false);
    expect(isImportableQuote({ ...makeQuote(), batchTotal: Number("1e999") })).toBe(false);
    expect(isImportableQuote({ ...makeQuote(), batchTotal: NaN })).toBe(false);
  });
});

describe("sortQuotes", () => {
  const q1 = makeQuote({
    id: "1",
    label: "Charlie",
    savedAt: "2026-01-01T00:00:00.000Z",
    batchTotal: 20,
    resolvedPrice: 20,
  });
  const q2 = makeQuote({
    id: "2",
    label: "alpha",
    savedAt: "2026-01-03T00:00:00.000Z",
    batchTotal: 5,
    resolvedPrice: 5,
  });
  const q3 = makeQuote({
    id: "3",
    label: "Bravo",
    savedAt: "2026-01-02T00:00:00.000Z",
    batchTotal: 20,
    resolvedPrice: 20,
  });
  const quotes = [q1, q2, q3];

  it("newest sorts by savedAt descending", () => {
    expect(sortQuotes(quotes, "newest").map((q) => q.id)).toEqual(["2", "3", "1"]);
  });

  it("oldest sorts by savedAt ascending", () => {
    expect(sortQuotes(quotes, "oldest").map((q) => q.id)).toEqual(["1", "3", "2"]);
  });

  it("price-desc sorts by displayPriceCents descending, tiebreaking on savedAt descending", () => {
    expect(sortQuotes(quotes, "price-desc").map((q) => q.id)).toEqual(["3", "1", "2"]);
  });

  it("price-asc sorts by displayPriceCents ascending, tiebreaking on savedAt descending", () => {
    expect(sortQuotes(quotes, "price-asc").map((q) => q.id)).toEqual(["2", "3", "1"]);
  });

  it("label sorts by localeCompare, tiebreaking on savedAt descending", () => {
    // localeCompare ordering, no ties among these labels.
    expect(sortQuotes(quotes, "label").map((q) => q.id)).toEqual(["2", "3", "1"]);
  });

  it("labels that tie fall back to savedAt descending", () => {
    const a = makeQuote({ id: "a", label: "Same", savedAt: "2026-01-01T00:00:00.000Z" });
    const b = makeQuote({ id: "b", label: "Same", savedAt: "2026-01-02T00:00:00.000Z" });
    expect(sortQuotes([a, b], "label").map((q) => q.id)).toEqual(["b", "a"]);
  });

  it("does not mutate the input array", () => {
    const original = [...quotes];
    sortQuotes(quotes, "price-asc");
    expect(quotes).toEqual(original);
    expect(quotes.map((q) => q.id)).toEqual(["1", "2", "3"]);
  });

  it("returns a new array, not the same reference", () => {
    expect(sortQuotes(quotes, "newest")).not.toBe(quotes);
  });

  it("price-desc sorts by displayPriceCents descending, with cost-only quotes (no price) last", () => {
    const priced1 = makeQuote({ id: "p1", savedAt: "2026-01-01T00:00:00.000Z", resolvedPrice: 500 });
    const priced2 = makeQuote({ id: "p2", savedAt: "2026-01-02T00:00:00.000Z", resolvedPrice: 900 });
    const costOnly = makeQuote({ id: "c1", savedAt: "2026-01-03T00:00:00.000Z" });
    const result = sortQuotes([priced1, costOnly, priced2], "price-desc");
    expect(result.map((q) => q.id)).toEqual(["p2", "p1", "c1"]);
  });

  it("price-asc sorts by displayPriceCents ascending, with cost-only quotes (no price) last", () => {
    const priced1 = makeQuote({ id: "p1", savedAt: "2026-01-01T00:00:00.000Z", resolvedPrice: 500 });
    const priced2 = makeQuote({ id: "p2", savedAt: "2026-01-02T00:00:00.000Z", resolvedPrice: 900 });
    const costOnly = makeQuote({ id: "c1", savedAt: "2026-01-03T00:00:00.000Z" });
    const result = sortQuotes([priced1, costOnly, priced2], "price-asc");
    expect(result.map((q) => q.id)).toEqual(["p1", "p2", "c1"]);
  });

  it("price-desc falls back to recommendedPrice when resolvedPrice is absent", () => {
    const a = makeQuote({ id: "a", savedAt: "2026-01-01T00:00:00.000Z", recommendedPrice: 500 });
    const b = makeQuote({ id: "b", savedAt: "2026-01-02T00:00:00.000Z", recommendedPrice: 900 });
    expect(sortQuotes([a, b], "price-desc").map((q) => q.id)).toEqual(["b", "a"]);
  });

  it("price ties tiebreak on savedAt descending", () => {
    const a = makeQuote({ id: "a", savedAt: "2026-01-01T00:00:00.000Z", resolvedPrice: 500 });
    const b = makeQuote({ id: "b", savedAt: "2026-01-02T00:00:00.000Z", resolvedPrice: 500 });
    expect(sortQuotes([a, b], "price-desc").map((q) => q.id)).toEqual(["b", "a"]);
    expect(sortQuotes([a, b], "price-asc").map((q) => q.id)).toEqual(["b", "a"]);
  });

  it("status sorts draft, sent, accepted, declined", () => {
    const draft = makeQuote({ id: "d", savedAt: "2026-01-01T00:00:00.000Z", status: "draft" });
    const sent = makeQuote({ id: "s", savedAt: "2026-01-01T00:00:00.000Z", status: "sent" });
    const accepted = makeQuote({ id: "a", savedAt: "2026-01-01T00:00:00.000Z", status: "accepted" });
    const declined = makeQuote({ id: "x", savedAt: "2026-01-01T00:00:00.000Z", status: "declined" });
    const result = sortQuotes([declined, accepted, sent, draft], "status");
    expect(result.map((q) => q.id)).toEqual(["d", "s", "a", "x"]);
  });

  it("status sort ties tiebreak on savedAt descending", () => {
    const a = makeQuote({ id: "a", savedAt: "2026-01-01T00:00:00.000Z", status: "sent" });
    const b = makeQuote({ id: "b", savedAt: "2026-01-02T00:00:00.000Z", status: "sent" });
    expect(sortQuotes([a, b], "status").map((q) => q.id)).toEqual(["b", "a"]);
  });

  it("status sort ignores derived expiry (an old draft still sorts as draft, not last)", () => {
    // sortQuotes has no `now`/`validDays`, so derived expiry is display-only and never affects the sort.
    const oldDraft = makeQuote({ id: "old-draft", savedAt: "2000-01-01T00:00:00.000Z", status: "draft" });
    const accepted = makeQuote({ id: "accepted", savedAt: "2026-01-01T00:00:00.000Z", status: "accepted" });
    expect(sortQuotes([accepted, oldDraft], "status").map((q) => q.id)).toEqual(["old-draft", "accepted"]);
  });

  it("two cost-only quotes (both null price) tiebreak on savedAt descending, in both directions", () => {
    // Both have null displayPriceCents, so the comparator hits NaN (falsy) and falls through to the
    // savedAt tiebreak; pin that explicitly.
    const a = makeQuote({ id: "a", savedAt: "2026-01-01T00:00:00.000Z" });
    const b = makeQuote({ id: "b", savedAt: "2026-01-02T00:00:00.000Z" });
    expect(sortQuotes([a, b], "price-desc").map((q) => q.id)).toEqual(["b", "a"]);
    expect(sortQuotes([a, b], "price-asc").map((q) => q.id)).toEqual(["b", "a"]);
  });
});

describe("export and import versions", () => {
  it("writes version 1", () => {
    expect(buildQuotesFile([]).version).toBe(1);
  });

  it("imports only version 1 files whose entries carry a version 1 form state", () => {
    const ok = makeQuote({ id: "b", inputs: { version: 1, parts: [{}, {}], order: {} }, partCount: 2 });
    expect(parseQuotesFile(JSON.stringify(buildQuotesFile([ok])))!.quotes).toHaveLength(1);
    // An unknown file version is refused outright.
    expect(parseQuotesFile(JSON.stringify({ version: 99, quotes: [ok] }))).toBeNull();
    // A flat form state without a version is skipped, not restored as a default part.
    const flatEntry = makeQuote({ id: "a", inputs: { weightG: 5 } });
    expect(parseQuotesFile(JSON.stringify({ version: 1, quotes: [flatEntry, ok] }))).toEqual({ quotes: [ok], skipped: 1 });
  });
});
