/** The "backfill before filtering" re-entry latch shared by QuotesTab, ClientsTab and InvoicesTab. A
 * .svelte.ts file because `$state` makes the flags reactive across components; a factory because each tab
 * needs its own instance. */

import { ensureFullyLoaded, type PageFetcher } from "./pagination";

export interface BackfillController {
  /** True while a backfill from ensureReady() or runNow() is in flight. */
  readonly backfilling: boolean;
  /** True once the latest backfill for the current filter key failed. A re-entry latch, not just a display
   * flag (see ensureReady). */
  readonly backfillFailed: boolean;
  /** Call on every change that might need the list backfilled before a filter runs, typically from a
   * `$effect`. `filterKey` is the filter state as a string; a blank key means no filter and is a no-op.
   * `backfillFailed` is a re-entry latch: without it a failed backfill leaves the effect's dependencies
   * unchanged except `backfilling`, whose true->false flip re-runs the effect and refetches in an
   * unbounded loop. Comparing `filterKey` to the last-seen key (a plain closure variable, so no
   * dependency) clears the latch when the filter changes. */
  ensureReady(filterKey: string): void;
  /** Backfills now, bypassing the `backfillFailed` latch but coalescing with any in-flight backfill, and
   * awaits completion; for one-shot flows like QuotesTab's export that need the full list. Check
   * `backfillFailed` afterwards. A no-op once fully loaded. */
  runNow(): Promise<void>;
}

export interface BackfillControllerOptions<T> {
  /** The currently loaded window, read fresh on every attempt. */
  getItems: () => readonly T[];
  /** The server's pre-slice total row count (X-Total-Count), read fresh like getItems. */
  getTotal: () => number;
  /** One "fetch this page" call, forwarded to ensureFullyLoaded. */
  fetchPage: PageFetcher<T>;
  /** Called with the complete array once a backfill succeeds; callers should replace their window and set
   * their total to `items.length`. */
  setItems: (items: T[]) => void;
}

/** Builds one controller scoped to a single list. A factory, not a singleton, because each of the three
 * tabs has an independent list. */
export function createBackfillController<T>(opts: BackfillControllerOptions<T>): BackfillController {
  const state = $state({ backfilling: false, backfillFailed: false });
  // Plain (non-$state) snapshot starting at null, never a real key, so even an empty initial filter text
  // counts as "changed" on the first ensureReady.
  let lastFilterKey: string | null = null;
  // The one in-flight backfill; ensureReady and runNow both route through it so runNow awaits the same
  // attempt instead of racing a second fetch loop.
  let inFlight: Promise<void> | null = null;

  function fullyLoaded(): boolean {
    return opts.getItems().length >= opts.getTotal();
  }

  function runBackfill(): Promise<void> {
    if (inFlight) return inFlight;
    state.backfilling = true;
    state.backfillFailed = false;
    inFlight = (async () => {
      const result = await ensureFullyLoaded(opts.getItems(), opts.getTotal(), opts.fetchPage);
      if (result === null) {
        // Keep what was already loaded; the filter still runs over the partial window.
        state.backfillFailed = true;
      } else {
        opts.setItems(result);
        state.backfillFailed = false;
      }
      state.backfilling = false;
      inFlight = null;
    })();
    return inFlight;
  }

  function ensureReady(filterKey: string): void {
    if (filterKey !== lastFilterKey) {
      lastFilterKey = filterKey;
      state.backfillFailed = false;
    }
    if (filterKey.trim() === "") return; // no filter active: nothing to do
    if (fullyLoaded() || state.backfilling || state.backfillFailed) return;
    void runBackfill();
  }

  async function runNow(): Promise<void> {
    if (fullyLoaded()) return;
    await runBackfill();
  }

  return {
    get backfilling() {
      return state.backfilling;
    },
    get backfillFailed() {
      return state.backfillFailed;
    },
    ensureReady,
    runNow,
  };
}
