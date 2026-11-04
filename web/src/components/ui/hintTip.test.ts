import { describe, expect, it } from "vitest";
import { placeTip, tipOpen, TIP_MARGIN } from "./hintTip";

const viewport = { width: 1000, height: 700 };
const anchor = (left: number, top: number) => ({ left, top, right: left + 24, bottom: top + 24 });

describe("placeTip", () => {
  it("centers below the anchor when there is room", () => {
    const p = placeTip(anchor(500, 100), { width: 200, height: 60 }, viewport);
    expect(p).toEqual({ left: 412, top: 124, placement: "below" });
  });
  it("stays inside the left and right edges", () => {
    expect(placeTip(anchor(2, 100), { width: 300, height: 60 }, viewport).left).toBe(TIP_MARGIN);
    expect(placeTip(anchor(990, 100), { width: 300, height: 60 }, viewport).left).toBe(viewport.width - 300 - TIP_MARGIN);
  });
  it("pins to the left margin when wider than the viewport", () => {
    expect(placeTip(anchor(100, 100), { width: 1200, height: 60 }, viewport).left).toBe(TIP_MARGIN);
  });
  it("flips above when below would cross the bottom edge and there is room above", () => {
    const p = placeTip(anchor(500, 660), { width: 200, height: 80 }, viewport);
    expect(p.placement).toBe("above");
    expect(p.top).toBe(660 - 80);
  });
  it("stays below when there is no room above either", () => {
    expect(placeTip(anchor(500, 20), { width: 200, height: 800 }, viewport).placement).toBe("below");
  });
});

describe("tipOpen", () => {
  const idle = { hover: false, focus: false, pinned: false, dismissed: false };
  it("is closed with no trigger", () => {
    expect(tipOpen(idle)).toBe(false);
  });
  it("opens on hover, focus, or a pin", () => {
    expect(tipOpen({ ...idle, hover: true })).toBe(true);
    expect(tipOpen({ ...idle, focus: true })).toBe(true);
    expect(tipOpen({ ...idle, pinned: true })).toBe(true);
  });
  it("is closed after a dismissal, even while still hovered", () => {
    expect(tipOpen({ hover: true, focus: true, pinned: true, dismissed: true })).toBe(false);
  });
});
