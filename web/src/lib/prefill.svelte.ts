import type { GcodeResult } from "./engine/index";
import type { PartFormState } from "./state.svelte";
import { splitSeconds } from "./gcodeTime";

/** Which picker last wrote weightG/timeHours/timeMinutes: GcodeDrop's file parse, GcodeDrop's model-volume
 * estimate, or RecentPrints' Moonraker pick. A fresh apply supersedes the others; only the owner's message
 * shows. */
export type PrefillSource = "gcode" | "moonraker" | "model";

/** The values an apply just wrote, so the owning picker can flag its message stale once weight/time is
 * hand-edited or the printer/material (diameter/density) changes. */
interface FilledSnapshot {
  weightG: number | null;
  timeHours: number | null;
  timeMinutes: number | null;
  printerId: string;
  materialId: string;
}

interface PrefillState {
  source: PrefillSource;
  label: string;
  snapshot: FilledSnapshot;
}

/** Prefill records keyed by part id, so a drop onto Part 2 never claims Part 1's values. Boxed (like
 * settingsDirty.svelte.ts) because an exported `let` can't be reassigned from an importing module. Records
 * for removed parts are harmless. */
export const prefill: { byPart: Record<string, PrefillState | undefined> } = $state({ byPart: {} });

export function prefillFor(part: { id: string }): PrefillState | null {
  return prefill.byPart[part.id] ?? null;
}

type PrefillTarget = PartFormState & { id: string };

/** Writes a resolved gcode/Moonraker result into a part's weight/time fields and records the owning picker;
 * the only place this rounding and splitSeconds write happens. A no-op when `resolved` has neither figure,
 * so the previous owner keeps its snapshot. */
export function applyResolvedPrefill(part: PrefillTarget, resolved: GcodeResult, source: PrefillSource, label: string): void {
  if (resolved.weightG === undefined && resolved.timeSeconds === undefined) return;

  if (resolved.weightG !== undefined) part.weightG = Math.round(resolved.weightG * 100) / 100;
  if (resolved.timeSeconds !== undefined) {
    const { hours, minutes } = splitSeconds(resolved.timeSeconds);
    part.timeHours = hours;
    part.timeMinutes = minutes;
  }

  prefill.byPart[part.id] = {
    source,
    label,
    snapshot: {
      weightG: part.weightG,
      timeHours: part.timeHours,
      timeMinutes: part.timeMinutes,
      printerId: part.printerId,
      materialId: part.materialId,
    },
  };
}

/** True once the part drifted from the snapshot an apply wrote (hand edit, or a printer/material switch).
 * False while nothing has been applied. */
export function isPrefillStale(part: PrefillTarget): boolean {
  const snapshot = prefill.byPart[part.id]?.snapshot;
  if (!snapshot) return false;
  return (
    part.weightG !== snapshot.weightG ||
    part.timeHours !== snapshot.timeHours ||
    part.timeMinutes !== snapshot.timeMinutes ||
    part.printerId !== snapshot.printerId ||
    part.materialId !== snapshot.materialId
  );
}

/** Clears a part's record only when `source` currently holds it, so a fresh attempt that finds nothing
 * doesn't leave a stale snapshot claiming ownership. A no-op when the other picker owns it. */
export function clearPrefillIfOwnedBy(part: { id: string }, source: PrefillSource): void {
  if (prefill.byPart[part.id]?.source === source) prefill.byPart[part.id] = undefined;
}
