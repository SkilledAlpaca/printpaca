<script lang="ts">
  import type { Snippet } from "svelte";

  // Lays fields out in as many columns as fit, one column on a phone. Labels stay above their controls (D9), and
  // the gap follows the density tokens.
  //
  // `align="controls"` makes the Fields that share a row share their rows too: each Field becomes a subgrid of
  // four rows (label, visible hint, control, error), so the controls start at the same height however long a
  // neighbour's label or visible hint runs (a tooltip hint takes no row), and an error under one control leaves the others where they are. Use it for a row of
  // Fields; its children must be Fields. Below 600px the grid is one column, so the rows are the field's own.
  let { align = "default", children }: { align?: "default" | "controls"; children: Snippet } = $props();
</script>

<div class="form-grid" class:aligned={align === "controls"}>{@render children()}</div>

<style>
  .form-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, var(--field-s)), 1fr));
    gap: var(--gap);
    align-items: start;
  }
  /* The row gap moves into the fields (a margin under each field's last row), so an empty hint or error row
   * adds no space. */
  .aligned { row-gap: 0; }
  .aligned > :global(.ui-field) {
    display: grid;
    grid-template-rows: subgrid;
    grid-row: span 4;
    row-gap: 0;
    margin-block-end: var(--gap);
  }
  .aligned > :global(.ui-field) > :global(.ui-field-head) { grid-row: 1; margin-block-end: var(--space-1); }
  .aligned > :global(.ui-field) > :global(small[id$="-hint"]) { grid-row: 2; margin-block-end: var(--space-1); }
  .aligned > :global(.ui-field) > :global(.ui-field-control) {
    grid-row: 3;
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
    min-width: 0;
  }
  .aligned > :global(.ui-field) > :global(small[id$="-err"]) { grid-row: 4; margin-block-start: var(--space-1); }
</style>
