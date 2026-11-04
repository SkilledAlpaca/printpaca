import { errorsFrom } from "./clientsApi";
import { DEFAULT_FETCH_TIMEOUT_MS, IMAGE_UPLOAD_TIMEOUT_MS } from "./fetchTimeouts";

/** One selectable item variant (e.g. a color); mirrors server/items.go's ItemOption. `id` and `imageFile`
 * are server-owned: a new option is sent without an id, and imageFile is set by the option photo endpoints. */
export interface ItemOption {
  id: string;
  name: string;
  deltaCents: number;
  imageFile: string;
}

/** One choice an item offers (e.g. "Color"); mirrors server/items.go's ItemOptionGroup. Array order is
 * display order. */
export interface ItemOptionGroup {
  id: string;
  label: string;
  required: boolean;
  options: ItemOption[];
}

/** An option group as sent in an item body: ids are optional (absent for new rows), imageFile is ignored. */
export interface ItemOptionGroupInput {
  id?: string;
  label: string;
  required: boolean;
  options: { id?: string; name: string; deltaCents: number }[];
}

/** The JSON shape of every /api/items endpoint (server/items.go's Item). */
export interface Item {
  id: string;
  name: string;
  description: string;
  priceCents: number;
  optionGroups: ItemOptionGroup[];
  imageFile: string;
  public: boolean;
  stockMode: "ready_to_print" | "in_stock";
  stockQty: number;
  /** Only meaningful when stockMode is "in_stock": lets a public order exceed stockQty up to the gallery's
   * global max. Stored as given for "ready_to_print" but inert there. */
  allowBackorder: boolean;
  sortOrder: number;
  /** The item's category id, "" for none (D17). Managed in Shop items, listed by /api/categories. */
  categoryId: string;
  /** Featured items come first within the selected shop view. */
  featured: boolean;
  createdAt: string;
  updatedAt: string;
  /** Who created/last wrote this item (server/items.go). "" when unattributed. */
  createdBy: string;
  updatedBy: string;
  /** Counts every write to the item (server/items.go's Item.Version). Send it back on a PUT so a save from a
   * stale form is rejected (ConflictError) instead of silently overwriting a newer write. */
  version?: number;
}

/** The POST/PUT body: every client-writable field. id, imageFile, timestamps and attribution are
 * server-assigned (imageFile is owned by uploadItemImage). */
export type ItemInput = Omit<Item, "id" | "imageFile" | "createdAt" | "updatedAt" | "createdBy" | "updatedBy" | "optionGroups" | "version" | "categoryId" | "featured"> & {
  optionGroups: ItemOptionGroupInput[];
  /** Absent keeps the stored value on a PUT (the list's one-field writes leave it out); "" clears the category. */
  categoryId?: string;
  featured?: boolean;
  /** The Item.version the form was loaded with; omit to skip the stale-save check. */
  version?: number;
};

/** The server answered 409: the item changed since the form loaded it (another tab or user, a photo upload,
 * or a stock change). The UI offers Reload or Overwrite. */
export class ConflictError extends Error {
  constructor() {
    super("This was changed in another tab or by another user.");
    this.name = "ConflictError";
  }
}

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
  if (res.status === 409) throw new ConflictError();
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

/** Posts the blob as an option's photo (server/items.go's handleOptionImageUpload); same body and limits as
 * uploadItemImage. Resolves to the updated item. */
export async function uploadOptionImage(itemId: string, optionId: string, blob: Blob): Promise<Item> {
  const contentType = acceptedImageContentTypes.has(blob.type) ? blob.type : "image/jpeg";
  const res = await fetch(`api/items/${encodeURIComponent(itemId)}/options/${encodeURIComponent(optionId)}/image`, {
    method: "POST",
    headers: { "Content-Type": contentType },
    body: blob,
    signal: AbortSignal.timeout(IMAGE_UPLOAD_TIMEOUT_MS),
  });
  if (!res.ok) throw await errorsFrom(res);
  return (await res.json()) as Item;
}

/** Removes an option's photo. Resolves to the updated item. */
export async function deleteOptionImage(itemId: string, optionId: string): Promise<Item> {
  const res = await fetch(`api/items/${encodeURIComponent(itemId)}/options/${encodeURIComponent(optionId)}/image`, {
    method: "DELETE",
    signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS),
  });
  if (!res.ok) throw await errorsFrom(res);
  return (await res.json()) as Item;
}
