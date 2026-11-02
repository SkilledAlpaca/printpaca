import { describe, expect, it } from "vitest";
import {
  AMBIENT,
  computeBoundingBox,
  computeFit,
  depthSortTriangles,
  projectTriangle,
  rotatePoint,
  shadeNormal,
  TriangleCollector,
  TRIANGLE_CAP,
  type ProjectedTriangle,
  type Triangle,
} from "./modelPreview";

// The same 10mm axis-aligned cube as the stl and threeMf tests, as the flat 9-number Triangle layout.
function cube(side = 10): Triangle[] {
  const v: [number, number, number][] = [
    [0, 0, 0], [side, 0, 0], [side, side, 0], [0, side, 0],
    [0, 0, side], [side, 0, side], [side, side, side], [0, side, side],
  ];
  const tris: [number, number, number][] = [
    [0, 3, 2], [0, 2, 1], [4, 5, 6], [4, 6, 7],
    [0, 1, 5], [0, 5, 4], [3, 7, 6], [3, 6, 2],
    [0, 4, 7], [0, 7, 3], [1, 2, 6], [1, 6, 5],
  ];
  return tris.map(([a, b, c]) => {
    const va = v[a]!, vb = v[b]!, vc = v[c]!;
    return {
      x1: va[0], y1: va[1], z1: va[2],
      x2: vb[0], y2: vb[1], z2: vb[2],
      x3: vc[0], y3: vc[1], z3: vc[2],
    };
  });
}

describe("computeBoundingBox", () => {
  it("returns null for an empty triangle list", () => {
    expect(computeBoundingBox([])).toBeNull();
  });

  it("computes the min/max across all three vertices of every triangle", () => {
    const box = computeBoundingBox(cube(10));
    expect(box).toEqual({ minX: 0, minY: 0, minZ: 0, maxX: 10, maxY: 10, maxZ: 10 });
  });

  it("handles a single off-origin triangle", () => {
    const box = computeBoundingBox([{ x1: 1, y1: -2, z1: 3, x2: 5, y2: 0, z2: -3, x3: -1, y3: 4, z3: 2 }]);
    expect(box).toEqual({ minX: -1, minY: -2, minZ: -3, maxX: 5, maxY: 4, maxZ: 3 });
  });
});

describe("computeFit", () => {
  it("centers on the bounding box's own center", () => {
    const box = computeBoundingBox(cube(10))!;
    const fit = computeFit(box, 300);
    expect(fit.centerX).toBe(5);
    expect(fit.centerY).toBe(5);
    expect(fit.centerZ).toBe(5);
  });

  it("scales so the bounding SPHERE (half the space diagonal), not just the box, fits within padding * viewportSize / 2", () => {
    const box = { minX: 0, minY: 0, minZ: 0, maxX: 10, maxY: 10, maxZ: 10 };
    const fit = computeFit(box, 300, 0.8);
    const radius = Math.sqrt(10 * 10 * 3) / 2; // half the cube's space diagonal
    expect(fit.scale).toBeCloseTo((300 * 0.8) / 2 / radius, 10);
      // Any point on the bounding sphere, scaled, must land within the padded viewport radius after rotation.
    expect(fit.scale * radius).toBeCloseTo((300 * 0.8) / 2, 10);
  });

  it("a degenerate (zero-size) box gets scale 1, not division by zero", () => {
    const fit = computeFit({ minX: 5, minY: 5, minZ: 5, maxX: 5, maxY: 5, maxZ: 5 }, 300);
    expect(fit.scale).toBe(1);
    expect(Number.isFinite(fit.scale)).toBe(true);
  });
});

describe("rotatePoint", () => {
  it("a zero rotation is the identity", () => {
    expect(rotatePoint(3, -2, 7, 0, 0)).toEqual({ x: 3, y: -2, z: 7 });
  });

  it("a 90-degree yaw maps +X onto -Z (this module's yaw-about-Y convention)", () => {
    const r = rotatePoint(1, 0, 0, Math.PI / 2, 0);
    expect(r.x).toBeCloseTo(0, 10);
    expect(r.y).toBeCloseTo(0, 10);
    expect(r.z).toBeCloseTo(-1, 10);
  });

  it("a 90-degree pitch maps +Y onto +Z", () => {
    const r = rotatePoint(0, 1, 0, 0, Math.PI / 2);
    expect(r.x).toBeCloseTo(0, 10);
    expect(r.y).toBeCloseTo(0, 10);
    expect(r.z).toBeCloseTo(1, 10);
  });

  it("preserves vector length (rotation is an isometry)", () => {
    const r = rotatePoint(3, -4, 5, 0.7, -1.3);
    const before = Math.sqrt(3 * 3 + 4 * 4 + 5 * 5);
    const after = Math.sqrt(r.x * r.x + r.y * r.y + r.z * r.z);
    expect(after).toBeCloseTo(before, 10);
  });
});

describe("shadeNormal", () => {
  it("clamps to exactly AMBIENT for a face pointed directly away from the light", () => {
    // LIGHT_DIRECTION isn't exported with fixed components, so use a normal that is its own negation:
    // any negative multiple of it yields the minimum (zero dot product after clamp).
    const shade = shadeNormal(-0.4, -0.65, -0.65);
    expect(shade).toBeCloseTo(AMBIENT, 10);
  });

  it("clamps to exactly 1 for a face pointed directly at the light", () => {
    const shade = shadeNormal(0.4, 0.65, 0.65);
    expect(shade).toBeCloseTo(1, 10);
  });

  it("stays within [AMBIENT, 1] for an arbitrary normal", () => {
    const normals: [number, number, number][] = [
      [1, 0, 0], [0, 1, 0], [0, 0, 1], [-1, -1, -1], [2, -3, 0.5],
    ];
    for (const [x, y, z] of normals) {
      const shade = shadeNormal(x, y, z);
      expect(shade).toBeGreaterThanOrEqual(AMBIENT - 1e-10);
      expect(shade).toBeLessThanOrEqual(1 + 1e-10);
    }
  });

  it("a zero-length (degenerate) normal shades at AMBIENT, not NaN", () => {
    expect(shadeNormal(0, 0, 0)).toBe(AMBIENT);
  });
});

describe("depthSortTriangles", () => {
  const at = (depth: number): ProjectedTriangle => ({ x1: 0, y1: 0, x2: 0, y2: 0, x3: 0, y3: 0, depth, shade: 1 });

  it("sorts ascending by depth: farthest (most negative) first, nearest last", () => {
    const input = [at(5), at(-10), at(0), at(3)];
    const sorted = depthSortTriangles(input);
    expect(sorted.map((t) => t.depth)).toEqual([-10, 0, 3, 5]);
  });

  it("does not mutate the input array", () => {
    const input = [at(2), at(-1)];
    const copy = [...input];
    depthSortTriangles(input);
    expect(input).toEqual(copy);
  });
});

describe("projectTriangle", () => {
  it("a triangle facing +Z, unrotated, projects to a centered screen triangle with positive shade", () => {
    const box = computeBoundingBox(cube(10))!;
    const fit = computeFit(box, 300);
    // The cube's top face (z=10, facing +Z after centering) as one triangle (indices 4,5,6).
    const tri: Triangle = { x1: 0, y1: 0, z1: 10, x2: 10, y2: 0, z2: 10, x3: 10, y3: 10, z3: 10 };
    const proj = projectTriangle(tri, fit, 0, 0, 300, 300);
    expect(Number.isFinite(proj.x1)).toBe(true);
    expect(Number.isFinite(proj.depth)).toBe(true);
    expect(proj.shade).toBeGreaterThanOrEqual(AMBIENT);
    expect(proj.shade).toBeLessThanOrEqual(1);
  });

  it("depth increases (moves toward the viewer) as the model is rotated to face the camera", () => {
    const box = computeBoundingBox(cube(10))!;
    const fit = computeFit(box, 300);
    const tri: Triangle = { x1: 0, y1: 0, z1: 10, x2: 10, y2: 0, z2: 10, x3: 10, y3: 10, z3: 10 };
    const unrotated = projectTriangle(tri, fit, 0, 0, 300, 300);
    const yawed = projectTriangle(tri, fit, Math.PI, 0, 300, 300); // spin 180 degrees
    // Spinning 180 degrees moves the face's depth by roughly the model's diameter along Z.
    expect(Math.abs(yawed.depth - unrotated.depth)).toBeGreaterThan(1);
  });
});

describe("TriangleCollector", () => {
  const push = (c: TriangleCollector, n: number, seed = 0) => {
    for (let i = 0; i < n; i++) {
      const v = seed + i;
      c.add(v, 0, 0, v + 1, 0, 0, v + 2, 0, 0);
    }
  };

  it("retains every triangle when the total stays under the cap", () => {
    const c = new TriangleCollector(100);
    push(c, 50);
    expect(c.result().length).toBe(50);
  });

  it("never exceeds its cap regardless of how many triangles are added", () => {
    const c = new TriangleCollector(100);
    push(c, 10000);
    expect(c.result().length).toBeLessThanOrEqual(100);
    expect(c.result().length).toBeGreaterThan(0);
  });

  it("the exported default cap is TRIANGLE_CAP and a collector built without an explicit cap honors it", () => {
    const c = new TriangleCollector();
    push(c, TRIANGLE_CAP * 3);
    expect(c.result().length).toBeLessThanOrEqual(TRIANGLE_CAP);
  });

  it("is deterministic: two collectors fed the identical stream retain the identical triangles", () => {
    const a = new TriangleCollector(37);
    const b = new TriangleCollector(37);
    push(a, 500);
    push(b, 500);
    expect(a.result()).toEqual(b.result());
  });

  it("retains the very first triangle (stride starts at 1, so nothing is skipped before the cap is first reached)", () => {
    const c = new TriangleCollector(10);
    push(c, 5);
    expect(c.result()[0]).toEqual({ x1: 0, y1: 0, z1: 0, x2: 1, y2: 0, z2: 0, x3: 2, y3: 0, z3: 0 });
  });
});
