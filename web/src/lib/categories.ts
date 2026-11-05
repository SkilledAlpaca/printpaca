// Pure logic for the Shop items categories manager. The server owns the rules (server/categories.go);
// this mirrors only what the form needs to preview and to keep rows in order.

import type { Category } from "./categoriesApi";

export const CATEGORY_NAME_MAX = 60;
export const CATEGORY_SLUG_MAX = 40;
export const MAX_CATEGORIES = 50;

const SLUG_RE = /^[a-z0-9]+(-[a-z0-9]+)*$/;

/** Mirrors the server's slugify: lowercase ASCII letters and digits, every other run becomes one hyphen,
 * trimmed to the slug limit. "" when nothing usable is left (the server then falls back to "category"). */
export function slugify(name: string): string {
  let out = "";
  let pendingHyphen = false;
  for (const ch of name.toLowerCase()) {
    if (/^[a-z0-9]$/.test(ch)) {
      if (pendingHyphen && out.length > 0) out += "-";
      pendingHyphen = false;
      out += ch;
    } else {
      pendingHyphen = true;
    }
  }
  if (out.length > CATEGORY_SLUG_MAX) out = out.slice(0, CATEGORY_SLUG_MAX).replace(/-+$/, "");
  return out;
}

/** A message for an edited slug the server would reject, else "". An empty slug is fine (derived or kept). */
export function slugProblem(slug: string): string {
  const s = slug.trim();
  if (s === "") return "";
  if (s.length > CATEGORY_SLUG_MAX || !SLUG_RE.test(s)) {
    return `Use lowercase letters, numbers, and single hyphens, at most ${CATEGORY_SLUG_MAX} characters.`;
  }
  return "";
}

/** The sortOrder changes needed to move the category at `index` by `delta`: the list is renumbered 0..n-1 in its
 * new order and only rows whose value differs are returned, so a swap writes two rows and a tidy list writes
 * none beyond them. Empty when the move would leave the list. */
export function reorderUpdates(list: readonly Pick<Category, "id" | "sortOrder">[], index: number, delta: -1 | 1): { id: string; sortOrder: number }[] {
  const to = index + delta;
  if (index < 0 || index >= list.length || to < 0 || to >= list.length) return [];
  const next = list.slice();
  next.splice(to, 0, ...next.splice(index, 1));
  const updates: { id: string; sortOrder: number }[] = [];
  next.forEach((c, i) => {
    if (c.sortOrder !== i) updates.push({ id: c.id, sortOrder: i });
  });
  return updates;
}

/** sortOrder for a category added at the end. */
export function nextCategoryOrder(list: readonly Pick<Category, "sortOrder">[]): number {
  return list.reduce((max, c) => Math.max(max, c.sortOrder), -1) + 1;
}

/** What deleting `category` does to its items, for the confirmation. */
export function deleteConsequence(category: Pick<Category, "name" | "itemCount">): string {
  const n = category.itemCount;
  if (n === 0) return `${category.name} has no items. Deleting it is permanent.`;
  return `${n === 1 ? "1 item uses" : `${n} items use`} ${category.name}. ${n === 1 ? "It" : "They"} will have no category. Deleting it is permanent.`;
}
