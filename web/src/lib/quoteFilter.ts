import { displayStatus, type DisplayStatus } from "./quoteStatus";
import type { SavedQuote } from "./api";

/** The Quotes list's status chips: every display status, plus "invoiced" (a quote converted to an invoice,
 * whatever its status). "all" shows everything. */
export type QuoteStatusFilter = "all" | DisplayStatus | "invoiced";

export const QUOTE_STATUS_CHIPS: { key: QuoteStatusFilter; label: string }[] = [
  { key: "all", label: "All" },
  { key: "draft", label: "Draft" },
  { key: "sent", label: "Sent" },
  { key: "accepted", label: "Accepted" },
  { key: "declined", label: "Declined" },
  { key: "expired", label: "Expired" },
  { key: "invoiced", label: "Invoiced" },
];

/** True when the quote passes the text and status filters (AND). The text matches the label or the client
 * name, case-insensitively; blank text matches everything. `now` is a parameter so tests can pin it. */
export function matchesQuoteFilter(
  quote: Pick<SavedQuote, "label" | "clientName" | "status" | "savedAt" | "invoicedAs">,
  text: string,
  status: QuoteStatusFilter,
  validDays: number,
  now: number,
): boolean {
  const q = text.trim().toLowerCase();
  if (q !== "" && !quote.label.toLowerCase().includes(q) && !(quote.clientName ?? "").toLowerCase().includes(q)) return false;
  if (status === "all") return true;
  if (status === "invoiced") return !!quote.invoicedAs;
  return displayStatus(quote, validDays, now) === status;
}
