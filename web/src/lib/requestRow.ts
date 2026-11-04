import type { QuoteRequest } from "./requestsApi";

/** The Requests list's status chips. "invoiced" and "needs-time" are derived: a finalized request with a live
 * invoice link, and a public estimate still waiting for its print time. */
export type RequestFilter = "all" | "new" | "viewed" | "finalized" | "invoiced" | "needs-time";

export const REQUEST_FILTER_CHIPS: { key: RequestFilter; label: string }[] = [
  { key: "all", label: "All" },
  { key: "new", label: "New" },
  { key: "viewed", label: "Viewed" },
  { key: "finalized", label: "Finalized" },
  { key: "invoiced", label: "Invoiced" },
  { key: "needs-time", label: "Needs print time" },
];

/** True while the request points at an invoice that still counts: one that exists and is not canceled. A
 * deleted invoice clears the link on the server, and a canceled one frees the request to be converted again. */
export function isInvoiced(r: Pick<QuoteRequest, "invoiceId" | "invoiceStatus">): boolean {
  return r.invoiceId !== "" && r.invoiceStatus !== "" && r.invoiceStatus !== "canceled";
}

/** The status the list and detail badge show: "invoiced" for a converted request, else its own status. */
export function requestDisplayStatus(r: Pick<QuoteRequest, "status" | "invoiceId" | "invoiceStatus">): string {
  return isInvoiced(r) ? "invoiced" : r.status;
}

/** True when the request passes the text and chip filters (AND). The text matches the requester's name, email,
 * reference, the gallery item, the option summary, the material and the uploaded file name, case-insensitively. */
export function matchesRequestFilter(r: QuoteRequest, text: string, chip: RequestFilter): boolean {
  const q = text.trim().toLowerCase();
  if (q !== "") {
    const hay = [r.name, r.email, r.reference ?? "", r.itemName, r.optionName, r.material, r.gcodeFilename].join("\n").toLowerCase();
    if (!hay.includes(q)) return false;
  }
  switch (chip) {
    case "all":
      return true;
    case "invoiced":
      return isInvoiced(r);
    case "needs-time":
      return r.needsPrintTime && r.status !== "finalized";
    case "finalized":
      return r.status === "finalized" && !isInvoiced(r);
    default:
      return r.status === chip;
  }
}

/** The reference a customer can quote: the short per-shop reference the public form gave them ("HL-1001"),
 * or for a row created before references existed the first eight characters of the request id, uppercase. */
export function requestReference(r: Pick<QuoteRequest, "id"> & Partial<Pick<QuoteRequest, "reference">>): string {
  return r.reference ? r.reference : r.id.slice(0, 8).toUpperCase();
}

/** A mailto: link that replies to the requester with the reference in the subject. */
export function replyMailto(r: Pick<QuoteRequest, "id" | "name" | "email"> & Partial<Pick<QuoteRequest, "reference">>): string {
  const ref = requestReference(r);
  const subject = `Re: your request ${ref}`;
  const body = `Hi ${r.name},\n\nAbout your request ${ref}:\n\n`;
  return `mailto:${encodeURIComponent(r.email)}?subject=${encodeURIComponent(subject)}&body=${encodeURIComponent(body)}`;
}

/** The "Delete all new" confirm sentence: the count and the permanence, since the stored files go with the rows. */
export function deleteAllNewQuestion(count: number): string {
  const noun = count === 1 ? "new request" : "new requests";
  return `Delete ${count} ${noun}? Their uploaded files are deleted permanently too. This can't be undone.`;
}

/** The confirm button's words for the same action. */
export function deleteAllNewConfirmLabel(count: number): string {
  return `Yes, delete ${count} ${count === 1 ? "request" : "requests"}`;
}
