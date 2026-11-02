import { toCents, type OrderBreakdown } from "./engine/index";
import { isMultiPart } from "./convertQuote";
import { orderLines } from "./orderView";
import { errorsFrom } from "./clientsApi";
import { extractQuantity, extractShippingCharge, extractTaxRatePct } from "./convertQuote";
import { DEFAULT_FETCH_TIMEOUT_MS, EMAIL_SEND_TIMEOUT_MS } from "./fetchTimeouts";
import { displayPriceCents } from "./quoteRow";
import type { SavedQuote } from "./api";
import type { Invoice } from "./invoicesApi";

/** The three document-delivery endpoints share one failure shape: a non-2xx with a {"errors":[...]} body
 * (409 smtp unconfigured, 422 validation/no recipient, 502 the smtp error verbatim). `reason` classifies
 * the status so callers can react (e.g. point at settings only for "unconfigured"); message is the
 * verbatim server text. */
export type SendReason = "unconfigured" | "invalid" | "smtp-error" | "unknown";

export type SendOutcome<T> = { ok: true; data: T } | { ok: false; error: string; reason: SendReason };

/** Maps an HTTP status from a send endpoint to a SendReason. Pure; an undocumented status reads as
 * "unknown" rather than throwing. */
export function reasonForStatus(status: number): SendReason {
  if (status === 409) return "unconfigured";
  if (status === 422) return "invalid";
  if (status === 502) return "smtp-error";
  return "unknown";
}

async function outcomeFrom<T>(res: Response): Promise<SendOutcome<T>> {
  if (!res.ok) {
    const err = await errorsFrom(res);
    return { ok: false, error: err.message, reason: reasonForStatus(res.status) };
  }
  return { ok: true, data: (await res.json()) as T };
}

/** POST /api/invoices/{id}/send (server/invoices.go's handleSend). `to` defaults server-side to the
 * invoice's frozen client-snapshot email. Returns the updated Invoice (sends grows by one; a draft flips
 * to sent). */
export async function sendInvoiceEmail(invoiceId: string, to?: string): Promise<SendOutcome<Invoice>> {
  const res = await fetch(`api/invoices/${encodeURIComponent(invoiceId)}/send`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(to ? { to } : {}),
    signal: AbortSignal.timeout(EMAIL_SEND_TIMEOUT_MS),
  });
  return outcomeFrom<Invoice>(res);
}

/** The `quote` field of POST /api/quotes/send (server/quotes.go's quoteSendInput): narrower than
 * SavedQuote, exactly what the server needs to compose the email. */
export interface QuoteSendPayload {
  id: string;
  /** The quote number or name the customer sees; empty means the email says "your quote". */
  reference: string;
  label: string;
  clientName: string;
  quantity: number;
  unitPriceCents: number;
  shippingCents: number;
  savedAt: string;
  /** One row per part of a multi-part order (server/quotes.go quoteSendLine); when present the email
   * renders these instead of the flat quantity/unitPriceCents. Part label only. */
  lines?: { label: string; quantity: number; unitPriceCents: number }[];
  /* Whether the quoted price already includes tax: drives the email's tax note via the same predicate as
   * convertTaxBp, so the note and the eventual invoice can't disagree. */
  taxIncluded: boolean;
}

/** Builds a QuoteSendPayload using the same extraction helpers as HistoryPanel's "Invoice again"
 * (convertQuote.ts, quoteRow.ts's displayPriceCents), in lockstep with server/quotes.go's quoteSendInput.
 * Unlike invoiceLineFromQuote it never falls back to cost: emailing a cost as a price is never correct, so
 * a cost-only quote returns null ("can't send"; same check as quoteHasPrice). */
export function buildQuoteSendPayload(q: SavedQuote, order?: OrderBreakdown | null): QuoteSendPayload | null {
  const priceCents = displayPriceCents(q);
  if (priceCents === null) return null;
  let lines: QuoteSendPayload["lines"];
  if (isMultiPart(q)) {
    // A multi-part order's per-part prices live only in the server's order answer; without it the payload
    // can't be built truthfully.
    const priced = order ? orderLines(order) : [];
    if (priced.length === 0) return null;
    lines = priced.map((l) => ({ label: l.label, quantity: l.quantity, unitPriceCents: Math.max(0, Math.round(l.unitPriceCents)) }));
  }
  return {
    ...(lines ? { lines } : {}),
    id: q.id,
    reference: q.reference ?? "",
    label: q.label,
    clientName: q.clientName ?? "",
    quantity: extractQuantity(q.inputs),
    unitPriceCents: Math.max(0, Math.round(priceCents)),
    shippingCents: toCents(extractShippingCharge(q.inputs)),
    savedAt: q.savedAt,
    taxIncluded: extractTaxRatePct(q.inputs) > 0,
  };
}

/** POST /api/quotes/send's success body (server/quotes.go's quoteSendResponse): sentAt is the server's
 * clock, reused as `at` for applyTransition so the status log records when the email actually went out. */
export interface QuoteSendResult {
  ok: boolean;
  sentAt: string;
}

/** POST /api/quotes/send (server/quotes.go's handleQuoteSend). Persists nothing server-side; the caller
 * applies the quote's sent-status transition. */
export async function sendQuoteEmail(quote: QuoteSendPayload, to: string): Promise<SendOutcome<QuoteSendResult>> {
  const res = await fetch("api/quotes/send", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ quote, to }),
    signal: AbortSignal.timeout(EMAIL_SEND_TIMEOUT_MS),
  });
  return outcomeFrom<QuoteSendResult>(res);
}

/** POST /api/smtp/test's body (server/smtp.go): always HTTP 200 unless smtp is unconfigured (409, surfaced
 * as reason "unconfigured"); `ok`/`error` say whether the test send succeeded. */
export interface SmtpTestResult {
  ok: boolean;
  error: string;
}

/** POST /api/smtp/test (server/smtp.go's handleSmtpTest): sends a fixed test message to smtp.to. */
export async function testSmtp(): Promise<SendOutcome<SmtpTestResult>> {
  const res = await fetch("api/smtp/test", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: "{}",
    signal: AbortSignal.timeout(EMAIL_SEND_TIMEOUT_MS),
  });
  return outcomeFrom<SmtpTestResult>(res);
}

/** POST /api/integrations/test's body (server/proxy.go): `detail` is always an operator-facing string,
 * whether ok ("HTTP 200 from /api/v1/spool") or not ("connection refused"). */
export interface IntegrationsTestResult {
  ok: boolean;
  detail: string;
}

/** POST /api/integrations/test (server/proxy.go's handleIntegrationsTest): probes the url currently typed
 * in the Integrations card (not necessarily saved) against kind's endpoint. Unlike testSmtp, a 400 (bad
 * url/kind) is a distinct failure the card shows; reasonForStatus maps it to "unknown". */
export async function testIntegration(
  kind: "spoolman" | "moonraker",
  url: string,
): Promise<SendOutcome<IntegrationsTestResult>> {
  const res = await fetch("api/integrations/test", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ kind, url }),
    signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS),
  });
  return outcomeFrom<IntegrationsTestResult>(res);
}
