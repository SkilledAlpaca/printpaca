import { describe, expect, it } from "vitest";
import { accentVars, prefersDark } from "./shopAccent";

describe("accentVars", () => {
  it("maps a scheme to the four accent properties", () => {
    expect(accentVars({ accent: "#9c4212", buttonText: "#ffffff" })).toEqual({
      "--accent": "#9c4212",
      "--accent-text": "#9c4212",
      "--focus-ring": "#9c4212",
      "--accent-contrast": "#ffffff",
    });
  });

  it("refuses anything that is not a plain hex color", () => {
    expect(accentVars({ accent: "red", buttonText: "#ffffff" })).toBeNull();
    expect(accentVars({ accent: "#9c4212; background: url(x)", buttonText: "#ffffff" })).toBeNull();
    expect(accentVars({ accent: "#9c4212", buttonText: "" })).toBeNull();
  });
});

describe("prefersDark", () => {
  const root = (theme?: string) => ({ getAttribute: () => theme ?? null }) as unknown as HTMLElement;
  it("follows the OS preference without an explicit theme", () => {
    expect(prefersDark(root(), { matches: true })).toBe(true);
    expect(prefersDark(root(), { matches: false })).toBe(false);
  });
  it("lets an explicit theme win", () => {
    expect(prefersDark(root("light"), { matches: true })).toBe(false);
    expect(prefersDark(root("dark"), { matches: false })).toBe(true);
  });
});
