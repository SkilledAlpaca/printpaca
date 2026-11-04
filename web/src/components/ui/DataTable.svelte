<script lang="ts" module>
  export interface DataColumn {
    key: string;
    label: string;
    /** Right-aligns the column in tabular figures (money, quantities). */
    num?: boolean;
    /** Extra class on the header cell, for a caller's own column rules. */
    class?: string;
    /** Keeps the header for assistive technology but hides it visually (a selection or actions column). */
    hideLabel?: boolean;
  }
</script>

<script lang="ts">
  import type { Snippet } from "svelte";
  import Card from "./Card.svelte";
  import LoadMoreRow from "../LoadMoreRow.svelte";

  // A long list in a scroll region (D24). The card holds, top to bottom: the filter and status-chip slot
  // (`filters`), the region (about 12 rows tall, so the page does not grow with the list), and the count and
  // Load more (`more`). The column header is sticky inside the region. The region is a keyboard stop with an
  // accessible name so the list scrolls without a mouse, and focus moving into a row scrolls it clear of the
  // sticky header (scroll-padding on the region, scroll-margin on the row's focusable parts).
  //
  // The caller renders the <tr>s (`children`), so rows keep their own cells, buttons and live status. Below
  // 600px the table becomes a card list inside the same region: the header is visually hidden, not removed,
  // and each <tr> becomes a block whose cells the caller lays out with its own grid areas.
  let {
    label,
    columns,
    rows = 12,
    filters,
    children,
    more,
    loadMore,
  }: {
    /** Accessible name of the scroll region ("Invoices"). */
    label: string;
    columns: DataColumn[];
    /** How many rows tall the region is before it scrolls. */
    rows?: number;
    filters?: Snippet;
    /** The <tr>s of the body: data rows, or a single empty/loading row. */
    children: Snippet;
    /** Extra content in the footer, after the count (for example a bulk-action bar). */
    more?: Snippet;
    /** Count and Load more. Omit while a filter is active: a filter must not run over a partial list. */
    loadMore?: { shown: number; total: number; loadingMore: boolean; onLoadMore: () => void } | null;
  } = $props();

  let headHeight = $state(0);
</script>

<Card>
  {#if filters}<div class="dt-filters">{@render filters()}</div>{/if}
  <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
  <div
    class="dt-scroll"
    tabindex="0"
    role="region"
    aria-label={label}
    style:--dt-rows={rows}
    style:--dt-head-h="{headHeight}px"
  >
    <table>
      <thead bind:offsetHeight={headHeight}>
        <tr>
          {#each columns as col (col.key)}
            <th class={col.class} class:num={col.num} scope="col">{#if col.hideLabel}<span class="visually-hidden">{col.label}</span>{:else}{col.label}{/if}</th>
          {/each}
        </tr>
      </thead>
      <tbody>{@render children()}</tbody>
    </table>
  </div>
  {#if loadMore}<LoadMoreRow {...loadMore} />{/if}
  {#if more}{@render more()}{/if}
</Card>

<style>
  .dt-filters { display: flex; flex-direction: column; gap: var(--gap); margin-bottom: var(--gap); }

  /* About `rows` rows tall, from the control height and the cell padding so it follows the density, and never
   * more than three quarters of the viewport so a short screen still reaches the controls below. */
  .dt-scroll {
    --row-h: calc(var(--control-h) + var(--cell-pad-y) * 2);
    max-height: min(calc(var(--dt-rows) * var(--row-h) + var(--dt-head-h)), 75dvh);
    overflow: auto;
    scroll-padding-top: var(--dt-head-h);
    position: relative;
  }
  .dt-scroll:focus-visible { outline-offset: -2px; }

  /* Sticky header: sticks to the top of this scroll container, so the container (not the page) must be the
   * one that scrolls. An opaque card-colored fill keeps rows from showing through; the rule under it is a
   * shadow because collapsed borders do not travel with a sticky cell. */
  thead th {
    position: sticky;
    top: 0;
    z-index: 1;
    background: var(--card-bg);
    border-top: 0;
    box-shadow: inset 0 -1px 0 var(--border);
  }
  tbody :global(:is(tr, a, button, input, select, textarea)) { scroll-margin-top: var(--dt-head-h); }

  @media (max-width: 600px) {
    .dt-scroll :global(table), .dt-scroll :global(tbody) { display: block; }
    .dt-scroll thead {
      position: absolute;
      width: 1px;
      height: 1px;
      overflow: hidden;
      clip-path: inset(50%);
      white-space: nowrap;
    }
    .dt-scroll :global(tbody td) { display: block; padding: 0; border: 0; min-width: 0; }
  }
</style>
