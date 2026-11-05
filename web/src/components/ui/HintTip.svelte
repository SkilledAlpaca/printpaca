<script lang="ts">
  import { onMount } from "svelte";
  import { placeTip, tipOpen } from "./hintTip";

  // A small "?" circle that shows a field's hint in a tooltip. It is a real button named "Help: <label>".
  // The tooltip opens on pointer hover, on keyboard focus, and on click or tap; Escape closes it; it stays open
  // while the pointer is over it (WCAG 1.4.13: dismissible, hoverable, persistent). The text is always in the
  // DOM under `id`, visually hidden until shown, so the control's aria-describedby keeps resolving to it. The
  // button's name is hidden text, not an aria-label, so a label lookup for the field ("Waste") finds only the control.
  let { id, label, text }: { id: string; label: string; text: string } = $props();

  let hover = $state(false);
  let focus = $state(false);
  let pinned = $state(false);
  let dismissed = $state(false);
  let wrap: HTMLElement | undefined = $state();
  let button: HTMLButtonElement | undefined = $state();
  let tip: HTMLElement | undefined = $state();
  let pos = $state({ left: 0, top: 0, placement: "below" as "below" | "above" });

  const open = $derived(tipOpen({ hover, focus, pinned, dismissed }));

  function reposition() {
    if (!button || !tip) return;
    const a = button.getBoundingClientRect();
    const t = tip.getBoundingClientRect();
    pos = placeTip(a, { width: t.width, height: t.height }, { width: document.documentElement.clientWidth, height: window.innerHeight });
  }

  function close() {
    hover = false;
    focus = false;
    pinned = false;
    dismissed = true;
  }

  $effect(() => {
    if (!open) return;
    reposition();
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      // Only this tooltip closes: a menu or drawer behind it keeps its own Escape.
      e.stopPropagation();
      close();
    };
    const onDown = (e: PointerEvent) => {
      if (wrap && !wrap.contains(e.target as Node)) {
        pinned = false;
      }
    };
    document.addEventListener("keydown", onKey, true);
    document.addEventListener("pointerdown", onDown, true);
    window.addEventListener("resize", reposition);
    window.addEventListener("scroll", reposition, true);
    return () => {
      document.removeEventListener("keydown", onKey, true);
      document.removeEventListener("pointerdown", onDown, true);
      window.removeEventListener("resize", reposition);
      window.removeEventListener("scroll", reposition, true);
    };
  });

  onMount(() => () => {
    pinned = false;
  });

  function onclick() {
    if (pinned) {
      pinned = false;
      dismissed = true;
    } else {
      pinned = true;
      dismissed = false;
    }
  }
</script>

<span
  class="hint-tip"
  bind:this={wrap}
  role="presentation"
  onpointerenter={(e) => {
    // A touch contact fires pointerenter too, but a tap is a click (below), not a hover.
    if (e.pointerType !== "touch") hover = true;
  }}
  onpointerleave={() => {
    hover = false;
    dismissed = false;
  }}
  onfocusin={(e) => {
    // Keyboard focus only: a mouse click also focuses the button, and the click handler owns that case.
    if ((e.target as HTMLElement).matches(":focus-visible")) focus = true;
  }}
  onfocusout={(e) => {
    if (wrap?.contains(e.relatedTarget as Node | null)) return;
    focus = false;
    pinned = false;
    dismissed = false;
  }}
>
  <button type="button" class="hint-button" bind:this={button} {onclick}><span aria-hidden="true">?</span><span class="visually-hidden">Help: {label}</span></button>
  <span
    {id}
    role="tooltip"
    class="hint-tooltip"
    class:visually-hidden={!open}
    class:shown={open}
    data-placement={pos.placement}
    bind:this={tip}
    style={open ? `left:${pos.left}px;top:${pos.top}px` : undefined}
  ><span class="hint-body">{text}</span></span>
</span>

<style>
  .hint-tip { display: inline-flex; align-items: center; }
  /* The button is the hit area, the inner circle the look. The circle is 24px (WCAG 2.5.8); on phones the button grows
   * to 44px, and the negative margin keeps that from making the label row taller. */
  .hint-button {
    min-height: 0;
    inline-size: var(--space-6);
    block-size: var(--space-6);
    padding: 0;
    border: 0;
    border-radius: 999px;
    background: transparent;
    color: var(--text);
    display: inline-flex;
    align-items: center;
    justify-content: center;
  }
  .hint-button > span {
    box-sizing: border-box;
    inline-size: var(--space-6);
    block-size: var(--space-6);
    border: 1px solid var(--control-border);
    border-radius: 999px;
    background: var(--surface);
    display: inline-flex;
    align-items: center;
    justify-content: center;
    font-size: var(--type-caption);
    font-weight: 700;
    line-height: 1;
  }
  .hint-button:hover > span { background: var(--surface-2); }
  @media (max-width: 600px) {
    .hint-button { min-height: 44px; min-width: 44px; margin: calc(var(--space-5) / -2); }
  }
  /* Hidden state: theme.css .visually-hidden, still in the DOM (not display:none) so aria-describedby reads it. */
  .hint-tooltip.shown {
    position: fixed;
    z-index: 300;
    inline-size: max-content;
    max-inline-size: min(var(--field-m), 90%);
    /* The padding is the gap to the button and stays part of the tooltip, so the pointer can cross it. */
    padding-block: var(--space-2);
  }
  .hint-body {
    display: block;
    padding: var(--space-2) var(--space-3);
    border-radius: calc(var(--radius) / 2);
    background: var(--text);
    color: var(--bg);
    font-size: var(--type-secondary);
    font-weight: 400;
    line-height: 1.45;
    box-shadow: var(--shadow);
  }
</style>
