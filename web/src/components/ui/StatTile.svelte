<script lang="ts">
  // One number with its label and period, kept together (plan: "A Dashboard tile shows its label, value, and period
  // together"). Three forms share one look: a plain tile, a link tile (`href`) that opens the page behind the
  // number, and a toggle tile (`pressed` given) that filters a list and reports its state with aria-pressed. The
  // value is set in tabular figures so a row of tiles lines up. `caption` is secondary text under the value
  // (for example "40% invoiced").
  let {
    label,
    value,
    period,
    caption,
    href,
    pressed,
    onclick,
  }: {
    label: string;
    value: string | number;
    period?: string;
    caption?: string;
    href?: string;
    /** Makes the tile a toggle button; omit for a plain or link tile. */
    pressed?: boolean;
    onclick?: () => void;
  } = $props();

  const cue = $derived(href ? "Open" : pressed !== undefined ? (pressed ? "Showing" : "Show") : "");
</script>

{#snippet body()}
  <span class="label">{label}</span>
  <span class="value">{value}</span>
  {#if period}<span class="meta">{period}</span>{/if}
  {#if caption}<span class="meta">{caption}</span>{/if}
  <!-- A visible cue that the tile is pressable (A9). Hidden from assistive technology: aria-pressed already says
       the state, and the cue would only lengthen the tile's name. -->
  {#if cue}<span class="cue" aria-hidden="true">{cue}</span>{/if}
{/snippet}

{#if href}
  <a class="stat-tile interactive" {href}>{@render body()}</a>
{:else if pressed !== undefined}
  <button type="button" class="stat-tile interactive toggle" class:pressed aria-pressed={pressed} {onclick}>{@render body()}</button>
{:else}
  <div class="stat-tile">{@render body()}</div>
{/if}

<style>
  .stat-tile {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: var(--space-1);
    min-width: 0;
    padding: var(--tile-pad);
    background: var(--card-bg);
    border: 0;
    border-radius: var(--radius);
    color: inherit;
    font: inherit;
    text-align: left;
    text-decoration: none;
  }
  .label {
    color: var(--muted);
    font-size: var(--type-secondary);
    font-weight: 600;
    line-height: 1.2;
    overflow-wrap: anywhere;
  }
  .value {
    font-size: var(--type-title);
    font-weight: 700;
    font-variant-numeric: tabular-nums lining-nums;
    line-height: 1.2;
    overflow-wrap: anywhere;
  }
  .meta {
    color: var(--muted);
    font-size: var(--type-caption);
    font-variant-numeric: tabular-nums;
  }
  .interactive { cursor: pointer; border: 1px solid var(--border); }
  .interactive:focus-visible { outline-offset: 2px; }
  .cue {
    margin-top: auto;
    color: var(--accent-text, var(--accent));
    font-size: var(--type-caption);
    font-weight: 600;
  }
  .interactive:hover .cue { text-decoration: underline; }
  .interactive:hover { background: var(--surface-2); }
  .interactive:hover .label { color: var(--text); }
  /* The pressed state is a tint plus an accent bar under the tile, so it never relies on color alone: aria-pressed
   * carries the state for assistive technology, the bar and the heavier label for sighted users. */
  .toggle.pressed {
    background: var(--surface-2);
    box-shadow: inset 0 calc(-1 * var(--space-1)) 0 var(--accent);
  }
  .toggle.pressed .label { color: var(--text); }
</style>
