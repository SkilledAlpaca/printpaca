<script lang="ts">
  import type { Snippet } from "svelte";
  import { stickyInset } from "../../lib/stickyInset";

  // The summary beside a long form. At `--summary-min` (1100px) and wider it is a column that sticks to the top
  // of the scroll container while the form scrolls, and its content (`children`) is always shown. Below that it
  // is a compact bar pinned to the bottom of the scroll container (`compact`: the headline numbers and the one
  // primary action), with an Expand button that shows the full content above the bar. The bar publishes its
  // height as --sticky-bottom-h (lib/stickyInset.ts), so the page's scroll-padding keeps a focused field out
  // from under it. `children` stays mounted when collapsed (hidden), so the state and registrations of
  // whatever it holds survive. The component renders one element that is a direct child of the layout: the
  // caller's layout places it (grid area `summary` from 1100px, last flex item below).
  let {
    label,
    compact,
    children,
    class: className = "",
  }: {
    /** Names the group. */
    label: string;
    /** The pinned bar's content below 1100px. */
    compact: Snippet;
    /** The full summary. */
    children: Snippet;
    /** Extra class on the root, for the caller's layout to place it. */
    class?: string;
  } = $props();

  const QUERY = "(min-width: 1100px)";
  let wide = $state(typeof matchMedia === "function" ? matchMedia(QUERY).matches : true);
  $effect(() => {
    if (typeof matchMedia !== "function") return;
    const mq = matchMedia(QUERY);
    const sync = () => (wide = mq.matches);
    sync();
    mq.addEventListener("change", sync);
    return () => mq.removeEventListener("change", sync);
  });

  let expanded = $state(false);
  const uid = $props.id();
  const bodyId = `${uid}-body`;
  const showBody = $derived(wide || expanded);
</script>

<div class="summary {className}" class:wide role="group" aria-label={label} use:stickyInset={{ edge: "bottom", active: !wide }}>
  <div class="body" id={bodyId} hidden={!showBody}>{@render children()}</div>
  {#if !wide}
    <div class="bar">
      <div class="bar-content">{@render compact()}</div>
      <button type="button" class="expand" aria-expanded={expanded} aria-controls={bodyId} onclick={() => (expanded = !expanded)}>
        {expanded ? "Collapse" : "Expand"}<span class="visually-hidden"> summary</span>
      </button>
    </div>
  {/if}
</div>

<style>
  .summary { min-width: 0; background: var(--card-bg); }

  /* Narrow: pinned to the bottom of the scroll container, in flow as the layout's last item so it ends the page
   * instead of covering its tail. The full summary opens above the bar (it comes first in the DOM, so focus order matches the picture) and scrolls inside its own height. */
  .summary:not(.wide) {
    position: sticky;
    bottom: 0;
    z-index: 3;
    display: flex;
    flex-direction: column;
    padding: var(--space-2) var(--space-4);
    border-radius: var(--radius) var(--radius) 0 0;
    box-shadow: var(--shadow);
  }
  .summary:not(.wide) .body { max-height: 60dvh; overflow-y: auto; padding-bottom: var(--space-3); }
  .bar { display: flex; align-items: center; gap: var(--space-3); }
  .bar-content { flex: 1 1 auto; min-width: 0; }
  .expand { flex: none; }

  /* Wide: a column that follows the scroll. */
  .summary.wide {
    position: sticky;
    top: var(--space-4);
    align-self: start;
    /* Never taller than the room between its sticky offset and the viewport bottom, less the page footer below
     * the layout: at the end of the page the column stops sticking where its area ends, above the footer, and a
     * taller one would be pushed up and lose its top (the price). The rest scrolls inside it. */
    max-height: calc(100dvh - var(--demo-banner-h, 0px) - var(--space-4) - var(--space-4) - var(--space-12) - var(--space-12));
    overflow-y: auto;
    padding: var(--tile-pad);
    border-radius: var(--radius);
  }
</style>
