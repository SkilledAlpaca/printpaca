/** Density (g/cm³) assumed when a filament's real density is unknown (PLA's approximate value).
 * FilamentTab, RequestsTab and the public quote page must agree on it, so all import this one constant. */
export const FALLBACK_DENSITY = 1.24;

/** A material's density when usable (finite and > 0), else FALLBACK_DENSITY. The public page and the admin
 * request verification share this so they derive the same weight from a filament length. */
export function densityOrFallback(d: number | null | undefined): number {
  return d != null && Number.isFinite(d) && d > 0 ? d : FALLBACK_DENSITY;
}
