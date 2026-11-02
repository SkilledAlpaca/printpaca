import { describe, expect, it } from "vitest";
import { attributionText, extractRealMultiUser } from "./attribution";

describe("extractRealMultiUser", () => {
  it("true when config.users.multiUser is exactly true", () => {
    expect(extractRealMultiUser({ users: { multiUser: true } })).toBe(true);
  });

  it("false when config.users.multiUser is false", () => {
    expect(extractRealMultiUser({ users: { multiUser: false } })).toBe(false);
  });

  it("false for null/undefined config", () => {
    expect(extractRealMultiUser(null)).toBe(false);
    expect(extractRealMultiUser(undefined)).toBe(false);
  });

  it("false when config is not an object", () => {
    expect(extractRealMultiUser("nope")).toBe(false);
    expect(extractRealMultiUser(42)).toBe(false);
  });

  it("false when the config has no users section", () => {
    expect(extractRealMultiUser({})).toBe(false);
  });

  it("false when users is present but not an object", () => {
    expect(extractRealMultiUser({ users: "nope" })).toBe(false);
    expect(extractRealMultiUser({ users: null })).toBe(false);
  });

  it("false when multiUser is present but not exactly boolean true", () => {
    expect(extractRealMultiUser({ users: { multiUser: "true" } })).toBe(false);
    expect(extractRealMultiUser({ users: { multiUser: 1 } })).toBe(false);
    expect(extractRealMultiUser({ users: {} })).toBe(false);
  });
});

describe("attributionText", () => {
  it("joins both sides with the middle dot when both are present", () => {
    expect(attributionText("admin", "sam")).toBe("Created by admin · Updated by sam");
  });

  it("omits the createdBy side when empty (quote requests have no createdBy)", () => {
    expect(attributionText("", "riley")).toBe("Updated by riley");
  });

  it("omits the updatedBy side when empty", () => {
    expect(attributionText("admin", "")).toBe("Created by admin");
  });

  it("returns null (not an empty string) when both are empty", () => {
    expect(attributionText("", "")).toBeNull();
  });
});
