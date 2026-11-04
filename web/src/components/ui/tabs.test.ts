import { describe, expect, it } from "vitest";
import { tabIds, tabTarget } from "./tabs";

describe("tabTarget", () => {
  it("moves right and left", () => {
    expect(tabTarget("ArrowRight", 0, 3)).toBe(1);
    expect(tabTarget("ArrowLeft", 2, 3)).toBe(1);
  });
  it("wraps at both ends", () => {
    expect(tabTarget("ArrowRight", 2, 3)).toBe(0);
    expect(tabTarget("ArrowLeft", 0, 3)).toBe(2);
  });
  it("jumps to the ends with Home and End", () => {
    expect(tabTarget("Home", 2, 4)).toBe(0);
    expect(tabTarget("End", 0, 4)).toBe(3);
  });
  it("ignores other keys and empty lists", () => {
    expect(tabTarget("Enter", 0, 3)).toBeNull();
    expect(tabTarget("ArrowDown", 0, 3)).toBeNull();
    expect(tabTarget("ArrowRight", 0, 0)).toBeNull();
  });
  it("stays on the only tab", () => {
    expect(tabTarget("ArrowRight", 0, 1)).toBe(0);
    expect(tabTarget("ArrowLeft", 0, 1)).toBe(0);
  });
});

describe("tabIds", () => {
  it("derives a tab id from the prefix and the tab, and one panel id per prefix", () => {
    expect(tabIds("p", "a")).toEqual({ tab: "p-tab-a", panel: "p-panel" });
  });
});
