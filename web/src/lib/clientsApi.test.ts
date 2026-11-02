import { afterEach, describe, expect, it, vi } from "vitest";
import { listClients, type Client } from "./clientsApi";

function client(overrides: Partial<Client> = {}): Client {
  return {
    id: "c1",
    name: "Sam",
    email: "sam@example.com",
    phone: "",
    billingAddress: "",
    shippingAddress: "",
    notes: "",
    createdAt: "2026-01-01T00:00:00.000Z",
    updatedAt: "2026-01-01T00:00:00.000Z",
    createdBy: "",
    updatedBy: "",
    ...overrides,
  };
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("listClients", () => {
  it("returns null on a network failure", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("network down")));
    expect(await listClients()).toBeNull();
  });

  it("called without a page: sends no query params and returns a plain array, byte-identical to before pagination", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ clients: [client()] }),
    } as unknown as Response);
    vi.stubGlobal("fetch", fetchMock);
    const result = await listClients();
    expect(fetchMock.mock.calls[0]?.[0]).toBe("api/clients");
    expect(result).toEqual([client()]);
  });

  it("called with a page: appends ?limit=&offset= and returns {items, total} from X-Total-Count", async () => {
    const headers = new Headers({ "X-Total-Count": "250" });
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      headers,
      json: async () => ({ clients: [client()] }),
    } as unknown as Response);
    vi.stubGlobal("fetch", fetchMock);
    const result = await listClients({ limit: 100, offset: 100 });
    expect(fetchMock.mock.calls[0]?.[0]).toBe("api/clients?limit=100&offset=100");
    expect(result).toEqual({ items: [client()], total: 250 });
  });

  it("falls back to items.length + offset when X-Total-Count is missing", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      headers: new Headers(),
      json: async () => ({ clients: [client(), client({ id: "c2" })] }),
    } as unknown as Response);
    vi.stubGlobal("fetch", fetchMock);
    const result = await listClients({ limit: 100, offset: 10 });
    expect(result).toEqual({ items: [client(), client({ id: "c2" })], total: 12 });
  });

  it("returns null on a non-2xx response even when a page is passed", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: false, status: 500 } as unknown as Response));
    expect(await listClients({ limit: 100, offset: 0 })).toBeNull();
  });
});
