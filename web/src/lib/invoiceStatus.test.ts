import { describe, expect, it, vi } from "vitest";
import { isInvoiceOverdue, invoiceStatusVariant, todayInTimezone } from "./invoiceStatus";

describe("isInvoiceOverdue", () => {
  it("true for a sent invoice with a due date in the past", () => {
    expect(isInvoiceOverdue({ status: "sent", dueDate: "2020-01-01" }, "2026-01-01")).toBe(true);
  });

  // `today` must default to the LOCAL date: at 09:00 on the 15th in Samoa (UTC+13) a due-the-14th
  // invoice must already be overdue.
  it("defaults today to the local date, not the UTC date, at a UTC+ boundary", () => {
    const originalTz = process.env.TZ;
    process.env.TZ = "Pacific/Apia"; // UTC+13
    vi.useFakeTimers();
    try {
      vi.setSystemTime(new Date("2026-01-14T20:00:00Z")); // 2026-01-15 09:00 local
      expect(todayInTimezone()).toBe("2026-01-15");
      expect(isInvoiceOverdue({ status: "sent", dueDate: "2026-01-14" })).toBe(true);
    } finally {
      vi.useRealTimers();
      process.env.TZ = originalTz;
    }
  });

    // At a UTC- boundary, 18:00 local on the 14th (UTC-7) is already the 15th in UTC; not overdue yet.
  it("defaults today to the local date, not the UTC date, at a UTC- boundary", () => {
    const originalTz = process.env.TZ;
    process.env.TZ = "Etc/GMT+7"; // UTC-7
    vi.useFakeTimers();
    try {
      vi.setSystemTime(new Date("2026-01-15T01:00:00Z")); // 2026-01-14 18:00 local
      expect(todayInTimezone()).toBe("2026-01-14");
      expect(isInvoiceOverdue({ status: "sent", dueDate: "2026-01-14" })).toBe(false);
    } finally {
      vi.useRealTimers();
      process.env.TZ = originalTz;
    }
  });

  // The configured zone wins over the browser's: 2026-01-15 01:00 UTC is still the 14th in New York.
  it("uses the configured time zone, not the browser's", () => {
    vi.useFakeTimers();
    try {
      vi.setSystemTime(new Date("2026-01-15T01:00:00Z"));
      expect(todayInTimezone("America/New_York")).toBe("2026-01-14");
      expect(todayInTimezone("Pacific/Apia")).toBe("2026-01-15");
      expect(isInvoiceOverdue({ status: "sent", dueDate: "2026-01-14" }, todayInTimezone("America/New_York"))).toBe(false);
    } finally {
      vi.useRealTimers();
    }
  });

  it("false for a sent invoice with a due date today or in the future", () => {
    expect(isInvoiceOverdue({ status: "sent", dueDate: "2026-01-01" }, "2026-01-01")).toBe(false);
    expect(isInvoiceOverdue({ status: "sent", dueDate: "2026-02-01" }, "2026-01-01")).toBe(false);
  });

  it("false for a sent invoice with no due date set", () => {
    expect(isInvoiceOverdue({ status: "sent", dueDate: "" }, "2026-01-01")).toBe(false);
  });

  it("false for any non-sent status regardless of due date", () => {
    for (const status of ["draft", "paid", "canceled"]) {
      expect(isInvoiceOverdue({ status, dueDate: "2020-01-01" }, "2026-01-01")).toBe(false);
    }
  });
});

describe("invoiceStatusVariant", () => {
  it("reports \"overdue\" for an overdue sent invoice", () => {
    expect(invoiceStatusVariant({ status: "sent", dueDate: "2020-01-01" }, "2026-01-01")).toBe("overdue");
  });

  it("otherwise reports the status verbatim", () => {
    expect(invoiceStatusVariant({ status: "sent", dueDate: "2026-02-01" }, "2026-01-01")).toBe("sent");
    expect(invoiceStatusVariant({ status: "draft", dueDate: "" }, "2026-01-01")).toBe("draft");
    expect(invoiceStatusVariant({ status: "paid", dueDate: "2020-01-01" }, "2026-01-01")).toBe("paid");
    expect(invoiceStatusVariant({ status: "canceled", dueDate: "2020-01-01" }, "2026-01-01")).toBe("canceled");
  });
});
