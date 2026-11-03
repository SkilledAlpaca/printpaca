<script lang="ts">
  import { errorsFrom } from "../../lib/clientsApi";
  import { DEFAULT_FETCH_TIMEOUT_MS } from "../../lib/fetchTimeouts";
  import type { ConfigUser } from "../../lib/permissions";
  import { appConfig } from "../../lib/state.svelte";
  import { demoLocal } from "../../lib/demoLocal.svelte";
  import { accessSignInSummary, accountCardMode } from "../../lib/accessSignIn";

  // Mounted for every authenticated user, admin included. Unlike other settings cards it needs neither GET
  // /api/settings nor SettingsEditor, only GET /api/config's "user" extra (passed as `user`) and POST
  // /api/me/password. GET /api/settings is hard admin-only (server/main.go), so an employee must still reach
  // this card although the rest of the page never loads for them (see SettingsTab.svelte).
  let { user }: { user: ConfigUser | null } = $props();

  // The demo banner's "User mode" toggle is presentation only: it hides the "Signed in as X (admin)" line,
  // the one part of this card that surfaces the multi-user concept, when set to "single". Outside the demo it
  // is always true.
  const showsMultiUser = $derived(!(appConfig.current?.demoMode === true) || demoLocal.userMode === "multi");

  // An Access sign-in has no printpaca password to change, so the card shows who Cloudflare Access says you
  // are instead of the password form.
  const mode = $derived(accountCardMode(user));

  const MIN_PASSWORD_LENGTH = 8;

  let current = $state("");
  let next = $state("");
  let submitting = $state(false);
  let error = $state("");
  let success = $state(false);

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (submitting) return;
    error = "";
    success = false;
    if (next.length < MIN_PASSWORD_LENGTH) {
      error = `New password must be at least ${MIN_PASSWORD_LENGTH} characters.`;
      return;
    }
    submitting = true;
    try {
      const res = await fetch("api/me/password", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ current, new: next }),
        signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS),
      });
      if (!res.ok) throw await errorsFrom(res);
      success = true;
      current = "";
      next = "";
    } catch (err) {
      error = err instanceof Error ? err.message : "Couldn't change your password.";
    } finally {
      submitting = false;
    }
  }
</script>

<div id="section-account" class="card roomy">
  <h2>Account</h2>
  {#if user && mode === "access"}
    <p class="hint">{accessSignInSummary(user)}</p>
  {:else if user && showsMultiUser}
    <p class="hint">
      Signed in as <strong>{user.username}</strong>{user.isAdmin ? " (admin)" : ""}.
    </p>
  {/if}
  {#if mode === "access"}
    <!-- No password form: this person doesn't sign in with a printpaca password. -->
  {:else if user && !user.isAdmin}
    <form class="field-rows" onsubmit={submit}>
      <label class="field">
        <span>Current password</span>
        <input type="password" bind:value={current} autocomplete="current-password" required />
      </label>
      <label class="field">
        <span>New password</span>
        <input type="password" bind:value={next} autocomplete="new-password" required minlength={MIN_PASSWORD_LENGTH} />
      </label>
      {#if error}<p class="warn-text" role="alert">{error}</p>{/if}
      {#if success}<p class="hint" role="status">Password changed.</p>{/if}
      <button type="submit" class="primary" aria-disabled={submitting} aria-busy={submitting}>{submitting ? "Changing…" : "Change password"}</button>
    </form>
  {:else if user?.signedInVia === "none"}
    <p class="hint">Sign-in is off. Set ADMIN_PASSWORD on the server to require a password.</p>
  {:else if user?.isAdmin}
    <p class="hint">The admin password is set by the ADMIN_PASSWORD environment variable and can't be changed here.</p>
  {/if}
</div>
