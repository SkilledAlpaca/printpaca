<script lang="ts">
  import type { PublicConfig } from "./api";
  import { telHref } from "./publicPricing";

  // One footer for every view of the shop (gallery, item views, estimate form, confirmations, Closed
  // page), in the same place with the same content, so help and navigation are consistent (WCAG 3.2.3, 3.2.6).
  // A full-width band on a tinted surface; its content sits in a container wider than the reading column.
  // Top row: the business (name, logo, address), Contact (email, phone) and Links (the owner's footerLinks);
  // an empty column collapses. Bottom row: copyright and Staff sign-in, and the credit on the right.
  // The shop is a single page, so there is no Home or Shop link; "Staff sign-in" shows only when the server
  // offers one. footerLinks are the owner's shop.footerLinks (https only, validated server-side).
  // showContact is false on the Closed page, which already lists the contact details above, so the Contact
  // column and the address are left out there and nothing repeats.
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

  const name = $derived(branding.businessName || "printpaca");
  const hasContact = $derived(showContact && !!(branding.email || branding.phone));
  const hasLinks = $derived(footerLinks.length > 0);
  const columns = $derived(1 + (hasContact ? 1 : 0) + (hasLinks ? 1 : 0));
  const year = new Date().getFullYear();
</script>

<footer class="shop-footer">
  <div class="inner">
    <div class="columns" style:--cols={columns}>
      <section class="col business" aria-label="Business">
        <p class="biz">
          {#if branding.logoUrl}<img class="footer-logo" src={branding.logoUrl} alt="" />{/if}
          <span class="biz-name">{name}</span>
        </p>
        {#if showContact && branding.address}<p class="shop-address">{branding.address}</p>{/if}
      </section>
      {#if hasContact}
        <section class="col contact" aria-labelledby="footer-contact">
          <h2 class="col-title" id="footer-contact">Contact</h2>
          <ul>
            {#if branding.email}<li><a href="mailto:{branding.email}">{branding.email}</a></li>{/if}
            {#if branding.phone}<li><a href={telHref(branding.phone)}>{branding.phone}</a></li>{/if}
          </ul>
        </section>
      {/if}
      {#if hasLinks}
        <nav class="col links" aria-labelledby="footer-links">
          <h2 class="col-title" id="footer-links">Links</h2>
          <ul>
            {#each footerLinks as link, i (i)}
              <li><a href={link.url} target="_blank" rel="noopener noreferrer">{link.label}</a></li>
            {/each}
          </ul>
        </nav>
      {/if}
    </div>
    <div class="bottom">
      <p class="legal">
        <span>&copy; {year} {name}</span>
        {#if staffUrl}<a href={staffUrl}>Staff sign-in</a>{/if}
      </p>
      <p class="powered-by">Powered by <a href="https://printpaca.com" target="_blank" rel="noopener">printpaca</a></p>
    </div>
  </div>
</footer>

<style>
  .shop-footer {
    background: var(--surface);
    color: var(--muted);
    font-size: var(--type-secondary);
    margin-top: var(--space-12);
    padding: var(--space-8) 0 var(--space-4);
    overflow-wrap: anywhere;
  }
  /* The same box as the shop header's column (.page in PublicQuotePage.svelte): the same width token and the
   * same side padding, so the footer's content lines up with the header's. The tinted band itself spans the page. */
  .inner {
    max-width: calc(var(--measure-record) + 2 * var(--card-pad));
    margin: 0 auto;
    padding-inline: var(--space-4);
    display: flex;
    flex-direction: column;
    gap: var(--space-6);
  }
  .shop-footer p, .shop-footer ul, .shop-footer h2 { margin: 0; }
  .columns { display: grid; grid-template-columns: minmax(0, 1fr); gap: var(--space-6); }
  .col { display: flex; flex-direction: column; gap: var(--space-2); min-width: 0; }
  .col-title {
    font-size: var(--type-caption);
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: var(--muted);
  }
  .col ul { list-style: none; padding: 0; display: flex; flex-direction: column; gap: var(--space-1); }
  .biz { display: flex; align-items: center; gap: var(--space-3); }
  .biz-name { font-size: var(--type-heading); font-weight: 700; color: var(--text); }
  .footer-logo { height: 2rem; width: auto; }
  .shop-address { white-space: pre-line; }

  /* Links take the text color, and the shop accent only on hover and focus. */
  .shop-footer a { color: var(--text); text-decoration: none; }
  .shop-footer a:hover, .shop-footer a:focus-visible { color: var(--accent-text); text-decoration: underline; text-underline-offset: 0.2em; }

  .bottom {
    border-top: 1px solid var(--border);
    padding-top: var(--space-4);
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-2) var(--space-6);
    font-size: var(--type-caption);
  }
  .legal { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-1) var(--space-4); }
  .powered-by { margin-left: auto; text-align: right; }
  /* The credit link sits inside a sentence, so it is underlined at rest (WCAG 1.4.1). */
  .powered-by a { text-decoration: underline; text-underline-offset: 0.2em; }

  @media (min-width: 720px) {
    .columns { grid-template-columns: repeat(var(--cols), minmax(0, 1fr)); gap: var(--space-8); }
  }
  /* 44 px tap targets on phones: the links are inline text, about 17 px tall by default. */
  @media (max-width: 600px) {
    .inner { padding-inline: var(--space-3); }
    .shop-footer a {
      display: inline-flex;
      align-items: center;
      min-height: 44px;
    }
    .col ul { gap: 0; }
    .bottom { flex-direction: column; align-items: flex-start; }
    .powered-by { margin-left: 0; text-align: left; }
  }
</style>
