import { errorsFrom } from "./clientsApi";
import { DEFAULT_FETCH_TIMEOUT_MS } from "./fetchTimeouts";
import { pageSuffix, totalFromHeader, type Page, type PagedResult } from "./pagination";

export type InvoiceStatus = "draft" | "sent" | "paid" | "canceled";

export type InvoiceLineKind = "item" | "shipping";

interface InvoiceLine {
  id: string;
  description: string;
  quantity: number;
  unitPriceCents: number;
  discountCents: number;
  quoteId: string;
  /** "shipping" lines are exempt from tax; everything else is "item". */
  kind: InvoiceLineKind;
  /** The gallery item this line sells ("" when unlinked), and the chosen option name. */
  itemId: string;
  optionName: string;
  /** Units the send took off the item's shelf; read-only, set by the server. */
  stockTaken: number;
  amountCents: number;
}

/** One payment record (server/payments.go's Payment). Append-and-delete only; there is no edit endpoint. */
interface Payment {
  id: string;
  paidDate: string;
  amountCents: number;
  method: string;
  note: string;
  /** Free-text pointer to where the money moved outside printpaca ("Square #4821"). "" when none. */
  reference: string;
  createdAt: string;
  /** Who recorded this payment; createdBy and updatedBy are always equal (no edit). "" when none. */
  createdBy: string;
  updatedBy: string;
}

/** One recorded delivery event (server/invoices.go's InvoiceSend), append-only, oldest first. channel is
 * "email" (POST .../send) or "manual" (a status PATCH to "sent"). */
interface InvoiceSend {
  id: string;
  sentAt: string;
  channel: "email" | "manual";
  recipient: string;
}

/** The JSON shape served by GET (single), POST, PUT and the status endpoint (server/invoices.go Invoice). */
export interface Invoice {
  id: string;
  number: string;
  status: InvoiceStatus;
  clientId: string;
  clientName: string;
  clientEmail: string;
  clientBillingAddress: string;
  clientShippingAddress: string;
  issueDate: string;
  dueDate: string;
  taxBp: number;
  discountCents: number;
  notes: string;
  subtotalCents: number;
  taxCents: number;
  totalCents: number;
  createdAt: string;
  updatedAt: string;
  lines: InvoiceLine[];
  /** The shop request this invoice was created from (full invoice only; '' or absent when none). */
  requestId?: string;
  requestReference?: string;
  payments: Payment[];
  paidCents: number;
  balanceCents: number;
  /** Delivery history, oldest first; only on the full Invoice, not InvoiceSummary. */
  sends: InvoiceSend[];
  /** Who created/last wrote this invoice (server/invoices.go). "" when unattributed. */
  createdBy: string;
  updatedBy: string;
  /** Concurrency token: starts at 1 and every write bumps it. Send it back on a PUT; a stale one is a 409
   * (see VersionConflictError). */
  version: number;
}

/** The list endpoint's shape: every Invoice field except lines, payments and sends. */
export type InvoiceSummary = Omit<Invoice, "lines" | "payments" | "sends">;

/** The POST /api/invoices/{id}/payments body (server/payments.go's paymentInput). */
export interface PaymentInput {
  paidDate: string;
  amountCents: number;
  method?: string;
  note?: string;
  reference?: string;
}

export interface InvoiceLineInput {
  description: string;
  quantity: number;
  unitPriceCents: number;
  discountCents: number;
  quoteId?: string;
  /** Omitted means "item". "shipping" exempts the line from tax. */
  kind?: InvoiceLineKind;
  /** Links the line to a gallery item so a counter sale counts in Top items and takes stock on send. Leave
   * unset for lines converted from a request or quote: that sale is already counted. */
  itemId?: string;
  optionName?: string;
}

/** The POST/PUT body (server/invoices.go's invoiceInput). Omitted or null taxBp means "use the configured
 * default". The server rejects unknown fields, so add no extra keys. */
export interface InvoiceInput {
  clientId?: string;
  clientName?: string;
  issueDate?: string;
  dueDate?: string;
  taxBp?: number | null;
  discountCents?: number;
  notes?: string;
  lines: InvoiceLineInput[];
  /** PUT only: the version the editor loaded. A different current version makes the server answer 409. */
  version?: number;
  /** POST only: converts this quote request. The server links it to the new invoice and finalizes it in the
   * same transaction, so Convert to invoice leaves the request "Invoiced". */
  requestId?: string;
}

/** Sales tax travels as a float percent in the UI and converts to integer basis points only at the API
 * edge, mirroring the server's defaultTaxBp conversion. */
export function pctToBp(pct: number): number {
  return Math.round(pct * 100);
}
export function bpToPct(bp: number): number {
  return bp / 100;
}

/** A network failure returns null so the page can show an offline note. Same optional-pagination overload
 * as listClients (see pagination.ts). */
export async function listInvoices(): Promise<InvoiceSummary[] | null>;
export async function listInvoices(page: Page): Promise<PagedResult<InvoiceSummary> | null>;
export async function listInvoices(page?: Page): Promise<InvoiceSummary[] | PagedResult<InvoiceSummary> | null> {
  try {
    const url = page ? `api/invoices${pageSuffix(page)}` : "api/invoices";
    const res = await fetch(url, { signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS) });
    if (!res.ok) return null;
    const body = (await res.json()) as { invoices?: unknown };
    const items = Array.isArray(body.invoices) ? (body.invoices as InvoiceSummary[]) : [];
    return page ? { items, total: totalFromHeader(res, items.length, page) } : items;
  } catch {
    return null;
  }
}

export async function getInvoice(id: string): Promise<Invoice> {
  const res = await fetch(`api/invoices/${encodeURIComponent(id)}`, { signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS) });
  if (!res.ok) throw await errorsFrom(res);
  return (await res.json()) as Invoice;
}

export async function createInvoice(input: InvoiceInput): Promise<Invoice> {
  const res = await fetch("api/invoices", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
    signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS),
  });
  if (!res.ok) throw await errorsFrom(res);
  return (await res.json()) as Invoice;
}

/** A save sent with a stale version: the invoice was changed in another tab or by another user. The editor
 * offers Reload and Overwrite instead of an error line. */
export class VersionConflictError extends Error {
  constructor() {
    super("This was changed in another tab or by another user.");
    this.name = "VersionConflictError";
  }
}

export async function updateInvoice(id: string, input: InvoiceInput): Promise<Invoice> {
  const res = await fetch(`api/invoices/${encodeURIComponent(id)}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
    signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS),
  });
  if (res.status === 409) {
    // Only the version conflict becomes the typed error; "only draft invoices can be edited" keeps its text.
    const body = (await res.clone().json().catch(() => ({}))) as { errors?: unknown };
    if (Array.isArray(body.errors) && body.errors.some((e) => typeof e === "string" && e.includes("changed in another tab"))) {
      throw new VersionConflictError();
    }
  }
  if (!res.ok) throw await errorsFrom(res);
  return (await res.json()) as Invoice;
}

export async function setInvoiceStatus(id: string, status: InvoiceStatus): Promise<Invoice> {
  const res = await fetch(`api/invoices/${encodeURIComponent(id)}/status`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ status }),
    signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS),
  });
  if (!res.ok) throw await errorsFrom(res);
  return (await res.json()) as Invoice;
}

export async function deleteInvoice(id: string): Promise<void> {
  const res = await fetch(`api/invoices/${encodeURIComponent(id)}`, {
    method: "DELETE",
    signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS),
  });
  if (!res.ok) throw await errorsFrom(res);
}

export async function addPayment(invoiceId: string, input: PaymentInput): Promise<Invoice> {
  const res = await fetch(`api/invoices/${encodeURIComponent(invoiceId)}/payments`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
    signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS),
  });
  if (!res.ok) throw await errorsFrom(res);
  return (await res.json()) as Invoice;
}

export async function deletePayment(invoiceId: string, paymentId: string): Promise<Invoice> {
  const res = await fetch(
    `api/invoices/${encodeURIComponent(invoiceId)}/payments/${encodeURIComponent(paymentId)}`,
    { method: "DELETE", signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS) },
  );
  if (!res.ok) throw await errorsFrom(res);
  return (await res.json()) as Invoice;
}
