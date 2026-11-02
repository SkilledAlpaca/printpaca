import { describe, expect, it, vi } from "vitest";
import { isoDate } from "./PrintView.svelte";

// printedOn/validUntil must render the LOCAL calendar date the Date was built from, not the UTC date.
describe("isoDate", () => {
  it("formats a UTC instant's date directly when local time doesn't shift the day", () => {
    expect(isoDate(new Date("2026-01-15T12:00:00Z"))).toBe("2026-01-15");
  });

  it("renders the local day, not the UTC day, at a UTC- boundary (18:00 local == next day UTC)", () => {
    const originalTz = process.env.TZ;
    process.env.TZ = "Etc/GMT+7"; // UTC-7 (POSIX TZ names invert the sign)
    vi.useFakeTimers();
    try {
      vi.setSystemTime(new Date("2026-01-15T01:00:00Z")); // 2026-01-14 18:00 local
      expect(isoDate(new Date())).toBe("2026-01-14");
    } finally {
      vi.useRealTimers();
      process.env.TZ = originalTz;
    }
  });

  it("renders the local day, not the UTC day, at a UTC+ boundary (09:00 local == previous day UTC)", () => {
    const originalTz = process.env.TZ;
    process.env.TZ = "Pacific/Apia"; // UTC+13
    vi.useFakeTimers();
    try {
      vi.setSystemTime(new Date("2026-01-14T20:00:00Z")); // 2026-01-15 09:00 local
      expect(isoDate(new Date())).toBe("2026-01-15");
    } finally {
      vi.useRealTimers();
      process.env.TZ = originalTz;
    }
  });
});
