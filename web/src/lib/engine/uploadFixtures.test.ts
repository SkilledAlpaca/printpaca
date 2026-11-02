import { createReadStream, readFileSync } from "node:fs";
import { createGunzip } from "node:zlib";
import { resolve } from "node:path";
import { describe, it, expect } from "vitest";
import { parseGcodeStream } from "./gcode";
import { parseStl } from "./stl";
import { parseThreeMf } from "./threeMf";

// Canonical uploader example files (testdata/uploads/, provenance in its README) with a frozen
// expected-stats snapshot each; a moved number is a deliberate recalibration or a regression.
const UPLOADS_DIR = resolve(__dirname, "../../../../testdata/uploads");

  /** Cube.gcode is stored gzipped like the server's uploads; decompress and stream it to parseGcodeStream. */
function gunzippedTextChunks(name: string): AsyncIterable<string> {
  const gunzip = createGunzip();
  createReadStream(resolve(UPLOADS_DIR, name)).pipe(gunzip);
  gunzip.setEncoding("utf8");
  return gunzip;
}

function readArrayBuffer(name: string): ArrayBuffer {
  const buf = readFileSync(resolve(UPLOADS_DIR, name));
  // Slice out exactly this file's bytes; buf.buffer is the whole shared pool.
  return buf.buffer.slice(buf.byteOffset, buf.byteOffset + buf.byteLength);
}

describe("testdata/uploads: g-code (gzipped on disk)", () => {
  it("Cube.gcode.gz: slicer-reported stats", async () => {
    const meta = await parseGcodeStream(gunzippedTextChunks("Cube.gcode.gz"));
    expect(meta.weightG).toBeCloseTo(36.34, 5);
    expect(meta.lengthM).toBeCloseTo(12.18461, 4);
    expect(meta.timeSeconds).toBe(3476);
    expect(meta.costEstimate).toBeCloseTo(0.73, 5);
    expect(meta.slicer).toContain("OrcaSlicer");
  });
});

describe("testdata/uploads: STL", () => {
  it("Cube.stl: 12 triangles, 125 cm3 (a 50mm test cube)", () => {
    const r = parseStl(readArrayBuffer("Cube.stl"));
    expect(r.status).toBe("ok");
    expect(r.triangleCount).toBe(12);
    expect(r.volumeCm3).toBeCloseTo(125, 5);
  });
});

describe("testdata/uploads: 3MF", () => {
  it("Cube.3mf: geometry-only project file, 12 triangles", async () => {
    const r = await parseThreeMf(readArrayBuffer("Cube.3mf"));
    expect(r.status).toBe("ok");
    if (r.status !== "ok") return;
    expect(r.kind).toBe("geometry");
    if (r.kind !== "geometry") return;
    expect(r.triangleCount).toBe(12);
    expect(r.volumeCm3).toBeCloseTo(19.683, 5);
  });
});
