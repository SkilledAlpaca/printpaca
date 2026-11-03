<script lang="ts">
  import type { Snippet } from "svelte";

  // A horizontally scrolling table wrapper that has no focusable content of its own. Without a tab stop a
  // keyboard user can't scroll it (WCAG 2.1.1), so it is a focusable, labeled region (axe:
  // scrollable-region-focusable). Use it only when the wrapped table has no buttons or inputs.
  let { label, children }: { label: string; children: Snippet } = $props();
</script>

<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
<div class="table-scroll" tabindex="0" role="region" aria-label={label}>
  {@render children()}
</div>
