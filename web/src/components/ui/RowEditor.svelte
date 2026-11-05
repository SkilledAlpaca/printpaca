<script lang="ts" module>
  export interface RowAction {
    /** Stable id, used for the item's `data-action`. */
    key: string;
    label: string;
    onselect: () => void;
    disabled?: boolean;
    danger?: boolean;
  }
</script>

<script lang="ts">
  import { tick, type Snippet } from "svelte";
  import { computeMenuAnchor, type MenuAnchor } from "../../lib/menuAnchor";
  import { handleMenuKeydown } from "../../lib/menuNav";
  import { announce } from "../../lib/a11y.svelte";
  import { rowActionsLabel, rowMenuKey } from "./rowEditor";

  // A layout component for one editable row of a list (an option group, a choice, a category). It takes the row's
  // fields and its actions and does not own the data: the caller reorders and removes. The fields wrap on a wide
  // screen and stack on a phone. The row's actions live behind a ⋯ button named after the row ("More actions for
  // Color"): Enter, Space or a click opens a labelled group of buttons and focuses the first (a row whose only action is Remove shows a plain Remove button instead); ArrowUp, ArrowDown,
  // Home and End move between them; Escape closes it with focus back on the ⋯ button; Tab closes it and moves on;
  // a click elsewhere or a scroll closes it.
  //
  // Move up and Move down call the caller and then put focus back on this row's ⋯ button, which follows the row
  // to its new place because the button is keyed by `rowKey`, not by position. Remove closes the menu and calls
  // the caller, which must move focus on (see focusAfterRowRemoved in ./rowEditor), because the row that held
  // focus is gone. Further actions (`actions`) are listed after Move and before Remove.
  let {
    label,
    name,
    rowKey,
    fields,
    below,
    notice,
    position,
    actions = [],
    onmoveup,
    onmovedown,
    onremove,
    removeLabel = "Remove",
    removeDisabled = false,
    tone = "default",
  }: {
    /** Names the row for assistive technology: "Group 1: Color". */
    label: string;
    /** What a move announcement calls the row ("Engraving moved to position 1 of 2."), when `label` carries its
     * place in the list and would read wrongly after the move. Defaults to `label`. */
    name?: string;
    /** Stable identity of the row; keys its ⋯ button so focus can find it again. */
    rowKey: string;
    fields: Snippet;
    /** Content under the fields, inside the row (the choices of a group, a delete confirmation). */
    below?: Snippet;
    /** A line shown after the fields and before `below`, for row-level notes. */
    notice?: Snippet;
    /** 0-based place in the list and the list length, to disable Move up at the top and Move down at the bottom and
     * to announce the result. Omit both for a list that cannot be reordered. */
    position?: { index: number; count: number };
    actions?: RowAction[];
    onmoveup?: () => void | Promise<void>;
    onmovedown?: () => void | Promise<void>;
    onremove?: () => void;
    removeLabel?: string;
    removeDisabled?: boolean;
    /** "inset" tints the row with the page color, for a row inside a card. */
    tone?: "default" | "inset";
  } = $props();

  const uid = $props.id();
  const panelId = `${uid}-panel`;
  const focusKey = $derived(rowMenuKey(rowKey));

  let open = $state(false);
  let anchor = $state<MenuAnchor | null>(null);
  let toggleEl = $state<HTMLButtonElement | null>(null);
  let scrollArmed = false;

  // Rough size of the panel, used only to decide whether to flip upward or nudge from the right edge.
  const PANEL_HEIGHT = 200;
  const PANEL_WIDTH = 200;
  const VIEWPORT_MARGIN = 8;

  async function openMenu() {
    if (!toggleEl) return;
    const rect = toggleEl.getBoundingClientRect();
    anchor = computeMenuAnchor(
      rect,
      { width: document.documentElement.clientWidth, height: window.innerHeight },
      { estimatedHeight: PANEL_HEIGHT, estimatedWidth: PANEL_WIDTH, margin: VIEWPORT_MARGIN },
    );
    open = true;
    scrollArmed = false;
    requestAnimationFrame(() => {
      scrollArmed = true;
    });
    await tick();
    document.getElementById(panelId)?.querySelector<HTMLElement>(".menu-item:not([aria-disabled='true']):not([disabled])")?.focus();
  }

  function close(restoreFocus = true) {
    open = false;
    anchor = null;
    if (restoreFocus) toggleEl?.focus();
  }

  function toggle() {
    if (open) close();
    else void openMenu();
  }

  function onrootkeydown(e: KeyboardEvent) {
    if (!open) return;
    const panel = document.getElementById(panelId);
    if (!panel) return;
    if (e.key === "Escape") {
      // Closes only this menu: the event stops here so a page-level Escape (leave the record) does not also run.
      e.preventDefault();
      e.stopPropagation();
      close();
      return;
    }
    if (e.target instanceof Node && (panel.contains(e.target) || e.target === toggleEl)) handleMenuKeydown(e, panel, close);
  }

  // Outside clicks and scrolls close the menu without taking focus back (the user moved on).
  $effect(() => {
    if (!open) return;
    const onpointer = (e: PointerEvent) => {
      if (e.target instanceof Node && (toggleEl?.contains(e.target) || document.getElementById(panelId)?.contains(e.target))) return;
      close(false);
    };
    const onscroll = (e: Event) => {
      if (!scrollArmed) return;
      if (e.target instanceof Element && e.target.closest(".menu-panel")) return;
      close(false);
    };
    window.addEventListener("pointerdown", onpointer, true);
    window.addEventListener("scroll", onscroll, true);
    return () => {
      window.removeEventListener("pointerdown", onpointer, true);
      window.removeEventListener("scroll", onscroll, true);
    };
  });

  function onfocusout(e: FocusEvent) {
    const next = e.relatedTarget;
    if (!(next instanceof Node)) return;
    if (e.currentTarget instanceof Node && e.currentTarget.contains(next)) return;
    close(false);
  }

  async function move(delta: -1 | 1) {
    const handler = delta < 0 ? onmoveup : onmovedown;
    if (!handler) return;
    close(false);
    // Read before the handler: it reorders the list, and `position` is reactive, so afterwards it already holds
    // the new place and adding the step again announced "position 0 of 3" (A14).
    const from = position?.index ?? 0;
    await handler();
    if (position) {
      const to = from + delta;
      announce(`${name ?? label} moved to position ${to + 1} of ${position.count}.`);
    }
    await tick();
    document.querySelector<HTMLElement>(`[data-focus-key="${CSS.escape(focusKey)}"]`)?.focus();
  }

  function select(a: RowAction) {
    if (a.disabled) return;
    close(false);
    a.onselect();
  }

  const canUp = $derived(!position || position.index > 0);
  const canDown = $derived(!position || position.index < position.count - 1);
  // A row whose only action is Remove gets a plain Remove button instead of a one-item menu. It carries the same
  // data-focus-key as the ⋯ button, so focusAfterRowRemoved finds either form.
  const directRemove = $derived(!!onremove && !onmoveup && !onmovedown && actions.length === 0);
  const hasMenu = $derived(!directRemove && (!!onmoveup || !!onmovedown || actions.length > 0 || !!onremove));
</script>

<div class="row-editor" class:inset={tone === "inset"} role="group" aria-label={label}>
  <div class="row-main">
    <div class="row-fields">{@render fields()}</div>
    {#if directRemove}
      <div class="row-menu">
        <button
          type="button"
          class="quiet remove-direct"
          aria-label={`Remove ${label}`}
          aria-disabled={removeDisabled ? "true" : undefined}
          data-focus-key={focusKey}
          onclick={() => { if (!removeDisabled) onremove?.(); }}
        >Remove</button>
      </div>
    {:else if hasMenu}
      <!-- svelte-ignore a11y_no_static_element_interactions -->
      <div class="row-menu" onkeydown={onrootkeydown} onfocusout={onfocusout}>
        <button
          type="button"
          class="quiet menu-toggle"
          bind:this={toggleEl}
          aria-label={rowActionsLabel(label)}
          aria-haspopup="true"
          aria-expanded={open}
          aria-controls={panelId}
          data-focus-key={focusKey}
          onclick={toggle}
        ><span aria-hidden="true">⋯</span></button>
        {#if open && anchor}
          <div
            class="menu-panel"
            id={panelId}
            style={`${anchor.top !== undefined ? `top:${anchor.top}px;` : `bottom:${anchor.bottom}px;`}right:${anchor.right}px;`}
          >
            <div class="menu-group" role="group" aria-label={rowActionsLabel(label)}>
              {#if onmoveup}
                <button type="button" class="quiet menu-item" data-action="move-up" aria-disabled={!canUp} onclick={() => { if (canUp) void move(-1); }}>Move up</button>
              {/if}
              {#if onmovedown}
                <button type="button" class="quiet menu-item" data-action="move-down" aria-disabled={!canDown} onclick={() => { if (canDown) void move(1); }}>Move down</button>
              {/if}
              {#each actions as a (a.key)}
                <button type="button" class="quiet menu-item" class:danger={a.danger} data-action={a.key} aria-disabled={a.disabled ? "true" : undefined} onclick={() => select(a)}>{a.label}</button>
              {/each}
              {#if onremove}
                <button
                  type="button"
                  class="quiet danger menu-item"
                  data-action="remove"
                  aria-disabled={removeDisabled ? "true" : undefined}
                  onclick={() => { if (!removeDisabled) { close(false); onremove(); } }}
                >{removeLabel}</button>
              {/if}
            </div>
          </div>
        {/if}
      </div>
    {/if}
  </div>
  {#if notice}{@render notice()}{/if}
  {#if below}<div class="row-below">{@render below()}</div>{/if}
</div>

<style>
  .row-editor { display: flex; flex-direction: column; gap: var(--space-3); min-width: 0; }
  .row-editor.inset { background: var(--inset-bg); border-radius: var(--radius); padding: var(--space-3) var(--space-4); }
  /* The fields take the room; the ⋯ button sits at the end of the row, level with the controls rather than the
   * labels. On a phone the fields stack and the button drops under them, at the end. */
  .row-main { display: flex; align-items: flex-end; gap: var(--space-3); min-width: 0; }
  .row-fields { flex: 1 1 auto; min-width: 0; display: flex; flex-direction: column; gap: var(--gap); }
  .row-menu { flex: none; align-self: flex-end; }
  .row-below { display: flex; flex-direction: column; gap: var(--space-3); min-width: 0; }

  /* button.quiet sets the small tap size; a row's controls follow the density (40 px comfortable, 32 px compact) and
   * 44 px on a phone in either. */
  .menu-toggle { min-width: var(--tap-sm); font-weight: 700; letter-spacing: 1px; }
  .menu-toggle, .menu-item, .remove-direct { min-height: var(--control-h); }
  .menu-panel {
    position: fixed;
    z-index: 5;
    min-width: var(--field-s);
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
    padding: var(--space-1);
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: calc(var(--radius) / 2);
    box-shadow: var(--shadow);
  }
  .menu-group { display: flex; flex-direction: column; gap: var(--space-1); }
  .menu-item { display: block; width: 100%; text-align: left; white-space: nowrap; }
  .menu-item[aria-disabled="true"] { opacity: 0.5; cursor: default; }

  @media (max-width: 600px) {
    .menu-toggle, .menu-item, .remove-direct { min-height: 44px; }
    .row-main { flex-direction: column; align-items: stretch; }
    .row-menu { align-self: flex-end; }
  }
</style>
