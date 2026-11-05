import { describe, it, expect } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import {
  nextStartWall,
  scheduleEnergy,
  validateEnergySchedule,
  wallFromInstant,
  wallToInstant,
  parseWallStart,
  type EnergyPeriod,
  type EnergySchedule,
} from "./schedule";
import { sceExampleSchedule } from "../scheduleExamples";

// testdata/schedule-vectors.json is shared with server/energy_schedule_test.go; both must produce the same
// numbers. Regenerate it from the Go test (see the file's description), never by hand.
interface Vector {
  name: string;
  schedule: string;
  timezone: string;
  start: string;
  hoursPerUnit: number;
  quantity: number;
  watts: number;
  holidays?: string[];
  expected: { total: number; periods: EnergyPeriod[] };
}
interface Fixture {
  schedules: Record<string, EnergySchedule>;
  vectors: Vector[];
  nextStart: { name: string; clock: string; timezone: string; now: string; expected: string }[];
}
const fx = JSON.parse(readFileSync(resolve(__dirname, "../../../../testdata/schedule-vectors.json"), "utf8")) as Fixture;

describe("schedule vectors shared with the Go server", () => {
  it("has the required coverage", () => {
    expect(fx.vectors.length).toBeGreaterThanOrEqual(10);
    const names = fx.vectors.map((v) => v.name).join("|");
    for (const word of ["midnight", "season", "DST", "holiday", "quantity"]) expect(names).toContain(word);
  });

  for (const v of fx.vectors) {
    it(v.name, () => {
      const sched = { ...fx.schedules[v.schedule]!, ...(v.holidays ? { holidays: v.holidays } : {}) };
      const got = scheduleEnergy(sched, v.timezone, v.start, v.hoursPerUnit * v.quantity, v.watts / 1000)!;
      expect(got).not.toBeNull();
      expect(got.total).toBeCloseTo(v.expected.total, 9);
      expect(got.periods.map((p) => p.name)).toEqual(v.expected.periods.map((p) => p.name));
      got.periods.forEach((p, i) => {
        expect(p.hours).toBeCloseTo(v.expected.periods[i]!.hours, 9);
        expect(p.cost).toBeCloseTo(v.expected.periods[i]!.cost, 9);
      });
    });
  }

  for (const n of fx.nextStart) {
    it(`next start: ${n.name}`, () => {
      expect(nextStartWall(n.clock, n.timezone, new Date(n.now))).toBe(n.expected);
    });
  }
});

describe("the SCE example", () => {
  it("equals the fixture's sce schedule and validates", () => {
    expect(sceExampleSchedule()).toEqual(fx.schedules.sce);
    expect(validateEnergySchedule(sceExampleSchedule(), true)).toEqual([]);
  });
});

describe("time zone helpers", () => {
  it("resolves DST edges deterministically", () => {
    const tz = "America/Los_Angeles";
    // 2026-03-08 02:30 does not exist; it clamps to the first instant after the gap.
    expect(wallFromInstant(tz, wallToInstant(tz, 2026, 3, 8, 150))).toBe("2026-03-08T03:00");
    expect(wallToInstant(tz, 2026, 3, 8, 150)).toBe(Date.UTC(2026, 2, 8, 10, 0));
    // 2026-11-01 01:30 happens twice; the earlier (PDT) instant wins.
    expect(wallToInstant(tz, 2026, 11, 1, 90)).toBe(Date.UTC(2026, 10, 1, 8, 30));
  });
  it("rejects impossible starts", () => {
    expect(parseWallStart("2026-02-30T10:00", "UTC")).toBeNull();
    expect(parseWallStart("2026-02-10 10:00", "UTC")).toBeNull();
    expect(scheduleEnergy(sceExampleSchedule(), "UTC", "nope", 1, 1)).toBeNull();
    expect(scheduleEnergy(sceExampleSchedule(), "Mars/Base", "2026-02-10T10:00", 1, 1)).toBeNull();
  });
});

describe("validateEnergySchedule", () => {
  const base = () => structuredClone(sceExampleSchedule());
  const cases: [string, (s: EnergySchedule) => void, string][] = [
    ["month in no season", (s) => s.seasons[1]!.months.shift(), "month(s) 1 are in no season"],
    ["month in two seasons", (s) => s.seasons[1]!.months.push(6), "month 6 is in both"],
    ["month out of range", (s) => s.seasons[0]!.months.push(13), "13 is not a month"],
    ["overlap", (s) => s.seasons[1]!.weekday.windows.push({ name: "X", start: "15:00", end: "17:00", rate: 1 }), "must not overlap"],
    ["start not before end", (s) => (s.seasons[0]!.weekday.windows[0]!.start = "21:00"), "start must be before end"],
    ["bad time", (s) => (s.seasons[0]!.weekday.windows[0]!.start = "4pm"), "start: must be HH:MM"],
    ["24:00 start", (s) => (s.seasons[0]!.weekday.windows[0]!.start = "24:00"), "start: must be HH:MM"],
    ["negative base rate", (s) => (s.seasons[0]!.weekday.baseRate = -1), "baseRate"],
    ["negative window rate", (s) => (s.seasons[0]!.weekday.windows[0]!.rate = -0.1), "rate: must be >= 0"],
    ["bad holiday", (s) => (s.holidays = ["2026-02-30"]), "holidays[0]"],
    ["bad default start", (s) => (s.defaultStartTime = "25:00"), "defaultStartTime"],
    ["missing base name", (s) => (s.seasons[0]!.weekend.baseName = " "), "baseName: required"],
    [
      "too many seasons",
      (s) => {
        while (s.seasons.length <= 6) s.seasons.push(structuredClone(s.seasons[0]!));
      },
      "exceeds max 6",
    ],
    [
      "too many windows",
      (s) => {
        s.seasons[0]!.weekday.windows = Array.from({ length: 9 }, () => ({ name: "w", start: "00:00", end: "01:00", rate: 1 }));
      },
      "exceeds max 8",
    ],
    [
      "too many holidays",
      (s) => {
        s.holidays = Array.from({ length: 101 }, () => "2026-01-01");
      },
      "holidays: 101",
    ],
  ];
  for (const [name, mutate, want] of cases) {
    it(name, () => {
      const s = base();
      mutate(s);
      expect(validateEnergySchedule(s, true).join("\n")).toContain(want);
    });
  }
  it("accepts end 24:00 and an unused empty schedule", () => {
    const s = base();
    s.seasons[0]!.weekday.windows[0]!.end = "24:00";
    expect(validateEnergySchedule(s, true)).toEqual([]);
    expect(validateEnergySchedule({ defaultStartTime: "22:00", holidays: [], seasons: [] }, false)).toEqual([]);
    expect(validateEnergySchedule({ defaultStartTime: "22:00", holidays: [], seasons: [] }, true)).not.toEqual([]);
  });
});

describe("sameWindowsAllYear", () => {
  const shared = (): EnergySchedule => {
    const day = (rate: number) => ({ baseName: "Off-peak", baseRate: rate, windows: [{ name: "Peak", start: "16:00", end: "21:00", rate: rate * 2 }] });
    return {
      defaultStartTime: "22:00",
      holidays: [],
      sameWindowsAllYear: true,
      seasons: [
        { name: "Summer", months: [6, 7, 8, 9], weekday: day(0.3), weekend: day(0.3) },
        { name: "Winter", months: [1, 2, 3, 4, 5, 10, 11, 12], weekday: day(0.2), weekend: day(0.2) },
      ],
    };
  };

  it("accepts the same windows with different rates, and does not constrain a schedule without the flag", () => {
    expect(validateEnergySchedule(shared(), true)).toEqual([]);
    const sce = sceExampleSchedule();
    expect(validateEnergySchedule({ ...sce, sameWindowsAllYear: false }, true)).toEqual([]);
  });

  it("rejects a season whose windows differ from the first season's, naming the season", () => {
    const sce = { ...sceExampleSchedule(), sameWindowsAllYear: true };
    const got = validateEnergySchedule(sce, true).join("\n");
    expect(got).toContain("energy.schedule.seasons[1].weekday.windows: sameWindowsAllYear");
    const s = shared();
    s.seasons[1]!.weekend.windows[0]!.end = "22:00";
    expect(validateEnergySchedule(s, true).join("\n")).toContain("seasons[1].weekend.windows");
    const t = shared();
    t.seasons[1]!.weekday.baseName = "Standard";
    expect(validateEnergySchedule(t, true)).not.toEqual([]);
  });

  it("prices the same with the flag on or off", () => {
    const s = shared();
    const off = { ...shared(), sameWindowsAllYear: false };
    for (const start of ["2026-07-03T14:00", "2026-01-14T15:00", "2026-09-30T20:00"]) {
      expect(scheduleEnergy(s, "America/Los_Angeles", start, 5, 1)).toEqual(scheduleEnergy(off, "America/Los_Angeles", start, 5, 1));
    }
  });
});
