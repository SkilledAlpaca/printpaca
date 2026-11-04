<script lang="ts">
  import { tick } from "svelte";
  import type { EffectiveConfig, OrderBreakdown } from "../lib/engine/index";
  import { quote, partLabel } from "../lib/state.svelte";
  import { money } from "../lib/format";
  import { quoteSummaryStats } from "../lib/quoteSummary";
  import { quoteBlockers } from "../lib/quoteBlockers";
  import { goToBlocker } from "../lib/goToBlocker";
  import SlicerHelp from "./SlicerHelp.svelte";

  let {
    cfg,
    order,
    previewStatus = "idle",
    previewError = "",
  }: {
    cfg: EffectiveConfig;
    order: OrderBreakdown;
    /** The server preview's state (QuotePreviewState["status"]); only "pending" and "error" show anything. */
    previewStatus?: string;
    /** The full message to show while previewStatus is "error". */
    previewError?: string;
  } = $props();

  // "Updating price…" appears only once the preview has been pending for PENDING_DELAY_MS, so a fast round trip
  // (the usual case while stepping a field) never flashes text. The slot below is always rendered, so showing
  // or hiding the text never changes the page height.
  const PENDING_DELAY_MS = 300;
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
    quoteBlockers(order, quote.parts.map((p, i) => partLabel(p, i))).filter((b) => b.missing),
  );
  const need = $derived(missing.find((b) => b.field === "timeHours" || b.field === "timeMinutes") ?? missing[0]);
  const needsTime = $derived(need !== undefined && (need.field === "timeHours" || need.field === "timeMinutes"));
  const needText = $derived.by(() => {
    if (need === undefined) return "";
    const rest = missing.length > 1 ? ` and ${missing.length - 1} more` : "";
    // Multi-part labels already carry the part name: "Print time (Part 2)".
    const what = needsTime ? need.label.replace(/^Print time/, "a print time") : need.label.toLowerCase();
    return `Price needs ${what}${rest}`;
  });

  // Opens the pricing panel and scrolls to it: the panel can be several tiles down, so a click that only
  // flips the toggle leaves the bar visibly unchanged.
  async function setPrice() {
    quote.order.sellingEnabled = true;
    await tick();
    const card = document.getElementById("pricing-card");
    card?.scrollIntoView({ behavior: matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth", block: "start" });
    // preventScroll: a plain .focus() jumps instantly and cancels the smooth scroll started above.
    card?.querySelector<HTMLInputElement>('input[type="checkbox"]')?.focus({ preventScroll: true });
  }
</script>

<div class="card summary" role="group" aria-label="Quote summary">
  <div class="group">
    <span class="section-label">Your cost</span>
    <div class="cost-stats">
      <div class="stat cost">
        <span class="stat-label">{order.parts.length > 1 ? `Order of ${order.parts.length} parts` : s.quantity > 1 ? `Batch of ${s.quantity}` : "This print"}</span>
        <span class="stat-value">{s.costCents !== null ? money(s.costCents, cfg) : "-"}</span>
      </div>
      {#if s.quantity > 1}
        <div class="stat per-unit">
          <span class="stat-label">Per unit</span>
          <span class="stat-value">{s.costCents !== null ? money(order.parts[0]?.breakdown.perUnitTotal ?? 0, cfg) : "-"}</span>
        </div>
      {/if}
    </div>
    <!-- The line is always reserved, so a field filling in doesn't pull the cards below it upward. -->
    <div class="cost-need">
      {#if need}
        <a
          href="#main"
          onclick={(e) => {
            e.preventDefault();
            void goToBlocker(need);
          }}>{needText}</a>
        {#if needsTime}<SlicerHelp />{/if}
      {/if}
    </div>
  </div>
  <div class="group sell">
    <span class="section-label">Client price</span>
    {#if s.pricingEnabled}
      <div class="stats">
        <div class="stat">
          <span class="stat-label">Price</span>
          <span class="stat-value">{s.priceCents !== null ? money(s.priceCents, cfg) : "-"}</span>
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
      <button type="button" class="set-price" onclick={setPrice}>
        Set a price
      </button>
    {/if}
  </div>
  <!-- Fixed-height status slot (always rendered, text swaps, height doesn't): a status line that came and went
       above the cards shifted everything below on every keystroke. The status region stays in the DOM so
       screen readers announce its text changes; the error is a separate alert. -->
  <div class="status-slot">
    <p class="status-text" role="status">{pendingVisible ? "Updating price…" : ""}</p>
    {#if previewStatus === "error" && previewError}
      <p class="status-text status-error" role="alert">{previewError}</p>
    {/if}
  </div>
</div>

<style>
  .summary {
    display: flex;
    flex-wrap: wrap;
    align-items: stretch;
    gap: 1rem 2rem;
    margin-bottom: 0.75rem;
  }
  /* One line tall at all times (the --text-sm line box), spanning the card's full width. An error message may
   * wrap onto more lines on a narrow screen; that is the one case the slot grows. */
  .status-slot { flex: 0 0 100%; min-height: calc(var(--text-sm) * 1.5); margin-top: -0.4rem; }
  .status-text { margin: 0; font-size: var(--text-sm); line-height: 1.5; color: var(--muted); }
  .status-text.status-error { color: var(--danger); }
  .group { display: flex; flex-direction: column; gap: 0.3rem; }
  .cost-need { min-height: calc(var(--text-sm) * 1.5); font-size: var(--text-sm); }
  .group.sell {
    border-left: 1px solid var(--border);
    padding-left: 2rem;
    flex: 1;
  }
  /* The left border is a column divider while the groups share a row; when the bar wraps it is a top rule. */
  @media (max-width: 640px) {
    .group.sell { border-left: none; border-top: 1px solid var(--border); padding-left: 0; padding-top: 0.6rem; }
  }
  .stats { display: flex; flex-wrap: wrap; gap: 1rem 2rem; }
  .cost-stats { display: flex; flex-wrap: wrap; align-items: baseline; gap: 0.5rem 1.5rem; }
  .stat { display: flex; flex-direction: column; gap: 0.15rem; min-width: 90px; }
  .stat-label { font-size: var(--text-sm); color: var(--muted); }
  .stat-value { font-size: var(--text-xl); font-weight: 700; font-variant-numeric: tabular-nums; }
  .stat.cost .stat-value { font-size: var(--text-2xl); }
  .stat.per-unit { min-width: 0; color: var(--muted); }
  .stat.per-unit .stat-value { font-size: var(--text-sm); font-weight: 600; }
  .stat.loss .stat-value { color: var(--danger); }
  .group.sell button { align-self: flex-start; }
  .set-price {
    background: transparent;
    border-color: var(--accent);
    color: var(--accent-text);
  }
</style>
