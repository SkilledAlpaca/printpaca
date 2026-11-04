// The customer shop's light/dark choice (D53). The choice lives in a first-party cookie, not localStorage, so the
// server can read it and put data-theme on <html> in the HTML itself (server/public.go): the public CSP has no
// inline script, so a client-only theme would flash. The admin theme (lib/theme.ts, localStorage) is separate.
export type ShopTheme = "light" | "dark";

export const SHOP_THEME_COOKIE = "pp_theme";
const ONE_YEAR_SECONDS = 365 * 24 * 60 * 60;

/** Only exactly "light" or "dark" is a choice; anything else counts as absent (the server applies the same rule). */
export function parseShopTheme(value: unknown): ShopTheme | null {
  return value === "light" || value === "dark" ? value : null;
}

/** The cookie string for a choice: SameSite=Lax, Path=/, one year, not HttpOnly (this script writes it). */
export function themeCookie(theme: ShopTheme, secure: boolean): string {
  return `${SHOP_THEME_COOKIE}=${theme}; Max-Age=${ONE_YEAR_SECONDS}; Path=/; SameSite=Lax${secure ? "; Secure" : ""}`;
}

/** Reads the choice out of a document.cookie string. */
export function readShopThemeCookie(cookies: string): ShopTheme | null {
  for (const part of cookies.split(";")) {
    const eq = part.indexOf("=");
    if (eq < 0) continue;
    if (part.slice(0, eq).trim() === SHOP_THEME_COOKIE) return parseShopTheme(part.slice(eq + 1).trim());
  }
  return null;
}

/** Applies the choice to <html> at once and remembers it. A blocked cookie only loses the next visit's choice. */
export function setShopTheme(theme: ShopTheme, doc: Document = document): void {
  doc.documentElement.setAttribute("data-theme", theme);
  try {
    doc.cookie = themeCookie(theme, doc.location?.protocol === "https:");
  } catch {
    /* cookies blocked: the choice lasts until the page closes */
  }
}
