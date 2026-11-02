import { describe, it, expect } from "vitest";
import { parseThreeMf } from "./threeMf";

// --- Minimal hand-built ZIP writer (stored and deflate) matching threeMf.ts's reader. CRC32 is 0
// throughout because the reader doesn't validate it. ---

interface ZipEntryInput {
  name: string;
  data: Uint8Array;
  method?: 0 | 8;
}

async function deflateRaw(data: Uint8Array): Promise<Uint8Array> {
  // TS 5.9 types Uint8Array as ArrayBufferLike-backed, which BlobPart rejects; this data is never shared.
  const stream = new Blob([data as BlobPart]).stream().pipeThrough(new CompressionStream("deflate-raw"));
  return new Uint8Array(await new Response(stream).arrayBuffer());
}

async function buildZip(entries: ZipEntryInput[]): Promise<ArrayBuffer> {
  const encoder = new TextEncoder();
  const localParts: Uint8Array[] = [];
  const centralParts: Uint8Array[] = [];
  let offset = 0;

  for (const entry of entries) {
    const method = entry.method ?? 0;
    const nameBytes = encoder.encode(entry.name);
    const stored = method === 0 ? entry.data : await deflateRaw(entry.data);

    const local = new Uint8Array(30 + nameBytes.length + stored.length);
    const lv = new DataView(local.buffer);
    lv.setUint32(0, 0x04034b50, true);
    lv.setUint16(4, 20, true);
    lv.setUint16(6, 0, true);
    lv.setUint16(8, method, true);
    lv.setUint16(10, 0, true);
    lv.setUint16(12, 0, true);
    lv.setUint32(14, 0, true);
    lv.setUint32(18, stored.length, true);
    lv.setUint32(22, entry.data.length, true);
    lv.setUint16(26, nameBytes.length, true);
    lv.setUint16(28, 0, true);
    local.set(nameBytes, 30);
    local.set(stored, 30 + nameBytes.length);
    localParts.push(local);

    const central = new Uint8Array(46 + nameBytes.length);
    const cv = new DataView(central.buffer);
    cv.setUint32(0, 0x02014b50, true);
    cv.setUint16(4, 20, true);
    cv.setUint16(6, 20, true);
    cv.setUint16(8, 0, true);
    cv.setUint16(10, method, true);
    cv.setUint16(12, 0, true);
    cv.setUint16(14, 0, true);
    cv.setUint32(16, 0, true);
    cv.setUint32(20, stored.length, true);
    cv.setUint32(24, entry.data.length, true);
    cv.setUint16(28, nameBytes.length, true);
    cv.setUint16(30, 0, true);
    cv.setUint16(32, 0, true);
    cv.setUint16(34, 0, true);
    cv.setUint16(36, 0, true);
    cv.setUint32(38, 0, true);
    cv.setUint32(42, offset, true);
    central.set(nameBytes, 46);
    centralParts.push(central);

    offset += local.length;
  }

  const centralDirOffset = offset;
  const centralDirSize = centralParts.reduce((sum, c) => sum + c.length, 0);

  const eocd = new Uint8Array(22);
  const ev = new DataView(eocd.buffer);
  ev.setUint32(0, 0x06054b50, true);
  ev.setUint16(8, entries.length, true);
  ev.setUint16(10, entries.length, true);
  ev.setUint32(12, centralDirSize, true);
  ev.setUint32(16, centralDirOffset, true);

  const total = new Uint8Array(centralDirOffset + centralDirSize + eocd.length);
  let pos = 0;
  for (const l of localParts) {
    total.set(l, pos);
    pos += l.length;
  }
  for (const c of centralParts) {
    total.set(c, pos);
    pos += c.length;
  }
  total.set(eocd, pos);
  return total.buffer;
}

const utf8 = (s: string) => new TextEncoder().encode(s);

async function parseSliceConfig(config: string, method?: 0 | 8) {
  const entry: ZipEntryInput = { name: "Metadata/slice_info.config", data: utf8(config) };
  if (method !== undefined) entry.method = method;
  return parseThreeMf(await buildZip([entry]));
}

// Same 10mm cube as stl.test.ts (1 cm3), as indexed 3MF geometry.
const CUBE_MODEL_XML = `<?xml version="1.0" encoding="UTF-8"?>
<model unit="millimeter" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02">
  <resources>
    <object id="1" type="model">
      <mesh>
        <vertices>
          <vertex x="0" y="0" z="0"/>
          <vertex x="10" y="0" z="0"/>
          <vertex x="10" y="10" z="0"/>
          <vertex x="0" y="10" z="0"/>
          <vertex x="0" y="0" z="10"/>
          <vertex x="10" y="0" z="10"/>
          <vertex x="10" y="10" z="10"/>
          <vertex x="0" y="10" z="10"/>
        </vertices>
        <triangles>
          <triangle v1="0" v2="3" v3="2"/>
          <triangle v1="0" v2="2" v3="1"/>
          <triangle v1="4" v2="5" v3="6"/>
          <triangle v1="4" v2="6" v3="7"/>
          <triangle v1="0" v2="1" v3="5"/>
          <triangle v1="0" v2="5" v3="4"/>
          <triangle v1="3" v2="7" v3="6"/>
          <triangle v1="3" v2="6" v3="2"/>
          <triangle v1="0" v2="4" v3="7"/>
          <triangle v1="0" v2="7" v3="3"/>
          <triangle v1="1" v2="2" v3="6"/>
          <triangle v1="1" v2="6" v3="5"/>
        </triangles>
      </mesh>
    </object>
  </resources>
  <build>
    <item objectid="1"/>
  </build>
</model>`;

// The same cube topology parameterized by side length, so the cache-taint tests get parts with distinct
// volumes and a coincidental cancellation can't hide an order-dependence bug.
const cubeMeshXml = (sideMm: number) => `
      <mesh>
        <vertices>
          <vertex x="0" y="0" z="0"/>
          <vertex x="${sideMm}" y="0" z="0"/>
          <vertex x="${sideMm}" y="${sideMm}" z="0"/>
          <vertex x="0" y="${sideMm}" z="0"/>
          <vertex x="0" y="0" z="${sideMm}"/>
          <vertex x="${sideMm}" y="0" z="${sideMm}"/>
          <vertex x="${sideMm}" y="${sideMm}" z="${sideMm}"/>
          <vertex x="0" y="${sideMm}" z="${sideMm}"/>
        </vertices>
        <triangles>
          <triangle v1="0" v2="3" v3="2"/>
          <triangle v1="0" v2="2" v3="1"/>
          <triangle v1="4" v2="5" v3="6"/>
          <triangle v1="4" v2="6" v3="7"/>
          <triangle v1="0" v2="1" v3="5"/>
          <triangle v1="0" v2="5" v3="4"/>
          <triangle v1="3" v2="7" v3="6"/>
          <triangle v1="3" v2="6" v3="2"/>
          <triangle v1="0" v2="4" v3="7"/>
          <triangle v1="0" v2="7" v3="3"/>
          <triangle v1="1" v2="2" v3="6"/>
          <triangle v1="1" v2="6" v3="5"/>
        </triangles>
      </mesh>`;

describe("parseThreeMf: sliced metadata (slice_info.config)", () => {
  it("reads prediction (seconds) and weight (grams) as exact sliced values", async () => {
    const config = `<?xml version="1.0" encoding="UTF-8"?>
<config>
  <plate>
    <metadata key="index" value="1"/>
    <metadata key="prediction" value="3600"/>
    <metadata key="weight" value="15.5"/>
  </plate>
</config>`;
    const r = await parseSliceConfig(config);
    expect(r.status).toBe("ok");
    expect(r.kind).toBe("sliced");
    expect(r.meta?.timeSeconds).toBe(3600);
    expect(r.meta?.weightG).toBeCloseTo(15.5, 5);
    expect(r.meta?.slicer).toBeTruthy();
  });

  it.each([
    [
      "tolerates attribute order (value before key) and a non-self-closing tag",
      `<config><plate><metadata value="7.25" key="weight"></metadata></plate></config>`,
      undefined,
      7.25,
    ],
    [
      "falls back to summed used_g when the weight key is absent",
      `<config><plate>
      <filament id="1" used_g="10.5"/>
      <filament id="2" used_g="4.25"/>
    </plate></config>`,
      undefined,
      14.75,
    ],
    [
      "reads a deflate-compressed slice_info.config entry",
      `<config><plate><metadata key="weight" value="9.9"/></plate></config>`,
      8,
      9.9,
    ],
  ] as const)("%s", async (_name, config, method, weightG) => {
    const r = await parseSliceConfig(config, method);
    expect(r.status).toBe("ok");
    expect(r.kind).toBe("sliced");
    expect(r.meta?.weightG).toBeCloseTo(weightG, 5);
  });
});

describe("parseThreeMf: embedded g-code fallback", () => {
  it("runs the shared g-code parser over Metadata/plate_1.gcode when slice_info.config is absent", async () => {
    const gcode = `; generated by PrusaSlicer 2.8.0\n; filament used [g] = 10.31\n; estimated printing time (normal mode) = 1h 56m 0s\nG28\n`;
    const zip = await buildZip([{ name: "Metadata/plate_1.gcode", data: utf8(gcode) }]);
    const r = await parseThreeMf(zip);
    expect(r.status).toBe("ok");
    expect(r.kind).toBe("sliced");
    expect(r.meta?.weightG).toBeCloseTo(10.31, 5);
    expect(r.meta?.timeSeconds).toBe(1 * 3600 + 56 * 60);
  });

  it("falls through from an unusable slice_info.config to the embedded g-code entry", async () => {
    const config = `<config><plate><metadata key="index" value="1"/></plate></config>`; // no prediction/weight
    const gcode = `; filament used [g] = 5.0\nG28\n`;
    const zip = await buildZip([
      { name: "Metadata/slice_info.config", data: utf8(config) },
      { name: "Metadata/plate_1.gcode", data: utf8(gcode) },
    ]);
    const r = await parseThreeMf(zip);
    expect(r.status).toBe("ok");
    expect(r.kind).toBe("sliced");
    expect(r.meta?.weightG).toBeCloseTo(5.0, 5);
  });
});

describe("parseThreeMf: geometry fallback (3D/3dmodel.model)", () => {
  it("computes volume from indexed vertices/triangles when no slicer metadata exists", async () => {
    const zip = await buildZip([{ name: "3D/3dmodel.model", data: utf8(CUBE_MODEL_XML) }]);
    const r = await parseThreeMf(zip);
    expect(r.status).toBe("ok");
    expect(r.kind).toBe("geometry");
    expect(r.volumeCm3).toBeCloseTo(1, 5);
    expect(r.triangleCount).toBe(12);
  });

  it("honors a non-millimeter unit attribute (a 1x1x1 'centimeter' cube -> 1 cm3, same as a 10mm cube)", async () => {
    const xml = `<model unit="centimeter" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02">
  <resources>
    <object id="1" type="model">
      <mesh>
        <vertices>
          <vertex x="0" y="0" z="0"/>
          <vertex x="1" y="0" z="0"/>
          <vertex x="1" y="1" z="0"/>
          <vertex x="0" y="1" z="0"/>
          <vertex x="0" y="0" z="1"/>
          <vertex x="1" y="0" z="1"/>
          <vertex x="1" y="1" z="1"/>
          <vertex x="0" y="1" z="1"/>
        </vertices>
        <triangles>
          <triangle v1="0" v2="3" v3="2"/>
          <triangle v1="0" v2="2" v3="1"/>
          <triangle v1="4" v2="5" v3="6"/>
          <triangle v1="4" v2="6" v3="7"/>
          <triangle v1="0" v2="1" v3="5"/>
          <triangle v1="0" v2="5" v3="4"/>
          <triangle v1="3" v2="7" v3="6"/>
          <triangle v1="3" v2="6" v3="2"/>
          <triangle v1="0" v2="4" v3="7"/>
          <triangle v1="0" v2="7" v3="3"/>
          <triangle v1="1" v2="2" v3="6"/>
          <triangle v1="1" v2="6" v3="5"/>
        </triangles>
      </mesh>
    </object>
  </resources>
</model>`;
    const zip = await buildZip([{ name: "3D/3dmodel.model", data: utf8(xml) }]);
    const r = await parseThreeMf(zip);
    expect(r.status).toBe("ok");
    expect(r.kind).toBe("geometry");
    expect(r.volumeCm3).toBeCloseTo(1, 5);
  });

  it("an object with vertices but no triangles is empty, not zero-volume ok", async () => {
    const xml = `<model unit="millimeter"><resources><object id="1"><mesh>
      <vertices><vertex x="0" y="0" z="0"/></vertices>
      <triangles></triangles>
    </mesh></object></resources></model>`;
    const zip = await buildZip([{ name: "3D/3dmodel.model", data: utf8(xml) }]);
    const r = await parseThreeMf(zip);
    expect(r.status).toBe("empty");
  });

  it("a non-finite vertex coordinate is skipped like an out-of-range index, not passed through as an NaN ok volume", async () => {
    // Vertex 0's x is unparsable; the 6 triangles referencing it are skipped, leaving half the cube.
    const xml = CUBE_MODEL_XML.replace('<vertex x="0" y="0" z="0"/>', '<vertex x="oops" y="0" z="0"/>');
    const zip = await buildZip([{ name: "3D/3dmodel.model", data: utf8(xml) }]);
    const r = await parseThreeMf(zip);
    expect(r.status).toBe("ok");
    expect(r.kind).toBe("geometry");
    expect(r.triangleCount).toBe(6);
    expect(Number.isFinite(r.volumeCm3)).toBe(true);
  });

  it("a malformed <vertex> tag missing its closing '>' under-fills the vertex array, and referencing triangles are skipped rather than pinned to (0,0,0)", async () => {
    // Drop the closing "/>" from vertex 6's tag. The fill loop's VERTEX_TAG then consumes vertex 6 and 7 as
    // one match, so vertex 7's slot is never written. Triangles #4, #7, #9, #10 reference vertex 7 and must
    // be skipped, not computed against an unwritten (0,0,0).
    const xml = CUBE_MODEL_XML.replace(
      '<vertex x="10" y="10" z="10"/>\n          <vertex x="0" y="10" z="10"/>',
      '<vertex x="10" y="10" z="10"\n          <vertex x="0" y="10" z="10"/>',
    );
    expect(xml).not.toBe(CUBE_MODEL_XML); // guard against the replace silently no-op'ing
    const zip = await buildZip([{ name: "3D/3dmodel.model", data: utf8(xml) }]);
    const r = await parseThreeMf(zip);
    expect(r.status).toBe("ok");
    expect(r.kind).toBe("geometry");
    expect(r.triangleCount).toBe(8);
    expect(Number.isFinite(r.volumeCm3)).toBe(true);
  });

  it("a mesh whose scaled coordinates overflow the volume sum to Infinity is malformed, not an ok NaN/Infinity volume", async () => {
    // A "meter" scale (x1000) on huge coordinates pushes the triangle-sum product to Infinity.
    const xml = `<model unit="meter"><resources><object id="1"><mesh>
      <vertices>
        <vertex x="1e100" y="0" z="0"/>
        <vertex x="0" y="1e100" z="0"/>
        <vertex x="0" y="0" z="1e100"/>
      </vertices>
      <triangles><triangle v1="0" v2="1" v3="2"/></triangles>
    </mesh></object></resources></model>`;
    const zip = await buildZip([{ name: "3D/3dmodel.model", data: utf8(xml) }]);
    const r = await parseThreeMf(zip);
    expect(r.status).toBe("malformed");
  });
});

describe("parseThreeMf: onTriangle sink", () => {
  it("geometry path: sink receives exactly the mesh's 12 triangles", async () => {
    const zip = await buildZip([{ name: "3D/3dmodel.model", data: utf8(CUBE_MODEL_XML) }]);
    const seen: number[][] = [];
    const r = await parseThreeMf(zip, {
      onTriangle: (x1, y1, z1, x2, y2, z2, x3, y3, z3) => seen.push([x1, y1, z1, x2, y2, z2, x3, y3, z3]),
    });
    expect(r.status).toBe("ok");
    expect(r.kind).toBe("geometry");
    expect(seen.length).toBe(12);
  });

  it("a part reached through two build-item references still emits its triangles once, not twice", async () => {
    const rootXml = `<?xml version="1.0" encoding="UTF-8"?>
<model unit="millimeter" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02" xmlns:p="http://schemas.microsoft.com/3dmanufacturing/production/2015/06">
  <resources></resources>
  <build>
    <item p:path="/3D/Objects/object_1.model" objectid="1"/>
    <item p:path="/3D/Objects/object_1.model" objectid="1"/>
  </build>
</model>`;
    const zip = await buildZip([
      { name: "3D/3dmodel.model", data: utf8(rootXml) },
      { name: "3D/Objects/object_1.model", data: utf8(CUBE_MODEL_XML) },
    ]);
    const seen: number[][] = [];
    const r = await parseThreeMf(zip, { onTriangle: (...tri) => seen.push(tri) });
    expect(r.status).toBe("ok");
    expect(r.triangleCount).toBe(24); // volume/triangleCount still count both instances
    expect(seen.length).toBe(12); // but the preview sink only sees the part's own mesh once
  });

  it("sliced metadata path (slice_info.config present, no 3D/3dmodel.model entry): the sink is never invoked", async () => {
    const config = `<config><plate><metadata key="weight" value="9.9"/></plate></config>`;
    const zip = await buildZip([{ name: "Metadata/slice_info.config", data: utf8(config) }]);
    const seen: number[][] = [];
    const r = await parseThreeMf(zip, { onTriangle: (...tri) => seen.push(tri) });
    expect(r.status).toBe("ok");
    expect(r.kind).toBe("sliced");
    expect(seen.length).toBe(0);
  });

  it("an absent sink changes nothing: same result as calling parseThreeMf with no second argument", async () => {
    const zip = await buildZip([{ name: "3D/3dmodel.model", data: utf8(CUBE_MODEL_XML) }]);
    const withOpts = await parseThreeMf(zip, {});
    const withoutOpts = await parseThreeMf(zip);
    expect(withOpts).toEqual(withoutOpts);
  });

  it("sliced archive with a 3D/3dmodel.model alongside: a sink IS fed the mesh's triangles as a best-effort preview", async () => {
    const config = `<config><plate><metadata key="weight" value="9.9"/></plate></config>`;
    const zip = await buildZip([
      { name: "Metadata/slice_info.config", data: utf8(config) },
      { name: "3D/3dmodel.model", data: utf8(CUBE_MODEL_XML) },
    ]);
    const seen: number[][] = [];
    const r = await parseThreeMf(zip, { onTriangle: (...tri) => seen.push(tri) });
    expect(r.status).toBe("ok");
    expect(r.kind).toBe("sliced");
    expect(seen.length).toBe(12);
  });

  it("sliced result is byte-identical whether or not a sink is passed: no volumeCm3/triangleCount leak in from the preview walk", async () => {
    const config = `<config><plate><metadata key="prediction" value="3600"/><metadata key="weight" value="15.5"/></plate></config>`;
    const zip = await buildZip([
      { name: "Metadata/slice_info.config", data: utf8(config) },
      { name: "3D/3dmodel.model", data: utf8(CUBE_MODEL_XML) },
    ]);
    const withSink = await parseThreeMf(zip, { onTriangle: () => {} });
    const withoutSink = await parseThreeMf(zip);
    expect(withSink).toEqual(withoutSink);
    expect(withSink).toEqual({
      status: "ok",
      kind: "sliced",
      meta: withSink.meta,
    });
    expect(withSink.volumeCm3).toBeUndefined();
    expect(withSink.triangleCount).toBeUndefined();
  });

  it("sliced archive with no 3D/3dmodel.model entry at all: sink stays empty, sliced result unaffected", async () => {
    const config = `<config><plate><metadata key="weight" value="9.9"/></plate></config>`;
    const zip = await buildZip([{ name: "Metadata/slice_info.config", data: utf8(config) }]);
    const seen: number[][] = [];
    const r = await parseThreeMf(zip, { onTriangle: (...tri) => seen.push(tri) });
    expect(r.status).toBe("ok");
    expect(r.kind).toBe("sliced");
    expect(seen.length).toBe(0);
  });

  it("sliced archive whose 3D/3dmodel.model is corrupt (unparsable, no mesh): sink stays empty, sliced result still returned, never throws", async () => {
    const config = `<config><plate><metadata key="weight" value="9.9"/></plate></config>`;
    const zip = await buildZip([
      { name: "Metadata/slice_info.config", data: utf8(config) },
      { name: "3D/3dmodel.model", data: utf8("not xml at all, and no <mesh> in sight") },
    ]);
    const seen: number[][] = [];
    const r = await parseThreeMf(zip, { onTriangle: (...tri) => seen.push(tri) });
    expect(r.status).toBe("ok");
    expect(r.kind).toBe("sliced");
    expect(r.meta?.weightG).toBeCloseTo(9.9, 5);
    expect(seen.length).toBe(0);
  });

  it("sliced archive whose 3D/3dmodel.model is over MAX_MODEL_GEOMETRY_BYTES: sink stays empty, sliced result still returned, budget still enforced", async () => {
    const config = `<config><plate><metadata key="weight" value="9.9"/></plate></config>`;
    const oversized = new Uint8Array(24 * 1024 * 1024 + 1024).fill(0x41);
    const zip = await buildZip([
      { name: "Metadata/slice_info.config", data: utf8(config) },
      { name: "3D/3dmodel.model", data: oversized },
    ]);
    const seen: number[][] = [];
    const r = await parseThreeMf(zip, { onTriangle: (...tri) => seen.push(tri) });
    expect(r.status).toBe("ok");
    expect(r.kind).toBe("sliced");
    expect(r.meta?.weightG).toBeCloseTo(9.9, 5);
    expect(seen.length).toBe(0);
  });
});

describe("parseThreeMf: Production Extension p:path references", () => {
  it("resolves a component p:path with no inline mesh to the referenced part's geometry (same volume as the equivalent inline-mesh cube)", async () => {
    const rootXml = `<?xml version="1.0" encoding="UTF-8"?>
<model unit="millimeter" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02" xmlns:p="http://schemas.microsoft.com/3dmanufacturing/production/2015/06">
  <resources>
    <object id="1" type="model" p:UUID="00000000-0000-0000-0000-000000000001">
      <components>
        <component p:path="/3D/Objects/object_1.model" objectid="1" p:UUID="00000000-0000-0000-0000-000000000002"/>
      </components>
    </object>
  </resources>
  <build>
    <item objectid="1"/>
  </build>
</model>`;
    const zip = await buildZip([
      { name: "3D/3dmodel.model", data: utf8(rootXml) },
      { name: "3D/Objects/object_1.model", data: utf8(CUBE_MODEL_XML) },
    ]);
    const r = await parseThreeMf(zip);
    expect(r.status).toBe("ok");
    expect(r.kind).toBe("geometry");
    expect(r.volumeCm3).toBeCloseTo(1, 5);
    expect(r.triangleCount).toBe(12);
  });

  it("two <build><item p:path=...> entries referencing the same external part contribute the volume twice, not once", async () => {
      // Two build items place two instances of the same part: a legitimate multi-instance build.
    const rootXml = `<?xml version="1.0" encoding="UTF-8"?>
<model unit="millimeter" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02" xmlns:p="http://schemas.microsoft.com/3dmanufacturing/production/2015/06">
  <resources></resources>
  <build>
    <item p:path="/3D/Objects/object_1.model" objectid="1"/>
    <item p:path="/3D/Objects/object_1.model" objectid="1"/>
  </build>
</model>`;
    const zip = await buildZip([
      { name: "3D/3dmodel.model", data: utf8(rootXml) },
      { name: "3D/Objects/object_1.model", data: utf8(CUBE_MODEL_XML) },
    ]);
    const r = await parseThreeMf(zip);
    expect(r.status).toBe("ok");
    expect(r.volumeCm3).toBeCloseTo(2, 5);
    expect(r.triangleCount).toBe(24);
  });

  it("two <component> refs (from two different objects) to the same external part each contribute their own instance", async () => {
    const rootXml = `<?xml version="1.0" encoding="UTF-8"?>
<model unit="millimeter" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02" xmlns:p="http://schemas.microsoft.com/3dmanufacturing/production/2015/06">
  <resources>
    <object id="1" type="model" p:UUID="00000000-0000-0000-0000-000000000001">
      <components>
        <component p:path="/3D/Objects/object_1.model" objectid="1" p:UUID="00000000-0000-0000-0000-000000000002"/>
      </components>
    </object>
    <object id="2" type="model" p:UUID="00000000-0000-0000-0000-000000000003">
      <components>
        <component p:path="/3D/Objects/object_1.model" objectid="1" p:UUID="00000000-0000-0000-0000-000000000002"/>
      </components>
    </object>
  </resources>
  <build>
    <item objectid="1"/>
    <item objectid="2"/>
  </build>
</model>`;
    const zip = await buildZip([
      { name: "3D/3dmodel.model", data: utf8(rootXml) },
      { name: "3D/Objects/object_1.model", data: utf8(CUBE_MODEL_XML) },
    ]);
    const r = await parseThreeMf(zip);
    expect(r.status).toBe("ok");
    expect(r.volumeCm3).toBeCloseTo(2, 5);
    expect(r.triangleCount).toBe(24);
  });

  it("a genuine reference cycle (A -> B -> A) terminates instead of recursing forever, and still counts each file's own mesh once", async () => {
    // A references B and B references back to A; the ancestor guard stops the second hop into A without
    // dropping A's or B's contribution.
    const aXml = `<?xml version="1.0" encoding="UTF-8"?>
<model unit="millimeter" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02" xmlns:p="http://schemas.microsoft.com/3dmanufacturing/production/2015/06">
  <resources>
    <object id="1" type="model">
      <components>
        <component p:path="/3D/Objects/b.model" objectid="1"/>
      </components>
    </object>
  </resources>
  <build>
    <item objectid="1"/>
  </build>
</model>`;
    const bXml = `<?xml version="1.0" encoding="UTF-8"?>
<model unit="millimeter" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02" xmlns:p="http://schemas.microsoft.com/3dmanufacturing/production/2015/06">
  <resources>
    <object id="1" type="model">
      <components>
        <component p:path="/3D/3dmodel.model" objectid="1"/>
      </components>
    </object>
  </resources>
  <build>
    <item objectid="1"/>
  </build>
</model>`;
    const zip = await buildZip([
      { name: "3D/3dmodel.model", data: utf8(aXml) },
      { name: "3D/Objects/b.model", data: utf8(bXml) },
    ]);
    const r = await parseThreeMf(zip);
    // Neither has its own <mesh>; only the skipped cycle hop would have supplied one.
    expect(r.status).toBe("malformed");
  });

  it("a genuine reference cycle where the cycle root also carries its own mesh still resolves, counting that mesh once", async () => {
    // A has its own mesh and a component referencing B, which references back to A. The cyclic hop is
    // skipped, but A's own mesh must come through exactly once.
    const aXml = CUBE_MODEL_XML.replace(
      'xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02">',
      'xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02" xmlns:p="http://schemas.microsoft.com/3dmanufacturing/production/2015/06">',
    ).replace(
      "      </mesh>\n    </object>",
      '      </mesh>\n      <components>\n        <component p:path="/3D/Objects/b.model" objectid="1"/>\n      </components>\n    </object>',
    );
    const bXml = `<?xml version="1.0" encoding="UTF-8"?>
<model unit="millimeter" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02" xmlns:p="http://schemas.microsoft.com/3dmanufacturing/production/2015/06">
  <resources>
    <object id="1" type="model">
      <components>
        <component p:path="/3D/3dmodel.model" objectid="1"/>
      </components>
    </object>
  </resources>
  <build>
    <item objectid="1"/>
  </build>
</model>`;
    const zip = await buildZip([
      { name: "3D/3dmodel.model", data: utf8(aXml) },
      { name: "3D/Objects/b.model", data: utf8(bXml) },
    ]);
    const r = await parseThreeMf(zip);
    expect(r.status).toBe("ok");
    // A's cube mesh (1 cm3, 12 triangles) counts once; the cyclic hop contributes nothing.
    expect(r.volumeCm3).toBeCloseTo(1, 5);
    expect(r.triangleCount).toBe(12);
  });
});

describe("parseThreeMf: cycle-adjacent cache must not leak a truncated volume to an acyclic reference", () => {
  // A <-> B is a 2-cycle with own meshes; C is outside it and references B. Cycle members are tainted and
  // never cached, so each chain re-walks the cycle: A's 1 + 8 = 9 cm3 (24 tri), C's 27 + 8 + 1 = 36 cm3
  // (36 tri), total 45 cm3 (60 tri), whichever of A or C is listed first.
  const aXml = `<?xml version="1.0" encoding="UTF-8"?>
<model unit="millimeter" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02" xmlns:p="http://schemas.microsoft.com/3dmanufacturing/production/2015/06">
  <resources>
    <object id="1" type="model">${cubeMeshXml(10)}
      <components>
        <component p:path="/3D/Objects/b.model" objectid="1"/>
      </components>
    </object>
  </resources>
  <build>
    <item objectid="1"/>
  </build>
</model>`;
  const bXml = `<?xml version="1.0" encoding="UTF-8"?>
<model unit="millimeter" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02" xmlns:p="http://schemas.microsoft.com/3dmanufacturing/production/2015/06">
  <resources>
    <object id="1" type="model">${cubeMeshXml(20)}
      <components>
        <component p:path="/3D/Objects/a.model" objectid="1"/>
      </components>
    </object>
  </resources>
  <build>
    <item objectid="1"/>
  </build>
</model>`;
  const cXml = `<?xml version="1.0" encoding="UTF-8"?>
<model unit="millimeter" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02" xmlns:p="http://schemas.microsoft.com/3dmanufacturing/production/2015/06">
  <resources>
    <object id="1" type="model">${cubeMeshXml(30)}
      <components>
        <component p:path="/3D/Objects/b.model" objectid="1"/>
      </components>
    </object>
  </resources>
  <build>
    <item objectid="1"/>
  </build>
</model>`;

  const EXPECTED_VOLUME_CM3 = 1 + 8 + (27 + 8 + 1); // A's chain (9) + C's chain (36) = 45
  const EXPECTED_TRIANGLES = 12 * 2 + 12 * 3; // A's chain (2 cubes) + C's chain (3 cubes) = 60

  it("build lists A then C: C's chain still gets B's full volume, not a cycle-truncated leftover from A's chain", async () => {
    const rootXml = `<?xml version="1.0" encoding="UTF-8"?>
<model unit="millimeter" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02" xmlns:p="http://schemas.microsoft.com/3dmanufacturing/production/2015/06">
  <resources></resources>
  <build>
    <item p:path="/3D/Objects/a.model" objectid="1"/>
    <item p:path="/3D/Objects/c.model" objectid="1"/>
  </build>
</model>`;
    const zip = await buildZip([
      { name: "3D/3dmodel.model", data: utf8(rootXml) },
      { name: "3D/Objects/a.model", data: utf8(aXml) },
      { name: "3D/Objects/b.model", data: utf8(bXml) },
      { name: "3D/Objects/c.model", data: utf8(cXml) },
    ]);
    const r = await parseThreeMf(zip);
    expect(r.status).toBe("ok");
    expect(r.volumeCm3).toBeCloseTo(EXPECTED_VOLUME_CM3, 5);
    expect(r.triangleCount).toBe(EXPECTED_TRIANGLES);
  });

  it("build lists C then A: same total as A-then-C (order independence)", async () => {
    const rootXml = `<?xml version="1.0" encoding="UTF-8"?>
<model unit="millimeter" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02" xmlns:p="http://schemas.microsoft.com/3dmanufacturing/production/2015/06">
  <resources></resources>
  <build>
    <item p:path="/3D/Objects/c.model" objectid="1"/>
    <item p:path="/3D/Objects/a.model" objectid="1"/>
  </build>
</model>`;
    const zip = await buildZip([
      { name: "3D/3dmodel.model", data: utf8(rootXml) },
      { name: "3D/Objects/a.model", data: utf8(aXml) },
      { name: "3D/Objects/b.model", data: utf8(bXml) },
      { name: "3D/Objects/c.model", data: utf8(cXml) },
    ]);
    const r = await parseThreeMf(zip);
    expect(r.status).toBe("ok");
    expect(r.volumeCm3).toBeCloseTo(EXPECTED_VOLUME_CM3, 5);
    expect(r.triangleCount).toBe(EXPECTED_TRIANGLES);
  });
});

describe("parseThreeMf: single-quoted XML attributes", () => {
  it("parses a fully single-quoted slice_info.config the same as double-quoted", async () => {
    const config = `<config><plate><metadata key='prediction' value='3600'/><metadata key='weight' value='15.5'/></plate></config>`;
    const r = await parseSliceConfig(config);
    expect(r.status).toBe("ok");
    expect(r.meta?.timeSeconds).toBe(3600);
    expect(r.meta?.weightG).toBeCloseTo(15.5, 5);
  });

  it.each([
    [
      "different attributes on the same tag may mix quote styles",
      `<config><plate><metadata key="weight" value='12.25'/></plate></config>`,
      12.25,
    ],
    [
      "sums single-quoted used_g attributes when the weight key is absent",
      `<config><plate><filament id='1' used_g='10.5'/><filament id='2' used_g='4.25'/></plate></config>`,
      14.75,
    ],
  ] as const)("%s", async (_name, config, weightG) => {
    const r = await parseSliceConfig(config);
    expect(r.status).toBe("ok");
    expect(r.kind).toBe("sliced");
    expect(r.meta?.weightG).toBeCloseTo(weightG, 5);
  });

  it("parses a fully single-quoted 3dmodel.model, including a non-default unit attribute", async () => {
    const xml = `<model unit='centimeter'>
  <resources>
    <object id='1' type='model'>
      <mesh>
        <vertices>
          <vertex x='0' y='0' z='0'/>
          <vertex x='1' y='0' z='0'/>
          <vertex x='1' y='1' z='0'/>
          <vertex x='0' y='1' z='0'/>
          <vertex x='0' y='0' z='1'/>
          <vertex x='1' y='0' z='1'/>
          <vertex x='1' y='1' z='1'/>
          <vertex x='0' y='1' z='1'/>
        </vertices>
        <triangles>
          <triangle v1='0' v2='3' v3='2'/>
          <triangle v1='0' v2='2' v3='1'/>
          <triangle v1='4' v2='5' v3='6'/>
          <triangle v1='4' v2='6' v3='7'/>
          <triangle v1='0' v2='1' v3='5'/>
          <triangle v1='0' v2='5' v3='4'/>
          <triangle v1='3' v2='7' v3='6'/>
          <triangle v1='3' v2='6' v3='2'/>
          <triangle v1='0' v2='4' v3='7'/>
          <triangle v1='0' v2='7' v3='3'/>
          <triangle v1='1' v2='2' v3='6'/>
          <triangle v1='1' v2='6' v3='5'/>
        </triangles>
      </mesh>
    </object>
  </resources>
</model>`;
    const zip = await buildZip([{ name: "3D/3dmodel.model", data: utf8(xml) }]);
    const r = await parseThreeMf(zip);
    expect(r.status).toBe("ok");
    expect(r.kind).toBe("geometry");
    expect(r.volumeCm3).toBeCloseTo(1, 5);
  });
});

describe("parseThreeMf: slice_info.config multi-plate summing", () => {
  it("single plate: no plate-count suffix on the slicer label", async () => {
    const config = `<config><plate><metadata key="prediction" value="1800"/><metadata key="weight" value="10"/></plate></config>`;
    const r = await parseSliceConfig(config);
    expect(r.status).toBe("ok");
    expect(r.meta?.timeSeconds).toBe(1800);
    expect(r.meta?.weightG).toBeCloseTo(10, 5);
    expect(r.meta?.slicer).toBe("Bambu Studio / OrcaSlicer");
  });

  it("multi plate with metadata keys: sums prediction and weight per plate, labels the plate count", async () => {
    const config = `<config>
      <plate>
        <metadata key="index" value="1"/>
        <metadata key="prediction" value="1800"/>
        <metadata key="weight" value="10"/>
      </plate>
      <plate>
        <metadata key="index" value="2"/>
        <metadata key="prediction" value="2400"/>
        <metadata key="weight" value="5"/>
      </plate>
    </config>`;
    const r = await parseSliceConfig(config);
    expect(r.status).toBe("ok");
    expect(r.meta?.timeSeconds).toBe(4200);
    expect(r.meta?.weightG).toBeCloseTo(15, 5);
    expect(r.meta?.slicer).toBe("Bambu Studio / OrcaSlicer, 2 plates");
  });

  it("multi plate with only used_g: sums used_g independently per plate rather than pooling across all plates", async () => {
    const config = `<config>
      <plate><filament id="1" used_g="10.5"/></plate>
      <plate><filament id="1" used_g="4.25"/><filament id="2" used_g="1.25"/></plate>
    </config>`;
    const r = await parseSliceConfig(config);
    expect(r.status).toBe("ok");
    expect(r.meta?.weightG).toBeCloseTo(16, 5);
    expect(r.meta?.timeSeconds).toBeUndefined();
    expect(r.meta?.slicer).toBe("Bambu Studio / OrcaSlicer, 2 plates");
  });
});

describe("parseThreeMf: malformed input", () => {
  it.each([
    ["non-ZIP bytes are malformed, never throw", async () => utf8("not a zip file").buffer as ArrayBuffer],
    [
      "a ZIP with no recognized entry is malformed",
      () => buildZip([{ name: "readme.txt", data: utf8("hello") }]),
    ],
    [
      "a truncated ZIP (no End Of Central Directory) is malformed, never throws",
      async () => {
        const zip = await buildZip([{ name: "Metadata/slice_info.config", data: utf8("<config/>") }]);
        return zip.slice(0, zip.byteLength - 22); // chop off the EOCD record
      },
    ],
  ] as const)("%s", async (_name, makeBytes) => {
    await expect(parseThreeMf(await makeBytes())).resolves.toEqual({ status: "malformed" });
  });

  it("a corrupt deflate stream resolves to malformed, never rejects", async () => {
    const name = "Metadata/slice_info.config";
    const payload = utf8("<config><plate/></config>");
    const compressed = await deflateRaw(payload);
    const good = await buildZip([{ name, data: payload, method: 8 }]);
    const bytes = new Uint8Array(good.slice(0));
    // Invert every byte of the compressed payload (past the 30-byte local header + filename), so
    // DecompressionStream throws mid-read.
    const start = 30 + utf8(name).length;
    for (let i = start; i < start + compressed.length; i++) bytes[i] = bytes[i]! ^ 0xff;
    await expect(parseThreeMf(bytes.buffer)).resolves.toEqual({ status: "malformed" });
  });
});

describe("parseThreeMf: oversized model geometry is 'too-large', not 'malformed'", () => {
  it("a 3D/3dmodel.model entry that decompresses past the geometry-text budget is reported as too-large", async () => {
    // Stored, so compressed size equals decompressed size: under inflateCapped's 64 MiB cap but over
    // MAX_MODEL_GEOMETRY_BYTES (24 MiB). The size guard trips before the filler is decoded.
    const oversized = new Uint8Array(24 * 1024 * 1024 + 1024).fill(0x41);
    const zip = await buildZip([{ name: "3D/3dmodel.model", data: oversized }]);
    const r = await parseThreeMf(zip);
    expect(r.status).toBe("too-large");
  });

  it("a same-shape file that is merely undersized-and-unparseable stays 'malformed', distinct from 'too-large'", async () => {
      // Same entry name and missing-mesh shape but under the geometry budget: the failure modes are distinct.
    const undersized = utf8("not xml at all, and no <mesh> in sight");
    const zip = await buildZip([{ name: "3D/3dmodel.model", data: undersized }]);
    const r = await parseThreeMf(zip);
    expect(r.status).toBe("malformed");
  });
});

describe("parseThreeMf: hostile archives can't blow up parse time", () => {
  // Each case would take seconds to minutes under a lazy regex or uncapped recursion. The assertion
  // is on wall-clock time, with a budget that separates roughly-linear from quadratic-or-worse. 30s is
  // sized for the slowest CI runner (over 10s seen), with the gap to quadratic still intact.
  const WALL_CLOCK_BUDGET_MS = 30_000;

  it("a <mesh> tag repeated with no closing tag (512 KB) rejects quickly instead of scanning quadratically", async () => {
    // No </mesh> anywhere: a lazy regex would backtrack to the end of the string from every open tag
    // (O(k*n)); extractTagBlocks stops at the first open and finds zero blocks.
    const filler = "<mesh>".repeat(Math.ceil((512 * 1024) / "<mesh>".length));
    const xml = `<model unit="millimeter">${filler}`;
    const zip = await buildZip([{ name: "3D/3dmodel.model", data: utf8(xml) }]);

    const start = performance.now();
    const r = await parseThreeMf(zip);
    const elapsedMs = performance.now() - start;

    expect(elapsedMs).toBeLessThan(WALL_CLOCK_BUDGET_MS);
    expect(r.status).toBe("malformed"); // no <mesh> block ever closes, so no mesh is ever found
  });

  it("slice_info.config with a <plate> tag repeated with no closing tag (512 KB) rejects quickly instead of scanning quadratically", async () => {
    // The same attack against PLATE_PATTERN via the slice_info.config path.
    const filler = "<plate>".repeat(Math.ceil((512 * 1024) / "<plate>".length));
    const config = `<config>${filler}`;
    const zip = await buildZip([{ name: "Metadata/slice_info.config", data: utf8(config) }]);

    const start = performance.now();
    const r = await parseThreeMf(zip);
    const elapsedMs = performance.now() - start;

    expect(elapsedMs).toBeLessThan(WALL_CLOCK_BUDGET_MS);
    // No <plate> closes, so the whole document is one plate with no metadata keys; the parse falls through.
    expect(r.status).toBe("malformed");
  });

  it("a 2-wide/18-level reference DAG whose leaves point back at the root aborts on the step budget instead of re-walking exponentially", async () => {
    // A diamond DAG: each of 18 levels has 2 files referencing both files at the next level, so paths double
    // per level while only 37 zip entries exist. The deepest level references the root, so every node is
    // tainted and uncacheable; without the budget this was 2^18 re-resolutions.
    const LEVELS = 18;
    const levelPath = (level: number, node: 0 | 1) => `3D/Objects/L${level}_${node}.model`;
    const referencerXml = (refs: string[]) => {
      const components = refs.map((p) => `<component p:path="${p}" objectid="1"/>`).join("");
      return `<?xml version="1.0" encoding="UTF-8"?>
<model xmlns:p="http://schemas.microsoft.com/3dmanufacturing/production/2015/06">
  <resources><object id="1"><components>${components}</components></object></resources>
  <build><item objectid="1"/></build>
</model>`;
    };

    const entries: ZipEntryInput[] = [];
    for (let level = 1; level <= LEVELS; level++) {
      for (const node of [0, 1] as const) {
        const refs =
          level === LEVELS
            ? ["/3D/3dmodel.model"] // leaf references back to the root -> closes the cycle
            : [`/${levelPath(level + 1, 0)}`, `/${levelPath(level + 1, 1)}`];
        entries.push({ name: levelPath(level, node), data: utf8(referencerXml(refs)) });
      }
    }
    entries.unshift({
      name: "3D/3dmodel.model",
      data: utf8(referencerXml([`/${levelPath(1, 0)}`, `/${levelPath(1, 1)}`])),
    });

    const zip = await buildZip(entries);

    const start = performance.now();
    const r = await parseThreeMf(zip);
    const elapsedMs = performance.now() - start;

    expect(elapsedMs).toBeLessThan(WALL_CLOCK_BUDGET_MS);
    // The component-step budget trips well before the walk could finish.
    expect(r.status).toBe("too-large");
  });

  it("a cumulative decompression budget aborts a part graph built from many individually-under-cap parts", async () => {
    // Each part is under MAX_MODEL_GEOMETRY_BYTES (24 MiB) but their sum crosses MAX_TOTAL_INFLATED_BYTES
    // (128 MiB). Each is referenced twice to confirm the per-path cache makes repeats free.
    const PART_COUNT = 6;
    const PART_BYTES = 24 * 1024 * 1024 - 1024; // just under the per-entry cap
    const REPEATS = 2;

    const items: string[] = [];
    const entries: ZipEntryInput[] = [];
    for (let p = 0; p < PART_COUNT; p++) {
      const name = `3D/Objects/big${p}.model`;
      entries.push({ name, data: new Uint8Array(PART_BYTES).fill(0x41) });
      for (let r = 0; r < REPEATS; r++) items.push(`<item p:path="/${name}" objectid="1"/>`);
    }
    const rootXml = `<?xml version="1.0" encoding="UTF-8"?>
<model xmlns:p="http://schemas.microsoft.com/3dmanufacturing/production/2015/06">
  <resources></resources>
  <build>${items.join("")}</build>
</model>`;
    entries.unshift({ name: "3D/3dmodel.model", data: utf8(rootXml) });

    const zip = await buildZip(entries);

    const start = performance.now();
    const r = await parseThreeMf(zip);
    const elapsedMs = performance.now() - start;

    expect(elapsedMs).toBeLessThan(WALL_CLOCK_BUDGET_MS);
    expect(r.status).toBe("too-large");
      // Explicit vitest timeout: the default 5s also covers buildZip deflating ~144 MiB (over 10s on slow
      // CI), while a broken cumulative budget would take far longer.
  }, 60_000);

  it("legitimate multi-part fixtures are unaffected: single-mesh and multi-part volumes still parse to identical, correct values", async () => {
    // A regression guard, not a hostile fixture: the budgets and rewritten extraction must not change
    // legitimate results.
    const single = await buildZip([{ name: "3D/3dmodel.model", data: utf8(CUBE_MODEL_XML) }]);

    const singleResult = await parseThreeMf(single);
    expect(singleResult.status).toBe("ok");
    expect(singleResult.kind).toBe("geometry");
    expect(singleResult.volumeCm3).toBeCloseTo(1, 5);
    expect(singleResult.triangleCount).toBe(12);

    const rootXml = `<?xml version="1.0" encoding="UTF-8"?>
<model unit="millimeter" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02" xmlns:p="http://schemas.microsoft.com/3dmanufacturing/production/2015/06">
  <resources></resources>
  <build>
    <item p:path="/3D/Objects/object_1.model" objectid="1"/>
    <item p:path="/3D/Objects/object_1.model" objectid="1"/>
  </build>
</model>`;
    const multi = await buildZip([
      { name: "3D/3dmodel.model", data: utf8(rootXml) },
      { name: "3D/Objects/object_1.model", data: utf8(CUBE_MODEL_XML) },
    ]);
    const multiResult = await parseThreeMf(multi);
    expect(multiResult.status).toBe("ok");
    expect(multiResult.volumeCm3).toBeCloseTo(2, 5);
    expect(multiResult.triangleCount).toBe(24);
  });
});
