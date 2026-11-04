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
  // A description list of read-only facts: label on the left, value on the right in tabular figures so amounts
  // line up. Dividers sit between rows, the one place D6 allows them. On a phone the label stacks above its
  // value and reading order is unchanged.
  let { rows, label }: { rows: KeyValueRow[]; label?: string } = $props();
</script>

<dl class="kv" aria-label={label}>
  {#each rows as row (row.label)}
    <dt>{row.label}</dt>
    <dd class:strong={row.strong}>{#if row.href}<a href={row.href}>{row.value}</a>{:else}{row.value}{/if}</dd>
  {/each}
</dl>

<style>
  .kv { display: grid; grid-template-columns: max-content minmax(0, 1fr); margin: 0; max-width: var(--measure-record); }
  dt, dd { margin: 0; padding: var(--cell-pad-y) 0; border-top: 1px solid var(--border); }
  dt:first-of-type, dt:first-of-type + dd { border-top: 0; }
  dt { color: var(--muted); font-size: var(--type-secondary); padding-inline-end: var(--space-6); }
  dd { font-variant-numeric: tabular-nums; overflow-wrap: anywhere; }
  dd.strong { font-weight: 700; }
  @media (max-width: 600px) {
    .kv { grid-template-columns: minmax(0, 1fr); }
    dt { padding-bottom: 0; }
    dd { border-top: 0; padding-top: var(--space-1); }
    dt:first-of-type + dd { padding-top: var(--space-1); }
  }
</style>
