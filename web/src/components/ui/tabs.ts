// Pure helpers for ui/Tabs: which tab a key moves to, and the ids a tab and its panel share.

export type TabKey = "ArrowLeft" | "ArrowRight" | "Home" | "End";

/** The index a navigation key moves to in a horizontal tablist of `count` tabs, or null for any other key.
 * Arrow keys wrap around, as the ARIA authoring practices describe; Home and End jump to the ends. */
export function tabTarget(key: string, index: number, count: number): number | null {
  if (count <= 0) return null;
  switch (key) {
    case "ArrowRight":
      return (index + 1) % count;
    case "ArrowLeft":
      return (index - 1 + count) % count;
    case "Home":
      return 0;
    case "End":
      return count - 1;
    default:
      return null;
  }
}

/** The element ids a tablist derives from its prefix, so the panel can name the selected tab and the tab can
 * point at the panel. */
export function tabIds(prefix: string, tabId: string): { tab: string; panel: string } {
  return { tab: `${prefix}-tab-${tabId}`, panel: `${prefix}-panel` };
}
