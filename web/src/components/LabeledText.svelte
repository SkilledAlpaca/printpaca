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
    help = "",
    size = "m",
    onchanged = () => {},
  }: {
    label: string;
    value: string;
    type?: "text" | "email";
    placeholder?: string;
    maxlength?: number;
    warn?: string;
    hint?: string;
    /** Always-visible explanation next to the label, linked with aria-describedby so keyboard, touch, and
     * screen reader users get it too (unlike a title tooltip). */
    help?: string;
    /** Preferred control width (ui/Field); names and emails are M by default. */
    size?: FieldSize;
    onchanged?: () => void;
  } = $props();

  const uid = $props.id();
  const inputId = `${uid}-input`;
  const hintId = `${uid}-hint2`;

  let touched = $state(false);
  function markTouched() {
    touched = true;
  }
</script>

<!-- Only the label text is the field name; help, hint, and warning are described, not named (WCAG 1.3.1, 4.1.2). -->
<Field id={inputId} {label} hint={help || undefined} error={warn !== "" && touched ? warn : undefined} {size} describedby={hint ? hintId : undefined}>
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
    {#if hint}<small id={hintId} class="hint-text">{hint}</small>{/if}
  {/snippet}
</Field>

<style>
  .hint-text { color: var(--muted); font-size: var(--type-secondary); }
</style>
