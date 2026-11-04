import type { EffectiveConfig } from "./engine/index";
import { DEFAULT_FETCH_TIMEOUT_MS } from "./fetchTimeouts";
import type { UsersModeConfig } from "./permissions";

/** The settings API's mergeable config shape: everything /api/config serves except env-derived and
 * per-request fields (integrations, moonraker, fluiddUrl, demoMode, integrationSources, user) and
 * build metadata (version, authEnabled). `users` is added on because EffectiveConfig doesn't declare it,
 * though server/config.go's Config.Users is a real config.yaml section round-tripped by /api/settings (see
 * permissions.ts's UsersModeConfig). */
export type ServerConfig = Omit<
  EffectiveConfig,
  | "integrations"
  | "version"
  | "authEnabled"
  | "moonraker"
  | "fluiddUrl"
  | "demoMode"
  | "integrationSources"
  | "user"
> & {
  users: UsersModeConfig;
};

export interface SettingsPayload {
  config: ServerConfig;
  builtin: ServerConfig;
  /** Set when config.yaml on disk is invalid; `config` then echoes the running config instead. */
  loadError?: string;
}

export async function loadSettings(): Promise<SettingsPayload | null> {
  try {
    const res = await fetch("api/settings", { signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS) });
    if (!res.ok) return null;
    return (await res.json()) as SettingsPayload;
  } catch {
    return null;
  }
}

export type SaveSettingsResult = { ok: true } | { ok: false; errors: string[] };

export async function saveSettings(config: ServerConfig): Promise<SaveSettingsResult> {
  let res: Response;
  try {
    res = await fetch("api/settings", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ config }),
      signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS),
    });
  } catch {
    return { ok: false, errors: ["Couldn't reach the server."] };
  }

  let body: unknown;
  try {
    body = await res.json();
  } catch {
    return { ok: false, errors: ["Couldn't reach the server."] };
  }

  if (res.ok) {
    return { ok: true };
  }

  const rawErrors = (body as { errors?: unknown } | null)?.errors;
  const errors = Array.isArray(rawErrors) ? rawErrors.filter((e): e is string => typeof e === "string") : [];
  return { ok: false, errors: errors.length > 0 ? errors : ["Couldn't reach the server."] };
}

/** Deep-clones a ServerConfig so `form` and `original` can be edited independently. */
export function cloneConfig(c: ServerConfig): ServerConfig {
  return structuredClone(c);
}

/** JSON.stringify comparison is stable because both sides come from the same construction paths (the server
 * response, cloneConfig of it, or edits layered on it), so key order agrees; it never compares
 * independently authored objects. */
export function configsEqual(a: ServerConfig, b: ServerConfig): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}

/** The section ids SettingsTab renders one card per; also scopes errorSection()'s output and the "jump to
 * this card" styling. */
const SECTION_IDS = new Set([
  "currency",
  "pricing",
  "timezone",
  "feeSchedules",
  "materials",
  "printers",
  "hardware",
  "energy",
  "presets",
  "defaults",
  "quote",
  "invoicing",
  "publicQuotes",
  "site",
  "shop",
  "customCss",
  "smtp",
  "ui",
  "integrationsUrls",
  "users",
]);

/** Maps a server error's leading path token ("pricing.marginPct: out of range" -> "pricing") to its
 * SettingsTab section so the failing card gets a danger border. Unmapped prefixes (decode errors, "config:
 * required") fall back to "general". */
export function errorSection(msg: string): string {
  const path = msg.split(":")[0] ?? "";
  const token = path.split(/[.[]/)[0] ?? "";
  return SECTION_IDS.has(token) ? token : "general";
}

/** First unused key of the form "base", "base-2", "base-3", ... for freshly added entries. */
export function nextKey(existing: string[], base: string): string {
  if (!existing.includes(base)) return base;
  let n = 2;
  while (existing.includes(`${base}-${n}`)) n++;
  return `${base}-${n}`;
}

/** A printer row with the starting values the Printers card's "Add printer" uses. */
export function newPrinter(existingIds: string[], name: string): ServerConfig["printers"][number] {
  const slug = name.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "") || "new-printer";
  return { id: nextKey(existingIds, slug), name, watts: 250, purchaseCost: 400, lifespanHours: 4000, filamentDiameterMm: 1.75 };
}
