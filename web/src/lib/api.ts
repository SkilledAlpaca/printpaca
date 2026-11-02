import type { EffectiveConfig } from "./engine/index";
import { DEFAULT_FETCH_TIMEOUT_MS } from "./fetchTimeouts";
import type { QuoteStatus, StatusLogEntry } from "./quoteStatus";

/** Set by loadConfig() when GET /api/config returns 401 (server/auth.go), to tell "not logged in" from
 * other failures that return null. A plain module flag read right after `await loadConfig()`; cleared on
 * every call that sees no 401. */
export let authRequired = false;

export async function loadConfig(): Promise<EffectiveConfig | null> {
  try {
    const res = await fetch("api/config", { signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS) });
    authRequired = res.status === 401;
    if (!res.ok) return null;
    return (await res.json()) as EffectiveConfig;
  } catch {
    authRequired = false;
    return null;
  }
}

/** The saved-quote shape the value modules operate on, built by quotesApi.ts's toSavedQuote. `inputs` is
 * the editor's form snapshot (the server's `formState`), not the engine bag the server recomputes from. */
export interface SavedQuote {
  id: string;
  savedAt: string;
  label: string;
  perUnitTotal: number;
  batchTotal: number;
  recommendedPrice?: number;
  resolvedPrice?: number; // per-unit cents after any sale-price override; absent on cost-only saves
  clientId?: string; // linked client's id, when the quote is associated with one
  clientName?: string; // linked client's display name, denormalized alongside clientId
  project?: string; // free-text project name from the quote tab's print toolbar
  reference?: string; // free-text quote number/reference from the quote tab's print toolbar
  invoicedAs?: string; // the id of the invoice this quote was converted to, if any
  inputs: unknown; // snapshot of the quote state, restored verbatim
  partCount: number; // parts in the quote: 1 = single part; 2+ = multi-part order (totals above describe it as one unit)
  status: QuoteStatus;
  statusLog: StatusLogEntry[];
  // Optional like status above; AttributionLine.svelte treats a missing value as "".
  createdBy?: string;
  updatedBy?: string;
}

/** A client-side id for rows the browser creates before (or without) any server round trip, such as quote
 * parts. crypto.randomUUID needs a secure context and is undefined on plain-HTTP LAN hosts, the main
 * self-hosted target; falls back to timestamp plus random. */
export function localId(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
}

