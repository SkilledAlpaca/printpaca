/** Pure helpers behind QuotesTab's per-row "Email quote" gating, so the disabled-reason precedence is
 * unit-testable. More involved than invoiceForm.ts's emailDisabledReason: it also depends on the
 * lazily-loaded client list and on the quote having a customer-facing price (quoteHasPrice). */

import type { SavedQuote } from "./api";
import type { Client } from "./clientsApi";
import { quoteHasPrice } from "./convertQuote";
import { hasTabAccess, type ConfigUser } from "./permissions";

// Disabled reason for "Email quote", from smtpConfigured (cfg.smtpConfigured) and the linked client's email in the
// lazily-loaded list. A quote without a customer-facing price is excluded: emailing convertQuote.ts's
// cost-only fallback as a price would be a real bug.
export type QuoteEmailDisabledReason =
  | "unconfigured"
  | "loading"
  | "no-price"
  | "no-client"
  | "clients-unavailable"
  | "no-email"
  | "needs-clients-access"
  | null;

/** The linked client's email, or null when the quote has no client or the client isn't in the loaded list. */
export function clientEmailFor(q: SavedQuote, clients: Client[] | null): string | null {
  if (!q.clientId) return null;
  return clients?.find((c) => c.id === q.clientId)?.email || null;
}

/** Precedence, first match wins: smtp unconfigured, clients loading, no customer-facing price, no client
 * linked, client list failed to load, linked client has no email. */
export function emailDisabledReason(
  q: SavedQuote,
  smtpConfigured: boolean,
  clientsLoaded: boolean,
  clients: Client[] | null,
  canManageClients = true,
): QuoteEmailDisabledReason {
  if (!smtpConfigured) return "unconfigured";
  if (!clientsLoaded) return "loading";
  if (!quoteHasPrice(q)) return "no-price";
  // No usable list (load failed, or a 403 without the grant): entry can't match or update clients; skip it.
  if (clients === null) return canManageClients ? "clients-unavailable" : "needs-clients-access";
  if (!q.clientId) return canManageClients ? "no-client" : "needs-clients-access";
  if (!clientEmailFor(q, clients)) return canManageClients ? "no-email" : "needs-clients-access";
  return null;
}

/** Whether the inline email entry resolves this reason ("no-client" / "no-email"): the action stays
 * available, asking for an address first. */
export function needsEmailEntry(reason: QuoteEmailDisabledReason): boolean {
  return reason === "no-client" || reason === "no-email";
}

/** Trimmed email when it passes the same `@` check as server/clients.go, else null. */
export function validEntryEmail(raw: string): string | null {
  const email = raw.trim();
  return email.includes("@") ? email : null;
}

/** The Clients-tab grant, needed to create a client or set its email. */
export function canManageClientsFor(user: ConfigUser | null | undefined): boolean {
  return hasTabAccess("clients", user);
}
