/** Keyboard behavior shared by the row ⋯ menus (Quotes rows, the Requests header menu). The panels are labeled
 * groups of plain buttons, not ARIA menus, so this adds only what keyboard users expect of a button list:
 * ArrowUp/ArrowDown move between items and stop at the ends (A12: wrapping past a disabled Move down landed on Move up), Home and End jump to the ends, and Tab closes the panel
 * with focus back on its toggle so the browser's Tab continues from there instead of dropping to <body>. */

/** The enabled items of an open panel, in DOM order. */
export function menuItems(panel: Element): HTMLElement[] {
  return Array.from(panel.querySelectorAll<HTMLElement>(".menu-item")).filter(
    (el) => !el.hasAttribute("disabled") && el.getAttribute("aria-disabled") !== "true",
  );
}

/** The index to focus after `key`, or null when the key isn't one this handles. `current` is -1 when focus
 * isn't on an item (the panel itself or the toggle). */
export function nextMenuIndex(key: string, current: number, count: number): number | null {
  if (count <= 0) return null;
  switch (key) {
    case "ArrowDown":
      return current < 0 ? 0 : Math.min(current + 1, count - 1);
    case "ArrowUp":
      return current < 0 ? count - 1 : Math.max(current - 1, 0);
    case "Home":
      return 0;
    case "End":
      return count - 1;
    default:
      return null;
  }
}

/** Keydown handler for an open panel. Returns true when it consumed the event. `close(restoreFocus)` closes the
 * panel; Tab calls it with true so focus lands on the toggle before the browser moves it on. */
export function handleMenuKeydown(e: KeyboardEvent, panel: Element, close: (restoreFocus: boolean) => void): boolean {
  if (e.key === "Tab") {
    const toggle = panel.id ? document.querySelector<HTMLElement>(`[aria-controls="${CSS.escape(panel.id)}"]`) : null;
    if (toggle && !e.shiftKey) {
      // The panel is still in the DOM while this keydown runs, so the browser's own Tab from the toggle would
      // step into it and then lose focus to <body> when it unmounts. Move focus to the element after the
      // toggle ourselves, skipping the panel.
      const target = tabTargetAfter(toggle, panel);
      close(false);
      e.preventDefault();
      (target ?? toggle).focus();
      return true;
    }
    close(true);
    return false; // Shift+Tab: the toggle is focused and the browser steps back from it
  }
  const items = menuItems(panel);
  const next = nextMenuIndex(e.key, items.indexOf(document.activeElement as HTMLElement), items.length);
  if (next === null) return false;
  e.preventDefault();
  items[next]?.focus();
  return true;
}

const TABBABLE = 'a[href], button, input, select, textarea, summary, [tabindex]';

/** The next Tab stop after `toggle` that isn't inside `panel`, or null when the toggle is the last one. */
export function tabTargetAfter(toggle: HTMLElement, panel: Element): HTMLElement | null {
  const all = Array.from(document.querySelectorAll<HTMLElement>(TABBABLE)).filter(
    (el) =>
      !panel.contains(el) &&
      !el.hasAttribute("disabled") &&
      el.getAttribute("tabindex") !== "-1" &&
      !el.closest("[hidden], [inert]") &&
      el.getClientRects().length > 0,
  );
  const i = all.indexOf(toggle);
  return i >= 0 ? (all[i + 1] ?? null) : null;
}
