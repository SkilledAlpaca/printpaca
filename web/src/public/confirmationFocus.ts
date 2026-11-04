import { tick } from "svelte";

/** Moves focus to a confirmation's region and scrolls its card to the top of the view. The region holds the
 * heading, the amount and the reference, so a magnifier user at 200% sees them where focus lands instead of a
 * lone heading at the bottom edge (WCAG 2.4.3, 1.4.4). */
export async function focusConfirmation(region: () => HTMLElement | null | undefined): Promise<void> {
  await tick();
  const el = region();
  if (!el) return;
  el.focus({ preventScroll: true });
  (el.closest<HTMLElement>(".confirmation-card") ?? el).scrollIntoView({ block: "start" });
}

/** Wraps a confirmation action so a tap that lands within `ms` of the confirmation appearing does nothing. A
 * second tap on Place order or Request an estimate hits whatever replaced it; without this guard it dismissed
 * the confirmation and lost the reference. */
export function createTapGuard(ms = 500): { arm: () => void; guarded: (fn: () => void) => () => void } {
  let until = 0;
  return {
    arm: () => {
      until = performance.now() + ms;
    },
    guarded: (fn) => () => {
      if (performance.now() < until) return;
      fn();
    },
  };
}
