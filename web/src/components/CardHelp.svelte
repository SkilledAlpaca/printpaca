<script lang="ts">
  import { helpState, setHelp } from "../lib/help.svelte";

  let {
    id,
    title,
    text,
    guideAnchor,
    guideEnabled = true,
  }: { id: string; title: string; text: string; guideAnchor?: string; guideEnabled?: boolean } = $props();

  const noteId = $derived(`help-${id}`);
  const open = $derived(helpState.open[id] === true);

  function toggle() {
    setHelp(id, !open);
  }
</script>

<button
  type="button"
  class="help-toggle"
  class:open
  aria-expanded={open}
  aria-controls={noteId}
  aria-label={`About ${title}`}
  onclick={toggle}
><span aria-hidden="true">?</span></button>

<!-- Always rendered (hidden, not removed) so a tile's aria-describedby={noteId} always resolves, even
     before first open. -->
<div id={noteId} role="note" class="help-note" hidden={!open}>
  <p>{text}</p>
  {#if guideAnchor && guideEnabled}
    <a href={`#/guide#${guideAnchor}`}>More in the guide</a>
  {/if}
</div>

<style>
  /* Round, not the shared rectangle: a bordered square holding one "?" at the 44px tap target reads as an
   * empty text field next to the heading. A circle is the conventional help affordance at the same size. */
  .help-toggle {
    width: var(--space-12);
    min-width: 44px;
    padding: 0;
    border-radius: 999px;
    line-height: 1;
    font-weight: 700;
    color: var(--muted);
    border-color: var(--border);
  }
  .help-toggle:hover,
  .help-toggle.open {
    color: var(--text);
  }

  /* grid-column: 1 / -1 applies only when the parent is a grid (.tile-head and equivalents in QuoteTab,
   * BreakdownTable, PricingPanel, HistoryPanel); it keeps the note on its own full-width row. */
  .help-note {
    grid-column: 1 / -1;
    background: var(--inset-bg);
    border-left: 3px solid var(--muted);
    border-radius: calc(var(--radius) / 2);
    padding: var(--space-2) var(--space-3);
    font-size: var(--text-sm);
    color: var(--text);
    max-width: var(--measure-prose);
  }
  .help-note p {
    margin: 0;
  }
  .help-note a {
    display: inline-block;
    margin-top: var(--space-1);
  }
</style>
