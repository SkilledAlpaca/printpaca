<script lang="ts">
  import type { PublicConfig } from "./api";
  import { telHref } from "./publicPricing";

  // One footer for every view of the shop (gallery, item views, estimate form, confirmations), in the same
  // place with the same content, so help and navigation are consistent (WCAG 3.2.3, 3.2.6). The shop is a
  // single page, so there is no Home or Shop link; "Staff sign-in" shows only when the server offers one.
  // footerLinks are the owner's shop.footerLinks (https only, validated server-side); they render as text.
  // showContact is false on the Closed page, which already lists the contact details above.
  let {
    branding,
    staffUrl = "",
    footerLinks = [],
    showContact = true,
  }: {
    branding: PublicConfig["branding"];
    staffUrl?: string;
    footerLinks?: { label: string; url: string }[];
    showContact?: boolean;
  } = $props();

</script>

<footer class="shop-footer">
  {#if staffUrl || footerLinks.length > 0}
    <nav aria-label="Footer">
      {#each footerLinks as link, i (i)}
        {#if i > 0}&middot;{/if}
        <a href={link.url} target="_blank" rel="noopener noreferrer">{link.label}</a>
      {/each}
      {#if footerLinks.length > 0 && staffUrl}&middot;{/if}
      {#if staffUrl}<a href={staffUrl}>Staff sign-in</a>{/if}
    </nav>
  {/if}
  {#if showContact && (branding.email || branding.phone)}
    <p>
      Questions?
      {#if branding.email}<a href="mailto:{branding.email}">{branding.email}</a>{/if}
      {#if branding.email && branding.phone}&middot;{/if}
      {#if branding.phone}<a href={telHref(branding.phone)}>{branding.phone}</a>{/if}
    </p>
  {/if}
  {#if showContact && branding.address}<p class="shop-address">{branding.address}</p>{/if}
</footer>

<style>
  .shop-footer {
    text-align: center;
    color: var(--muted);
    font-size: var(--type-secondary);
    margin-top: var(--space-2);
    overflow-wrap: anywhere;
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
  }
  .shop-footer p, .shop-footer nav { margin: 0; }
  .shop-footer a { color: inherit; }
  /* 44 px tap targets on phones: the links are inline text, about 17 px tall by default. */
  @media (max-width: 600px) {
    .shop-footer a {
      display: inline-flex;
      align-items: center;
      min-height: 44px;
      padding: 0 var(--space-2);
    }
  }
</style>
