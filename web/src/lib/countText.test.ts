import { describe, expect, it } from "vitest";
import { characterCountText } from "./countText";

describe("characterCountText", () => {
  it("says characters, and notes extra weight only for multi-byte text", () => {
    expect(characterCountText("hello", 100)).toBe("5 / 100 characters");
    expect(characterCountText("café", 100)).toBe("5 / 100 characters (accented letters and emoji count as more than one)");
    expect(characterCountText("", 100)).toBe("0 / 100 characters");
  });
});
