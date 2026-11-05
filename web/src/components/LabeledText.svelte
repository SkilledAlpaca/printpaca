<script lang="ts">
  import Field from "./ui/Field.svelte";
  import type { FieldSize } from "./ui/field";

  let {
    label,
    value = $bindable(),
    type = "text",
    placeholder = "",
    maxlength,
    warn = "",
    hint = "",
    note = "",
    help = "",
    helpVisible = false,
    size = "m",
    onchanged = () => {},
  }: {
    label: string;
    value: string;
    type?: "text" | "email";
    placeholder?: string;
    maxlength?: number;
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
    /** Preferred control width (ui/Field); names and emails are M by default. */
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

<!-- Only the label text is the field name; help, hint, and warning are described, not named (WCAG 1.3.1, 4.1.2). -->
<Field id={inputId} {label} hint={tip || undefined} hintVisible={helpVisible} error={warn !== "" && touched ? warn : undefined} {size} describedby={note ? noteId : undefined}>
  {#snippet children(a)}
    <input
      id={a.id}
      {type}
      bind:value
      {placeholder}
      {maxlength}
      class:warn={warn !== "" && touched}
      oninput={() => {
        markTouched();
        onchanged();
      }}
      onblur={() => {
        if (value !== "") markTouched();
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
