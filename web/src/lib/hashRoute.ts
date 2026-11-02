/** Pure hash-routing helpers shared by App.svelte and the list/detail tabs, testable without a component. */

/** Splits "#/invoices#7a1c…" into its base route and an optional second "#" fragment (found from index 1). */
export function splitHash(hash: string): { base: string; fragment: string | null } {
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
