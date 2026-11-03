import type { PartFormState } from "./state.svelte";
import { DEFAULT_FETCH_TIMEOUT_MS } from "./fetchTimeouts";

/** Path called through the server-side proxy: GET/HEAD only, no trailing slash (the proxy rejects those
 * tree-wide). A fixed literal, never built from user input. */
export const SPOOLMAN_SPOOL_PATH = "api/spoolman/api/v1/spool";

/** Spoolman API v1 spool shape (subset used). Fields Spoolman can omit are optional so a partial record
 * falls back instead of throwing. Per-spool `price` and `initial_weight` take priority over the filament
 * preset's. */
export interface SpoolDto {
  id: number;
  remaining_weight?: number;
  price?: number;
  initial_weight?: number;
  archived?: boolean;
  location?: string;
  comment?: string;
  filament: {
    id?: number;
    color_hex?: string;
    name?: string;
    material?: string;
    density: number;
    weight?: number;
    price?: number;
    vendor?: { name?: string };
  };
}

export type SpoolOverride = NonNullable<PartFormState["spoolOverride"]>;

/** Runtime guard for one fetched element: a malformed record (missing id or filament) must not TypeError
 * the whole picker. */
export function isValidSpool(s: unknown): s is SpoolDto {
  if (typeof s !== "object" || s === null) return false;
  const r = s as Record<string, unknown>;
  return typeof r.id === "number" && typeof r.filament === "object" && r.filament !== null;
}

/** "<vendor> <name-or-material> #<id>"; the id disambiguates spools of one filament and guarantees a
 * non-empty label. */
export function spoolLabel(s: SpoolDto): string {
  const f = s.filament;
  return [f.vendor?.name, f.name ?? f.material, `#${s.id}`].filter(Boolean).join(" ");
}

/** $/kg for a spool: the spool's own `price`/`initial_weight` win over the filament preset's (more accurate
 * for that physical spool). Returns null (never 0) when neither yields a strictly positive price and
 * weight, so the caller leaves the price alone. */
export function spoolPricePerKg(s: SpoolDto): number | null {
  const price = s.price ?? s.filament.price;
  const weightG = s.initial_weight ?? s.filament.weight;
  if (!price || price <= 0 || !weightG || weightG <= 0) return null;
  const perKg = price / (weightG / 1000);
  return Number.isFinite(perKg) && perKg > 0 ? perKg : null;
}

/** Builds the part.spoolOverride a picked spool writes. pricePerKg is cent-rounded once so it matches
 * part.pricePerKg exactly. remainingG is null (unknown), not 0 (confirmed empty), when Spoolman doesn't
 * report it, so low-stock never fires on missing data. */
export function spoolOverrideFrom(s: SpoolDto): SpoolOverride {
  const perKg = spoolPricePerKg(s);
  return {
    label: spoolLabel(s),
    pricePerKg: perKg === null ? null : Math.round(perKg * 100) / 100,
    density: s.filament.density,
    remainingG: s.remaining_weight ?? null,
    spoolId: s.id,
  };
}

/** True when the job's filament need exceeds what's left on the spool. Never fires for `remainingG ===
 * null` or `neededG` of 0. */
export function isLowStock(remainingG: number | null, neededG: number): boolean {
  return remainingG !== null && neededG > 0 && remainingG < neededG;
}

// ---------------------------------------------------------------------------
// Spoolman inventory (Filament tab). Reads need the "filament" grant; writes are admin-only and the server
// proxy (server/spoolman_guard.go) allows only POST /spool and PATCH /spool/{id} with the fields below.
// ---------------------------------------------------------------------------

const SPOOLMAN_API = "api/spoolman/api/v1";

/** Spoolman's list hides archived spools unless asked; the inventory fetches all and filters client-side so
 * the Show archived toggle needs no second request. */
export const SPOOLMAN_INVENTORY_URL = `${SPOOLMAN_API}/spool?allow_archived=true`;
export const SPOOLMAN_FILAMENT_URL = `${SPOOLMAN_API}/filament`;

/** Spoolman filament shape (subset used by the "Add spool" picker). */
export interface FilamentDto {
  id: number;
  name?: string;
  material?: string;
  color_hex?: string;
  weight?: number;
  price?: number;
  vendor?: { name?: string };
}

export function isValidFilament(f: unknown): f is FilamentDto {
  return typeof f === "object" && f !== null && typeof (f as Record<string, unknown>).id === "number";
}

/** "<vendor> <name> (<material>)" for the filament picker. */
export function filamentLabel(f: FilamentDto): string {
  const name = [f.vendor?.name, f.name ?? f.material].filter(Boolean).join(" ") || `Filament #${f.id}`;
  return f.material && f.name ? `${name} (${f.material})` : name;
}

/** Keeps rows matching `query` (case-insensitive, across id, vendor, name, material, colour, location and
 * comment) and hides archived rows unless `showArchived`. */
export function filterSpools(spools: SpoolDto[], query: string, showArchived: boolean): SpoolDto[] {
  const q = query.trim().toLowerCase();
  return spools.filter((s) => {
    if (!showArchived && s.archived === true) return false;
    if (q === "") return true;
    const f = s.filament;
    const hay = [`#${s.id}`, String(s.id), f.vendor?.name, f.name, f.material, f.color_hex, colorName(f.color_hex), s.location, s.comment]
      .filter(Boolean)
      .join(" ")
      .toLowerCase();
    return hay.includes(q);
  });
}

const BASIC_COLORS: [string, [number, number, number]][] = [
  ["Black", [0, 0, 0]],
  ["White", [255, 255, 255]],
  ["Gray", [128, 128, 128]],
  ["Silver", [192, 192, 192]],
  ["Red", [220, 30, 30]],
  ["Orange", [245, 130, 20]],
  ["Yellow", [250, 220, 30]],
  ["Green", [40, 160, 60]],
  ["Teal", [0, 150, 150]],
  ["Blue", [30, 80, 220]],
  ["Navy", [20, 30, 100]],
  ["Purple", [130, 50, 170]],
  ["Pink", [240, 130, 180]],
  ["Brown", [120, 70, 30]],
  ["Beige", [220, 200, 160]],
];

/** Normalises Spoolman's "RRGGBB" (no "#") to "#RRGGBB", or null when it isn't a 6-digit hex. */
export function normalizeHex(hex: string | undefined): string | null {
  if (!hex) return null;
  const m = /^#?([0-9a-fA-F]{6})/.exec(hex.trim());
  return m ? `#${m[1]!.toUpperCase()}` : null;
}

/** Nearest basic colour name for a hex value, or "" when the hex is unusable. */
export function colorName(hex: string | undefined): string {
  const norm = normalizeHex(hex);
  if (!norm) return "";
  const rgb = [1, 3, 5].map((i) => parseInt(norm.slice(i, i + 2), 16));
  let best = "";
  let bestDist = Infinity;
  for (const [name, c] of BASIC_COLORS) {
    const d = (c[0] - rgb[0]!) ** 2 + (c[1] - rgb[1]!) ** 2 + (c[2] - rgb[2]!) ** 2;
    if (d < bestDist) {
      bestDist = d;
      best = name;
    }
  }
  return best;
}

/** "Red (#DC1E1E)", or "No colour" when Spoolman has none. */
export function colorText(hex: string | undefined): string {
  const norm = normalizeHex(hex);
  return norm ? `${colorName(hex)} (${norm})` : "No colour";
}

export type SpoolWriteResult<T> = { ok: true; data: T } | { ok: false; error: string };

async function spoolWrite<T>(url: string, method: "POST" | "PATCH", body: object): Promise<SpoolWriteResult<T>> {
  try {
    const res = await fetch(url, {
      method,
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
      signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS),
    });
    if (!res.ok) {
      if (res.status === 403) return { ok: false, error: "Only an admin can change Spoolman inventory." };
      let detail = "";
      try {
        const b = (await res.json()) as { errors?: unknown; error?: unknown };
        detail = Array.isArray(b.errors) ? b.errors.filter((e) => typeof e === "string").join(" ") : typeof b.error === "string" ? b.error : "";
      } catch {
        // not JSON
      }
      return { ok: false, error: detail || `Spoolman didn't accept the change (HTTP ${res.status}).` };
    }
    return { ok: true, data: (await res.json()) as T };
  } catch {
    return { ok: false, error: "Couldn't reach Spoolman. Check the connection and try again." };
  }
}

/** Fields POST /api/v1/spool accepts through the proxy. */
export interface NewSpoolBody {
  filament_id: number;
  initial_weight?: number;
  remaining_weight?: number;
  price?: number;
  location?: string;
}

/** Fields PATCH /api/v1/spool/{id} accepts through the proxy. */
export interface SpoolPatchBody {
  remaining_weight?: number;
  /** null clears the price: Spoolman keeps the old value when the key is omitted. */
  price?: number | null;
  location?: string | null;
  archived?: boolean;
}

export function createSpool(body: NewSpoolBody): Promise<SpoolWriteResult<SpoolDto>> {
  return spoolWrite<SpoolDto>(`${SPOOLMAN_API}/spool`, "POST", body);
}

export function patchSpool(id: number, body: SpoolPatchBody): Promise<SpoolWriteResult<SpoolDto>> {
  return spoolWrite<SpoolDto>(`${SPOOLMAN_API}/spool/${id}`, "PATCH", body);
}
