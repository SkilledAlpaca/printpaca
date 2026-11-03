import { describe, expect, it } from "vitest";
import { timezoneOptions } from "./timezones";

describe("timezoneOptions", () => {
  it("puts UTC first when the browser list leaves it out (Chromium)", () => {
    expect(timezoneOptions(() => ["America/Phoenix", "Europe/Paris"])).toEqual(["UTC", "America/Phoenix", "Europe/Paris"]);
  });

  it("lists UTC once when the browser list already has it (Firefox)", () => {
    const list = timezoneOptions(() => ["America/Phoenix", "UTC", "Europe/Paris"]);
    expect(list).toEqual(["UTC", "America/Phoenix", "Europe/Paris"]);
    expect(new Set(list).size).toBe(list.length);
  });

  it("falls back to UTC when Intl.supportedValuesOf is unavailable", () => {
    expect(timezoneOptions(() => { throw new TypeError("not supported"); })).toEqual(["UTC"]);
  });
});
