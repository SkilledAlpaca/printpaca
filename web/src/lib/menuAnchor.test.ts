import { describe, expect, it } from "vitest";
import { computeMenuAnchor, type MenuAnchorRect } from "./menuAnchor";

// Matches QuotesTab's MENU_ESTIMATED_HEIGHT/WIDTH and MENU_VIEWPORT_MARGIN thresholds.
const sizing = { estimatedHeight: 260, estimatedWidth: 200, margin: 8 };

function rect(overrides: Partial<MenuAnchorRect>): MenuAnchorRect {
  return { top: 100, right: 300, bottom: 120, ...overrides };
}

describe("computeMenuAnchor", () => {
  it("opens downward, anchored 4px below rect.bottom, when it fits", () => {
    const viewport = { width: 1000, height: 800 };
    const anchor = computeMenuAnchor(rect({ top: 100, bottom: 120, right: 300 }), viewport, sizing);
    expect(anchor).toEqual({ top: 124, right: 700 });
  });

  it("flips upward when rect.bottom + estimated height would overflow the viewport", () => {
    const viewport = { width: 1000, height: 400 };
    const anchor = computeMenuAnchor(rect({ top: 330, bottom: 350, right: 300 }), viewport, sizing);
    // bottom = 400 - 330 + 4 = 74, clamped to [8, 132].
    expect(anchor).toEqual({ bottom: 74, right: 700 });
  });

  it("uses > (not >=) for the flip decision: an exact fit still opens downward", () => {
    const viewport = { width: 1000, height: 800 };
    // rect.bottom + estimatedHeight equals viewportHeight exactly (not >), so it opens down;
    // raw top 544 clamps to maxTop 532.
    const anchor = computeMenuAnchor(rect({ top: 500, bottom: 540, right: 300 }), viewport, sizing);
    expect(anchor).toEqual({ top: 532, right: 700 });
  });

  it("clamps the right offset in from the right edge when the toggle is near it", () => {
    const viewport = { width: 1000, height: 800 };
    // Raw right offset 5 clamps up to the margin (8).
    const anchor = computeMenuAnchor(rect({ top: 100, bottom: 120, right: 995 }), viewport, sizing);
    expect(anchor.right).toBe(8);
  });

  it("clamps the right offset to maxRight when the toggle is near the left edge", () => {
    const viewport = { width: 1000, height: 800 };
    // Raw right offset 990 clamps down to maxRight 792.
    const anchor = computeMenuAnchor(rect({ top: 100, bottom: 120, right: 10 }), viewport, sizing);
    expect(anchor.right).toBe(792);
  });

  it("clamps top to the bottom viewport margin when opening down near the bottom of a tall viewport", () => {
    const viewport = { width: 1000, height: 300 };
    // Use a rect that still opens down but whose raw top would overflow.
    const anchor = computeMenuAnchor(rect({ top: 30, bottom: 35, right: 300 }), viewport, sizing);
    // 35 + 260 = 295, not > 300: opens down; raw top 39 clamps to maxTop 32.
    expect(anchor).toEqual({ top: 32, right: 700 });
  });

  it("clamps bottom to the top viewport margin when flipped up near the top of a short viewport", () => {
    const viewport = { width: 1000, height: 300 };
    const anchor = computeMenuAnchor(rect({ top: 5, bottom: 280, right: 300 }), viewport, sizing);
    // 280 + 260 > 300 flips up; raw bottom 299 clamps down to maxBottom 32.
    expect(anchor).toEqual({ bottom: 32, right: 700 });
  });

  it("floors right and top/bottom at the viewport margin even in a tiny viewport", () => {
    const viewport = { width: 50, height: 50 };
    const anchor = computeMenuAnchor(rect({ top: 0, bottom: 0, right: 0 }), viewport, sizing);
    // maxRight = max(8, -158) = 8 and maxBottom = max(8, -218) = 8, so both raw offsets clamp down to 8.
    expect(anchor).toEqual({ bottom: 8, right: 8 });
  });
});
