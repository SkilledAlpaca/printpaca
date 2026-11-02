import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createQuotePreview, type QuotePreviewState, type QuotePreviewToState } from "./quotePreview";

const INPUT_A = { material: "PLA", quantity: 1, weightG: 50, timeSeconds: 3600 };
const INPUT_B = { material: "PLA", quantity: 2, weightG: 50, timeSeconds: 3600 };

  /** The public page's fetcher-result shape and resolved/error mapping, since lib/ can't import public/. */
interface FetchResult {
  ok: boolean;
  errors: string[];
  priceCents?: number;
  shippingCents?: number;
}
type Resolved = { priceCents: number; shippingCents: number };
const toState: QuotePreviewToState<FetchResult, Resolved> = (result) => {
  if (result.ok && typeof result.priceCents === "number" && typeof result.shippingCents === "number") {
    return { status: "resolved", priceCents: result.priceCents, shippingCents: result.shippingCents };
  }
  return { status: "error", errors: result.errors.length > 0 ? result.errors : ["Couldn't estimate this request."] };
};

  /** A fetcher whose resolution the test controls, so overlapping requests can arrive in a scripted order. */
function deferred<T>(): { promise: Promise<T>; resolve: (v: T) => void } {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
}

describe("createQuotePreview", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("stays idle until update() is called with a non-null input", () => {
    const states: QuotePreviewState<Resolved>[] = [];
    createQuotePreview({ fetcher: vi.fn(), toState, onChange: (s) => states.push(s) });
    expect(states).toEqual([]);
  });

  it("update(null) after a valid input cancels the debounce and resets to idle, never calling the fetcher", async () => {
    const fetcher = vi.fn();
    const states: QuotePreviewState<Resolved>[] = [];
    const c = createQuotePreview({ fetcher, toState, onChange: (s) => states.push(s) });

    c.update(INPUT_A);
    expect(states.at(-1)).toEqual({ status: "pending" });
    c.update(null);
    expect(states.at(-1)).toEqual({ status: "idle" });

    await vi.advanceTimersByTimeAsync(1000);
    expect(fetcher).not.toHaveBeenCalled();
  });

  it("debounces: only one fetch fires after rapid updates, using the latest input", async () => {
    const fetcher = vi.fn<(input: typeof INPUT_A) => Promise<FetchResult>>().mockResolvedValue({
      ok: true,
      errors: [],
      priceCents: 999,
      shippingCents: 100,
    });
    const states: QuotePreviewState<Resolved>[] = [];
    const c = createQuotePreview({ fetcher, toState, onChange: (s) => states.push(s), debounceMs: 350 });

    c.update(INPUT_A);
    await vi.advanceTimersByTimeAsync(100);
    c.update(INPUT_B); // supersedes INPUT_A before its debounce timer fires
    await vi.advanceTimersByTimeAsync(350);

    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(fetcher).toHaveBeenCalledWith(INPUT_B);
    expect(states.at(-1)).toEqual({ status: "resolved", priceCents: 999, shippingCents: 100 });
  });

  it("an identical input to the last one is a no-op (no debounce restart, no extra onChange)", async () => {
    const fetcher = vi.fn<(input: typeof INPUT_A) => Promise<FetchResult>>().mockResolvedValue({
      ok: true,
      errors: [],
      priceCents: 500,
      shippingCents: 0,
    });
    const states: QuotePreviewState<Resolved>[] = [];
    const c = createQuotePreview({ fetcher, toState, onChange: (s) => states.push(s), debounceMs: 350 });

    c.update({ ...INPUT_A });
    const pendingCount = states.filter((s) => s.status === "pending").length;
    c.update({ ...INPUT_A }); // same values, new object identity -- must not re-emit "pending" or restart the timer
    expect(states.filter((s) => s.status === "pending").length).toBe(pendingCount);

    await vi.advanceTimersByTimeAsync(350);
    expect(fetcher).toHaveBeenCalledTimes(1);
  });

  it("goes pending immediately, then resolved once the debounce elapses and the fetch succeeds", async () => {
    const fetcher = vi.fn<(input: typeof INPUT_A) => Promise<FetchResult>>().mockResolvedValue({
      ok: true,
      errors: [],
      priceCents: 1234,
      shippingCents: 500,
    });
    const states: QuotePreviewState<Resolved>[] = [];
    const c = createQuotePreview({ fetcher, toState, onChange: (s) => states.push(s), debounceMs: 350 });

    c.update(INPUT_A);
    expect(states).toEqual([{ status: "pending" }]);
    expect(fetcher).not.toHaveBeenCalled();

    await vi.advanceTimersByTimeAsync(350);
    expect(fetcher).toHaveBeenCalledWith(INPUT_A);
    expect(states.at(-1)).toEqual({ status: "resolved", priceCents: 1234, shippingCents: 500 });
  });

  it("surfaces the fetcher's validation errors as an error state", async () => {
    const fetcher = vi.fn<(input: typeof INPUT_A) => Promise<FetchResult>>().mockResolvedValue({
      ok: false,
      errors: ["Quantity must be a whole number from 1 to 1000."],
    });
    const states: QuotePreviewState<Resolved>[] = [];
    const c = createQuotePreview({ fetcher, toState, onChange: (s) => states.push(s), debounceMs: 350 });

    c.update(INPUT_A);
    await vi.advanceTimersByTimeAsync(350);
    expect(states.at(-1)).toEqual({ status: "error", errors: ["Quantity must be a whole number from 1 to 1000."] });
  });

  it("falls back to a generic message when the fetcher reports failure with no errors", async () => {
    const fetcher = vi.fn<(input: typeof INPUT_A) => Promise<FetchResult>>().mockResolvedValue({
      ok: false,
      errors: [],
    });
    const states: QuotePreviewState<Resolved>[] = [];
    const c = createQuotePreview({ fetcher, toState, onChange: (s) => states.push(s), debounceMs: 350 });

    c.update(INPUT_A);
    await vi.advanceTimersByTimeAsync(350);
    expect(states.at(-1)).toEqual({ status: "error", errors: ["Couldn't estimate this request."] });
  });

  it("retries after a failure: resubmitting the identical input following an error state fetches again and can resolve", async () => {
    const fetcher = vi
      .fn<(input: typeof INPUT_A) => Promise<FetchResult>>()
      .mockResolvedValueOnce({ ok: false, errors: ["Server unavailable."] })
      .mockResolvedValueOnce({ ok: true, errors: [], priceCents: 1000, shippingCents: 0 });
    const states: QuotePreviewState<Resolved>[] = [];
    const c = createQuotePreview({ fetcher, toState, onChange: (s) => states.push(s), debounceMs: 350 });

    c.update(INPUT_A);
    await vi.advanceTimersByTimeAsync(350);
    expect(states.at(-1)).toEqual({ status: "error", errors: ["Server unavailable."] });

      // Same input again (a second submit, no field change): lastKey still held INPUT_A from the failure.
    c.update({ ...INPUT_A });
    expect(states.at(-1)).toEqual({ status: "pending" });
    await vi.advanceTimersByTimeAsync(350);

    expect(fetcher).toHaveBeenCalledTimes(2);
    expect(states.at(-1)).toEqual({ status: "resolved", priceCents: 1000, shippingCents: 0 });
  });

  it("a stale in-flight response is discarded once a newer input has been requested", async () => {
    const first = deferred<FetchResult>();
    const second = deferred<FetchResult>();
    const fetcher = vi
      .fn<(input: typeof INPUT_A) => Promise<FetchResult>>()
      .mockImplementationOnce(() => first.promise)
      .mockImplementationOnce(() => second.promise);
    const states: QuotePreviewState<Resolved>[] = [];
    const c = createQuotePreview({ fetcher, toState, onChange: (s) => states.push(s), debounceMs: 350 });

    c.update(INPUT_A);
    await vi.advanceTimersByTimeAsync(350); // first fetch is now in flight

    c.update(INPUT_B);
    await vi.advanceTimersByTimeAsync(350); // second fetch is now also in flight

    // The newer request answers first; the older one's later answer must not overwrite it.
    second.resolve({ ok: true, errors: [], priceCents: 200, shippingCents: 0 });
    await Promise.resolve();
    await Promise.resolve();
    expect(states.at(-1)).toEqual({ status: "resolved", priceCents: 200, shippingCents: 0 });

    first.resolve({ ok: true, errors: [], priceCents: 100, shippingCents: 0 });
    await Promise.resolve();
    await Promise.resolve();
    expect(states.at(-1)).toEqual({ status: "resolved", priceCents: 200, shippingCents: 0 });
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it("dispose() cancels a pending debounce and suppresses a still-in-flight response", async () => {
    const pending = deferred<FetchResult>();
    const fetcher = vi.fn<(input: typeof INPUT_A) => Promise<FetchResult>>().mockReturnValue(pending.promise);
    const states: QuotePreviewState<Resolved>[] = [];
    const c = createQuotePreview({ fetcher, toState, onChange: (s) => states.push(s), debounceMs: 350 });

    c.update(INPUT_A);
    await vi.advanceTimersByTimeAsync(350); // fetch now in flight
    c.dispose();

    pending.resolve({ ok: true, errors: [], priceCents: 1, shippingCents: 0 });
    await Promise.resolve();
    await Promise.resolve();
    expect(states.at(-1)).toEqual({ status: "pending" }); // never advanced past the in-flight state; dispose suppressed the resolution

    c.update(INPUT_B);
    await vi.advanceTimersByTimeAsync(1000);
    expect(fetcher).toHaveBeenCalledTimes(1);
  });

  it("settled() resolves immediately while idle", async () => {
    const fetcher = vi.fn();
    const c = createQuotePreview({ fetcher, toState, onChange: () => {} });

    await expect(c.settled()).resolves.toEqual({ status: "idle" });
    expect(fetcher).not.toHaveBeenCalled();
  });

  it("settled() called mid-debounce waits for the timer and the fetch, then resolves with the resolved state", async () => {
    const fetcher = vi
      .fn<(input: typeof INPUT_A) => Promise<FetchResult>>()
      .mockResolvedValue({ ok: true, errors: [], priceCents: 700, shippingCents: 50 });
    const c = createQuotePreview({ fetcher, toState, onChange: () => {}, debounceMs: 350 });

    c.update(INPUT_A);
    const settledPromise = c.settled(); // called before the debounce timer has fired
    await vi.advanceTimersByTimeAsync(350);

    await expect(settledPromise).resolves.toEqual({ status: "resolved", priceCents: 700, shippingCents: 50 });
  });

  it("settled() called while the fetch is already in flight waits for it to resolve", async () => {
    const pending = deferred<FetchResult>();
    const fetcher = vi.fn<(input: typeof INPUT_A) => Promise<FetchResult>>().mockReturnValue(pending.promise);
    const c = createQuotePreview({ fetcher, toState, onChange: () => {}, debounceMs: 350 });

    c.update(INPUT_A);
    await vi.advanceTimersByTimeAsync(350); // debounce elapsed, fetch now in flight
    const settledPromise = c.settled();

    pending.resolve({ ok: true, errors: [], priceCents: 300, shippingCents: 25 });
    await expect(settledPromise).resolves.toEqual({ status: "resolved", priceCents: 300, shippingCents: 25 });
  });

  it("settled() resolves with the superseding update's outcome, not the superseded one", async () => {
    const fetcher = vi
      .fn<(input: typeof INPUT_A) => Promise<FetchResult>>()
      .mockResolvedValue({ ok: true, errors: [], priceCents: 999, shippingCents: 1 });
    const c = createQuotePreview({ fetcher, toState, onChange: () => {}, debounceMs: 350 });

    c.update(INPUT_A);
    const settledPromise = c.settled();
    await vi.advanceTimersByTimeAsync(100);
    c.update(INPUT_B); // supersedes INPUT_A before its debounce timer fires
    await vi.advanceTimersByTimeAsync(350);

    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(fetcher).toHaveBeenCalledWith(INPUT_B);
    await expect(settledPromise).resolves.toEqual({ status: "resolved", priceCents: 999, shippingCents: 1 });
  });

  it("a rejecting fetcher resolves to an error state instead of hanging pending forever", async () => {
    const fetcher = vi.fn<(input: typeof INPUT_A) => Promise<FetchResult>>().mockRejectedValue(new Error("boom"));
    const states: QuotePreviewState<Resolved>[] = [];
    const c = createQuotePreview({ fetcher, toState, onChange: (s) => states.push(s), debounceMs: 350 });

    c.update(INPUT_A);
    await vi.advanceTimersByTimeAsync(350);

    expect(states.at(-1)).toEqual({ status: "error", errors: ["Couldn't reach the server."] });
  });

  it("a rejecting fetcher resolves every settled() waiter with an error state, and allows a retry", async () => {
    const fetcher = vi
      .fn<(input: typeof INPUT_A) => Promise<FetchResult>>()
      .mockRejectedValueOnce(new Error("boom"))
      .mockResolvedValueOnce({ ok: true, errors: [], priceCents: 1000, shippingCents: 0 });
    const c = createQuotePreview({ fetcher, toState, onChange: () => {}, debounceMs: 350 });

    c.update(INPUT_A);
    const settledPromise = c.settled(); // queued before the fetch rejects
    await vi.advanceTimersByTimeAsync(350);

    await expect(settledPromise).resolves.toEqual({ status: "error", errors: ["Couldn't reach the server."] });

    // lastKey was cleared on the rejection, so resubmitting the identical input fetches again.
    c.update({ ...INPUT_A });
    await vi.advanceTimersByTimeAsync(350);

    expect(fetcher).toHaveBeenCalledTimes(2);
    await expect(c.settled()).resolves.toEqual({ status: "resolved", priceCents: 1000, shippingCents: 0 });
  });

  it("settled() resolves with the last known state instead of hanging after dispose()", async () => {
    const pending = deferred<FetchResult>();
    const fetcher = vi.fn<(input: typeof INPUT_A) => Promise<FetchResult>>().mockReturnValue(pending.promise);
    const c = createQuotePreview({ fetcher, toState, onChange: () => {}, debounceMs: 350 });

    c.update(INPUT_A);
    await vi.advanceTimersByTimeAsync(350); // fetch now in flight
    const settledPromise = c.settled(); // queued before dispose
    c.dispose();

    await expect(settledPromise).resolves.toEqual({ status: "pending" }); // last known state, not a hang

    await expect(c.settled()).resolves.toEqual({ status: "pending" });
  });
});
