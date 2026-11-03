<script lang="ts">
  import type { PublicConfig } from "./api";

  // One footer for every public page (front door, shop, item views, confirmations), in the same place with the
  // same content, so help and navigation are consistent (WCAG 3.2.3, 3.2.6).
  let { branding }: { branding: PublicConfig["branding"] } = $props();

  /** A phone number as a tel: target: digits and a leading plus only. */
  const telHref = $derived(`tel:${branding.phone.replace(/[^\d+]/g, "")}`);
</script>

<footer class="shop-footer">
  <nav aria-label="Footer">
    <a href="/">Home</a>
    &middot;
    <a href="/order">Shop</a>
  </nav>
  {#if branding.email || branding.phone}
    <p>
      Questions?
      {#if branding.email}<a href="mailto:{branding.email}">{branding.email}</a>{/if}
      {#if branding.email && branding.phone}&middot;{/if}
      {#if branding.phone}<a href={telHref}>{branding.phone}</a>{/if}
    </p>
  {/if}
  {#if branding.address}<p class="shop-address">{branding.address}</p>{/if}
</footer>

<style>
  .shop-footer {
    text-align: center;
    color: var(--muted);
    font-size: var(--text-sm);
    margin-top: var(--space-2);
    overflow-wrap: anywhere;
  }
  .shop-footer p, .shop-footer nav { margin: 0.2rem 0; }
  .shop-footer a { color: inherit; }
</style>
