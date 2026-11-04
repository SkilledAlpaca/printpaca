import type { OrderBreakdown } from "./engine/index";
import { quote, partLabel, orderStamp } from "./state.svelte";
import { quoteBlockers, type QuoteBlocker } from "./quoteBlockers";

/** The blockers of the latest price answer, mapped onto the editor as it is now. The answer can describe an
 * earlier state of the part list (a part duplicated, removed or renamed since), so each blocker is tied to a
 * part by id and labelled from the live part; one whose part no longer exists is dropped. */
export function currentBlockers(order: OrderBreakdown): QuoteBlocker[] {
  const ids = order.parts.map((_, i) => orderStamp.partIds[i] ?? quote.parts[i]?.id ?? "");
  const labels = ids.map((id) => {
    const at = quote.parts.findIndex((p) => p.id === id);
    return at < 0 ? "" : partLabel(quote.parts[at]!, at);
  });
  return quoteBlockers(order, labels, ids).filter((b) => b.partId === null || quote.parts.some((p) => p.id === b.partId));
}
