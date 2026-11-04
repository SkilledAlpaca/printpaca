<script module lang="ts">
  // The local calendar date, not toISOString()'s UTC date (a UTC-7 shop printing at 6pm would get
  // tomorrow's). Exported for PrintView.test.ts.
  export function isoDate(d: Date): string {
    return d.toLocaleDateString("en-CA"); // en-CA formats as YYYY-MM-DD
  }
</script>

<script lang="ts">
  import type { EffectiveConfig, OrderBreakdown } from "../lib/engine/index";
  import { formatDate, money } from "../lib/format";
  import { printLogo } from "../lib/printLogo.svelte";

  let {
    cfg,
    order,
    mode,
    meta,
  }: {
    cfg: EffectiveConfig;
    order: OrderBreakdown;
    mode: "client" | "internal";
    meta: { customer: string; project: string; reference: string };
  } = $props();

  const p = $derived(order.pricing);
  const multi = $derived(order.parts.length > 1);
  // A single-part order prints off the sole part's breakdown.
  const breakdown = $derived(order.parts[0]!.breakdown);
  // Guards pricing validity independently of QuoteTab's canPrint, so a $0.00 quote can never render even if
  // reached another way.
  const pricingBroken = $derived(!!p && !p.valid);

  const printedOn = formatDate(new Date().toISOString());
  const validUntil = $derived.by(() => {
    const d = new Date();
    d.setDate(d.getDate() + cfg.quote.validDays);
    return formatDate(d.toISOString());
  });
</script>

<div id="print-view" class="quote">
  <header>
    <!-- Rendered from printLogo.readyUrl (preloaded by App.svelte), not cfg.quote.logoUrl, so
         window.print() never waits on the network (see lib/printLogo.svelte.ts). -->
    {#if printLogo.readyUrl}<img class="logo" src={printLogo.readyUrl} alt="" />{/if}
    <!-- A literal " - " text node before {/if} loses its trailing space to Svelte's whitespace trimming;
         building the prefix as one expression keeps it. -->
    <h1>{cfg.quote.businessName ? `${cfg.quote.businessName}: ` : ""}{mode === "client" ? "Quote" : "Internal cost audit"}</h1>
    <div class="seller">
      {#if cfg.quote.address}<p class="address">{cfg.quote.address}</p>{/if}
      {#if cfg.quote.email}<p>{cfg.quote.email}</p>{/if}
      {#if cfg.quote.phone}<p>{cfg.quote.phone}</p>{/if}
      {#if cfg.quote.taxId}<p>Tax ID: {cfg.quote.taxId}</p>{/if}
    </div>
    <p>
      {#if meta.reference}Quote #: {meta.reference} · {/if}
      {#if meta.customer}For: {meta.customer} · {/if}
      {#if meta.project}Project: {meta.project} · {/if}
      Date: {printedOn} · Valid until {validUntil}
    </p>
  </header>

  {#if mode === "internal"}
    <table>
      <tbody>
        {#each order.parts as part, pi (pi)}
          {#if multi}
            <tr><th colspan="2" class="part-head">{part.label}</th></tr>
          {/if}
          {#each part.breakdown.perUnit as line (line.id)}
            <tr>
              <td>
                {line.label}
                <br /><small class="formula">{line.formula}</small>
              </td>
              <td class="num">{money(line.amount, cfg)}</td>
            </tr>
          {/each}
          <tr><th>Cost per unit</th><th class="num">{money(part.breakdown.perUnitTotal, cfg)}</th></tr>
          <tr><th>Batch total (×{part.breakdown.quantity})</th><th class="num">{money(part.breakdown.batchTotal, cfg)}</th></tr>
          {#if multi && p && !pricingBroken}
            <tr><td>Sale price (×{part.breakdown.quantity})</td><td class="num">{money(part.itemRevenue, cfg)}</td></tr>
          {/if}
        {/each}
        {#if multi}
          <tr><th>Order cost total</th><th class="num">{money(order.batchTotal, cfg)}</th></tr>
        {/if}
        {#if p}
          {#if pricingBroken}
            <tr><td colspan="2" class="invalid-note">Pricing is invalid. Fix the Price and profit inputs before quoting.</td></tr>
          {:else}
            {#if !multi}
              <tr><td>Sale price (×{breakdown.quantity})</td><td class="num">{money(order.itemRevenue, cfg)}</td></tr>
            {:else}
              <tr><td>Items total</td><td class="num">{money(order.itemRevenue, cfg)}</td></tr>
            {/if}
            {#if p.shippingCharge > 0}
              <tr><td>Shipping charged (customer pays)</td><td class="num">{money(p.shippingCharge, cfg)}</td></tr>
            {/if}
            <tr><th>Revenue</th><th class="num">{money(p.revenue, cfg)}</th></tr>
            {#each p.feeLines as f, i (i)}
              <tr><td>Fee: {f.label}</td><td class="num">-{money(f.amount, cfg)}</td></tr>
            {/each}
            <tr><td>Shipping cost (paid)</td><td class="num">-{money(p.shippingCost, cfg)}</td></tr>
            {#if p.tax > 0}<tr><td>Tax</td><td class="num">-{money(p.tax, cfg)}</td></tr>{/if}
            <tr><th>Net profit</th><th class="num">{money(p.netProfit, cfg)} ({p.revenue > 0 ? `${p.achievedMarginPct.toFixed(1)}%` : "n/a"})</th></tr>
          {/if}
        {/if}
      </tbody>
    </table>
  {:else}
    <table>
      <tbody>
        {#if multi}
          {#each order.parts as part, i (i)}
            {@const qty = part.breakdown.quantity}
            <tr>
              <td>
                {part.label}{qty > 1 ? ` × ${qty}` : ""}{qty > 1 && p && !pricingBroken ? ` (${money(part.resolvedPrice, cfg)} each)` : ""}
              </td>
              <td class="num">
                {#if pricingBroken}-{:else if p}{money(part.itemRevenue, cfg)}{:else}{money(part.breakdown.batchTotal, cfg)}{/if}
              </td>
            </tr>
          {/each}
        {:else}
          <tr>
            <td>
              3D printed part{breakdown.quantity > 1 ? `s × ${breakdown.quantity}` : ""}{breakdown.quantity > 1 && p && !pricingBroken ? ` (${money(order.parts[0]!.resolvedPrice, cfg)} each)` : ""}
            </td>
            <td class="num">
              {#if pricingBroken}-{:else if p}{money(order.itemRevenue, cfg)}{:else}{money(breakdown.batchTotal, cfg)}{/if}
            </td>
          </tr>
        {/if}
        {#if p && !pricingBroken && p.shippingCharge > 0}
          <tr><td>Shipping and handling</td><td class="num">{money(p.shippingCharge, cfg)}</td></tr>
        {/if}
        <tr>
          <th>Total</th>
          <th class="num">
            {#if pricingBroken}-{:else if p}{money(p.revenue, cfg)}{:else}{money(order.batchTotal, cfg)}{/if}
          </th>
        </tr>
        {#if pricingBroken}
          <tr><td colspan="2" class="invalid-note">Pricing is invalid. This quote isn't ready to send.</td></tr>
        {/if}
      </tbody>
    </table>
    {#if p && !pricingBroken && p.tax > 0}<p class="tax-note">Total includes {money(p.tax, cfg)} tax.</p>{/if}
    {#if cfg.quote.paymentInstructions}<p><strong>Payment:</strong> {cfg.quote.paymentInstructions}</p>{/if}
    {#if cfg.quote.termsText}<p class="terms">{cfg.quote.termsText}</p>{/if}
  {/if}
</div>

<style>
  /* Print colors stay literal (black on white); spacing and type sizes reuse the screen scale. */
  #print-view { background: white; color: black; padding: var(--space-8); }
  /* theme.css stripes and highlights table rows with tokens; a dark scheme would put black text on those. */
  #print-view tr, #print-view th, #print-view td { background: transparent; }
  #print-view header { margin-bottom: var(--space-4); }
  .logo { display: block; max-height: 56px; max-width: 240px; object-fit: contain; margin-bottom: var(--space-2); }
  .seller { font-size: var(--text-sm); color: #444; margin: var(--space-1) 0 0; }
  .seller p { margin: 0.1rem 0; white-space: pre-line; }
  table { width: 100%; border-collapse: collapse; }
  td, th { text-align: left; padding: 0.35rem var(--space-2); border-bottom: 1px solid #ccc; }
  .num { text-align: right; font-variant-numeric: tabular-nums; }
  .terms { font-size: var(--text-sm); color: #444; }
  .tax-note { font-size: var(--text-sm); color: #666; }
  .formula { color: #666; font-weight: 400; font-size: var(--text-xs); }
  #print-view .part-head { background: #f2f2f2; }
  .invalid-note { color: #a00; font-style: italic; }
</style>
