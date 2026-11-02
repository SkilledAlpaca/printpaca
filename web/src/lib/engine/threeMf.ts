import { parseGcodeText, type RawGcodeMeta } from "./gcode";
import { tetrahedronTermX6, volumeCm3FromSignedSumX6 } from "./stl";

/** Optional triangle sink fed by the geometry walk: the geometry-fallback path, and best-effort preview
 * painting for sliced archives (see feedSlicedPreview). Triangles of a part referenced several times are
 * emitted once; instance placement is not represented. Coordinates are mm after <model unit> scaling. With
 * no sink, a sliced archive's geometry entries are never read. */
export interface ThreeMfParseOptions {
  onTriangle?: (
    x1: number, y1: number, z1: number,
    x2: number, y2: number, z2: number,
    x3: number, y3: number, z3: number,
  ) => void;
}

export interface ThreeMfResult {
  /** "too-large" is a resource limit (model entry past MAX_MODEL_GEOMETRY_BYTES), distinct from
   * "malformed", so the UI can say the file is too big rather than corrupt. */
  status: "ok" | "empty" | "malformed" | "too-large";
  /** "sliced": exact weightG/timeSeconds from slicer metadata. "geometry": no metadata, so
   * volumeCm3/triangleCount come from the mesh. Set only when status is "ok". */
  kind?: "sliced" | "geometry";
  meta?: RawGcodeMeta;
  volumeCm3?: number;
  triangleCount?: number;
}

// Minimal ZIP reader: central directory walk, stored (0) and deflate (8) entries only; 3MF archives have
// known sizes up front, never streamed or spanned.

interface ZipEntry {
  name: string;
  method: number;
  compressedSize: number;
  localHeaderOffset: number;
}

const EOCD_SIGNATURE = 0x06054b50;
const CENTRAL_DIR_SIGNATURE = 0x02014b50;
const LOCAL_HEADER_SIGNATURE = 0x04034b50;
const EOCD_MIN_BYTES = 22;
const MAX_ZIP_COMMENT_BYTES = 65535;

/** Scans backward for the EOCD signature; its comment field is variable length, so its start can't be
 * computed from the end. */
function findEndOfCentralDirectory(view: DataView): number | null {
  const start = Math.max(0, view.byteLength - EOCD_MIN_BYTES - MAX_ZIP_COMMENT_BYTES);
  for (let i = view.byteLength - EOCD_MIN_BYTES; i >= start; i--) {
    if (view.getUint32(i, true) === EOCD_SIGNATURE) return i;
  }
  return null;
}

function readCentralDirectory(data: ArrayBuffer): ZipEntry[] | null {
  if (data.byteLength < EOCD_MIN_BYTES) return null;
  const view = new DataView(data);
  const eocdOffset = findEndOfCentralDirectory(view);
  if (eocdOffset === null) return null;

  const entryCount = view.getUint16(eocdOffset + 10, true);
  let offset = view.getUint32(eocdOffset + 16, true);
  const entries: ZipEntry[] = [];
  for (let i = 0; i < entryCount; i++) {
    if (offset + 46 > data.byteLength || view.getUint32(offset, true) !== CENTRAL_DIR_SIGNATURE) return null;
    const method = view.getUint16(offset + 10, true);
    const compressedSize = view.getUint32(offset + 20, true);
    const nameLength = view.getUint16(offset + 28, true);
    const extraLength = view.getUint16(offset + 30, true);
    const commentLength = view.getUint16(offset + 32, true);
    const localHeaderOffset = view.getUint32(offset + 42, true);
    const nameStart = offset + 46;
    if (nameStart + nameLength > data.byteLength) return null;
    const name = new TextDecoder().decode(new Uint8Array(data, nameStart, nameLength));
    entries.push({ name, method, compressedSize, localHeaderOffset });
    offset = nameStart + nameLength + extraLength + commentLength;
  }
  return entries;
}

// Same cap as gcodeMessage.ts's pre-read guard, here bounding one entry's decompressed size since deflate can
// expand a small archive without limit.
const MAX_ENTRY_BYTES = 64 * 1024 * 1024;

// Tighter cap for 3D/3dmodel.model: it is parsed as one in-memory XML string, so its size bounds peak memory
// and main-thread parse time. 24 MiB is generous for the single unsliced part this fallback serves and avoids
// the ~1 GB / ~2 s blowup a 64 MiB entry caused.
export const MAX_MODEL_GEOMETRY_BYTES = 24 * 1024 * 1024;

// Cumulative caps per parseThreeMf call, tracked in a per-call ResolutionBudget (never module state:
// concurrent parses must not share a counter). An adversarial part graph can visit exponentially many (path,
// occurrence) pairs, each under the per-entry cap; these bound the graph-wide inflated bytes and
// resolveModelGeometry step count.
const MAX_TOTAL_INFLATED_BYTES = 128 * 1024 * 1024;
const MAX_COMPONENT_STEPS = 1024;

interface ResolutionBudget {
  inflatedBytes: number;
  steps: number;
}

/** Reads the data from the local file header's own offsets rather than trusting the central directory's
 * name/extra lengths. Returns null for unsupported methods or out-of-range headers. */
async function readZipEntry(data: ArrayBuffer, entry: ZipEntry): Promise<ArrayBuffer | null> {
  const view = new DataView(data);
  const offset = entry.localHeaderOffset;
  if (offset + 30 > data.byteLength || view.getUint32(offset, true) !== LOCAL_HEADER_SIGNATURE) return null;
  const nameLength = view.getUint16(offset + 26, true);
  const extraLength = view.getUint16(offset + 28, true);
  const dataStart = offset + 30 + nameLength + extraLength;
  if (dataStart + entry.compressedSize > data.byteLength) return null;
  const raw = data.slice(dataStart, dataStart + entry.compressedSize);

  if (entry.method === 0) return raw;
  if (entry.method === 8) return await inflateCapped(raw);
  return null;
}

/** Inflates in bounded chunks: Response.arrayBuffer() has no size limit, so a zip bomb would buffer
 * unbounded memory first. A corrupt stream returns null (reported as "malformed"). */
async function inflateCapped(compressed: ArrayBuffer): Promise<ArrayBuffer | null> {
  const stream = new Blob([compressed]).stream().pipeThrough(new DecompressionStream("deflate-raw"));
  const reader = stream.getReader();
  const chunks: Uint8Array[] = [];
  let total = 0;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      total += value.byteLength;
      if (total > MAX_ENTRY_BYTES) {
        await reader.cancel();
        return null;
      }
      chunks.push(value);
    }
  } catch {
    return null; // corrupt/truncated deflate stream
  }
  const out = new Uint8Array(total);
  let pos = 0;
  for (const chunk of chunks) {
    out.set(chunk, pos);
    pos += chunk.byteLength;
  }
  return out.buffer;
}

// Targeted XML extraction, not a general parser: an attribute-map regex tolerant of attribute order and
// self-closing or open/close forms.

// The \2 backreference requires the closing quote to match the opener, so a mixed-quote attribute fails to
// match instead of mis-slicing.
function parseXmlAttributes(tag: string): Record<string, string> {
  const attrs: Record<string, string> = {};
  for (const m of tag.matchAll(/([\w:-]+)\s*=\s*(["'])([^"']*)\2/g)) attrs[m[1]!] = m[3]!;
  return attrs;
}

function metadataValue(xml: string, key: string): number | undefined {
  for (const m of xml.matchAll(/<metadata\b([^>]*)\/?>/gi)) {
    const attrs = parseXmlAttributes(m[1]!);
    if (attrs.key !== key || attrs.value === undefined) continue;
    const v = Number.parseFloat(attrs.value);
    if (Number.isFinite(v)) return v;
  }
  return undefined;
}

function sumAttribute(xml: string, attr: string): number | undefined {
  let sum = 0;
  let found = false;
  for (const m of xml.matchAll(new RegExp(`\\b${attr}=(["'])([^"']*)\\1`, "gi"))) {
    const v = Number.parseFloat(m[2]!);
    if (Number.isFinite(v)) {
      sum += v;
      found = true;
    }
  }
  return found && sum > 0 ? sum : undefined;
}

/** Case-insensitive indexOf for a lowercase ASCII needle without case-folding the whole haystack: a hostile
 * all-uppercase entry would make that cost the whole document. Folding only the characters compared bounds
 * work by haystack length times needle length. */
function indexOfAsciiCI(haystack: string, needleLower: string, fromIndex: number): number {
  const n = needleLower.length;
  const limit = haystack.length - n;
  for (let i = fromIndex; i <= limit; i++) {
    let j = 0;
    while (j < n) {
      const c = haystack.charCodeAt(i + j);
      if ((c >= 65 && c <= 90 ? c + 32 : c) !== needleLower.charCodeAt(j)) break;
      j++;
    }
    if (j === n) return i;
  }
  return -1;
}

/** Single-pass indexOf scan replacing a lazy cross-tag regex, whose backtracking is O(k*n) on content with
 * many opens and few closes. Each search resumes where the last ended, so no byte is rescanned. An
 * unclosed open tag ends the scan with zero blocks. `tagName` must be lowercase. */
function extractTagBlocks(xml: string, tagName: string): string[] {
  const openPrefix = `<${tagName}`;
  const closeTag = `</${tagName}>`;
  const blocks: string[] = [];
  let pos = 0;
  for (;;) {
    const openIdx = indexOfAsciiCI(xml, openPrefix, pos);
    if (openIdx === -1) break;
    const afterName = openIdx + openPrefix.length;
    const boundaryChar = xml[afterName];
    // The next character must not continue an identifier ("<meshx" is not a "<mesh" open tag).
    if (boundaryChar !== undefined && /\w/.test(boundaryChar)) {
      pos = afterName;
      continue;
    }
    const gt = xml.indexOf(">", afterName);
    if (gt === -1) break; // unterminated opening tag -- nothing further to find
    const closeIdx = indexOfAsciiCI(xml, closeTag, gt + 1);
    if (closeIdx === -1) break; // no matching close anywhere after this open -- stop, don't rescan
    blocks.push(xml.slice(gt + 1, closeIdx));
    pos = closeIdx + closeTag.length;
  }
  return blocks;
}

const SLICER_LABEL = "Bambu Studio / OrcaSlicer";

/** One plate's prediction (seconds) and weight (grams, else summed used_g), scoped to its <plate> block so
 * multi-plate metadata is never mixed. */
function parsePlateMetadata(plateXml: string): { timeSeconds?: number; weightG?: number } {
  const meta: { timeSeconds?: number; weightG?: number } = {};
  const prediction = metadataValue(plateXml, "prediction");
  if (prediction !== undefined && prediction > 0) meta.timeSeconds = prediction;
  const weight = metadataValue(plateXml, "weight") ?? sumAttribute(plateXml, "used_g");
  if (weight !== undefined && weight > 0) meta.weightG = weight;
  return meta;
}

/** Bambu Studio / OrcaSlicer slice_info.config: sums each plate's own metadata. Returns null when no plate
 * has a finite positive time or weight, so the caller falls back to the embedded g-code. */
function parseSliceInfoConfig(xml: string): RawGcodeMeta | null {
  // Older single-plate exports may omit the <plate> wrapper; treat the whole document as one plate.
  const plateBlocks = extractTagBlocks(xml, "plate");
  const blocks = plateBlocks.length > 0 ? plateBlocks : [xml];

  let totalTimeSeconds = 0;
  let totalWeightG = 0;
  let plateCount = 0;
  let hasTime = false;
  let hasWeight = false;

  for (const block of blocks) {
    const plate = parsePlateMetadata(block);
    if (plate.timeSeconds === undefined && plate.weightG === undefined) continue;
    plateCount++;
    if (plate.timeSeconds !== undefined) {
      totalTimeSeconds += plate.timeSeconds;
      hasTime = true;
    }
    if (plate.weightG !== undefined) {
      totalWeightG += plate.weightG;
      hasWeight = true;
    }
  }

  if (!hasTime && !hasWeight) return null;
  const meta: RawGcodeMeta = {};
  if (hasTime) meta.timeSeconds = totalTimeSeconds;
  if (hasWeight) meta.weightG = totalWeightG;
  meta.slicer = plateCount > 1 ? `${SLICER_LABEL}, ${plateCount} plates` : SLICER_LABEL;
  return meta;
}

function unitScaleToMm(unit: string): number {
  switch (unit) {
    case "micron":
      return 0.001;
    case "centimeter":
      return 10;
    case "meter":
      return 1000;
    case "inch":
      return 25.4;
    case "foot":
      return 304.8;
    default:
      return 1; // millimeter, and the 3MF core spec's own default when unset
  }
}

// Direct per-attribute lookups for the vertex/triangle hot loop, avoiding parseXmlAttributes' per-tag object.
// `\b` keeps "x" from matching inside a longer name; the literal "=" keeps "v1" from matching a prefix of
// "v10=".
const ATTR_X = /\bx\s*=\s*(["'])([^"']*)\1/;
const ATTR_Y = /\by\s*=\s*(["'])([^"']*)\1/;
const ATTR_Z = /\bz\s*=\s*(["'])([^"']*)\1/;
const ATTR_V1 = /\bv1\s*=\s*(["'])([^"']*)\1/;
const ATTR_V2 = /\bv2\s*=\s*(["'])([^"']*)\1/;
const ATTR_V3 = /\bv3\s*=\s*(["'])([^"']*)\1/;

function attrValue(tag: string, pattern: RegExp): string | undefined {
  return pattern.exec(tag)?.[2];
}

const VERTEX_TAG = /<vertex\b([^>]*)\/?>/gi;
const TRIANGLE_TAG = /<triangle\b([^>]*)\/?>/gi;

/** Valid only inside [0, count); NaN (unparsable index) fails every comparison and takes the skip path. */
function inRange(i: number, count: number): boolean {
  return i >= 0 && i < count;
}

interface GeometryAccumulator {
  totalVolumeCm3: number;
  totalTriangles: number;
  sawMesh: boolean;
}

/** Scans one model XML's <mesh> vertices/triangles into `acc` without following p:path
 * (resolveModelGeometry does that). Volume is summed per mesh as an absolute value, so opposite-winding
 * objects can't cancel. Vertices live in three Float64Arrays sized by a counting pass; an unparsable
 * vertex stores NaN in x as a sentinel, so triangles referencing it are skipped like out-of-range indices.
 * Returns false on a finite-vertices-but-overflowing-sum ("malformed"). */
function accumulateMeshesInModelXml(xml: string, acc: GeometryAccumulator, opts?: ThreeMfParseOptions): boolean {
  const unitMatch = xml.match(/<model\b[^>]*\bunit\s*=\s*(["'])([^"']*)\1/i);
  const scale = unitScaleToMm(unitMatch?.[2]?.toLowerCase() ?? "millimeter");

  for (const meshXml of extractTagBlocks(xml, "mesh")) {
    acc.sawMesh = true;

    // The counting pass can match more tags than the fill loop (e.g. a malformed `<vertex` with no `>`);
    // prefilling with NaN sends unreached slots down the sentinel skip path instead of keeping (0,0,0).
    const vertexCount = meshXml.match(/<vertex\b/gi)?.length ?? 0;
    const vx = new Float64Array(vertexCount).fill(NaN);
    const vy = new Float64Array(vertexCount).fill(NaN);
    const vz = new Float64Array(vertexCount).fill(NaN);
    let vi = 0;
    for (const m of meshXml.matchAll(VERTEX_TAG)) {
      const tag = m[1]!;
      const x = Number.parseFloat(attrValue(tag, ATTR_X) ?? "") * scale;
      const y = Number.parseFloat(attrValue(tag, ATTR_Y) ?? "") * scale;
      const z = Number.parseFloat(attrValue(tag, ATTR_Z) ?? "") * scale;
      const valid = Number.isFinite(x) && Number.isFinite(y) && Number.isFinite(z);
      vx[vi] = valid ? x : NaN;
      vy[vi] = valid ? y : NaN;
      vz[vi] = valid ? z : NaN;
      vi++;
    }

    let sumX6 = 0;
    let triangleCount = 0;
    for (const m of meshXml.matchAll(TRIANGLE_TAG)) {
      const tag = m[1]!;
      const i1 = Number.parseInt(attrValue(tag, ATTR_V1) ?? "", 10);
      const i2 = Number.parseInt(attrValue(tag, ATTR_V2) ?? "", 10);
      const i3 = Number.parseInt(attrValue(tag, ATTR_V3) ?? "", 10);
      if (!inRange(i1, vertexCount) || !inRange(i2, vertexCount) || !inRange(i3, vertexCount)) continue;
      const x1 = vx[i1]!, y1 = vy[i1]!, z1 = vz[i1]!;
      const x2 = vx[i2]!, y2 = vy[i2]!, z2 = vz[i2]!;
      const x3 = vx[i3]!, y3 = vy[i3]!, z3 = vz[i3]!;
      // Number.isNaN, not falsy: 0 is a legitimate coordinate.
      if (Number.isNaN(x1) || Number.isNaN(x2) || Number.isNaN(x3)) continue;
      sumX6 += tetrahedronTermX6(x1, y1, z1, x2, y2, z2, x3, y3, z3);
      triangleCount++;
      opts?.onTriangle?.(x1, y1, z1, x2, y2, z2, x3, y3, z3);
    }
    if (triangleCount > 0) {
      const meshVolumeCm3 = volumeCm3FromSignedSumX6(sumX6);
      if (!Number.isFinite(meshVolumeCm3)) return false; // finite vertices that still overflow the sum
      acc.totalVolumeCm3 += meshVolumeCm3;
      acc.totalTriangles += triangleCount;
    }
  }
  return true;
}

// Matches the Production Extension's <component p:path> and <build><item p:path>; both point at another zip
// entry, so one pattern covers both.
const P_PATH_TAG = /<(?:component|item)\b([^>]*)\/?>/gi;

function extractPPaths(xml: string): string[] {
  const paths: string[] = [];
  for (const m of xml.matchAll(P_PATH_TAG)) {
    const p = parseXmlAttributes(m[1]!)["p:path"];
    if (p) paths.push(p);
  }
  return paths;
}

/** Resolves a p:path relative to the referencing model file; a leading "/" means the OPC package root. */
function resolveZipPath(basePath: string, ref: string): string {
  if (ref.startsWith("/")) return ref.slice(1);
  const slash = basePath.lastIndexOf("/");
  return (slash >= 0 ? basePath.slice(0, slash + 1) : "") + ref;
}

const normalizeZipPath = (name: string) => name.replace(/^\/+/, "").toLowerCase();

/** Parses one model file's <mesh> geometry and follows its p:path references to sibling parts, summing
 * volumes and triangles. `refCounts` counts how often a path appears in one file's XML (N identical items
 * count N times). `cache` holds each untainted resolved part, reused at every call site and charged
 * against the byte budget once. `ancestors` (paths on the current call stack only) guards genuine cycles.
 * `tainted` marks a result truncated by that guard; tainted results are never cached, because a truncation
 * depends on the ancestor chain and caching it would make the total depend on traversal order (a
 * correctness bug). An adversarial DAG can force exponential recomputation of tainted nodes, so only
 * ResolutionBudget bounds it: the walk is not fast but terminates quickly. Missing or unreadable targets
 * are skipped; "malformed" and "too-large" short-circuit. Returns null when no <mesh> was seen anywhere. */
async function resolveModelGeometry(
  xml: string,
  modelPath: string,
  data: ArrayBuffer,
  entries: ZipEntry[],
  ancestors: Set<string>,
  cache: Map<string, ThreeMfResult | null>,
  budget: ResolutionBudget,
  opts?: ThreeMfParseOptions,
): Promise<{ result: ThreeMfResult | null; tainted: boolean }> {
  // One step per invocation (tainted recompute or cache miss); this is what an adversarial DAG multiplies.
  // Cache hits never reach here.
  budget.steps++;
  if (budget.steps > MAX_COMPONENT_STEPS) return { result: { status: "too-large" }, tainted: false };

  const acc: GeometryAccumulator = { totalVolumeCm3: 0, totalTriangles: 0, sawMesh: false };
  if (!accumulateMeshesInModelXml(xml, acc, opts)) return { result: { status: "malformed" }, tainted: false };

  const refCounts = new Map<string, number>();
  for (const ref of extractPPaths(xml)) {
    const key = normalizeZipPath(resolveZipPath(modelPath, ref));
    refCounts.set(key, (refCounts.get(key) ?? 0) + 1);
  }

  let tainted = false;

  for (const [key, count] of refCounts) {
    if (ancestors.has(key)) {
      // Cycle guard: this key is already on the call stack. Skipping it leaves this call's total incomplete,
      // so `tainted` blocks caching.
      tainted = true;
      continue;
    }

    let subResult = cache.get(key);
    let subTainted = false;
    if (subResult === undefined) {
      const target = entries.find((e) => normalizeZipPath(e.name) === key);
      if (!target) {
        subResult = null; // missing target -> skip this ref
      } else {
        const bytes = await readZipEntry(data, target);
        if (!bytes) {
          subResult = null;
        } else {
          // Charged to the cumulative budget before the per-entry check: many individually small parts must
          // still be bounded in aggregate.
          budget.inflatedBytes += bytes.byteLength;
          if (budget.inflatedBytes > MAX_TOTAL_INFLATED_BYTES) return { result: { status: "too-large" }, tainted: false };

          // Same tighter cap as the root model entry. Not cached; the whole parse aborts here.
          if (bytes.byteLength > MAX_MODEL_GEOMETRY_BYTES) return { result: { status: "too-large" }, tainted: false };

          ancestors.add(key);
          const subXml = new TextDecoder().decode(bytes);
          const sub = await resolveModelGeometry(subXml, target.name, data, entries, ancestors, cache, budget, opts);
          subResult = sub.result;
          subTainted = sub.tainted;
          ancestors.delete(key);
        }
      }
      // Only an untainted resolution is safe to memoize (see the doc comment above resolveModelGeometry).
      if (!subTainted) cache.set(key, subResult);
      if (subTainted) tainted = true;
    }
    // Cache hit: only untainted results are cached, so a hit never propagates taint.

    if (subResult?.status === "malformed" || subResult?.status === "too-large") {
      return { result: subResult, tainted: false };
    }
    if (subResult?.status === "ok") {
      acc.totalVolumeCm3 += (subResult.volumeCm3 ?? 0) * count;
      acc.totalTriangles += (subResult.triangleCount ?? 0) * count;
      acc.sawMesh = true;
    } else if (subResult?.status === "empty") {
      acc.sawMesh = true; // that part had a <mesh>, just zero triangles
    }
  }

  if (!acc.sawMesh) return { result: null, tainted };
  if (acc.totalTriangles === 0) return { result: { status: "empty" }, tainted };
  if (!Number.isFinite(acc.totalVolumeCm3)) return { result: { status: "malformed" }, tainted: false };
  return {
    result: { status: "ok", kind: "geometry", volumeCm3: acc.totalVolumeCm3, triangleCount: acc.totalTriangles },
    tainted,
  };
}

const SLICE_INFO_PATTERN = /(^|\/)metadata\/slice_info\.config$/i;
const EMBEDDED_GCODE_PATTERN = /(^|\/)metadata\/.*\.gcode$/i;
const MODEL_PATTERN = /(^|\/)3d\/3dmodel\.model$/i;

/** Best-effort feed of opts.onTriangle for a sliced archive, purely for the public quote page's decorative
 * 3D preview; everything it computes is discarded because the slicer metadata is authoritative. Every
 * failure is swallowed so a hostile or truncated geometry entry can't downgrade a good sliced result. The
 * size check still runs before decoding, so this can't bypass the geometry-fallback budget. */
async function feedSlicedPreview(data: ArrayBuffer, entries: ZipEntry[], opts: ThreeMfParseOptions): Promise<void> {
  try {
    const modelEntry = entries.find((e) => MODEL_PATTERN.test(e.name));
    if (!modelEntry) return;
    const bytes = await readZipEntry(data, modelEntry);
    if (!bytes || bytes.byteLength > MAX_MODEL_GEOMETRY_BYTES) return; // missing/unreadable/oversized -> empty sink, no error
    // Fresh budget for this call's resolution tree, never shared with the geometry-fallback branch (the two
    // never run for the same parse).
    const budget: ResolutionBudget = { inflatedBytes: bytes.byteLength, steps: 0 };
    await resolveModelGeometry(
      new TextDecoder().decode(bytes),
      modelEntry.name,
      data,
      entries,
      new Set([normalizeZipPath(modelEntry.name)]),
      new Map(),
      budget,
      opts,
    );
  } catch {
    // Preview only: nothing here may fail an otherwise-valid sliced archive.
  }
}

// opts.onTriangle fires along the geometry-fallback branch and, via feedSlicedPreview, after a sliced
// success, only when a sink was passed.
export async function parseThreeMf(data: ArrayBuffer, opts?: ThreeMfParseOptions): Promise<ThreeMfResult> {
  const entries = readCentralDirectory(data);
  if (!entries) return { status: "malformed" };

  const sliceInfoEntry = entries.find((e) => SLICE_INFO_PATTERN.test(e.name));
  if (sliceInfoEntry) {
    const bytes = await readZipEntry(data, sliceInfoEntry);
    if (bytes) {
      const meta = parseSliceInfoConfig(new TextDecoder().decode(bytes));
      if (meta) {
        if (opts?.onTriangle) await feedSlicedPreview(data, entries, opts);
        return { status: "ok", kind: "sliced", meta };
      }
    }
  }

  const gcodeEntry = entries.find((e) => EMBEDDED_GCODE_PATTERN.test(e.name));
  if (gcodeEntry) {
    const bytes = await readZipEntry(data, gcodeEntry);
    if (bytes) {
      const meta = parseGcodeText(new TextDecoder().decode(bytes));
      if (meta.weightG !== undefined || meta.timeSeconds !== undefined || meta.lengthM !== undefined) {
        if (opts?.onTriangle) await feedSlicedPreview(data, entries, opts);
        return { status: "ok", kind: "sliced", meta };
      }
    }
  }

  const modelEntry = entries.find((e) => MODEL_PATTERN.test(e.name));
  if (modelEntry) {
    const bytes = await readZipEntry(data, modelEntry);
    if (bytes) {
      // Checked on the decompressed bytes before a TextDecoder pass; this entry has a tighter budget than
      // MAX_ENTRY_BYTES. Reported as "too-large", not "malformed".
      if (bytes.byteLength > MAX_MODEL_GEOMETRY_BYTES) return { status: "too-large" };
      // Taint is discarded: it only decides whether a sub-path may be cached, and this is the outermost call.
      // The budget is fresh per parseThreeMf call and includes the root entry's bytes.
      const budget: ResolutionBudget = { inflatedBytes: bytes.byteLength, steps: 0 };
      const { result: geometry } = await resolveModelGeometry(
        new TextDecoder().decode(bytes),
        modelEntry.name,
        data,
        entries,
        new Set([normalizeZipPath(modelEntry.name)]),
        new Map(),
        budget,
        opts,
      );
      if (geometry) return geometry;
    }
  }

  return { status: "malformed" };
}
