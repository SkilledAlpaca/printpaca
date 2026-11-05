import { untrack } from "svelte";
import { appConfig } from "./state.svelte";

/**
 * Runs `load` when an integration-backed component mounts and again whenever the app config is refreshed
 * (appConfig.current is replaced after every Settings save, so a saved Spoolman or Moonraker URL applies live)
 * or `enabled()` flips. Call it during component setup.
 *
 * Why it must re-run: the config refetch after a save can land after the user opened the screen. The screen
 * then mounted with the old `integrations.spoolman` / `moonraker` flag, or loaded from the old URL, and a
 * load that ran only on mount would stay on "Loading…" or on stale data until a full reload.
 *
 * `load` receives an AbortSignal that fires when a newer run starts or the component is destroyed, so a
 * request still pending on the old URL cannot land after the new one. `onDisabled` runs when `enabled()` is
 * false, to drop data fetched from a URL that is gone.
 */
export function useLiveLoad(
  enabled: () => boolean,
  load: (signal: AbortSignal) => void | Promise<void>,
  onDisabled?: () => void,
): void {
  $effect(() => {
    void appConfig.current; // re-run on every config refresh
    const on = enabled();
    const controller = new AbortController();
    untrack(() => {
      if (on) void load(controller.signal);
      else onDisabled?.();
    });
    return () => controller.abort();
  });
}
