/** Pure nav-filtering logic pulled out of App.svelte for testing: which destinations ui.disabledTabs hides,
 * whether a section header survives, and where routing falls back. Config-driven decluttering only, never
 * access control; a hidden tab's API routes stay reachable. */

export interface NavSection<T extends string> {
  label: string | null;
  items: T[];
}

/** Drops disabled ids from every section; a section left with no items is dropped, header included. */
export function filterNavSections<T extends string>(
  sections: readonly NavSection<T>[],
  disabled: ReadonlySet<string>,
): NavSection<T>[] {
  return sections
    .map((s) => ({ label: s.label, items: s.items.filter((id) => !disabled.has(id)) }))
    .filter((s) => s.items.length > 0);
}

/** Resolves a hash base (fragment already stripped, see App.svelte's tabFromHash) to a Tab; a hash naming a
 * disabled tab returns null, same as one naming nothing. */
export function resolveHashTab<T extends string>(
  base: string,
  hashTabs: Partial<Record<string, T>>,
  disabled: ReadonlySet<string>,
): T | null {
  const t = hashTabs[base];
  if (!t || disabled.has(t)) return null;
  return t;
}

/** `fallback` once `tab` is disabled (a settings save can disable the current tab), else `tab` unchanged. */
export function fallbackIfDisabled<T extends string>(tab: T, disabled: ReadonlySet<string>, fallback: T): T {
  return disabled.has(tab) ? fallback : tab;
}

/** The first tab an identity sees, in sidebar order after filtering, used as the landing tab when a no-hash
 * boot resolved to an ungranted one. null when every section emptied out. */
export function firstNavTab<T extends string>(sections: readonly NavSection<T>[]): T | null {
  return sections[0]?.items[0] ?? null;
}

/** Where the brand link goes: `preferred` (the Dashboard) when the identity's nav has it, otherwise the first
 * tab in the nav. null when every section emptied out. A hidden Dashboard (disabled, or not granted) is
 * absent from the filtered sections, so the logo never sends anyone to a page they can't open. */
export function brandTab<T extends string>(sections: readonly NavSection<T>[], preferred: T): T | null {
  return sections.some((s) => s.items.includes(preferred)) ? preferred : firstNavTab(sections);
}
