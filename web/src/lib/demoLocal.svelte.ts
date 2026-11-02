// Demo-local, per-browser display preferences (theme, currency) kept in localStorage and never sent to the
// server. App.svelte calls initDemoLocal() only under demoMode, so the non-demo app never touches
// localStorage or the DOM through this module; importing it is inert.
import type { CurrencyConfig } from "./engine/index";

export type ThemeChoice = "system" | "light" | "dark";
export type CurrencyPresetId = "usd" | "eur" | "gbp" | "jpy";
/** The demo banner's "User mode" select: a presentation-only override of what the Users card, Account card
 * and attribution lines show, never enforcement (demo writes are blocked anyway). "multi" is the default
 * because the demo config is seeded with users.multiUser and two example employees (server/demoseed.go). */
export type UserModeChoice = "single" | "multi";

const THEME_KEY = "printpaca-demo-theme";
const CURRENCY_KEY = "printpaca-demo-currency";
const USER_MODE_KEY = "printpaca-demo-usermode";

/** Four cosmetic presets: symbol and decimals only, no conversion of amounts. */
const CURRENCY_PRESETS: Record<CurrencyPresetId, CurrencyConfig> = {
  usd: { symbol: "$", decimals: 2, code: "" },
  eur: { symbol: "€", decimals: 2, code: "" },
  gbp: { symbol: "£", decimals: 2, code: "" },
  jpy: { symbol: "¥", decimals: 0, code: "" },
};

export const CURRENCY_PRESET_OPTIONS: { id: CurrencyPresetId; label: string }[] = [
  { id: "usd", label: "$ USD" },
  { id: "eur", label: "€ EUR" },
  { id: "gbp", label: "£ GBP" },
  { id: "jpy", label: "¥ JPY" },
];

function isThemeChoice(v: unknown): v is ThemeChoice {
  return v === "system" || v === "light" || v === "dark";
}

function isCurrencyPresetId(v: unknown): v is CurrencyPresetId {
  return v === "usd" || v === "eur" || v === "gbp" || v === "jpy";
}

function isUserModeChoice(v: unknown): v is UserModeChoice {
  return v === "single" || v === "multi";
}

/** Reactive source of truth for the banner selects; state.svelte.ts's effectiveConfig() reads `currency` to
 * override the server's currency object. */
export const demoLocal: { theme: ThemeChoice; currency: CurrencyPresetId | null; userMode: UserModeChoice } = $state({
  theme: "system",
  currency: null,
  userMode: "multi",
});

let initialized = false;

/** Sets or clears data-theme on <html>: "system" removes it so the OS-preference media query takes over;
 * "light"/"dark" win over the OS (see the tri-state note in theme.css). */
function applyTheme(theme: ThemeChoice): void {
  if (theme === "system") {
    document.documentElement.removeAttribute("data-theme");
  } else {
    document.documentElement.setAttribute("data-theme", theme);
  }
}

function readTheme(): ThemeChoice {
  try {
    const raw = localStorage.getItem(THEME_KEY);
    return isThemeChoice(raw) ? raw : "system";
  } catch {
    return "system";
  }
}

function readCurrency(): CurrencyPresetId | null {
  try {
    const raw = localStorage.getItem(CURRENCY_KEY);
    return isCurrencyPresetId(raw) ? raw : null;
  } catch {
    return null;
  }
}

function readUserMode(): UserModeChoice {
  try {
    const raw = localStorage.getItem(USER_MODE_KEY);
    return isUserModeChoice(raw) ? raw : "multi";
  } catch {
    return "multi";
  }
}

/** Loads the persisted theme and currency (defaults on any missing or corrupt value) and applies the theme.
 * Idempotent and never throws. */
export function initDemoLocal(): void {
  if (initialized) return;
  initialized = true;
  demoLocal.theme = readTheme();
  demoLocal.currency = readCurrency();
  demoLocal.userMode = readUserMode();
  applyTheme(demoLocal.theme);
}

/** Applies and persists the theme. A persistence failure (private-mode Safari, quota) is swallowed; the
 * theme still applies for the session. */
export function setTheme(theme: ThemeChoice): void {
  demoLocal.theme = theme;
  applyTheme(theme);
  try {
    localStorage.setItem(THEME_KEY, theme);
  } catch {
  }
}

/** Sets the currency preset (null = no override) and persists it; state.svelte.ts's effectiveConfig()
 * threads it into the rendered currency. */
export function setCurrency(id: CurrencyPresetId | null): void {
  demoLocal.currency = id;
  try {
    if (id === null) localStorage.removeItem(CURRENCY_KEY);
    else localStorage.setItem(CURRENCY_KEY, id);
  } catch {
  }
}

/** Sets and persists the banner's user-mode choice. A display switch only: it never writes to the server or
 * changes which tabs are reachable. */
export function setUserMode(mode: UserModeChoice): void {
  demoLocal.userMode = mode;
  try {
    localStorage.setItem(USER_MODE_KEY, mode);
  } catch {
  }
}

/** Resolves a stored preset id to its currency, or null for no override (null or unrecognized id). */
export function resolveCurrencyPreset(id: CurrencyPresetId | null): CurrencyConfig | null {
  return id ? (CURRENCY_PRESETS[id] ?? null) : null;
}
