import { describe, expect, it, vi } from "vitest";
import { pageSave, registerPageSave, runPageSave } from "./pageShortcuts.svelte";

describe("pageShortcuts", () => {
  it("runs the newest registration and falls back after it unregisters", () => {
    const a = vi.fn();
    const b = vi.fn();
    expect(runPageSave()).toBe(false);
    const offA = registerPageSave(a);
    const offB = registerPageSave(b);
    expect(pageSave.active).toBe(true);
    expect(runPageSave()).toBe(true);
    expect(b).toHaveBeenCalledTimes(1);
    expect(a).not.toHaveBeenCalled();
    offB();
    runPageSave();
    expect(a).toHaveBeenCalledTimes(1);
    offA();
    expect(pageSave.active).toBe(false);
    expect(runPageSave()).toBe(false);
  });

  it("unregistering twice is harmless", () => {
    const off = registerPageSave(() => {});
    off();
    off();
    expect(pageSave.active).toBe(false);
  });
});
