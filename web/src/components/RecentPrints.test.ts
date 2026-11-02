import { describe, expect, it } from "vitest";
import { metadataFetchSignal } from "./RecentPrints.svelte";

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

// metadataFetchSignal combines the caller's signal with a timeout so a stalled response body can't leave
// `applying` true forever. Real timers: AbortSignal.timeout doesn't observe vi.useFakeTimers().
describe("metadataFetchSignal", () => {
  it("aborts once the timeout elapses even though the caller's own controller never fires", async () => {
    const controller = new AbortController();
    const signal = metadataFetchSignal(controller.signal, 10);
    expect(signal.aborted).toBe(false);
    await sleep(30);
    expect(signal.aborted).toBe(true);
    expect(controller.signal.aborted).toBe(false); // the caller's own signal is untouched
  });

  it("aborts immediately when the caller's own controller aborts (a superseding pick)", () => {
    const controller = new AbortController();
    const signal = metadataFetchSignal(controller.signal, 5000);
    controller.abort();
    expect(signal.aborted).toBe(true);
  });

  it("never aborts before the timeout when the caller doesn't abort", async () => {
    const controller = new AbortController();
    const signal = metadataFetchSignal(controller.signal, 200);
    await sleep(10);
    expect(signal.aborted).toBe(false);
  });
});
