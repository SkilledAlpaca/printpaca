import { describe, expect, it, vi } from "vitest";
import { createBackfillController } from "./backfillController.svelte";
import type { Page, PagedResult } from "./pagination";

  /** A fixed "server" of `total` numbered rows, paged like server/pagination.go's paginate(). */
function fakeServer(total: number): number[] {
  return Array.from({ length: total }, (_, i) => i);
}

  /** Builds a controller over a plain mutable box standing in for a tab's `items`/`total` fields. */
function makeHarness(fetchPage: (page: Page) => Promise<PagedResult<number> | null>, initial: number[] = [], total = initial.length) {
  const box = { items: initial, total };
  const controller = createBackfillController<number>({
    getItems: () => box.items,
    getTotal: () => box.total,
    fetchPage,
    setItems: (items) => {
      box.items = items;
      box.total = items.length;
    },
  });
  return { box, controller };
}

function pagedFetcher(all: number[]) {
  return vi.fn(async (page: Page) => ({
    items: all.slice(page.offset, page.offset + page.limit),
    total: all.length,
  }));
}

describe("createBackfillController", () => {
  it("starts idle and does nothing for a blank filter key", () => {
    const { controller } = makeHarness(vi.fn());
    expect(controller.backfilling).toBe(false);
    expect(controller.backfillFailed).toBe(false);
    controller.ensureReady("");
    controller.ensureReady("   ");
    expect(controller.backfilling).toBe(false);
  });

  it("backfills once a non-blank filter key appears, then stops (fully loaded)", async () => {
    const all = fakeServer(12);
    const fetchPage = pagedFetcher(all);
    const { box, controller } = makeHarness(fetchPage, all.slice(0, 5), 12);

    controller.ensureReady("a");
    expect(controller.backfilling).toBe(true);
    await vi.waitFor(() => expect(controller.backfilling).toBe(false));

    expect(box.items).toEqual(all);
    expect(box.total).toBe(12);
    expect(controller.backfillFailed).toBe(false);
  });

  it("rapid successive ensureReady calls with the same key don't double-fetch (re-entry)", async () => {
    const all = fakeServer(12);
    const fetchPage = pagedFetcher(all);
    const { controller } = makeHarness(fetchPage, [], 12);

    // Three calls fired before the first backfill settles (re-runs on every keystroke).
    controller.ensureReady("ab");
    controller.ensureReady("ab");
    controller.ensureReady("ab");
    await vi.waitFor(() => expect(controller.backfilling).toBe(false));

      // One chunk request only: a 12-row list backfills in one MAX_PAGE_LIMIT chunk; more is redundant.
    expect(fetchPage).toHaveBeenCalledTimes(1);
  });

  it("latches after a failure: same key doesn't retry until it changes", async () => {
    const fetchPage = vi.fn(async () => null); // every page fails
    const { controller } = makeHarness(fetchPage, [], 12);

    controller.ensureReady("x");
    await vi.waitFor(() => expect(controller.backfilling).toBe(false));
    expect(controller.backfillFailed).toBe(true);
    expect(fetchPage).toHaveBeenCalledTimes(1);

    controller.ensureReady("x");
    expect(controller.backfilling).toBe(false);
    expect(fetchPage).toHaveBeenCalledTimes(1);

    controller.ensureReady("xy");
    expect(controller.backfilling).toBe(true);
    await vi.waitFor(() => expect(controller.backfilling).toBe(false));
    expect(controller.backfillFailed).toBe(true);
    expect(fetchPage).toHaveBeenCalledTimes(2);
  });

  it("recovers once retried successfully after a prior failure", async () => {
    const all = fakeServer(12);
    let fail = true;
    const fetchPage = vi.fn(async (page: Page) => {
      if (fail) return null;
      return { items: all.slice(page.offset, page.offset + page.limit), total: all.length };
    });
    const { box, controller } = makeHarness(fetchPage, [], 12);

    controller.ensureReady("q");
    await vi.waitFor(() => expect(controller.backfilling).toBe(false));
    expect(controller.backfillFailed).toBe(true);

    fail = false;
    controller.ensureReady("q2"); // key change: clears the latch
    await vi.waitFor(() => expect(controller.backfilling).toBe(false));
    expect(controller.backfillFailed).toBe(false);
    expect(box.items).toEqual(all);
  });

  it("clearing the filter key resets the failure latch even though it's a no-op itself", async () => {
    const fetchPage = vi.fn(async () => null);
    const { controller } = makeHarness(fetchPage, [], 12);

    controller.ensureReady("z");
    await vi.waitFor(() => expect(controller.backfilling).toBe(false));
    expect(controller.backfillFailed).toBe(true);

    // Blank key (the filter box cleared) resets backfillFailed without attempting anything.
    controller.ensureReady("");
    expect(controller.backfillFailed).toBe(false);
    expect(controller.backfilling).toBe(false);
    expect(fetchPage).toHaveBeenCalledTimes(1);
  });

  it("a filter-key change mid-flight doesn't cancel the in-flight backfill, but does clear the latch for what comes after", async () => {
    const all = fakeServer(12);
    // A plain object holder, so TS doesn't narrow a callback-assigned `let` to never.
    const firstChunkGate: { release: () => void } = { release: () => {} };
    const fetchPage = vi.fn(async (page: Page) => {
      if (fetchPage.mock.calls.length === 1) {
        await new Promise<void>((resolve) => {
          firstChunkGate.release = resolve;
        });
      }
      return { items: all.slice(page.offset, page.offset + page.limit), total: all.length };
    });
    const { box, controller } = makeHarness(fetchPage, [], 12);

    controller.ensureReady("a");
    expect(controller.backfilling).toBe(true);

      // The key changes while the first fetch is pending; a backfill is running, so no second loop starts.
    controller.ensureReady("ab");
    expect(fetchPage).toHaveBeenCalledTimes(1);

    firstChunkGate.release();
    await vi.waitFor(() => expect(controller.backfilling).toBe(false));
    expect(box.items).toEqual(all);
    expect(controller.backfillFailed).toBe(false);
  });

  it("runNow bypasses the failure latch and awaits completion, reporting the outcome via backfillFailed", async () => {
    const all = fakeServer(12);
    let fail = true;
    const fetchPage = vi.fn(async (page: Page) => {
      if (fail) return null;
      return { items: all.slice(page.offset, page.offset + page.limit), total: all.length };
    });
    const { box, controller } = makeHarness(fetchPage, [], 12);

    // Latch a failure first, as when an export runs after a filter's backfill failed.
    controller.ensureReady("x");
    await vi.waitFor(() => expect(controller.backfilling).toBe(false));
    expect(controller.backfillFailed).toBe(true);

    // runNow retries anyway, ignoring the latch.
    fail = false;
    await controller.runNow();
    expect(controller.backfillFailed).toBe(false);
    expect(box.items).toEqual(all);
  });

  it("runNow is a no-op once the list is already fully loaded", async () => {
    const fetchPage = vi.fn();
    const { controller } = makeHarness(fetchPage, fakeServer(5), 5);
    await controller.runNow();
    expect(fetchPage).not.toHaveBeenCalled();
    expect(controller.backfillFailed).toBe(false);
  });

  it("runNow coalesces with an in-flight ensureReady-triggered backfill instead of racing a second one", async () => {
    const all = fakeServer(12);
    const fetchPage = pagedFetcher(all);
    const { box, controller } = makeHarness(fetchPage, [], 12);

    controller.ensureReady("a");
    expect(controller.backfilling).toBe(true);
    await controller.runNow(); // joins the already-running backfill
    expect(controller.backfillFailed).toBe(false);
    expect(box.items).toEqual(all);
    expect(fetchPage).toHaveBeenCalledTimes(1);
  });
});
