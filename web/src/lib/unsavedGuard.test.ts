import { describe, expect, it, vi } from "vitest";
import { UNSAVED_PROMPT, canLeave, escapeBelongsToField } from "./unsavedGuard";

function el(tagName: string, isContentEditable = false): EventTarget {
  return { tagName, isContentEditable } as unknown as EventTarget;
}

describe("escapeBelongsToField", () => {
  it.each(["INPUT", "TEXTAREA", "SELECT"])("is true for %s", (tag) => {
    expect(escapeBelongsToField(el(tag))).toBe(true);
  });
  it("is true for contenteditable", () => {
    expect(escapeBelongsToField(el("DIV", true))).toBe(true);
  });
  it("is false for other elements and null", () => {
    expect(escapeBelongsToField(el("BUTTON"))).toBe(false);
    expect(escapeBelongsToField(null)).toBe(false);
  });
});

describe("canLeave", () => {
  it("allows leaving a clean form without asking", () => {
    const ask = vi.fn(() => false);
    expect(canLeave(false, ask)).toBe(true);
    expect(ask).not.toHaveBeenCalled();
  });
  it("asks when dirty and follows the answer", () => {
    const yes = vi.fn(() => true);
    expect(canLeave(true, yes)).toBe(true);
    expect(yes).toHaveBeenCalledWith(UNSAVED_PROMPT);
    expect(canLeave(true, () => false)).toBe(false);
  });
});
