import { describe, expect, it } from "vitest";
import { applyDensity, DENSITY_STORAGE_KEY, loadDensity, parseDensity, saveDensity } from "./density";

describe("parseDensity", () => {
  it("accepts only compact; everything else is comfortable", () => {
    expect(parseDensity("compact")).toBe("compact");
    expect(parseDensity("comfortable")).toBe("comfortable");
    expect(parseDensity(null)).toBe("comfortable");
    expect(parseDensity(undefined)).toBe("comfortable");
    expect(parseDensity("Compact")).toBe("comfortable");
    expect(parseDensity("dense")).toBe("comfortable");
  });
});

describe("loadDensity / saveDensity", () => {
  it("round-trips through storage", () => {
    const map = new Map<string, string>();
    const storage = { getItem: (k: string) => map.get(k) ?? null, setItem: (k: string, v: string) => void map.set(k, v) };
    expect(loadDensity(storage)).toBe("comfortable");
    saveDensity("compact", storage);
    expect(map.get(DENSITY_STORAGE_KEY)).toBe("compact");
    expect(loadDensity(storage)).toBe("compact");
  });

  it("falls back to comfortable when reading throws", () => {
    const storage = { getItem: () => { throw new Error("blocked"); } };
    expect(loadDensity(storage)).toBe("comfortable");
  });

  it("does not throw when writing fails", () => {
    const storage = { setItem: () => { throw new Error("quota"); } };
    expect(() => saveDensity("compact", storage)).not.toThrow();
  });

  it("treats unavailable storage as the default", () => {
    expect(loadDensity(undefined)).toBe("comfortable");
    expect(() => saveDensity("compact", undefined)).not.toThrow();
  });
});

describe("applyDensity", () => {
  it("sets data-density for compact and clears it for comfortable", () => {
    const attrs = new Map<string, string>();
    const root = { setAttribute: (k: string, v: string) => void attrs.set(k, v), removeAttribute: (k: string) => void attrs.delete(k) };
    applyDensity("compact", root);
    expect(attrs.get("data-density")).toBe("compact");
    applyDensity("comfortable", root);
    expect(attrs.has("data-density")).toBe(false);
  });
});
