export interface StlResult {
  status: "ok" | "empty" | "malformed";
  volumeCm3?: number;
  triangleCount?: number;
}

/** Optional triangle sink shared by the binary and ASCII parsers and threeMf.ts's geometry walk.
 * Coordinates are in millimeters, unit-scaled by the caller. */
export interface StlParseOptions {
  onTriangle?: (
    x1: number, y1: number, z1: number,
    x2: number, y2: number, z2: number,
    x3: number, y3: number, z3: number,
  ) => void;
}

const BINARY_HEADER_BYTES = 80;
const BINARY_MIN_BYTES = 84; // header + count
const BINARY_TRIANGLE_BYTES = 50;

/** Six times one triangle's signed tetrahedron term (divergence theorem), in mm. The single source of this
 * arithmetic so every parse path produces bit-identical volumes. */
export function tetrahedronTermX6(
  x1: number, y1: number, z1: number,
  x2: number, y2: number, z2: number,
  x3: number, y3: number, z3: number,
): number {
  return x1 * (y2 * z3 - y3 * z2) - y1 * (x2 * z3 - x3 * z2) + z1 * (x2 * y3 - x3 * y2);
}

/** mm3 sum (signed, already divided by 6) to cm3. Sign depends on winding, so callers take abs. */
export function volumeCm3FromSignedSumX6(sumX6: number): number {
  return Math.abs(sumX6 / 6) / 1000;
}

/** Binary if the header's triangle count exactly accounts for the file length; a "solid" prefix is not
 * checked because some binary exporters write one. Returns null so the caller can fall back to ASCII. */
function tryParseBinary(data: ArrayBuffer, opts?: StlParseOptions): StlResult | null {
  if (data.byteLength < BINARY_MIN_BYTES) return null;
  const view = new DataView(data);
  const count = view.getUint32(BINARY_HEADER_BYTES, true);
  if (BINARY_MIN_BYTES + count * BINARY_TRIANGLE_BYTES !== data.byteLength) return null;
  if (count === 0) return { status: "empty" };

  // Accumulate straight from the DataView; copying vertices first would cost ~90 MB at the 64 MiB
  // MAX_MODEL_BYTES cap.
  let offset = BINARY_MIN_BYTES;
  let sumX6 = 0;
  const v = new Float64Array(9); // one triangle's 3 vertices, reused
  for (let i = 0; i < count; i++) {
    offset += 12; // facet normal, unused for volume
    for (let p = 0; p < 3; p++) {
      const x = view.getFloat32(offset, true);
      const y = view.getFloat32(offset + 4, true);
      const z = view.getFloat32(offset + 8, true);
      if (!Number.isFinite(x) || !Number.isFinite(y) || !Number.isFinite(z)) return { status: "malformed" };
      v[p * 3] = x;
      v[p * 3 + 1] = y;
      v[p * 3 + 2] = z;
      offset += 12;
    }
    offset += 2; // attribute byte count, unused
    sumX6 += tetrahedronTermX6(v[0]!, v[1]!, v[2]!, v[3]!, v[4]!, v[5]!, v[6]!, v[7]!, v[8]!);
    opts?.onTriangle?.(v[0]!, v[1]!, v[2]!, v[3]!, v[4]!, v[5]!, v[6]!, v[7]!, v[8]!);
  }
  const volumeCm3 = volumeCm3FromSignedSumX6(sumX6);
  if (!Number.isFinite(volumeCm3)) return { status: "malformed" }; // finite vertices that still overflow the sum
  return { status: "ok", volumeCm3, triangleCount: count };
}

const ASCII_VERTEX = /vertex\s+(-?[\d.]+(?:[eE][-+]?\d+)?)\s+(-?[\d.]+(?:[eE][-+]?\d+)?)\s+(-?[\d.]+(?:[eE][-+]?\d+)?)/g;

function parseAscii(data: ArrayBuffer, opts?: StlParseOptions): StlResult {
  const text = new TextDecoder().decode(data);
  if (!/solid|facet/i.test(text)) return { status: "malformed" }; // not recognizable as STL at all

  // Same inline accumulation. A vertex count that isn't a multiple of 3 must still fail "malformed", so it is
  // tracked separately from `v`.
  let sumX6 = 0;
  let vertexCount = 0;
  const v = new Float64Array(9);
  for (const m of text.matchAll(ASCII_VERTEX)) {
    const x = Number.parseFloat(m[1]!);
    const y = Number.parseFloat(m[2]!);
    const z = Number.parseFloat(m[3]!);
    if (!Number.isFinite(x) || !Number.isFinite(y) || !Number.isFinite(z)) return { status: "malformed" };
    const slot = vertexCount % 3;
    v[slot * 3] = x;
    v[slot * 3 + 1] = y;
    v[slot * 3 + 2] = z;
    vertexCount++;
    if (slot === 2) {
      sumX6 += tetrahedronTermX6(v[0]!, v[1]!, v[2]!, v[3]!, v[4]!, v[5]!, v[6]!, v[7]!, v[8]!);
      opts?.onTriangle?.(v[0]!, v[1]!, v[2]!, v[3]!, v[4]!, v[5]!, v[6]!, v[7]!, v[8]!);
    }
  }
  if (vertexCount === 0) return { status: "empty" };
  if (vertexCount % 3 !== 0) return { status: "malformed" }; // vertices don't group into whole triangles
  const volumeCm3 = volumeCm3FromSignedSumX6(sumX6);
  if (!Number.isFinite(volumeCm3)) return { status: "malformed" }; // finite vertices that still overflow the sum
  return { status: "ok", volumeCm3, triangleCount: vertexCount / 3 };
}

export function parseStl(data: ArrayBuffer, opts?: StlParseOptions): StlResult {
  return tryParseBinary(data, opts) ?? parseAscii(data, opts);
}
