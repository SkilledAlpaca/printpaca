// Applies the shop's accent color (shop.accent from /api/public/config) to the public page only. The server has
// already checked it against the shop backgrounds and derived the dark-mode variant (server/accent.go), so this
// only picks the scheme and sets four custom properties on <html>. The admin app never loads this module.
//
// Set through the CSSOM (style.setProperty) rather than an injected <style> element: the public listener's CSP
// has no 'unsafe-inline' for styles. The scheme follows the OS preference, or an explicit data-theme on <html> (the server sets it from the pp_theme
// cookie, and the shop's theme toggle changes it; a MutationObserver re-applies the matching variant).
import type { PublicShop } from "./api";

type ShopAccent = NonNullable<PublicShop["accent"]>;
type AccentScheme = ShopAccent["light"];

const HEX = /^#[0-9a-f]{6}$/i;

/** The custom properties for one scheme, or null when either color isn't a plain #rrggbb value. The accent
 * doubles as the link text and focus-ring color: the server's check requires 4.5:1 for it as text on every shop
 * background, which also clears the 3:1 a focus ring needs. buttonText is the label color on the accent fill. */
export function accentVars(scheme: AccentScheme): Record<string, string> | null {
  if (!HEX.test(scheme.accent) || !HEX.test(scheme.buttonText)) return null;
  return {
    "--accent": scheme.accent,
    "--accent-text": scheme.accent,
    "--focus-ring": scheme.accent,
    "--accent-contrast": scheme.buttonText,
  };
}

/** True when the page renders in the dark scheme. */
export function prefersDark(root: HTMLElement, mq: Pick<MediaQueryList, "matches">): boolean {
  const explicit = root.getAttribute("data-theme");
  return explicit === "dark" || (explicit !== "light" && mq.matches);
}

/** Applies the accent and keeps it in step with the OS scheme. Returns a cleanup that removes it. A null accent
 * (the theme's own) does nothing. */
export function applyShopAccent(accent: ShopAccent | null, root: HTMLElement = document.documentElement): () => void {
  if (!accent) return () => {};
  const mq = window.matchMedia("(prefers-color-scheme: dark)");
  const set: string[] = [];
  const apply = () => {
    const vars = accentVars(prefersDark(root, mq) ? accent.dark : accent.light);
    if (!vars) return;
    for (const [name, value] of Object.entries(vars)) {
      root.style.setProperty(name, value);
      if (!set.includes(name)) set.push(name);
    }
  };
  apply();
  mq.addEventListener("change", apply);
  // The shop's theme toggle (D53) changes data-theme on <html>; re-apply so the matching variant follows at once.
  const mo = new MutationObserver(apply);
  mo.observe(root, { attributes: true, attributeFilter: ["data-theme"] });
  return () => {
    mq.removeEventListener("change", apply);
    mo.disconnect();
    for (const name of set) root.style.removeProperty(name);
  };
}
