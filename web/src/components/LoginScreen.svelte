<script lang="ts">
  import { onMount } from "svelte";
  import logoUrl from "../assets/alpaca-logo.svg";
  import { DEFAULT_FETCH_TIMEOUT_MS } from "../lib/fetchTimeouts";

  // Rendered by App.svelte in place of the app shell when GET /api/config 401s (ADMIN_PASSWORD set, no valid
  // session) or a mid-session request 401s (accessWatch.sessionExpired). onLoggedIn re-runs App.svelte's boot
  // flow after a successful POST /api/login. `notice` renders only for the sessionExpired case.
  let { onLoggedIn, notice }: { onLoggedIn: () => void; notice?: string } = $props();

  let username = $state("");
  let password = $state("");
  let error = $state("");
  let submitting = $state(false);
  // $state, not a plain `let`: the autofocus effect reads usernameInput reactively and must re-run when
  // bind:this assigns it, which happens only after multiUser flips true and the {#if multiUser} block mounts.
  let usernameInput: HTMLInputElement | undefined = $state();
  let passwordInput: HTMLInputElement | undefined;
  // True once GET /api/demo (server/auth.go's handleDemo, unauthenticated: the one signal reachable before a
  // session) confirms DEMO_MODE=1. Left false on a failed fetch or ordinary instance.
  let demoMode = $state(false);
  // The customer-facing quote site (DEMO_PUBLIC_URL, server/main.go's deriveDemoPublicURL); populated only
  // alongside demoMode.
  let publicUrl = $state("");
  // users.multiUser, from the same GET /api/demo response (the pre-auth login-info surface; no separate
  // endpoint exists). Gates the username field, so a single-user login screen is unchanged.
  let multiUser = $state(false);

  // Password gets the initial focus (single-user's only field, and the default before the async multiUser
  // answer). Once the /api/demo response confirms multi-user mode, focus moves to Username as it mounts
  // (fires once in practice).
  onMount(() => passwordInput?.focus());
  $effect(() => {
    if (multiUser) usernameInput?.focus();
  });

  onMount(async () => {
    try {
      const res = await fetch("api/demo", { signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS) });
      if (!res.ok) return;
      const body = (await res.json()) as { demoMode?: boolean; publicUrl?: string; multiUser?: boolean };
      if (body.demoMode === true) {
        demoMode = true;
        password = "demo"; // prefilled, not locked -- the visitor can still edit it
        if (body.publicUrl) publicUrl = body.publicUrl;
      }
      multiUser = body.multiUser === true;
    } catch {
      /* Not a real instance failure: the login form still works with a hand-typed password; stay silent. */
    }
  });

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (submitting || password === "") return;
    submitting = true;
    error = "";
    try {
      const res = await fetch("api/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        // username always rides along (server/auth.go's handleLogin trims it and treats absent and empty the
        // same), so sending "" in single-user mode needs no conditional.
        body: JSON.stringify({ username, password }),
        signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS),
      });
      if (res.ok) {
        username = "";
        password = "";
        onLoggedIn();
        return;
      }
      // Single-user mode has no username field, so "Incorrect password." is accurate. Once a username was
      // typed (multi-user), either field may be wrong, so the message names both without saying which,
      // keeping username enumeration no easier.
      error =
        res.status === 429
          ? "Too many attempts. Try again in a minute."
          : username.trim() !== ""
            ? "Incorrect username or password."
            : "Incorrect password.";
    } catch {
      error = "Couldn't reach the server. Check your connection and try again.";
    } finally {
      submitting = false;
    }
  }
</script>

<div class="login-shell">
  <form class="card login-card" onsubmit={submit}>
    <img class="logo" src={logoUrl} alt="" />
    <h1>printpaca</h1>
    {#if notice}
      <!-- Set only for the mid-session-401 case (App passes accessWatch.sessionExpired). A plain
           informational note, not `.warn-text`, which is reserved for the submit-time error that can show
           at the same time and must read as more urgent. -->
      <p class="banner-muted session-notice" role="status">{notice}</p>
    {/if}
    {#if demoMode}
      <p class="banner-muted demo-notice">
        This is a read-only demo. Sign in with the password <strong>demo</strong> to look around. Nothing you do is saved.
      </p>
      {#if publicUrl}
        <p class="banner-muted demo-notice">
          You can also try the customer-facing estimate page:
          <a href={publicUrl} target="_blank" rel="noopener noreferrer">{publicUrl}</a>.
        </p>
      {/if}
    {/if}
    {#if multiUser}
      <label class="field">
        <span>Username</span>
        <input bind:value={username} autocomplete="username" placeholder="Leave empty to sign in as the admin" bind:this={usernameInput} />
      </label>
    {/if}
    <label class="field">
      <span>Password</span>
      <input type="password" bind:value={password} autocomplete="current-password" required bind:this={passwordInput} />
    </label>
    {#if error}
      <p class="warn-text" role="alert">{error}</p>
    {/if}
    <button type="submit" class="primary" disabled={submitting || password === ""}>
      {submitting ? "Signing in…" : "Sign in"}
    </button>
  </form>
</div>

<style>
  .login-shell {
    min-height: 100vh;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: var(--space-4, 1rem);
    /* #app's desktop layout is a sidebar+main grid applied unconditionally; as its only child this shell
     * would be auto-placed into the sidebar column. Spanning every column lets the flex centering center
     * the card. */
    grid-column: 1 / -1;
  }
  .login-card {
    display: flex;
    flex-direction: column;
    align-items: stretch;
    gap: var(--space-3, 0.75rem);
    width: 100%;
    max-width: 320px;
  }
  .logo {
    height: 40px;
    width: auto;
    display: block;
    margin: 0 auto;
  }
  .login-card h1 {
    margin: 0 0 var(--space-2, 0.5rem);
    font-size: 1.25rem;
    text-align: center;
  }
</style>
