<script lang="ts">
  import type { Snippet } from "svelte";

  // Lays fields out in as many columns as fit, one column on a phone. Labels stay above their controls (D9),
  // and the gap follows the density tokens.
  let { children }: { children: Snippet } = $props();
</script>

<div class="form-grid">{@render children()}</div>

<style>
  .form-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, var(--field-s)), 1fr));
    gap: var(--gap);
    align-items: start;
  }
</style>
