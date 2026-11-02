/** Flags identical rapid submissions that are otherwise indistinguishable in the Requests inbox. A possible
 * duplicate: an earlier request (by createdAt) with the same email (case-insensitive), kind ("quote" or
 * "item"), itemId (kind "item"), G-code filename (kind "quote") and quantity, within 60 minutes.
 * kind/itemId are in the key so a gallery order never collides with a no-gcode quote request, nor two
 * different items at one quantity. Client-side only, a UI hint. */

export interface DuplicateCandidate {
  id: string;
  email: string;
  kind: "quote" | "item";
  itemId: string;
  gcodeFilename: string;
  quantity: number;
  createdAt: string;
}

const DUPLICATE_WINDOW_MS = 60 * 60 * 1000;

/** Ids of requests with an earlier match inside the 60-minute window. Requests with an identical createdAt:
 * the one earlier in the array is the later arrival (RequestsTab sorts newest first). Bucketed by the join
 * key instead of all pairs (O(n^2) on every $derived re-run; the list is unpaginated). Within a bucket,
 * sorted by earliness, one adjacent-pair scan suffices: the preceding entry has the smallest gap, so if it
 * is outside the window everything earlier is too. */
export function findDuplicateRequestIds(requests: DuplicateCandidate[]): Set<string> {
  // Paired with its parsed timestamp and index up front, avoiding re-indexing `requests` (which
  // noUncheckedIndexedAccess flags).
  const candidates = requests.map((r, i) => ({ r, i, t: new Date(r.createdAt).getTime() }));

  const buckets = new Map<string, typeof candidates>();
  for (const c of candidates) {
    if (Number.isNaN(c.t)) continue; // unparsable createdAt: never a match on either side, same as before
    // "::" separator, not a space: gcodeFilename can contain spaces and a space join could collide different
    // (email, filename) pairs.
    const key = `${c.r.email.toLowerCase()}::${c.r.kind}::${c.r.itemId}::${c.r.gcodeFilename}::${c.r.quantity}`;
    const bucket = buckets.get(key);
    if (bucket) bucket.push(c);
    else buckets.set(key, [c]);
  }

  const dup = new Set<string>();
  for (const bucket of buckets.values()) {
    if (bucket.length < 2) continue;
    // Ascending by earliness: timestamp, ties broken by the higher original index.
    bucket.sort((x, y) => x.t - y.t || y.i - x.i);
    for (let k = 1; k < bucket.length; k++) {
      const prev = bucket[k - 1]!;
      const cur = bucket[k]!;
      if (cur.t - prev.t <= DUPLICATE_WINDOW_MS) dup.add(cur.r.id);
    }
  }
  return dup;
}
