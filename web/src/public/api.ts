import type { CurrencyConfig } from "../lib/engine/index";
import { DEFAULT_FETCH_TIMEOUT_MS } from "../lib/fetchTimeouts";

// Shapes match server/public.go's publicConfigResponse and publicMaterial field for field: deliberately
// narrow, never the full admin Config. Pricing is server-side (POST /api/public/quote/preview), so this
// config carries only what the page needs to render the picker and parse a dropped file.

interface PublicMaterial {
  name: string;
  /** g/cm³; converts a filament length or model volume to weight. */
  density: number;
}

export interface PublicConfig {
  /** email/phone/address mirror server/config.go's QuoteBranding fields; "" when unset. */
  branding: { businessName: string; logoUrl: string; email: string; phone: string; address: string };
  /** Where the shop footer's Staff sign-in link points: "/admin/" on the main port, site.adminUrl on the
   * public port, "" to hide the link (server/public.go's staffURLFunc). */
  staffUrl: string;
  introText: string;
  currency: CurrencyConfig;
  materials: PublicMaterial[];
  /** The public printer's filament diameter (mm), needed by resolveGcode's length-to-weight fallback when
   * metadata reports only length. */
  filamentDiameterMm: number;
  /** Mirrors server/public.go's publicMaxUploadBytes so the drop zone can reject or degrade an over-limit
   * file before uploading. */
  maxUploadBytes: number;
  /** Whether DEMO_MODE=1 (server/main.go): the submission is simulated (validated and priced, nothing
   * persisted), so the page shows a banner. */
  demoMode: boolean;
}

/** A plain fetch/json cast like web/src/lib/api.ts's loadConfig(): null on any network failure or non-2xx,
 * never throws. */
export async function loadPublicConfig(): Promise<PublicConfig | null> {
  try {
    const res = await fetch("/api/public/config", { signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS) });
    if (!res.ok) return null;
    return (await res.json()) as PublicConfig;
  } catch {
    return null;
  }
}

// Server validation errors are "<field>: <problem>" with raw wire field names, which would leak API internals
// ("weightG: must be 0-100000"); this map lets humanizePublicFieldError swap the prefix for the form's
// wording and keep the rest as written.
const PUBLIC_FIELD_LABELS: Record<string, string> = {
  weightG: "Model weight",
  timeSeconds: "Print time",
  quantity: "Quantity",
  material: "Material",
  item: "Item",
  note: "Note",
  name: "Name",
  email: "Email",
};

/** Maps one server validation error to customer-facing copy. `optionName` prefers the item's own option
 * label. An unrecognized identifier prefix gets a generic message rather than a raw field name; a string
 * without the "field: message" shape (no colon, or a non-identifier prefix) passes through as prose. Wire
 * strings are untouched. */
function sentence(s: string): string {
  const t = s.trim();
  if (t === "") return t;
  const c = t.charAt(0).toUpperCase() + t.slice(1);
  return /[.!?]$/.test(c) ? c : `${c}.`;
}

function humanizePublicFieldError(raw: string, optionLabel?: string): string {
  const idx = raw.indexOf(":");
  if (idx === -1) return raw; // no "field: message" shape at all -- already-friendly prose, leave it alone
  const field = raw.slice(0, idx);
  // A prefix with a space (not a bare identifier) is prose containing a colon, not a leaked field name.
  if (!/^[a-zA-Z][a-zA-Z0-9]*$/.test(field)) return raw;
  // The price error is a shop-side problem the customer can't fix by editing a field, so show the sentence alone.
  if (field === "price") return sentence(raw.slice(idx + 1));
  if (field === "optionName") return sentence(`${optionLabel || "Option"}${raw.slice(idx)}`);
  const label = PUBLIC_FIELD_LABELS[field];
  return label ? sentence(`${label}${raw.slice(idx)}`) : "Check the form and try again.";
}

function humanizePublicFieldErrors(errors: string[], optionLabel?: string): string[] {
  return errors.map((e) => humanizePublicFieldError(e, optionLabel));
}

export interface SubmitResult {
  ok: boolean;
  status: number;
  errors: string[];
  id?: string;
  priceCents?: number;
  /** Set on every successful submission (absent on failures). The storage-full path (server/public.go)
   * accepts the submission without the file and signals fileStored: false in the 201 body; true when the
   * file persisted or none was sent. */
  fileStored?: boolean;
}

// GET /api/public/items, POST /api/public/item-order

// Shapes match server/public.go's publicItem/publicItemStock field for field: a narrow wire shape, not the
// admin-only Item/ItemOption from lib/itemsApi.ts (CRUD concerns have no place in the public entry).

interface ItemOption {
  name: string;
  deltaCents: number;
}

export interface PublicItem {
  id: string;
  name: string;
  description: string;
  priceCents: number;
  optionLabel: string;
  options: ItemOption[];
  imageUrl: string;
  stock: { mode: "ready_to_print" | "in_stock"; qty?: number; allowBackorder: boolean };
}

/** Null on any network failure or non-2xx, never throws. A missing or malformed `items` resolves to []
 * (like listItems()); null means "couldn't ask", not "none". */
export async function loadPublicItems(): Promise<PublicItem[] | null> {
  try {
    const res = await fetch("/api/public/items", { signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS) });
    if (!res.ok) return null;
    const body = (await res.json()) as { items?: unknown };
    return Array.isArray(body.items) ? (body.items as PublicItem[]) : [];
  } catch {
    return null;
  }
}

/** POSTs the JSON item-order body (server/public.go's publicItemOrderRequest). Never throws: failure
 * resolves to ok:false. Unlike submitQuoteRequest there is no upload, so a client-side timeout (10s) is
 * safe. */
export async function submitItemOrder(
  body: {
    itemId: string;
    optionName: string;
    quantity: number;
    name: string;
    email: string;
    note: string;
  },
  optionLabel?: string,
): Promise<SubmitResult> {
  let res: Response;
  try {
    res = await fetch("/api/public/item-order", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
      signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS),
    });
  } catch {
    return { ok: false, status: 0, errors: ["Couldn't reach the server. Try again."] };
  }

  let parsed: unknown = null;
  try {
    parsed = await res.json();
  } catch {
    parsed = null;
  }

  if (res.ok) {
    const b = parsed as { id: string; priceCents: number } | null;
    return { ok: true, status: res.status, errors: [], id: b?.id, priceCents: b?.priceCents };
  }

  const errBody = parsed as { errors?: string[] } | null;
  const errors = errBody?.errors?.length
    ? humanizePublicFieldErrors(errBody.errors, optionLabel)
    : ["Something went wrong submitting the order."];
  return { ok: false, status: res.status, errors };
}

/** POSTs the multipart quote request (server/public.go's handlePublicQuoteRequest). Never throws; failure
 * or an unparseable body resolves to ok:false. Deliberately no AbortSignal.timeout: it aborts the whole
 * exchange, upload included, at a wall-clock deadline. The server already bounds the request (5-minute
 * body read deadline publicUploadReadDeadline, 32 MiB MaxBytesReader, upload semaphore) for slow
 * residential uplinks; a 10s client timeout made sliced g-code over ~400 KB unsendable and blamed the
 * visitor's connection. */
export async function submitQuoteRequest(form: FormData): Promise<SubmitResult> {
  let res: Response;
  try {
    res = await fetch("/api/public/quote-request", { method: "POST", body: form });
  } catch {
    return { ok: false, status: 0, errors: ["Network error. Check your connection and try again."] };
  }

  let body: unknown = null;
  try {
    body = await res.json();
  } catch {
    body = null;
  }

  if (res.ok) {
    const b = body as { id: string; priceCents: number; fileStored: boolean } | null;
    return { ok: true, status: res.status, errors: [], id: b?.id, priceCents: b?.priceCents, fileStored: b?.fileStored };
  }

  // A 413 comes from http.MaxBytesReader (publicMaxUploadBytes) and carries Go's plain-text page, not
  // {errors: [...]}; handle it explicitly so the visitor sees the actionable "drop the file" message instead
  // of "Something went wrong".
  if (res.status === 413) {
    return {
      ok: false,
      status: 413,
      errors: ["This file is too large for this request. Remove it and submit the estimate without it, or ask to send the file separately."],
    };
  }

  const errBody = body as { errors?: string[] } | null;
  const errors = errBody?.errors?.length
    ? humanizePublicFieldErrors(errBody.errors)
    : ["Something went wrong submitting the request."];
  return { ok: false, status: res.status, errors };
}

// POST /api/public/quote/preview: the visitor's live estimate, priced server-side from the same config the
// submission uses. lib/quotePreview.ts owns the debounce and staleness machine; this is the wire contract.

export interface QuotePreviewInput {
  material: string;
  quantity: number;
  weightG: number;
  timeSeconds: number;
}

export interface QuotePreviewResult {
  ok: boolean;
  status: number;
  errors: string[];
  priceCents?: number;
  shippingCents?: number;
}

/** Never throws: failure resolves to ok:false. A small JSON body with no upload, so a short client timeout
 * is safe. Its own per-IP rate bucket means a 429 gets its own copy, not submitErrors' hourly-cap message. */
export async function fetchQuotePreview(input: QuotePreviewInput): Promise<QuotePreviewResult> {
  let res: Response;
  try {
    res = await fetch("/api/public/quote/preview", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
      signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS),
    });
  } catch {
    return { ok: false, status: 0, errors: ["Network error. Check your connection and try again."] };
  }

  let body: unknown = null;
  try {
    body = await res.json();
  } catch {
    body = null;
  }

  if (res.ok) {
    const b = body as { priceCents?: number; shippingCents?: number } | null;
    if (typeof b?.priceCents === "number" && typeof b?.shippingCents === "number") {
      return { ok: true, status: res.status, errors: [], priceCents: b.priceCents, shippingCents: b.shippingCents };
    }
    return { ok: false, status: res.status, errors: ["Couldn't estimate this request."] };
  }

  if (res.status === 429) {
    return { ok: false, status: 429, errors: ["Too many estimate requests right now. Wait a moment and try again."] };
  }

  const errBody = body as { errors?: string[] } | null;
  const errors = errBody?.errors?.length
    ? humanizePublicFieldErrors(errBody.errors)
    : ["Couldn't estimate this request."];
  return { ok: false, status: res.status, errors };
}
