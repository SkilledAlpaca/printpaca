// Pure helpers for ui/HintTip: where the tooltip sits, and whether it is open.

export interface Box {
  left: number;
  top: number;
  right: number;
  bottom: number;
}

export interface TipPlacement {
  left: number;
  top: number;
  placement: "below" | "above";
}

/** Viewport edge the tooltip keeps clear of. */
export const TIP_MARGIN = 8;

/** Fixed-position coordinates for a tooltip of `tip` size next to `anchor`, kept inside the viewport.
 * It is centered on the anchor, then shifted to stay within TIP_MARGIN of the left and right edges (and pinned to
 * the left margin when it is wider than the viewport). It opens below the anchor unless that would cross the
 * bottom edge and there is room above. The tooltip's own padding is the gap to the anchor, so the pointer can
 * travel from the button into the tooltip without leaving either (WCAG 1.4.13 hoverable). */
export function placeTip(anchor: Box, tip: { width: number; height: number }, viewport: { width: number; height: number }): TipPlacement {
  const centered = (anchor.left + anchor.right) / 2 - tip.width / 2;
  const maxLeft = viewport.width - tip.width - TIP_MARGIN;
  const left = Math.max(TIP_MARGIN, Math.min(centered, maxLeft));
  const fitsBelow = anchor.bottom + tip.height <= viewport.height - TIP_MARGIN;
  const fitsAbove = anchor.top - tip.height >= TIP_MARGIN;
  if (!fitsBelow && fitsAbove) return { left, top: anchor.top - tip.height, placement: "above" };
  return { left, top: anchor.bottom, placement: "below" };
}

export interface TipState {
  hover: boolean;
  focus: boolean;
  pinned: boolean;
  /** Set by Escape or by a second click; cleared when the pointer leaves or focus moves away. */
  dismissed: boolean;
}

/** The tooltip is open while the pointer is on it, keyboard focus is on its button, or a click pinned it, unless
 * it was dismissed. */
export function tipOpen(s: TipState): boolean {
  return !s.dismissed && (s.hover || s.focus || s.pinned);
}

/** Admin forms show a field's hint in a tooltip; the customer shop keeps it visible. */
export type HintMode = "tooltip" | "visible";
