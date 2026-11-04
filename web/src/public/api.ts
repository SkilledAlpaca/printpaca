import type { CurrencyConfig } from "../lib/engine/index";
import { DEFAULT_FETCH_TIMEOUT_MS } from "../lib/fetchTimeouts";
import type { ChoiceGroup } from "../lib/itemOptions";

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
  /** shop.maxOrderQty: the largest quantity one request or order may carry. Absent from a server that predates
   * the setting; the page then assumes 100. */
  maxOrderQty?: number;
  /** The storefront settings (server/public.go's publicShop). Absent from a server that predates them; read it
   * through `shopOf`, which fills the v1.1 behavior. All strings are plain text: render them as text. */
  shop?: PublicShop;
}

export interface PublicShop {
  customEstimates: boolean;
  gallery: boolean;
  acceptingOrders: boolean;
  /** The owner's message, or a default; meaningful when acceptingOrders is false. */
  pausedMessage: string;
  /** The view the shop opens on; "estimate" only when estimates are on. */
  defaultView: "gallery" | "estimate";
  heading: string;
  announcement: string;
  heroImage: { url: string; alt: string } | null;
  /** null for the theme accent. buttonText is the label color that passes contrast on the accent. */
  accent: { light: { accent: string; buttonText: string }; dark: { accent: string; buttonText: string } } | null;
  showStock: boolean;
  showFromPrice: boolean;
  itemSort: "manual" | "name" | "price";
  footerLinks: { label: string; url: string }[];
  showContact: boolean;
  shippingNote: string;
  turnaroundNote: string;
}

/** What a server without storefront settings behaves like: v1.1. */
export const DEFAULT_PUBLIC_SHOP: PublicShop = {
  customEstimates: true,
  gallery: true,
  acceptingOrders: true,
  pausedMessage: "We aren't taking new orders right now.",
  defaultView: "gallery",
  heading: "",
  announcement: "",
  heroImage: null,
  accent: null,
  showStock: true,
  showFromPrice: true,
  itemSort: "manual",
  footerLinks: [],
  showContact: true,
  shippingNote: "",
  turnaroundNote: "",
};

export function shopOf(cfg: PublicConfig | null): PublicShop {
  return cfg?.shop ? { ...DEFAULT_PUBLIC_SHOP, ...cfg.shop } : DEFAULT_PUBLIC_SHOP;
}

/** The vacation-mode message when a submit was refused because the shop isn't taking orders (the server's 503
 * with `paused: true`), else null. The message is the owner's text, so it is shown as written, not run
 * through the field-error humanizer (which would eat a colon in it). */
function pausedMessageOf(body: unknown): string | null {
  const b = body as { paused?: unknown; pausedMessage?: unknown } | null;
  return b?.paused === true && typeof b.pausedMessage === "string" && b.pausedMessage !== "" ? b.pausedMessage : null;
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

/** Maps one server validation error to customer-facing copy. A `selections` error already names the group
 * ("selections: choose Color"), so only the prefix is dropped. An unrecognized identifier prefix gets a generic message rather than a raw field name; a string
 * without the "field: message" shape (no colon, or a non-identifier prefix) passes through as prose. Wire
 * strings are untouched. */
function sentence(s: string): string {
  const t = s.trim();
  if (t === "") return t;
  const c = t.charAt(0).toUpperCase() + t.slice(1);
  return /[.!?]$/.test(c) ? c : `${c}.`;
}

function humanizePublicFieldError(raw: string): string {
  const idx = raw.indexOf(":");
  if (idx === -1) return raw; // no "field: message" shape at all -- already-friendly prose, leave it alone
  const field = raw.slice(0, idx);
  // A prefix with a space (not a bare identifier) is prose containing a colon, not a leaked field name.
  if (!/^[a-zA-Z][a-zA-Z0-9]*$/.test(field)) return raw;
  // The price error is a shop-side problem the customer can't fix by editing a field, so show the sentence alone.
  if (field === "price") return sentence(raw.slice(idx + 1));
  if (field === "selections") return sentence(raw.slice(idx + 1));
  const label = PUBLIC_FIELD_LABELS[field];
  return label ? sentence(`${label}${raw.slice(idx)}`) : "Check the form and try again.";
}

function humanizePublicFieldErrors(errors: string[]): string[] {
  return errors.map((e) => humanizePublicFieldError(e));
}

export interface SubmitResult {
  ok: boolean;
  status: number;
  errors: string[];
  id?: string;
  /** The short order reference to show the customer ("HL-1001"); falls back to `id` for a server that predates it. */
  reference?: string;
  priceCents?: number;
  /** True when an estimate was accepted without a print time: priceCents is 0 and means "to be confirmed". */
  needsPrintTime?: boolean;
  /** Set on every successful submission (absent on failures). The storage-full path (server/public.go)
   * accepts the submission without the file and signals fileStored: false in the 201 body; true when the
   * file persisted or none was sent. */
  fileStored?: boolean;
  /** Set when the shop is paused (accepting orders is off): the owner's message, also the only entry in `errors`. */
  paused?: string;
}

// GET /api/public/items, POST /api/public/item-order

// Shapes match server/public.go's publicItem/publicItemStock field for field: a narrow wire shape, not the
// admin-only Item/ItemOption from lib/itemsApi.ts (CRUD concerns have no place in the public entry).


export interface PublicItem {
  id: string;
  name: string;
  description: string;
  priceCents: number;
  optionGroups: ChoiceGroup[];
  imageUrl: string;
  stock: { mode: "ready_to_print" | "in_stock"; qty?: number; allowBackorder: boolean };
  /** Null for an item with no category or one in a hidden category. Absent from a server that predates categories. */
  category?: PublicCategory | null;
  /** Featured items come first; the server already sorted them. */
  featured?: boolean;
}

export interface PublicCategory {
  slug: string;
  name: string;
}

export interface PublicCatalog {
  items: PublicItem[];
  /** Visible categories that have at least one public item, in shop order. */
  categories: PublicCategory[];
}

/** Null on any network failure or non-2xx, never throws. A missing or malformed `items` resolves to []
 * (like listItems()); null means "couldn't ask", not "none". */
export async function loadPublicCatalog(): Promise<PublicCatalog | null> {
  try {
    const res = await fetch("/api/public/items", { signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS) });
    if (!res.ok) return null;
    const body = (await res.json()) as { items?: unknown; categories?: unknown };
    return {
      items: Array.isArray(body.items) ? (body.items as PublicItem[]) : [],
      categories: Array.isArray(body.categories) ? (body.categories as PublicCategory[]) : [],
    };
  } catch {
    return null;
  }
}

/** loadPublicCatalog's items alone. */
export async function loadPublicItems(): Promise<PublicItem[] | null> {
  return (await loadPublicCatalog())?.items ?? null;
}

/** POSTs the JSON item-order body (server/public.go's publicItemOrderRequest). Never throws: failure
 * resolves to ok:false. Unlike submitQuoteRequest there is no upload, so a client-side timeout (10s) is
 * safe. */
export async function submitItemOrder(
  body: {
    itemId: string;
    selections: { groupId: string; optionId: string }[];
    quantity: number;
    name: string;
    email: string;
    note: string;
  },
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
    const b = parsed as { id: string; reference?: string; priceCents: number } | null;
    return { ok: true, status: res.status, errors: [], id: b?.id, reference: b?.reference || b?.id, priceCents: b?.priceCents };
  }

  const paused = pausedMessageOf(parsed);
  if (paused) return { ok: false, status: res.status, errors: [paused], paused };
  const errBody = parsed as { errors?: string[] } | null;
  const errors = errBody?.errors?.length
    ? humanizePublicFieldErrors(errBody.errors)
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
    const b = body as { id: string; reference?: string; priceCents: number; needsPrintTime?: boolean; fileStored: boolean } | null;
    return {
      ok: true,
      status: res.status,
      errors: [],
      id: b?.id,
      reference: b?.reference || b?.id,
      priceCents: b?.priceCents,
      needsPrintTime: b?.needsPrintTime === true,
      fileStored: b?.fileStored,
    };
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

  const paused = pausedMessageOf(body);
  if (paused) return { ok: false, status: res.status, errors: [paused], paused };
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
  /** Set when the shop is paused; see SubmitResult.paused. */
  paused?: string;
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

  const paused = pausedMessageOf(body);
  if (paused) return { ok: false, status: res.status, errors: [paused], paused };
  const errBody = body as { errors?: string[] } | null;
  const errors = errBody?.errors?.length
    ? humanizePublicFieldErrors(errBody.errors)
    : ["Couldn't estimate this request."];
  return { ok: false, status: res.status, errors };
}
