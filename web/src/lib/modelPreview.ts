// Pure geometry/math for the public quote page's model preview. No DOM: ModelPreview.svelte owns the canvas
// and calls in once per frame.

/** One triangle in the 9-number layout the engine's onTriangle sinks hand out, so a sink triangle goes
 * straight into TriangleCollector.add. */
export interface Triangle {
  x1: number; y1: number; z1: number;
  x2: number; y2: number; z2: number;
  x3: number; y3: number; z3: number;
}

// Decimation: caps retained triangles at TRIANGLE_CAP so a large model never makes the canvas paint, or the
// collector hold, more than a preview needs.

/** ~30000: more detail than a thumbnail canvas can resolve, and about what a single 2D-canvas paint stays
 * cheap at. */
export const TRIANGLE_CAP = 30000;

/** Single-pass, bounded-memory, deterministic decimator ("block halving"): keep 1-in-`stride` (starting at
 * 1) until the buffer reaches `cap`, then drop every other retained triangle and double `stride`. O(1)
 * amortized per triangle. Bias: an emission-order sample only asymptotically fair, with no notion of
 * surface area, so a small distinct detail can be thinned as much as the bulk. Fine for a shape preview,
 * not for a specific region. */
export class TriangleCollector {
  private readonly cap: number;
  private triangles: Triangle[] = [];
  private stride = 1;
  private counter = 0;

  constructor(cap: number = TRIANGLE_CAP) {
    this.cap = Math.max(1, Math.floor(cap));
  }

  add(
    x1: number, y1: number, z1: number,
    x2: number, y2: number, z2: number,
    x3: number, y3: number, z3: number,
  ): void {
    this.counter++;
    if ((this.counter - 1) % this.stride !== 0) return; // not this triangle's turn at the current stride
    if (this.triangles.length >= this.cap) {
      const kept: Triangle[] = [];
      for (let i = 0; i < this.triangles.length; i += 2) kept.push(this.triangles[i]!);
      this.triangles = kept;
      this.stride *= 2;
    }
    this.triangles.push({ x1, y1, z1, x2, y2, z2, x3, y3, z3 });
  }

  /** What is retained so far (never longer than `cap`); the live array, treated as read-only by callers. */
  result(): Triangle[] {
    return this.triangles;
  }
}

// Bounding box + fit-to-viewport

export interface BoundingBox {
  minX: number; minY: number; minZ: number;
  maxX: number; maxY: number; maxZ: number;
}

/** null for an empty list, so callers can tell "nothing parsed yet" from a real, possibly zero-volume box. */
export function computeBoundingBox(triangles: Triangle[]): BoundingBox | null {
  if (triangles.length === 0) return null;
  let minX = Infinity, minY = Infinity, minZ = Infinity;
  let maxX = -Infinity, maxY = -Infinity, maxZ = -Infinity;
  for (const t of triangles) {
    for (const [x, y, z] of [
      [t.x1, t.y1, t.z1],
      [t.x2, t.y2, t.z2],
      [t.x3, t.y3, t.z3],
    ] as const) {
      if (x < minX) minX = x;
      if (x > maxX) maxX = x;
      if (y < minY) minY = y;
      if (y > maxY) maxY = y;
      if (z < minZ) minZ = z;
      if (z > maxZ) maxZ = z;
    }
  }
  return { minX, minY, minZ, maxX, maxY, maxZ };
}

export interface Fit {
  scale: number;
  centerX: number; centerY: number; centerZ: number;
}

/** Scales and centers so the model stays on screen at any yaw/pitch: sizes off the bounding sphere (half
 * the box's space diagonal), the only radius that can't clip mid-rotation. `padding` (0-1) leaves
 * headroom; viewportSize is the canvas's smaller side. A zero-size box gets scale 1 to avoid dividing by
 * zero. */
export function computeFit(bbox: BoundingBox, viewportSize: number, padding = 0.85): Fit {
  const centerX = (bbox.minX + bbox.maxX) / 2;
  const centerY = (bbox.minY + bbox.maxY) / 2;
  const centerZ = (bbox.minZ + bbox.maxZ) / 2;
  const dx = bbox.maxX - bbox.minX;
  const dy = bbox.maxY - bbox.minY;
  const dz = bbox.maxZ - bbox.minZ;
  const radius = Math.sqrt(dx * dx + dy * dy + dz * dz) / 2;
  const scale = radius > 0 ? (viewportSize * padding) / 2 / radius : 1;
  return { scale, centerX, centerY, centerZ };
}

// Rotation (yaw about Y, then pitch about X) and orthographic projection.

export interface Point3 { x: number; y: number; z: number; }

/** Yaw first, then pitch, about the re-centered origin, matching how a drag composes (horizontal spins in
 * place, vertical tilts the spun model). Radians. */
export function rotatePoint(x: number, y: number, z: number, yaw: number, pitch: number): Point3 {
  const cosY = Math.cos(yaw), sinY = Math.sin(yaw);
  const rx = x * cosY + z * sinY;
  const rz1 = -x * sinY + z * cosY;

  const cosP = Math.cos(pitch), sinP = Math.sin(pitch);
  const ry = y * cosP - rz1 * sinP;
  const rz = y * sinP + rz1 * cosP;

  return { x: rx, y: ry, z: rz };
}

/** Orthographic: shifts the origin to the canvas center and flips Y (model up is +Y, canvas down is +Y). z
 * passes through for depth; scaling is the caller's job. */
function projectPoint(x: number, y: number, z: number, viewportWidth: number, viewportHeight: number): Point3 {
  return { x: viewportWidth / 2 + x, y: viewportHeight / 2 - y, z };
}

// Flat shading: one normal and one brightness per triangle; a flat-shaded low-poly look is the target.

/** Fixed light in the same rotated view space as the normals, so it moves with the model like a headlamp.
 * Not configurable. */
const LIGHT_DIRECTION: Point3 = normalize({ x: 0.4, y: 0.65, z: 0.65 });

/** Brightness floor so faces turned from the light don't go pure black; with no fill light, full darkness
 * would make concave geometry look like holes. */
export const AMBIENT = 0.25;

function normalize(v: Point3): Point3 {
  const len = Math.sqrt(v.x * v.x + v.y * v.y + v.z * v.z);
  if (len === 0) return { x: 0, y: 0, z: 1 };
  return { x: v.x / len, y: v.y / len, z: v.z / len };
}

/** Flat-shade brightness in [AMBIENT, 1]: clamped Lambertian diffuse floored at AMBIENT. A zero-length
 * normal (degenerate triangle) shades at AMBIENT rather than NaN. */
export function shadeNormal(nx: number, ny: number, nz: number): number {
  const len = Math.sqrt(nx * nx + ny * ny + nz * nz);
  if (len === 0) return AMBIENT; // degenerate/collinear triangle: no well-defined face direction
  const n = { x: nx / len, y: ny / len, z: nz / len };
  const dot = n.x * LIGHT_DIRECTION.x + n.y * LIGHT_DIRECTION.y + n.z * LIGHT_DIRECTION.z;
  const clamped = Math.max(0, dot);
  return AMBIENT + clamped * (1 - AMBIENT);
}

// Per-frame pipeline: rotate, project and shade a triangle, then depth-sort a frame for painter's algorithm.

export interface ProjectedTriangle {
  x1: number; y1: number; x2: number; y2: number; x3: number; y3: number;
  /** Average view-space z of the three vertices (larger = closer; rotatePoint leaves +z toward the camera).
   * Used only for depth ordering. */
  depth: number;
  shade: number;
}

/** Re-centers on the fit's center, rotates, takes the face normal from the rotated vertices (so shading
 * follows the drag angle), then scales and projects. */
export function projectTriangle(
  t: Triangle,
  fit: Fit,
  yaw: number,
  pitch: number,
  viewportWidth: number,
  viewportHeight: number,
): ProjectedTriangle {
  const r1 = rotatePoint(t.x1 - fit.centerX, t.y1 - fit.centerY, t.z1 - fit.centerZ, yaw, pitch);
  const r2 = rotatePoint(t.x2 - fit.centerX, t.y2 - fit.centerY, t.z2 - fit.centerZ, yaw, pitch);
  const r3 = rotatePoint(t.x3 - fit.centerX, t.y3 - fit.centerY, t.z3 - fit.centerZ, yaw, pitch);

  // Flat face normal from two edges of the rotated triangle. Winding isn't consistent across STL/3MF
  // exporters, so a face lit from behind its normal just reads as AMBIENT (shadeNormal clamps negatives);
  // acceptable for a preview.
  const ux = r2.x - r1.x, uy = r2.y - r1.y, uz = r2.z - r1.z;
  const vx = r3.x - r1.x, vy = r3.y - r1.y, vz = r3.z - r1.z;
  const nx = uy * vz - uz * vy;
  const ny = uz * vx - ux * vz;
  const nz = ux * vy - uy * vx;

  const p1 = projectPoint(r1.x * fit.scale, r1.y * fit.scale, r1.z, viewportWidth, viewportHeight);
  const p2 = projectPoint(r2.x * fit.scale, r2.y * fit.scale, r2.z, viewportWidth, viewportHeight);
  const p3 = projectPoint(r3.x * fit.scale, r3.y * fit.scale, r3.z, viewportWidth, viewportHeight);

  return {
    x1: p1.x, y1: p1.y,
    x2: p2.x, y2: p2.y,
    x3: p3.x, y3: p3.y,
    depth: (r1.z + r2.z + r3.z) / 3,
    shade: shadeNormal(nx, ny, nz),
  };
}

/** Painter's algorithm: ascending depth, farthest first and nearest last. Returns a new array. */
export function depthSortTriangles(triangles: ProjectedTriangle[]): ProjectedTriangle[] {
  return [...triangles].sort((a, b) => a.depth - b.depth);
}
