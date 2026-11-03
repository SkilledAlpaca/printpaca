import { describe, expect, it } from "vitest";
import type { QuoteRequest } from "./requestsApi";
import {
  deleteAllNewConfirmLabel,
  deleteAllNewQuestion,
  isInvoiced,
  matchesRequestFilter,
  replyMailto,
  requestDisplayStatus,
  requestReference,
} from "./requestRow";

function req(over: Partial<QuoteRequest> = {}): QuoteRequest {
  return {
    id: "0123abcd4567ef89",
    status: "viewed",
    name: "Sam Rivera",
    email: "sam@example.com",
    note: "",
    material: "PLA",
    quantity: 1,
    weightG: 10,
    timeSeconds: 600,
    priceCents: 1200,
    shippingCents: 0,
    taxInclusive: false,
    gcodeFilename: "",
    gcodePath: "",
    gcodeBytes: 0,
    createdAt: "2026-11-01T00:00:00Z",
    gcodePrunedAt: "",
    kind: "quote",
    itemId: "",
    itemName: "",
    optionName: "",
    updatedBy: "",
    invoiceId: "",
    invoiceNumber: "",
    invoiceStatus: "",
    needsPrintTime: false,
    ...over,
  };
}

describe("isInvoiced / requestDisplayStatus", () => {
  it("is invoiced only while the linked invoice exists and isn't canceled", () => {
    expect(isInvoiced(req({ invoiceId: "i1", invoiceStatus: "draft" }))).toBe(true);
    expect(isInvoiced(req({ invoiceId: "i1", invoiceStatus: "paid" }))).toBe(true);
    expect(isInvoiced(req({ invoiceId: "i1", invoiceStatus: "canceled" }))).toBe(false);
    expect(isInvoiced(req({ invoiceId: "i1", invoiceStatus: "" }))).toBe(false);
    expect(isInvoiced(req())).toBe(false);
  });
  it("shows invoiced over the stored status", () => {
    expect(requestDisplayStatus(req({ status: "finalized", invoiceId: "i1", invoiceStatus: "sent" }))).toBe("invoiced");
    expect(requestDisplayStatus(req({ status: "new" }))).toBe("new");
  });
});

describe("matchesRequestFilter", () => {
  it("matches name, email, item, option, material and file name", () => {
    const r = req({ kind: "item", itemName: "Dragon", optionName: "Color: Red", gcodeFilename: "benchy.stl" });
    for (const text of ["rivera", "SAM@", "dragon", "red", "pla", "benchy"]) expect(matchesRequestFilter(r, text, "all")).toBe(true);
    expect(matchesRequestFilter(r, "zebra", "all")).toBe(false);
  });
  it("filters by status chip, keeping invoiced requests out of Finalized", () => {
    const invoiced = req({ status: "finalized", invoiceId: "i1", invoiceStatus: "sent" });
    const plain = req({ status: "finalized" });
    expect(matchesRequestFilter(invoiced, "", "invoiced")).toBe(true);
    expect(matchesRequestFilter(invoiced, "", "finalized")).toBe(false);
    expect(matchesRequestFilter(plain, "", "finalized")).toBe(true);
    expect(matchesRequestFilter(req({ status: "new" }), "", "new")).toBe(true);
    expect(matchesRequestFilter(req({ status: "new" }), "", "viewed")).toBe(false);
  });
  it("lists requests waiting for a print time under Needs print time until they are finalized", () => {
    expect(matchesRequestFilter(req({ needsPrintTime: true }), "", "needs-time")).toBe(true);
    expect(matchesRequestFilter(req({ needsPrintTime: true, status: "finalized" }), "", "needs-time")).toBe(false);
    expect(matchesRequestFilter(req(), "", "needs-time")).toBe(false);
  });
});

describe("reply and delete wording", () => {
  it("prefills the reference in the reply subject and body", () => {
    expect(requestReference(req())).toBe("0123ABCD");
    const link = replyMailto(req());
    expect(link.startsWith("mailto:sam%40example.com?subject=")).toBe(true);
    expect(decodeURIComponent(link)).toContain("Re: your request 0123ABCD");
    expect(decodeURIComponent(link)).toContain("Hi Sam Rivera,");
  });
  it("states the count and the permanence", () => {
    expect(deleteAllNewQuestion(8)).toBe("Delete 8 new requests? Their uploaded files are deleted permanently too. This can't be undone.");
    expect(deleteAllNewQuestion(1)).toContain("Delete 1 new request?");
    expect(deleteAllNewConfirmLabel(8)).toBe("Yes, delete 8 requests");
    expect(deleteAllNewConfirmLabel(1)).toBe("Yes, delete 1 request");
  });
});
