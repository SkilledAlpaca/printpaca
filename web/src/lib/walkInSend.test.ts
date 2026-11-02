import { describe, expect, it, vi } from "vitest";
import type { SavedQuote } from "./api";
import type { Client } from "./clientsApi";
import { sendWithEnteredEmail, type CreatedClients, type WalkInSendDeps } from "./walkInSend";

function quote(o: Partial<SavedQuote> = {}): SavedQuote {
  return { id: "q1", savedAt: "", label: "L", perUnitTotal: 1, batchTotal: 1, resolvedPrice: 100, clientName: "Sam", inputs: {}, status: "draft", statusLog: [], partCount: 1, ...o };
}
function client(o: Partial<Client> = {}): Client {
  return { id: "c1", name: "Sam", email: "", phone: "", billingAddress: "", shippingAddress: "", notes: "", createdAt: "", updatedAt: "", createdBy: "", updatedBy: "", ...o };
}

function setup(over: Partial<WalkInSendDeps<string>> = {}) {
  const calls: string[] = [];
  let n = 0;
  const deps: WalkInSendDeps<string> = {
    createClient: vi.fn(async (i) => { calls.push("create"); return client({ id: `new${++n}`, name: i.name, email: i.email }); }),
    updateClient: vi.fn(async (id, i) => { calls.push("update"); return client({ id, name: i.name, email: i.email }); }),
    linkQuote: vi.fn(async () => { calls.push("link"); }),
    send: vi.fn(async () => { calls.push("send"); return "sent"; }),
    onClients: vi.fn(),
    ...over,
  };
  return { deps, calls, created: new Map() as CreatedClients };
}

describe("sendWithEnteredEmail", () => {
  it("no client: creates, links, then sends, in that order", async () => {
    const { deps, calls, created } = setup();
    await expect(sendWithEnteredEmail({ quote: quote(), email: "a@b.co", clients: [], created, deps })).resolves.toBe("sent");
    expect(calls).toEqual(["create", "link", "send"]);
    expect(deps.createClient).toHaveBeenCalledWith(expect.objectContaining({ name: "Sam", email: "a@b.co" }));
  });
  it("names the client Walk-in when the quote has no name", async () => {
    const { deps, created } = setup();
    await sendWithEnteredEmail({ quote: quote({ clientName: " " }), email: "a@b.co", clients: [], created, deps });
    expect(deps.createClient).toHaveBeenCalledWith(expect.objectContaining({ name: "Walk-in" }));
  });
  it("an unnamed quote never adopts an existing Walk-in placeholder client", async () => {
    const { deps, calls, created } = setup();
    const placeholder = client({ id: "w1", name: "Walk-in" });
    await sendWithEnteredEmail({ quote: quote({ clientName: "" }), email: "a@b.co", clients: [placeholder], created, deps });
    expect(calls).toEqual(["create", "link", "send"]);
    expect(deps.updateClient).not.toHaveBeenCalled();
  });
  it("linked client without email: updates it, no create or relink", async () => {
    const { deps, calls, created } = setup();
    await sendWithEnteredEmail({ quote: quote({ clientId: "c1" }), email: "a@b.co", clients: [client()], created, deps });
    expect(calls).toEqual(["update", "send"]);
  });
  it("reuses a same-name client with no email (case-insensitive) instead of creating", async () => {
    const { deps, calls, created } = setup();
    await sendWithEnteredEmail({ quote: quote({ clientName: "sam " }), email: "a@b.co", clients: [client()], created, deps });
    expect(calls).toEqual(["update", "link", "send"]);
  });
  it("reuses a same-name client already holding that email", async () => {
    const { deps, calls, created } = setup();
    await sendWithEnteredEmail({ quote: quote(), email: "A@b.co", clients: [client({ email: "a@b.co" })], created, deps });
    expect(calls).toEqual(["link", "send"]);
  });
  it("creates a new client when the same-name client has a different email", async () => {
    const { deps, calls, created } = setup();
    await sendWithEnteredEmail({ quote: quote(), email: "a@b.co", clients: [client({ email: "x@y.co" })], created, deps });
    expect(calls).toEqual(["create", "link", "send"]);
  });
  it("link failure then retry: no second create", async () => {
    let fail = true;
    const { deps, calls, created } = setup({
      linkQuote: vi.fn(async () => { calls.push("link"); if (fail) throw new Error("boom"); }),
    });
    const clients: import("./clientsApi").Client[] = [];
    (deps.onClients as ReturnType<typeof vi.fn>).mockImplementation((l) => { clients.splice(0, clients.length, ...l); });
    await expect(sendWithEnteredEmail({ quote: quote(), email: "a@b.co", clients: [], created, deps })).rejects.toThrow("boom");
    fail = false;
    await sendWithEnteredEmail({ quote: quote(), email: "a@b.co", clients: [...clients], created, deps });
    expect(calls.filter((c) => c === "create")).toHaveLength(1);
    expect(calls).toEqual(["create", "link", "link", "send"]);
  });
  it("send failure after link leaves the link in place and surfaces the outcome", async () => {
    const { deps, calls, created } = setup({ send: vi.fn(async () => { calls.push("send"); return "failed"; }) });
    await expect(sendWithEnteredEmail({ quote: quote(), email: "a@b.co", clients: [], created, deps })).resolves.toBe("failed");
    expect(calls).toEqual(["create", "link", "send"]);
  });
  it("throws when the linked client is missing from the list", async () => {
    const { deps, created } = setup();
    await expect(sendWithEnteredEmail({ quote: quote({ clientId: "gone" }), email: "a@b.co", clients: [], created, deps })).rejects.toThrow(/Couldn't find/);
  });
});
