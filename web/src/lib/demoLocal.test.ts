import { afterEach, describe, expect, it, vi } from "vitest";

// demoLocal has a one-shot init latch (initDemoLocal is idempotent), and importing it must touch
// nothing, so each test needs a fresh module: vi.resetModules() plus a dynamic re-import.

class FakeStorage {
  private store = new Map<string, string>();
  getItem(key: string): string | null {
    return this.store.has(key) ? this.store.get(key)! : null;
  }
  setItem(key: string, value: string): void {
    this.store.set(key, value);
  }
  removeItem(key: string): void {
    this.store.delete(key);
  }
}

function fakeDocumentElement() {
  const attrs = new Map<string, string>();
  return {
    setAttribute(name: string, value: string) {
      attrs.set(name, value);
    },
    removeAttribute(name: string) {
      attrs.delete(name);
    },
    getAttribute(name: string): string | null {
      return attrs.has(name) ? attrs.get(name)! : null;
    },
  };
}

async function freshModule() {
  vi.resetModules();
  return import("./demoLocal.svelte");
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("demoLocal", () => {
  it("is inert on import: no localStorage or DOM access until initDemoLocal runs", async () => {
    const storage = new FakeStorage();
    const getItemSpy = vi.spyOn(storage, "getItem");
    const setItemSpy = vi.spyOn(storage, "setItem");
    const docEl = fakeDocumentElement();
    const setAttrSpy = vi.spyOn(docEl, "setAttribute");
    vi.stubGlobal("localStorage", storage);
    vi.stubGlobal("document", { documentElement: docEl });

    const mod = await freshModule();

    expect(getItemSpy).not.toHaveBeenCalled();
    expect(setItemSpy).not.toHaveBeenCalled();
    expect(setAttrSpy).not.toHaveBeenCalled();
    // Reading the default state before init is fine; touching localStorage/the DOM is not.
    expect(mod.demoLocal.theme).toBe("system");
    expect(mod.demoLocal.currency).toBeNull();
    expect(mod.demoLocal.userMode).toBe("multi");
  });

  it("round-trips theme, currency, and user mode through localStorage", async () => {
    const storage = new FakeStorage();
    vi.stubGlobal("localStorage", storage);
    vi.stubGlobal("document", { documentElement: fakeDocumentElement() });
    const { initDemoLocal, setTheme, setCurrency, setUserMode } = await freshModule();

    initDemoLocal();
    setTheme("dark");
    setCurrency("eur");
    setUserMode("single");
    expect(storage.getItem("printpaca-demo-theme")).toBe("dark");
    expect(storage.getItem("printpaca-demo-currency")).toBe("eur");
    expect(storage.getItem("printpaca-demo-usermode")).toBe("single");

    // A fresh module instance (a reload) re-reads what was persisted.
    const reloaded = await freshModule();
    reloaded.initDemoLocal();
    expect(reloaded.demoLocal.theme).toBe("dark");
    expect(reloaded.demoLocal.currency).toBe("eur");
    expect(reloaded.demoLocal.userMode).toBe("single");
  });

  it("clearing the currency preset (back to demo default) removes the stored key", async () => {
    const storage = new FakeStorage();
    vi.stubGlobal("localStorage", storage);
    vi.stubGlobal("document", { documentElement: fakeDocumentElement() });
    const { initDemoLocal, setCurrency } = await freshModule();

    initDemoLocal();
    setCurrency("gbp");
    expect(storage.getItem("printpaca-demo-currency")).toBe("gbp");
    setCurrency(null);
    expect(storage.getItem("printpaca-demo-currency")).toBeNull();
  });

  it("applies data-theme for light/dark and removes it for system", async () => {
    const docEl = fakeDocumentElement();
    vi.stubGlobal("localStorage", new FakeStorage());
    vi.stubGlobal("document", { documentElement: docEl });
    const { initDemoLocal, setTheme } = await freshModule();

    initDemoLocal();
    expect(docEl.getAttribute("data-theme")).toBeNull();

    setTheme("dark");
    expect(docEl.getAttribute("data-theme")).toBe("dark");

    setTheme("light");
    expect(docEl.getAttribute("data-theme")).toBe("light");

    setTheme("system");
    expect(docEl.getAttribute("data-theme")).toBeNull();
  });

  it("treats a bogus stored theme/currency/userMode as the default, never throwing", async () => {
    const storage = new FakeStorage();
    storage.setItem("printpaca-demo-theme", "purple");
    storage.setItem("printpaca-demo-currency", "btc");
    storage.setItem("printpaca-demo-usermode", "team");
    vi.stubGlobal("localStorage", storage);
    vi.stubGlobal("document", { documentElement: fakeDocumentElement() });
    const { initDemoLocal, demoLocal } = await freshModule();

    expect(() => initDemoLocal()).not.toThrow();
    expect(demoLocal.theme).toBe("system");
    expect(demoLocal.currency).toBeNull();
    expect(demoLocal.userMode).toBe("multi");
  });

  it("initDemoLocal is idempotent: a second call does not re-read storage over an in-session change", async () => {
    const storage = new FakeStorage();
    vi.stubGlobal("localStorage", storage);
    vi.stubGlobal("document", { documentElement: fakeDocumentElement() });
    const { initDemoLocal, setTheme, setUserMode, demoLocal } = await freshModule();

    initDemoLocal();
    setTheme("dark");
    setUserMode("single");
    initDemoLocal(); // no-op: must not reset demoLocal.theme/userMode
    expect(demoLocal.theme).toBe("dark");
    expect(demoLocal.userMode).toBe("single");
  });

  it("never throws when localStorage itself throws (private-mode Safari)", async () => {
    const throwing = {
      getItem: () => {
        throw new Error("blocked");
      },
      setItem: () => {
        throw new Error("blocked");
      },
      removeItem: () => {
        throw new Error("blocked");
      },
    };
    vi.stubGlobal("localStorage", throwing);
    vi.stubGlobal("document", { documentElement: fakeDocumentElement() });
    const { initDemoLocal, setTheme, setCurrency, setUserMode, demoLocal } = await freshModule();

    expect(() => initDemoLocal()).not.toThrow();
    expect(() => setTheme("dark")).not.toThrow();
    expect(() => setCurrency("usd")).not.toThrow();
    expect(() => setUserMode("single")).not.toThrow();
    expect(demoLocal.theme).toBe("dark");
    expect(demoLocal.currency).toBe("usd");
    expect(demoLocal.userMode).toBe("single");
  });

  it("resolveCurrencyPreset maps a preset id to its symbol/decimals, and null to no override", async () => {
    vi.stubGlobal("localStorage", new FakeStorage());
    vi.stubGlobal("document", { documentElement: fakeDocumentElement() });
    const { resolveCurrencyPreset } = await freshModule();

    expect(resolveCurrencyPreset(null)).toBeNull();
    expect(resolveCurrencyPreset("usd")).toEqual({ symbol: "$", decimals: 2, code: "" });
    expect(resolveCurrencyPreset("eur")).toEqual({ symbol: "€", decimals: 2, code: "" });
    expect(resolveCurrencyPreset("gbp")).toEqual({ symbol: "£", decimals: 2, code: "" });
    expect(resolveCurrencyPreset("jpy")).toEqual({ symbol: "¥", decimals: 0, code: "" });
  });
});
