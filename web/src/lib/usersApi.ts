import { errorsFrom } from "./clientsApi";
import { DEFAULT_FETCH_TIMEOUT_MS } from "./fetchTimeouts";

/** The JSON shape every /api/users response serves (server/users.go's User); never a password or hash. */
export interface UserRecord {
  id: string;
  username: string;
  permissions: string[];
  email: string;
  disabled: boolean;
  createdAt: string;
  updatedAt: string;
}

export type ListUsersResult =
  | { ok: true; users: UserRecord[] }
  // The 409 every /api/users* endpoint answers while users.multiUser is off (server/users.go's
  // requireMultiUser): not an error, so UsersSettings.svelte shows its mode-toggle-only view.
  | { ok: false; multiUserOff: true }
  | { ok: false; multiUserOff: false; error: string };

export async function listUsers(): Promise<ListUsersResult> {
  try {
    const res = await fetch("api/users", { signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS) });
    if (res.status === 409) return { ok: false, multiUserOff: true };
    if (!res.ok) return { ok: false, multiUserOff: false, error: (await errorsFrom(res)).message };
    const body = (await res.json()) as { users?: unknown };
    return { ok: true, users: Array.isArray(body.users) ? (body.users as UserRecord[]) : [] };
  } catch {
    return { ok: false, multiUserOff: false, error: "Couldn't reach the server." };
  }
}

export async function createUser(
  username: string,
  password: string,
  permissions: string[],
  email = "",
): Promise<UserRecord> {
  const res = await fetch("api/users", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(createUserBody(username, password, permissions, email)),
    signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS),
  });
  if (!res.ok) throw await errorsFrom(res);
  return (await res.json()) as UserRecord;
}

/** POST /api/users' body: email is sent only when set. */
export function createUserBody(username: string, password: string, permissions: string[], email: string) {
  const trimmed = email.trim();
  return trimmed === "" ? { username, password, permissions } : { username, password, permissions, email: trimmed };
}

/** PUT /api/users/{id}: every field optional server-side (username is immutable), so callers pass only what
 * changes. */
export async function updateUser(id: string, patch: { permissions?: string[]; disabled?: boolean; email?: string }): Promise<UserRecord> {
  const res = await fetch(`api/users/${encodeURIComponent(id)}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(patch),
    signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS),
  });
  if (!res.ok) throw await errorsFrom(res);
  return (await res.json()) as UserRecord;
}

export async function resetUserPassword(id: string, newPassword: string): Promise<void> {
  const res = await fetch(`api/users/${encodeURIComponent(id)}/password`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ new: newPassword }),
    signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS),
  });
  if (!res.ok) throw await errorsFrom(res);
}

export async function deleteUser(id: string): Promise<void> {
  const res = await fetch(`api/users/${encodeURIComponent(id)}`, {
    method: "DELETE",
    signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS),
  });
  if (!res.ok) throw await errorsFrom(res);
}
