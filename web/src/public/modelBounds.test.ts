import { describe, expect, it } from "vitest";
import { BoundsCollector, boundsWarning, formatBounds } from "./modelBounds";

function cube(c: BoundsCollector, size: number): void {
  c.add(0, 0, 0, size, 0, 0, 0, size, 0);
  c.add(0, 0, size, size, size, size, 0, size, size);
}

describe("BoundsCollector", () => {
  it("reports the extents of the triangles it saw", () => {
    const c = new BoundsCollector();
    cube(c, 20);
    expect(c.result()).toEqual({ x: 20, y: 20, z: 20 });
  });

  it("handles negative coordinates", () => {
    const c = new BoundsCollector();
    c.add(-5, -5, -5, 5, 5, 5, 0, 0, 0);
    expect(c.result()).toEqual({ x: 10, y: 10, z: 10 });
  });

  it("returns null with no triangles", () => {
    expect(new BoundsCollector().result()).toBeNull();
  });
});

describe("formatBounds", () => {
  it("rounds to a tenth of a millimetre and drops decimals past 100", () => {
    expect(formatBounds({ x: 20, y: 20.04, z: 123.4 })).toBe("20 × 20 × 123 mm");
    expect(formatBounds({ x: 0.787, y: 0.787, z: 0.787 })).toBe("0.8 × 0.8 × 0.8 mm");
  });
});

describe("boundsWarning", () => {
  it("flags a model that looks exported in inches", () => {
    expect(boundsWarning({ x: 0.787, y: 0.787, z: 0.787 })).toMatch(/inches/);
  });
  it("flags a model larger than any printer", () => {
    expect(boundsWarning({ x: 2500, y: 10, z: 10 })).toMatch(/larger than most printers/);
  });
  it("is quiet for a normal part", () => {
    expect(boundsWarning({ x: 20, y: 20, z: 20 })).toBeNull();
  });
});
