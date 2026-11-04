/** Pure hash-routing helpers shared by App.svelte and the list/detail tabs, testable without a component. */

/** An invoice's address is `#/invoices/<id>`; the older `#/invoices#<id>` still resolves. */
export function invoiceHash(id: string): string {
  return `#/invoices/${encodeURIComponent(id)}`;
}

/** Splits "#/invoices#7a1c…" into its base route and an optional second "#" fragment (found from index 1).
 * The newer "#/invoices/7a1c…" is read the same way: base "#/invoices", fragment "7a1c…". */
export function splitHash(hash: string): { base: string; fragment: string | null } {
  const inv = /^#\/invoices\/([^/#]+)$/.exec(hash);
  if (inv) {
    try {
      return { base: "#/invoices", fragment: decodeURIComponent(inv[1]!) };
    } catch {
      return { base: "#/invoices", fragment: null };
    }
  }
  const cut = hash.indexOf("#", 1);
  return cut > 0 ? { base: hash.slice(0, cut), fragment: hash.slice(cut + 1) } : { base: hash, fragment: null };
}

/** Reducer behind each list/detail tab's hashchange handler: opens a different id, closes the detail view,
 * or does nothing. `isOpen` is separate from `currentId` because composing an unsaved record shows the
 * detail with a null id, and a hashchange to the bare list hash must still close it. */
export function syncDetail(
  hash: string,
  base: string,
  currentId: string | null,
  isOpen: boolean,
  onOpen: (id: string) => void,
  onClose: () => void,
): void {
  const split = splitHash(hash);
  if (split.base !== base) return; // a different tab; App.svelte owns that transition
  if (split.fragment) {
    if (split.fragment !== currentId) onOpen(split.fragment);
  } else if (isOpen) {
    onClose();
  }
}

/** The editor's address for a saved quote is `#/quote/<id>`. Maps it to the tab's own hash (`#/quote`) so the
 * router finds the tab; any other base passes through. */
export function quoteRouteBase(base: string): string {
  return base.startsWith("#/quote/") ? "#/quote" : base;
}

/** The saved quote id in a `#/quote/<id>` address, or null for `#/quote` and every other route. */
export function quoteIdFromHash(hash: string): string | null {
  const m = /^#\/quote\/([^/#]+)/.exec(hash);
  if (!m) return null;
  try {
    return decodeURIComponent(m[1]!);
  } catch {
    return null;
  }
}
