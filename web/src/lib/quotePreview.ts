/** Debounce/fetch/state machine shared by every server-authoritative price preview (the public page's
 * estimate and QuoteTab's live quote). The server is the only pricing implementation, so each input change
 * is a round trip; this keeps a slow or superseded answer from landing out of order. Framework-agnostic:
 * the caller owns one instance, feeds `update()` and mirrors `onChange` into its own `$state`. `fetcher`
 * is injected for tests; `toState` maps a fetcher result to resolved or error, since "usable" differs per
 * consumer. */

export type QuotePreviewState<TResolved> =
  | { status: "idle" }
  | { status: "pending" }
  | ({ status: "resolved" } & TResolved)
  | { status: "error"; errors: string[] };

/** The fetcher may reject freely: run() catches it and surfaces {status: "error"}, so a network failure
 * never hangs settled() or leaves the controller "pending". */
export type QuotePreviewFetcher<TInput, TFetchResult> = (input: TInput) => Promise<TFetchResult>;

export type QuotePreviewToState<TFetchResult, TResolved> = (
  result: TFetchResult,
) => ({ status: "resolved" } & TResolved) | { status: "error"; errors: string[] };

export interface QuotePreviewController<TInput, TResolved> {
  /** Call on every change to the tracked inputs. `null` means not all required inputs are valid: cancels
   * any debounce or in-flight request and resets to idle. A non-null input identical (by JSON) to the last
   * is a no-op. */
  update(input: TInput | null): void;
  /** Stops the debounce timer and suppresses any in-flight result; call from onDestroy so a late response
   * never fires onChange. */
  dispose(): void;
  /** Resolves with the state to act on now: the current one if nothing is in flight, else the next
   * non-pending state, never a stale "pending" or a superseded update's outcome. After dispose() it
   * resolves immediately with the last state. Lets "Ctrl+S during the debounce" wait out the round trip. */
  settled(): Promise<QuotePreviewState<TResolved>>;
}

const DEFAULT_DEBOUNCE_MS = 350;

export function createQuotePreview<TInput, TFetchResult, TResolved>(opts: {
  fetcher: QuotePreviewFetcher<TInput, TFetchResult>;
  toState: QuotePreviewToState<TFetchResult, TResolved>;
  onChange: (state: QuotePreviewState<TResolved>) => void;
  debounceMs?: number;
}): QuotePreviewController<TInput, TResolved> {
  const debounceMs = opts.debounceMs ?? DEFAULT_DEBOUNCE_MS;
  let timer: ReturnType<typeof setTimeout> | null = null;
  // Bumped on every input change and on dispose: a run() whose token no longer matches lost a race and must
  // not publish its result.
  let token = 0;
  // The last input acted on (JSON-keyed; null when idle). Stops a $derived that recomputes a new object
  // identity with equal values from restarting an in-flight or resolved request.
  let lastKey: string | null = null;
  let disposed = false;
  // Mirrors the last emitted state independent of `disposed`, so settled() can answer synchronously once
  // nothing is in flight and has something to return after dispose().
  let currentState: QuotePreviewState<TResolved> = { status: "idle" };
  // Resolvers for settled() calls made while a request is in flight or queued. Flushed with whatever state
  // lands on the next non-pending emit(), or on dispose(). Shared, not per-token: a superseding update()
  // keeps the state "pending", so waiters stay queued until the final state.
  let settledWaiters: Array<(state: QuotePreviewState<TResolved>) => void> = [];

  function clearTimer(): void {
    if (timer !== null) {
      clearTimeout(timer);
      timer = null;
    }
  }

  function emit(state: QuotePreviewState<TResolved>): void {
    currentState = state;
    if (disposed) return;
    opts.onChange(state);
    if (state.status !== "pending" && settledWaiters.length > 0) {
      const waiters = settledWaiters;
      settledWaiters = [];
      for (const resolve of waiters) resolve(state);
    }
  }

  async function run(myToken: number, input: TInput): Promise<void> {
    // Both current fetchers never throw, so this catch is a safety net: `fetcher` is an injection point with
    // no enforced contract, and a rejection would otherwise leave currentState "pending" forever and hang
    // every settled() waiter.
    let result: TFetchResult;
    try {
      result = await opts.fetcher(input);
    } catch {
      if (disposed || myToken !== token) return; // superseded or torn down while the fetch was in flight
      lastKey = null; // see below: a failed attempt must not block a retry of the same input
      emit({ status: "error", errors: ["Couldn't reach the server."] });
      return;
    }
    if (disposed || myToken !== token) return; // superseded or torn down while the fetch was in flight
    const state = opts.toState(result);
    // A failed attempt must not poison lastKey: otherwise the dedup check would block a deliberate retry of
    // the same input (e.g. after a transient network failure) until an unrelated field changed.
    if (state.status === "error") lastKey = null;
    emit(state);
  }

  function update(input: TInput | null): void {
    if (disposed) return;
    if (input === null) {
      clearTimer();
      token++;
      lastKey = null;
      emit({ status: "idle" });
      return;
    }
    const key = JSON.stringify(input);
    if (key === lastKey) return; // nothing actually changed since the last request
    lastKey = key;
    clearTimer();
    emit({ status: "pending" });
    const myToken = ++token;
    timer = setTimeout(() => {
      timer = null;
      void run(myToken, input);
    }, debounceMs);
  }

  function dispose(): void {
    disposed = true;
    token++;
    clearTimer();
    if (settledWaiters.length > 0) {
      const waiters = settledWaiters;
      settledWaiters = [];
      for (const resolve of waiters) resolve(currentState);
    }
  }

  function settled(): Promise<QuotePreviewState<TResolved>> {
    if (disposed || (currentState.status !== "pending" && timer === null)) {
      return Promise.resolve(currentState);
    }
    return new Promise((resolve) => {
      settledWaiters.push(resolve);
    });
  }

  return { update, dispose, settled };
}
