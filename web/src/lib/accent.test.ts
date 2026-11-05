import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import {
  ACCENT_DARK_BACKGROUNDS,
  ACCENT_LIGHT_BACKGROUNDS,
  accentContrast,
  accentProblem,
  checkAccentColor,
  type AccentCheck,
} from "./accent";
import { contrastRatio } from "./contrast";

// testdata/accent-vectors.json is shared with server/accent_test.go; both must agree. Regenerate it from the
// Go test (see the file's description), never by hand.
interface Vector {
  name: string;
  input: string;
  expected?: AccentCheck;
  problem: string;
}
const fx = JSON.parse(readFileSync(resolve(__dirname, "../../../testdata/accent-vectors.json"), "utf8")) as {
  vectors: Vector[];
};

const near = (a: number, b: number) => Math.abs(a - b) <= 0.001;

describe("shared accent vectors (Go parity)", () => {
  it("has vectors", () => expect(fx.vectors.length).toBeGreaterThan(10));
  for (const v of fx.vectors) {
    it(v.name, () => {
      expect(accentProblem(v.input)).toBe(v.problem);
      if (!v.expected) return;
      const got = checkAccentColor(v.input);
      const want = v.expected;
      expect(got.ok).toBe(want.ok);
      expect(got.accent).toBe(want.accent);
      expect(got.suggestion).toBe(want.suggestion);
      for (const scheme of ["light", "dark"] as const) {
        expect(got[scheme].accent).toBe(want[scheme].accent);
        expect(got[scheme].buttonText).toBe(want[scheme].buttonText);
        expect(got[scheme].pass).toBe(want[scheme].pass);
        expect(near(got[scheme].linkRatio, want[scheme].linkRatio)).toBe(true);
        expect(near(got[scheme].buttonRatio, want[scheme].buttonRatio)).toBe(true);
      }
    });
  }
});

describe("accent contrast", () => {
  it("agrees with the theme's contrast helper", () => {
    for (const [a, b] of [["#9c4212", "#fffbef"], ["#e06020", "#fff5dc"], ["#f1b750", "#141414"]]) {
      expect(accentContrast(a!, b!)).toBeCloseTo(contrastRatio(a!, b!), 10);
    }
  });

  it("a suggestion always passes the light pairs, and a derived dark variant the dark pairs", () => {
    for (const hex of ["#e06020", "#ffd700", "#2a9d8f", "#d62828", "#808080", "#00ff00", "#9c4212", "#123456"]) {
      const res = checkAccentColor(hex);
      if (res.light.pass) {
        expect(res.ok).toBe(true);
        expect(res.dark.pass).toBe(true);
        for (const bg of ACCENT_DARK_BACKGROUNDS) expect(accentContrast(res.dark.accent, bg)).toBeGreaterThanOrEqual(4.5);
        expect(accentContrast(res.dark.accent, res.dark.buttonText)).toBeGreaterThanOrEqual(4.5);
      } else {
        expect(res.ok).toBe(false);
        expect(res.suggestion).not.toBe("");
        for (const bg of ACCENT_LIGHT_BACKGROUNDS) expect(accentContrast(res.suggestion, bg)).toBeGreaterThanOrEqual(4.5);
        expect(checkAccentColor(res.suggestion).ok).toBe(true);
      }
    }
  });

  it("rejects the theme orange as link text and names the ratios", () => {
    expect(accentProblem("#e06020")).toMatch(/links measure \d\.\d\d:1/);
  });

  it("accepts an empty value and normalizes case", () => {
    expect(accentProblem("")).toBe("");
    expect(checkAccentColor("#9C4212").accent).toBe("#9c4212");
  });

  it("rejects formats other than six hex digits", () => {
    for (const bad of ["#fff", "9c4212", "orange", "#9c421", "#9c42123"]) {
      expect(accentProblem(bad)).toContain("six hex digits");
      expect(checkAccentColor(bad).ok).toBe(false);
    }
  });
});
