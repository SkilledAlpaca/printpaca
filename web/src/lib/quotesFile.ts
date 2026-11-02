import type { SavedQuote } from "./api";
import { displayPriceCents } from "./quoteRow";
import { statusSortWeight } from "./quoteStatus";

/** Export format: `inputs` carries formState ({version: 1, parts, order}). Import accepts only this
 * version. */
export interface QuotesFilePayload {
  version: 1;
  quotes: SavedQuote[];
}

export function buildQuotesFile(quotes: SavedQuote[]): QuotesFilePayload {
  return { version: 1, quotes };
}

export type QuoteSortOrder = "newest" | "oldest" | "price-desc" | "price-asc" | "label" | "status";

// savedAt is ISO 8601, so string comparison sorts chronologically.
function bySavedAtDesc(a: SavedQuote, b: SavedQuote): number {
  return b.savedAt.localeCompare(a.savedAt);
}

/** Pure sort for the history panel; returns a new array. Price orders use `displayPriceCents` and sort
 * cost-only quotes (no price) last in both directions. Price and label orders tiebreak on savedAt
 * descending. "status" groups by stored status; derived expiry never affects order (see quoteStatus.ts). */
export function sortQuotes(quotes: SavedQuote[], order: QuoteSortOrder): SavedQuote[] {
  const copy = [...quotes];
  switch (order) {
    case "newest":
      return copy.sort((a, b) => b.savedAt.localeCompare(a.savedAt));
    case "oldest":
      return copy.sort((a, b) => a.savedAt.localeCompare(b.savedAt));
    case "price-desc":
      return copy.sort(
        (a, b) => (displayPriceCents(b) ?? -Infinity) - (displayPriceCents(a) ?? -Infinity) || bySavedAtDesc(a, b),
      );
    case "price-asc":
      return copy.sort(
        (a, b) => (displayPriceCents(a) ?? Infinity) - (displayPriceCents(b) ?? Infinity) || bySavedAtDesc(a, b),
      );
    case "label":
      return copy.sort((a, b) => a.label.localeCompare(b.label) || bySavedAtDesc(a, b));
    case "status":
      return copy.sort((a, b) => statusSortWeight(a) - statusSortWeight(b) || bySavedAtDesc(a, b));
  }
}

const QUOTE_STATUSES: readonly string[] = ["draft", "sent", "accepted", "declined"];

// Structural check for one imported entry; failures are dropped and counted rather than crashing the import.
// An entry must carry a version 1 form state, a stored status and status log, and a part count.
export function isImportableQuote(x: unknown): x is SavedQuote {
  if (typeof x !== "object" || x === null) return false;
  const o = x as Record<string, unknown>;
  const formState = o.inputs as { version?: unknown; parts?: unknown } | null;
  return (
    typeof o.id === "string" &&
    typeof o.savedAt === "string" &&
    !Number.isNaN(Date.parse(o.savedAt)) &&
    typeof o.label === "string" &&
    typeof o.perUnitTotal === "number" &&
    Number.isFinite(o.perUnitTotal) &&
    typeof o.batchTotal === "number" &&
    Number.isFinite(o.batchTotal) &&
    typeof formState === "object" &&
    formState !== null &&
    formState.version === 1 &&
    Array.isArray(formState.parts) &&
    typeof o.status === "string" &&
    QUOTE_STATUSES.includes(o.status) &&
    Array.isArray(o.statusLog) &&
    typeof o.partCount === "number" &&
    Number.isInteger(o.partCount) &&
    o.partCount >= 1
  );
}

/** Parses an imported file: an object `{version: 1, quotes: [...]}`; anything else, including invalid JSON,
 * a bare array, or another version, returns null. Entries failing `isImportableQuote` are dropped and
 * counted as `skipped`. */
export function parseQuotesFile(text: string): { quotes: SavedQuote[]; skipped: number } | null {
  let parsed: unknown;
  try {
    parsed = JSON.parse(text);
  } catch {
    return null;
  }

  if (!parsed || typeof parsed !== "object") return null;
  const file = parsed as { version?: unknown; quotes?: unknown };
  if (file.version !== 1 || !Array.isArray(file.quotes)) return null;
  const rawList: unknown[] = file.quotes;

  const quotes: SavedQuote[] = [];
  let skipped = 0;
  for (const entry of rawList) {
    if (isImportableQuote(entry)) quotes.push(entry);
    else skipped++;
  }

  return { quotes, skipped };
}
