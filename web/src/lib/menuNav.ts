/** Keyboard behavior shared by the row ⋯ menus (Quotes rows, the Requests header menu). The panels are labeled
 * groups of plain buttons, not ARIA menus, so this adds only what keyboard users expect of a button list:
 * ArrowUp/ArrowDown move between items (wrapping), Home and End jump to the ends, and Tab closes the panel
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
      return current < 0 ? 0 : (current + 1) % count;
    case "ArrowUp":
      return current < 0 ? count - 1 : (current - 1 + count) % count;
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
    close(true);
    return false; // not consumed: the browser's Tab now starts from the toggle
  }
  const items = menuItems(panel);
  const next = nextMenuIndex(e.key, items.indexOf(document.activeElement as HTMLElement), items.length);
  if (next === null) return false;
  e.preventDefault();
  items[next]?.focus();
  return true;
}
