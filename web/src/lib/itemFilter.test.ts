import { describe, expect, it } from "vitest";
import { filterItems } from "./itemFilter";
import type { Item } from "./itemsApi";

const item = (name: string, description = ""): Item =>
  ({ id: name, name, description }) as unknown as Item;

describe("filterItems", () => {
  const all = [item("Cable Clip 8-Pack", "Desk cable holder"), item("Benchy", "A small boat"), item("Lamp")];

  it("returns everything for a blank query", () => {
    expect(filterItems(all, "  ")).toEqual(all);
  });
  it("matches name or description, case-insensitively", () => {
    expect(filterItems(all, "BOAT").map((i) => i.name)).toEqual(["Benchy"]);
    expect(filterItems(all, "clip").map((i) => i.name)).toEqual(["Cable Clip 8-Pack"]);
  });
  it("requires every word to match", () => {
    expect(filterItems(all, "cable boat")).toEqual([]);
    expect(filterItems(all, "cable desk").map((i) => i.name)).toEqual(["Cable Clip 8-Pack"]);
  });
});
