import { tick } from "svelte";
import { quote, setActivePart } from "./state.svelte";
import { noteSaveAttempt } from "./saveAttempt.svelte";
import { findFieldByLabel } from "./fieldErrors";
import type { QuoteBlocker } from "./quoteBlockers";

/** Switches to the blocker's part, reveals its field errors, and focuses the input once the part's cards
 * have rendered. Shared by the cost bar and the Price and profit card, which both list what is missing. */
export async function goToBlocker(b: QuoteBlocker): Promise<void> {
  const target = b.partIndex === null ? undefined : quote.parts[b.partIndex];
  if (target) setActivePart(target.id);
  noteSaveAttempt();
  await tick();
  await tick();
  findFieldByLabel(b.inputLabel)?.focus();
}
