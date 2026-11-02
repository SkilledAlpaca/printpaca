import { describe, it, expect } from "vitest";
import { parseStl } from "./stl";

// A 10mm axis-aligned cube: 1000 mm3 = 1 cm3. Twelve triangles wound outward, so the signed sum is +1000.
const V0: [number, number, number] = [0, 0, 0];
const V1: [number, number, number] = [10, 0, 0];
const V2: [number, number, number] = [10, 10, 0];
const V3: [number, number, number] = [0, 10, 0];
const V4: [number, number, number] = [0, 0, 10];
const V5: [number, number, number] = [10, 0, 10];
const V6: [number, number, number] = [10, 10, 10];
const V7: [number, number, number] = [0, 10, 10];

const CUBE_TRIANGLES: [
  [number, number, number],
  [number, number, number],
  [number, number, number],
][] = [
  [V0, V3, V2],
  [V0, V2, V1],
  [V4, V5, V6],
  [V4, V6, V7],
  [V0, V1, V5],
  [V0, V5, V4],
  [V3, V7, V6],
  [V3, V6, V2],
  [V0, V4, V7],
  [V0, V7, V3],
  [V1, V2, V6],
  [V1, V6, V5],
];

function buildBinaryStl(triangles: typeof CUBE_TRIANGLES): ArrayBuffer {
  const bytes = new ArrayBuffer(84 + triangles.length * 50);
  const view = new DataView(bytes);
  view.setUint32(80, triangles.length, true);
  let offset = 84;
  for (const [a, b, c] of triangles) {
    offset += 12; // normal, left zeroed
    for (const [x, y, z] of [a, b, c]) {
      view.setFloat32(offset, x, true);
      view.setFloat32(offset + 4, y, true);
      view.setFloat32(offset + 8, z, true);
      offset += 12;
    }
    offset += 2; // attribute byte count, left zeroed
  }
  return bytes;
}

function buildAsciiStl(triangles: typeof CUBE_TRIANGLES): ArrayBuffer {
  const lines = ["solid cube"];
  for (const [a, b, c] of triangles) {
    lines.push("  facet normal 0 0 0", "    outer loop");
    for (const [x, y, z] of [a, b, c]) lines.push(`      vertex ${x} ${y} ${z}`);
    lines.push("    endloop", "  endfacet");
  }
  lines.push("endsolid cube");
  return new TextEncoder().encode(lines.join("\n")).buffer as ArrayBuffer;
}

describe("parseStl: binary", () => {
  it("parses a 10mm cube to 1 cm3 across 12 triangles", () => {
    const r = parseStl(buildBinaryStl(CUBE_TRIANGLES));
    expect(r.status).toBe("ok");
    expect(r.volumeCm3).toBeCloseTo(1, 5);
    expect(r.triangleCount).toBe(12);
  });

  it("a zero-triangle header is empty, not a zero-volume ok", () => {
    const r = parseStl(buildBinaryStl([]));
    expect(r.status).toBe("empty");
    expect(r.volumeCm3).toBeUndefined();
  });

  it("truncated binary (declared count doesn't fit the byte length) falls through to ASCII and fails as malformed", () => {
    const full = buildBinaryStl(CUBE_TRIANGLES);
    const truncated = full.slice(0, full.byteLength - 10);
    const r = parseStl(truncated);
    expect(r.status).toBe("malformed");
  });

  it("a binary file whose header text happens to start with 'solid' is still read as binary via the size check", () => {
    const bytes = buildBinaryStl(CUBE_TRIANGLES);
    const view = new Uint8Array(bytes);
    const prefix = new TextEncoder().encode("solid ");
    view.set(prefix, 0); // overwrite the first bytes of the 80-byte header
    const r = parseStl(bytes);
    expect(r.status).toBe("ok");
    expect(r.triangleCount).toBe(12);
    expect(r.volumeCm3).toBeCloseTo(1, 5);
  });
});

describe("parseStl: ASCII", () => {
  it("parses a 10mm cube to 1 cm3 across 12 triangles", () => {
    const r = parseStl(buildAsciiStl(CUBE_TRIANGLES));
    expect(r.status).toBe("ok");
    expect(r.volumeCm3).toBeCloseTo(1, 5);
    expect(r.triangleCount).toBe(12);
  });

  it("an empty solid (no facets) is empty, not a zero-volume ok", () => {
    const empty = new TextEncoder().encode("solid nothing\nendsolid nothing\n").buffer as ArrayBuffer;
    const r = parseStl(empty);
    expect(r.status).toBe("empty");
  });

  it("non-numeric vertex data is malformed", () => {
    const bad = new TextEncoder().encode(
      "solid bad\n  facet normal 0 0 0\n    outer loop\n      vertex NaN 0 0\n" +
        "      vertex 1 0 0\n      vertex 0 1 0\n    endloop\n  endfacet\nendsolid bad\n",
    ).buffer as ArrayBuffer;
    expect(parseStl(bad).status).toBe("malformed");
  });
});

describe("parseStl: malformed input", () => {
  it("non-STL bytes are malformed, never throw", () => {
    const junk = new TextEncoder().encode("this is not an STL file at all").buffer as ArrayBuffer;
    expect(() => parseStl(junk)).not.toThrow();
    expect(parseStl(junk).status).toBe("malformed");
  });

  it("a short truncated buffer is malformed, never throws", () => {
    const short = new ArrayBuffer(10);
    expect(() => parseStl(short)).not.toThrow();
    expect(parseStl(short).status).toBe("malformed");
  });
});

describe("parseStl: onTriangle sink", () => {
  it("binary: sink receives exactly the mesh's 12 triangles, each vertex matching the source cube", () => {
    const seen: number[][] = [];
    const r = parseStl(buildBinaryStl(CUBE_TRIANGLES), {
      onTriangle: (x1, y1, z1, x2, y2, z2, x3, y3, z3) => seen.push([x1, y1, z1, x2, y2, z2, x3, y3, z3]),
    });
    expect(r.status).toBe("ok");
    expect(seen.length).toBe(12);
    seen.forEach((tri, i) => {
      const [a, b, c] = CUBE_TRIANGLES[i]!;
      expect(tri).toEqual([...a, ...b, ...c]);
    });
  });

  it("ASCII: sink receives exactly the mesh's 12 triangles", () => {
    const seen: number[][] = [];
    const r = parseStl(buildAsciiStl(CUBE_TRIANGLES), {
      onTriangle: (x1, y1, z1, x2, y2, z2, x3, y3, z3) => seen.push([x1, y1, z1, x2, y2, z2, x3, y3, z3]),
    });
    expect(r.status).toBe("ok");
    expect(seen.length).toBe(12);
  });

  it("an absent sink changes nothing: same status/volume/triangleCount as calling parseStl with no second argument", () => {
    const withOpts = parseStl(buildBinaryStl(CUBE_TRIANGLES), {});
    const withoutOpts = parseStl(buildBinaryStl(CUBE_TRIANGLES));
    expect(withOpts).toEqual(withoutOpts);
  });

  it("a malformed file never invokes the sink for the triangle that failed the finite check", () => {
    const bytes = buildBinaryStl(CUBE_TRIANGLES);
    const view = new DataView(bytes);
    view.setFloat32(84 + 12, NaN, true); // first vertex's x of the first triangle
    const seen: number[][] = [];
    const r = parseStl(bytes, { onTriangle: (...tri) => seen.push(tri) });
    expect(r.status).toBe("malformed");
    expect(seen.length).toBe(0); // the bad triangle short-circuits before any sink call
  });
});

describe("parseStl: non-finite coordinates never reach an 'ok' NaN volume", () => {
  it("a binary vertex holding Infinity is malformed, not ok with a NaN/Infinity volume", () => {
    const bytes = buildBinaryStl(CUBE_TRIANGLES);
    const view = new DataView(bytes);
    view.setFloat32(84 + 12, Infinity, true); // first vertex's x of the first triangle
    const r = parseStl(bytes);
    expect(r.status).toBe("malformed");
    expect(r.volumeCm3).toBeUndefined();
  });

  it("a binary vertex holding NaN is malformed", () => {
    const bytes = buildBinaryStl(CUBE_TRIANGLES);
    const view = new DataView(bytes);
    view.setFloat32(84 + 12, NaN, true);
    const r = parseStl(bytes);
    expect(r.status).toBe("malformed");
  });

  it("ASCII coordinates that individually parse as finite but overflow the volume sum to Infinity are malformed", () => {
    // 1e300 cubed overflows to Infinity.
    const bad = new TextEncoder().encode(
      "solid huge\n  facet normal 0 0 0\n    outer loop\n      vertex 1e300 0 0\n" +
        "      vertex 0 1e300 0\n      vertex 0 0 1e300\n    endloop\n  endfacet\nendsolid huge\n",
    ).buffer as ArrayBuffer;
    const r = parseStl(bad);
    expect(r.status).toBe("malformed");
    expect(r.volumeCm3).toBeUndefined();
  });
});
