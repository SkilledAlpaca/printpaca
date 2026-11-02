/** Pure helpers behind InvoicesTab's detail/edit and payment forms, so the money/units boundary (dollars in
 * the form, cents and basis points in the API) and draft-vs-sent email gating are unit-testable. */

import { formatDateTime } from "./format";
import { toCents, invoiceTotals, type InvoiceTotals } from "./engine/index";
import { pctToBp, bpToPct, type Invoice, type InvoiceInput, type InvoiceLineKind } from "./invoicesApi";
import { todayInTimezone } from "./invoiceStatus";
import type { Client } from "./clientsApi";
import type { Item } from "./itemsApi";
import { itemUnitCents } from "./itemMath";

// The editable form keeps money in currency units and tax in percent; buildInput() converts to cents and
// basis points at the API edge.
export interface LineForm {
  description: string;
  quantity: number;
  unitPrice: number;
  discount: number;
  quoteId: string;
  /** "shipping" marks a line that is exempt from tax. */
  kind: InvoiceLineKind;
  /** The linked gallery item ("" when unlinked) and its chosen option. */
  itemId: string;
  optionName: string;
}
export interface InvoiceForm {
  clientId: string;
  clientName: string;
  issueDate: string;
  dueDate: string;
  taxPct: number;
  discount: number;
  notes: string;
  lines: LineForm[];
}

// `dates` stays optional so emptyForm is a pure helper with no clock or cfg dependency; only a brand-new
// draft (InvoicesTab's openNew) passes newInvoiceDates(). Existing invoices go through formFromInvoice.
export function emptyForm(defaultTaxPct: number, dates?: { issueDate: string; dueDate: string }): InvoiceForm {
  return {
    clientId: "",
    clientName: "",
    issueDate: dates?.issueDate ?? "",
    dueDate: dates?.dueDate ?? "",
    taxPct: defaultTaxPct,
    discount: 0,
    notes: "",
    lines: [],
  };
}

/** issueDate/dueDate for a new draft: issueDate defaults to today in the configured time zone (callers pass todayInTimezone(cfg.timezone)), dueDate to issueDate +
 * dueDays, the same formula as server/invoices.go's fillSentDates on draft->sent. `issueDate` is a
 * parameter so callers and tests can pin it. The dueDate arithmetic is UTC-based on purpose (adding whole
 * days to a Y-M-D string is timezone-agnostic). */
export function newInvoiceDates(
  dueDays: number,
  issueDate: string = todayInTimezone(),
): { issueDate: string; dueDate: string } {
  const base = new Date(`${issueDate}T00:00:00Z`);
  base.setUTCDate(base.getUTCDate() + dueDays);
  return { issueDate, dueDate: base.toISOString().slice(0, 10) };
}

export function formFromInvoice(inv: Invoice): InvoiceForm {
  return {
    clientId: inv.clientId,
    clientName: inv.clientId ? "" : inv.clientName,
    issueDate: inv.issueDate,
    dueDate: inv.dueDate,
    taxPct: bpToPct(inv.taxBp),
    discount: inv.discountCents / 100,
    notes: inv.notes,
    lines: inv.lines.map((l) => ({
      description: l.description,
      quantity: l.quantity,
      unitPrice: l.unitPriceCents / 100,
      discount: l.discountCents / 100,
      quoteId: l.quoteId,
      kind: l.kind,
      itemId: l.itemId,
      optionName: l.optionName,
    })),
  };
}

/** Seeds a duplicate draft: copies client, lines, invoice-level discount/tax and notes; drops id/number,
 * status, payments, the sent/paid log, each line's quoteId, and the dates (the caller fills them via
 * newInvoiceDates()). Each line keeps its kind, so a copied Shipping line stays exempt from tax. */
export function formFromInvoiceDuplicate(inv: Invoice): InvoiceForm {
  return {
    clientId: inv.clientId,
    clientName: inv.clientId ? "" : inv.clientName,
    issueDate: "",
    dueDate: "",
    taxPct: bpToPct(inv.taxBp),
    discount: inv.discountCents / 100,
    notes: inv.notes,
    lines: inv.lines.map((l) => ({
      description: l.description,
      quantity: l.quantity,
      unitPrice: l.unitPriceCents / 100,
      discount: l.discountCents / 100,
      quoteId: "",
      kind: l.kind,
      itemId: l.itemId,
      optionName: l.optionName,
    })),
  };
}

export function buildInput(form: InvoiceForm): InvoiceInput {
  return {
    clientId: form.clientId,
    clientName: form.clientId ? "" : form.clientName,
    issueDate: form.issueDate,
    dueDate: form.dueDate,
    taxBp: pctToBp(form.taxPct),
    discountCents: toCents(form.discount),
    notes: form.notes,
    lines: form.lines.map((l) => ({
      description: l.description,
      quantity: l.quantity,
      unitPriceCents: toCents(l.unitPrice),
      discountCents: toCents(l.discount),
      quoteId: l.quoteId,
      kind: l.kind,
      itemId: l.itemId,
      optionName: l.optionName,
    })),
  };
}

/** Links a line to a gallery item: fills the description ("Name" or "Name (Option)"), the unit price (item
 * price plus the option's delta), and the link fields. A quantity that isn't a whole number is rounded to
 * one, since linked lines move stock in whole units. Description and price stay editable afterward. */
export function linkLineToItem(line: LineForm, item: Item, optionName: string): LineForm {
  const option = item.options.find((o) => o.name === optionName);
  return {
    ...line,
    description: option ? `${item.name} (${option.name})` : item.name,
    unitPrice: itemUnitCents(item.priceCents, option?.deltaCents ?? 0) / 100,
    quantity: Math.max(1, Math.round(line.quantity)),
    itemId: item.id,
    optionName: option ? option.name : "",
  };
}

/** Clears a line's item link, keeping its description, price and quantity as typed. */
export function unlinkLine(line: LineForm): LineForm {
  return { ...line, itemId: "", optionName: "" };
}

/** Live totals preview with the engine's invoiceTotals (mirrors server/invoices.go's computeTotals); the
 * server recomputes on save. kind is passed per line so a shipping line previews exempt exactly as stored. */
export function previewTotalsFor(form: InvoiceForm): InvoiceTotals {
  return invoiceTotals(
    form.lines.map((l) => ({
      quantity: l.quantity,
      unitPriceCents: toCents(l.unitPrice),
      discountCents: toCents(l.discount),
      kind: l.kind,
    })),
    toCents(form.discount),
    pctToBp(form.taxPct),
  );
}

/** True once any line carries a quoteId, i.e. it came from a saved quote (Convert to invoice). */
export function hasQuoteProvenance(form: Pick<InvoiceForm, "lines">): boolean {
  return form.lines.some((l) => l.quoteId !== "");
}

/** Warns when the free-text "Client name" case-insensitively matches a known client while none is linked:
 * otherwise the invoice silently never shows in that client's detail or money summary. */
export function matchingClientByName(clients: Client[], form: Pick<InvoiceForm, "clientId" | "clientName">): Client | null {
  if (form.clientId !== "") return null;
  const typed = form.clientName.trim().toLowerCase();
  if (typed === "") return null;
  return clients.find((c) => c.name.trim().toLowerCase() === typed) ?? null;
}

// The payment-recording form; money stays in currency units, converted at the API edge in InvoicesTab's
// addPaymentSubmit().
export interface PaymentForm {
  paidDate: string;
  amount: number;
  method: string;
  note: string;
  reference: string;
}

/** `paidDate` defaults to the browser's date; callers pass todayInTimezone(cfg.timezone). It is a parameter so callers and tests can pin it. */
export function defaultPaymentForm(
  balanceCents: number,
  defaultMethod: string,
  paidDate: string = todayInTimezone(),
): PaymentForm {
  return {
    paidDate,
    amount: balanceCents / 100,
    method: defaultMethod,
    note: "",
    reference: "",
  };
}

// Disabled reason for "Email to client", from cfg.smtp.host (already loaded). "to" is never overridden here:
// the server defaults it to the invoice's frozen client-snapshot email, which emailDisabledReason checks
// exists.
export type InvoiceEmailDisabledReason = "unconfigured" | "no-email" | null;
export function emailDisabledReason(smtpHost: string, inv: Invoice): InvoiceEmailDisabledReason {
  if (smtpHost === "") return "unconfigured";
  if (!inv.clientEmail) return "no-email";
  return null;
}

// Sends carry a full RFC3339 timestamp (server/invoices.go's InvoiceSend.SentAt), unlike the display-ready
// YYYY-MM-DD issueDate/dueDate.
export function formatSentAt(iso: string): string {
  return formatDateTime(iso);
}
