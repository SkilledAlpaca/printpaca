<script lang="ts">
  import { onMount } from "svelte";
  import { announce } from "../lib/a11y.svelte";
  import logoUrl from "../assets/alpaca-logo.svg";
  import { DEFAULT_FETCH_TIMEOUT_MS } from "../lib/fetchTimeouts";

  // Rendered by App.svelte in place of the app shell when GET /api/config 401s (ADMIN_PASSWORD set, no valid
  // session) or a mid-session request 401s (accessWatch.sessionExpired). onLoggedIn re-runs App.svelte's boot
  // flow after a successful POST /api/login. `notice` renders only for the sessionExpired case.
  // businessName/logoSrc are the public branding (or printpaca and the bundled mark), resolved by App.svelte.
  let {
    onLoggedIn,
    notice,
    businessName = "printpaca",
    logoSrc = logoUrl,
    onLogoError = () => {},
  }: { onLoggedIn: () => void; notice?: string; businessName?: string; logoSrc?: string; onLogoError?: () => void } = $props();

  let username = $state("");
  let password = $state("");
  let error = $state("");
  let submitting = $state(false);
  // $state, not a plain `let`: the autofocus effect reads usernameInput reactively and must re-run when
  // bind:this assigns it, which happens only after multiUser flips true and the {#if multiUser} block mounts.
  let usernameInput: HTMLInputElement | undefined = $state();
  let passwordInput: HTMLInputElement | undefined = $state();
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
  // The session-expired notice appears with the screen; announce it once instead of relying on a live
  // region that is inserted already populated (WCAG 4.1.3).
  onMount(() => {
    if (notice) announce(notice);
  });
  // Skipped when the visitor has already started typing a password: moving focus then would send the rest
  // of the password into the visible Username field (WCAG 2.4.3).
  $effect(() => {
    if (multiUser && password === "" && document.activeElement === passwordInput) usernameInput?.focus();
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
    if (submitting) return;
    if (password === "") {
      // The button stays enabled so an empty submit gets feedback instead of nothing (WCAG 3.3.1, 3.3.3).
      error = "Enter your password.";
      passwordInput?.focus();
      return;
    }
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

<main class="login-shell">
  <form class="card login-card" onsubmit={submit}>
    <img class="logo" src={logoSrc} alt="" onerror={onLogoError} />
    <h1>{businessName}</h1>
    {#if notice}
      <!-- Set only for the mid-session-401 case (App passes accessWatch.sessionExpired). A plain
           informational note, not `.warn-text`, which is reserved for the submit-time error that can show
           at the same time and must read as more urgent. -->
      <p class="banner-muted session-notice">{notice}</p>
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
      <div class="field">
        <label for="login-username">Username</label>
        <input
          id="login-username"
          bind:value={username}
          autocomplete="username"
          aria-describedby={error ? "username-hint login-error" : "username-hint"}
          aria-invalid={error ? "true" : undefined}
          bind:this={usernameInput}
        />
        <small class="field-hint" id="username-hint">Leave empty to sign in as the admin.</small>
      </div>
    {/if}
    <label class="field">
      <span>Password</span>
      <input
        type="password"
        bind:value={password}
        autocomplete="current-password"
        aria-describedby={error ? "login-error" : undefined}
        aria-invalid={error ? "true" : undefined}
        bind:this={passwordInput}
      />
    </label>
    <!-- Persistent container: an alert inserted already populated is announced unreliably. -->
    <div role="alert">
      {#if error}<p class="warn-text" id="login-error">{error}</p>{/if}
    </div>
    <button type="submit" class="primary" aria-disabled={submitting ? "true" : undefined}>
      {submitting ? "Signing in…" : "Sign in"}
    </button>
  </form>
</main>

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
