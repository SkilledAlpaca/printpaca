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

  let touched = $state(false);
  const describedBy = $derived(
    [help ? helpId : "", warn !== "" && touched ? warnId : ""].filter(Boolean).join(" ") || undefined,
  );
  function markTouched() {
    touched = true;
  }
</script>

<label class="field">
  <span>{label}{suffix ? ` (${suffix})` : ""}</span>
  <input
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
  {#if hint}<small class="field-hint">{hint}</small>{/if}
  {#if warn !== "" && touched}<small id={warnId} class="warn-text">{warn}</small>{/if}
</label>
