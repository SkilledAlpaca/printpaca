import type { EngineOptions, OrderBreakdown, OrderInputs } from "./engine/index";
import { errorsFrom } from "./clientsApi";
import { DEFAULT_FETCH_TIMEOUT_MS } from "./fetchTimeouts";
import { pageSuffix, totalFromHeader, type Page, type PagedResult } from "./pagination";
import type { QuoteStatus, StatusLogEntry } from "./quoteStatus";
import type { SavedQuote } from "./api";

/** The JSON shape of every /api/quotes endpoint (server/quotestore.go's Quote). Fields through formState
 * are client-writable (see QuoteInput); fields from quantity on are server-computed and ignored on
 * POST/PUT. */
export interface ServerQuote {
  id: string;
  savedAt: string;
  label: string;
  clientId: string | null;
  clientName: string;
  project: string;
  reference: string;
  status: QuoteStatus;
  statusLog: StatusLogEntry[];
  invoicedAs: string | null;
  /** The engine inputs bag (state.svelte.ts's toOrderInputs), verbatim as last written, not the editor's
   * form snapshot (see formState). */
  inputs: OrderInputs;
  options: EngineOptions;
  /** Opaque snapshot of the admin editor's form state, stored and echoed verbatim:
   * printer/material/fee-schedule identity and raw fields can't be recovered from `inputs`/`options`.
   * Always a JSON object, never null. */
  formState: unknown;
  quantity: number;
  perUnitTotalCents: number;
  batchTotalCents: number;
  recommendedPriceCents: number | null;
  resolvedPriceCents: number | null;
  netProfitCents: number | null;
  achievedMarginPct: number | null;
  valid: boolean;
  /** Parts in the stored inputs bag. At 2 or more the output columns describe
   * the order as one unit (quantity 1, per-unit = batch = sum of part batch totals, prices = order item
   * revenue). */
  partCount: number;
  /** Who created/last wrote this quote: the env admin's literal "admin" or an employee's username
   * (server/quotestore.go). AttributionLine.svelte gates display (single-user mode and
   * the demo user-mode toggle hide it). */
  createdBy: string;
  updatedBy: string;
}

/** The POST/PUT body: every client-writable ServerQuote field. Sending id, savedAt or a server-computed
 * field is a 400 (DisallowUnknownFields). */
export interface QuoteInput {
  label: string;
  clientId?: string;
  clientName: string;
  project: string;
  reference: string;
  status: QuoteStatus;
  statusLog: StatusLogEntry[];
  invoicedAs?: string;
  inputs: OrderInputs;
  options: EngineOptions;
  formState: unknown;
}

/** A network failure returns null so the page can show an offline note. Same optional-pagination overload
 * as listClients/listInvoices (see pagination.ts). */
export async function listQuotes(): Promise<ServerQuote[] | null>;
export async function listQuotes(page: Page): Promise<PagedResult<ServerQuote> | null>;
export async function listQuotes(page?: Page): Promise<ServerQuote[] | PagedResult<ServerQuote> | null> {
  try {
    const url = page ? `api/quotes${pageSuffix(page)}` : "api/quotes";
    const res = await fetch(url, { signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS) });
    if (!res.ok) return null;
    const body = (await res.json()) as { quotes?: unknown };
    const items = Array.isArray(body.quotes) ? (body.quotes as ServerQuote[]) : [];
    return page ? { items, total: totalFromHeader(res, items.length, page) } : items;
  } catch {
    return null;
  }
}

export async function createQuote(input: QuoteInput): Promise<ServerQuote> {
  const res = await fetch("api/quotes", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
    signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS),
  });
  if (!res.ok) throw await errorsFrom(res);
  return (await res.json()) as ServerQuote;
}

/** The server refused a save because the quote changed after it was loaded (409). */
export class QuoteConflictError extends Error {
  constructor() {
    super("This was changed in another tab or by another user.");
    this.name = "QuoteConflictError";
  }
}

/** One quote by id, or null when it doesn't exist or can't be read. */
export async function getQuote(id: string): Promise<ServerQuote | null> {
  try {
    const res = await fetch(`api/quotes/${encodeURIComponent(id)}`, { signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS) });
    return res.ok ? ((await res.json()) as ServerQuote) : null;
  } catch {
    return null;
  }
}

/** PUT /api/quotes/{id}. With `expectedSavedAt` (the savedAt the caller loaded) a stale save is refused
 * with a QuoteConflictError instead of overwriting; without it the write always lands. */
export async function updateQuote(id: string, input: QuoteInput, expectedSavedAt?: string): Promise<ServerQuote> {
  const headers: Record<string, string> = { "Content-Type": "application/json" };
  if (expectedSavedAt) headers["If-Match"] = `"${expectedSavedAt}"`;
  const res = await fetch(`api/quotes/${encodeURIComponent(id)}`, {
    method: "PUT",
    headers,
    body: JSON.stringify(input),
    signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS),
  });
  if (res.status === 409) throw new QuoteConflictError();
  if (!res.ok) throw await errorsFrom(res);
  return (await res.json()) as ServerQuote;
}

export async function deleteQuote(id: string): Promise<void> {
  const res = await fetch(`api/quotes/${encodeURIComponent(id)}`, {
    method: "DELETE",
    signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS),
  });
  if (!res.ok) throw await errorsFrom(res);
}

async function postCompute(inputs: OrderInputs, options: EngineOptions): Promise<unknown> {
  try {
    const res = await fetch("api/quote/compute", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ inputs, options }),
      signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS),
    });
    return res.ok ? await res.json() : undefined;
  } catch {
    return undefined;
  }
}

/** POST /api/quote/compute's outcome. ok:true means the server computed a breakdown (valid may still be
 * false for an incomplete form); ok:false means no usable answer (network error, non-2xx, unparseable
 * body). QuoteTab needs the split: a stale valid breakdown stays on screen and printable while a request
 * is in flight, but only ok:false blocks printing. */
export type ComputeOrderResult = { ok: true; order: OrderBreakdown } | { ok: false };

/** POST /api/quote/compute: the authoritative price-on-demand endpoint, independent of the quotes CRUD.
 * Never throws; failure resolves to {ok:false}. */
export async function computeOrder(inputs: OrderInputs, options: EngineOptions): Promise<ComputeOrderResult> {
  const body = await postCompute(inputs, options);
  return body === undefined ? { ok: false } : { ok: true, order: body as OrderBreakdown };
}

/** The server's OrderBreakdown for a stored quote's own inputs bag, for convert and send to get per-part
 * prices. Priced from the stored bag (not current config) to match the saved totals. Null on a failed
 * request. */
export async function orderForQuote(q: Pick<ServerQuote, "inputs" | "options">): Promise<OrderBreakdown | null> {
  const r = await computeOrder(q.inputs, q.options);
  return r.ok ? r.order : null;
}

/** Maps a server Quote onto SavedQuote. `inputs` is deliberately `q.formState`, not `q.inputs`:
 * SavedQuote.inputs is the editor snapshot that HistoryPanel's restore() copies back and extractQuantity
 * and friends read; the engine bag can't fill that role (printer/material identity is unrecoverable from
 * resolved numbers). */
export function toSavedQuote(q: ServerQuote): SavedQuote {
  return {
    id: q.id,
    savedAt: q.savedAt,
    label: q.label,
    clientId: q.clientId ?? undefined,
    clientName: q.clientName,
    project: q.project || undefined,
    reference: q.reference || undefined,
    status: q.status,
    statusLog: q.statusLog,
    invoicedAs: q.invoicedAs ?? undefined,
    perUnitTotal: q.perUnitTotalCents,
    batchTotal: q.batchTotalCents,
    recommendedPrice: q.recommendedPriceCents ?? undefined,
    resolvedPrice: q.resolvedPriceCents ?? undefined,
    inputs: q.formState,
    partCount: q.partCount,
    createdBy: q.createdBy || undefined,
    updatedBy: q.updatedBy || undefined,
  };
}

/** The hand-supplied half of a QuoteInput (everything but `inputs`/`options`/`formState`), so call sites
 * can build just this and hand it to toQuoteInput. */
export interface QuoteInputMeta {
  label: string;
  clientId?: string;
  clientName: string;
  project: string;
  reference: string;
  status: QuoteStatus;
  statusLog: StatusLogEntry[];
  invoicedAs?: string;
}

/** Assembles a QuoteInput from the engine bag, options, form snapshot and metadata: the one place the wire
 * shape is built, so every save, re-PUT, duplicate and import agree. */
export function toQuoteInput(
  formState: unknown,
  engineInputs: OrderInputs,
  options: EngineOptions,
  meta: QuoteInputMeta,
): QuoteInput {
  return { ...meta, inputs: engineInputs, options, formState };
}
