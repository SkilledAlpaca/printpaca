import { describe, expect, it } from "vitest";
import { applyTransition, displayStatus, statusSortWeight } from "./quoteStatus";

const DAY = 86_400_000;

describe("displayStatus", () => {
  it("returns the stored status when not expired", () => {
    const now = Date.parse("2026-01-05T00:00:00.000Z");
    expect(displayStatus({ status: "draft", savedAt: "2026-01-01T00:00:00.000Z" }, 30, now)).toBe("draft");
    expect(displayStatus({ status: "sent", savedAt: "2026-01-01T00:00:00.000Z" }, 30, now)).toBe("sent");
  });

  it("derives expired for a draft past validDays", () => {
    const savedAt = "2026-01-01T00:00:00.000Z";
    const now = Date.parse(savedAt) + 31 * DAY;
    expect(displayStatus({ status: "draft", savedAt }, 30, now)).toBe("expired");
  });

  it("derives expired for a sent quote past validDays", () => {
    const savedAt = "2026-01-01T00:00:00.000Z";
    const now = Date.parse(savedAt) + 31 * DAY;
    expect(displayStatus({ status: "sent", savedAt }, 30, now)).toBe("expired");
  });

  it("is not expired exactly at the validDays boundary (now === expiresAt)", () => {
    const savedAt = "2026-01-01T00:00:00.000Z";
    const now = Date.parse(savedAt) + 30 * DAY;
    expect(displayStatus({ status: "draft", savedAt }, 30, now)).toBe("draft");
  });

  it("is expired just past the validDays boundary", () => {
    const savedAt = "2026-01-01T00:00:00.000Z";
    const now = Date.parse(savedAt) + 30 * DAY + 1;
    expect(displayStatus({ status: "draft", savedAt }, 30, now)).toBe("expired");
  });

  it("never derives expired for accepted, regardless of age", () => {
    const savedAt = "2020-01-01T00:00:00.000Z";
    const now = Date.parse(savedAt) + 5000 * DAY;
    expect(displayStatus({ status: "accepted", savedAt }, 30, now)).toBe("accepted");
  });

  it("never derives expired for declined, regardless of age", () => {
    const savedAt = "2020-01-01T00:00:00.000Z";
    const now = Date.parse(savedAt) + 5000 * DAY;
    expect(displayStatus({ status: "declined", savedAt }, 30, now)).toBe("declined");
  });
});

describe("applyTransition", () => {
  it("sets status and appends a log entry", () => {
    const q = { status: "draft" as const, statusLog: [], label: "Benchy" };
    const next = applyTransition(q, "sent", "2026-01-02T00:00:00.000Z");
    expect(next.status).toBe("sent");
    expect(next.statusLog).toEqual([{ status: "sent", at: "2026-01-02T00:00:00.000Z" }]);
  });

  it("appends to an existing log rather than replacing it (log append ordering)", () => {
    const q = {
      status: "sent" as const,
      statusLog: [{ status: "draft" as const, at: "2026-01-01T00:00:00.000Z" }],
    };
    const next = applyTransition(q, "accepted", "2026-01-03T00:00:00.000Z");
    expect(next.statusLog).toEqual([
      { status: "draft", at: "2026-01-01T00:00:00.000Z" },
      { status: "accepted", at: "2026-01-03T00:00:00.000Z" },
    ]);
  });

  it("does not mutate the input quote or its statusLog", () => {
    const log = [{ status: "draft" as const, at: "2026-01-01T00:00:00.000Z" }];
    const q = { status: "draft" as const, statusLog: log };
    applyTransition(q, "sent", "2026-01-02T00:00:00.000Z");
    expect(q.status).toBe("draft");
    expect(q.statusLog).toBe(log);
    expect(log).toHaveLength(1);
  });

  it("preserves other fields on the returned object (generic passthrough)", () => {
    const q = { status: "draft" as const, statusLog: [], id: "q1", label: "Benchy" };
    const next = applyTransition(q, "declined", "2026-01-02T00:00:00.000Z");
    expect(next.id).toBe("q1");
    expect(next.label).toBe("Benchy");
  });

  it("records sentTo on the log entry when supplied", () => {
    const q = { status: "draft" as const, statusLog: [] };
    const next = applyTransition(q, "sent", "2026-01-02T00:00:00.000Z", "sam@example.com");
    expect(next.statusLog).toEqual([{ status: "sent", at: "2026-01-02T00:00:00.000Z", sentTo: "sam@example.com" }]);
  });

  it("omits sentTo from the log entry when not supplied (unchanged shape for non-email callers)", () => {
    const q = { status: "draft" as const, statusLog: [] };
    const next = applyTransition(q, "sent", "2026-01-02T00:00:00.000Z");
    expect(next.statusLog).toEqual([{ status: "sent", at: "2026-01-02T00:00:00.000Z" }]);
    expect(next.statusLog?.[0]).not.toHaveProperty("sentTo");
  });
});

describe("statusSortWeight", () => {
  it("orders draft < sent < accepted < declined", () => {
    expect(statusSortWeight({ status: "draft" })).toBeLessThan(statusSortWeight({ status: "sent" }));
    expect(statusSortWeight({ status: "sent" })).toBeLessThan(statusSortWeight({ status: "accepted" }));
    expect(statusSortWeight({ status: "accepted" })).toBeLessThan(statusSortWeight({ status: "declined" }));
  });
});
