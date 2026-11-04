import { tick } from "svelte";
import { quote, setActivePart } from "./state.svelte";
import { noteSaveAttempt } from "./saveAttempt.svelte";
import { findFieldByLabel } from "./fieldErrors";
import type { QuoteBlocker } from "./quoteBlockers";

/** Where focus goes when the blocker's part or field no longer exists. */
function focusSummary(): void {
  document.querySelector<HTMLElement>('[role="group"][aria-label="Quote summary"]')?.focus();
}

/** Switches to the blocker's part, reveals its field errors, and focuses the input once the part's cards
 * have rendered. Shared by the cost bar and the Price and profit card, which both list what is missing.
 *
 * The blocker comes from the last price answer, which can be older than the editor. The part is therefore
 * found by id in the editor as it is at click time (never by the answer's index), and when the part or its
 * field is gone, focus goes to the summary instead of to some other part's field. */
export async function goToBlocker(b: QuoteBlocker): Promise<void> {
  if (b.partId !== null) {
    const target = quote.parts.find((p) => p.id === b.partId);
    if (!target) {
      focusSummary();
      return;
    }
    setActivePart(target.id);
  }
  noteSaveAttempt();
  await tick();
  await tick();
  // The part may have been removed while the cards rendered.
  if (b.partId !== null && quote.activePartId !== b.partId) {
    focusSummary();
    return;
  }
  const field = findFieldByLabel(b.inputLabel);
  if (!field) {
    focusSummary();
    return;
  }
  // A field inside a collapsed "More pricing options" (or any closed <details>) can't take focus: open it.
  for (let d = field.closest("details"); d; d = d.parentElement?.closest("details") ?? null) d.open = true;
  field.focus();
}
