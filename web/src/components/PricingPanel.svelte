<script lang="ts">
  import type { EffectiveConfig, OrderBreakdown } from "../lib/engine/index";
  import { quote, partLabel, type PartForm } from "../lib/state.svelte";
  import { quoteBlockers, type QuoteBlocker } from "../lib/quoteBlockers";
  import { goToBlocker } from "../lib/goToBlocker";
  import { money, warnFor, friendlyMessage, feeScheduleDisplayName } from "../lib/format";
  import { otherPricingWarnings, partPricingWarning } from "../lib/orderView";
  import LabeledNumber from "./LabeledNumber.svelte";
  import CardHelp from "./CardHelp.svelte";
  import { TILE_HELP } from "../lib/guideContent";

  // `part` is the part whose sale price override the field edits (the active part); the rest is order-level.
  let { cfg, order, part, partIndex }: { cfg: EffectiveConfig; order: OrderBreakdown; part: PartForm; partIndex: number } = $props();

  const form = quote.order;
  const uid = $props.id();
  const strategyId = `${uid}-strategy`;
  const feeId = `${uid}-fee`;
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
  // What blocks pricing, by name. Each entry links to its input; "add" is for blank fields, "fix" for values
  // the server rejected. Empty only when nothing specific is known, which is when the generic line shows.
  const blockers = $derived(quoteBlockers(order, quote.parts.map((x, i) => partLabel(x, i))));
  const missingBlockers = $derived(blockers.filter((b) => b.missing));
  const invalidBlockers = $derived(blockers.filter((b) => !b.missing));

  // The href is only for semantics: following it would rewrite location.hash, the tab router.
  function onBlockerClick(e: MouseEvent, b: QuoteBlocker) {
    e.preventDefault();
    void goToBlocker(b);
  }

  // A deduction shown with a leading minus. A zero amount is plain "$0.00": "-$0.00" reads as a mistake.
  function minus(cents: number): string {
    return cents > 0 ? `-${money(cents, cfg)}` : money(0, cfg);
  }

  // Tax and shipping sit behind "More pricing options": closed for the usual direct sale, open as soon as any of
  // them holds a value (or has a warning), and never closed again by a change underneath the user.
  const hasMoreOptions = $derived(
    (form.taxRatePct ?? 0) !== 0 ||
      (form.shippingCharge ?? 0) !== 0 ||
      (form.shippingCost ?? 0) !== 0 ||
      ["taxRatePct", "shippingCharge", "shippingCost"].some((f) => warnFor(pw, f)),
  );
  let moreOpen = $state(false);
  $effect(() => {
    if (hasMoreOptions) moreOpen = true;
  });

  const first = $derived(order.parts[0]);
  const overridden = $derived(order.parts.some((x) => x.resolvedPrice !== x.recommendedPrice));
  const recommendedItems = $derived(order.parts.reduce((sum, x) => sum + x.recommendedPrice * x.breakdown.quantity, 0));
</script>

{#snippet blockerLinks(list: QuoteBlocker[])}
  {#each list as b, i (b.key)}{#if i > 0}, {/if}<a href="#main" onclick={(e) => onBlockerClick(e, b)}>{b.label}</a>{/each}
{/snippet}

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
      <div class="field">
        <label for={strategyId}>Pricing strategy</label>
        <select id={strategyId} bind:value={form.strategy} aria-describedby="{strategyId}-help">
          <option value="margin">Margin on price</option>
          <option value="markup">Markup on cost</option>
        </select>
        <small id="{strategyId}-help" class="field-hint">
          {form.strategy === "margin"
            ? "Margin: the share of the selling price you keep as profit."
            : "Markup: the selling price is your cost times a number."}
        </small>
      </div>
      {#if form.strategy === "margin"}
        <LabeledNumber label="Margin" suffix="%" bind:value={form.marginPct} warn={warnFor(pw, "marginPct")} help="30% means you keep 30 cents of every dollar charged, before fees." />
      {:else}
        <LabeledNumber label="Markup" suffix="×" step={0.1} bind:value={form.markupMultiplier} warn={warnFor(pw, "markupMultiplier")} help="2.5 means you charge 2.5 times what the print costs you." />
      {/if}
      <div class="field">
        <label for={feeId}>Fee schedule</label>
        <select id={feeId} bind:value={form.feeScheduleName} aria-describedby="{feeId}-help">
          <option value="none">None (direct sale)</option>
          {#each Object.keys(cfg.feeSchedules) as name (name)}<option value={name}>{feeScheduleDisplayName(name)}</option>{/each}
        </select>
        <small id="{feeId}-help" class="field-hint">Marketplace or payment fees, taken out of what the customer pays.</small>
      </div>
      <details class="more-pricing" bind:open={moreOpen}>
        <summary>More pricing options</summary>
        <p class="field-hint">Tax and shipping. Leave them at zero if you don't charge them.</p>
        <div class="field-rows">
        <LabeledNumber label="Tax (included in price)" suffix="%" bind:value={form.taxRatePct} warn={warnFor(pw, "taxRatePct")} help="Sales tax added to the price. It is set aside, not counted as profit." />
        <LabeledNumber label="Shipping charged" suffix="customer pays" bind:value={form.shippingCharge} warn={warnFor(pw, "shippingCharge")} help="What you charge the customer for shipping." />
        <LabeledNumber label="Shipping cost" suffix="you pay" bind:value={form.shippingCost} warn={warnFor(pw, "shippingCost")} help="What the carrier charges you. The difference from the charge comes out of profit." />
        </div>
      </details>
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

    {#if !p.valid}
      {#if blockers.length === 0}
        <p class="warn-text">Pricing is incomplete. Fix the flagged fields above.</p>
      {:else}
        <div class="blockers warn-text">
          {#if missingBlockers.length > 0}
            <p>To price this quote, add: {@render blockerLinks(missingBlockers)}.</p>
          {/if}
          {#if invalidBlockers.length > 0}
            <p>{missingBlockers.length > 0 ? "Also fix" : "To price this quote, fix"}: {@render blockerLinks(invalidBlockers)}.</p>
          {/if}
        </div>
      {/if}
    {/if}
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
      <div class="sub"><dt>Product cost</dt><dd>{p.valid ? minus(order.batchTotal) : "-"}</dd></div>
      <div class="sub"><dt>Shipping cost</dt><dd>{p.valid ? minus(p.shippingCost) : "-"}</dd></div>
      {#each p.feeLines as f, i (i)}
        <div class="sub"><dt>Fee: {f.label}</dt><dd>{p.valid ? minus(f.amount) : "-"}</dd></div>
      {/each}
      {#if p.tax > 0}<div class="sub"><dt>Tax (included in price)</dt><dd>{p.valid ? minus(p.tax) : "-"}</dd></div>{/if}
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
  .blockers { margin-top: 0.6rem; }
  .blockers p { margin: 0.2rem 0; }
  .pricing-basis { margin: 0.4rem 0 0; font-size: 0.85rem; color: var(--muted); }
  .pricing-out { margin: 0.8rem 0 0; }
  .pricing-out div { display: flex; justify-content: space-between; gap: 0.5rem; padding: 0.25rem 0; border-top: 1px solid var(--border); }
  .pricing-out .sub dt { color: var(--muted); padding-left: 0.8rem; }
  .pricing-out .net { font-weight: 700; font-size: 1.05rem; }
  .pricing-out .net.loss dd { color: var(--danger); }
  .pricing-out.invalid .net { border-left: 2px solid var(--danger); padding-left: 0.5rem; }
  dl div dt { margin: 0; } dl div dd { margin: 0; font-variant-numeric: tabular-nums; white-space: nowrap; }
  .more-pricing { grid-column: 1 / -1; }
  .more-pricing summary { cursor: pointer; margin: 0.2rem 0 0.4rem; }
</style>
