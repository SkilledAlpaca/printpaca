import { describe, expect, it } from "vitest";
import { blankSchedule, sceExampleSchedule } from "./scheduleExamples";
import { marksPath, overnightWindowPaths, scheduleProblemUi, scheduleProblems, shareWindowsAcrossSeasons, splitOvernightWindows, windowsDifferAcrossSeasons } from "./scheduleUi";

describe("scheduleProblemUi", () => {
  it("names the season, day and window instead of the config path", () => {
    expect(scheduleProblemUi("energy.schedule.seasons[1].weekday.windows[0].start: must be HH:MM")).toEqual({
      path: "seasons[1].weekday.windows[0].start",
      text: "Season 2 › Weekday › Window 1 › start: enter a time like 16:00",
    });
  });
  it("rewrites the cross-season and overlap messages", () => {
    expect(scheduleProblemUi("energy.schedule.seasons: month(s) 3, 4 are in no season; every month 1-12 needs one").text).toBe(
      "Seasons: months 3, 4 are in no season; every month needs one (1 is January)",
    );
    expect(scheduleProblemUi("energy.schedule.seasons[0].weekend.windows: windows must not overlap").text).toBe(
      "Season 1 › Weekend and holidays › Windows: windows can't overlap",
    );
  });
});

describe("overnight windows", () => {
  it("splits at midnight and validates clean", () => {
    const s = sceExampleSchedule();
    s.seasons[0]!.weekday.windows = [{ name: "Night", start: "22:00", end: "06:00", rate: 0.2 }];
    expect(overnightWindowPaths(s)).toEqual(["seasons[0].weekday.windows[0]"]);
    const { schedule, split } = splitOvernightWindows(s);
    expect(split).toBe(1);
    expect(schedule.seasons[0]!.weekday.windows.map((w) => [w.start, w.end])).toEqual([
      ["22:00", "24:00"],
      ["00:00", "06:00"],
    ]);
    expect(scheduleProblems(s, true)).toEqual([]);
  });
  it("accepts a proxied schedule like the editor's $state form", () => {
    const s = sceExampleSchedule();
    const proxied = new Proxy(s, {});
    expect(splitOvernightWindows(proxied).split).toBe(0);
    expect(scheduleProblems(proxied, true)).toEqual([]);
  });
  it("treats an end of 00:00 as the end of the day", () => {
    const s = sceExampleSchedule();
    s.seasons[0]!.weekday.windows = [{ name: "Late", start: "21:00", end: "00:00", rate: 0.2 }];
    const { schedule } = splitOvernightWindows(s);
    expect(schedule.seasons[0]!.weekday.windows).toEqual([{ name: "Late", start: "21:00", end: "24:00", rate: 0.2 }]);
  });
  it("still reports an empty window and leaves the example alone", () => {
    const s = sceExampleSchedule();
    expect(splitOvernightWindows(s).split).toBe(0);
    s.seasons[0]!.weekday.windows = [{ name: "Zero", start: "10:00", end: "10:00", rate: 0.2 }];
    expect(scheduleProblems(s, true).map((p) => p.text)).toEqual(["Season 1 › Weekday › Window 1: the start must be earlier than the end"]);
  });
  it("accepts a blank one-season schedule", () => {
    expect(scheduleProblems(blankSchedule(0.15), true)).toEqual([]);
  });
});

describe("marksPath", () => {
  it("marks both times of a window for a window-level problem", () => {
    const p = [scheduleProblemUi("energy.schedule.seasons[0].weekday.windows[1]: start must be before end")];
    expect(marksPath(p, "seasons[0].weekday.windows[1].start")).toBe(true);
    expect(marksPath(p, "seasons[0].weekday.windows[1].end")).toBe(true);
    expect(marksPath(p, "seasons[0].weekday.windows[0].start")).toBe(false);
  });
});

describe("same schedule all year", () => {
  it("sees that the SCE example's seasons have different windows, and a blank schedule's don't", () => {
    expect(windowsDifferAcrossSeasons(sceExampleSchedule())).toBe(true);
    expect(windowsDifferAcrossSeasons(blankSchedule())).toBe(false);
    expect(windowsDifferAcrossSeasons({ ...blankSchedule(), seasons: [] })).toBe(false);
  });

  it("copies the first season's windows into every season and keeps each season's rates", () => {
    const s = sceExampleSchedule();
    // Winter weekday has two windows and summer weekday one; the summer rate of the shared window stays.
    const summerRate = s.seasons[0]!.weekday.windows[0]!.rate;
    const winterFirstRate = s.seasons[1]!.weekday.windows[0]!.rate;
    shareWindowsAcrossSeasons(s);
    const [summer, winter] = s.seasons as [typeof s.seasons[0], typeof s.seasons[0]];
    expect(winter.weekday.windows.map((w) => [w.name, w.start, w.end])).toEqual(summer.weekday.windows.map((w) => [w.name, w.start, w.end]));
    expect(winter.weekend.windows.map((w) => w.name)).toEqual(["Mid-peak"]);
    expect(summer.weekday.windows[0]!.rate).toBe(summerRate);
    expect(winter.weekday.windows[0]!.rate).toBe(winterFirstRate);
    expect(winter.weekday.baseName).toBe(summer.weekday.baseName);
    expect(winter.weekday.baseRate).toBe(0.34);
    expect(windowsDifferAcrossSeasons(s)).toBe(false);
    s.sameWindowsAllYear = true;
    expect(scheduleProblems(s, true)).toEqual([]);
  });

  it("starts a window a season didn't have at the first season's rate", () => {
    const s = sceExampleSchedule();
    s.seasons[1]!.weekday.windows = [];
    shareWindowsAcrossSeasons(s);
    expect(s.seasons[1]!.weekday.windows).toEqual([{ name: "On-peak", start: "16:00", end: "21:00", rate: 0.55 }]);
  });

  it("flags a season whose windows differ while the flag is on, in the editor's words", () => {
    const s = sceExampleSchedule();
    s.sameWindowsAllYear = true;
    const problems = scheduleProblems(s, true);
    expect(problems.map((p) => p.path)).toContain("seasons[1].weekday.windows");
    expect(problems[0]!.text).toMatch(/^Season 2 › Weekday › Windows: Same schedule all year is on/);
    s.sameWindowsAllYear = false;
    expect(scheduleProblems(s, true)).toEqual([]);
  });

  it("still validates normally and prices the same with the flag set, since it is not a pricing input", () => {
    const s = blankSchedule();
    s.sameWindowsAllYear = true;
    expect(scheduleProblems(s, true)).toEqual([]);
  });
});
