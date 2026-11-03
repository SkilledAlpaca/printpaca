<script lang="ts">
  import { onMount } from "svelte";
  import projectLogoUrl from "../assets/alpaca-logo.svg";
  import PublicFooter from "./PublicFooter.svelte";
  import { loadPublicConfig, type PublicConfig } from "./api";

  // The page a customer lands on at "/": who you are, an Order button, and (when the server offers one) a
  // Staff sign-in button. Same public config and look as the shop page, so branding and custom.css apply.
  let phase = $state<"loading" | "failed" | "ready">("loading");
  let cfg = $state.raw<PublicConfig | null>(null);

  onMount(async () => {
    const result = await loadPublicConfig();
    if (!result) {
      phase = "failed";
      return;
    }
    cfg = result;
    phase = "ready";
  });
</script>

<svelte:head>
  <title>{cfg?.branding.businessName || "printpaca"}</title>
</svelte:head>

<main class="page">
  {#if phase === "loading"}
    <p class="muted center" role="status">Loading…</p>
  {:else if phase === "failed"}
    <p class="muted center">This page is temporarily unavailable.</p>
  {:else if cfg}
    <header class="brand">
      <!-- Custom logo (quote.logoUrl) when set, else the project mark. Decorative next to the <h1>. -->
      <img class="logo" src={cfg.branding.logoUrl || projectLogoUrl} alt="" />
      <h1>{cfg.branding.businessName || "printpaca"}</h1>
    </header>
    {#if cfg.introText}<p class="intro">{cfg.introText}</p>{/if}

    <div class="actions">
      <a class="action primary" href="/order">Order or get an estimate</a>
      {#if cfg.staffUrl}<a class="action" href={cfg.staffUrl}>Staff sign-in</a>{/if}
    </div>
    <PublicFooter branding={cfg.branding} />
  {/if}
</main>

<style>
  .page {
    max-width: 760px;
    margin: 0 auto;
    padding: var(--space-4);
    display: flex;
    flex-direction: column;
    gap: var(--space-4);
  }
  .center { text-align: center; margin-top: var(--space-8); }
  .muted { color: var(--muted); }

  .brand {
    display: flex;
    align-items: center;
    gap: var(--space-3);
  }
  .brand .logo { height: 2.5rem; width: auto; }
  .brand h1 { margin: 0; }

  .intro { color: var(--muted); margin: 0; }

  /* Stacked full-width buttons on a phone, side by side once there is room. Anchors styled like the
   * theme's buttons: a plain navigation, so they stay real links. */
  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-3);
  }
  .action {
    flex: 1 1 12rem;
    box-sizing: border-box;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    min-height: 44px;
    padding: 0.4rem 1rem;
    border: 1px solid var(--border);
    border-radius: calc(var(--radius) / 2);
    background: var(--surface);
    color: var(--text);
    font-weight: 600;
    text-decoration: none;
  }
  .action:hover { background: var(--surface-2); }
  .action.primary {
    background: var(--accent);
    color: var(--accent-contrast);
    border-color: var(--accent);
  }
  .action.primary:hover { filter: brightness(1.05); }
</style>
