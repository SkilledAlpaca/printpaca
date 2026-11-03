import { describe, expect, it } from "vitest";
import { blankSchedule, sceExampleSchedule } from "./scheduleExamples";
import { marksPath, overnightWindowPaths, scheduleProblemUi, scheduleProblems, splitOvernightWindows } from "./scheduleUi";

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
