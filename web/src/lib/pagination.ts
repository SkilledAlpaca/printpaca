/** Client half of server/pagination.go's opt-in list pagination (?limit=&offset=, X-Total-Count), shared by
 * the four list fetchers, plus the backfill loop the list tabs use so a filter never runs over a partial
 * list. */

/** One opt-in page request; mirrors server/pagination.go's `?limit=&offset=`. */
export interface Page {
  limit: number;
  offset: number;
}

/** One paginated result: the sliced rows plus the pre-slice total (X-Total-Count). */
export interface PagedResult<T> {
  items: T[];
  total: number;
}

/** Server's paginationMaxLimit: the largest `limit` a request can carry, and the chunk size
 * ensureFullyLoaded sweeps with. */
const MAX_PAGE_LIMIT = 500;

/** Initial and load-more page size for the four list tabs, well under MAX_PAGE_LIMIT. */
export const INITIAL_PAGE_LIMIT = 100;

/** Builds the `?limit=&offset=` suffix for a paginated list request. */
export function pageSuffix(page: Page): string {
  return `?limit=${page.limit}&offset=${page.offset}`;
}

/** Reads X-Total-Count off a paginated response; falls back to `itemsLength + page.offset` (the smallest
 * consistent total) when missing or unparseable. */
export function totalFromHeader(res: Response, itemsLength: number, page: Page): number {
  const raw = res.headers.get("X-Total-Count");
  const n = raw === null || raw === "" ? NaN : Number(raw);
  return Number.isFinite(n) && n >= 0 ? n : itemsLength + page.offset;
}

/** Limit for a refetch preserving a loaded window: at least INITIAL_PAGE_LIMIT, at most the server cap.
 * parsePagination 400s (no clamp) above paginationMaxLimit, so asking for a larger loaded count would turn
 * a refresh into "unavailable". */
export function refetchLimit(loadedCount: number): number {
  return Math.min(MAX_PAGE_LIMIT, Math.max(INITIAL_PAGE_LIMIT, loadedCount));
}

/** One "fetch this page" call: null on any network or server failure, a PagedResult otherwise. */
export type PageFetcher<T> = (page: Page) => Promise<PagedResult<T> | null>;

/** Fetches every row beyond `items` (already `items.length` of `total`) in `chunkLimit` chunks, so a filter
 * never silently runs over partial data. Returns the complete array, or null if any chunk fails, never a
 * half-backfilled array. Callers keep what they displayed and offer a retry. */
export async function ensureFullyLoaded<T>(
  items: readonly T[],
  total: number,
  fetchPage: PageFetcher<T>,
  chunkLimit: number = MAX_PAGE_LIMIT,
): Promise<T[] | null> {
  let loaded = items.slice();
  while (loaded.length < total) {
    const page = await fetchPage({ limit: chunkLimit, offset: loaded.length });
    if (page === null) return null;
    if (page.items.length === 0) break; // guards against a stuck total (e.g. concurrent deletes)
    loaded = loaded.concat(page.items);
  }
  return loaded;
}

/** Outcome of resolveDetailMiss: `found`, `miss` (id absent once everything is loaded) or `error` (backfill
 * failed). `found` and `miss` carry the fully-loaded array to replace the caller's window. */
export type DetailMissResolution<T> =
  | { kind: "found"; item: T; items: T[] }
  | { kind: "miss"; items: T[] }
  | { kind: "error" };

/** Resolves a detail lookup that missed the loaded window by backfilling via ensureFullyLoaded and
 * re-looking-up, so a deep link past the first page still resolves. Callers look in their own array first:
 * this always pays for a full backfill. */
export async function resolveDetailMiss<T>(
  id: string,
  items: readonly T[],
  total: number,
  getId: (item: T) => string,
  fetchPage: PageFetcher<T>,
): Promise<DetailMissResolution<T>> {
  const result = await ensureFullyLoaded(items, total, fetchPage);
  if (result === null) return { kind: "error" };
  const found = result.find((item) => getId(item) === id);
  if (!found) return { kind: "miss", items: result };
  return { kind: "found", item: found, items: result };
}
