import { formatServerErrors } from "./serverErrors";
import { DEFAULT_FETCH_TIMEOUT_MS } from "./fetchTimeouts";
import { pageSuffix, totalFromHeader, type Page, type PagedResult } from "./pagination";

export interface Client {
  id: string;
  name: string;
  email: string;
  phone: string;
  billingAddress: string;
  shippingAddress: string;
  notes: string;
  createdAt: string;
  updatedAt: string;
  createdBy: string;
  updatedBy: string;
}

/** POST/PUT body: Client minus server-assigned id, timestamps, and attribution stamps. */
export type ClientInput = Omit<Client, "id" | "createdAt" | "updatedAt" | "createdBy" | "updatedBy">;

/** Reads a {"errors":[...]} body into an Error with readable copy (see serverErrors.ts). Falls back to a
 * generic message, not the raw status. Shared with other API modules such as invoicesApi.ts. */
export async function errorsFrom(res: Response): Promise<Error> {
  try {
    const body = (await res.json()) as { errors?: unknown };
    const errors = Array.isArray(body.errors) ? body.errors.filter((e): e is string => typeof e === "string") : [];
    if (errors.length > 0) return new Error(formatServerErrors(errors));
  } catch {
  }
  return new Error("The server couldn't complete that request. Try again.");
}

/** A network failure returns null so the page can show an offline note. With no argument no
 * `?limit=&offset=` is sent and a plain array is returned (server/pagination.go's opt-in contract); with a
 * `page` it returns `{items, total}`, `total` from X-Total-Count (see pagination.ts). */
export async function listClients(): Promise<Client[] | null>;
export async function listClients(page: Page): Promise<PagedResult<Client> | null>;
export async function listClients(page?: Page): Promise<Client[] | PagedResult<Client> | null> {
  try {
    const url = page ? `api/clients${pageSuffix(page)}` : "api/clients";
    const res = await fetch(url, { signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS) });
    if (!res.ok) return null;
    const body = (await res.json()) as { clients?: unknown };
    const items = Array.isArray(body.clients) ? (body.clients as Client[]) : [];
    return page ? { items, total: totalFromHeader(res, items.length, page) } : items;
  } catch {
    return null;
  }
}

export async function createClient(input: ClientInput): Promise<Client> {
  const res = await fetch("api/clients", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
    signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS),
  });
  if (!res.ok) throw await errorsFrom(res);
  return (await res.json()) as Client;
}

export async function updateClient(id: string, input: ClientInput): Promise<Client> {
  const res = await fetch(`api/clients/${encodeURIComponent(id)}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
    signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS),
  });
  if (!res.ok) throw await errorsFrom(res);
  return (await res.json()) as Client;
}

export async function deleteClient(id: string): Promise<void> {
  const res = await fetch(`api/clients/${encodeURIComponent(id)}`, {
    method: "DELETE",
    signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS),
  });
  if (!res.ok) throw await errorsFrom(res);
}
