// RequestsTab's storage-capacity banner. CONFIG_DIR/uploads has a hard cap (server/public.go's
// publicUploadsCapBytes, 2 GiB) past which a submission is accepted without its file ("dropped" file state,
// see classifyRequestFileState). That degradation is otherwise silent, so this surfaces the capacity from GET
// /api/requests/storage's {usedBytes, capBytes}.

type StorageBannerLevel = "warn" | "full";

export interface StorageBanner {
  level: StorageBannerLevel;
  text: string;
}

// 80%: early enough for the owner to delete old requests (which prunes their files) before uploads start
// being dropped.
const WARN_THRESHOLD = 0.8;

/** Pure threshold/message selection, separate from the fetch so it's testable. Returns null below the warn
 * threshold or when capBytes is non-positive (fail toward no banner, never a NaN or Infinity percentage). */
export function describeStorageBanner(usedBytes: number, capBytes: number): StorageBanner | null {
  if (capBytes <= 0) return null;
  const ratio = usedBytes / capBytes;
  if (ratio >= 1) {
    return {
      level: "full",
      text: "Upload storage is full; new visitor files are being dropped.",
    };
  }
  if (ratio >= WARN_THRESHOLD) {
    return {
      level: "warn",
      // Floored, not rounded: "80% full" must not show before the ratio actually reaches WARN_THRESHOLD.
      text: `Visitor upload storage is ${Math.floor(ratio * 100)}% full; delete old requests to free space.`,
    };
  }
  return null;
}
