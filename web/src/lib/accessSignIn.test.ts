import { describe, expect, it } from "vitest";
import {
  ACCESS_LOGOUT_URL,
  accessSignInSummary,
  accountCardMode,
  isAccessSignIn,
  signOutAction,
} from "./accessSignIn";
import type { ConfigUser } from "./permissions";

const viaAccess: ConfigUser = {
  username: "admin",
  isAdmin: true,
  permissions: [],
  signedInVia: "access",
  accessEmail: "owner@example.com",
};
const viaPassword: ConfigUser = { username: "alice", isAdmin: false, permissions: ["quotes"], signedInVia: "password" };
const admin: ConfigUser = { username: "admin", isAdmin: true, permissions: [], signedInVia: "password" };

describe("isAccessSignIn", () => {
  it("is true only for signedInVia access", () => {
    expect(isAccessSignIn(viaAccess)).toBe(true);
    expect(isAccessSignIn(viaPassword)).toBe(false);
    expect(isAccessSignIn({ ...admin, signedInVia: "none" })).toBe(false);
    expect(isAccessSignIn(null)).toBe(false);
  });
});

describe("signOutAction", () => {
  it("sends an Access sign-in to Cloudflare's logout URL, even with auth disabled", () => {
    for (const authEnabled of [true, false]) {
      expect(signOutAction(viaAccess, authEnabled)).toEqual({
        kind: "access",
        label: "Sign out of Cloudflare Access",
        href: "/cdn-cgi/access/logout",
      });
    }
    expect(ACCESS_LOGOUT_URL).toBe("/cdn-cgi/access/logout");
  });

  it("keeps the session log out for a password sign-in", () => {
    expect(signOutAction(viaPassword, true)).toEqual({ kind: "session", label: "Sign out" });
    expect(signOutAction(null, true)).toEqual({ kind: "session", label: "Sign out" });
  });

  it("shows no control when authentication is off and nothing came through Access", () => {
    expect(signOutAction({ ...admin, signedInVia: "none" }, false)).toBeNull();
    expect(signOutAction(null, false)).toBeNull();
  });
});

describe("accountCardMode", () => {
  it("hides the password form for an Access sign-in, admin or employee", () => {
    expect(accountCardMode(viaAccess)).toBe("access");
    expect(accountCardMode({ ...viaPassword, signedInVia: "access", accessEmail: "a@example.com" })).toBe("access");
  });

  it("keeps the existing branches otherwise", () => {
    expect(accountCardMode(admin)).toBe("admin");
    expect(accountCardMode(viaPassword)).toBe("employee");
    expect(accountCardMode({ username: "x", isAdmin: false, permissions: [], signedInVia: "password" })).toBe("employee");
    expect(accountCardMode(null)).toBe("none");
  });
});

describe("accessSignInSummary", () => {
  it("names the email", () => {
    expect(accessSignInSummary(viaAccess)).toBe("Signed in through Cloudflare Access as owner@example.com.");
  });

  it("copes with a token that carried no email", () => {
    expect(accessSignInSummary({ ...viaAccess, accessEmail: undefined })).toBe("Signed in through Cloudflare Access.");
  });
});
