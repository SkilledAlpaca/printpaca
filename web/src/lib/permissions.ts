/** Pure permission-model helpers over the `user` identity /api/config carries (server/main.go's
 * handleConfig, server/users.go's meResponse). */

/** The ten grantable tab keys (server/identity.go's validPermissionKeys). App.svelte's "guide" (open to
 * everyone) and "settings" (admin-only) are not grantable; see hasTabAccess. Independent of App.svelte so
 * this module has no Svelte dependency. */
export const GRANTABLE_TAB_KEYS = [
  "quote",
  "quotes",
  "requests",
  "invoices",
  "items",
  "clients",
  "dashboard",
  "filament",
  "electricity",
  "printers",
] as const;

export type GrantableTabKey = (typeof GRANTABLE_TAB_KEYS)[number];

/** Display labels for the ten keys, matching App.svelte's TABS labels so the Users card's permission grid
 * reads like the sidebar. */
export const GRANTABLE_TAB_LABELS: Record<GrantableTabKey, string> = {
  quote: "New quote",
  quotes: "Quotes",
  requests: "Requests",
  invoices: "Invoices",
  items: "Shop items",
  clients: "Clients",
  dashboard: "Dashboard",
  filament: "Filament",
  electricity: "Electricity",
  printers: "Printers",
};

const GRANTABLE_SET: ReadonlySet<string> = new Set(GRANTABLE_TAB_KEYS);

/** Whether `id` is one of the ten grantable keys ("guide" and "settings" are not; see hasTabAccess). */
export function isGrantableTab(id: string): id is GrantableTabKey {
  return GRANTABLE_SET.has(id);
}

/** GET /api/me's body and /api/config's "user" extra (server/users.go's meResponse): {username, isAdmin,
 * permissions}. */
export interface ConfigUser {
  username: string;
  isAdmin: boolean;
  permissions: string[];
  /** How this request was authenticated (server/users.go's meResponse): "access" is a verified Cloudflare
   * Access token, "password" a printpaca session, "none" authentication off. */
  signedInVia: SignedInVia;
  /** The Access token's email; set only when signedInVia is "access". */
  accessEmail?: string;
}

type SignedInVia = "password" | "access" | "none";

/** Whether `user` grants access to tab `id`. Admin, a missing `user` (config not loaded yet) and
 * non-grantable tabs always pass; a grantable tab passes only if named in user.permissions. UX
 * decluttering only, mirroring server/identity.go's requirePermission; the server enforces the real
 * boundary. */
export function hasTabAccess(id: string, user: ConfigUser | null | undefined): boolean {
  if (!user || user.isAdmin) return true;
  if (!isGrantableTab(id)) return true;
  return user.permissions.includes(id);
}

/** Whether `user` can use the quote editor. This is UI gating, not a server boundary: pricing in the editor
 * calls POST /api/quote/compute, which the server gates on the "quote" grant (server/static.go), so the
 * buttons follow that grant. The server still lets a "quotes" holder create and update saved quotes
 * (POST/PUT /api/quotes are gated on "quotes"), so hiding New quote and Open in editor for a user holding
 * only "quotes" avoids leading them to "No access" at pricing time; it does not stop them calling those
 * endpoints directly. The New quote nav entry follows the same grant (hasTabAccess), so nav and buttons agree. */
export function canEditQuotes(user: ConfigUser | null | undefined): boolean {
  return hasTabAccess("quote", user);
}

/** Whether `user` may open Settings' admin cards (integrations, users, email). Settings itself is open to
 * everyone (employees get the Account card only), so setup links elsewhere ("Set it up in Settings") should
 * show only when this is true, and read "Ask an admin" otherwise. */
export function canConfigureShop(user: ConfigUser | null | undefined): boolean {
  return !user || user.isAdmin;
}

/** Mirrors the server gate on GET /api/settings (requireAnyPermission: filament, electricity or printers).
 * Admin or a null user always pass; an employee needs one of those three grants, not any other tab's.
 * Needed because InvoicesTab embeds a SettingsEditor, so an employee holding only "invoices" would
 * otherwise 403 on mount and look like an outage. */
export function canReadSettings(user: ConfigUser | null | undefined): boolean {
  if (!user || user.isAdmin) return true;
  return user.permissions.some((p) => p === "filament" || p === "electricity" || p === "printers");
}

/** The subset of `ids` NOT granted to `user`, to union with ui.disabledTabs before filterNavSections. Nav
 * visibility only, not the "no access" check App.svelte does for a direct hash navigation. */
export function ungrantedTabs<T extends string>(ids: readonly T[], user: ConfigUser | null | undefined): Set<T> {
  return new Set(ids.filter((id) => !hasTabAccess(id, user)));
}

/** The "user" extra of a loaded /api/config; null while the config hasn't loaded, which callers treat as
 * "everything granted". The config is first-party, so there is no shape validation. */
export function extractConfigUser(cfg: { user: ConfigUser } | null | undefined): ConfigUser | null {
  return cfg?.user ?? null;
}

/** The Settings "Users" section's persisted shape (server/config.go's Users): config-file content
 * round-tripped through /api/settings, unlike the per-request ConfigUser identity (see settings.ts's
 * ServerConfig). */
export interface UsersModeConfig {
  multiUser: boolean;
  /** The Cloudflare Access email that signs in as admin in multi-user mode (server/config.go's
   * Users.AccessAdminEmail); "" when unset. */
  accessAdminEmail: string;
}
