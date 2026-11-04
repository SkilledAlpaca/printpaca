<script lang="ts">
  import type { PublicConfig } from "./api";

  // One footer for every view of the shop (gallery, item views, estimate form, confirmations), in the same
  // place with the same content, so help and navigation are consistent (WCAG 3.2.3, 3.2.6). The shop is a
  // single page, so there is no Home or Shop link; "Staff sign-in" shows only when the server offers one.
  let { branding, staffUrl = "" }: { branding: PublicConfig["branding"]; staffUrl?: string } = $props();

  /** A phone number as a tel: target: digits and a leading plus only. */
  const telHref = $derived(`tel:${branding.phone.replace(/[^\d+]/g, "")}`);
</script>

<footer class="shop-footer">
  {#if staffUrl}
    <nav aria-label="Footer">
      <a href={staffUrl}>Staff sign-in</a>
    </nav>
  {/if}
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
  /* 44 px tap targets on phones: the links are inline text, about 17 px tall by default. */
  @media (max-width: 600px) {
    .shop-footer a {
      display: inline-block;
      padding: 0.7rem 0.5rem;
    }
  }
</style>
