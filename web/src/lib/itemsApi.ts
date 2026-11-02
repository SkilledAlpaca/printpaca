import { errorsFrom } from "./clientsApi";
import { DEFAULT_FETCH_TIMEOUT_MS, IMAGE_UPLOAD_TIMEOUT_MS } from "./fetchTimeouts";

/** One selectable item variant (e.g. a color); mirrors server/items.go's ItemOption. */
export interface ItemOption {
  name: string;
  deltaCents: number;
}

/** The JSON shape of every /api/items endpoint (server/items.go's Item). */
export interface Item {
  id: string;
  name: string;
  description: string;
  priceCents: number;
  optionLabel: string;
  options: ItemOption[];
  imageFile: string;
  public: boolean;
  stockMode: "ready_to_print" | "in_stock";
  stockQty: number;
  /** Only meaningful when stockMode is "in_stock": lets a public order exceed stockQty up to the gallery's
   * global max. Stored as given for "ready_to_print" but inert there. */
  allowBackorder: boolean;
  sortOrder: number;
  createdAt: string;
  updatedAt: string;
  /** Who created/last wrote this item (server/items.go). "" when unattributed. */
  createdBy: string;
  updatedBy: string;
}

/** The POST/PUT body: every client-writable field. id, imageFile, timestamps and attribution are
 * server-assigned (imageFile is owned by uploadItemImage). */
export type ItemInput = Omit<Item, "id" | "imageFile" | "createdAt" | "updatedAt" | "createdBy" | "updatedBy">;

/** A network failure returns null so the page can show an offline note. */
export async function listItems(): Promise<Item[] | null> {
  try {
    const res = await fetch("api/items", { signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS) });
    if (!res.ok) return null;
    const body = (await res.json()) as { items?: unknown };
    return Array.isArray(body.items) ? (body.items as Item[]) : [];
  } catch {
    return null;
  }
}

export async function createItem(input: ItemInput): Promise<Item> {
  const res = await fetch("api/items", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
    signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS),
  });
  if (!res.ok) throw await errorsFrom(res);
  return (await res.json()) as Item;
}

export async function updateItem(id: string, input: ItemInput): Promise<Item> {
  const res = await fetch(`api/items/${encodeURIComponent(id)}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
    signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS),
  });
  if (!res.ok) throw await errorsFrom(res);
  return (await res.json()) as Item;
}

export async function deleteItem(id: string): Promise<void> {
  const res = await fetch(`api/items/${encodeURIComponent(id)}`, {
    method: "DELETE",
    signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS),
  });
  if (!res.ok) throw await errorsFrom(res);
}

// Content-Types csrfGuard (server/auth.go) accepts for an image upload.
const acceptedImageContentTypes = new Set(["image/jpeg", "image/png", "image/webp"]);

/** Posts the blob as the raw body (not multipart), matching server/items.go's handleImageUpload. Longer
 * timeout: even after downscaleImage it can upload up to the 5 MiB cap (itemImageMaxBytes). */
export async function uploadItemImage(id: string, blob: Blob): Promise<Item> {
  // csrfGuard 415s Content-Types outside the allowlist, and a typeless File leaves blob.type ""; fall back to
  // image/jpeg so the server's byte-sniffing handler decides.
  const contentType = acceptedImageContentTypes.has(blob.type) ? blob.type : "image/jpeg";
  const res = await fetch(`api/items/${encodeURIComponent(id)}/image`, {
    method: "POST",
    headers: { "Content-Type": contentType },
    body: blob,
    signal: AbortSignal.timeout(IMAGE_UPLOAD_TIMEOUT_MS),
  });
  if (!res.ok) throw await errorsFrom(res);
  return (await res.json()) as Item;
}
