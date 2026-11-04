<script lang="ts">
  import type { Snippet } from "svelte";
  import { describedBy, fieldIds, type FieldSize } from "./field";

  // A label above one control (D9), at every width. The label is a native <label for>, the hint and the error
  // are separate elements whose ids combine in aria-describedby with any extra ids the caller supplies (a live
  // notice, say), and the control itself stays the caller's: it receives { id, describedby, invalid } to spread
  // onto an input, select or textarea. `size` is a preferred width (XS, S, M, L) capped by the space available,
  // so currency and quantities don't stretch across a wide card; "full" fills its column.
  let {
    id,
    label,
    hint,
    error,
    size = "full",
    describedby,
    children,
  }: {
    id: string;
    label: string;
    hint?: string;
    error?: string;
    size?: FieldSize;
    /** Extra ids appended after the hint and before the error. */
    describedby?: string;
    children: Snippet<[{ id: string; describedby: string | undefined; invalid: "true" | undefined }]>;
  } = $props();

  const ids = $derived(fieldIds(id));
  const described = $derived(describedBy(hint && ids.hint, describedby, error && ids.error));
</script>

<div class="ui-field" data-size={size}>
  <label for={id}>{label}</label>
  {#if hint}<small class="hint-text" id={ids.hint}>{hint}</small>{/if}
  <!-- display: contents, so the control and anything the caller adds beside it lay out as direct children of the
       field; FormGrid's align="controls" turns it into the field's one third row. -->
  <div class="ui-field-control">{@render children({ id, describedby: described, invalid: error ? "true" : undefined })}</div>
  {#if error}<small class="error-text" id={ids.error}>{error}</small>{/if}
</div>

<style>
  .ui-field { display: flex; flex-direction: column; gap: var(--space-1); min-width: 0; }
  label { font-size: var(--type-secondary); font-weight: 600; color: var(--text); }
  .ui-field-control { display: contents; }
  .hint-text { color: var(--muted); font-size: var(--type-secondary); }
  .error-text { color: var(--danger); font-size: var(--type-secondary); }
  .ui-field :global(:is(input, select, textarea)) { width: 100%; min-width: 0; }
  .ui-field[data-size="xs"] :global(:is(input, select, textarea)) { max-width: min(100%, var(--field-xs)); }
  .ui-field[data-size="s"] :global(:is(input, select, textarea)) { max-width: min(100%, var(--field-s)); }
  .ui-field[data-size="m"] :global(:is(input, select, textarea)) { max-width: min(100%, var(--field-m)); }
  .ui-field[data-size="l"] :global(:is(input, select, textarea)) { max-width: min(100%, var(--field-l)); }
</style>
