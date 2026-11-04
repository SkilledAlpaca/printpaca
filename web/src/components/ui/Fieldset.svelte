<script lang="ts">
  import type { Snippet } from "svelte";

  // A native <fieldset> and <legend> for controls that answer one question together (hours and minutes of a
  // print time). Borderless: the legend is the group's label, and the fields inside are Fields.
  let { legend, hint, hintId, children }: { legend: string; hint?: string; hintId?: string; children: Snippet } = $props();
</script>

<fieldset aria-describedby={hint ? hintId : undefined}>
  <legend>{legend}</legend>
  {#if hint}<p class="fieldset-hint" id={hintId}>{hint}</p>{/if}
  {@render children()}
</fieldset>

<style>
  fieldset { border: 0; margin: 0; padding: 0; min-width: 0; display: flex; flex-direction: column; gap: var(--space-2); }
  legend { padding: 0; font-size: var(--type-secondary); font-weight: 600; margin-bottom: var(--space-1); }
  .fieldset-hint { margin: 0; color: var(--muted); font-size: var(--type-secondary); }
</style>
