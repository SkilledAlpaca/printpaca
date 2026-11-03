import { tick } from "svelte";

// One polite and one assertive message, rendered by <LiveAnnouncer> in each app root (App.svelte, PublicApp.svelte).
// Regions stay mounted and only their text changes, so screen readers announce every update (WCAG 4.1.3).
export const announcer = $state({ polite: "", assertive: "" });

/** Announces a status message without moving focus. Repeating the same text announces it again. */
export function announce(message: string, politeness: "polite" | "assertive" = "polite"): void {
  announcer[politeness] = "";
  // Clear first, then set on the next frame: an unchanged string would not be re-announced.
  requestAnimationFrame(() => {
    announcer[politeness] = message;
  });
}

/**
 * Moves focus after Svelte has applied pending DOM updates (WCAG 2.4.3). Use it when the element to focus is
 * created, re-enabled, or made non-inert by the same state change. Accepts an element, a CSS selector, or a
 * getter for a bound element. Elements that aren't natively focusable need tabindex="-1".
 */
export async function focusAfterUpdate(target: HTMLElement | string | (() => HTMLElement | null | undefined)): Promise<void> {
  await tick();
  const el = typeof target === "string" ? document.querySelector<HTMLElement>(target) : typeof target === "function" ? target() : target;
  el?.focus();
}
