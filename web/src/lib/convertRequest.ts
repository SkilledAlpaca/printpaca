import { pctToBp, type InvoiceInput, type InvoiceLineInput } from "./invoicesApi";
import { SHIPPING_LINE_DESCRIPTION } from "./convertQuote";
import type { QuoteRequest } from "./requestsApi";
import type { Client, ClientInput } from "./clientsApi";
import type { Invoice } from "./invoicesApi";

/** The config fields the request conversion reads. */
export interface RequestConversionConfig {
  invoicing: { salesTaxPct: number };
}

/** Tax basis points for an invoice made from a public request. A pricing-engine ("quote") request's price
 * already includes tax when pricing.taxRatePct was above 0 at submit (the server stores that as taxInclusive),
 * so the invoice adds none, the same rule as convertQuote.ts's convertTaxBp. A gallery item order's price is
 * the item's listed price with no tax in it, so it takes the invoicing default. */
export function requestTaxBp(req: QuoteRequest, cfg: RequestConversionConfig): number {
  // The basis stored at submit wins over the current pricing.taxRatePct.
  if (req.kind !== "item" && req.taxInclusive) return 0;
  return pctToBp(cfg.invoicing.salesTaxPct);
}

/** The invoice lines for a public request: the product line, then the Shipping line the visitor saw beside
 * the price when the request stored one (shippingCents is 0 for item orders). The Shipping line has kind "shipping", so it stays out of the taxed subtotal. */
export function requestInvoiceLines(req: QuoteRequest): InvoiceLineInput[] {
  const product: InvoiceLineInput =
    req.kind === "item"
      ? {
          description: req.optionName ? `${req.itemName} (${req.optionName})` : req.itemName,
          quantity: req.quantity,
          // priceCents is the order total (server-computed unit x quantity), so this division is exact.
          unitPriceCents: req.priceCents / req.quantity,
          discountCents: 0,
          quoteId: "",
          // Linked to the gallery item with its option summary, so the invoice line shows what was ordered.
          // The server finalizes the request on conversion WITHOUT recording a sale or taking stock, so
          // sending the invoice counts the sale and the stock once. It drops the link instead when the order
          // was already finalized (that sale is already counted) or the item has been deleted.
          itemId: req.itemId,
          optionName: req.itemId ? req.optionName : "",
        }
      : {
          description: req.gcodeFilename ? `3D print: ${req.material} (${req.gcodeFilename})` : `3D print: ${req.material}`,
          quantity: req.quantity,
          // priceCents is server-computed at insert, the one authoritative figure to invoice from.
          unitPriceCents: Math.round(req.priceCents / req.quantity),
          discountCents: 0,
          quoteId: "",
        };
  const lines = [product];
  if (req.shippingCents > 0) {
    lines.push({
      description: SHIPPING_LINE_DESCRIPTION,
      kind: "shipping",
      quantity: 1,
      unitPriceCents: req.shippingCents,
      discountCents: 0,
      quoteId: "",
    });
  }
  return lines;
}

/** The POST /api/invoices body for converting a request. */
export function invoiceInputFromRequest(req: QuoteRequest, cfg: RequestConversionConfig, clientId: string): InvoiceInput {
  return {
    clientId,
    taxBp: requestTaxBp(req, cfg),
    discountCents: 0,
    notes: "",
    lines: requestInvoiceLines(req),
    // The server links the request to the new invoice and finalizes it in the same transaction.
    requestId: req.id,
  };
}

/** The API calls convertRequestToInvoice makes, injected so the flow is testable without a server. */
export interface RequestConversionApi {
  listClients(): Promise<Client[] | null>;
  createClient(input: ClientInput): Promise<Client>;
  deleteClient(id: string): Promise<void>;
  createInvoice(input: InvoiceInput): Promise<Invoice>;
}

/**
 * Converts a request to an invoice for the client with its email, creating that client first when none
 * exists. The invoice needs the client id, so the client comes first; when the invoice POST then fails (a
 * 409 because another conversion won, say) a client created by this call is deleted again, so a failed
 * conversion leaves no orphan. A matching client that was already on file is never touched.
 */
export async function convertRequestToInvoice(req: QuoteRequest, cfg: RequestConversionConfig, api: RequestConversionApi): Promise<Invoice> {
  const existing = await api.listClients();
  const match = (existing ?? []).find((c) => c.email.toLowerCase() === req.email.toLowerCase());
  let createdId = "";
  let clientId = match?.id ?? "";
  if (!match) {
    const created = await api.createClient({ name: req.name, email: req.email, phone: "", billingAddress: "", shippingAddress: "", notes: "" });
    createdId = created.id;
    clientId = created.id;
  }
  try {
    return await api.createInvoice(invoiceInputFromRequest(req, cfg, clientId));
  } catch (err) {
    if (createdId) await api.deleteClient(createdId).catch(() => undefined);
    throw err;
  }
}
