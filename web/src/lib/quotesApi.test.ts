import { afterEach, describe, expect, it, vi } from "vitest";
import {
  computeOrder,
  createQuote,
  deleteQuote,
  listQuotes,
  toQuoteInput,
  toSavedQuote,
  updateQuote,
  type ServerQuote,
} from "./quotesApi";

function jsonResponse(body: unknown, status = 200) {
  return { ok: status >= 200 && status < 300, status, json: async () => body } as unknown as Response;
}

function serverQuote(overrides: Partial<ServerQuote> = {}): ServerQuote {
  return {
    id: "q1",
    savedAt: "2026-01-01T00:00:00.000Z",
    label: "Benchy",
    clientId: null,
    clientName: "",
    project: "",
    reference: "",
    status: "draft",
    statusLog: [],
    invoicedAs: null,
    inputs: { weightG: 20 } as unknown as ServerQuote["inputs"],
    options: { laborPerUnit: false } as ServerQuote["options"],
    formState: { printerId: "p1" },
    quantity: 1,
    perUnitTotalCents: 500,
    batchTotalCents: 500,
    recommendedPriceCents: null,
    resolvedPriceCents: null,
    netProfitCents: null,
    achievedMarginPct: null,
    valid: true,
    partCount: 1,
    createdBy: "",
    updatedBy: "",
    ...overrides,
  };
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("listQuotes", () => {
  it("returns the quotes array on success", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ quotes: [serverQuote()] }));
    vi.stubGlobal("fetch", fetchMock);
    const result = await listQuotes();
    expect(result).toHaveLength(1);
    expect(result?.[0]?.id).toBe("q1");
    const [, options] = fetchMock.mock.calls[0]!;
    expect(options?.signal).toBeInstanceOf(AbortSignal);
  });

  it("returns [] when the body's quotes field isn't an array", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({})));
    expect(await listQuotes()).toEqual([]);
  });

  it("returns null on a non-2xx response", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({}, 500)));
    expect(await listQuotes()).toBeNull();
  });

  it("returns null on a network failure", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("network down")));
    expect(await listQuotes()).toBeNull();
  });

  it("sends no query params and returns a plain array when called without a page", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ quotes: [serverQuote()] }));
    vi.stubGlobal("fetch", fetchMock);
    const result = await listQuotes();
    expect(fetchMock.mock.calls[0]?.[0]).toBe("api/quotes");
    expect(Array.isArray(result)).toBe(true);
  });

  it("appends ?limit=&offset= and returns {items, total} from X-Total-Count when called with a page", async () => {
    const headers = new Headers({ "X-Total-Count": "37" });
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      headers,
      json: async () => ({ quotes: [serverQuote()] }),
    } as unknown as Response);
    vi.stubGlobal("fetch", fetchMock);
    const result = await listQuotes({ limit: 100, offset: 0 });
    expect(fetchMock.mock.calls[0]?.[0]).toBe("api/quotes?limit=100&offset=0");
    expect(result).toEqual({ items: [serverQuote()], total: 37 });
  });
});

describe("createQuote", () => {
  it("POSTs the input and returns the created ServerQuote", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(serverQuote({ id: "new" }), 201));
    vi.stubGlobal("fetch", fetchMock);

    const input = toQuoteInput(
      { printerId: "p1" },
      { weightG: 20 } as never,
      { laborPerUnit: false },
      { label: "Benchy", clientName: "", project: "", reference: "", status: "draft", statusLog: [] },
    );
    const result = await createQuote(input);
    expect(result.id).toBe("new");

    const [url, options] = fetchMock.mock.calls[0]!;
    expect(url).toBe("api/quotes");
    expect(options.method).toBe("POST");
    expect(options.headers).toEqual({ "Content-Type": "application/json" });
    expect(JSON.parse(options.body as string)).toEqual(input);
  });

  it("throws the joined server error on a non-2xx response", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ errors: ["label: max 500 characters"] }, 422)));
    const input = toQuoteInput({}, {} as never, {} as never, {
      label: "x",
      clientName: "",
      project: "",
      reference: "",
      status: "draft",
      statusLog: [],
    });
    await expect(createQuote(input)).rejects.toThrow("Label: max 500 characters.");
  });
});

describe("updateQuote", () => {
  it("PUTs to the id-scoped endpoint and returns the updated ServerQuote", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(serverQuote({ id: "q1", label: "Renamed" })));
    vi.stubGlobal("fetch", fetchMock);

    const input = toQuoteInput({}, {} as never, {} as never, {
      label: "Renamed",
      clientName: "",
      project: "",
      reference: "",
      status: "draft",
      statusLog: [],
    });
    const result = await updateQuote("q1", input);
    expect(result.label).toBe("Renamed");

    const [url, options] = fetchMock.mock.calls[0]!;
    expect(url).toBe("api/quotes/q1");
    expect(options.method).toBe("PUT");
  });

  it("encodes the id into the URL", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(serverQuote()));
    vi.stubGlobal("fetch", fetchMock);
    await updateQuote("weird/id with space", toQuoteInput({}, {} as never, {} as never, {
      label: "x",
      clientName: "",
      project: "",
      reference: "",
      status: "draft",
      statusLog: [],
    }));
    const [url] = fetchMock.mock.calls[0]!;
    expect(url).toBe(`api/quotes/${encodeURIComponent("weird/id with space")}`);
  });
});

describe("deleteQuote", () => {
  it("DELETEs the id-scoped endpoint", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({}, 204));
    vi.stubGlobal("fetch", fetchMock);
    await deleteQuote("q1");
    const [url, options] = fetchMock.mock.calls[0]!;
    expect(url).toBe("api/quotes/q1");
    expect(options.method).toBe("DELETE");
  });

  it("throws on a non-2xx response", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ errors: ["quote not found"] }, 404)));
    await expect(deleteQuote("missing")).rejects.toThrow("Quote not found.");
  });
});

describe("computeOrder", () => {
  it("POSTs inputs/options and returns {ok:true, order}", async () => {
    const order = { parts: [], batchTotal: 500, valid: true };
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(order));
    vi.stubGlobal("fetch", fetchMock);

    const result = await computeOrder({ parts: [{ weightG: 20 }] } as never, { laborPerUnit: false } as never);
    expect(result).toEqual({ ok: true, order });

    const [url, options] = fetchMock.mock.calls[0]!;
    expect(url).toBe("api/quote/compute");
    expect(JSON.parse(options.body as string)).toEqual({ inputs: { parts: [{ weightG: 20 }] }, options: { laborPerUnit: false } });
  });

  it("returns {ok:false} on a non-2xx response", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({}, 422)));
    expect(await computeOrder({} as never, {} as never)).toEqual({ ok: false });
  });

  it("returns {ok:false} on a network failure", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("network down")));
    expect(await computeOrder({} as never, {} as never)).toEqual({ ok: false });
  });
});

describe("toSavedQuote", () => {
  it("maps every field, using formState (not the engine inputs bag) as SavedQuote.inputs", () => {
    const q = serverQuote({
      id: "q1",
      savedAt: "2026-01-01T00:00:00.000Z",
      label: "Benchy",
      clientId: "c1",
      clientName: "Sam",
      project: "Widget",
      reference: "REF-1",
      status: "sent",
      statusLog: [{ status: "sent", at: "2026-01-01T00:00:00.000Z" }],
      invoicedAs: "inv-1",
      formState: { printerId: "p1", quantity: 3 },
      perUnitTotalCents: 1234,
      batchTotalCents: 3702,
      recommendedPriceCents: 2000,
      resolvedPriceCents: 1900,
    });
    const saved = toSavedQuote(q);
    expect(saved).toEqual({
      id: "q1",
      savedAt: "2026-01-01T00:00:00.000Z",
      label: "Benchy",
      clientId: "c1",
      clientName: "Sam",
      project: "Widget",
      reference: "REF-1",
      status: "sent",
      statusLog: [{ status: "sent", at: "2026-01-01T00:00:00.000Z" }],
      invoicedAs: "inv-1",
      perUnitTotal: 1234,
      batchTotal: 3702,
      recommendedPrice: 2000,
      resolvedPrice: 1900,
      inputs: { printerId: "p1", quantity: 3 },
      partCount: 1,
      createdBy: undefined,
      updatedBy: undefined,
    });
  });

  it("carries partCount", () => {
    const saved = toSavedQuote(serverQuote({ formState: { version: 1, parts: [{}, {}], order: {} }, partCount: 2 }));
    expect(saved.partCount).toBe(2);
  });

  it("maps null clientId/invoicedAs/prices to undefined, and blank project/reference to undefined", () => {
    const saved = toSavedQuote(serverQuote({ clientId: null, invoicedAs: null, project: "", reference: "" }));
    expect(saved.clientId).toBeUndefined();
    expect(saved.invoicedAs).toBeUndefined();
    expect(saved.project).toBeUndefined();
    expect(saved.reference).toBeUndefined();
    expect(saved.recommendedPrice).toBeUndefined();
    expect(saved.resolvedPrice).toBeUndefined();
  });

  it("round-trips createdBy/updatedBy", () => {
    const saved = toSavedQuote(serverQuote({ createdBy: "sam", updatedBy: "alex" }));
    expect(saved.createdBy).toBe("sam");
    expect(saved.updatedBy).toBe("alex");
  });

  it("maps blank createdBy/updatedBy to undefined", () => {
    const saved = toSavedQuote(serverQuote({ createdBy: "", updatedBy: "" }));
    expect(saved.createdBy).toBeUndefined();
    expect(saved.updatedBy).toBeUndefined();
  });
});

describe("toQuoteInput", () => {
  it("assembles a QuoteInput from formState, engine inputs, options, and meta", () => {
    const formState = { printerId: "p1" };
    const engineInputs = { weightG: 20 } as never;
    const options = { laborPerUnit: true } as never;
    const meta = {
      label: "Benchy",
      clientId: "c1",
      clientName: "Sam",
      project: "Widget",
      reference: "REF-1",
      status: "draft" as const,
      statusLog: [],
      invoicedAs: "inv-1",
    };
    expect(toQuoteInput(formState, engineInputs, options, meta)).toEqual({
      ...meta,
      inputs: engineInputs,
      options,
      formState,
    });
  });
});
