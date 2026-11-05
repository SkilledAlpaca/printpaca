// Admin theme preference. The choice lives in this browser's localStorage and applies to the admin
// app only (the public shop never imports this). No stored choice means the device setting decides: no
// data-theme attribute is set and theme.css's prefers-color-scheme block applies. Pure helpers here;
// theme.svelte.ts holds the reactive state.

export type Theme = "light" | "dark";

export const THEME_STORAGE_KEY = "printpaca-theme";

/** Only exactly "light" or "dark" is a choice; a missing, stale or tampered value means no choice. */
export function parseTheme(value: unknown): Theme | null {
  return value === "light" || value === "dark" ? value : null;
}

/** Reads the stored choice. Storage can throw (private mode, blocked site data), which means no choice. */
export function loadTheme(storage: Pick<Storage, "getItem"> | undefined = safeStorage()): Theme | null {
  try {
    return parseTheme(storage?.getItem(THEME_STORAGE_KEY));
  } catch {
    return null;
  }
}

/** Persists the choice; a failure only loses the preference for the next visit, so it is swallowed. */
export function saveTheme(theme: Theme, storage: Pick<Storage, "setItem"> | undefined = safeStorage()): void {
  try {
    storage?.setItem(THEME_STORAGE_KEY, theme);
  } catch {
    /* Safari private mode / quota: the preference just doesn't persist. */
  }
}

/** Sets data-theme on <html> for a choice, or clears it so the device setting applies. */
export function applyTheme(theme: Theme | null, root: Pick<HTMLElement, "setAttribute" | "removeAttribute"> | undefined = globalThis.document?.documentElement): void {
  if (!root) return;
  if (theme) root.setAttribute("data-theme", theme);
  else root.removeAttribute("data-theme");
}

/** The theme actually showing: the explicit attribute when present, else the device preference. */
export function effectiveTheme(
  root: Pick<HTMLElement, "getAttribute"> | undefined = globalThis.document?.documentElement,
  prefersDark: boolean = safePrefersDark(),
): Theme {
  const attr = parseTheme(root?.getAttribute("data-theme"));
  return attr ?? (prefersDark ? "dark" : "light");
}

function safePrefersDark(): boolean {
  try {
    return globalThis.matchMedia?.("(prefers-color-scheme: dark)").matches === true;
  } catch {
    return false;
  }
}

function safeStorage(): Storage | undefined {
  try {
    return globalThis.localStorage;
  } catch {
    return undefined;
  }
}
