import { describe, expect, it } from "vitest";
import { isAccessChange403, isSessionExpired401 } from "./accessWatch.svelte";
import type { ConfigUser } from "./permissions";

function employee(permissions: string[]): ConfigUser {
  return { username: "sam", isAdmin: false, permissions, signedInVia: "password" };
}

describe("isAccessChange403", () => {
  it("flags a 403 on an ordinary API path outside demo mode", () => {
    expect(isAccessChange403("api/invoices", false)).toBe(true);
  });

  it("never flags anything while demo mode is on -- every write 403s there by design", () => {
    expect(isAccessChange403("api/invoices", true)).toBe(false);
    expect(isAccessChange403("api/clients", true)).toBe(false);
  });

  it("ignores paths outside /api/", () => {
    expect(isAccessChange403("/custom.css", false)).toBe(false);
    expect(isAccessChange403("/", false)).toBe(false);
  });

  it("exempts the login/logout endpoints", () => {
    expect(isAccessChange403("api/login", false)).toBe(false);
    expect(isAccessChange403("api/logout", false)).toBe(false);
  });

    // A tab that auxiliary-fetches another feature's endpoint the employee wasn't granted must not trip it.
  describe("cross-feature auxiliary fetches", () => {
    it("does not flag a 403 on a path this identity was never granted", () => {
      const quotesOnly = employee(["quotes"]);
      expect(isAccessChange403("api/invoices", false, quotesOnly)).toBe(false);
      expect(isAccessChange403("api/clients", false, quotesOnly)).toBe(false);
    });

    it("does not flag a clients-only identity's 403s from ClientsTab's ledger fetches", () => {
      const clientsOnly = employee(["clients"]);
      expect(isAccessChange403("api/invoices", false, clientsOnly)).toBe(false);
      expect(isAccessChange403("api/quotes", false, clientsOnly)).toBe(false);
    });

    it("does not flag a requests-only identity's 403 from the auto-verify compute call", () => {
      const requestsOnly = employee(["requests"]);
      expect(isAccessChange403("api/quote/compute", false, requestsOnly)).toBe(false);
    });

    it("still flags a REAL revocation: a 403 on a path the identity IS granted", () => {
      const quotesOnly = employee(["quotes"]);
      expect(isAccessChange403("api/quotes", false, quotesOnly)).toBe(true);
    });

    it("still flags a 403 on a permission-gated path with no known identity (admin/single-user/pre-config-load)", () => {
      expect(isAccessChange403("api/invoices", false, null)).toBe(true);
    });

    it("still flags a 403 on a path with no permission mapping at all (e.g. an admin-only route)", () => {
      const quotesOnly = employee(["quotes"]);
      expect(isAccessChange403("api/settings", false, quotesOnly)).toBe(true);
    });

    it("distinguishes /api/quote/compute (permission \"quote\") from /api/quotes (permission \"quotes\")", () => {
      const quoteOnly = employee(["quote"]);
      expect(isAccessChange403("api/quote/compute", false, quoteOnly)).toBe(true);
      expect(isAccessChange403("api/quotes", false, quoteOnly)).toBe(false);
    });
  });
});

// A 401 from any /api/* route other than login/logout/config mid-session means the session died; the
// boot-time 401 on /api/config is handled elsewhere, with no "signed out" notice.
describe("isSessionExpired401", () => {
  it("flags a 401 from an ordinary API path", () => {
    expect(isSessionExpired401("api/invoices")).toBe(true);
    expect(isSessionExpired401("api/quotes")).toBe(true);
    expect(isSessionExpired401("api/settings")).toBe(true);
  });

  it("ignores paths outside /api/", () => {
    expect(isSessionExpired401("/custom.css")).toBe(false);
    expect(isSessionExpired401("/")).toBe(false);
  });

  it("exempts login and logout: a bad-password attempt must never look like a mid-session sign-out", () => {
    expect(isSessionExpired401("api/login")).toBe(false);
    expect(isSessionExpired401("api/logout")).toBe(false);
  });

  it("exempts /api/config: the boot-time never-logged-in-yet 401 has its own, notice-free handling", () => {
    expect(isSessionExpired401("api/config")).toBe(false);
  });
});
