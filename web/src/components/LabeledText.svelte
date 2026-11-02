<script lang="ts">
  let {
    label,
    value = $bindable(),
    type = "text",
    placeholder = "",
    maxlength,
    warn = "",
    hint = "",
    help = "",
    onchanged = () => {},
  }: {
    label: string;
    value: string;
    type?: "text" | "email";
    placeholder?: string;
    maxlength?: number;
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
  <span>{label}</span>
  <input
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
    aria-invalid={warn !== "" && touched}
    aria-describedby={describedBy}
  />
  {#if help}<small id={helpId} class="field-hint">{help}</small>{/if}
  {#if hint}<small class="field-hint">{hint}</small>{/if}
  {#if warn !== "" && touched}<small id={warnId} class="warn-text">{warn}</small>{/if}
</label>
