import { describe, expect, it } from "vitest";
import { TIMEZONES, deviceTimezone, filterTimezones } from "./timezones";

describe("TIMEZONES", () => {
  it("starts with UTC and has no duplicates", () => {
    expect(TIMEZONES[0]).toBe("UTC");
    expect(new Set(TIMEZONES).size).toBe(TIMEZONES.length);
    expect(TIMEZONES).toContain("America/Los_Angeles");
  });
});

describe("filterTimezones", () => {
  it("lists everything for an empty query", () => {
    expect(filterTimezones("  ")).toHaveLength(TIMEZONES.length);
  });

  it("matches a partial name, ignoring case", () => {
    expect(filterTimezones("Los")).toContain("America/Los_Angeles");
    expect(filterTimezones("los")).toEqual(filterTimezones("LOS"));
  });

  it("treats a space like an underscore", () => {
    expect(filterTimezones("new york")).toContain("America/New_York");
  });

  it("filters by region prefix", () => {
    const r = filterTimezones("America/");
    expect(r.length).toBeGreaterThan(50);
    expect(r.every((z) => z.startsWith("America/"))).toBe(true);
  });

  it("puts a city prefix match before a substring match", () => {
    const r = filterTimezones("york");
    expect(r[0]).toBe("America/New_York");
  });

  it("returns nothing for a name that is not a zone", () => {
    expect(filterTimezones("Mars/Phobos")).toEqual([]);
  });
});

describe("deviceTimezone", () => {
  it("returns a non-empty name in Node", () => {
    expect(deviceTimezone()).not.toBe("");
  });
});
