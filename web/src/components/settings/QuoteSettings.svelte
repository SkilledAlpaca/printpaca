<script lang="ts">
  import type { ServerConfig } from "../../lib/settings";
  import { settingsWarnings, QUOTE_PAYMENT_INSTRUCTIONS_MAX_BYTES, QUOTE_TERMS_TEXT_MAX_BYTES } from "../../lib/settingsRules";
  import { warnFor } from "../../lib/format";
  import { utf8ByteLength } from "../../lib/publicFormLimits";

  let { form, hasError }: { form: ServerConfig; hasError: (section: string) => boolean } = $props();

  const warnings = $derived(settingsWarnings(form));
  const paymentInstructionsBytes = $derived(utf8ByteLength(form.quote.paymentInstructions));
  const termsTextBytes = $derived(utf8ByteLength(form.quote.termsText));

  // Live, non-blocking feedback (never gates saving): checks whether the entered logo URL loads as an image,
  // tracking the form's unsaved edits (App.svelte's preload tracks only the saved logoUrl). Debounced per
  // keystroke; a `token` stops a stale check's onload/onerror landing after a newer edit started.
  const LOGO_CHECK_DEBOUNCE_MS = 400;
  let logoLoadFailed = $state(false);
  let checkToken = 0;
  $effect(() => {
    const url = form.quote.logoUrl.trim();
    const token = ++checkToken;
    if (url === "") {
      logoLoadFailed = false;
      return;
    }
    const timer = setTimeout(() => {
      const img = new Image();
      img.onload = () => {
        if (token !== checkToken) return;
        logoLoadFailed = false;
      };
      img.onerror = () => {
        if (token !== checkToken) return;
        logoLoadFailed = true;
      };
      img.src = url;
    }, LOGO_CHECK_DEBOUNCE_MS);
    return () => clearTimeout(timer);
  });
</script>

<div id="section-quote" class="card roomy" class:danger-border={hasError("quote")}>
  <h2>Business identity</h2>
  <p class="hint">Shown on printed quotes and invoices.</p>
  <div class="field-rows">
    <label class="field">
      <span>Business name</span>
      <input bind:value={form.quote.businessName} autocomplete="organization" />
    </label>
    <div class="field">
      <label for="quote-logo-url">Logo URL</label>
      <input id="quote-logo-url" bind:value={form.quote.logoUrl} aria-describedby={logoLoadFailed ? "quote-logo-hint" : undefined} />
      {#if logoLoadFailed}
        <p id="quote-logo-hint" class="advisory-text logo-hint">
          This logo URL couldn't be loaded, so it won't appear on printed quotes or invoices. Use a direct link to a
          PNG, JPG, or SVG image.
        </p>
      {/if}
    </div>
    <label class="field">
      <span>Email</span>
      <input type="email" bind:value={form.quote.email} autocomplete="email" />
    </label>
    <label class="field">
      <span>Phone</span>
      <input type="tel" bind:value={form.quote.phone} autocomplete="tel" />
    </label>
    <label class="field">
      <span>Tax ID</span>
      <input bind:value={form.quote.taxId} />
    </label>
    <label class="field">
      <span>Quote valid for (days)</span>
      <input type="number" min="0" step="1" bind:value={form.quote.validDays} />
    </label>
    <div class="field">
      <label for="quote-reference-prefix">Order reference prefix</label>
      <input id="quote-reference-prefix" bind:value={form.quote.referencePrefix} maxlength="10" autocomplete="off" aria-describedby="quote-reference-prefix-hint" />
      <small class="field-hint" id="quote-reference-prefix-hint">Starts the short reference customers see on their order, such as HL-1001. Leave blank to use your business initials.</small>
    </div>
  </div>
  <label class="field field-textarea">
    <span>Address</span>
    <textarea rows="3" bind:value={form.quote.address} autocomplete="street-address"></textarea>
  </label>
  <div class="field field-textarea">
    <label for="quote-payment-instructions">Payment instructions</label>
    <!-- Byte cap mirroring server/config.go's quotePaymentInstructionsMaxBytes (bytes, not characters;
         see PublicQuotesSettings.svelte). -->
    <textarea
      id="quote-payment-instructions"
      rows="5"
      bind:value={form.quote.paymentInstructions}
      class:warn={warnFor(warnings, "quote.paymentInstructions") !== ""}
      aria-invalid={warnFor(warnings, "quote.paymentInstructions") !== ""}
      aria-describedby={warnFor(warnings, "quote.paymentInstructions") !== "" ? "quote-payment-count quote-payment-warn" : "quote-payment-count"}
    ></textarea>
    <small id="quote-payment-count" class="field-hint">{paymentInstructionsBytes} / {QUOTE_PAYMENT_INSTRUCTIONS_MAX_BYTES} bytes</small>
    {#if warnFor(warnings, "quote.paymentInstructions") !== ""}
      <small id="quote-payment-warn" class="warn-text">{warnFor(warnings, "quote.paymentInstructions")}</small>
    {/if}
  </div>
  <div class="field field-textarea">
    <label for="quote-terms-text">Terms text</label>
    <!-- Byte cap mirroring server/config.go's quoteTermsTextMaxBytes. -->
    <textarea
      id="quote-terms-text"
      rows="5"
      bind:value={form.quote.termsText}
      class:warn={warnFor(warnings, "quote.termsText") !== ""}
      aria-invalid={warnFor(warnings, "quote.termsText") !== ""}
      aria-describedby={warnFor(warnings, "quote.termsText") !== "" ? "quote-terms-count quote-terms-warn" : "quote-terms-count"}
    ></textarea>
    <small id="quote-terms-count" class="field-hint">{termsTextBytes} / {QUOTE_TERMS_TEXT_MAX_BYTES} bytes</small>
    {#if warnFor(warnings, "quote.termsText") !== ""}
      <small id="quote-terms-warn" class="warn-text">{warnFor(warnings, "quote.termsText")}</small>
    {/if}
  </div>
</div>

<style>
  /* Mirrors theme.css's .app-main .field-hint/.warn-text: lands in the control column under the Logo URL
   * input. .advisory-text supplies the --warn color and size; this adds only the grid placement. */
  .logo-hint { grid-column: 2; margin: 0; }
  @media (max-width: 600px) {
    .logo-hint { grid-column: auto; }
  }
</style>
