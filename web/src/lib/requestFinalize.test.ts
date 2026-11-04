import { describe, expect, it } from "vitest";
import { FINALIZE_NEEDS_TIME, finalizeBlockedReason, splitBulkFinalize } from "./requestFinalize";

describe("finalizeBlockedReason", () => {
  it("blocks a request waiting for its print time", () => {
    expect(finalizeBlockedReason({ needsPrintTime: true })).toBe(FINALIZE_NEEDS_TIME);
  });
  it("allows a priced request", () => {
    expect(finalizeBlockedReason({ needsPrintTime: false })).toBe("");
  });
});

describe("splitBulkFinalize", () => {
  it("skips rows that still need a print time", () => {
    const rows = [
      { id: "a", needsPrintTime: false },
      { id: "b", needsPrintTime: true },
      { id: "c", needsPrintTime: false },
    ];
    expect(splitBulkFinalize(["a", "b", "c"], rows)).toEqual({ ready: ["a", "c"], skipped: ["b"] });
  });
});
