<script lang="ts">
  import { characterCountText } from "../../lib/countText";
  import type { ServerConfig } from "../../lib/settings";
  import { settingsWarnings, QUOTE_PAYMENT_INSTRUCTIONS_MAX_BYTES, QUOTE_TERMS_TEXT_MAX_BYTES } from "../../lib/settingsRules";
  import { warnFor } from "../../lib/format";
  import { describeLogoDataUrl, logoToDataUrl, LOGO_ACCEPT } from "../../lib/logoUpload";
  import { announce } from "../../lib/a11y.svelte";
  import Card from "../ui/Card.svelte";
  import Field from "../ui/Field.svelte";
  import Fieldset from "../ui/Fieldset.svelte";
  import FormGrid from "../ui/FormGrid.svelte";

  let { form, hasError }: { form: ServerConfig; hasError: (section: string) => boolean } = $props();

  const warnings = $derived(settingsWarnings(form));

  // Live, non-blocking feedback (never gates saving): checks whether the entered logo URL loads as an image,
  // tracking the form's unsaved edits (App.svelte's preload tracks only the saved logoUrl). Debounced per
  // keystroke; a `token` stops a stale check's onload/onerror landing after a newer edit started.
  let logoUploadError = $state("");
  const uploadedLogo = $derived(describeLogoDataUrl(form.quote.logoUrl));
  async function onLogoFile(e: Event) {
    const input = e.currentTarget as HTMLInputElement;
    const file = input.files?.[0];
    input.value = "";
    if (!file) return;
    const result = await logoToDataUrl(file);
    if ("error" in result) {
      logoUploadError = result.error;
      return;
    }
    logoUploadError = "";
    form.quote.logoUrl = result.url;
    announce("Logo uploaded. Save changes to keep it.");
  }
  function removeLogo() {
    form.quote.logoUrl = "";
    logoUploadError = "";
    announce("Logo removed. Save changes to keep that.");
  }

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

<Card id="section-quote" tone={hasError("quote") ? "danger" : "default"} heading="Business identity">
  <p class="hint">Shown on printed quotes and invoices, in the admin header, and on the customer shop.</p>
  <div class="sections">
    <FormGrid align="controls">
      <Field id="quote-business-name" label="Business name" size="l">
        {#snippet children(a)}
          <input id={a.id} aria-describedby={a.describedby} bind:value={form.quote.businessName} autocomplete="organization" />
        {/snippet}
      </Field>
      <Field id="quote-email" label="Email" size="l">
        {#snippet children(a)}
          <input id={a.id} aria-describedby={a.describedby} type="email" bind:value={form.quote.email} autocomplete="email" />
        {/snippet}
      </Field>
      <Field id="quote-phone" label="Phone" size="m">
        {#snippet children(a)}
          <input id={a.id} aria-describedby={a.describedby} type="tel" bind:value={form.quote.phone} autocomplete="tel" />
        {/snippet}
      </Field>
      <Field id="quote-tax-id" label="Tax ID" size="m">
        {#snippet children(a)}
          <input id={a.id} aria-describedby={a.describedby} bind:value={form.quote.taxId} />
        {/snippet}
      </Field>
      <Field id="quote-valid-days" label="Quote valid for (days)" size="xs">
        {#snippet children(a)}
          <input id={a.id} aria-describedby={a.describedby} type="number" min="0" step="1" bind:value={form.quote.validDays} />
        {/snippet}
      </Field>
      <Field
        id="quote-reference-prefix"
        label="Order reference prefix"
        size="xs"
        hint="Starts the short reference customers see on their order, such as HL-1001. Leave blank to use your business initials."
      >
        {#snippet children(a)}
          <input id={a.id} aria-describedby={a.describedby} bind:value={form.quote.referencePrefix} maxlength="10" autocomplete="off" />
        {/snippet}
      </Field>
    </FormGrid>

    <Fieldset legend="Logo">
      {#if uploadedLogo}
        <div class="logo-row">
          <span>{uploadedLogo}</span>
          <button type="button" onclick={removeLogo}>Remove logo</button>
        </div>
      {:else}
        <FormGrid>
          <Field id="quote-logo-url" label="Logo URL" size="l" describedby={logoLoadFailed ? "quote-logo-hint" : undefined}>
            {#snippet children(a)}
              <input id={a.id} bind:value={form.quote.logoUrl} aria-describedby={a.describedby} />
            {/snippet}
          </Field>
        </FormGrid>
      {/if}
      {#if logoLoadFailed}
        <p id="quote-logo-hint" class="advisory-text">
          This logo URL couldn't be loaded, so it won't appear on printed quotes or invoices. Use a direct link to a
          PNG, JPG, or SVG image.
        </p>
      {/if}
      <FormGrid>
        <Field
          id="quote-logo-file"
          label="Upload logo"
          size="l"
          hint="PNG, JPG, WebP, or SVG, up to 64 KB. Larger pictures are shrunk to fit. An uploaded logo shows in the app and on printouts; emails show your business name instead. Use a public https:// URL to show a logo in emails."
          error={logoUploadError || undefined}
        >
          {#snippet children(a)}
            <input id={a.id} type="file" accept={LOGO_ACCEPT} aria-describedby={a.describedby} aria-invalid={a.invalid ?? "false"} onchange={onLogoFile} />
          {/snippet}
        </Field>
      </FormGrid>
      {#if form.quote.logoUrl.trim() !== "" && !logoLoadFailed}
        <img class="logo-preview" src={form.quote.logoUrl} alt={uploadedLogo ? "Your logo" : "Logo preview"} />
      {/if}
    </Fieldset>

    <Field id="quote-address" label="Address">
      {#snippet children(a)}
        <textarea id={a.id} aria-describedby={a.describedby} rows="3" bind:value={form.quote.address} autocomplete="street-address"></textarea>
      {/snippet}
    </Field>
    <Field
      id="quote-payment-instructions"
      label="Payment instructions"
      error={warnFor(warnings, "quote.paymentInstructions") || undefined}
      describedby="quote-payment-count"
    >
      {#snippet children(a)}
        <!-- Byte cap mirroring server/config.go's quotePaymentInstructionsMaxBytes (bytes, not characters;
             see StorefrontSettings.svelte). -->
        <textarea
          id={a.id}
          rows="5"
          bind:value={form.quote.paymentInstructions}
          class:warn={warnFor(warnings, "quote.paymentInstructions") !== ""}
          aria-invalid={a.invalid ?? "false"}
          aria-describedby={a.describedby}
        ></textarea>
        <small id="quote-payment-count" class="count">{characterCountText(form.quote.paymentInstructions, QUOTE_PAYMENT_INSTRUCTIONS_MAX_BYTES)}</small>
      {/snippet}
    </Field>
    <Field id="quote-terms-text" label="Terms text" error={warnFor(warnings, "quote.termsText") || undefined} describedby="quote-terms-count">
      {#snippet children(a)}
        <!-- Byte cap mirroring server/config.go's quoteTermsTextMaxBytes. -->
        <textarea
          id={a.id}
          rows="5"
          bind:value={form.quote.termsText}
          class:warn={warnFor(warnings, "quote.termsText") !== ""}
          aria-invalid={a.invalid ?? "false"}
          aria-describedby={a.describedby}
        ></textarea>
        <small id="quote-terms-count" class="count">{characterCountText(form.quote.termsText, QUOTE_TERMS_TEXT_MAX_BYTES)}</small>
      {/snippet}
    </Field>
  </div>
</Card>

<style>
  .sections { display: flex; flex-direction: column; gap: var(--gap-section); }
  .count { color: var(--muted); font-size: var(--type-secondary); }
  .logo-preview { max-height: 3rem; max-width: var(--field-m); width: auto; object-fit: contain; background: var(--surface-2); border-radius: var(--radius); }
  .logo-row { display: flex; align-items: center; gap: var(--space-3); flex-wrap: wrap; }
</style>
