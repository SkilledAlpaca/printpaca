/** Global 403 watcher: surfaces "your access changed -- reload" when a grant is revoked live. No API module
 * shares a fetch wrapper, so this patches the global fetch once, installed from App.svelte before any
 * child requests. A 403 here only means adminOnly/requirePermission (server/identity.go) or demoWriteGuard
 * (server/auth.go) refused; a 401 is handled by api.ts's authRequired. isAccessChange403 exempts demo
 * mode's expected write blocks and cross-feature fetches the identity was never granted. */

import { hasTabAccess, type ConfigUser } from "./permissions";

/** Every path here is relative to the admin app's base ("api/quotes" for GET /admin/api/quotes): the app
 * lives under /admin/ and calls the API with relative URLs, so installAccessWatch strips the base before
 * asking these functions. */

/** Paths whose 401/403 never means "access changed" or "signed out": api/login 401s on a bad password,
 * api/logout's response is ignored, and api/config legitimately 401s on every unauthenticated boot. */
const EXEMPT_PATHS = new Set(["api/login", "api/logout", "api/config"]);

/** Maps an api/* path to the permission key that gates it server-side (mirrors server/static.go and
 * server/proxy.go), matched on the first path segment so "api/quote/compute" never collides with
 * "api/quotes". null for paths with no permission gate: they get no exemption. Extend this table when
 * adding a cross-feature fetch. */
function permissionForPath(path: string): import("./permissions").GrantableTabKey | null {
  const segments = path.split("/").filter(Boolean); // "api/quotes" -> ["api", "quotes"]
  if (segments[0] !== "api") return null;
  switch (segments[1]) {
    case "quotes":
      return "quotes";
    case "quote": // /api/quote/compute
      return "quote";
    case "clients":
      return "clients";
    case "invoices":
      return "invoices";
    case "requests":
      return "requests";
    case "items":
      return "items";
    case "dashboard":
      return "dashboard";
    case "spoolman": // proxy.go's Spoolman proxy, gated on "filament"
      return "filament";
    case "moonraker": // proxy.go's Moonraker proxy, gated on "quote"
      return "quote";
    default:
      return null;
  }
}

/** Pure decision function, separate from the fetch patch so it is unit-testable. `path` has no query or
 * hash. Demo mode's writes are permanent expected 403s and never trip the banner. A 403 on a path this
 * identity was never granted is suppressed; for a held permission (null user counts as everything granted)
 * it is the live-revocation signal. */
export function isAccessChange403(path: string, demoMode: boolean, user?: ConfigUser | null): boolean {
  if (demoMode) return false;
  if (!path.startsWith("api/")) return false;
  if (EXEMPT_PATHS.has(path)) return false;
  const permission = permissionForPath(path);
  if (permission && !hasTabAccess(permission, user)) return false;
  return true;
}

/** A 401 from any api/* path outside EXEMPT_PATHS: tabs render only after GET /api/config succeeded, so it
 * means the session died (server restart, cookie expiry, password change), never "not logged in yet". */
export function isSessionExpired401(path: string): boolean {
  if (!path.startsWith("api/")) return false;
  return !EXEMPT_PATHS.has(path);
}

/** `revoked` and `sessionExpired` flip true once and stay until resetAccessRevoked() (fresh load or
 * re-login). Separate flags: the revoked banner overlays the app shell, while sessionExpired replaces it
 * with LoginScreen. */
export const accessWatch: { revoked: boolean; sessionExpired: boolean } = $state({
  revoked: false,
  sessionExpired: false,
});

export function resetAccessRevoked(): void {
  accessWatch.revoked = false;
  accessWatch.sessionExpired = false;
}

/** The request's path relative to the admin app's base: "/admin/api/quotes" -> "api/quotes". A URL outside
 * the base (another origin's path, the Cloudflare sign-out) keeps its path so it never matches api/. */
function appRelativePath(rawUrl: string): string {
  const base = new URL("./", window.location.href).pathname; // "/admin/"
  const { pathname } = new URL(rawUrl, window.location.href);
  return pathname.startsWith(base) ? pathname.slice(base.length) : pathname;
}

let installed = false;

/** Wraps window.fetch once per page load. `isDemoMode` and `getUser` are read fresh per response because
 * neither is known until /api/config lands; the null-user default exempts nothing. Best-effort: if a
 * response can't be inspected, the original is returned untouched. */
export function installAccessWatch(isDemoMode: () => boolean, getUser: () => ConfigUser | null = () => null): void {
  if (installed || typeof window === "undefined" || typeof window.fetch !== "function") return;
  installed = true;
  const original = window.fetch.bind(window);
  window.fetch = async (...args: Parameters<typeof fetch>) => {
    const res = await original(...args);
    try {
      if (res.status === 403 || res.status === 401) {
        const input = args[0];
        const rawUrl = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
        const path = appRelativePath(rawUrl);
        if (res.status === 403) {
          if (isAccessChange403(path, isDemoMode(), getUser())) accessWatch.revoked = true;
        } else if (isSessionExpired401(path)) {
          accessWatch.sessionExpired = true;
        }
      }
    } catch {
    }
    return res;
  };
}
