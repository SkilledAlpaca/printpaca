<script lang="ts">
  import type { PublicConfig } from "./api";
  import ContactDetails from "./ContactDetails.svelte";
  import Card from "../components/ui/Card.svelte";
  import projectLogoUrl from "../assets/alpaca-logo.svg";

  // D27: what every public route shows while the shop isn't accepting orders. Props only, so Settings can render
  // the same page as a preview of what customers see. The footer (Staff sign-in) belongs to the host page.
  let {
    businessName = "",
    logoUrl = "",
    message = "",
    branding,
  }: {
    businessName?: string;
    /** Custom logo (quote.logoUrl); the project mark when empty. */
    logoUrl?: string;
    /** The owner's pausedMessage; nothing extra is shown when it is blank. */
    message?: string;
    branding: Pick<PublicConfig["branding"], "email" | "phone" | "address">;
  } = $props();
</script>

<section class="closed" aria-labelledby="closed-heading">
  <header class="brand">
    <!-- Decorative next to the business name, so alt is empty. -->
    <img class="logo" src={logoUrl || projectLogoUrl} alt="" />
    <p class="brand-name">{businessName || "printpaca"}</p>
  </header>
  <Card>
    <div class="closed-body">
      <h1 id="closed-heading">We're closed right now</h1>
      {#if message}<p class="paused-message">{message}</p>{/if}
      <ContactDetails {branding} />
    </div>
  </Card>
</section>

<style>
  .closed { display: flex; flex-direction: column; gap: var(--gap); }
  .brand { display: flex; align-items: center; gap: var(--space-3); }
  .logo { height: 2.5rem; width: auto; }
  .brand-name { margin: 0; font-size: var(--type-title); font-weight: 700; overflow-wrap: anywhere; }
  .closed-body { display: flex; flex-direction: column; gap: var(--gap); align-items: flex-start; }
  h1 { margin: 0; }
  .paused-message { margin: 0; font-size: var(--type-heading); overflow-wrap: anywhere; }
</style>
