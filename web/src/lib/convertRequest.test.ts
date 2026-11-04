import { describe, expect, it, vi } from "vitest";
import { invoiceTotals } from "./engine/index";
import { convertRequestToInvoice, invoiceInputFromRequest, requestInvoiceLines, requestTaxBp } from "./convertRequest";
import type { QuoteRequest } from "./requestsApi";

function request(overrides: Partial<QuoteRequest> = {}): QuoteRequest {
  return {
    id: "r1",
    status: "viewed",
    name: "Sam",
    email: "sam@example.com",
    note: "",
    material: "PLA",
    quantity: 1,
    weightG: 10,
    timeSeconds: 600,
    priceCents: 1200,
    shippingCents: 400,
    taxInclusive: false,
    gcodeFilename: "",
    gcodePath: "",
    gcodeBytes: 0,
    createdAt: "2026-01-01T00:00:00.000Z",
    gcodePrunedAt: "",
    kind: "quote",
    itemId: "",
    itemName: "",
    optionName: "",
    updatedBy: "",
    invoiceId: "",
    invoiceNumber: "",
    invoiceStatus: "",
    needsPrintTime: false,
    reference: "",
    ...overrides,
  };
}

const cfg = (salesTaxPct: number) => ({ invoicing: { salesTaxPct } });

/** Totals the converted invoice will have, through the shared invoiceTotals. */
function requestInvoiceTotals(req: QuoteRequest, c: ReturnType<typeof cfg>) {
  return invoiceTotals(requestInvoiceLines(req), 0, requestTaxBp(req, c));
}

describe("requestTaxBp", () => {
  it("is 0 when the public price included tax, even with an invoicing default", () => {
    expect(requestTaxBp(request({ taxInclusive: true }), cfg(20))).toBe(0);
  });
  it("uses the invoicing default when the public price had no tax in it", () => {
    expect(requestTaxBp(request(), cfg(8.25))).toBe(825);
  });
  it("uses the invoicing default for a gallery item order, whose price carries no tax", () => {
    expect(requestTaxBp(request({ kind: "item" }), cfg(8))).toBe(800);
  });
});

describe("requestInvoiceLines", () => {
  it("adds the Shipping line the visitor saw, as a shipping-kind line", () => {
    const lines = requestInvoiceLines(request());
    expect(lines).toHaveLength(2);
    expect(lines[0]).toMatchObject({ description: "3D print: PLA", unitPriceCents: 1200 });
    expect(lines[1]).toEqual({
      description: "Shipping",
      kind: "shipping",
      quantity: 1,
      unitPriceCents: 400,
      discountCents: 0,
      quoteId: "",
    });
  });
  it("adds no Shipping line when the request stored none", () => {
    expect(requestInvoiceLines(request({ shippingCents: 0 }))).toHaveLength(1);
  });
  it("splits the order total into a per-unit price and names the file when there is one", () => {
    const lines = requestInvoiceLines(request({ quantity: 3, priceCents: 3000, shippingCents: 0, gcodeFilename: "a.gcode" }));
    expect(lines[0]).toMatchObject({ description: "3D print: PLA (a.gcode)", quantity: 3, unitPriceCents: 1000 });
  });
  it("names an item order after the item and option and carries the gallery item link", () => {
    const lines = requestInvoiceLines(
      request({ kind: "item", itemName: "Dragon", optionName: "Red", quantity: 2, priceCents: 3000, shippingCents: 0, itemId: "it1" }),
    );
    expect(lines[0]).toMatchObject({ description: "Dragon (Red)", quantity: 2, unitPriceCents: 1500, itemId: "it1", optionName: "Red" });
  });
});

describe("request to invoice totals", () => {
  // A tax-inclusive price of $12.00 plus $4.00 shipping: the visitor saw $16.00, and the invoice must total
  // $16.00, not $14.40 (double tax) or $12.00 (no shipping).
  it("totals what the visitor saw when the price included tax", () => {
    const t = requestInvoiceTotals(request({ taxInclusive: true }), cfg(20));
    expect(t.taxCents).toBe(0);
    expect(t.totalCents).toBe(1600);
  });
  it("taxes only the product line when the default sales tax applies", () => {
    const t = requestInvoiceTotals(request(), cfg(10));
    expect(t.taxCents).toBe(120);
    expect(t.totalCents).toBe(1720);
  });
  it("builds the POST body with the client id", () => {
    const input = invoiceInputFromRequest(request({ taxInclusive: true }), cfg(20), "c1");
    expect(input).toMatchObject({ clientId: "c1", taxBp: 0, discountCents: 0 });
    expect(input.lines).toHaveLength(2);
  });
  it("names the request so the server links and finalizes it with the invoice", () => {
    expect(invoiceInputFromRequest(request({ id: "r9" }), cfg(0), "c1").requestId).toBe("r9");
  });
});

describe("convertRequestToInvoice", () => {
  const cfg = { invoicing: { salesTaxPct: 0 } };
  function api(overrides: Record<string, unknown> = {}) {
    return {
      listClients: vi.fn(async () => []),
      createClient: vi.fn(async () => ({ id: "c-new" })),
      deleteClient: vi.fn(async () => undefined),
      createInvoice: vi.fn(async () => ({ id: "inv1" })),
      ...overrides,
    } as never as Parameters<typeof convertRequestToInvoice>[2] & Record<string, ReturnType<typeof vi.fn>>;
  }

  it("creates the client then the invoice", async () => {
    const a = api();
    await convertRequestToInvoice(request(), cfg, a);
    expect(a.createClient).toHaveBeenCalledOnce();
    expect(a.createInvoice).toHaveBeenCalledWith(expect.objectContaining({ clientId: "c-new" }));
    expect(a.deleteClient).not.toHaveBeenCalled();
  });

  it("removes the client it created when the invoice POST fails", async () => {
    const a = api({ createInvoice: vi.fn(async () => { throw new Error("already converted"); }) });
    await expect(convertRequestToInvoice(request(), cfg, a)).rejects.toThrow("already converted");
    expect(a.deleteClient).toHaveBeenCalledWith("c-new");
  });

  it("reuses a matching client and never deletes it on failure", async () => {
    const a = api({
      listClients: vi.fn(async () => [{ id: "c-old", email: "SAM@example.com" }]),
      createInvoice: vi.fn(async () => { throw new Error("409"); }),
    });
    await expect(convertRequestToInvoice(request(), cfg, a)).rejects.toThrow("409");
    expect(a.createClient).not.toHaveBeenCalled();
    expect(a.deleteClient).not.toHaveBeenCalled();
  });
});
