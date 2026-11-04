<script lang="ts">
  import type { PublicConfig } from "./api";
  import { telHref } from "./publicPricing";

  // The 429 copy: no jargon, and the shop's own contact details as tappable links when it has published them.
  let { branding }: { branding: Pick<PublicConfig["branding"], "email" | "phone"> } = $props();
</script>

<p class="warn-text rate-limited">
  We've had a lot of requests from your connection. Try again later{#if branding.email}
    or email <a href="mailto:{branding.email}">{branding.email}</a>{/if}{#if branding.phone}
    or call <a class="phone" href={telHref(branding.phone)}>{branding.phone}</a>{/if}.
</p>

<style>
  .phone { white-space: nowrap; }
  a { overflow-wrap: anywhere; }
</style>
