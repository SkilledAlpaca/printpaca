<script lang="ts" module>
  export interface KeyValueRow {
    label: string;
    value: string;
    /** Renders the value as a link (a mailto: for an email address). */
    href?: string;
    /** The one amount that matters in the list. */
    strong?: boolean;
  }
</script>

<script lang="ts">
  // A description list of read-only facts: a fixed label column with the value beside it in tabular figures so
  // amounts line up (D28). Dividers sit between rows, the one place D6 allows them. On a phone the label
  // stacks above its value and reading order is unchanged.
  let { rows, label }: { rows: KeyValueRow[]; label?: string } = $props();
</script>

<dl class="kv" aria-label={label}>
  {#each rows as row (row.label)}
    <dt>{row.label}</dt>
    <dd class:strong={row.strong}>{#if row.href}<a href={row.href}>{row.value}</a>{:else}{row.value}{/if}</dd>
  {/each}
</dl>

<style>
  /* Spans the full card width (D28): the label column is a fixed field-size token wide and values sit beside
   * it, so every table of facts lines up the same way. The block bleeds out to the card's edge by the card's
   * own padding (--card-pad, which theme.css lowers to --space-4 on a phone) so the row dividers run edge to
   * edge, and the cells put that padding back. Place it directly in a Card. */
  .kv {
    --bleed: var(--card-pad);
    display: grid;
    grid-template-columns: var(--field-s) minmax(0, 1fr);
    margin: 0 calc(var(--bleed) * -1);
  }
  dt, dd { margin: 0; padding: var(--cell-pad-y) 0; border-top: 1px solid var(--border); }
  dt { padding-inline: var(--bleed) var(--space-4); color: var(--muted); font-size: var(--type-secondary); overflow-wrap: anywhere; }
  dd { padding-inline-end: var(--bleed); font-variant-numeric: tabular-nums; overflow-wrap: anywhere; white-space: pre-line; }
  dt:first-of-type, dt:first-of-type + dd { border-top: 0; }
  dd.strong { font-weight: 700; }
  @media (max-width: 600px) {
    .kv { grid-template-columns: minmax(0, 1fr); }
    dt { padding-bottom: 0; border-top: 1px solid var(--border); }
    dd { border-top: 0; padding-top: var(--space-1); }
    dt:first-of-type { border-top: 0; }
  }
</style>
