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
      <input bind:value={form.quote.businessName} />
    </label>
    <label class="field">
      <span>Logo URL</span>
      <input bind:value={form.quote.logoUrl} />
      {#if logoLoadFailed}
        <p class="advisory-text logo-hint">
          This logo URL couldn't be loaded, so it won't appear on printed quotes or invoices. Use a direct link to a
          PNG, JPG, or SVG image.
        </p>
      {/if}
    </label>
    <label class="field">
      <span>Email</span>
      <input type="email" bind:value={form.quote.email} />
    </label>
    <label class="field">
      <span>Phone</span>
      <input bind:value={form.quote.phone} />
    </label>
    <label class="field">
      <span>Tax ID</span>
      <input bind:value={form.quote.taxId} />
    </label>
    <label class="field">
      <span>Quote valid for (days)</span>
      <input type="number" min="0" step="1" bind:value={form.quote.validDays} />
    </label>
  </div>
  <label class="field field-textarea">
    <span>Address</span>
    <textarea rows="3" bind:value={form.quote.address}></textarea>
  </label>
  <label class="field field-textarea">
    <span>Payment instructions</span>
    <!-- Byte cap mirroring server/config.go's quotePaymentInstructionsMaxBytes (bytes, not characters;
         see PublicQuotesSettings.svelte). -->
    <textarea
      rows="5"
      bind:value={form.quote.paymentInstructions}
      class:warn={warnFor(warnings, "quote.paymentInstructions") !== ""}
      aria-invalid={warnFor(warnings, "quote.paymentInstructions") !== ""}
    ></textarea>
    <small class="field-hint">{paymentInstructionsBytes} / {QUOTE_PAYMENT_INSTRUCTIONS_MAX_BYTES} bytes</small>
    {#if warnFor(warnings, "quote.paymentInstructions") !== ""}
      <small class="warn-text">{warnFor(warnings, "quote.paymentInstructions")}</small>
    {/if}
  </label>
  <label class="field field-textarea">
    <span>Terms text</span>
    <!-- Byte cap mirroring server/config.go's quoteTermsTextMaxBytes. -->
    <textarea
      rows="5"
      bind:value={form.quote.termsText}
      class:warn={warnFor(warnings, "quote.termsText") !== ""}
      aria-invalid={warnFor(warnings, "quote.termsText") !== ""}
    ></textarea>
    <small class="field-hint">{termsTextBytes} / {QUOTE_TERMS_TEXT_MAX_BYTES} bytes</small>
    {#if warnFor(warnings, "quote.termsText") !== ""}
      <small class="warn-text">{warnFor(warnings, "quote.termsText")}</small>
    {/if}
  </label>
</div>

<style>
  /* Mirrors theme.css's .app-main .field-hint/.warn-text: lands in the control column under the Logo URL
   * input. .advisory-text supplies the --warn color and size; this adds only the grid placement. */
  .logo-hint { grid-column: 2; margin: 0; }
  @media (max-width: 600px) {
    .logo-hint { grid-column: auto; }
  }
</style>
