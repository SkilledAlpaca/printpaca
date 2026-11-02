import { describe, expect, it } from "vitest";
import { densityOrFallback, FALLBACK_DENSITY } from "./filamentDefaults";

describe("densityOrFallback", () => {
  it("returns a usable density unchanged", () => {
    expect(densityOrFallback(1.27)).toBe(1.27);
  });

  it.each([undefined, null, 0, -1, NaN, Infinity])("falls back for %s", (d) => {
    expect(densityOrFallback(d)).toBe(FALLBACK_DENSITY);
  });
});
