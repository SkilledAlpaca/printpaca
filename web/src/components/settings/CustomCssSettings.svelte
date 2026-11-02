<script lang="ts">
  import type { ServerConfig } from "../../lib/settings";
  import { settingsWarnings, CUSTOM_CSS_MAX_BYTES } from "../../lib/settingsRules";
  import { warnFor } from "../../lib/format";
  import { utf8ByteLength } from "../../lib/publicFormLimits";

  let { form, hasError }: { form: ServerConfig; hasError: (section: string) => boolean } = $props();

  const warnings = $derived(settingsWarnings(form));
  const customCssBytes = $derived(utf8ByteLength(form.customCss));
</script>

<div id="section-custom-css" class="card roomy" class:danger-border={hasError("customCss")}>
  <h2>Custom CSS</h2>
  <p class="hint">
    Loads after the app's own styles on your public estimate page (on screen) and on printed quotes and invoices
    (print only, never the on-screen admin UI). Target <code>#print-view.invoice</code>,
    <code>#print-view.quote</code>, or theme tokens such as <code>--accent</code> to restyle one surface.
  </p>
  <div class="field field-textarea">
    <label for="custom-css">CSS</label>
    <!-- No maxlength attribute: the limit is in BYTES (server/config.go's customCssMaxBytes) and HTML
         maxlength counts UTF-16 units, undercounting multi-byte scripts. The byte counter and warning
         below measure as the server does (same as PublicQuotesSettings' intro text). -->
    <textarea
      id="custom-css"
      rows="10"
      class="mono"
      bind:value={form.customCss}
      class:warn={warnFor(warnings, "customCss") !== ""}
      aria-invalid={warnFor(warnings, "customCss") !== ""}
      aria-describedby={warnFor(warnings, "customCss") !== "" ? "custom-css-count custom-css-warn" : "custom-css-count"}
    ></textarea>
    <small id="custom-css-count" class="field-hint">{customCssBytes} / {CUSTOM_CSS_MAX_BYTES} bytes</small>
    {#if warnFor(warnings, "customCss") !== ""}
      <small id="custom-css-warn" class="warn-text">{warnFor(warnings, "customCss")}</small>
    {/if}
  </div>
</div>

<style>
  /* Custom CSS is source code: monospace keeps braces, selectors and indentation legible. */
  .mono {
    font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace;
  }
</style>
