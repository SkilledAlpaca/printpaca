/** Pure helpers behind InvoicesTab's detail/edit and payment forms, so the money/units boundary (dollars in
 * the form, cents and basis points in the API) and draft-vs-sent email gating are unit-testable. */

import { formatDate, formatDateTime } from "./format";
import { toCents, invoiceTotals, type InvoiceTotals } from "./engine/index";
import { pctToBp, bpToPct, type Invoice, type InvoiceInput, type InvoiceLineKind } from "./invoicesApi";
import { todayInTimezone } from "./invoiceStatus";
import type { Client } from "./clientsApi";
import type { Item } from "./itemsApi";
import { itemUnitCents } from "./itemMath";
import { optionSummary, selectedDeltaCents, type Selected } from "./itemOptions";

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

/** Links a line to a gallery item: fills the description ("Name" or "Name (Color: Red · Size: Large)"), the
 * unit price (item price plus the selected options' deltas), and the link fields; optionName holds the same
 * summary the server stores for gallery orders. A quantity that isn't a whole number is rounded to
 * one, since linked lines move stock in whole units. Description and price stay editable afterward. */
export function linkLineToItem(line: LineForm, item: Item, selected: Selected): LineForm {
  const summary = optionSummary(item.optionGroups, selected);
  return {
    ...line,
    description: summary ? `${item.name} (${summary})` : item.name,
    unitPrice: itemUnitCents(item.priceCents, selectedDeltaCents(item.optionGroups, selected)) / 100,
    quantity: Math.max(1, Math.round(line.quantity)),
    itemId: item.id,
    optionName: summary,
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
    form.lines.map((l) =>
      // A line with a problem (negative or zero quantity, a sub-cent price) is left out of the preview rather
      // than priced from a value that can't be saved.
      lineHasProblem(l)
        ? { quantity: 0, unitPriceCents: 0, discountCents: 0, kind: l.kind }
        : {
            quantity: l.quantity,
            unitPriceCents: toCents(l.unitPrice),
            discountCents: toCents(l.discount),
            kind: l.kind,
          },
    ),
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

/**
 * The Amount the payment form should hold after the invoice's balance changed (a save, a new total, a
 * payment). A user-typed amount stays; an untouched one (still equal to the previous default) follows the
 * balance, so a freshly saved invoice never offers "Record $0.00". Returns the amount in currency units.
 */
export function followBalanceAmount(
  current: number | null | undefined,
  previousDefaultCents: number,
  balanceCents: number,
): number | null | undefined {
  const untouched = current === null || current === undefined || current === 0 || toCents(current) === previousDefaultCents;
  return untouched ? balanceCents / 100 : current;
}

// Disabled reason for "Email to client", from cfg.smtpConfigured (already loaded). "to" is never overridden here:
// the server defaults it to the invoice's frozen client-snapshot email, which emailDisabledReason checks
// exists.
export type InvoiceEmailDisabledReason = "unconfigured" | "no-email" | null;
export function emailDisabledReason(smtpConfigured: boolean, inv: Invoice): InvoiceEmailDisabledReason {
  if (!smtpConfigured) return "unconfigured";
  if (!inv.clientEmail) return "no-email";
  return null;
}

// Sends carry a full RFC3339 timestamp (server/invoices.go's InvoiceSend.SentAt), unlike the display-ready
// YYYY-MM-DD issueDate/dueDate.
export function formatSentAt(iso: string): string {
  return formatDateTime(iso);
}

// Money validation. The form keeps currency units in number inputs; toCents() would round a third decimal
// away silently ($0.333 becomes $0.33), so a value with more than two decimals is refused before it is sent.

/** True when `value` has at most two decimal places once float noise is ignored (0.1 + 0.2 passes). */
export function hasAtMostTwoDecimals(value: number): boolean {
  return Number.isFinite(value) && Math.abs(value * 100 - Math.round(value * 100)) < 1e-6;
}

/** A cleared number input binds as null; money fields treat that as zero, as toCents(0) does on save. */
function amountOf(value: number | null | undefined): number {
  return typeof value === "number" ? value : 0;
}

function moneyProblem(label: string, value: number | null | undefined): string | null {
  const v = amountOf(value);
  if (!Number.isFinite(v)) return `${label}: enter a number.`;
  if (v < 0) return `${label}: can't be negative.`;
  if (!hasAtMostTwoDecimals(v)) return `${label}: use at most two decimal places.`;
  return null;
}

/** The problems that stop an invoice save, one sentence each, naming the line (1-based) and field. The server
 * checks the same bounds; this runs first so a sub-cent price never reaches it. */
export function invoiceFormProblems(form: InvoiceForm): string[] {
  const problems: string[] = [];
  const add = (p: string | null) => {
    if (p) problems.push(p);
  };
  add(moneyProblem("Invoice discount", form.discount));
  form.lines.forEach((l, i) => {
    const n = i + 1;
    const qty = amountOf(l.quantity);
    if (!Number.isFinite(qty) || qty <= 0) problems.push(`Line ${n} quantity: must be greater than zero.`);
    add(moneyProblem(`Line ${n} unit price`, l.unitPrice));
    add(moneyProblem(`Line ${n} discount`, l.discount));
  });
  return problems;
}

/** The problems that stop a payment from being recorded. */
export function paymentFormProblems(form: PaymentForm): string[] {
  const problems: string[] = [];
  const amount = amountOf(form.amount);
  if (!Number.isFinite(amount) || amount <= 0) problems.push("Amount: must be greater than zero.");
  else if (!hasAtMostTwoDecimals(amount)) problems.push("Amount: use at most two decimal places.");
  if (form.paidDate === "") problems.push("Date: choose the day the payment was received.");
  return problems;
}

/** The overpayment, in cents: how far the payments on an invoice exceed its total (0 when they don't). The
 * Dashboard counts at most the total as revenue, so this is the part that never reaches it. */
export function overpaidCents(inv: Pick<Invoice, "paidCents" | "totalCents">): number {
  return Math.max(0, inv.paidCents - inv.totalCents);
}

/** "Record $7.35 cash, today": the button states what it will record. `amount` is the formatted money
 * string; the method is lowercased and omitted when empty; the date reads "today" when it is. */
export function recordPaymentLabel(amount: string, method: string, paidDate: string, today: string): string {
  const parts = [method.trim().toLowerCase(), paidDate === today ? "today" : `on ${formatDate(paidDate)}`].filter(Boolean);
  return `Record ${amount} ${parts.join(", ")}`.replace(/\s+/g, " ");
}

/** The confirm question for a payment above the balance. */
export function overpaymentQuestion(excess: string): string {
  return `This is ${excess} more than the balance. Record anyway?`;
}

export type TotalsRowKey = "subtotal" | "shipping" | "discount" | "tax" | "total";
export interface TotalsRow {
  key: TotalsRowKey;
  label: string;
  /** Signed: the discount row is negative, so it renders as "-$1.00" and the rows add up to the total. */
  cents: number;
}

/** The rows under an invoice's lines, shared by the editor, the sent view and the print view so they can't
 * disagree. Shipping lines are shown apart from the item subtotal and the invoice discount gets its own row,
 * so subtotal + shipping - discount + tax equals the total. Shipping and Discount appear only when non-zero.
 * (When the discount exceeds the subtotal the server clamps the total at tax-only, so the rows can then
 * overshoot; that is an edge a real invoice doesn't reach.) */
export function totalsRows(t: {
  lineKinds: string[];
  lineAmountsCents: number[];
  subtotalCents: number;
  discountCents: number;
  taxCents: number;
  totalCents: number;
}): TotalsRow[] {
  const shipping = t.lineAmountsCents.reduce((sum, c, i) => sum + (t.lineKinds[i] === "shipping" ? c : 0), 0);
  const rows: TotalsRow[] = [{ key: "subtotal", label: "Subtotal", cents: t.subtotalCents - shipping }];
  if (shipping !== 0) rows.push({ key: "shipping", label: "Shipping", cents: shipping });
  if (t.discountCents > 0) rows.push({ key: "discount", label: "Discount", cents: -t.discountCents });
  rows.push({ key: "tax", label: "Tax", cents: t.taxCents });
  rows.push({ key: "total", label: "Total", cents: t.totalCents });
  return rows;
}

/** Per-field problems of one invoice line, as short sentences for the message under the field. */
export interface LineProblems {
  quantity?: string;
  unitPrice?: string;
  discount?: string;
}

function moneyFieldProblem(value: number | null | undefined): string | undefined {
  const v = amountOf(value);
  if (!Number.isFinite(v)) return "Enter a number.";
  if (v < 0) return "Can't be negative.";
  if (!hasAtMostTwoDecimals(v)) return "Use at most two decimal places.";
  return undefined;
}

/** What is wrong with a line right now, as you type: a quantity of zero or less, a negative or sub-cent price
 * or discount. Empty when the line can be saved. */
export function lineProblems(l: Pick<LineForm, "quantity" | "unitPrice" | "discount">): LineProblems {
  const out: LineProblems = {};
  const qty = amountOf(l.quantity);
  if (!Number.isFinite(qty) || qty <= 0) out.quantity = "Must be greater than zero.";
  const price = moneyFieldProblem(l.unitPrice);
  if (price) out.unitPrice = price;
  const discount = moneyFieldProblem(l.discount);
  if (discount) out.discount = discount;
  return out;
}

export function lineHasProblem(l: Pick<LineForm, "quantity" | "unitPrice" | "discount">): boolean {
  return Object.keys(lineProblems(l)).length > 0;
}
