import type { Item } from "./itemsApi";

/** Items whose name or description contains every whitespace-separated word of `query` (case-insensitive).
 * An empty query returns all items in their given order. */
export function filterItems(items: Item[], query: string): Item[] {
  const words = query.trim().toLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) return items;
  return items.filter((it) => {
    const hay = `${it.name} ${it.description}`.toLowerCase();
    return words.every((w) => hay.includes(w));
  });
}
