<script lang="ts">
  import type { Snippet } from "svelte";
  import { focusOnMount } from "../lib/a11yFocus";
  import { recordTitle } from "../lib/recordTitle";

  // The one h1 of a record's detail view (WCAG 1.3.1, 2.4.6). It takes focus when the view opens (2.4.3), so a
  // screen reader announces the record that just replaced the list. Styled like the card headings it replaced.
  let { children }: { children: Snippet } = $props();

  // The h1's text is also the record segment of document.title (WCAG 2.4.2); see lib/recordTitle.ts.
</script>

<h1 class="detail-title" tabindex="-1" use:focusOnMount use:recordTitle>{@render children()}</h1>

<style>
  .detail-title { font-size: var(--text-lg); margin: 0 0 var(--space-3) 0; }
</style>
