import { fieldLabel, hasFieldLabel } from "./format";

export const GENERIC_SERVER_ERROR = "The server couldn't accept that request. Check your entries and try again.";

const US = new Intl.NumberFormat("en-US");

function capitalize(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1);
}

function sentence(s: string): string {
  const t = s.trim();
  if (t === "") return t;
  const c = capitalize(t);
  return /[.!?]$/.test(c) ? c : `${c}.`;
}

let currencySymbol = "$";

/** Sets the symbol used when a cent range in a server message is shown as money. App.svelte calls this with
 * the configured currency.symbol whenever the config loads. */
export function setErrorCurrencySymbol(symbol: string): void {
  currencySymbol = symbol;
}

function dollars(cents: number): string {
  return `${currencySymbol}${US.format(cents / 100)}`;
}

/** Rewrites a cent range in a server message ("must be 0-100000000", "must be between 0 and 100000000") as
 * money in the configured currency, because the UI takes whole currency units everywhere. */
function centsRangeToDollars(text: string): string {
  return text.replace(/(\d+)\s*(?:-|to|and)\s*(\d+)/, (_m, lo: string, hi: string) => `${dollars(Number(lo))} to ${dollars(Number(hi))}`)
    .replace(new RegExp(`^must be (?:between )?(?=${currencySymbol.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")})`), "must be from ");
}

export interface MappedError {
  /** Readable sentence, safe to show as-is. */
  message: string;
  /** The raw server text when the error was not recognized; null when the message is fully mapped. */
  details: string | null;
}

/** Maps one server validation string ("path: text") to a readable sentence. A path the UI knows becomes its
 * field label and cent ranges become dollars. A path the UI doesn't know falls back to a generic sentence
 * with the raw text kept in `details`. A string without a path prefix is already a sentence and passes
 * through capitalized. */
export function mapServerError(raw: string): MappedError {
  const text = raw.trim();
  const m = /^([A-Za-z_][\w.[\]]*):\s*(.+)$/.exec(text);
  if (!m) return { message: sentence(text), details: null };
  const path = m[1]!;
  const rest = m[2]!;
  if (!hasFieldLabel(path)) return { message: GENERIC_SERVER_ERROR, details: text };
  const label = fieldLabel(path);
  const key = path.replace(/\[\d+\]/g, "").split(".").pop() ?? path;
  const body = key.endsWith("Cents") ? centsRangeToDollars(rest) : rest;
  return { message: sentence(`${label}: ${body}`), details: null };
}

/** Joins mapped errors into one user-facing string. Unrecognized errors contribute the generic sentence once,
 * followed by a "Details:" line with the raw text. */
export function formatServerErrors(raws: string[]): string {
  const messages: string[] = [];
  const details: string[] = [];
  for (const raw of raws) {
    const mapped = mapServerError(raw);
    if (!messages.includes(mapped.message)) messages.push(mapped.message);
    if (mapped.details) details.push(mapped.details);
  }
  const body = messages.join(" ");
  return details.length > 0 ? `${body} Details: ${details.join("; ")}` : body;
}
