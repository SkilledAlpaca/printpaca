import { errorsFrom } from "./clientsApi";
import { DEFAULT_FETCH_TIMEOUT_MS, GCODE_DOWNLOAD_TIMEOUT_MS } from "./fetchTimeouts";
import { pageSuffix, totalFromHeader, type Page, type PagedResult } from "./pagination";

/** Mirrors server/requests.go's QuoteRequest json tags: every quote_requests column, including gcodePath
 * (admin-only route, so the filesystem detail is not secret). */
export interface QuoteRequest {
  id: string;
  status: string;
  name: string;
  email: string;
  note: string;
  material: string;
  quantity: number;
  weightG: number;
  timeSeconds: number;
  priceCents: number;
  /** The shipping charge the visitor saw beside the price, in cents; 0 for item orders. Not part of
   * priceCents. */
  shippingCents: number;
  /** Whether priceCents included tax when the visitor submitted. */
  taxInclusive: boolean;
  gcodeFilename: string;
  gcodePath: string;
  gcodeBytes: number;
  createdAt: string;
  /** Set by server/retention.go once retention removes a finalized request's stored file past its grace
   * period. "" while the file is intact or was never stored (storage-full accept path; see
   * gcodeMessage.ts's classifyRequestFileState). */
  gcodePrunedAt: string;
  /** kind is "quote" for pricing-engine estimates or "item" for gallery orders. itemId/itemName/optionName
   * are set only for "item"; the names are display snapshots at order time, not foreign keys. */
  kind: "quote" | "item";
  itemId: string;
  itemName: string;
  optionName: string;
  /** Who last touched this request's status (server/requests.go). No createdBy: the public flow has no
   * staff identity. "" while still "new". */
  updatedBy: string;
  /** The invoice this request was converted to ("" when not converted). A converted request is finalized; the
   * link is what the admin app shows as "Invoiced". invoiceNumber and invoiceStatus come through the link and
   * are "" when the invoice was deleted. A canceled invoice (invoiceStatus "canceled") frees the request to be
   * converted again. */
  invoiceId: string;
  invoiceNumber: string;
  invoiceStatus: string;
  /** A public estimate submitted without a print time: timeSeconds and priceCents are 0 until the owner
   * enters the time (setRequestPrintTime). */
  needsPrintTime: boolean;
  /** The short per-shop reference the customer saw ("HL-1001"); "" for rows from before references existed. */
  reference: string;
}

/** A network failure returns null so the inbox can show an offline note. Same optional-pagination overload
 * as the other list fetchers (see pagination.ts). */
export async function loadRequests(): Promise<QuoteRequest[] | null>;
export async function loadRequests(page: Page): Promise<PagedResult<QuoteRequest> | null>;
export async function loadRequests(page?: Page): Promise<QuoteRequest[] | PagedResult<QuoteRequest> | null> {
  try {
    const url = page ? `api/requests${pageSuffix(page)}` : "api/requests";
    const res = await fetch(url, { signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS) });
    if (!res.ok) return null;
    const body = (await res.json()) as { requests?: unknown };
    const items = Array.isArray(body.requests) ? (body.requests as QuoteRequest[]) : [];
    return page ? { items, total: totalFromHeader(res, items.length, page) } : items;
  } catch {
    return null;
  }
}

/** GET one request. The server flips status new -> viewed as a side effect (handleGet): opening it in the
 * admin app is what "viewed" means. */
export async function loadRequest(id: string): Promise<QuoteRequest> {
  const res = await fetch(`api/requests/${encodeURIComponent(id)}`, { signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS) });
  if (!res.ok) throw await errorsFrom(res);
  return (await res.json()) as QuoteRequest;
}

export async function finalizeRequest(id: string): Promise<QuoteRequest> {
  const res = await fetch(`api/requests/${encodeURIComponent(id)}/status`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ status: "finalized" }),
    signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS),
  });
  if (!res.ok) throw await errorsFrom(res);
  return (await res.json()) as QuoteRequest;
}

export async function deleteRequest(id: string): Promise<void> {
  const res = await fetch(`api/requests/${encodeURIComponent(id)}`, {
    method: "DELETE",
    signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS),
  });
  if (!res.ok) throw await errorsFrom(res);
}

/** Bulk-deletes every request still "new" (server/requests.go's handleDeleteNew), the recovery path for a
 * public-request flood. Resolves to the number removed. */
export async function deleteAllNewRequests(): Promise<number> {
  const res = await fetch("api/requests/new", {
    method: "DELETE",
    signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS),
  });
  if (!res.ok) throw await errorsFrom(res);
  const body = (await res.json()) as { deletedCount?: number };
  return typeof body.deletedCount === "number" ? body.deletedCount : 0;
}

/** Fetches the stored file as text. The server serves it gunzipped as application/octet-stream (g-code, STL
 * or 3MF bytes), not text/plain. */
export async function fetchRequestGcode(id: string): Promise<string> {
  const res = await fetch(`api/requests/${encodeURIComponent(id)}/gcode`, {
    signal: AbortSignal.timeout(GCODE_DOWNLOAD_TIMEOUT_MS),
  });
  if (!res.ok) throw await errorsFrom(res);
  return await res.text();
}

export interface RequestsStorageUsage {
  usedBytes: number;
  capBytes: number;
}

/** CONFIG_DIR/uploads usage vs. server/public.go's publicUploadsCapBytes, backing RequestsTab's storage
 * banner. A network failure or non-OK response returns null so the banner is absent rather than failing
 * the tab. */
export async function fetchRequestsStorage(): Promise<RequestsStorageUsage | null> {
  try {
    const res = await fetch("api/requests/storage", { signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS) });
    if (!res.ok) return null;
    const body = (await res.json()) as Partial<RequestsStorageUsage>;
    if (typeof body.usedBytes !== "number" || typeof body.capBytes !== "number") return null;
    return { usedBytes: body.usedBytes, capBytes: body.capBytes };
  } catch {
    return null;
  }
}

/** Supplies the print time a public estimate was submitted without. The server prices the request from the
 * current settings and clears needsPrintTime; resolves to the updated request. */
export async function setRequestPrintTime(id: string, timeSeconds: number): Promise<QuoteRequest> {
  const res = await fetch(`api/requests/${encodeURIComponent(id)}/print-time`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ timeSeconds }),
    signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS),
  });
  if (!res.ok) throw await errorsFrom(res);
  return (await res.json()) as QuoteRequest;
}
