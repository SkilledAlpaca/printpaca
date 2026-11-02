// Shared focus helpers for admin views (WCAG 2.4.3). The rules live in the WCAG fix spec, decision 3:
// opening a record focuses its h1; closing it focuses the row that opened it; a two-step confirm focuses the
// confirm button, and Escape or Cancel put focus back on the trigger; a confirmed delete focuses the next row,
// else the previous row, else the list heading.
import { tick } from "svelte";
import { focusAfterUpdate } from "../lib/a11y.svelte";

/** Svelte action: focuses the element when it mounts. Use on a detail view's h1 (tabindex="-1"). */
export function focusOnMount(node: HTMLElement): void {
  node.focus({ preventScroll: false });
}

/** Attribute value for `data-row-id`, the hook that "back to list" uses to find the row that opened a record. */
export function rowSelector(id: string): string {
  return `[data-row-id="${CSS.escape(id)}"]`;
}

/** Selector for a trigger tagged with `data-focus-key`. */
export function keySelector(key: string): string {
  return `[data-focus-key="${CSS.escape(key)}"]`;
}

/** The id to focus after `id` leaves `ids`: the next one, else the previous one, else null. */
export function neighborId(ids: readonly string[], id: string): string | null {
  const i = ids.indexOf(id);
  if (i < 0) return null;
  return ids[i + 1] ?? ids[i - 1] ?? null;
}

/** Focuses the row for `id` (the opener on "back to list", or a neighbor after a delete), else the list heading. */
export function focusRowOrHeading(id: string | null | undefined): Promise<void> {
  return focusAfterUpdate(() => (id ? document.querySelector<HTMLElement>(rowSelector(id)) : null) ?? document.querySelector<HTMLElement>(".page h1, main h1"));
}

interface ConfirmFocusOptions {
  /** Selector of the trigger that opened the confirm. Focus returns there when the confirm closes and focus was lost. */
  restore?: string;
  /** Called on Escape inside the confirm group; the handler stops the event so a tab-level Escape (close detail) doesn't also run. */
  cancel?: () => void;
}

/**
 * Svelte action for the confirm button of a two-step confirm. It takes focus when it appears (the trigger it
 * replaced was destroyed, so focus would otherwise fall to <body>). When the confirm closes and focus has been
 * lost, focus goes back to the trigger, whether the user cancelled, pressed Escape, or the action finished.
 * If the trigger is gone too (a deleted row), the caller moves focus itself with focusRowOrHeading.
 */
export function confirmFocus(node: HTMLElement, options: ConfirmFocusOptions = {}) {
  node.focus();
  const group = node.parentElement ?? node;
  const onKeydown = (e: KeyboardEvent) => {
    if (e.key !== "Escape" || !options.cancel) return;
    e.preventDefault();
    e.stopPropagation();
    options.cancel();
  };
  group.addEventListener("keydown", onKeydown);
  return {
    destroy() {
      group.removeEventListener("keydown", onKeydown);
      if (!options.restore) return;
      const selector = options.restore;
      void tick().then(() => {
        const active = document.activeElement;
        if (active && active !== document.body) return;
        document.querySelector<HTMLElement>(selector)?.focus();
      });
    },
  };
}

/** After the DOM updates, focuses `selector` only if focus was lost to <body> (the focused control was replaced). */
export async function refocusIfLost(selector: string): Promise<void> {
  await tick();
  const active = document.activeElement;
  if (active && active !== document.body) return;
  document.querySelector<HTMLElement>(selector)?.focus();
}

/**
 * After a row is removed from an editable list, focuses the Remove button that took its place (`data-focus-key`
 * of `${prefix}-${index}`), else the list's Add button (`addKey`). The removed row held focus, so without this
 * it falls to <body>.
 */
export function focusAfterRowRemoval(prefix: string, index: number, remaining: number, addKey: string): Promise<void> {
  return focusAfterUpdate(
    () => (remaining > 0 ? document.querySelector<HTMLElement>(keySelector(`${prefix}-${Math.min(index, remaining - 1)}`)) : null) ?? document.querySelector<HTMLElement>(keySelector(addKey)),
  );
}
