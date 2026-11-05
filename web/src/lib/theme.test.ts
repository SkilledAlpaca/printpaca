import { describe, expect, it } from "vitest";
import { applyTheme, effectiveTheme, loadTheme, parseTheme, saveTheme, THEME_STORAGE_KEY } from "./theme";

function fakeRoot() {
  const attrs = new Map<string, string>();
  return {
    attrs,
    setAttribute: (k: string, v: string) => void attrs.set(k, v),
    removeAttribute: (k: string) => void attrs.delete(k),
    getAttribute: (k: string) => attrs.get(k) ?? null,
  };
}

describe("parseTheme", () => {
  it("accepts only light and dark", () => {
    expect(parseTheme("light")).toBe("light");
    expect(parseTheme("dark")).toBe("dark");
    expect(parseTheme("system")).toBeNull();
    expect(parseTheme("Dark")).toBeNull();
    expect(parseTheme(null)).toBeNull();
  });
});

describe("loadTheme / saveTheme", () => {
  it("round-trips through storage and has no choice by default", () => {
    const map = new Map<string, string>();
    const storage = { getItem: (k: string) => map.get(k) ?? null, setItem: (k: string, v: string) => void map.set(k, v) };
    expect(loadTheme(storage)).toBeNull();
    saveTheme("dark", storage);
    expect(map.get(THEME_STORAGE_KEY)).toBe("dark");
    expect(loadTheme(storage)).toBe("dark");
  });

  it("survives storage that throws", () => {
    expect(loadTheme({ getItem: () => { throw new Error("blocked"); } })).toBeNull();
    expect(() => saveTheme("light", { setItem: () => { throw new Error("quota"); } })).not.toThrow();
  });
});

describe("applyTheme / effectiveTheme", () => {
  it("sets and clears data-theme", () => {
    const root = fakeRoot();
    applyTheme("dark", root);
    expect(root.attrs.get("data-theme")).toBe("dark");
    applyTheme(null, root);
    expect(root.attrs.has("data-theme")).toBe(false);
  });

  it("uses the attribute, else the device preference", () => {
    const root = fakeRoot();
    expect(effectiveTheme(root, true)).toBe("dark");
    expect(effectiveTheme(root, false)).toBe("light");
    applyTheme("light", root);
    expect(effectiveTheme(root, true)).toBe("light");
  });
});
