import { describe, expect, it } from "vitest";
import { GUIDE_SEARCH_ENTRIES, markWords, queryWords, resultCountText, searchGuide, type SearchEntry } from "./guideSearch";
import { GUIDE_NAV } from "./guideContent";

const entries: SearchEntry[] = [
  { id: "a", title: "Quote a print", passages: ["Price one print.", "Enter the Model Weight in grams."] },
  { id: "b", title: "Save a quote", passages: ["Press Save. The weight stays."] },
  { id: "c", title: "Weight rules", passages: ["Nothing else."] },
];

describe("queryWords", () => {
  it("lower-cases, splits on whitespace, and drops duplicates", () => {
    expect(queryWords("  Model  WEIGHT model ")).toEqual(["model", "weight"]);
    expect(queryWords("   ")).toEqual([]);
  });
});

describe("searchGuide", () => {
  it("returns null for a blank query", () => {
    expect(searchGuide(entries, "  ")).toBeNull();
  });
  it("matches every word, in any order, case-insensitively, across title and passages", () => {
    expect(searchGuide(entries, "WEIGHT model")!.map((r) => r.id)).toEqual(["a"]);
    expect(searchGuide(entries, "quote weight")!.map((r) => r.id)).toEqual(["a", "b"]);
  });
  it("lists title matches first, then keeps content order", () => {
    expect(searchGuide(entries, "weight")!.map((r) => r.id)).toEqual(["c", "a", "b"]);
  });
  it("returns an empty list when a word matches nothing", () => {
    expect(searchGuide(entries, "weight zebra")).toEqual([]);
  });
  it("treats regex characters literally", () => {
    expect(searchGuide(entries, "(")).toEqual([]);
    expect(markWords("a (b) c", ["(b)"])).toEqual([{ text: "a ", mark: false }, { text: "(b)", mark: true }, { text: " c", mark: false }]);
  });
  it("builds a snippet from the passage with the most matched words, with the words marked", () => {
    const r = searchGuide(entries, "model weight")![0]!;
    expect(r.snippet.map((p) => p.text).join("")).toBe("Enter the Model Weight in grams.");
    expect(r.snippet.filter((p) => p.mark).map((p) => p.text)).toEqual(["Model", "Weight"]);
  });
  it("shortens a long passage around the first match", () => {
    const long = `${"alpha ".repeat(40)}needle ${"omega ".repeat(40)}`;
    const r = searchGuide([{ id: "x", title: "T", passages: [long] }], "needle")![0]!;
    const text = r.snippet.map((p) => p.text).join("");
    expect(text.length).toBeLessThan(140);
    expect(text.startsWith("…") && text.endsWith("…")).toBe(true);
    expect(r.snippet.find((p) => p.mark)?.text).toBe("needle");
  });
});

describe("resultCountText", () => {
  it("announces none, one, and many", () => {
    expect(resultCountText(0)).toBe("No topics match");
    expect(resultCountText(1)).toBe("1 topic matches");
    expect(resultCountText(4)).toBe("4 topics match");
  });
});

describe("GUIDE_SEARCH_ENTRIES", () => {
  it("covers every topic in the nav, and finds a topic by step text", () => {
    const ids = GUIDE_NAV.flatMap((g) => g.items.map((i) => i.id));
    expect(GUIDE_SEARCH_ENTRIES.map((e) => e.id)).toEqual(ids);
    expect(searchGuide(GUIDE_SEARCH_ENTRIES, "spoolman url")!.map((r) => r.id)).toContain("filament-and-spoolman");
  });
});
