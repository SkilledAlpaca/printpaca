import { describe, expect, it } from "vitest";
import { neighborId } from "./a11yFocus";

// After a confirmed delete, focus goes to the next row, else the previous row, else (null) the list heading.
describe("neighborId", () => {
  it("returns the next id when the removed row has one after it", () => {
    expect(neighborId(["a", "b", "c"], "a")).toBe("b");
    expect(neighborId(["a", "b", "c"], "b")).toBe("c");
  });

  it("falls back to the previous id for the last row", () => {
    expect(neighborId(["a", "b", "c"], "c")).toBe("b");
  });

  it("returns null when the removed row was the only one", () => {
    expect(neighborId(["a"], "a")).toBeNull();
  });

  it("returns null for an id that isn't in the list", () => {
    expect(neighborId(["a", "b"], "z")).toBeNull();
    expect(neighborId([], "a")).toBeNull();
  });
});
