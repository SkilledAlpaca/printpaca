import { afterEach, describe, expect, it, vi } from "vitest";
import { deleteHeroImage, uploadHeroImage } from "./shopHeroApi";

function mockFetch(status: number, body: unknown) {
  const fn = vi.fn(async () => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } }));
  vi.stubGlobal("fetch", fn);
  return fn;
}

afterEach(() => vi.unstubAllGlobals());

describe("uploadHeroImage", () => {
  it("posts the raw image with the alt text in the query", async () => {
    const fn = mockFetch(200, { file: "a.png", url: "/item-images/a.png", alt: "Shelves & prints" });
    const blob = new Blob(["x"], { type: "image/png" });
    const out = await uploadHeroImage(blob, "Shelves & prints");
    expect(out.file).toBe("a.png");
    const [url, init] = fn.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("api/shop/hero-image?alt=Shelves%20%26%20prints");
    expect(init.method).toBe("POST");
    expect((init.headers as Record<string, string>)["Content-Type"]).toBe("image/png");
    expect(init.body).toBe(blob);
  });

  it("sends a typeless file as image/jpeg", async () => {
    const fn = mockFetch(200, { file: "a.jpg", url: "/item-images/a.jpg", alt: "x" });
    await uploadHeroImage(new Blob(["x"]), "x");
    const init = (fn.mock.calls[0] as unknown as [string, RequestInit])[1];
    expect((init.headers as Record<string, string>)["Content-Type"]).toBe("image/jpeg");
  });

  it("rejects with the server's message", async () => {
    mockFetch(422, { errors: ["shop.heroImage.alt: required; describe the image for people who can't see it"] });
    await expect(uploadHeroImage(new Blob(["x"], { type: "image/png" }), "")).rejects.toThrow(/alt/);
  });
});

describe("deleteHeroImage", () => {
  it("sends DELETE and returns the empty image", async () => {
    const fn = mockFetch(200, { file: "", url: "", alt: "" });
    expect((await deleteHeroImage()).file).toBe("");
    expect((fn.mock.calls[0] as unknown as [string, RequestInit])[1].method).toBe("DELETE");
  });
});
