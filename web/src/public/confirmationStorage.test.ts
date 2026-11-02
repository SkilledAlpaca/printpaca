import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { clearConfirmation, loadConfirmation, saveConfirmation } from "./confirmationStorage";

// Minimal in-memory Storage stand-in: vitest's node environment has no sessionStorage global.
class FakeStorage {
  private map = new Map<string, string>();
  getItem(key: string): string | null {
    return this.map.has(key) ? this.map.get(key)! : null;
  }
  setItem(key: string, value: string): void {
    this.map.set(key, value);
  }
  removeItem(key: string): void {
    this.map.delete(key);
  }
}

describe("confirmationStorage", () => {
  beforeEach(() => {
    vi.stubGlobal("sessionStorage", new FakeStorage());
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("round-trips a saved value", () => {
    saveConfirmation("quote", { id: "abc", priceCents: 1234 });
    expect(loadConfirmation<{ id: string; priceCents: number }>("quote")).toEqual({ id: "abc", priceCents: 1234 });
  });

  it("returns null for a key that was never saved", () => {
    expect(loadConfirmation("item-order")).toBeNull();
  });

  it("keeps independent keys from colliding", () => {
    saveConfirmation("quote", { a: 1 });
    saveConfirmation("item-order", { b: 2 });
    expect(loadConfirmation("quote")).toEqual({ a: 1 });
    expect(loadConfirmation("item-order")).toEqual({ b: 2 });
  });

  it("clearConfirmation removes only its own key", () => {
    saveConfirmation("quote", { a: 1 });
    saveConfirmation("item-order", { b: 2 });
    clearConfirmation("quote");
    expect(loadConfirmation("quote")).toBeNull();
    expect(loadConfirmation("item-order")).toEqual({ b: 2 });
  });

  it("never throws when sessionStorage is unavailable", () => {
    vi.stubGlobal("sessionStorage", undefined);
    expect(() => saveConfirmation("quote", { a: 1 })).not.toThrow();
    expect(loadConfirmation("quote")).toBeNull();
    expect(() => clearConfirmation("quote")).not.toThrow();
  });
});
