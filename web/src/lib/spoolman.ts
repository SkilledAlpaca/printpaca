import type { PartFormState } from "./state.svelte";

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
  filament: {
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
