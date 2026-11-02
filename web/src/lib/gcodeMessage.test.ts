import { describe, expect, it } from "vitest";
import { estimateWeightFromVolume, type GcodeResult } from "./engine/index";
import {
  classifyFileKind,
  classifyRequestFileState,
  classifyStoredFile,
  describeDroppedFileHint,
  describeFileStoredOutcome,
  describeGcodeResult,
  describeModelAttachment,
  describeModelParseFailure,
  describeModelTooLarge,
  describeReadError,
  describeStoredModelHint,
  describeVolumeEstimate,
  MAX_MODEL_BYTES,
  MULTIPART_OVERHEAD_BYTES,
  shouldAutoVerifyStoredFile,
} from "./gcodeMessage";

describe("describeGcodeResult", () => {
  it("not-found: lists what was searched, names the file, no silent failure", () => {
    const r: GcodeResult = { status: "not-found", source: "none" };
    const m = describeGcodeResult(r, "blank.gcode", 1.75, 1.24);
    expect(m.kind).toBe("not-found");
    expect(m.text).toContain("blank.gcode");
    expect(m.text).toContain("Cura ;TIME");
    expect(m.text).toContain("estimated printing time");
    expect(m.text).toContain("manually");
  });

  it("invalid-profile: distinct message, not 'nothing found'", () => {
    const r: GcodeResult = { status: "invalid-profile", source: "none", lengthM: 3.2 };
    const m = describeGcodeResult(r, "model.gcode", NaN, 0);
    expect(m.kind).toBe("invalid-profile");
    expect(m.text).toContain("material profile has no usable");
    expect(m.text).not.toContain("No slicer metadata found");
  });

  it("invalid-profile with time present: notes time filled, weight manual", () => {
    const r: GcodeResult = { status: "invalid-profile", source: "none", lengthM: 3.2, timeSeconds: 600 };
    const m = describeGcodeResult(r, "model.gcode", -1, 1.24);
    expect(m.text).toContain("Print time filled automatically");
  });

  it("ok / reported-weight: names slicer and source", () => {
    const r: GcodeResult = { status: "ok", source: "reported-weight", weightG: 12, slicer: "PrusaSlicer" };
    const m = describeGcodeResult(r, "part.gcode", 1.75, 1.24);
    expect(m.kind).toBe("ok");
    expect(m.text).toContain("part.gcode");
    expect(m.text).toContain("PrusaSlicer");
    expect(m.text).toContain("weight reported by slicer");
  });

  it("ok / derived-from-length: shows length, diameter, density", () => {
    const r: GcodeResult = { status: "ok", source: "derived-from-length", weightG: 10.3, lengthM: 3.456 };
    const m = describeGcodeResult(r, "part.gcode", 1.75, 1.24);
    expect(m.text).toContain("3.46 m");
    expect(m.text).toContain("1.75 mm");
    expect(m.text).toContain("1.24 g/cm³");
  });

  it("ok / source none (time-only): explains weight was not found", () => {
    const r: GcodeResult = { status: "ok", source: "none", timeSeconds: 120 };
    const m = describeGcodeResult(r, "part.gcode", 1.75, 1.24);
    expect(m.text).toContain("no weight data in file");
  });

  it("time-only regression: exactly one clause mentions time, not two (the double-time-sentence bug this replaces moonraker.ts's old resolveMoonrakerPrefill/describeMoonrakerPrefill path, which independently named both the time-only case and the time source)", () => {
    const r: GcodeResult = { status: "ok", source: "none", timeSeconds: 120 };
    const m = describeGcodeResult(r, "part.gcode", 1.75, 1.24);
    const timeClauses = m.text.split(" · ").filter((clause) => clause.toLowerCase().includes("time"));
    expect(timeClauses).toHaveLength(1);
  });

  it("ok / reported-weight with no time figure: notes to enter time manually", () => {
    const r: GcodeResult = { status: "ok", source: "reported-weight", weightG: 12 };
    const m = describeGcodeResult(r, "part.gcode", 1.75, 1.24);
    expect(m.text).toContain("no time data found");
    expect(m.text).toContain("enter time manually");
  });

  it("ok / derived-from-length with no time figure: notes to enter time manually", () => {
    const r: GcodeResult = { status: "ok", source: "derived-from-length", weightG: 10.3, lengthM: 3.456 };
    const m = describeGcodeResult(r, "part.gcode", 1.75, 1.24);
    expect(m.text).toContain("no time data found");
    expect(m.text).toContain("enter time manually");
  });

  it("ok / reported-weight with a time figure present: no spurious 'enter time manually' note", () => {
    const r: GcodeResult = { status: "ok", source: "reported-weight", weightG: 12, timeSeconds: 600 };
    const m = describeGcodeResult(r, "part.gcode", 1.75, 1.24);
    expect(m.text).not.toContain("enter time manually");
  });

  it("appends slicer's own cost estimate when present, never with our currency symbol", () => {
    const r: GcodeResult = {
      status: "ok",
      source: "reported-weight",
      weightG: 12,
      costEstimate: 3.5,
    };
    const m = describeGcodeResult(r, "part.gcode", 1.75, 1.24);
    expect(m.text).toContain("cost estimate from slicer: 3.50 (slicer's own units)");
    expect(m.text).not.toContain("$3.50");
  });
});

describe("describeVolumeEstimate", () => {
  it("routes weight through estimateWeightFromVolume, not a local reimplementation", () => {
    const est = describeVolumeEstimate("part.stl", 10, 1.24, 30);
    expect(est.weightG).toBeCloseTo(estimateWeightFromVolume(10, 1.24, 30), 10);
    expect(est.weightG).toBeCloseTo(3.72, 5); // 10 cm3 x 1.24 g/cm3 x 30%
  });

  it("labels the message clearly as an estimate, names the file, and notes it needs adjustment after slicing", () => {
    const est = describeVolumeEstimate("part.stl", 10, 1.24, 30);
    expect(est.message.kind).toBe("ok");
    expect(est.message.text).toContain("part.stl");
    expect(est.message.text).toContain("estimated from model volume");
    expect(est.message.text).toContain("adjust after slicing");
  });

  it("reflects a changed solidity in both the weight and the message", () => {
    const low = describeVolumeEstimate("part.stl", 10, 1.24, 30);
    const high = describeVolumeEstimate("part.stl", 10, 1.24, 80);
    expect(high.weightG).toBeGreaterThan(low.weightG);
    expect(high.message.text).toContain("80%");
  });
});

describe("describeModelParseFailure", () => {
  it("empty: distinct wording from 'malformed', names the file, no silent failure", () => {
    const m = describeModelParseFailure("part.stl", "empty");
    expect(m.kind).toBe("not-found");
    expect(m.text).toContain("part.stl");
    expect(m.text).toContain("empty or degenerate mesh");
    expect(m.text).toContain("manually");
  });

  it("malformed: distinct wording from gcode's 'no slicer metadata' message", () => {
    const m = describeModelParseFailure("part.3mf", "malformed");
    expect(m.kind).toBe("not-found");
    expect(m.text).toContain("part.3mf");
    expect(m.text).not.toContain("No slicer metadata found");
    expect(m.text).toContain("manually");
  });
});

describe("describeModelTooLarge", () => {
  it("names the file, the 64 MiB cap, and offers a manual-entry way forward", () => {
    expect(MAX_MODEL_BYTES).toBe(64 * 1024 * 1024);
    const m = describeModelTooLarge("giant.3mf");
    expect(m.text).toContain("giant.3mf");
    expect(m.text).toContain("64 MiB");
    expect(m.text).toContain("manually");
  });
});

describe("describeModelAttachment", () => {
  const maxUploadBytes = 32 * 1024 * 1024;
  const effectiveMax = maxUploadBytes - MULTIPART_OVERHEAD_BYTES;

  it("within the effective cap: attaches, names the file in the owner text, says so plainly", () => {
    const a = describeModelAttachment("part.stl", 1024, maxUploadBytes, "weight estimated from geometry");
    expect(a.attach).toBe(true);
    expect(a.ownerText).toContain("part.stl");
    expect(a.ownerText).toContain("weight estimated from geometry");
    expect(a.ownerText).toContain("attached");
    expect(a.visitorText).toBe("");
  });

  it("exactly at the effective cap still attaches (only strictly over is oversized)", () => {
    const a = describeModelAttachment("part.stl", effectiveMax, maxUploadBytes, "weight estimated from geometry");
    expect(a.attach).toBe(true);
  });

  it("just past the effective cap, but still under the raw server cap, does not attach (multipart overhead headroom, finding 2)", () => {
    const a = describeModelAttachment("part.stl", effectiveMax + 1, maxUploadBytes, "weight estimated from geometry");
    expect(a.attach).toBe(false);
    expect(effectiveMax + 1).toBeLessThan(maxUploadBytes); // sanity: this file alone is under the raw upload cap
  });

  it("over the upload cap: does not attach; owner text is third-person, visitor text says 'you'", () => {
    const a = describeModelAttachment("giant.3mf", maxUploadBytes + 1, maxUploadBytes, "sliced 3MF, weight/time from the slicer's own prediction");
    expect(a.attach).toBe(false);
    expect(a.ownerText).toContain("giant.3mf");
    expect(a.ownerText).toContain("32 MiB");
    expect(a.ownerText).toContain("was not attached");
    expect(a.ownerText).not.toContain("you");
    expect(a.visitorText).toContain("giant.3mf");
    expect(a.visitorText).toContain("32 MiB");
    expect(a.visitorText).toContain("you");
  });

  it("plugs the caller's detail clause into the owner text, so STL/geometry-3MF and sliced-3MF each read distinctly", () => {
    const sliced = describeModelAttachment("part.3mf", 1024, maxUploadBytes, "sliced 3MF, weight/time from the slicer's own prediction");
    const geometry = describeModelAttachment("part.3mf", 1024, maxUploadBytes, "weight estimated from geometry");
    expect(sliced.ownerText).not.toBe(geometry.ownerText);
  });
});

describe("classifyFileKind", () => {
  it("gcode extensions", () => {
    expect(classifyFileKind("part.gcode")).toBe("gcode");
    expect(classifyFileKind("PART.GCODE")).toBe("gcode");
    expect(classifyFileKind("part.gco")).toBe("gcode");
    expect(classifyFileKind("part.g")).toBe("gcode");
    expect(classifyFileKind("part.nc")).toBe("gcode");
  });

  it("stl and 3mf classify distinctly, not folded into one bucket", () => {
    expect(classifyFileKind("part.stl")).toBe("stl");
    expect(classifyFileKind("PART.3MF")).toBe("3mf");
  });

  it("an unrecognized extension classifies as unknown", () => {
    expect(classifyFileKind("upload.bin")).toBe("unknown");
    expect(classifyFileKind("mystery")).toBe("unknown");
    expect(classifyFileKind("")).toBe("unknown");
  });
});

describe("classifyStoredFile", () => {
  it("gcode extensions classify as gcode", () => {
    expect(classifyStoredFile("part.gcode")).toBe("gcode");
    expect(classifyStoredFile("PART.GCODE")).toBe("gcode");
    expect(classifyStoredFile("part.gco")).toBe("gcode");
    expect(classifyStoredFile("part.g")).toBe("gcode");
    expect(classifyStoredFile("part.nc")).toBe("gcode");
  });

  it("model extensions (stl and 3mf) classify as one 'model' bucket", () => {
    expect(classifyStoredFile("part.stl")).toBe("model");
    expect(classifyStoredFile("PART.3MF")).toBe("model");
  });

  it("an unrecognized extension, including the server's upload.bin fallback, classifies as unknown", () => {
    expect(classifyStoredFile("upload.bin")).toBe("unknown");
    expect(classifyStoredFile("mystery")).toBe("unknown");
    expect(classifyStoredFile("")).toBe("unknown");
  });
});

describe("classifyRequestFileState", () => {
  it("no filename: none, regardless of the other fields", () => {
    expect(classifyRequestFileState("", "", "")).toBe("none");
    expect(classifyRequestFileState("", "/some/path", "2026-01-01")).toBe("none");
  });

  it("filename, real path, never pruned: stored", () => {
    expect(classifyRequestFileState("part.gcode", "/data/uploads/1.gcode.gz", "")).toBe("stored");
  });

  it("filename, prunedAt set: pruned, regardless of gcodePath", () => {
    expect(classifyRequestFileState("part.gcode", "", "2026-01-01T00:00:00Z")).toBe("pruned");
  });

  it("filename, empty path, never pruned: dropped (storage-full accept)", () => {
    expect(classifyRequestFileState("part.stl", "", "")).toBe("dropped");
  });
});

describe("shouldAutoVerifyStoredFile", () => {
  const maxAutoBytes = 8 * 1024 * 1024;

  it("a small stored g-code file auto-verifies", () => {
    expect(shouldAutoVerifyStoredFile("stored", "part.gcode", 1024, maxAutoBytes)).toBe(true);
  });

  it("a small stored STL never auto-verifies: no slicer comments to find (finding 1)", () => {
    expect(shouldAutoVerifyStoredFile("stored", "part.stl", 1024, maxAutoBytes)).toBe(false);
  });

  it("a small stored 3MF never auto-verifies", () => {
    expect(shouldAutoVerifyStoredFile("stored", "part.3mf", 1024, maxAutoBytes)).toBe(false);
  });

  it("an unrecognized extension never auto-verifies (still gets a manual button, just not auto-fired)", () => {
    expect(shouldAutoVerifyStoredFile("stored", "upload.bin", 1024, maxAutoBytes)).toBe(false);
  });

  it("a g-code file at or over the size threshold never auto-verifies", () => {
    expect(shouldAutoVerifyStoredFile("stored", "part.gcode", maxAutoBytes, maxAutoBytes)).toBe(false);
  });

  it("no stored file (empty filename) never auto-verifies", () => {
    expect(shouldAutoVerifyStoredFile("none", "", 0, maxAutoBytes)).toBe(false);
  });

  it("a dropped g-code file never auto-verifies: gcodePath is empty, there's nothing to fetch (finding 1)", () => {
    expect(shouldAutoVerifyStoredFile("dropped", "part.gcode", 1024, maxAutoBytes)).toBe(false);
  });

  it("a pruned g-code file still auto-verifies: unchanged pre-existing behavior, out of scope here", () => {
    expect(shouldAutoVerifyStoredFile("pruned", "part.gcode", 1024, maxAutoBytes)).toBe(true);
  });
});

describe("describeStoredModelHint", () => {
  it("covers both a geometry estimate and sliced-3MF slicer metadata, truthfully for either", () => {
    const text = describeStoredModelHint();
    expect(text).toContain("slicer's own metadata");
    expect(text).toContain("geometry estimate");
    expect(text).not.toContain("estimated from this file's model geometry"); // a sliced 3MF is not described as geometry-only
    expect(text).toContain("no stored g-code to re-verify");
  });
});

describe("describeDroppedFileHint", () => {
  it("explains storage was full, not the visitor's fault, no download/verify implied", () => {
    const text = describeDroppedFileHint();
    expect(text).toContain("upload storage was full");
    expect(text).toContain("no copy to download or verify");
  });
});

describe("describeFileStoredOutcome", () => {
  it("no file attached: never a caveat, regardless of fileStored", () => {
    expect(describeFileStoredOutcome(false, false)).toBeNull();
    expect(describeFileStoredOutcome(false, undefined)).toBeNull();
  });

  it("a file was attached and fileStored is absent or true: no caveat (the common case, a stored file)", () => {
    expect(describeFileStoredOutcome(true, undefined)).toBeNull();
    expect(describeFileStoredOutcome(true, true)).toBeNull();
  });

  it("a file was attached but fileStored is false: a caveat explaining the file wasn't stored", () => {
    const outcome = describeFileStoredOutcome(true, false);
    expect(outcome).not.toBeNull();
    expect(outcome!.text).toContain("couldn't be stored");
  });
});

describe("describeReadError", () => {
  it("names the file and the reason, never fails silently", () => {
    const m = describeReadError("part.gcode", "permission denied");
    expect(m.kind).toBe("read-error");
    expect(m.text).toContain("part.gcode");
    expect(m.text).toContain("permission denied");
    expect(m.text).toContain("manually");
  });
});
