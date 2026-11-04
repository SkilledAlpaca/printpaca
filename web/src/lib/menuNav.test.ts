import { describe, expect, it } from "vitest";
import { nextMenuIndex } from "./menuNav";

describe("nextMenuIndex", () => {
  it("moves down and wraps at the end", () => {
    expect(nextMenuIndex("ArrowDown", 0, 3)).toBe(1);
    expect(nextMenuIndex("ArrowDown", 2, 3)).toBe(0);
  });
  it("moves up and wraps at the start", () => {
    expect(nextMenuIndex("ArrowUp", 2, 3)).toBe(1);
    expect(nextMenuIndex("ArrowUp", 0, 3)).toBe(2);
  });
  it("starts from the ends when focus is not on an item", () => {
    expect(nextMenuIndex("ArrowDown", -1, 3)).toBe(0);
    expect(nextMenuIndex("ArrowUp", -1, 3)).toBe(2);
  });
  it("jumps with Home and End", () => {
    expect(nextMenuIndex("Home", 2, 4)).toBe(0);
    expect(nextMenuIndex("End", 0, 4)).toBe(3);
  });
  it("ignores other keys and empty menus", () => {
    expect(nextMenuIndex("a", 0, 3)).toBeNull();
    expect(nextMenuIndex("ArrowDown", 0, 0)).toBeNull();
  });
});
