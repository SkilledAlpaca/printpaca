import type { EffectiveConfig } from "./engine/index";
import { defaultPrintStart } from "./state.svelte";

/** What the "Use the next HH:MM" action does to the order's print start, and what to tell the user.
 *
 * `value` is the next occurrence of the schedule's default start time, as wall-clock "YYYY-MM-DDTHH:MM" in the
 * shop's time zone. `changed` is false when the field already holds it: the field is seeded with the same value,
 * so a click then changes nothing on screen, and the message has to say so. */
export function useNextPrintStart(
  cfg: EffectiveConfig,
  current: string,
  partHasOwnStart: boolean,
  now: Date = new Date(),
): { value: string; changed: boolean; message: string } {
  const value = defaultPrintStart(cfg, now);
  const changed = value !== current;
  const shown = `${value.replace("T", " ")} (${cfg.timezone || "UTC"})`;
  let message = changed ? `Print start set to ${shown}.` : `Print start is already the next ${cfg.energy.schedule.defaultStartTime}: ${shown}.`;
  if (partHasOwnStart) message += " This part has its own start, so its price doesn't change.";
  return { value, changed, message };
}
