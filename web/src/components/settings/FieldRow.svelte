<script lang="ts">
  import type { Snippet } from "svelte";

  // One editable row of a settings list: a FormGrid of Fields and a Remove button at its end. Rows wrap on a
  // desktop and stack on a phone. The row owns only layout and the Remove button; the caller owns the data and
  // moves focus after a removal (a11yFocus.focusAfterRowRemoval, keyed by `focusKey`). The Remove button's name
  // says which row it removes.
  let {
    removeLabel,
    focusKey,
    onremove,
    disabled = false,
    children,
  }: {
    removeLabel: string;
    focusKey: string;
    onremove: () => void;
    disabled?: boolean;
    children: Snippet;
  } = $props();
</script>

<div class="field-row">
  <div class="field-row-fields">{@render children()}</div>
  <button type="button" aria-label={removeLabel} data-focus-key={focusKey} {disabled} onclick={onremove}>Remove</button>
</div>

<style>
  .field-row { display: flex; flex-wrap: wrap; align-items: end; gap: var(--space-2) var(--gap); }
  .field-row-fields { flex: 1 1 var(--field-l); min-width: 0; }
</style>
