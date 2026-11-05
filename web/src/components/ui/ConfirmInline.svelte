<script lang="ts">
  import type { Snippet } from "svelte";
  import { confirmFocus } from "../../lib/a11yFocus";

  // Two-step confirmation in place of the button that asked for it. The confirm button states the verb, the
  // object and the count (the caller writes the label: "Yes, delete this request"); an optional note says
  // whether it is permanent. Focus moves to the confirm button on open, Escape closes only this confirmation,
  // and when it closes focus returns to the control matching `restore` (a [data-focus-key] selector) if the
  // trigger is still there. Completion is the caller's job: it must focus a control that still exists.
  let {
    confirmLabel,
    cancelLabel = "Cancel",
    onconfirm,
    oncancel,
    restore,
    danger = false,
    busy = false,
    children,
  }: {
    confirmLabel: string;
    cancelLabel?: string;
    onconfirm: () => void;
    oncancel: () => void;
    restore: string;
    danger?: boolean;
    busy?: boolean;
    children?: Snippet;
  } = $props();
</script>

<span class="confirm-inline">
  <button
    type="button"
    class={danger ? "danger" : "primary"}
    aria-disabled={busy ? "true" : undefined}
    onclick={() => {
      if (!busy) onconfirm();
    }}
    use:confirmFocus={{ restore, cancel: oncancel }}
  >{confirmLabel}</button>
  <button type="button" onclick={oncancel}>{cancelLabel}</button>
  {#if children}<span class="note">{@render children()}</span>{/if}
</span>

<style>
  .confirm-inline { display: contents; }
  .note { color: var(--muted); font-size: var(--type-secondary); }
</style>
