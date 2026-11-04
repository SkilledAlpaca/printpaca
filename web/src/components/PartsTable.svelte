<script lang="ts">
  import type { EffectiveConfig, OrderBreakdown } from "../lib/engine/index";
  import { money } from "../lib/format";
  import ScrollRegion from "./ScrollRegion.svelte";

  // Per-part summary for a multi-part order: cost and, once a selling price is set, revenue. Order-level
  // fees, tax and shipping are in the Price and profit card.
  let { cfg, order }: { cfg: EffectiveConfig; order: OrderBreakdown } = $props();
</script>

<div class="order-by-part">
  <h3>Order by part</h3>
  <ScrollRegion label="Order by part table">
    <table class="parts-table" class:priced={!!order.pricing}>
      <thead>
        <tr>
          <th>Part</th>
          <th class="num">Quantity</th>
          <th class="num">Cost each</th>
          <th class="num">Cost total</th>
          {#if order.pricing}<th class="num">Price each</th><th class="num">Line total</th>{/if}
        </tr>
      </thead>
      <tbody>
        {#each order.parts as p, i (i)}
          {@const ok = p.breakdown.valid}
          {@const priced = order.pricing?.valid === true}
          <tr>
            <td>{p.label}</td>
            <td class="num">{p.breakdown.quantity}</td>
            <td class="num">{ok ? money(p.breakdown.perUnitTotal, cfg) : "-"}</td>
            <td class="num">{ok ? money(p.breakdown.batchTotal, cfg) : "-"}</td>
            {#if order.pricing}
              <td class="num">{ok && priced ? money(p.resolvedPrice, cfg) : "-"}</td>
              <td class="num">{ok && priced ? money(p.itemRevenue, cfg) : "-"}</td>
            {/if}
          </tr>
        {/each}
      </tbody>
      <tfoot class="order-total">
        <tr>
          <!-- One footer cell per body column (the label spans Part and Quantity), so the column-hiding rules
               below apply to the footer by position instead of leaving a stray extra column on a phone. -->
          <th colspan="2" scope="row">Order total</th>
          <td></td>
          <td class="num total-cell">{order.parts.every((p) => p.breakdown.valid) ? money(order.batchTotal, cfg) : "-"}</td>
          {#if order.pricing}
            <td></td>
            <td class="num total-cell">{order.pricing.valid ? money(order.itemRevenue, cfg) : "-"}</td>
          {/if}
        </tr>
      </tfoot>
    </table>
  </ScrollRegion>
</div>

<style>
  /* In a narrow container (the summary column, or a phone): drops the per-unit cost column (and the cost total once prices show) and tightens cells so
   * the table fits without sideways scrolling; costs stay in the summary bar and breakdown card. */
  .order-by-part { min-width: 0; container-type: inline-size; }
  h3 { margin: 0; font-size: var(--type-heading); }
  @container (max-width: 34rem) {
    .parts-table { font-size: var(--text-sm); }
    .parts-table :is(th, td) { padding: var(--space-1); }
    .parts-table thead th:nth-child(3),
    .parts-table tbody td:nth-child(3) { display: none; }
    .parts-table.priced thead th:nth-child(4),
    .parts-table.priced tbody td:nth-child(4),
    .parts-table.priced tfoot td:nth-child(3) { display: none; }
    .parts-table tfoot td:nth-child(2) { display: none; }
  }
  /* Bold, matching the "Order total" row header. */
  .total-cell { font-weight: 700; }
</style>
