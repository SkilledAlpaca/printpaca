<script lang="ts">
  import { tick } from "svelte";
  import type { EffectiveConfig, OrderBreakdown } from "../lib/engine/index";
  import { quote } from "../lib/state.svelte";
  import { money } from "../lib/format";
  import { quoteSummaryStats } from "../lib/quoteSummary";
  import { needSentence } from "../lib/quoteBlockers";
  import { goToBlocker } from "../lib/goToBlocker";
  import { currentBlockers } from "../lib/currentBlockers";
  import { findFieldByLabel } from "../lib/fieldErrors";

  let {
    cfg,
    order,
    previewStatus = "idle",
    previewError = "",
    variant = "full",
  }: {
    cfg: EffectiveConfig;
    order: OrderBreakdown;
    /** The server preview's state (QuotePreviewState["status"]); only "pending" and "error" show anything. */
    previewStatus?: string;
    /** The full message to show while previewStatus is "error". */
    previewError?: string;
    /** "full" is the summary column; "compact" is the headline numbers for the pinned bottom bar. */
    variant?: "full" | "compact";
  } = $props();

  // "Updating price…" appears only once the preview has been pending for PENDING_DELAY_MS, so a fast round trip
  // (the usual case while stepping a field) never flashes text. The slot below is always rendered, so showing
  // or hiding the text never changes the page height.
  const PENDING_DELAY_MS = 300;
  const PRICE_FIELD_WAIT_MS = 3000;
  let pendingVisible = $state(false);
  $effect(() => {
    if (previewStatus !== "pending") {
      pendingVisible = false;
      return;
    }
    const timer = setTimeout(() => (pendingVisible = true), PENDING_DELAY_MS);
    return () => clearTimeout(timer);
  });

  const s = $derived(quoteSummaryStats(order, quote.order.sellingEnabled));
  const loss = $derived(s.profitCents !== null && s.profitCents < 0);

  // Why the cost shows "-": the first blank field, named here so the reason sits next to the number instead of
  // in a message further down. Print time wins because it is what a model file never supplies.
  const missing = $derived(
    currentBlockers(order).filter((b) => b.missing),
  );
  const need = $derived(missing.find((b) => b.field === "timeHours" || b.field === "timeMinutes") ?? missing[0]);
  const needText = $derived(needSentence(missing));
  // The pinned bar is one short line, not the whole list: the count, or the one thing missing. The full list
  // stays in the expanded summary.
  const compactNeed = $derived(
    missing.length === 1 ? `Needs ${needText.replace(/^Price needs /, "")}.` : `Needs ${missing.length} more values.`,
  );

  // Turns selling on, scrolls to the Price section, and focuses its price target (the Margin or Markup value,
  // whichever the strategy uses): the section can be several tiles down, so a click that only flipped the
  // switch would leave the summary visibly unchanged.
  async function setPrice() {
    quote.order.sellingEnabled = true;
    await tick();
    const card = document.getElementById("pricing-card");
    card?.scrollIntoView({ behavior: matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth", block: "start" });
    // preventScroll: a plain .focus() jumps instantly and cancels the smooth scroll started above.
    // The strategy fields render once the price preview has answered, so wait for them briefly; the switch itself
    // takes focus when they don't come.
    const find = () => (card ? (findFieldByLabel("Margin", card) ?? findFieldByLabel("Markup", card)) : null);
    let target = find();
    for (let waited = 0; !target && waited < PRICE_FIELD_WAIT_MS; waited += 50) {
      await new Promise((r) => setTimeout(r, 50));
      target = find();
    }
    (target ?? card?.querySelector<HTMLInputElement>('input[type="checkbox"]'))?.focus({ preventScroll: true });
  }
</script>

{#snippet needLink()}
  {#if need}
    <a
      href="#main"
      onclick={(e) => {
        e.preventDefault();
        if (need) void goToBlocker(need);
      }}>{needText}</a>
  {/if}
{/snippet}

{#if variant === "compact"}
  <div class="compact">
    {#if s.pricingEnabled}
      <span class="compact-stat"><span class="stat-label">Price</span> <strong>{s.priceCents !== null ? money(s.priceCents, cfg) : "-"}</strong></span>
      <span class="compact-stat" class:loss><span class="stat-label">Profit</span> <strong>{s.profitCents !== null ? money(s.profitCents, cfg) : "-"}</strong></span>
    {:else}
      <span class="compact-stat"><span class="stat-label">Cost</span> <strong>{s.costCents !== null ? money(s.costCents, cfg) : "-"}</strong></span>
    {/if}
    {#if need}
      <span class="compact-need">
        {compactNeed}
        <a
          href="#main"
          aria-label={`Show ${need.label}`}
          onclick={(e) => {
            e.preventDefault();
            if (need) void goToBlocker(need);
          }}>Show</a>
      </span>
    {/if}
  </div>
{:else}
  <div class="summary-stats">
    <div class="group">
      <span class="section-label">{s.pricingEnabled ? "Client price" : "Your cost"}</span>
      {#if s.pricingEnabled}
        <span class="stat-value headline">{s.priceCents !== null ? money(s.priceCents, cfg) : "-"}</span>
      {:else}
        <div class="cost-stats"><div class="stat cost"><span class="stat-value headline">{s.costCents !== null ? money(s.costCents, cfg) : "-"}</span></div></div>
      {/if}
      <span class="context">{order.parts.length > 1 ? `Order of ${order.parts.length} parts` : s.quantity > 1 ? `Batch of ${s.quantity}` : "This print"}</span>
    </div>
    {#if s.pricingEnabled}
      <div class="stats">
        <div class="cost-stats">
          <div class="stat cost">
            <span class="stat-label">Your cost</span>
            <span class="stat-value">{s.costCents !== null ? money(s.costCents, cfg) : "-"}</span>
          </div>
          {#if s.quantity > 1}
            <div class="stat per-unit">
              <span class="stat-label">Per unit</span>
              <span class="stat-value">{s.costCents !== null ? money(order.parts[0]?.breakdown.perUnitTotal ?? 0, cfg) : "-"}</span>
            </div>
          {/if}
        </div>
        <div class="stat" class:loss>
          <span class="stat-label">Profit</span>
          <span class="stat-value">{s.profitCents !== null ? money(s.profitCents, cfg) : "-"}</span>
        </div>
        <div class="stat">
          <span class="stat-label">Margin</span>
          <span class="stat-value">{s.marginPct !== null ? `${s.marginPct.toFixed(1)}%` : "-"}</span>
        </div>
      </div>
    {:else}
      {#if s.quantity > 1}
        <div class="stat per-unit">
          <span class="stat-label">Per unit</span>
          <span class="stat-value">{s.costCents !== null ? money(order.parts[0]?.breakdown.perUnitTotal ?? 0, cfg) : "-"}</span>
        </div>
      {/if}
      <button type="button" class="set-price" onclick={setPrice}>Set a client price</button>
    {/if}
    <!-- One reserved line for the missing-field notice and the "Updating price…" status, so neither shifts what is
         below it. The status region stays in the DOM so screen readers announce its text changes; the error is
         a separate alert. -->
    <div class="notice">
      {#if need}
        <div class="cost-need">{@render needLink()}</div>
      {/if}
      <p class="status-text" role="status">{pendingVisible ? "Updating price…" : ""}</p>
      {#if previewStatus === "error" && previewError}
        <p class="status-text status-error" role="alert">{previewError}</p>
      {/if}
    </div>
  </div>
{/if}

<style>
  .summary-stats { display: flex; flex-direction: column; gap: var(--gap); }
  .compact { display: flex; flex-wrap: wrap; align-items: baseline; gap: var(--space-1) var(--space-4); font-variant-numeric: tabular-nums; }
  .compact-stat strong { font-size: var(--type-heading); }
  /* Never more than two lines of text, however much is missing. */
  .compact-need {
    font-size: var(--type-secondary);
    flex: 1 1 100%;
    display: -webkit-box;
    -webkit-box-orient: vertical;
    -webkit-line-clamp: 2;
    line-clamp: 2;
    overflow: hidden;
  }
  .stat-label { font-size: var(--type-secondary); color: var(--muted); }
  .compact-stat.loss strong { color: var(--danger); }
  .group { display: flex; flex-direction: column; gap: var(--space-1); }
  .stat-value.headline { font-size: var(--text-2xl); font-weight: 700; line-height: 1.1; font-variant-numeric: tabular-nums; }
  .context { font-size: var(--type-secondary); color: var(--muted); }
  /* Your cost, profit and margin under the headline: smaller, in tabular figures so they line up. */
  .stats { display: flex; flex-wrap: wrap; align-items: flex-start; gap: var(--space-2) var(--space-6); font-variant-numeric: tabular-nums; }
  .cost-stats { display: flex; flex-wrap: wrap; align-items: baseline; gap: var(--space-2) var(--space-6); }
  .stats > .cost-stats { display: contents; }
  .stat { display: flex; flex-direction: column; gap: var(--space-1); }
  .stat-value { font-size: var(--type-heading); font-weight: 700; font-variant-numeric: tabular-nums; }
  .stat.per-unit { color: var(--muted); }
  .stat.per-unit .stat-value { font-size: var(--type-body); font-weight: 600; }
  .stat.loss .stat-value { color: var(--danger); }
  .notice { display: flex; flex-direction: column; gap: var(--space-1); }
  .cost-need { font-size: var(--type-secondary); }
  .status-text { margin: 0; font-size: var(--type-secondary); line-height: 1.5; color: var(--muted); }
  .status-text.status-error { color: var(--danger); }
  .set-price { align-self: flex-start; }
</style>
