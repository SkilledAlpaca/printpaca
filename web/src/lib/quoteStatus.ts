/** Stored quote status. `expired` is deliberately not a member: it is always derived for display
 * (displayStatus), so a later acceptance beats an earlier expiry check and no job ever mutates saved
 * quotes. */
export type QuoteStatus = "draft" | "sent" | "accepted" | "declined";

/** What the ledger and badges show: a stored QuoteStatus, or the derived-only "expired". */
export type DisplayStatus = QuoteStatus | "expired";

export interface StatusLogEntry {
  status: QuoteStatus;
  at: string; // ISO 8601 timestamp
  // Recipient address, present only on a "sent" entry recorded by emailing the quote (the log entry is the
  // send record). Optional so every earlier entry shape round-trips.
  sentTo?: string;
}

const ACTIVE_STATUSES: ReadonlySet<QuoteStatus> = new Set(["draft", "sent"]);
const MS_PER_DAY = 86_400_000;

/** Structural type instead of importing SavedQuote, so api.ts can import QuoteStatus/StatusLogEntry from
 * here without a cycle. */
export interface QuoteStatusFields {
  status: QuoteStatus;
  savedAt: string;
}

/** The status to show: the stored one, except a draft or sent quote
 * reports "expired" once `validDays` have elapsed since `savedAt`. Pure: `now` (epoch ms) is a parameter. */
export function displayStatus(quote: QuoteStatusFields, validDays: number, now: number): DisplayStatus {
  const stored = quote.status;
  if (!ACTIVE_STATUSES.has(stored)) return stored;
  const expiresAt = Date.parse(quote.savedAt) + validDays * MS_PER_DAY;
  return now > expiresAt ? "expired" : stored;
}

export interface QuoteStatusLogFields {
  status: QuoteStatus;
  statusLog: StatusLogEntry[];
}

/** Applies an explicit transition: returns a new object (never mutating `quote` or its log) with `status`
 * set and a `{ status, at, sentTo? }` entry appended. Pure: the caller supplies `at`. Generic over any
 * status/statusLog shape. `sentTo` is only for an email-delivered "sent" transition. */
export function applyTransition<T extends QuoteStatusLogFields>(
  quote: T,
  status: QuoteStatus,
  at: string,
  sentTo?: string,
): T {
  const entry: StatusLogEntry = sentTo ? { status, at, sentTo } : { status, at };
  return { ...quote, status, statusLog: [...quote.statusLog, entry] };
}

/** The verb on a manual transition button. Badge and lede text both say "sent", so the button matches;
 * draft -> sent can also happen automatically (emailing a quote), which TRANSITION_HINTS.sent explains. */
export const TRANSITION_LABELS: Record<QuoteStatus, string> = {
  draft: "Mark draft",
  sent: "Mark sent",
  accepted: "Mark accepted",
  declined: "Mark declined",
};

/** Tooltip for a transition button. Only "sent" needs one, because it can also happen automatically. */
export const TRANSITION_HINTS: Partial<Record<QuoteStatus, string>> = {
  sent: "Record that you sent this quote yourself, for example as a printout or by delivery outside printpaca. Emailing a quote from here marks it sent automatically.",
};

/** Past-tense phrase for the post-action status line, kept in step with TRANSITION_LABELS. */
export const STATUS_MARKED_MESSAGE: Record<QuoteStatus, string> = {
  draft: "marked draft",
  sent: "marked sent",
  accepted: "marked accepted",
  declined: "marked declined",
};

const STATUS_SORT_ORDER: Record<QuoteStatus, number> = { draft: 0, sent: 1, accepted: 2, declined: 3 };

/** Sort weight for the ledger's status order (draft, sent, accepted, declined) by stored status; derived
 * expiry never affects sort. */
export function statusSortWeight(quote: { status: QuoteStatus }): number {
  return STATUS_SORT_ORDER[quote.status];
}
