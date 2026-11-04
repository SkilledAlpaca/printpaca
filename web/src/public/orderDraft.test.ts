import { describe, it, expect, beforeEach } from "vitest";
import { clearDraft, loadDraft, refusedNotice, saveDraft } from "./orderDraft";

const store = new Map<string, string>();
beforeEach(() => {
  store.clear();
  Object.defineProperty(globalThis, "sessionStorage", {
    configurable: true,
    value: { getItem: (k: string) => store.get(k) ?? null, setItem: (k: string, v: string) => void store.set(k, v), removeItem: (k: string) => void store.delete(k) },
  });
});

describe("orderDraft", () => {
  it("round-trips an order draft and clears it", () => {
    saveDraft({ kind: "order", itemId: "i1", selected: { g: "o" }, quantity: 2, note: "hi", name: "A", email: "a@b.co" });
    expect(loadDraft()).toMatchObject({ kind: "order", itemId: "i1", quantity: 2, note: "hi" });
    clearDraft();
    expect(loadDraft()).toBeNull();
  });
  it("ignores a draft of an unknown shape", () => {
    store.set("printpaca:public-confirmation:draft", JSON.stringify({ kind: "other" }));
    expect(loadDraft()).toBeNull();
  });
  it("says the order wasn't placed", () => {
    expect(refusedNotice("order")).toContain("Your order wasn't placed. The shop closed while you were ordering.");
  });
});
