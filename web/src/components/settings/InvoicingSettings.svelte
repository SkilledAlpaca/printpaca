<script lang="ts">
  import type { ServerConfig } from "../../lib/settings";
  import { settingsWarnings } from "../../lib/settingsRules";
  import { warnFor } from "../../lib/format";
  import LabeledNumber from "../LabeledNumber.svelte";
  import LabeledText from "../LabeledText.svelte";
  import { focusAfterRowRemoval } from "../a11yFocus";

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

<div id="section-invoicing" class="card roomy" class:danger-border={hasError("invoicing")}>
  <p class="section-label">Settings</p>
  <h2>Invoicing</h2>
  <p class="hint">Invoice numbering, default sales tax, and payment terms new invoices start with.</p>
  <div class="field-rows">
    <LabeledText
      label="Number prefix"
      maxlength={10}
      bind:value={form.invoicing.numberPrefix}
      warn={warnFor(warnings, "invoicing.numberPrefix")}
    />
    <!-- min/max mirror server/config.go's validate(): salesTaxPct out of range [0,30]. -->
    <LabeledNumber
      label="Sales tax added to invoices (%)"
      min={0}
      max={30}
      step={0.01}
      bind:value={form.invoicing.salesTaxPct}
      warn={warnFor(warnings, "invoicing.salesTaxPct")}
      hint="Applied to the invoice subtotal. Ignored when a converted quote already included tax."
    />
    <LabeledText
      label="PayPal.me handle"
      maxlength={20}
      bind:value={form.invoicing.paypalMe}
      warn={warnFor(warnings, "invoicing.paypalMe")}
      hint="Enter only the handle, such as yourshop for paypal.me/yourshop. Invoices with a balance get a pay link and QR code. To prefill the amount, set the currency code under Currency."
    />
    <!-- min/max mirror server/config.go's validate(): dueDays out of range [0,365]. -->
    <LabeledNumber
      label="Payment due (days after issue)"
      min={0}
      max={365}
      step={1}
      bind:value={form.invoicing.dueDays}
      warn={warnFor(warnings, "invoicing.dueDays")}
    />
  </div>

  <h3 class="section-label">Payment methods</h3>
  <p class="hint">Offered as a dropdown when recording a payment on an invoice.</p>
  {#each form.invoicing.paymentMethods as _method, i (i)}
    {@const methodWarn = warnFor(warnings, `invoicing.paymentMethods[${i}]`)}
    {@const rowWarn = methodWarn !== "" && (touched[i] ?? false)}
    <div class="charge-row">
      <label class="field field-inline">
        <span>Method {i + 1}</span>
        <input
          aria-label={`Payment method ${i + 1}`}
          bind:value={form.invoicing.paymentMethods[i]}
          class:warn={rowWarn}
          oninput={() => { touched[i] = true; }}
          onblur={() => { if (form.invoicing.paymentMethods[i] !== "") touched[i] = true; }}
          aria-invalid={rowWarn}
          aria-describedby={rowWarn ? `payment-method-${i}-warn` : undefined}
        />
        {#if rowWarn}<small id={`payment-method-${i}-warn`} class="warn-text">{methodWarn}</small>{/if}
      </label>
      <button
        type="button"
        aria-label={`Remove payment method ${i + 1}`}
        data-focus-key={`remove-payment-method-${i}`}
        disabled={form.invoicing.paymentMethods.length <= 1}
        onclick={() => removePaymentMethod(i)}
      >Remove</button>
    </div>
  {/each}
  {#if warnFor(warnings, "invoicing.paymentMethods") !== ""}
    <small class="warn-text">{warnFor(warnings, "invoicing.paymentMethods")}</small>
  {/if}
  <button type="button" data-focus-key="add-payment-method" onclick={addPaymentMethod}>Add payment method</button>
</div>

<style>
  .section-label { margin: 0.6rem 0 0.3rem; }

  /* .charge-row is also rendered by SettingsTab.svelte's fee-schedule rows. Svelte scopes <style> per
   * component, so each copy stays local; it was deliberately not promoted to theme.css. */
  .charge-row { display: flex; align-items: end; gap: 0.5rem; margin-bottom: 0.4rem; flex-wrap: wrap; }
  .charge-row .field { flex: 1 1 100px; min-width: 90px; }
</style>
