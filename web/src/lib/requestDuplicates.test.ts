import { describe, expect, it } from "vitest";
import { findDuplicateRequestIds, type DuplicateCandidate } from "./requestDuplicates";

function req(overrides: Partial<DuplicateCandidate> & { id: string }): DuplicateCandidate {
  return {
    email: "a@example.com",
    kind: "quote",
    itemId: "",
    gcodeFilename: "part.gcode",
    quantity: 1,
    createdAt: "2026-01-01T12:00:00Z",
    ...overrides,
  };
}

describe("findDuplicateRequestIds", () => {
  it("flags the later of two matching requests within 60 minutes", () => {
    const requests = [
      req({ id: "b", createdAt: "2026-01-01T12:30:00Z" }), // later, list order newest-first
      req({ id: "a", createdAt: "2026-01-01T12:00:00Z" }),
    ];
    expect(findDuplicateRequestIds(requests)).toEqual(new Set(["b"]));
  });

  it("does not flag the earlier request itself", () => {
    const requests = [
      req({ id: "b", createdAt: "2026-01-01T12:30:00Z" }),
      req({ id: "a", createdAt: "2026-01-01T12:00:00Z" }),
    ];
    const dup = findDuplicateRequestIds(requests);
    expect(dup.has("a")).toBe(false);
  });

  it("does not flag requests more than 60 minutes apart", () => {
    const requests = [
      req({ id: "b", createdAt: "2026-01-01T13:00:01Z" }),
      req({ id: "a", createdAt: "2026-01-01T12:00:00Z" }),
    ];
    expect(findDuplicateRequestIds(requests)).toEqual(new Set());
  });

  it("flags requests exactly 60 minutes apart (inclusive boundary)", () => {
    const requests = [
      req({ id: "b", createdAt: "2026-01-01T13:00:00Z" }),
      req({ id: "a", createdAt: "2026-01-01T12:00:00Z" }),
    ];
    expect(findDuplicateRequestIds(requests)).toEqual(new Set(["b"]));
  });

  it("does not flag when the email differs", () => {
    const requests = [
      req({ id: "b", email: "other@example.com", createdAt: "2026-01-01T12:05:00Z" }),
      req({ id: "a", createdAt: "2026-01-01T12:00:00Z" }),
    ];
    expect(findDuplicateRequestIds(requests)).toEqual(new Set());
  });

  it("matches email case-insensitively", () => {
    const requests = [
      req({ id: "b", email: "A@EXAMPLE.com", createdAt: "2026-01-01T12:05:00Z" }),
      req({ id: "a", email: "a@example.com", createdAt: "2026-01-01T12:00:00Z" }),
    ];
    expect(findDuplicateRequestIds(requests)).toEqual(new Set(["b"]));
  });

  it("does not flag when the filename differs", () => {
    const requests = [
      req({ id: "b", gcodeFilename: "other.gcode", createdAt: "2026-01-01T12:05:00Z" }),
      req({ id: "a", gcodeFilename: "part.gcode", createdAt: "2026-01-01T12:00:00Z" }),
    ];
    expect(findDuplicateRequestIds(requests)).toEqual(new Set());
  });

  it("does not flag when the quantity differs", () => {
    const requests = [
      req({ id: "b", quantity: 2, createdAt: "2026-01-01T12:05:00Z" }),
      req({ id: "a", quantity: 1, createdAt: "2026-01-01T12:00:00Z" }),
    ];
    expect(findDuplicateRequestIds(requests)).toEqual(new Set());
  });

  it("breaks ties by array position when createdAt is identical", () => {
    const requests = [
      req({ id: "b", createdAt: "2026-01-01T12:00:00Z" }), // earlier in a newest-first list = later arrival
      req({ id: "a", createdAt: "2026-01-01T12:00:00Z" }),
    ];
    expect(findDuplicateRequestIds(requests)).toEqual(new Set(["b"]));
  });

  it("returns an empty set for an empty or single-item list", () => {
    expect(findDuplicateRequestIds([])).toEqual(new Set());
    expect(findDuplicateRequestIds([req({ id: "a" })])).toEqual(new Set());
  });

  it("flags only the third of three matching rapid submissions, chaining off the closest match", () => {
    const requests = [
      req({ id: "c", createdAt: "2026-01-01T12:10:00Z" }),
      req({ id: "b", createdAt: "2026-01-01T12:05:00Z" }),
      req({ id: "a", createdAt: "2026-01-01T12:00:00Z" }),
    ];
    expect(findDuplicateRequestIds(requests)).toEqual(new Set(["b", "c"]));
  });

  // The join key must not let different (email, filename) pairs collide when gcodeFilename contains spaces.
  it("does not merge different (email, filename) pairs whose concatenation could otherwise collide", () => {
    const requests = [
      req({ id: "b", email: "ab@example.com", gcodeFilename: "c.gcode", createdAt: "2026-01-01T12:05:00Z" }),
      req({ id: "a", email: "a@example.com", gcodeFilename: "b c.gcode", createdAt: "2026-01-01T12:00:00Z" }),
    ];
    expect(findDuplicateRequestIds(requests)).toEqual(new Set());
  });

  // Bucketed adjacent-pair scan (O(n log n)) must stay fast at ~5000 requests (an all-pairs scan would take
  // ~25M iterations) and stay correct at that scale.
  it("stays fast and correct on a large, mixed list (~5000 requests)", () => {
    const requests: DuplicateCandidate[] = [];
    const base = Date.parse("2026-01-01T00:00:00Z");
    // 1000 (email, quantity) groups of 5 rapid duplicates, newest-first: each group's oldest is never
    // flagged, the other 4 are.
    for (let g = 0; g < 1000; g++) {
      for (let k = 4; k >= 0; k--) {
        requests.push(
          req({
            id: `g${g}-${k}`,
            email: `user${g}@example.com`,
            quantity: g % 7,
            createdAt: new Date(base + g * 24 * 60 * 60 * 1000 + k * 60 * 1000).toISOString(),
          }),
        );
      }
    }

    const t0 = performance.now();
    const dup = findDuplicateRequestIds(requests);
    const elapsedMs = performance.now() - t0;

    expect(dup.size).toBe(4000); // 4 of every 5 in each group
    for (let g = 0; g < 1000; g++) {
      expect(dup.has(`g${g}-0`)).toBe(false); // oldest in the group
      for (let k = 1; k <= 4; k++) expect(dup.has(`g${g}-${k}`)).toBe(true);
    }
    // Generous ceiling for a debug/CI machine.
    expect(elapsedMs).toBeLessThan(500);
  });

  // Item orders carry gcodeFilename "", so the bucket key needs kind/itemId to tell them apart.

  it("still flags a repeat order of the same item at the same quantity", () => {
    const requests = [
      req({ id: "b", kind: "item", itemId: "item-1", gcodeFilename: "", createdAt: "2026-01-01T12:05:00Z" }),
      req({ id: "a", kind: "item", itemId: "item-1", gcodeFilename: "", createdAt: "2026-01-01T12:00:00Z" }),
    ];
    expect(findDuplicateRequestIds(requests)).toEqual(new Set(["b"]));
  });

  it("does not flag two different items ordered at the same quantity", () => {
    const requests = [
      req({ id: "b", kind: "item", itemId: "item-2", gcodeFilename: "", createdAt: "2026-01-01T12:05:00Z" }),
      req({ id: "a", kind: "item", itemId: "item-1", gcodeFilename: "", createdAt: "2026-01-01T12:00:00Z" }),
    ];
    expect(findDuplicateRequestIds(requests)).toEqual(new Set());
  });

  it("does not flag an item order against a no-gcode quote request", () => {
    const requests = [
      req({ id: "b", kind: "item", itemId: "item-1", gcodeFilename: "", createdAt: "2026-01-01T12:05:00Z" }),
      req({ id: "a", kind: "quote", itemId: "", gcodeFilename: "", createdAt: "2026-01-01T12:00:00Z" }),
    ];
    expect(findDuplicateRequestIds(requests)).toEqual(new Set());
  });
});
