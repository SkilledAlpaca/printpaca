<script lang="ts">
  import type { Snippet } from "svelte";
  import { focusOnMount } from "./a11yFocus";
  import { pageTitle } from "../lib/pageTitle.svelte";

  // The one h1 of a record's detail view (WCAG 1.3.1, 2.4.6). It takes focus when the view opens (2.4.3), so a
  // screen reader announces the record that just replaced the list. Styled like the card headings it replaced.
  let { children }: { children: Snippet } = $props();

  // The h1's text is also the record segment of document.title (WCAG 2.4.2): every detail view gets it from
  // this one place. The text can change while open (a client name that loads after "Client"), so observe it,
  // and clear it when the view closes so the list's title is just the tab name.
  function recordTitle(node: HTMLElement) {
    const sync = () => { pageTitle.record = (node.textContent ?? "").trim(); };
    sync();
    const observer = new MutationObserver(sync);
    observer.observe(node, { childList: true, characterData: true, subtree: true });
    return { destroy() { observer.disconnect(); pageTitle.record = ""; } };
  }
</script>

<h1 class="detail-title" tabindex="-1" use:focusOnMount use:recordTitle>{@render children()}</h1>

<style>
  .detail-title { font-size: var(--text-lg); margin: 0 0 var(--space-3) 0; }
</style>
