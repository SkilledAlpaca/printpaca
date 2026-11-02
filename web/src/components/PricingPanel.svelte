<script lang="ts">
  import type { EffectiveConfig, OrderBreakdown } from "../lib/engine/index";
  import { quote, type PartForm } from "../lib/state.svelte";
  import { money, warnFor, friendlyMessage } from "../lib/format";
  import { otherPricingWarnings, partPricingWarning } from "../lib/orderView";
  import LabeledNumber from "./LabeledNumber.svelte";
  import CardHelp from "./CardHelp.svelte";
  import { TILE_HELP } from "../lib/guideContent";

  // `part` is the part whose sale price override the field edits (the active part); the rest is order-level.
  let { cfg, order, part, partIndex }: { cfg: EffectiveConfig; order: OrderBreakdown; part: PartForm; partIndex: number } = $props();

  const form = quote.order;
  const p = $derived(order.pricing);
  const pw = $derived(p?.warnings ?? []);
  const multi = $derived(order.parts.length > 1);
  const shippingDelta = $derived(p ? p.shippingCharge - p.shippingCost : 0);
  // Guide is a Tab like any other: when disabled, App.svelte's hashchange fallback rewrites the link off the
  // guide, so don't render it as if it went somewhere.
  const guideEnabled = $derived(!cfg.ui.disabledTabs.includes("guide"));

  // Warnings whose field has no visible input (e.g. a zero-cost basis flags "perUnitTotal"; a bad
  // fee-schedule entry flags "feeSchedule") would be dropped; surface them here.
  const inputFields = new Set([
    "marginPct",
    "markupMultiplier",
    "taxRatePct",
    "shippingCharge",
    "shippingCost",
  ]);
  const otherWarnings = $derived(otherPricingWarnings(pw, inputFields));
  const overrideWarn = $derived(partPricingWarning(pw, partIndex, "salePriceOverride"));

  // Per-part prices come from OrderBreakdown.parts[i] (per unit) and item revenue from
  // OrderBreakdown.itemRevenue; pricing.recommendedPrice/resolvedPrice are per-unit with one part but order
  // totals with 2+, so they are never read here.
  const first = $derived(order.parts[0]);
  const overridden = $derived(order.parts.some((x) => x.resolvedPrice !== x.recommendedPrice));
  const recommendedItems = $derived(order.parts.reduce((sum, x) => sum + x.recommendedPrice * x.breakdown.quantity, 0));
</script>

<div class="card" id="pricing-card">
  <div class="head-row with-help">
    <h2>Price and profit</h2>
    <CardHelp id="pricing" title="Price and profit" text={TILE_HELP.pricing} guideAnchor="margin-vs-markup" {guideEnabled} />
  </div>
  <label class="toggle">
    <input type="checkbox" bind:checked={form.sellingEnabled} />
    <span>Set a selling price</span>
  </label>

  {#if form.sellingEnabled && p}
    <div class="field-rows">
      <label class="field">
        <span>Pricing strategy</span>
        <select bind:value={form.strategy}>
          <option value="margin">Margin on price</option>
          <option value="markup">Markup on cost</option>
        </select>
      </label>
      {#if form.strategy === "margin"}
        <LabeledNumber label="Margin" suffix="%" bind:value={form.marginPct} warn={warnFor(pw, "marginPct")} />
      {:else}
        <LabeledNumber label="Markup" suffix="×" step={0.1} bind:value={form.markupMultiplier} warn={warnFor(pw, "markupMultiplier")} />
      {/if}
      <label class="field">
        <span>Fee schedule</span>
        <select bind:value={form.feeScheduleName}>
          <option value="none">None (direct sale)</option>
          {#each Object.keys(cfg.feeSchedules) as name (name)}<option value={name}>{name}</option>{/each}
        </select>
      </label>
      <LabeledNumber label="Tax (included in price)" suffix="%" bind:value={form.taxRatePct} warn={warnFor(pw, "taxRatePct")} />
      <LabeledNumber label="Shipping charged" suffix="customer pays" bind:value={form.shippingCharge} warn={warnFor(pw, "shippingCharge")} />
      <LabeledNumber label="Shipping cost" suffix="you pay" bind:value={form.shippingCost} warn={warnFor(pw, "shippingCost")} />
      <LabeledNumber
        label="Sale price override"
        suffix={multi ? `per unit for ${part.label.trim() || `Part ${partIndex + 1}`}, blank = recommended` : "per unit, blank = recommended"}
        bind:value={part.salePriceOverride}
        warn={overrideWarn}
      />
    </div>

    {#if otherWarnings.length > 0}
      <div class="pricing-warnings" role="alert">
        {#each otherWarnings as w, i (`${w.field}-${w.code}-${i}`)}
          <p class={w.severity === "warning" ? "advisory-text" : "warn-text"}>{friendlyMessage(w)}</p>
        {/each}
      </div>
    {/if}

    {#if shippingDelta < 0}
      <p class="advisory-text">Shipping is undercharged by {money(-shippingDelta, cfg)}. The difference comes out of profit.</p>
    {/if}

    <p class="pricing-basis">
      Priced by {p.strategy === "margin" ? "margin on price" : "markup on cost"}{overridden ? ", price overridden" : ""}
    </p>

    <dl class="pricing-out measure-cap" class:invalid={!p.valid}>
      {#if multi}
        <div><dt>Recommended items total</dt><dd>{p.valid ? money(recommendedItems, cfg) : "-"}</dd></div>
        {#if overridden}
          <div><dt>Your items total</dt><dd>{p.valid ? money(order.itemRevenue, cfg) : "-"}</dd></div>
        {/if}
      {:else}
        <div><dt>Recommended price (per unit)</dt><dd>{p.valid && first ? money(first.recommendedPrice, cfg) : "-"}</dd></div>
        {#if overridden}
          <div><dt>Your price</dt><dd>{p.valid && first ? money(first.resolvedPrice, cfg) : "-"}</dd></div>
        {/if}
      {/if}
      <div><dt>Revenue (including shipping)</dt><dd>{p.valid ? money(p.revenue, cfg) : "-"}</dd></div>
      <div class="sub"><dt>Product cost</dt><dd>{p.valid ? `-${money(order.batchTotal, cfg)}` : "-"}</dd></div>
      <div class="sub"><dt>Shipping cost</dt><dd>{p.valid ? `-${money(p.shippingCost, cfg)}` : "-"}</dd></div>
      {#each p.feeLines as f, i (i)}
        <div class="sub"><dt>Fee: {f.label}</dt><dd>{p.valid ? `-${money(f.amount, cfg)}` : "-"}</dd></div>
      {/each}
      {#if p.tax > 0}<div class="sub"><dt>Tax (included in price)</dt><dd>{p.valid ? `-${money(p.tax, cfg)}` : "-"}</dd></div>{/if}
      <div class="net" class:loss={p.netProfit < 0}>
        <dt>Net profit</dt>
        <dd>{p.valid ? `${money(p.netProfit, cfg)} (${p.revenue > 0 ? `${p.achievedMarginPct.toFixed(1)}%` : "n/a"})` : "-"}</dd>
      </div>
    </dl>
  {/if}
</div>

<style>
  .pricing-warnings { margin-top: 0.6rem; }
  .pricing-warnings .warn-text { margin: 0.2rem 0; }
  .pricing-basis { margin: 0.4rem 0 0; font-size: 0.85rem; color: var(--muted); }
  .pricing-out { margin: 0.8rem 0 0; }
  .pricing-out div { display: flex; justify-content: space-between; gap: 0.5rem; padding: 0.25rem 0; border-top: 1px solid var(--border); }
  .pricing-out .sub dt { color: var(--muted); padding-left: 0.8rem; }
  .pricing-out .net { font-weight: 700; font-size: 1.05rem; }
  .pricing-out .net.loss dd { color: var(--danger); }
  .pricing-out.invalid { opacity: 0.6; }
  .pricing-out.invalid .net { border-left: 2px solid var(--danger); padding-left: 0.5rem; }
  dl div dt { margin: 0; } dl div dd { margin: 0; font-variant-numeric: tabular-nums; white-space: nowrap; }
</style>
