import { describe, expect, it } from "vitest";
import { describedBy, fieldIds } from "./field";

describe("describedBy", () => {
  it("joins ids in order and skips falsy entries", () => {
    expect(describedBy("a-hint", undefined, "a-err")).toBe("a-hint a-err");
    expect(describedBy(false, null, "", "  ")).toBeUndefined();
  });
  it("keeps a caller's existing space-separated ids and removes duplicates", () => {
    expect(describedBy("x-hint", "notice other", "x-hint")).toBe("x-hint notice other");
  });
  it("returns undefined when nothing describes the control", () => {
    expect(describedBy()).toBeUndefined();
  });
});

describe("fieldIds", () => {
  it("derives stable ids from the control id", () => {
    expect(fieldIds("item-name")).toEqual({ hint: "item-name-hint", error: "item-name-err" });
  });
});
