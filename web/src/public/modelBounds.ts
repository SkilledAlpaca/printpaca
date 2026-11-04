// Bounding box of an uploaded model, read from the same triangle stream the weight estimate uses. Slicers and
// CAD tools write STL and 3MF in millimetres by default, so the numbers are shown as mm; a model exported in
// inches or metres shows up as a tiny or huge box, which is the cue the visitor needs.

export interface ModelBounds {
  /** Extents along x, y and z, in model units (treated as mm). */
  x: number;
  y: number;
  z: number;
}

/** Tracks min/max over the triangles handed to `add`. Matches the engine's `onTriangle` sink signature. */
export class BoundsCollector {
  private min = [Infinity, Infinity, Infinity];
  private max = [-Infinity, -Infinity, -Infinity];
  private count = 0;

  add(x1: number, y1: number, z1: number, x2: number, y2: number, z2: number, x3: number, y3: number, z3: number): void {
    const v = [x1, y1, z1, x2, y2, z2, x3, y3, z3];
    for (let i = 0; i < 9; i++) {
      const axis = i % 3;
      const n = v[i]!;
      if (!Number.isFinite(n)) continue;
      if (n < this.min[axis]!) this.min[axis] = n;
      if (n > this.max[axis]!) this.max[axis] = n;
    }
    this.count++;
  }

  /** Null when no triangle contributed a finite coordinate. */
  result(): ModelBounds | null {
    if (this.count === 0 || !this.min.every(Number.isFinite) || !this.max.every(Number.isFinite)) return null;
    return { x: this.max[0]! - this.min[0]!, y: this.max[1]! - this.min[1]!, z: this.max[2]! - this.min[2]! };
  }
}

/** Below this largest dimension a model is probably in inches (a 20 mm part is 0.79 in). */
export const TINY_MODEL_MM = 2;
/** Above this largest dimension a model is probably in micrometres or has a scale error; no desktop printer builds it. */
export const HUGE_MODEL_MM = 1000;

function trimmed(n: number): string {
  if (n >= 100) return String(Math.round(n));
  return String(Math.round(n * 10) / 10);
}

/** "20 × 20 × 20 mm". */
export function formatBounds(b: ModelBounds): string {
  return `${trimmed(b.x)} × ${trimmed(b.y)} × ${trimmed(b.z)} mm`;
}

/** A warning about the model's scale, or null when the size looks plausible. */
export function boundsWarning(b: ModelBounds): string | null {
  const longest = Math.max(b.x, b.y, b.z);
  if (longest < TINY_MODEL_MM) {
    return `This model is only ${formatBounds(b)}. If you exported it in inches, export it in millimetres or scale it up, or the weight estimate will be far too low.`;
  }
  if (longest > HUGE_MODEL_MM) {
    return `This model is ${formatBounds(b)}, which is larger than most printers can build. Check the model's scale, or tell us the size in the note.`;
  }
  return null;
}
