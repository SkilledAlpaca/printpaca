<script lang="ts">
  import type { ServerConfig } from "../../lib/settings";
  import { settingsWarnings } from "../../lib/settingsRules";
  import { warnFor } from "../../lib/format";
  import LabeledNumber from "../LabeledNumber.svelte";
  import LabeledText from "../LabeledText.svelte";
  import { focusAfterRowRemoval } from "../../lib/a11yFocus";
  import Card from "../ui/Card.svelte";
  import Field from "../ui/Field.svelte";
  import FormGrid from "../ui/FormGrid.svelte";
  import FieldRow from "./FieldRow.svelte";

  let { form, hasError }: { form: ServerConfig; hasError: (section: string) => boolean } = $props();

  const warnings = $derived(settingsWarnings(form));

  // Payment methods: a plain string array, one input per row.

  // Kept aligned with the index-keyed each below for edits through this card's add/remove helpers.
  // SettingsEditor's discard()/refresh() can replace `form` wholesale without unmounting, leaving `touched`
  // out of sync; the only effect is whether an already-valid row's warning shows.
  let touched = $state<boolean[]>([]);

  function addPaymentMethod() {
    form.invoicing.paymentMethods.push("");
    touched.push(false);
  }
  function removePaymentMethod(index: number) {
    form.invoicing.paymentMethods = form.invoicing.paymentMethods.filter((_, i) => i !== index);
    touched.splice(index, 1);
    void focusAfterRowRemoval("remove-payment-method", index, form.invoicing.paymentMethods.length, "add-payment-method");
  }
</script>

<Card id="section-invoicing" tone={hasError("invoicing") ? "danger" : "default"} heading="Invoicing">
  <p class="hint">Invoice numbering, default sales tax, and payment terms new invoices start with.</p>
  <FormGrid align="controls">
    <LabeledText
      label="Number prefix"
      size="xs"
      maxlength={10}
      bind:value={form.invoicing.numberPrefix}
      warn={warnFor(warnings, "invoicing.numberPrefix")}
    />
    <!-- min/max mirror server/config.go's validate(): salesTaxPct out of range [0,100]. -->
    <LabeledNumber
      label="Sales tax added to invoices (%)"
      min={0}
      max={100}
      step={0.01}
      bind:value={form.invoicing.salesTaxPct}
      warn={warnFor(warnings, "invoicing.salesTaxPct")}
      hint="Applied to the invoice subtotal. Ignored when a converted quote already included tax."
    />
    <LabeledText
      label="PayPal.me handle"
      size="s"
      maxlength={20}
      bind:value={form.invoicing.paypalMe}
      warn={warnFor(warnings, "invoicing.paypalMe")}
      hint="Enter only the handle, such as yourshop for paypal.me/yourshop. Invoices with a balance get a pay link and QR code. To prefill the amount, set the currency code under Currency."
    />
    <!-- min/max mirror server/config.go's validate(): dueDays out of range [0,365]. -->
    <LabeledNumber
      label="Payment due (days after issue)"
      min={0}
      max={10000}
      step={1}
      bind:value={form.invoicing.dueDays}
      warn={warnFor(warnings, "invoicing.dueDays")}
    />
  </FormGrid>

  <h3 class="section-label">Payment methods</h3>
  <p class="hint">Offered as a dropdown when recording a payment on an invoice.</p>
  {#each form.invoicing.paymentMethods as _method, i (i)}
    {@const methodWarn = warnFor(warnings, `invoicing.paymentMethods[${i}]`)}
    <FieldRow
      removeLabel={`Remove payment method ${i + 1}`}
      focusKey={`remove-payment-method-${i}`}
      disabled={form.invoicing.paymentMethods.length <= 1}
      onremove={() => removePaymentMethod(i)}
    >
      <FormGrid>
        <Field id={`payment-method-${i}`} label={`Payment method ${i + 1}`} size="m" error={methodWarn !== "" && (touched[i] ?? false) ? methodWarn : undefined}>
          {#snippet children(a)}
            <input
              id={a.id}
              bind:value={form.invoicing.paymentMethods[i]}
              class:warn={methodWarn !== "" && (touched[i] ?? false)}
              oninput={() => { touched[i] = true; }}
              onblur={() => { if (form.invoicing.paymentMethods[i] !== "") touched[i] = true; }}
              aria-invalid={a.invalid ?? "false"}
              aria-describedby={a.describedby}
            />
          {/snippet}
        </Field>
      </FormGrid>
    </FieldRow>
  {/each}
  {#if warnFor(warnings, "invoicing.paymentMethods") !== ""}
    <small class="warn-text">{warnFor(warnings, "invoicing.paymentMethods")}</small>
  {/if}
  <div class="row-actions">
    <button type="button" data-focus-key="add-payment-method" onclick={addPaymentMethod}>Add payment method</button>
  </div>
</Card>

<style>
  .section-label { margin: var(--space-4) 0 var(--space-1); }
  .row-actions { margin-block-start: var(--space-3); }
</style>
