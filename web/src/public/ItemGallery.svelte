<script lang="ts">
  import { formatMoney, type CurrencyConfig } from "../lib/engine/index";
  import type { PublicItem } from "./api";

  let {
    items,
    currency,
    onselect,
    oncustomestimate,
  }: {
    items: PublicItem[];
    currency: CurrencyConfig;
    onselect: (item: PublicItem) => void;
    /** Switches to the custom-estimate view. Omitted when that flow isn't available (PublicQuotePage's
     * cfg.materials.length > 0 gate); the trailing CTA card renders only when set. */
    oncustomestimate?: () => void;
  } = $props();

  function outOfStock(item: PublicItem): boolean {
    return item.stock.mode === "in_stock" && (item.stock.qty ?? 0) <= 0 && !item.stock.allowBackorder;
  }
  function stockLabel(item: PublicItem): string {
    if (item.stock.mode === "ready_to_print") return "Made to order";
    const qty = item.stock.qty ?? 0;
    if (qty > 0) return `In stock: ${qty}`;
    return item.stock.allowBackorder ? "Available to order" : "Out of stock";
  }
  // The "from" prefix only applies when an option can raise the price above the card's base;
  // zero/negative-only options mean the price is exact.
  function hasPositiveDelta(item: PublicItem): boolean {
    return item.optionGroups.some((g) => g.options.some((o) => o.deltaCents > 0));
  }
</script>

<div class="grid">
  {#each items as item (item.id)}
    <button
      type="button"
      class="card item-card"
      class:sold-out={outOfStock(item)}
      data-item-id={item.id}
      aria-disabled={outOfStock(item) ? "true" : undefined}
      onclick={() => {
        // aria-disabled, not disabled: a sold-out card stays in the Tab order, so keyboard users learn it
        // exists and is out of stock (WCAG 2.1.1, 1.3.1).
        if (!outOfStock(item)) onselect(item);
      }}
    >
      {#if item.imageUrl}
        <img class="thumb" src={item.imageUrl} alt="" loading="lazy" decoding="async" />
      {:else}
        <div class="thumb thumb-empty" aria-hidden="true"></div>
      {/if}
      <p class="i-name">{item.name}</p>
      {#if item.description}<p class="i-desc">{item.description}</p>{/if}
      <p class="i-price">
        <!-- &nbsp;, not a trailing space: Svelte trims whitespace at the end of an element, which
             rendered "from$9.00". -->
        {#if hasPositiveDelta(item)}<span class="from">from&nbsp;</span>{/if}{formatMoney(item.priceCents, currency)}
      </p>
      <span class="pill stock-badge" class:sold-out-badge={outOfStock(item)}>{stockLabel(item)}</span>
    </button>
  {/each}
  {#if oncustomestimate}
    <button type="button" class="card item-card cta-card" onclick={oncustomestimate}>
      <p class="cta-heading">Need something custom?</p>
      <p class="cta-desc">Upload your model for an instant estimate.</p>
    </button>
  {/if}
</div>

<style>
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(14rem, 1fr));
    gap: var(--space-3);
  }

  /* Overrides the plain <button> rule's layout (theme.css) so the card is a vertical stack; button:disabled
   * already dims an out-of-stock card. */
  .item-card {
    display: flex;
    flex-direction: column;
    align-items: stretch;
    gap: var(--space-1);
    text-align: left;
    width: 100%;
    min-height: 0;
  }

  .thumb {
    width: 100%;
    aspect-ratio: 4 / 3;
    object-fit: cover;
    border-radius: calc(var(--radius) / 2);
    background: var(--surface-2);
  }
  .thumb-empty { border: 1px dashed var(--border); }

  .i-name { margin: var(--space-1) 0 0; font-weight: 600; }
  .i-desc {
    margin: 0;
    color: var(--muted);
    font-size: var(--text-sm);
    display: -webkit-box;
    -webkit-line-clamp: 1;
    line-clamp: 1;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }
  .i-price {
    margin: var(--space-1) 0 0;
    font-weight: 600;
    font-variant-numeric: tabular-nums;
  }
  .from { font-weight: 400; color: var(--muted); }

  .stock-badge { align-self: flex-start; color: var(--muted); }
  /* Sold out: muted card with a full-contrast "Out of stock" badge instead of opacity (opacity would drop text
   * below 4.5:1). */
  .sold-out { cursor: default; background: var(--surface-2); }
  .sold-out .i-name, .sold-out .i-price { color: var(--muted); }
  .sold-out-badge { color: var(--text); border-color: var(--text); }

  /* Dashed accent border and tinted background mark an action tile, not a listing; same size as .item-card */
  .cta-card {
    justify-content: center;
    align-items: flex-start;
    border: 1px dashed var(--accent-text);
    background: color-mix(in srgb, var(--accent) 10%, var(--surface));
  }
  .cta-heading { margin: 0; font-weight: 600; color: var(--accent-text); }
  .cta-desc { margin: 0; color: var(--muted); font-size: var(--text-sm); }
</style>
