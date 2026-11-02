import { afterEach, describe, expect, it, vi } from "vitest";
import { authRequired, loadConfig, localId, type SavedQuote } from "./api";

// loadConfig()'s authRequired: a 401 sets it, anything else clears it. It's a plain module export, not a
// rune, so tests read it as an ordinary binding after each await.
describe("loadConfig authRequired signal", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("a 401 sets authRequired and returns null", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({ ok: false, status: 401, json: async () => ({}) } as unknown as Response),
    );
    const result = await loadConfig();
    expect(result).toBeNull();
    expect(authRequired).toBe(true);
  });

  it("a normal 200 clears authRequired", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => ({ currency: {} }) } as unknown as Response),
    );
    const result = await loadConfig();
    expect(result).not.toBeNull();
    expect(authRequired).toBe(false);
  });

  it("a network failure clears authRequired (distinct from a 401)", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({ ok: false, status: 401, json: async () => ({}) } as unknown as Response),
    );
    await loadConfig(); // leaves authRequired = true
    vi.stubGlobal(
      "fetch",
      vi.fn().mockRejectedValue(new Error("network down")),
    );
    const result = await loadConfig();
    expect(result).toBeNull();
    expect(authRequired).toBe(false);
  });

  it("a non-401 failure (e.g. 500) clears authRequired", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({ ok: false, status: 500, json: async () => ({}) } as unknown as Response),
    );
    const result = await loadConfig();
    expect(result).toBeNull();
    expect(authRequired).toBe(false);
  });
});

function quote(overrides: Partial<SavedQuote>): SavedQuote {
  return {
    id: "q1",
    savedAt: "2026-01-01T00:00:00.000Z",
    label: "quote",
    perUnitTotal: 100,
    batchTotal: 100,
    inputs: {},
    status: "draft",
    statusLog: [],
    partCount: 1,
    ...overrides,
  };
}

describe("localId", () => {
  it("returns the platform UUID when crypto.randomUUID is available", () => {
    expect(localId()).toMatch(/^[0-9a-f-]{36}$/i);
  });

  it("falls back to a non-empty id when crypto.randomUUID is unavailable", () => {
    const original = crypto.randomUUID;
    // @ts-expect-error simulating a plain-HTTP (non-secure) context
    crypto.randomUUID = undefined;
    try {
      const id = localId();
      expect(id.length).toBeGreaterThan(0);
      expect(id).not.toBe("undefined");
    } finally {
      crypto.randomUUID = original;
    }
  });

  it("fallback ids are unique-ish across 1000 calls", () => {
    const original = crypto.randomUUID;
    // @ts-expect-error simulating a plain-HTTP (non-secure) context
    crypto.randomUUID = undefined;
    try {
      const ids = new Set<string>();
      for (let i = 0; i < 1000; i++) ids.add(localId());
      expect(ids.size).toBe(1000);
    } finally {
      crypto.randomUUID = original;
    }
  });
});
