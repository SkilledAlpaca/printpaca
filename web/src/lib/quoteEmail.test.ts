import { describe, expect, it } from "vitest";
import { clientEmailFor, emailDisabledReason, needsEmailEntry, validEntryEmail, canManageClientsFor } from "./quoteEmail";
import type { SavedQuote } from "./api";
import type { Client } from "./clientsApi";

function makeQuote(overrides: Partial<SavedQuote> = {}): SavedQuote {
  return {
    id: "q1",
    savedAt: "2026-01-01T00:00:00Z",
    label: "Bracket",
    perUnitTotal: 500,
    batchTotal: 1000,
    resolvedPrice: 1200,
    clientId: "client-1",
    inputs: {},
    status: "draft",
    statusLog: [],
    partCount: 1,
    ...overrides,
  };
}

function makeClient(overrides: Partial<Client> = {}): Client {
  return {
    id: "client-1",
    name: "Acme Co",
    email: "acme@example.com",
    phone: "",
    billingAddress: "",
    shippingAddress: "",
    notes: "",
    createdAt: "",
    updatedAt: "",
    createdBy: "",
    updatedBy: "",
    ...overrides,
  };
}

describe("clientEmailFor", () => {
  it("null when the quote isn't linked to a client", () => {
    expect(clientEmailFor(makeQuote({ clientId: undefined }), [makeClient()])).toBeNull();
  });
  it("null when the clients list hasn't loaded", () => {
    expect(clientEmailFor(makeQuote(), null)).toBeNull();
  });
  it("null when the linked client id isn't in the loaded list", () => {
    expect(clientEmailFor(makeQuote({ clientId: "missing" }), [makeClient()])).toBeNull();
  });
  it("null when the linked client has no email on file", () => {
    expect(clientEmailFor(makeQuote(), [makeClient({ email: "" })])).toBeNull();
  });
  it("the linked client's email when present", () => {
    expect(clientEmailFor(makeQuote(), [makeClient()])).toBe("acme@example.com");
  });
});

describe("emailDisabledReason", () => {
  const clients = [makeClient()];

  it('"unconfigured" when smtp host is blank, regardless of everything else', () => {
    expect(emailDisabledReason(makeQuote(), false, true, clients)).toBe("unconfigured");
    expect(emailDisabledReason(makeQuote({ resolvedPrice: undefined, recommendedPrice: undefined }), false, false, null)).toBe(
      "unconfigured",
    );
  });

  it('"loading" when smtp is configured but clients haven\'t loaded yet, beating no-price/no-client', () => {
    expect(emailDisabledReason(makeQuote(), true, false, clients)).toBe("loading");
    expect(
      emailDisabledReason(makeQuote({ resolvedPrice: undefined, recommendedPrice: undefined, clientId: undefined }), true, false, null),
    ).toBe("loading");
  });

  it('"no-price" when the quote has no customer-facing price, beating no-client', () => {
    expect(
      emailDisabledReason(
        makeQuote({ resolvedPrice: undefined, recommendedPrice: undefined, clientId: undefined }),
        true,
        true,
        clients,
      ),
    ).toBe("no-price");
  });

  it('"no-client" when the quote has a price but isn\'t linked to a client', () => {
    expect(emailDisabledReason(makeQuote({ clientId: undefined }), true, true, clients)).toBe("no-client");
  });

  it('"clients-unavailable" when the client list failed to load, beating no-email', () => {
    expect(emailDisabledReason(makeQuote(), true, true, null)).toBe("clients-unavailable");
  });

  it('"no-email" when the linked client has no email on file', () => {
    expect(emailDisabledReason(makeQuote(), true, true, [makeClient({ email: "" })])).toBe("no-email");
  });

  it("null (enabled) when everything checks out", () => {
    expect(emailDisabledReason(makeQuote(), true, true, clients)).toBeNull();
  });

  it("falls back to recommendedPrice when resolvedPrice is absent (still counts as having a price)", () => {
    expect(emailDisabledReason(makeQuote({ resolvedPrice: undefined, recommendedPrice: 900 }), true, true, clients)).toBeNull();
  });
});

describe("needs-clients-access", () => {
  const smtp = true;
  it("unloaded client list: clients-unavailable with the grant, needs-clients-access without", () => {
    expect(emailDisabledReason(makeQuote({ clientId: undefined }), smtp, true, null)).toBe("clients-unavailable");
    expect(emailDisabledReason(makeQuote({ clientId: undefined }), smtp, true, null, false)).toBe("needs-clients-access");
    expect(emailDisabledReason(makeQuote(), smtp, true, null, false)).toBe("needs-clients-access");
  });
  it("replaces no-client when the user lacks the clients grant", () => {
    expect(emailDisabledReason(makeQuote({ clientId: undefined }), smtp, true, [], false)).toBe("needs-clients-access");
  });
  it("replaces no-email when the user lacks the clients grant", () => {
    expect(emailDisabledReason(makeQuote(), smtp, true, [makeClient({ email: "" })], false)).toBe("needs-clients-access");
  });
  it("does not affect a linked client that already has an email", () => {
    expect(emailDisabledReason(makeQuote(), smtp, true, [makeClient()], false)).toBeNull();
  });
  it("loses to earlier reasons (no-price, unconfigured)", () => {
    const noPrice = makeQuote({ clientId: undefined, resolvedPrice: undefined, recommendedPrice: undefined });
    expect(emailDisabledReason(noPrice, smtp, true, [], false)).toBe("no-price");
    expect(emailDisabledReason(makeQuote({ clientId: undefined }), false, true, [], false)).toBe("unconfigured");
  });
  it("needsEmailEntry is true only for the two entry reasons", () => {
    expect(needsEmailEntry("no-client")).toBe(true);
    expect(needsEmailEntry("no-email")).toBe(true);
    expect(needsEmailEntry("needs-clients-access")).toBe(false);
    expect(needsEmailEntry(null)).toBe(false);
  });
  it("validEntryEmail trims and requires @", () => {
    expect(validEntryEmail("  a@b.co ")).toBe("a@b.co");
    expect(validEntryEmail("nope")).toBeNull();
    expect(validEntryEmail("   ")).toBeNull();
  });
  it("canManageClientsFor follows the clients grant", () => {
    expect(canManageClientsFor(null)).toBe(true);
    expect(canManageClientsFor({ username: "a", isAdmin: false, permissions: ["quotes"], signedInVia: "password" })).toBe(false);
    expect(canManageClientsFor({ username: "a", isAdmin: false, permissions: ["clients"], signedInVia: "password" })).toBe(true);
  });
});
