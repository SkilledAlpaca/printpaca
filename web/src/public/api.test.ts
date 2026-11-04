import { afterEach, describe, expect, it, vi } from "vitest";
import { DEFAULT_PUBLIC_SHOP, fetchQuotePreview, loadPublicCatalog, loadPublicItems, shopOf, submitItemOrder, submitQuoteRequest } from "./api";

const PREVIEW_INPUT = { material: "PLA", quantity: 1, weightG: 50, timeSeconds: 3600 };

// submitQuoteRequest never throws: every case resolves, so tests await it directly.

describe("submitQuoteRequest", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("a normal success passes id/priceCents/fileStored through", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: true,
        status: 201,
        json: async () => ({ id: "r1", priceCents: 1234, fileStored: true }),
      } as unknown as Response),
    );
    const result = await submitQuoteRequest(new FormData());
    expect(result.ok).toBe(true);
    expect(result.id).toBe("r1");
    expect(result.priceCents).toBe(1234);
    expect(result.fileStored).toBe(true);
    // A server without the short reference falls back to the id.
    expect(result.reference).toBe("r1");
    expect(result.needsPrintTime).toBe(false);
  });

  it("passes the short reference and the needs-print-time flag through", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: true,
        status: 201,
        json: async () => ({ id: "r1", reference: "HL-1001", priceCents: 0, needsPrintTime: true, fileStored: true }),
      } as unknown as Response),
    );
    const result = await submitQuoteRequest(new FormData());
    expect(result.reference).toBe("HL-1001");
    expect(result.needsPrintTime).toBe(true);
    expect(result.priceCents).toBe(0);
  });

  it("a 413 (http.MaxBytesReader tripping server-side) maps to actionable copy, not the generic fallback", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: false,
        status: 413,
        json: async () => {
          throw new Error("not json"); // Go's plain-text error page, not this app's {errors} shape
        },
      } as unknown as Response),
    );
    const result = await submitQuoteRequest(new FormData());
    expect(result.ok).toBe(false);
    expect(result.status).toBe(413);
    expect(result.errors).toHaveLength(1);
    expect(result.errors[0]).toContain("Remove it");
    expect(result.errors[0]).not.toBe("Something went wrong submitting the request.");
  });

  it("a non-413 failure still falls back to the generic message when the body has no errors", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: false,
        status: 422,
        json: async () => ({}),
      } as unknown as Response),
    );
    const result = await submitQuoteRequest(new FormData());
    expect(result.ok).toBe(false);
    expect(result.errors).toEqual(["Something went wrong submitting the request."]);
  });

  it("a network failure resolves to ok:false rather than rejecting", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("offline")));
    const result = await submitQuoteRequest(new FormData());
    expect(result.ok).toBe(false);
    expect(result.status).toBe(0);
  });
});

describe("fetchQuotePreview", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("a normal success passes priceCents/shippingCents through", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: async () => ({ priceCents: 1234, shippingCents: 500, currency: "usd" }),
      } as unknown as Response),
    );
    const result = await fetchQuotePreview(PREVIEW_INPUT);
    expect(result.ok).toBe(true);
    expect(result.priceCents).toBe(1234);
    expect(result.shippingCents).toBe(500);
  });

  it("a 200 body missing priceCents/shippingCents is treated as a failure, not a crash", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => ({}) } as unknown as Response),
    );
    const result = await fetchQuotePreview(PREVIEW_INPUT);
    expect(result.ok).toBe(false);
    expect(result.errors).toHaveLength(1);
  });

  it("a 429 maps to its own copy naming the preview endpoint's own rate bucket", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({ ok: false, status: 429, json: async () => ({}) } as unknown as Response),
    );
    const result = await fetchQuotePreview(PREVIEW_INPUT);
    expect(result.ok).toBe(false);
    expect(result.status).toBe(429);
    expect(result.errors[0]).toContain("Wait a moment");
  });

  it("a validation failure surfaces the server's own errors array", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: false,
        status: 422,
        json: async () => ({ errors: ["Quantity must be a whole number from 1 to 1000."] }),
      } as unknown as Response),
    );
    const result = await fetchQuotePreview(PREVIEW_INPUT);
    expect(result.ok).toBe(false);
    expect(result.errors).toEqual(["Quantity must be a whole number from 1 to 1000."]);
  });

  it("a non-JSON failure body falls back to the generic message", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: false,
        status: 500,
        json: async () => {
          throw new Error("not json");
        },
      } as unknown as Response),
    );
    const result = await fetchQuotePreview(PREVIEW_INPUT);
    expect(result.ok).toBe(false);
    expect(result.errors).toEqual(["Couldn't estimate this request."]);
  });

  it("a network failure resolves to ok:false rather than rejecting", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("offline")));
    const result = await fetchQuotePreview(PREVIEW_INPUT);
    expect(result.ok).toBe(false);
    expect(result.status).toBe(0);
  });
});

describe("storefront settings", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  const stubFetch = (status: number, body: unknown) =>
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: status < 400, status, json: async () => body } as unknown as Response));

  it("shopOf falls back to v1.1 behavior for a server without the shop block, and merges a partial one", () => {
    expect(shopOf(null)).toEqual(DEFAULT_PUBLIC_SHOP);
    expect(shopOf({} as never)).toEqual(DEFAULT_PUBLIC_SHOP);
    expect(shopOf({ shop: { acceptingOrders: false, pausedMessage: "Back soon" } } as never)).toMatchObject({
      customEstimates: true,
      gallery: true,
      acceptingOrders: false,
      pausedMessage: "Back soon",
    });
  });

  it("loadPublicCatalog returns items and categories; loadPublicItems keeps returning items alone", async () => {
    stubFetch(200, { items: [{ id: "a" }], categories: [{ slug: "toys", name: "Toys" }] });
    expect(await loadPublicCatalog()).toEqual({ items: [{ id: "a" }], categories: [{ slug: "toys", name: "Toys" }] });
    expect(await loadPublicItems()).toEqual([{ id: "a" }]);
    stubFetch(200, { items: [{ id: "a" }] });
    expect((await loadPublicCatalog())?.categories).toEqual([]);
    stubFetch(200, {});
    expect(await loadPublicItems()).toEqual([]);
    stubFetch(500, {});
    expect(await loadPublicCatalog()).toBeNull();
  });

  const paused = { errors: ["Closed: back Jan 5"], paused: true, pausedMessage: "Closed: back Jan 5" };

  it("a paused refusal carries the owner's message through unchanged, colon and all", async () => {
    stubFetch(503, paused);
    const order = await submitItemOrder({ itemId: "i", selections: [], quantity: 1, name: "n", email: "e@example.com", note: "" });
    expect(order).toMatchObject({ ok: false, status: 503, errors: ["Closed: back Jan 5"], paused: "Closed: back Jan 5" });
    const req = await submitQuoteRequest(new FormData());
    expect(req).toMatchObject({ ok: false, status: 503, errors: ["Closed: back Jan 5"], paused: "Closed: back Jan 5" });
    const preview = await fetchQuotePreview(PREVIEW_INPUT);
    expect(preview).toMatchObject({ ok: false, status: 503, errors: ["Closed: back Jan 5"], paused: "Closed: back Jan 5" });
  });

  it("other refusals still go through the field-error humanizer", async () => {
    stubFetch(422, { errors: ["weightG: must be 0-100000"] });
    const preview = await fetchQuotePreview(PREVIEW_INPUT);
    expect(preview.paused).toBeUndefined();
    expect(preview.errors[0]).toContain("Model weight");
  });
});
