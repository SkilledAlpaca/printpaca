<script lang="ts">
  import type { Snippet } from "svelte";
  import { tabIds, tabTarget } from "./tabs";

  // An ARIA tablist. Arrow keys move between tabs (and wrap), Home and End jump to the first and last, and the
  // focused tab is selected as it takes focus. Only the selected tab is in the Tab order; Tab from it leaves the
  // list for the panel. `after` renders beside the tabs inside the list's row (an "+ Add part" button): it is
  // outside the tablist element, so it never counts as a tab. The panel is this component's `children`, labelled
  // by the selected tab. Each tab carries data-row-id so a caller can focus a surviving tab after removing one
  // (lib/a11yFocus.ts).
  export interface TabItem {
    id: string;
    label: string;
    /** Secondary text after the label (a quantity), part of the tab's name. */
    meta?: string;
  }

  let {
    tabs,
    selected,
    onselect,
    label,
    idPrefix,
    after,
    children,
  }: {
    tabs: TabItem[];
    selected: string;
    onselect: (id: string) => void;
    /** Names the tablist for assistive technology. */
    label: string;
    /** Prefix for the generated tab and panel ids; unique per tablist on the page. */
    idPrefix: string;
    after?: Snippet;
    children?: Snippet;
  } = $props();

  let listEl: HTMLElement | undefined = $state();

  // Keep the selected tab fully inside the scrolling strip (on click, arrow keys, Home and End, and when parts are
  // added or removed). Scrolls the strip itself, never the page.
  $effect(() => {
    const el = listEl;
    if (!el) return;
    const sel = selected;
    void tabs.length;
    const tab = el.querySelector<HTMLElement>(`[data-row-id="${CSS.escape(sel)}"]`);
    if (!tab) return;
    const left = tab.offsetLeft;
    const right = left + tab.offsetWidth;
    if (left < el.scrollLeft) el.scrollLeft = left;
    else if (right > el.scrollLeft + el.clientWidth) el.scrollLeft = right - el.clientWidth;
  });

  function onkeydown(e: KeyboardEvent) {
    const current = tabs.findIndex((t) => t.id === selected);
    const target = tabTarget(e.key, current < 0 ? 0 : current, tabs.length);
    if (target === null) return;
    e.preventDefault();
    const next = tabs[target];
    if (!next) return;
    onselect(next.id);
    listEl?.querySelector<HTMLElement>(`[data-row-id="${CSS.escape(next.id)}"]`)?.focus();
  }
</script>

<div class="tabs">
  <div class="tabs-row">
    <div class="tablist" role="tablist" aria-label={label} aria-orientation="horizontal" bind:this={listEl}>
      {#each tabs as tab (tab.id)}
        {@const ids = tabIds(idPrefix, tab.id)}
        {@const on = tab.id === selected}
        <button
          type="button"
          role="tab"
          id={ids.tab}
          class="tab"
          class:on
          aria-selected={on}
          aria-controls={ids.panel}
          tabindex={on ? 0 : -1}
          data-row-id={tab.id}
          onclick={() => onselect(tab.id)}
          {onkeydown}
        >
          <span class="tab-label">{tab.label}</span>{#if tab.meta}<span class="tab-meta"> {tab.meta}</span>{/if}
        </button>
      {/each}
    </div>
    {#if after}<div class="tabs-after">{@render after()}</div>{/if}
  </div>
  <div class="tabpanel" role="tabpanel" id={tabIds(idPrefix, selected).panel} aria-labelledby={tabIds(idPrefix, selected).tab}>
    {#if children}{@render children()}{/if}
  </div>
</div>

<style>
  .tabs { display: flex; flex-direction: column; gap: var(--gap); min-width: 0; }
  .tabs-row { display: flex; align-items: flex-end; gap: var(--space-2); min-width: 0; }
  /* Scrolls sideways when many tabs don't fit, instead of wrapping onto a second row of tabs. */
  .tablist { position: relative; display: flex; gap: var(--space-1); min-width: 0; overflow-x: auto; overscroll-behavior-x: contain; scrollbar-width: thin; }
  /* Touch screens scroll by swipe, so the strip's scrollbar is hidden there; desktop keeps the thin one. */
  @media (pointer: coarse) {
    .tablist { scrollbar-width: none; }
    .tablist::-webkit-scrollbar { display: none; }
  }
  .tabs-after { flex: none; }
  .tab {
    flex: none;
    max-width: var(--field-m);
    border: 0;
    border-bottom: 3px solid transparent;
    border-radius: var(--radius) var(--radius) 0 0;
    background: transparent;
    color: var(--muted);
    font-weight: 600;
    padding: 0 var(--space-3);
    white-space: nowrap;
  }
  .tab-label { overflow: hidden; text-overflow: ellipsis; display: inline-block; vertical-align: bottom; max-width: var(--field-s); }
  .tab-meta { font-weight: 400; margin-inline-start: var(--space-1); }
  .tab:hover { background: var(--surface-2); color: var(--text); }
  .tab.on { background: var(--card-bg); color: var(--text); border-bottom-color: var(--accent-text); }
  .tabpanel { display: flex; flex-direction: column; gap: var(--gap-section); min-width: 0; }
</style>
