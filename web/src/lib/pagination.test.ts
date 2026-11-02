import { describe, expect, it, vi } from "vitest";
import {
  ensureFullyLoaded,
  pageSuffix,
  refetchLimit,
  resolveDetailMiss,
  totalFromHeader,
  type Page,
  type PagedResult,
} from "./pagination";

describe("pageSuffix", () => {
  it("builds a ?limit=&offset= query string", () => {
    expect(pageSuffix({ limit: 100, offset: 0 })).toBe("?limit=100&offset=0");
    expect(pageSuffix({ limit: 500, offset: 250 })).toBe("?limit=500&offset=250");
  });
});

describe("totalFromHeader", () => {
  function responseWithHeader(value: string | null): Response {
    return { headers: { get: () => value } } as unknown as Response;
  }

  it("reads and parses X-Total-Count when present", () => {
    const res = responseWithHeader("42");
    expect(totalFromHeader(res, 10, { limit: 10, offset: 0 })).toBe(42);
  });

  it("falls back to itemsLength + offset when the header is missing", () => {
    const res = responseWithHeader(null);
    expect(totalFromHeader(res, 10, { limit: 10, offset: 20 })).toBe(30);
  });

  it("falls back when the header isn't a valid non-negative number", () => {
    expect(totalFromHeader(responseWithHeader("not-a-number"), 5, { limit: 10, offset: 0 })).toBe(5);
    expect(totalFromHeader(responseWithHeader("-3"), 5, { limit: 10, offset: 0 })).toBe(5);
    expect(totalFromHeader(responseWithHeader(""), 5, { limit: 10, offset: 0 })).toBe(5);
  });
});

describe("refetchLimit", () => {
  it("asks for at least the initial page size", () => {
    expect(refetchLimit(0)).toBe(100);
    expect(refetchLimit(37)).toBe(100);
  });

  it("preserves a loaded window between 100 and the server cap", () => {
    expect(refetchLimit(100)).toBe(100);
    expect(refetchLimit(342)).toBe(342);
    expect(refetchLimit(500)).toBe(500);
  });

  it("never exceeds the server cap, which would 400 instead of clamping", () => {
    expect(refetchLimit(501)).toBe(500);
    expect(refetchLimit(1200)).toBe(500);
  });
});

describe("ensureFullyLoaded", () => {
  // A fetcher over a fixed "server" array of `total` numbered rows, sliced like server/pagination.go.
  function fakeServer(total: number): PagedResult<number>["items"] {
    return Array.from({ length: total }, (_, i) => i);
  }

  it("returns items unchanged when already fully loaded", async () => {
    const fetchPage = vi.fn();
    const result = await ensureFullyLoaded([1, 2, 3], 3, fetchPage);
    expect(result).toEqual([1, 2, 3]);
    expect(fetchPage).not.toHaveBeenCalled();
  });

  it("fetches one chunk when everything remaining fits in it", async () => {
    const all = fakeServer(10);
    const fetchPage = vi.fn(async (page: Page) => ({
      items: all.slice(page.offset, page.offset + page.limit),
      total: all.length,
    }));
    const result = await ensureFullyLoaded(all.slice(0, 3), 10, fetchPage, 500);
    expect(result).toEqual(all);
    expect(fetchPage).toHaveBeenCalledTimes(1);
    expect(fetchPage).toHaveBeenCalledWith({ limit: 500, offset: 3 });
  });

  it("loops in chunks until the full list is loaded", async () => {
    const all = fakeServer(12);
    const fetchPage = vi.fn(async (page: Page) => ({
      items: all.slice(page.offset, page.offset + page.limit),
      total: all.length,
    }));
    const result = await ensureFullyLoaded([], 12, fetchPage, 5);
    expect(result).toEqual(all);
    expect(fetchPage).toHaveBeenCalledTimes(3);
    expect(fetchPage.mock.calls.map(([p]) => p)).toEqual([
      { limit: 5, offset: 0 },
      { limit: 5, offset: 5 },
      { limit: 5, offset: 10 },
    ]);
  });

  it("returns null, not a partial array, when a chunk fails mid-loop", async () => {
    const all = fakeServer(12);
    let call = 0;
    const fetchPage = vi.fn(async (page: Page) => {
      call++;
      if (call === 2) return null; // fails on the second chunk
      return { items: all.slice(page.offset, page.offset + page.limit), total: all.length };
    });
    const result = await ensureFullyLoaded([], 12, fetchPage, 5);
    expect(result).toBeNull();
    expect(fetchPage).toHaveBeenCalledTimes(2);
  });

  it("stops instead of looping forever if a page comes back empty before total is reached", async () => {
    // Rows disappear (concurrent deletes) after `total` was read: the server returns fewer than claimed.
    const fetchPage = vi.fn(async () => ({ items: [], total: 20 }));
    const result = await ensureFullyLoaded([], 20, fetchPage, 5);
    expect(result).toEqual([]);
    expect(fetchPage).toHaveBeenCalledTimes(1);
  });
});

describe("resolveDetailMiss", () => {
  interface Row {
    id: string;
  }

  // A "server" of `count` rows with ids "0".."count-1", paged like server/pagination.go's paginate().
  function fakeServer(count: number): Row[] {
    return Array.from({ length: count }, (_, i) => ({ id: String(i) }));
  }

  it("finds a record past the first page by backfilling and re-looking-up", async () => {
    const all = fakeServer(150);
    const fetchPage = vi.fn(async (page: Page) => ({
      items: all.slice(page.offset, page.offset + page.limit),
      total: all.length,
    }));
    const loaded = all.slice(0, 100);
    const result = await resolveDetailMiss("149", loaded, all.length, (r) => r.id, fetchPage);
    expect(result.kind).toBe("found");
    if (result.kind === "found") {
      expect(result.item).toEqual({ id: "149" });
      expect(result.items).toEqual(all);
    }
  });

  it("reports a genuine miss once the backfill completes and the id still isn't there", async () => {
    const all = fakeServer(150);
    const fetchPage = vi.fn(async (page: Page) => ({
      items: all.slice(page.offset, page.offset + page.limit),
      total: all.length,
    }));
    const loaded = all.slice(0, 100);
    const result = await resolveDetailMiss("does-not-exist", loaded, all.length, (r) => r.id, fetchPage);
    expect(result.kind).toBe("miss");
    if (result.kind === "miss") {
      expect(result.items).toEqual(all); // backfill still ran to completion
    }
  });

  it("reports an error, not a miss, when the backfill itself fails", async () => {
    const fetchPage = vi.fn(async () => null);
    const loaded: Row[] = [{ id: "0" }];
    const result = await resolveDetailMiss("149", loaded, 150, (r) => r.id, fetchPage);
    expect(result.kind).toBe("error");
  });
});
