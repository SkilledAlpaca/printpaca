import { MediaQuery } from "svelte/reactivity";
import { applyTheme, loadTheme, saveTheme, type Theme } from "./theme";

// Reactive admin theme. Importing this module reads the stored choice and applies it to <html> immediately,
// before the first render, so the admin never flashes the wrong theme. Only App.svelte (the admin shell)
// imports it. With no stored choice the attribute stays absent and the device setting decides.
const stored = loadTheme();
if (stored) applyTheme(stored);

const deviceDark = new MediaQuery("prefers-color-scheme: dark", false);
const state = $state<{ attr: Theme | null }>({ attr: stored });

export const theme = {
  /** The theme showing now: the explicit choice, else the device preference. */
  get current(): Theme {
    return state.attr ?? (deviceDark.current ? "dark" : "light");
  },
};

/** Re-reads <html data-theme> after something else changed it (the demo banner's theme select). */
export function syncThemeFromDocument(): void {
  const attr = document.documentElement.getAttribute("data-theme");
  state.attr = attr === "light" || attr === "dark" ? attr : null;
}

export function setTheme(next: Theme): void {
  state.attr = next;
  applyTheme(next);
  saveTheme(next);
}

export function toggleTheme(): Theme {
  const next: Theme = theme.current === "dark" ? "light" : "dark";
  setTheme(next);
  return next;
}
