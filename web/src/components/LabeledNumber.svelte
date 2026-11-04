<script lang="ts">
  import Field from "./ui/Field.svelte";
  import type { FieldSize } from "./ui/field";

  let {
    label,
    value = $bindable(),
    min = 0,
    max,
    step = "any",
    suffix = "",
    warn = "",
    hint = "",
    note = "",
    help = "",
    helpVisible = false,
    size = "s",
    onchanged = () => {},
  }: {
    label: string;
    value: number | null;
    min?: number;
    max?: number;
    step?: string | number;
    suffix?: string;
    warn?: string;
    /** More description, joined to `help` in the same tooltip. */
    hint?: string;
    /** A status or advisory that must always be seen: a visible line under the control, never in a tooltip. */
    note?: string;
    /** Explanation of the field, in the "?" tooltip beside the label (ui/Field); aria-describedby links it to the
     * control either way. */
    help?: string;
    /** Keep `help` and `hint` visible under the label instead of in a tooltip, for a hint that is a status or a warning. */
    helpVisible?: boolean;
    /** Preferred control width (ui/Field); numbers are short, so S by default. */
    size?: FieldSize;
    onchanged?: () => void;
  } = $props();

  const uid = $props.id();
  const inputId = `${uid}-input`;
  const noteId = `${uid}-note`;

  // `help` and `hint` both describe the field, so they share one tooltip (or one visible line).
  const tip = $derived([help, hint].filter(Boolean).join(" "));

  let touched = $state(false);
  function markTouched() {
    touched = true;
  }
</script>

<!-- Only the label text is the field name; help, hint, and warning are described, not named (WCAG 1.3.1, 4.1.2).
     The warning stays quiet until the field is touched; the error line is the field's own (ui/Field). -->
<Field id={inputId} label={`${label}${suffix ? ` (${suffix})` : ""}`} hint={tip || undefined} hintVisible={helpVisible} error={warn !== "" && touched ? warn : undefined} {size} describedby={note ? noteId : undefined}>
  {#snippet children(a)}
    <input
      id={a.id}
      type="number"
      bind:value
      {min}
      {max}
      {step}
      class:warn={warn !== "" && touched}
      oninput={() => {
        markTouched();
        onchanged();
      }}
      onblur={() => {
        if (value !== null) markTouched();
      }}
      aria-invalid={a.invalid ?? "false"}
      aria-describedby={a.describedby}
    />
    {#if note}<small id={noteId} class="note-text">{note}</small>{/if}
  {/snippet}
</Field>


<style>
  .note-text { color: var(--muted); font-size: var(--type-secondary); }
</style>
