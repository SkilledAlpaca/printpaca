import { estimateWeightFromVolume, MAX_MODEL_GEOMETRY_BYTES, type GcodeResult } from "./engine/index";

/** Mirrors GcodeResult["status"] plus "read-error" (file read failures before a result exists).
 * "invalid-profile" is a config problem (no usable diameter/density), not "nothing found". */
type GcodeMessageKind = GcodeResult["status"] | "read-error";

export interface GcodeMessage {
  kind: GcodeMessageKind;
  text: string;
}

/** File read or permission failures are never silent; they surface with the same explicit-message contract
 * as "not-found". */
export function describeReadError(fileName: string, reason: string): GcodeMessage {
  return {
    kind: "read-error",
    text: `Couldn't read ${fileName}: ${reason}. Enter weight and time manually.`,
  };
}

// STL/3MF parsing buffers the whole file (no streaming), so refuse anything a tab can't hold. Shared by the
// admin GcodeDrop and the public quote page.
export const MAX_MODEL_BYTES = 64 * 1024 * 1024;

/** Checked against file.size before the bytes are read, so the refusal is instant. */
export function describeModelTooLarge(fileName: string): GcodeMessage {
  const maxMiB = Math.round(MAX_MODEL_BYTES / (1024 * 1024));
  return {
    kind: "read-error",
    text: `${fileName} is larger than ${maxMiB} MiB, too large for your browser to parse. Try a smaller export or enter the values manually.`,
  };
}

export interface VolumeEstimate {
  weightG: number;
  message: GcodeMessage;
}

/** Weight-from-geometry estimate for STL / unsliced 3MF drops, shared by both drop zones via
 * estimateWeightFromVolume. Time is left manual. */
export function describeVolumeEstimate(
  fileName: string,
  volumeCm3: number,
  density: number,
  solidityPct: number,
): VolumeEstimate {
  const weightG = estimateWeightFromVolume(volumeCm3, density, solidityPct);
  return {
    weightG,
    message: {
      kind: "ok",
      text:
        `${fileName} · weight estimated from model volume (${volumeCm3.toFixed(2)} cm³ × ` +
        `${density.toFixed(2)} g/cm³ × ${solidityPct}% solidity) · adjust after slicing`,
    },
  };
}

/** STL/3MF geometry parse failures, worded apart from "no slicer metadata". "too-large" is its own reason
 * (a lower-poly export can fix it), not a corrupt file. */
export function describeModelParseFailure(fileName: string, reason: "empty" | "malformed" | "too-large"): GcodeMessage {
  if (reason === "too-large") {
    const maxGeometryMiB = Math.round(MAX_MODEL_GEOMETRY_BYTES / (1024 * 1024));
    return {
      kind: "not-found",
      text:
        `${fileName}'s model geometry is larger than this browser tab can parse (over ${maxGeometryMiB} MiB of decompressed ` +
        `mesh data). Try a lower-poly or decimated export, or enter weight and time manually.`,
    };
  }
  return {
    kind: "not-found",
    text:
      reason === "empty"
        ? `${fileName} has no usable geometry (empty or degenerate mesh). Enter weight and time manually.`
        : `Couldn't parse ${fileName} as STL or 3MF. Enter weight and time manually.`,
  };
}

// The 32 MiB server cap (publicMaxUploadBytes, server/public.go) covers the whole multipart body, not just
// the file, so a file near the cap could pass here and still 413. This headroom is subtracted from
// maxUploadBytes before every comparison so both callers share one margin.
export const MULTIPART_OVERHEAD_BYTES = 16 * 1024;

export interface ModelAttachment {
  attach: boolean;
  /** Shown to the visitor near the drop zone only when attach is false ("" otherwise); UI copy addressed to
   * "you", never folded into the note. */
  visitorText: string;
  /** Appended to the request note at submit time either way, so the owner knows a model file produced the
   * weight/time. Third person: the owner reads it, never the visitor. */
  ownerText: string;
}

/** Decides whether the parsed File rides along in the "gcode" field (when it fits the server cap minus
 * MULTIPART_OVERHEAD_BYTES) and phrases the note either way. An oversized file degrades to the client-side
 * estimate instead of blocking submission. `detail` carries the source-specific clause. */
export function describeModelAttachment(fileName: string, fileSize: number, maxUploadBytes: number, detail: string): ModelAttachment {
  const effectiveMax = Math.max(0, maxUploadBytes - MULTIPART_OVERHEAD_BYTES);
  if (fileSize > effectiveMax) {
    const maxMiB = Math.round(maxUploadBytes / (1024 * 1024));
    return {
      attach: false,
      visitorText: `${fileName} is larger than the ${maxMiB} MiB upload limit, so it wasn't attached to your request. You may be asked to send it separately.`,
      ownerText: `Model file: ${fileName} (${detail}); the file exceeded the ${maxMiB} MiB upload limit and was not attached.`,
    };
  }
  return { attach: true, visitorText: "", ownerText: `Model file: ${fileName} (${detail}, attached).` };
}

// Filename-extension classification: the one place deciding gcode vs stl vs 3mf vs unknown, shared by fresh
// drops and stored filenames so they can't drift.

export type FileExtensionKind = "gcode" | "stl" | "3mf" | "unknown";

const GCODE_EXTENSIONS = [".gcode", ".gco", ".g", ".nc"];

export function classifyFileKind(filename: string): FileExtensionKind {
  const lower = filename.toLowerCase();
  if (GCODE_EXTENSIONS.some((ext) => lower.endsWith(ext))) return "gcode";
  if (lower.endsWith(".stl")) return "stl";
  if (lower.endsWith(".3mf")) return "3mf";
  return "unknown";
}

// Stored-request-file classification (RequestsTab). A stored file may be g-code, STL or 3MF, so "has a file"
// does not mean "has slicer comments to re-parse". stl and 3mf collapse into "model"; server/public.go's
// "upload.bin" fallback stays "unknown".

export type StoredFileKind = "gcode" | "model" | "unknown";

export function classifyStoredFile(filename: string): StoredFileKind {
  const kind = classifyFileKind(filename);
  if (kind === "gcode") return "gcode";
  if (kind === "unknown") return "unknown";
  return "model"; // stl or 3mf
}

/** Auto-verify (verifyRequest replays parseGcodeText/resolveGcode) only works on slicer-comment text, so it
 * fires only for classifyStoredFile === "gcode"; an unrecognized extension still gets a manual Verify
 * button. It also never fires for a "dropped" file (storage-full submission, no file written):
 * fetchRequestGcode would 404. "pruned" still auto-fires and surfaces the download error. */
export function shouldAutoVerifyStoredFile(
  fileState: RequestFileState,
  filename: string,
  bytes: number,
  maxAutoBytes: number,
): boolean {
  if (fileState === "dropped") return false;
  return filename !== "" && bytes < maxAutoBytes && classifyStoredFile(filename) === "gcode";
}

// A stored request's file state, from the fields server/requests.go persists: "none" (no gcodeFilename);
// "pruned" (gcodePrunedAt set, retention removed the bytes); "dropped" (filename, empty gcodePath and
// gcodePrunedAt: accepted while uploads were at their cap, file never written); "stored" (filename and
// gcodePath). One pure classifier keeps the encoding in one place.

export type RequestFileState = "none" | "stored" | "pruned" | "dropped";

export function classifyRequestFileState(gcodeFilename: string, gcodePath: string, gcodePrunedAt: string): RequestFileState {
  if (gcodeFilename === "") return "none";
  if (gcodePrunedAt !== "") return "pruned";
  if (gcodePath === "") return "dropped";
  return "stored";
}

/** Neutral hint for a stored STL/3MF. Covers both an unsliced model (geometry estimate) and a sliced 3MF
 * (slicer metadata), which the extension can't distinguish. Silent on time: an unsliced model's time is
 * typed by the visitor. */
export function describeStoredModelHint(): string {
  return (
    "Weight came from the uploaded model file itself: either the slicer's own metadata, if it was already " +
    "sliced, or a geometry estimate, computed in the visitor's browser. There's no stored g-code to re-verify it against."
  );
}

/** Explains the "dropped" state: the request went through but CONFIG_DIR/uploads was at its cap
 * (publicUploadsCapBytes). A plain hint, not an alert, since the storage banner is where capacity is acted
 * on. */
export function describeDroppedFileHint(): string {
  return (
    "This file was sent with the request, but visitor upload storage was full at that moment, so it couldn't be " +
    "stored. There's no copy to download or verify; the weight and time above are what the visitor's browser computed."
  );
}

// Submission outcome: server/public.go's storage-full path accepts a submission without its file instead of
// 507ing, signaled by fileStored: false in the 201 body.

export interface FileStoredOutcome {
  text: string;
}

/** Whether the confirmation needs a caveat about their file: only when a file was sent and the server
 * reports it wasn't stored. */
export function describeFileStoredOutcome(hadFile: boolean, fileStored: boolean | undefined): FileStoredOutcome | null {
  if (!hadFile || fileStored !== false) return null;
  return {
    text: "Your request was submitted, but the file itself couldn't be stored right now. You may be asked to send it another way.",
  };
}

/** Single source of truth so the "nothing found" message matches what gcode.ts scans for. */
const SEARCHED_PATTERNS =
  "Cura ;TIME / ;Filament used:, PrusaSlicer/Orca 'filament used [g]' / 'estimated printing time', " +
  "and 'total estimated time' comment headers";

export function describeGcodeResult(
  result: GcodeResult,
  fileName: string,
  diameterMm: number,
  density: number,
): GcodeMessage {
  if (result.status === "not-found") {
    return {
      kind: "not-found",
      text: `No slicer metadata found in ${fileName}. Looked for ${SEARCHED_PATTERNS}. Enter weight and time manually.`,
    };
  }

  if (result.status === "invalid-profile") {
    const timeNote =
      result.timeSeconds !== undefined
        ? "Print time filled automatically; enter weight manually."
        : "Enter weight and time manually.";
    return {
      kind: "invalid-profile",
      text:
        `Filament length found in ${fileName}, but the current material profile has no usable ` +
        `diameter/density; check material settings. ${timeNote}`,
    };
  }

  const parts: string[] = [fileName];
  if (result.slicer) parts.push(result.slicer);
  const weightFound = result.source === "reported-weight" || result.source === "derived-from-length";
  if (result.source === "reported-weight") {
    parts.push("weight reported by slicer");
  } else if (result.source === "derived-from-length") {
    parts.push(
      `weight derived from ${(result.lengthM ?? 0).toFixed(2)} m of filament ` +
        `(${diameterMm} mm ⌀, density ${density.toFixed(2)} g/cm³)`,
    );
  } else if (result.timeSeconds !== undefined) {
    parts.push("print time found; no weight data in file; enter weight manually");
  }
  // Mirror of the "weight found, no time" note above: weight alone can leave time unfilled too (GcodeDrop and
  // the Moonraker picker share this function).
  if (weightFound && result.timeSeconds === undefined) {
    parts.push("no time data found; enter time manually");
  }
  if (result.costEstimate !== undefined) {
    parts.push(`cost estimate from slicer: ${result.costEstimate.toFixed(2)} (slicer's own units)`);
  }
  return { kind: "ok", text: parts.join(" · ") };
}
