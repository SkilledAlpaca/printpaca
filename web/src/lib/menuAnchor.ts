/** Pure viewport-positioning math behind QuotesTab's row overflow menu: the right-edge clamp and the
 * flip-up decision, taking plain numbers and never touching the DOM. */

export interface MenuAnchorRect {
  top: number;
  right: number;
  bottom: number;
}
export interface MenuAnchorViewport {
  width: number;
  height: number;
}
export interface MenuAnchorSizing {
  estimatedHeight: number;
  estimatedWidth: number;
  margin: number;
}
// Exactly one of top (opens downward) or bottom (flipped upward near the viewport bottom) is set; QuotesTab's
// template matches on it.
export interface MenuAnchor {
  top?: number;
  bottom?: number;
  right: number;
}

/** right is clamped to stay at least `margin` from either viewport edge. Flips upward (anchored from the
 * viewport bottom) when rect.bottom plus the estimated panel height would overflow; otherwise anchors 4px
 * below rect.bottom. top and bottom are also clamped to `margin` inside their edges. */
export function computeMenuAnchor(rect: MenuAnchorRect, viewport: MenuAnchorViewport, sizing: MenuAnchorSizing): MenuAnchor {
  const { width: viewportWidth, height: viewportHeight } = viewport;
  const { estimatedHeight, estimatedWidth, margin } = sizing;

  const maxRight = Math.max(margin, viewportWidth - estimatedWidth - margin);
  const right = Math.min(Math.max(margin, viewportWidth - rect.right), maxRight);

  if (rect.bottom + estimatedHeight > viewportHeight) {
    const maxBottom = Math.max(margin, viewportHeight - estimatedHeight - margin);
    const bottom = Math.min(Math.max(margin, viewportHeight - rect.top + 4), maxBottom);
    return { bottom, right };
  }
  const maxTop = Math.max(margin, viewportHeight - estimatedHeight - margin);
  return { top: Math.min(rect.bottom + 4, maxTop), right };
}
