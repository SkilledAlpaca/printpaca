import { describe, expect, it } from "vitest";
import type { EffectiveConfig } from "./engine/index";
import { resolveGcode } from "./engine/index";
import { describeGcodeResult } from "./gcodeMessage";
import {
  MOONRAKER_HISTORY_PATH,
  MOONRAKER_METADATA_PATH,
  fluiddUrl,
  isCompletedJob,
  isValidJob,
  isValidMetadata,
  moonrakerEnabled,
  moonrakerHistoryUrl,
  moonrakerJobToRawMeta,
  moonrakerMetadataUrl,
  moonrakerTimeSource,
  parseCompletedJobs,
  parseHistoryPage,
  filterJobs,
  HISTORY_PAGE_SIZE,
  parseMetadata,
  type MoonrakerJob,
} from "./moonraker";

function job(overrides: Partial<MoonrakerJob> = {}): MoonrakerJob {
  return { job_id: "1", filename: "part.gcode", status: "completed", ...overrides };
}

describe("path constants", () => {
  it("are fixed literals matching the server's pathAllowed allowlist", () => {
    expect(MOONRAKER_HISTORY_PATH).toBe("api/moonraker/server/history/list");
    expect(MOONRAKER_METADATA_PATH).toBe("api/moonraker/server/files/metadata");
  });

  it("moonrakerHistoryUrl defaults to a 50-item newest-first page", () => {
    expect(moonrakerHistoryUrl()).toBe("api/moonraker/server/history/list?limit=50&order=desc");
  });

  it("moonrakerHistoryUrl honors a custom limit", () => {
    expect(moonrakerHistoryUrl(5)).toBe("api/moonraker/server/history/list?limit=5&order=desc");
  });

  it("moonrakerMetadataUrl encodes the filename, never building the base path from user input", () => {
    expect(moonrakerMetadataUrl("a b/c.gcode")).toBe(
      "api/moonraker/server/files/metadata?filename=a%20b%2Fc.gcode",
    );
  });
});

describe("isValidJob", () => {
  it("accepts a well-formed job", () => {
    expect(isValidJob(job())).toBe(true);
  });

  it("rejects null, non-objects, and elements missing job_id/filename/status without throwing", () => {
    const fixtures: unknown[] = [null, undefined, 42, "job", {}, { job_id: "1" }, { job_id: 1, filename: "x", status: "completed" }];
    for (const f of fixtures) {
      expect(() => isValidJob(f)).not.toThrow();
      expect(isValidJob(f)).toBe(false);
    }
  });

  it("filters a mixed-shape array down to only the valid elements", () => {
    const good = job({ job_id: "1" });
    const mixed: unknown[] = [good, null, { notAJob: true }, 42, job({ job_id: "2" })];
    const kept = mixed.filter(isValidJob);
    expect(kept.map((j) => j.job_id)).toEqual(["1", "2"]);
  });
});

describe("isCompletedJob", () => {
  it("true only for status \"completed\"", () => {
    expect(isCompletedJob(job({ status: "completed" }))).toBe(true);
  });

  it("false for canceled/error/in_progress jobs", () => {
    expect(isCompletedJob(job({ status: "cancelled" }))).toBe(false);
    expect(isCompletedJob(job({ status: "error" }))).toBe(false);
    expect(isCompletedJob(job({ status: "in_progress" }))).toBe(false);
  });
});

describe("isValidMetadata", () => {
  it("accepts an object with every optional field present", () => {
    expect(isValidMetadata({ filament_total: 1000, filament_weight_total: 12, estimated_time: 3600 })).toBe(true);
  });

  it("accepts an empty object -- every field is optional", () => {
    expect(isValidMetadata({})).toBe(true);
  });

  it("rejects null and non-objects without throwing", () => {
    for (const f of [null, undefined, 42, "metadata"]) {
      expect(() => isValidMetadata(f)).not.toThrow();
      expect(isValidMetadata(f)).toBe(false);
    }
  });
});

describe("moonrakerJobToRawMeta", () => {
  const diameterMm = 1.75;
  const density = 1.24;

  it("weight: metadata filament_weight_total wins outright, mapped straight to weightG", () => {
    const raw = moonrakerJobToRawMeta(job({ filament_used: 5000 }), { filament_total: 4800, filament_weight_total: 42 });
    expect(raw.weightG).toBe(42);
    expect(raw.lengthM).toBeUndefined();
    const result = resolveGcode(raw, diameterMm, density);
    expect(result.status).toBe("ok");
    expect(result.source).toBe("reported-weight");
  });

  it("weight: falls back to metadata.filament_total (mm), converted to meters, when no reported weight", () => {
    const raw = moonrakerJobToRawMeta(job({ filament_used: 5000 }), { filament_total: 4800 });
    expect(raw.weightG).toBeUndefined();
    expect(raw.lengthM).toBeCloseTo(4.8, 5);
    const result = resolveGcode(raw, diameterMm, density);
    expect(result.status).toBe("ok");
    expect(result.source).toBe("derived-from-length");
    expect(result.weightG).toBeCloseTo((4800 / 1000) * Math.PI * (1.75 / 2) ** 2 * 1.24, 5);
  });

  it("weight: falls back to the job's filament_used (mm) when metadata has no weight or length", () => {
    const raw = moonrakerJobToRawMeta(job({ filament_used: 3000 }), {});
    expect(raw.lengthM).toBeCloseTo(3, 5);
  });

  it("weight: metadata.filament_total takes priority over the job's filament_used when both present", () => {
    const withMetaLength = moonrakerJobToRawMeta(job({ filament_used: 3000 }), { filament_total: 9000 });
    const withJobLengthOnly = moonrakerJobToRawMeta(job({ filament_used: 9000 }), {});
    expect(withMetaLength.lengthM).toBeCloseTo(withJobLengthOnly.lengthM!, 5);
  });

  it("weight: > 0 guard -- a zero or negative filament_weight_total/filament_total/filament_used is treated as absent", () => {
    expect(moonrakerJobToRawMeta(job(), { filament_weight_total: 0 }).weightG).toBeUndefined();
    expect(moonrakerJobToRawMeta(job(), { filament_total: 0 }).lengthM).toBeUndefined();
    expect(moonrakerJobToRawMeta(job({ filament_used: -5 }), {}).lengthM).toBeUndefined();
  });

  it("weight: invalid-profile when only a length figure is available and diameter/density are unusable (via the shared resolveGcode)", () => {
    const raw = moonrakerJobToRawMeta(job({ filament_used: 3000 }), {});
    const result = resolveGcode(raw, 0, density);
    expect(result.status).toBe("invalid-profile");
    expect(result.weightG).toBeUndefined();
  });

  it("weight: neither weightG nor lengthM set when nothing at all is reported", () => {
    const raw = moonrakerJobToRawMeta(job(), {});
    expect(raw.weightG).toBeUndefined();
    expect(raw.lengthM).toBeUndefined();
  });

  it("time: job.print_duration wins over metadata.estimated_time", () => {
    const raw = moonrakerJobToRawMeta(job({ print_duration: 3600 }), { estimated_time: 7200 });
    expect(raw.timeSeconds).toBe(3600);
  });

  it("time: falls back to metadata.estimated_time when print_duration is absent", () => {
    const raw = moonrakerJobToRawMeta(job(), { estimated_time: 7200 });
    expect(raw.timeSeconds).toBe(7200);
  });

  it("time: falls back to metadata.estimated_time when print_duration is zero", () => {
    const raw = moonrakerJobToRawMeta(job({ print_duration: 0 }), { estimated_time: 7200 });
    expect(raw.timeSeconds).toBe(7200);
  });

  it("time: > 0 guard -- a negative estimated_time is treated as absent when print_duration is also absent", () => {
    const raw = moonrakerJobToRawMeta(job(), { estimated_time: -1 });
    expect(raw.timeSeconds).toBeUndefined();
  });

  it("invalid-profile still carries whatever time figure was found (mirrors resolveGcode's own contract)", () => {
    const raw = moonrakerJobToRawMeta(job({ filament_used: 3000, print_duration: 1800 }), {});
    const result = resolveGcode(raw, 0, density);
    expect(result.status).toBe("invalid-profile");
    expect(result.timeSeconds).toBe(1800);
  });

  it("resolveGcode reports not-found when the mapper finds neither weight, length, nor time", () => {
    const raw = moonrakerJobToRawMeta(job(), {});
    const result = resolveGcode(raw, diameterMm, density);
    expect(result.status).toBe("not-found");
  });

  it("resolveGcode reports ok with only a time figure when there's no weight/length data at all", () => {
    const raw = moonrakerJobToRawMeta(job({ print_duration: 1800 }), {});
    const result = resolveGcode(raw, diameterMm, density);
    expect(result.status).toBe("ok");
    expect(result.weightG).toBeUndefined();
    expect(result.timeSeconds).toBe(1800);
  });
});

describe("moonrakerTimeSource", () => {
  it("names \"actual\" when print_duration wins, mirroring moonrakerJobToRawMeta's own priority", () => {
    expect(moonrakerTimeSource(job({ print_duration: 3600 }), { estimated_time: 7200 })).toBe("actual");
  });

  it("names \"slicer-estimate\" when only estimated_time is usable", () => {
    expect(moonrakerTimeSource(job(), { estimated_time: 7200 })).toBe("slicer-estimate");
  });

  it("falls back to \"slicer-estimate\" when print_duration is zero", () => {
    expect(moonrakerTimeSource(job({ print_duration: 0 }), { estimated_time: 7200 })).toBe("slicer-estimate");
  });

  it("undefined when neither field is usable, same >0 guard as moonrakerJobToRawMeta", () => {
    expect(moonrakerTimeSource(job(), { estimated_time: -1 })).toBeUndefined();
    expect(moonrakerTimeSource(job(), {})).toBeUndefined();
  });
});

describe("MoonrakerJob.metadata (inline metadata snapshot)", () => {
  it("isValidMetadata accepts a job's inline metadata field as-is, so RecentPrints can use it without a network fetch", () => {
    const j = job({ metadata: { filament_weight_total: 42, estimated_time: 3600 } });
    expect(isValidMetadata(j.metadata)).toBe(true);
    expect(moonrakerJobToRawMeta(j, j.metadata!)).toEqual({ weightG: 42, timeSeconds: 3600 });
  });

  it("a job with no inline metadata still maps cleanly with an empty metadata object (the fetch-fallback shape)", () => {
    const j = job({ filament_used: 2000, print_duration: 900 });
    expect(j.metadata).toBeUndefined();
    expect(moonrakerJobToRawMeta(j, j.metadata ?? {})).toEqual({ lengthM: 2, timeSeconds: 900 });
  });
});

describe("parseCompletedJobs", () => {
  it("drills into result.jobs and keeps only valid, completed jobs", () => {
    const data = {
      result: {
        count: 3,
        jobs: [job({ job_id: "1", status: "completed" }), job({ job_id: "2", status: "cancelled" }), { bad: true }],
      },
    };
    expect(parseCompletedJobs(data).map((j) => j.job_id)).toEqual(["1"]);
  });

  it("never throws on a malformed/missing body, returns empty array instead", () => {
    for (const bad of [null, undefined, 42, {}, { result: {} }, { result: { jobs: "nope" } }]) {
      expect(() => parseCompletedJobs(bad)).not.toThrow();
      expect(parseCompletedJobs(bad)).toEqual([]);
    }
  });
});

describe("parseMetadata", () => {
  it("drills into result and returns it when well-formed", () => {
    const data = { result: { filament_total: 1000, estimated_time: 3600 } };
    expect(parseMetadata(data)).toEqual({ filament_total: 1000, estimated_time: 3600 });
  });

  it("never throws on a malformed/missing body, returns {} instead", () => {
    for (const bad of [null, undefined, 42, {}, { result: null }, { result: 42 }]) {
      expect(() => parseMetadata(bad)).not.toThrow();
      expect(parseMetadata(bad)).toEqual({});
    }
  });
});

describe("mapper -> resolveGcode -> describeGcodeResult (the shared path RecentPrints uses)", () => {
  const diameterMm = 1.75;
  const density = 1.24;

  it("not-found: tells the user to enter weight and time manually", () => {
    const raw = moonrakerJobToRawMeta(job(), {});
    const result = resolveGcode(raw, diameterMm, density);
    const msg = describeGcodeResult(result, "part.gcode", diameterMm, density);
    expect(msg.kind).toBe("not-found");
    expect(msg.text).toContain("part.gcode");
    expect(msg.text).toContain("manually");
  });

  it("invalid-profile: flags the material profile and still notes an auto-filled time", () => {
    const raw = moonrakerJobToRawMeta(job({ filament_used: 3000, print_duration: 1800 }), {});
    const result = resolveGcode(raw, 0, density);
    const msg = describeGcodeResult(result, "part.gcode", 0, density);
    expect(msg.kind).toBe("invalid-profile");
    expect(msg.text).toContain("material profile");
    expect(msg.text).toContain("Print time filled automatically");
  });

  it("ok: names the weight source, and exactly one clause mentions time (no double time sentence)", () => {
    const raw = moonrakerJobToRawMeta(job({ print_duration: 1800 }), { filament_weight_total: 42 });
    const result = resolveGcode(raw, diameterMm, density);
    const msg = describeGcodeResult(result, "part.gcode", diameterMm, density);
    expect(msg.kind).toBe("ok");
    expect(msg.text).toContain("part.gcode");
    expect(msg.text).toContain("weight reported by slicer");
    const timeClauses = msg.text.split(" · ").filter((c) => c.toLowerCase().includes("time"));
    expect(timeClauses).toHaveLength(0); // weight + time both present, describeGcodeResult adds no time clause at all here
  });
});

describe("moonrakerEnabled / fluiddUrl", () => {
  function cfg(extra: Record<string, unknown>): EffectiveConfig {
    return { ...(extra as object) } as EffectiveConfig;
  }

  it("moonrakerEnabled true only when the config field is exactly true", () => {
    expect(moonrakerEnabled(cfg({ moonraker: true }))).toBe(true);
    expect(moonrakerEnabled(cfg({ moonraker: false }))).toBe(false);
    expect(moonrakerEnabled(cfg({}))).toBe(false);
  });

  it("fluiddUrl returns the configured value or empty string when unset/null config", () => {
    expect(fluiddUrl(cfg({ fluiddUrl: "http://fluidd.local" }))).toBe("http://fluidd.local");
    expect(fluiddUrl(cfg({}))).toBe("");
    expect(fluiddUrl(null)).toBe("");
  });
});

describe("history paging", () => {
  it("moonrakerHistoryUrl adds the start offset only past the first page", () => {
    expect(moonrakerHistoryUrl(50, 0)).not.toContain("start");
    expect(moonrakerHistoryUrl(50, 50)).toBe("api/moonraker/server/history/list?limit=50&order=desc&start=50");
    expect(HISTORY_PAGE_SIZE).toBe(50);
  });

  it("parseHistoryPage counts jobs of every status for the next offset", () => {
    const job = (id: string, status: string) => ({ job_id: id, filename: `${id}.gcode`, status });
    const page = parseHistoryPage({ result: { count: 120, jobs: [job("1", "completed"), job("2", "cancelled"), { bad: true }] } });
    expect(page.jobs.map((j) => j.job_id)).toEqual(["1"]);
    expect(page.rawCount).toBe(3);
    expect(page.total).toBe(120);
  });

  it("parseHistoryPage tolerates a malformed body", () => {
    for (const bad of [null, undefined, 5, "x", {}, { result: null }, { result: { jobs: "no" } }]) {
      expect(parseHistoryPage(bad)).toEqual({ jobs: [], rawCount: 0, total: null });
    }
  });

  it("filterJobs matches file names case-insensitively and keeps all for an empty query", () => {
    const jobs = [
      { job_id: "1", filename: "Benchy.gcode", status: "completed" },
      { job_id: "2", filename: "vase_tall.gcode", status: "completed" },
    ];
    expect(filterJobs(jobs, "")).toHaveLength(2);
    expect(filterJobs(jobs, "  BENCH ").map((j) => j.job_id)).toEqual(["1"]);
    expect(filterJobs(jobs, "zzz")).toEqual([]);
  });
});
