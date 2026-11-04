import { afterEach, describe, expect, it, vi } from "vitest";
import { deleteAllNewRequests, loadRequests, type QuoteRequest } from "./requestsApi";

function request(overrides: Partial<QuoteRequest> = {}): QuoteRequest {
  return {
    id: "r1",
    status: "new",
    name: "Sam",
    email: "sam@example.com",
    note: "",
    material: "PLA",
    quantity: 1,
    weightG: 10,
    timeSeconds: 600,
    priceCents: 500,
    taxInclusive: false,
    shippingCents: 0,
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

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("loadRequests", () => {
  it("returns null on a network failure", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("network down")));
    expect(await loadRequests()).toBeNull();
  });

  it("called without a page: sends no query params and returns a plain array, byte-identical to before pagination", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ requests: [request()] }),
    } as unknown as Response);
    vi.stubGlobal("fetch", fetchMock);
    const result = await loadRequests();
    expect(fetchMock.mock.calls[0]?.[0]).toBe("api/requests");
    expect(result).toEqual([request()]);
  });

  it("called with a page: appends ?limit=&offset= and returns {items, total} from X-Total-Count", async () => {
    const headers = new Headers({ "X-Total-Count": "5" });
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      headers,
      json: async () => ({ requests: [request()] }),
    } as unknown as Response);
    vi.stubGlobal("fetch", fetchMock);
    const result = await loadRequests({ limit: 100, offset: 0 });
    expect(fetchMock.mock.calls[0]?.[0]).toBe("api/requests?limit=100&offset=0");
    expect(result).toEqual({ items: [request()], total: 5 });
  });
});

describe("deleteAllNewRequests", () => {
  it("DELETEs /api/requests/new and resolves to deletedCount", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ deletedCount: 3 }),
    } as unknown as Response);
    vi.stubGlobal("fetch", fetchMock);

    const result = await deleteAllNewRequests();

    expect(fetchMock.mock.calls[0]?.[0]).toBe("api/requests/new");
    expect(fetchMock.mock.calls[0]?.[1]).toMatchObject({ method: "DELETE" });
    expect(result).toBe(3);
  });

  it("throws the server's error message on a non-OK response", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: false,
      status: 500,
      json: async () => ({ errors: ["requests not writable"] }),
    } as unknown as Response);
    vi.stubGlobal("fetch", fetchMock);

    await expect(deleteAllNewRequests()).rejects.toThrow("Requests not writable.");
  });
});
