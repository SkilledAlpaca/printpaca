import { afterEach, describe, expect, it, vi } from "vitest";
import { listInvoices, type InvoiceSummary } from "./invoicesApi";

function invoiceSummary(overrides: Partial<InvoiceSummary> = {}): InvoiceSummary {
  return {
    id: "i1",
    number: "INV-0001",
    status: "draft",
    clientId: "",
    clientName: "",
    clientEmail: "",
    clientBillingAddress: "",
    clientShippingAddress: "",
    issueDate: "",
    dueDate: "",
    taxBp: 0,
    discountCents: 0,
    notes: "",
    subtotalCents: 0,
    taxCents: 0,
    totalCents: 0,
    createdAt: "2026-01-01T00:00:00.000Z",
    updatedAt: "2026-01-01T00:00:00.000Z",
    paidCents: 0,
    balanceCents: 0,
    createdBy: "",
    updatedBy: "",
    ...overrides,
  };
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("listInvoices", () => {
  it("returns null on a network failure", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("network down")));
    expect(await listInvoices()).toBeNull();
  });

  it("called without a page: sends no query params and returns a plain array, byte-identical to before pagination", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ invoices: [invoiceSummary()] }),
    } as unknown as Response);
    vi.stubGlobal("fetch", fetchMock);
    const result = await listInvoices();
    expect(fetchMock.mock.calls[0]?.[0]).toBe("api/invoices");
    expect(result).toEqual([invoiceSummary()]);
  });

  it("called with a page: appends ?limit=&offset= and returns {items, total} from X-Total-Count", async () => {
    const headers = new Headers({ "X-Total-Count": "9" });
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      headers,
      json: async () => ({ invoices: [invoiceSummary()] }),
    } as unknown as Response);
    vi.stubGlobal("fetch", fetchMock);
    const result = await listInvoices({ limit: 100, offset: 0 });
    expect(fetchMock.mock.calls[0]?.[0]).toBe("api/invoices?limit=100&offset=0");
    expect(result).toEqual({ items: [invoiceSummary()], total: 9 });
  });
});
