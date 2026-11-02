import type { ConfigUser } from "./permissions";

/** Cloudflare Access serves this path on every protected hostname; it ends the Access session. */
export const ACCESS_LOGOUT_URL = "/cdn-cgi/access/logout";

/** Whether this session was authenticated by a Cloudflare Access token rather than a printpaca session
 * (server/users.go's signedInVia). */
export function isAccessSignIn(user: ConfigUser | null | undefined): boolean {
  return user?.signedInVia === "access";
}

/** What the sidebar sign-out control does. An Access sign-in leaves through Cloudflare's logout URL;
 * everything else uses POST /api/logout, shown only when authentication is on. */
export type SignOutAction =
  | { kind: "access"; label: string; href: string }
  | { kind: "session"; label: string }
  | null;

export function signOutAction(user: ConfigUser | null | undefined, authEnabled: boolean): SignOutAction {
  if (isAccessSignIn(user)) {
    return { kind: "access", label: "Sign out of Cloudflare Access", href: ACCESS_LOGOUT_URL };
  }
  if (authEnabled) return { kind: "session", label: "Sign out" };
  return null;
}

export type AccountCardMode = "access" | "admin" | "employee" | "none";

export function accountCardMode(user: ConfigUser | null | undefined): AccountCardMode {
  if (!user) return "none";
  if (isAccessSignIn(user)) return "access";
  return user.isAdmin ? "admin" : "employee";
}

export function accessSignInSummary(user: ConfigUser): string {
  return user.accessEmail
    ? `Signed in through Cloudflare Access as ${user.accessEmail}.`
    : "Signed in through Cloudflare Access.";
}
