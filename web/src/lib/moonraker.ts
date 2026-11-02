import type { EffectiveConfig, RawGcodeMeta } from "./engine/index";

/** Fixed proxy paths (server/proxy.go's pathAllowed exact-matches these two). Never build a Moonraker path
 * from user input; only the metadata filename is appended, as an encoded query value. */
export const MOONRAKER_HISTORY_PATH = "api/moonraker/server/history/list";
export const MOONRAKER_METADATA_PATH = "api/moonraker/server/files/metadata";

/** Newest first, capped at a small page: a "recent completed prints" picker, not a history browser. */
export function moonrakerHistoryUrl(limit = 20): string {
  return `${MOONRAKER_HISTORY_PATH}?limit=${limit}&order=desc`;
}

export function moonrakerMetadataUrl(filename: string): string {
  return `${MOONRAKER_METADATA_PATH}?filename=${encodeURIComponent(filename)}`;
}

/** Moonraker job history entry (subset used). Measurement fields are optional because Moonraker can omit
 * them (errored jobs, old entries). `metadata` is the slicer snapshot carried inline on the row; when
 * well-formed the picker uses it instead of an extra fetch. */
export interface MoonrakerJob {
  job_id: string;
  filename: string;
  status: string;
  filament_used?: number; // mm
  print_duration?: number; // s, actual time spent printing
  total_duration?: number; // s, includes pauses; not used for prefill
  end_time?: number; // unix seconds
  metadata?: MoonrakerMetadata;
}

/** Slicer metadata for one file (subset used); every field is optional and validated defensively, like
 * spoolman.ts's SpoolDto. */
export interface MoonrakerMetadata {
  filament_total?: number; // mm
  filament_weight_total?: number; // g
  estimated_time?: number; // s
}

/** Drills into `{ result: { jobs: [...] } }`, dropping malformed or non-completed jobs; never throws on a
 * bad body. */
export function parseCompletedJobs(data: unknown): MoonrakerJob[] {
  const raw = data as { result?: { jobs?: unknown } } | null;
  const rawJobs = raw && typeof raw === "object" ? raw.result?.jobs : undefined;
  if (!Array.isArray(rawJobs)) return [];
  return rawJobs.filter(isValidJob).filter(isCompletedJob);
}

/** Drills into `{ result: {...} }` for the metadata endpoint; an empty object rather than a throw on a
 * missing or malformed body. */
export function parseMetadata(data: unknown): MoonrakerMetadata {
  const raw = data as { result?: unknown } | null;
  const result = raw && typeof raw === "object" ? raw.result : undefined;
  return isValidMetadata(result) ? result : {};
}

/** Guard for one history element: a record missing job_id/filename/status must not TypeError the picker. */
export function isValidJob(j: unknown): j is MoonrakerJob {
  if (typeof j !== "object" || j === null) return false;
  const r = j as Record<string, unknown>;
  return typeof r.job_id === "string" && typeof r.filename === "string" && typeof r.status === "string";
}

/** Only completed jobs are offered; a canceled or errored job's filament/time figures don't describe a
 * finished print. */
export function isCompletedJob(j: MoonrakerJob): boolean {
  return j.status === "completed";
}

/** Every metadata field is optional, so only a non-object body needs guarding. */
export function isValidMetadata(m: unknown): m is MoonrakerMetadata {
  return typeof m === "object" && m !== null;
}

/** Maps a completed job plus its metadata to the RawGcodeMeta resolveGcode consumes for a dropped .gcode,
 * so RecentPrints runs the same resolveGcode and describeGcodeResult path as GcodeDrop instead of a forked
 * one. Weight: metadata.filament_weight_total (grams, > 0) goes to weightG; else a length (> 0) becomes
 * lengthM in meters (Moonraker reports mm), preferring metadata.filament_total (the slicer's figure) over
 * the job's filament_used. Time: the job's measured print_duration (> 0) wins over metadata's
 * estimated_time (> 0). */
export function moonrakerJobToRawMeta(job: MoonrakerJob, metadata: MoonrakerMetadata): RawGcodeMeta {
  const raw: RawGcodeMeta = {};

  if (metadata.filament_weight_total !== undefined && metadata.filament_weight_total > 0) {
    raw.weightG = metadata.filament_weight_total;
  } else {
    const lengthMm =
      metadata.filament_total !== undefined && metadata.filament_total > 0
        ? metadata.filament_total
        : job.filament_used !== undefined && job.filament_used > 0
          ? job.filament_used
          : undefined;
    if (lengthMm !== undefined) raw.lengthM = lengthMm / 1000;
  }

  if (job.print_duration !== undefined && job.print_duration > 0) {
    raw.timeSeconds = job.print_duration;
  } else if (metadata.estimated_time !== undefined && metadata.estimated_time > 0) {
    raw.timeSeconds = metadata.estimated_time;
  }

  return raw;
}

/** Which field moonrakerJobToRawMeta drew timeSeconds from, so a caller can name the source in a
 * confirmation. undefined exactly when timeSeconds is left unset. */
export function moonrakerTimeSource(
  job: MoonrakerJob,
  metadata: MoonrakerMetadata,
): "actual" | "slicer-estimate" | undefined {
  if (job.print_duration !== undefined && job.print_duration > 0) return "actual";
  if (metadata.estimated_time !== undefined && metadata.estimated_time > 0) return "slicer-estimate";
  return undefined;
}

/** GET /api/config's `moonraker`/`fluiddUrl` keys (server/main.go), typed on EffectiveConfig. */
export function moonrakerEnabled(cfg: EffectiveConfig): boolean {
  return cfg.moonraker === true;
}

export function fluiddUrl(cfg: EffectiveConfig | null): string {
  return cfg?.fluiddUrl ?? "";
}
