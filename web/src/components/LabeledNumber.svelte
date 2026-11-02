<script lang="ts">
  let {
    label,
    value = $bindable(),
    min = 0,
    max,
    step = "any",
    suffix = "",
    warn = "",
    hint = "",
    help = "",
    onchanged = () => {},
  }: {
    label: string;
    value: number | null;
    min?: number;
    max?: number;
    step?: string | number;
    suffix?: string;
    warn?: string;
    hint?: string;
    /** Always-visible explanation under the field, linked with aria-describedby so keyboard, touch, and
     * screen reader users get it too (unlike a title tooltip). */
    help?: string;
    onchanged?: () => void;
  } = $props();

  const uid = $props.id();
  const warnId = `${uid}-warn`;
  const helpId = `${uid}-help`;
  const hintId = `${uid}-hint`;
  const inputId = `${uid}-input`;

  let touched = $state(false);
  const describedBy = $derived(
    [help ? helpId : "", hint ? hintId : "", warn !== "" && touched ? warnId : ""].filter(Boolean).join(" ") || undefined,
  );
  function markTouched() {
    touched = true;
  }
</script>

<!-- Only the label text is the field name; help, hint, and warning are described, not named (WCAG 1.3.1, 4.1.2). -->
<div class="field">
  <label for={inputId}>{label}{suffix ? ` (${suffix})` : ""}</label>
  <input
    id={inputId}
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
    aria-invalid={warn !== "" && touched}
    aria-describedby={describedBy}
  />
  {#if help}<small id={helpId} class="field-hint">{help}</small>{/if}
  {#if hint}<small id={hintId} class="field-hint">{hint}</small>{/if}
  {#if warn !== "" && touched}<small id={warnId} class="warn-text">{warn}</small>{/if}
</div>
