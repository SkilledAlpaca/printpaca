import { focusAfterUpdate } from "../../lib/a11y.svelte";
import { keySelector } from "../../lib/a11yFocus";

/** The `data-focus-key` of a row's first action control: its ⋯ button, or its Remove button when Remove is the only action. Stable per row (not per position), so focus follows a row that moves. */
export function rowMenuKey(rowKey: string): string {
  return `row-menu-${rowKey}`;
}

/**
 * After a row is removed, focuses the first action control (⋯ or Remove) of the row that took its place (the one now at `index`, else the
 * one before it), else the list's Add button (`addKey`, a `data-focus-key`). The removed row held focus, so without
 * this it would fall to <body>. `remainingKeys` are the row keys left, in order.
 */
export function focusAfterRowRemoved(remainingKeys: readonly string[], index: number, addKey: string): Promise<void> {
  const next = remainingKeys.length > 0 ? remainingKeys[Math.min(index, remainingKeys.length - 1)] : undefined;
  return focusAfterUpdate(
    () => (next !== undefined ? document.querySelector<HTMLElement>(keySelector(rowMenuKey(next))) : null) ?? document.querySelector<HTMLElement>(keySelector(addKey)),
  );
}

/** The accessible name of a row's ⋯ trigger and of its action group. */
export function rowActionsLabel(label: string): string {
  return `More actions for ${label}`;
}
