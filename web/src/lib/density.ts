// Admin density preference (v1.2 D5, D20): "comfortable" (40px controls, 16px gaps) or "compact" (32px controls,
// 8-12px gaps). It lives in this browser's localStorage and applies to the admin app only; the public pages
// never set data-density, so they stay comfortable. Pure helpers here; density.svelte.ts holds the reactive state.

export type Density = "comfortable" | "compact";

export const DENSITY_STORAGE_KEY = "printpaca-density";
export const DEFAULT_DENSITY: Density = "comfortable";

/** Anything that is not exactly "compact" is the default, so a missing, stale or tampered value is harmless. */
export function parseDensity(value: unknown): Density {
  return value === "compact" ? "compact" : DEFAULT_DENSITY;
}

/** Reads the stored choice. Storage can throw (private mode, blocked site data), which falls back to the default. */
export function loadDensity(storage: Pick<Storage, "getItem"> | undefined = safeStorage()): Density {
  try {
    return parseDensity(storage?.getItem(DENSITY_STORAGE_KEY));
  } catch {
    return DEFAULT_DENSITY;
  }
}

/** Persists the choice; a failure only loses the preference for the next visit, so it is swallowed. */
export function saveDensity(density: Density, storage: Pick<Storage, "setItem"> | undefined = safeStorage()): void {
  try {
    storage?.setItem(DENSITY_STORAGE_KEY, density);
  } catch {
    /* Safari private mode / quota: the preference just doesn't persist. */
  }
}

/** Sets or clears data-density on <html>. Comfortable is the attribute's absence, so the base tokens apply. */
export function applyDensity(density: Density, root: Pick<HTMLElement, "setAttribute" | "removeAttribute"> | undefined = globalThis.document?.documentElement): void {
  if (!root) return;
  if (density === "compact") root.setAttribute("data-density", "compact");
  else root.removeAttribute("data-density");
}

function safeStorage(): Storage | undefined {
  try {
    return globalThis.localStorage;
  } catch {
    return undefined;
  }
}
