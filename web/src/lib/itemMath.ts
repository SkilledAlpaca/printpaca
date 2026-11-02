/** Base price plus one option's delta, both cents: the per-unit price a gallery visitor pays. Never
 * negative in practice (server/items.go's validateItemInput rejects it). */
export function itemUnitCents(priceCents: number, deltaCents: number): number {
  return priceCents + deltaCents;
}

/** itemUnitCents times quantity. The public gallery order flow imports both for its live total. */
export function itemTotalCents(priceCents: number, deltaCents: number, quantity: number): number {
  return itemUnitCents(priceCents, deltaCents) * quantity;
}

/** New sortOrder pair for an up/down swap. Swapping tied values would be a no-op (refresh()'s ORDER BY
 * sort_order, name leaves the row unmoved), so ties keep `a`'s value and nudge `b` one step away from the
 * direction `a` is moving. `movingDown` is true when `b` sits later in the list. */
export function nextSortOrders(aSortOrder: number, bSortOrder: number, movingDown: boolean): [number, number] {
  if (aSortOrder !== bSortOrder) return [bSortOrder, aSortOrder];
  return movingDown ? [aSortOrder, aSortOrder - 1] : [aSortOrder - 1, aSortOrder];
}

/** Dollars typed into a form field to integer cents: the engine's `toCents` under this file's callers'
 * name, the one conversion point for the Items tab (convert once at the boundary). */
export { toCents as dollarsToCents } from "./engine/index";
