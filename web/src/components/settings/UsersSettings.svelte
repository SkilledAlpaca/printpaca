<script lang="ts">
  import type { ServerConfig } from "../../lib/settings";
  import { GRANTABLE_TAB_KEYS, GRANTABLE_TAB_LABELS, type GrantableTabKey } from "../../lib/permissions";
  import { createUser, deleteUser, listUsers, resetUserPassword, updateUser, type UserRecord } from "../../lib/usersApi";
  import { appConfig } from "../../lib/state.svelte";
  import { demoLocal } from "../../lib/demoLocal.svelte";

  // Admin-only card (SettingsTab mounts the settings form only for the admin identity). `original`, not
  // `form`, gates the employee list and add-user UI: `form.users.multiUser` is the still-unsaved draft, but
  // GET /api/users answers 409 until the flip is saved, so the card must react to the persisted value (see
  // the $effect).
  let {
    form,
    original,
    hasError,
  }: {
    form: ServerConfig;
    original: ServerConfig;
    hasError: (section: string) => boolean;
  } = $props();

  type LoadState =
    | { status: "loading" }
    // The 409 every /api/users* endpoint answers while users.multiUser is off (server/users.go's
    // requireMultiUser): not an error, so the card shows only the mode toggle plus a hint.
    | { status: "off" }
    | { status: "error"; message: string }
    | { status: "ready"; users: UserRecord[] };

  let usersState = $state<LoadState>({ status: "loading" });

  // The demo banner's "User mode" toggle is presentation only. The demo config seeds users.multiUser: true
  // (server/demoseed.go), so GET /api/users succeeds regardless; demoSingle forces the off-state presentation
  // without touching usersState or refetching. Outside the demo it is always false.
  const demoSingle = $derived(appConfig.current?.demoMode === true && demoLocal.userMode === "single");

  async function refreshUsers() {
    usersState = { status: "loading" };
    const result = await listUsers();
    if (result.ok) {
      usersState = { status: "ready", users: result.users };
    } else if (result.multiUserOff) {
      usersState = { status: "off" };
    } else {
      usersState = { status: "error", message: result.error };
    }
  }

  // Re-fetches whenever the persisted mode flips; also the first run (mount), since reading
  // original.users.multiUser establishes the dependency.
  $effect(() => {
    if (original.users.multiUser) {
      refreshUsers();
    } else {
      // The list endpoint answers 409 while multi-user mode is off, so skip the call.
      usersState = { status: "off" };
    }
  });

  let rowSaving = $state<Record<string, boolean>>({});
  let rowError = $state<Record<string, string>>({});

  async function applyUpdate(id: string, patch: { permissions?: string[]; disabled?: boolean; email?: string }): Promise<boolean> {
    rowSaving = { ...rowSaving, [id]: true };
    rowError = { ...rowError, [id]: "" };
    try {
      const updated = await updateUser(id, patch);
      if (usersState.status === "ready") {
        usersState = { status: "ready", users: usersState.users.map((u) => (u.id === id ? updated : u)) };
      }
      return true;
    } catch (err) {
      rowError = { ...rowError, [id]: err instanceof Error ? err.message : "Couldn't update the user." };
      return false;
    } finally {
      rowSaving = { ...rowSaving, [id]: false };
    }
  }

  function togglePermission(u: UserRecord, key: GrantableTabKey, checked: boolean) {
    const next = checked ? [...u.permissions, key] : u.permissions.filter((p) => p !== key);
    void applyUpdate(u.id, { permissions: next });
  }

  // The server folds only ASCII letters, so compare the same way (toLowerCase would also fold characters such
  // as the Kelvin sign onto ASCII).
  async function changeEmail(u: UserRecord, input: HTMLInputElement) {
    const value = input.value.trim();
    if (value.replace(/[A-Z]/g, (c) => c.toLowerCase()) === u.email) return;
    // A failed save keeps the row error but restores the stored address, so the box never shows an email the
    // server rejected.
    if (!(await applyUpdate(u.id, { email: value }))) input.value = u.email;
  }

  function toggleDisabled(u: UserRecord) {
    void applyUpdate(u.id, { disabled: !u.disabled });
  }

  let resetOpenId = $state<string | null>(null);
  let resetValue = $state("");
  let resetSubmitting = $state(false);
  let resetError = $state("");

  function openReset(id: string) {
    resetOpenId = id;
    resetValue = "";
    resetError = "";
  }
  function cancelReset() {
    resetOpenId = null;
  }
  async function submitReset(e: SubmitEvent, u: UserRecord) {
    e.preventDefault();
    if (resetValue.length < 8) {
      resetError = "Must be at least 8 characters.";
      return;
    }
    resetSubmitting = true;
    resetError = "";
    try {
      await resetUserPassword(u.id, resetValue);
      resetOpenId = null;
      resetValue = "";
    } catch (err) {
      resetError = err instanceof Error ? err.message : "Couldn't reset the password.";
    } finally {
      resetSubmitting = false;
    }
  }

  let confirmDeleteId = $state<string | null>(null);
  function askDelete(id: string) {
    confirmDeleteId = id;
  }
  function cancelDeleteRow() {
    confirmDeleteId = null;
  }
  async function remove(u: UserRecord) {
    confirmDeleteId = null;
    try {
      await deleteUser(u.id);
      if (usersState.status === "ready") {
        usersState = { status: "ready", users: usersState.users.filter((x) => x.id !== u.id) };
      }
    } catch (err) {
      rowError = { ...rowError, [u.id]: err instanceof Error ? err.message : "Couldn't delete the user." };
    }
  }

  // A plain attribute-expression binding (pattern={USERNAME_PATTERN}), not an inline "...{1,64}" string:
  // Svelte's parser treats a brace inside a quoted attribute as a mustache expression. The hyphen is escaped
  // (\\-) although last in the class: Chromium compiles `pattern` with the regex `v` flag, which rejects an
  // unescaped hyphen there and silently discards the whole attribute and all client-side username validation.
  // Mirrors server/users.go's usernamePattern.
  const USERNAME_PATTERN = "[a-z0-9._\\-]{1,64}";

  let newUsername = $state("");
  let newPassword = $state("");
  let newEmail = $state("");
  let newPermissions = $state<Set<GrantableTabKey>>(new Set());
  let addError = $state("");
  let adding = $state(false);

  function toggleNewPermission(key: GrantableTabKey, checked: boolean) {
    const next = new Set(newPermissions);
    if (checked) next.add(key);
    else next.delete(key);
    newPermissions = next;
  }

  async function addUser(e: SubmitEvent) {
    e.preventDefault();
    if (adding) return;
    adding = true;
    addError = "";
    try {
      const created = await createUser(newUsername.trim(), newPassword, [...newPermissions], newEmail);
      const existing = usersState.status === "ready" ? usersState.users : [];
      usersState = { status: "ready", users: [...existing, created].sort((a, b) => a.username.localeCompare(b.username)) };
      newUsername = "";
      newPassword = "";
      newEmail = "";
      newPermissions = new Set();
    } catch (err) {
      addError = err instanceof Error ? err.message : "Couldn't create the user.";
    } finally {
      adding = false;
    }
  }
</script>

<div id="section-users" class="card roomy" class:danger-border={hasError("users")}>
  <h2>Users</h2>
  <p class="hint">
    Multi-user mode lets employees sign in with their own username and password, each limited to the tabs you grant
    them. Enabling it requires ADMIN_PASSWORD to be set on the server.
  </p>
  <label class="toggle">
    <input type="checkbox" bind:checked={form.users.multiUser} />
    <span>Multi-user mode</span>
  </label>
  <p class="hint">
    Turning this off doesn't delete employee accounts. Their logins are refused and their sessions end, but the
    accounts and permissions return as they were if you turn it back on. Employees whose email is set also can't get in
    through Cloudflare Access while it's off; only the admin can.
  </p>
  <div class="field">
    <label for="access-admin-email">Your Cloudflare Access email</label>
    <input
      id="access-admin-email"
      type="email"
      bind:value={form.users.accessAdminEmail}
      autocomplete="email"
      placeholder="owner@example.com"
    />
    <small class="field-hint">
      Only used when you reach printpaca through Cloudflare Access with multi-user mode on. The address you sign in to
      Cloudflare Access with signs in here as the admin. Leave it empty to sign in with the admin password instead.
    </small>
  </div>

  {#if demoSingle}
    <p class="hint">Switch the demo banner's User mode to "multi" to see example employee accounts here.</p>
  {:else if usersState.status === "loading"}
    <p class="status-line">Loading employee accounts…</p>
  {:else if usersState.status === "off"}
    <p class="hint">Turn on multi-user mode above and save changes to create and manage employee accounts here.</p>
  {:else if usersState.status === "error"}
    <div class="banner-error">
      <p>{usersState.message}</p>
      <button type="button" onclick={refreshUsers}>Retry</button>
    </div>
  {:else}
    {#if usersState.users.length === 0}
      <p class="hint">No employee accounts yet.</p>
    {:else}
      <div class="table-scroll">
        <table class="users-table">
          <thead>
            <tr>
              <th scope="col">Username</th>
              <th scope="col">Email</th>
              <th scope="col">Permissions</th>
              <th scope="col">Disabled</th>
              <th scope="col">Actions</th>
            </tr>
          </thead>
          <tbody>
            {#each usersState.users as u (u.id)}
              <tr>
                <td>{u.username}</td>
                <td>
                  <input
                    type="email"
                    class="email-input"
                    value={u.email}
                    disabled={rowSaving[u.id]}
                    aria-label={`Cloudflare Access email for ${u.username}`}
                    placeholder="Not set"
                    onchange={(e) => changeEmail(u, e.currentTarget)}
                  />
                </td>
                <td>
                  <div class="perm-grid">
                    {#each GRANTABLE_TAB_KEYS as key (key)}
                      <label class="toggle perm-toggle">
                        <input
                          type="checkbox"
                          checked={u.permissions.includes(key)}
                          disabled={rowSaving[u.id]}
                          onchange={(e) => togglePermission(u, key, e.currentTarget.checked)}
                        />
                        <span>{GRANTABLE_TAB_LABELS[key]}<span class="visually-hidden"> for {u.username}</span></span>
                      </label>
                    {/each}
                  </div>
                </td>
                <td>
                  <input
                    type="checkbox"
                    checked={u.disabled}
                    disabled={rowSaving[u.id]}
                    aria-label={`Disable ${u.username}`}
                    onchange={() => toggleDisabled(u)}
                  />
                </td>
                <td>
                  <div class="row-actions">
                    {#if resetOpenId === u.id}
                      <form class="reset-form" onsubmit={(e) => submitReset(e, u)}>
                        <label class="field field-inline">
                          <span class="visually-hidden">{`New password for ${u.username}`}</span>
                          <input type="password" bind:value={resetValue} placeholder="New password" minlength={8} required />
                        </label>
                        <button type="submit" disabled={resetSubmitting}>{resetSubmitting ? "Saving…" : "Save"}</button>
                        <button type="button" onclick={cancelReset}>Cancel</button>
                      </form>
                      {#if resetError}<p class="warn-text" role="alert">{resetError}</p>{/if}
                    {:else}
                      <button type="button" onclick={() => openReset(u.id)} aria-label={`Reset password for ${u.username}`}>
                        Reset password
                      </button>
                    {/if}
                    {#if confirmDeleteId === u.id}
                      <button type="button" class="danger" onclick={() => remove(u)} aria-label={`Confirm delete ${u.username}`}>
                        Delete?
                      </button>
                      <button type="button" onclick={cancelDeleteRow} aria-label={`Cancel deleting ${u.username}`}>Cancel</button>
                    {:else}
                      <button type="button" onclick={() => askDelete(u.id)} aria-label={`Delete ${u.username}`}>Delete</button>
                    {/if}
                  </div>
                  {#if rowError[u.id]}<p class="warn-text" role="alert">{rowError[u.id]}</p>{/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}

    <div class="subcard">
      <h3>Add employee</h3>
      <form class="field-rows" onsubmit={addUser}>
        <label class="field">
          <span>Username</span>
          <input bind:value={newUsername} required pattern={USERNAME_PATTERN} placeholder="For example, jordan" />
          <small class="field-hint">Use only lowercase letters, digits, periods, underscores, and hyphens.</small>
        </label>
        <label class="field">
          <span>Password</span>
          <input type="password" bind:value={newPassword} required minlength={8} autocomplete="new-password" />
        </label>
        <div class="field">
          <label for="new-user-email">Cloudflare Access email (optional)</label>
          <input id="new-user-email" type="email" bind:value={newEmail} autocomplete="off" placeholder="jordan@example.com" />
          <small class="field-hint">The address this person signs in to Cloudflare Access with. It lets them skip the password when they reach printpaca through Access.</small>
        </div>
        <div class="perm-grid">
          {#each GRANTABLE_TAB_KEYS as key (key)}
            <label class="toggle perm-toggle">
              <input
                type="checkbox"
                checked={newPermissions.has(key)}
                onchange={(e) => toggleNewPermission(key, e.currentTarget.checked)}
              />
              <span>{GRANTABLE_TAB_LABELS[key]}</span>
            </label>
          {/each}
        </div>
        {#if addError}<p class="warn-text" role="alert">{addError}</p>{/if}
        <button type="submit" class="primary" disabled={adding}>{adding ? "Adding…" : "Add employee"}</button>
      </form>
    </div>
  {/if}
</div>

<style>
  .table-scroll { overflow-x: auto; }
  .users-table { width: 100%; border-collapse: collapse; }
  .users-table th,
  .users-table td {
    text-align: left;
    padding: 0.4rem 0.5rem;
    vertical-align: top;
    border-bottom: 1px solid var(--border);
  }
  .perm-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 9rem), 1fr));
    gap: 0.2rem 0.6rem;
    min-width: 18rem;
  }
  .perm-toggle { min-height: 28px; font-size: var(--text-sm); }
  .row-actions { display: flex; flex-wrap: wrap; gap: 0.4rem; align-items: center; }
  .reset-form { display: flex; flex-wrap: wrap; gap: 0.4rem; align-items: center; }
  .reset-form .field-inline { margin: 0; }
  .subcard { margin-top: 1rem; }
  .email-input { min-width: 12rem; }
</style>
