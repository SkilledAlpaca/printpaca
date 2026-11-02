/** Pure helpers behind AttributionLine.svelte's "Created by X · Updated by Y" display, framework-free so
 * the rules are unit-testable. */

/** Extracts config.users.multiUser from a decoded /api/config or /api/settings body. Read defensively (like
 * permissions.ts's extractConfigUser) because EffectiveConfig doesn't declare it; anything but exactly
 * {multiUser: boolean} resolves to false. */
export function extractRealMultiUser(cfg: unknown): boolean {
  if (!cfg || typeof cfg !== "object") return false;
  const raw = (cfg as { users?: unknown }).users;
  if (!raw || typeof raw !== "object") return false;
  return (raw as Record<string, unknown>).multiUser === true;
}

/** Formats "Created by / Updated by" from createdBy/updatedBy ("" on pre-feature rows; quote_requests has
 * no createdBy). Omits empty sides and returns null when there is nothing to show, so callers can use it
 * as an #if guard. */
export function attributionText(createdBy: string, updatedBy: string): string | null {
  const parts: string[] = [];
  if (createdBy) parts.push(`Created by ${createdBy}`);
  if (updatedBy) parts.push(`Updated by ${updatedBy}`);
  return parts.length > 0 ? parts.join(" · ") : null;
}
