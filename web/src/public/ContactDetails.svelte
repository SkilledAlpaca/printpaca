<script lang="ts">
  import type { PublicConfig } from "./api";
  import { telHref } from "./publicPricing";

  // The shop's contact details as tappable links, one per line. Renders nothing when the shop published none
  // (the server withholds them entirely when shop.showContact is off). Used on the Closed page and under the
  // "Custom estimates aren't offered" note; the footer keeps its own one-line form.
  let { branding }: { branding: Pick<PublicConfig["branding"], "email" | "phone" | "address"> } = $props();
</script>

{#if branding.email || branding.phone || branding.address}
  <ul class="contact" aria-label="Contact details">
    {#if branding.email}<li><a href="mailto:{branding.email}">{branding.email}</a></li>{/if}
    {#if branding.phone}<li><a class="phone" href={telHref(branding.phone)}>{branding.phone}</a></li>{/if}
    {#if branding.address}<li class="address">{branding.address}</li>{/if}
  </ul>
{/if}

<style>
  .contact { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--space-1); overflow-wrap: anywhere; }
  .contact a { display: inline-flex; align-items: center; min-height: 44px; }
  .phone { white-space: nowrap; }
  .address { color: var(--muted); }
</style>
