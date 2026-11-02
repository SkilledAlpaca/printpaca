/** Orchestration behind QuotesTab's "email a quote with no deliverable address": ensure a known client
 * carries the entered email, link the quote to it, then send. API calls are injected so ordering and retry
 * are unit-testable. */

import type { SavedQuote } from "./api";
import type { Client, ClientInput } from "./clientsApi";

export interface WalkInSendDeps<R> {
  createClient: (input: ClientInput) => Promise<Client>;
  updateClient: (id: string, input: ClientInput) => Promise<Client>;
  linkQuote: (quoteId: string, client: Client) => Promise<void>;
  send: (email: string) => Promise<R>;
  /** Called with the latest client list after every client change, even if a later step throws, so the
   * caller's list never misses a created client. */
  onClients: (clients: Client[]) => void;
}

/** quoteId to the client created for it by an earlier, partly failed attempt; a retry reuses it instead of
 * creating a duplicate. */
export type CreatedClients = Map<string, Client>;

function toInput(c: Client, email: string): ClientInput {
  return { name: c.name, email, phone: c.phone, billingAddress: c.billingAddress, shippingAddress: c.shippingAddress, notes: c.notes };
}

function sameName(a: string, b: string): boolean {
  return a.trim().toLowerCase() === b.trim().toLowerCase();
}

export async function sendWithEnteredEmail<R>(args: {
  quote: SavedQuote;
  email: string;
  clients: Client[];
  created: CreatedClients;
  deps: WalkInSendDeps<R>;
}): Promise<R> {
  const { quote, email, created, deps } = args;
  let clients = args.clients;
  const replace = (c: Client) => {
    clients = clients.map((x) => (x.id === c.id ? c : x));
    deps.onClients(clients);
  };

  if (quote.clientId) {
    // Linked client without an email: set it, no relink needed.
    const existing = clients.find((c) => c.id === quote.clientId);
    if (!existing) throw new Error("Couldn't find this quote's client. Reload and try again.");
    replace(await deps.updateClient(existing.id, toInput(existing, email)));
    return deps.send(email);
  }

  const typedName = quote.clientName?.trim() ?? "";
  const name = typedName || "Walk-in";
  let client = created.get(quote.id);
  if (!client) {
    // Only a real typed name may match an existing client: the "Walk-in" placeholder would pin every unnamed
    // customer's email onto one shared record.
    const match = typedName ? clients.find((c) => sameName(c.name, typedName)) : undefined;
    if (match && !match.email) {
      client = await deps.updateClient(match.id, toInput(match, email));
      replace(client);
    } else if (match && match.email.toLowerCase() === email.toLowerCase()) {
      client = match;
    } else {
      client = await deps.createClient({ name, email, phone: "", billingAddress: "", shippingAddress: "", notes: "" });
      clients = [...clients, client];
      deps.onClients(clients);
    }
    created.set(quote.id, client);
  }
  await deps.linkQuote(quote.id, client);
  created.delete(quote.id);
  return deps.send(email);
}
