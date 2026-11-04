import { describe, it, expect, vi } from "vitest";
import { createTapGuard } from "./confirmationFocus";

describe("createTapGuard", () => {
  it("ignores an action right after arming and runs it after the window", () => {
    vi.useFakeTimers();
    const now = vi.spyOn(performance, "now");
    now.mockReturnValue(1000);
    const guard = createTapGuard(800);
    const fn = vi.fn();
    const act = guard.guarded(fn);
    act();
    expect(fn).toHaveBeenCalledTimes(1); // never armed: runs
    guard.arm();
    now.mockReturnValue(1500);
    act();
    expect(fn).toHaveBeenCalledTimes(1);
    now.mockReturnValue(1801);
    act();
    expect(fn).toHaveBeenCalledTimes(2);
    now.mockRestore();
    vi.useRealTimers();
  });
});
