// The shop's hero image endpoints (server/shop.go). Both write config.yaml at once, outside the Settings save:
// the image file is owned by these endpoints, and a Settings save only carries the alt text.
import { DEFAULT_FETCH_TIMEOUT_MS } from "./fetchTimeouts";
import { errorsFrom } from "./clientsApi";

/** What both endpoints return: the stored image, or empty strings after a delete. */
export interface HeroImage {
  file: string;
  url: string;
  alt: string;
}

const HERO_UPLOAD_TIMEOUT_MS = 60_000;
const ACCEPTED_TYPES = new Set(["image/jpeg", "image/png", "image/webp"]);

/** Posts the image as the raw body with the alt text in the query (alt is required). A typeless blob goes as
 * image/jpeg, which the server's byte sniffing then checks. Rejects with the server's message. */
export async function uploadHeroImage(blob: Blob, alt: string): Promise<HeroImage> {
  const res = await fetch(`api/shop/hero-image?alt=${encodeURIComponent(alt)}`, {
    method: "POST",
    headers: { "Content-Type": ACCEPTED_TYPES.has(blob.type) ? blob.type : "image/jpeg" },
    body: blob,
    signal: AbortSignal.timeout(HERO_UPLOAD_TIMEOUT_MS),
  });
  if (!res.ok) throw await errorsFrom(res);
  return (await res.json()) as HeroImage;
}

/** Removes the hero image and its alt text. Removing when there is none succeeds. */
export async function deleteHeroImage(): Promise<HeroImage> {
  const res = await fetch("api/shop/hero-image", { method: "DELETE", signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS) });
  if (!res.ok) throw await errorsFrom(res);
  return (await res.json()) as HeroImage;
}
