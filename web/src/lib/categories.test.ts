import { describe, expect, it } from "vitest";
import { deleteConsequence, nextCategoryOrder, reorderUpdates, slugify, slugProblem } from "./categories";

describe("slugify", () => {
  it("lowercases and joins runs with single hyphens", () => {
    expect(slugify("  Holiday & Gifts! ")).toBe("holiday-gifts");
    expect(slugify("Desk  Organizers")).toBe("desk-organizers");
  });
  it("returns empty when nothing usable is left", () => {
    expect(slugify("!!!")).toBe("");
    expect(slugify("日本")).toBe("");
  });
  it("trims to 40 characters without a trailing hyphen", () => {
    const s = slugify("a".repeat(39) + " b");
    expect(s.length).toBeLessThanOrEqual(40);
    expect(s.endsWith("-")).toBe(false);
  });
});

describe("slugProblem", () => {
  it("accepts empty and valid slugs", () => {
    expect(slugProblem("")).toBe("");
    expect(slugProblem("desk-organizers")).toBe("");
  });
  it("rejects capitals, spaces, double hyphens and long slugs", () => {
    for (const bad of ["Desk", "a b", "a--b", "-a", "a-", "x".repeat(41)]) expect(slugProblem(bad)).not.toBe("");
  });
});

describe("reorderUpdates", () => {
  const list = [
    { id: "a", sortOrder: 0 },
    { id: "b", sortOrder: 1 },
    { id: "c", sortOrder: 2 },
  ];
  it("swaps two neighbours", () => {
    expect(reorderUpdates(list, 1, -1)).toEqual([
      { id: "b", sortOrder: 0 },
      { id: "a", sortOrder: 1 },
    ]);
  });
  it("renumbers ties so the move shows", () => {
    const tied = [
      { id: "a", sortOrder: 0 },
      { id: "b", sortOrder: 0 },
    ];
    // b is already 0; only a has to move to 1 for b to sort first.
    expect(reorderUpdates(tied, 1, -1)).toEqual([{ id: "a", sortOrder: 1 }]);
  });
  it("does nothing at the ends", () => {
    expect(reorderUpdates(list, 0, -1)).toEqual([]);
    expect(reorderUpdates(list, 2, 1)).toEqual([]);
  });
});

describe("nextCategoryOrder", () => {
  it("is one past the largest", () => {
    expect(nextCategoryOrder([])).toBe(0);
    expect(nextCategoryOrder([{ sortOrder: 4 }, { sortOrder: 2 }])).toBe(5);
  });
});

describe("deleteConsequence", () => {
  it("states the count and that items lose the category", () => {
    expect(deleteConsequence({ name: "Gifts", itemCount: 3 })).toContain("3 items use Gifts");
    expect(deleteConsequence({ name: "Gifts", itemCount: 1 })).toContain("1 item uses Gifts");
    expect(deleteConsequence({ name: "Gifts", itemCount: 0 })).toContain("no items");
  });
});
