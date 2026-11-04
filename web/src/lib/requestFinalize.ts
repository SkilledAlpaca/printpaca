import type { QuoteRequest } from "./requestsApi";

/** Shown beside a disabled Finalize and returned by the server's 409 for the same case. */
export const FINALIZE_NEEDS_TIME = "Enter the print time before finalizing";

/** Why Finalize can't run for this request right now, or "" when it can. */
export function finalizeBlockedReason(r: Pick<QuoteRequest, "needsPrintTime">): string {
  return r.needsPrintTime ? FINALIZE_NEEDS_TIME : "";
}

/** Splits bulk-selected ids into the ones Finalize selected sends and the ones it skips (no print time yet). */
export function splitBulkFinalize(
  ids: string[],
  requests: Pick<QuoteRequest, "id" | "needsPrintTime">[],
): { ready: string[]; skipped: string[] } {
  const waiting = new Set(requests.filter((r) => r.needsPrintTime).map((r) => r.id));
  return { ready: ids.filter((id) => !waiting.has(id)), skipped: ids.filter((id) => waiting.has(id)) };
}
