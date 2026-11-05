<script lang="ts">
  import { tick } from "svelte";

  // A screenshot that opens full size in a modal dialog on tap or Enter. The trigger is a button named
  // "Enlarge: <alt>". The dialog is a native <dialog> (showModal), so the page behind it is inert; Tab is wrapped
  // between Close and the scroll region so it never leaves the dialog. Escape and a backdrop click close it, and
  // focus returns to the trigger. The full-size image is rendered only while open, so it adds no request until used.
  let {
    src,
    alt,
    width,
    height,
    imgClass = "",
  }: { src: string; alt: string; width?: number; height?: number; imgClass?: string } = $props();

  let dialog: HTMLDialogElement | undefined = $state();
  let trigger: HTMLButtonElement | undefined = $state();
  let closeBtn: HTMLButtonElement | undefined = $state();
  let scroller: HTMLDivElement | undefined = $state();
  let open = $state(false);

  async function show() {
    open = true;
    await tick();
    dialog?.showModal();
    closeBtn?.focus();
  }
  function onclose() {
    open = false;
    trigger?.focus();
  }
  function onkeydown(e: KeyboardEvent) {
    if (e.key !== "Tab" || !closeBtn || !scroller) return;
    const first = closeBtn;
    const last = scroller;
    const active = document.activeElement;
    if (e.shiftKey && active === first) {
      e.preventDefault();
      last.focus();
    } else if (!e.shiftKey && active === last) {
      e.preventDefault();
      first.focus();
    }
  }
</script>

<button type="button" class="enlarge" aria-label={`Enlarge: ${alt}`} bind:this={trigger} onclick={show}>
  <img class={imgClass} {src} {alt} {width} {height} loading="lazy" decoding="async" />
</button>

<!-- The backdrop is the dialog element itself, so a click whose target is the dialog (not its content) is a
     backdrop click. Escape is handled natively and fires close. -->
<dialog
  class="lightbox"
  bind:this={dialog}
  aria-label={`Enlarged: ${alt}`}
  {onclose}
  {onkeydown}
  onclick={(e) => {
    if (e.target === dialog) dialog?.close();
  }}
>
  {#if open}
    <button type="button" class="lightbox-close" bind:this={closeBtn} onclick={() => dialog?.close()}>Close</button>
    <!-- A scrollable region must be keyboard focusable so arrow keys can scroll it. -->
    <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
    <div class="lightbox-scroll" tabindex="0" role="region" aria-label="Full-size image, scrollable" bind:this={scroller}>
      <img class="lightbox-img" {src} {alt} {width} {height} />
    </div>
  {/if}
</dialog>

<style>
  .enlarge {
    display: block;
    max-width: 100%;
    padding: 0;
    border: 0;
    background: none;
    cursor: zoom-in;
    min-height: 0;
  }
  .enlarge img { display: block; max-width: 100%; height: auto; border-radius: var(--radius); }
  .enlarge:focus-visible { outline: 2px solid var(--focus-ring); outline-offset: 2px; border-radius: var(--radius); }

  .lightbox {
    box-sizing: border-box;
    width: min(100% - var(--space-4), var(--content-max));
    max-width: none;
    max-height: calc(100% - var(--space-4));
    margin: auto;
    padding: var(--space-3);
    border: 0;
    border-radius: var(--radius);
    background: var(--surface);
    color: var(--text);
  }
  .lightbox[open] { display: flex; flex-direction: column; gap: var(--space-3); }
  .lightbox::backdrop { background: rgb(0 0 0 / 0.6); }
  .lightbox-close { align-self: flex-end; flex: none; min-height: 44px; }
  /* The image keeps its natural size; the region scrolls in both directions and allows pinch zoom on phones. */
  .lightbox-scroll {
    flex: 1 1 auto;
    min-height: 0;
    overflow: auto;
    touch-action: pan-x pan-y pinch-zoom;
    overscroll-behavior: contain;
  }
  .lightbox-img { display: block; max-width: none; height: auto; }
</style>
