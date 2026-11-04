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
    headingLevel = 1,
    notice = "",
    homeHref = "",
  }: {
    businessName?: string;
    /** Custom logo (quote.logoUrl); the project mark when empty. */
    logoUrl?: string;
    /** The owner's pausedMessage; nothing extra is shown when it is blank. */
    message?: string;
    branding: Pick<PublicConfig["branding"], "email" | "phone" | "address">;
    /** Heading level of "We're closed right now": 1 on the shop's own page, 2 inside the admin Settings preview. */
    headingLevel?: 1 | 2;
    /** Shown above the heading when a submit was refused because the shop closed mid-form ("Your order wasn't
     * placed."). The host focuses it by id (closed-notice). */
    notice?: string;
    /** D45: when set, the logo and name are one link to it (a reload of the Closed page on the shop). The Settings preview leaves it empty. */
    homeHref?: string;
  } = $props();
</script>

<section class="closed" aria-labelledby="closed-heading">
  <header class="brand">
    {#if homeHref}
      <a class="brand-link" href={homeHref} aria-label="{businessName || 'printpaca'}, shop home">
        <img class="logo" src={logoUrl || projectLogoUrl} alt="" />
        <span class="brand-name">{businessName || "printpaca"}</span>
      </a>
    {:else}
      <!-- Decorative next to the business name, so alt is empty. -->
      <img class="logo" src={logoUrl || projectLogoUrl} alt="" />
      <p class="brand-name">{businessName || "printpaca"}</p>
    {/if}
  </header>
  <Card>
    <div class="closed-body">
      {#if notice}<p class="closed-notice" id="closed-notice" role="status" tabindex="-1">{notice}</p>{/if}
      <svelte:element this={`h${headingLevel}`} id="closed-heading" class="closed-heading">We're closed right now</svelte:element>
      {#if message}<p class="paused-message">{message}</p>{/if}
      <ContactDetails {branding} />
    </div>
  </Card>
</section>

<style>
  .closed { display: flex; flex-direction: column; gap: var(--gap); }
  .brand { display: flex; align-items: center; gap: var(--space-3); }
  .logo { height: 2.5rem; width: auto; }
  .brand-link { display: inline-flex; align-items: center; gap: var(--space-3); color: inherit; text-decoration: none; min-height: 44px; border-radius: var(--radius); }
  .brand-link:hover .brand-name { text-decoration: underline; text-underline-offset: 0.15em; }
  .brand-link:focus-visible { outline: 2px solid var(--accent-text); outline-offset: 3px; }
  .brand-name { margin: 0; font-size: var(--type-title); font-weight: 700; overflow-wrap: anywhere; }
  .closed-body { display: flex; flex-direction: column; gap: var(--gap); align-items: flex-start; }
  .closed-heading { margin: 0; }
  .closed-notice { margin: 0; padding: var(--space-3); border-left: 3px solid var(--danger); background: var(--inset-bg); font-weight: 600; overflow-wrap: anywhere; }
  .closed-notice:focus-visible { outline: none; }
  .paused-message { margin: 0; font-size: var(--type-heading); overflow-wrap: anywhere; }
</style>
