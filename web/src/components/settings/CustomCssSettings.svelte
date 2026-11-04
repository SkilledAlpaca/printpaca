<script lang="ts">
  import { characterCountText } from "../../lib/countText";
  import type { ServerConfig } from "../../lib/settings";
  import { settingsWarnings, CUSTOM_CSS_MAX_BYTES } from "../../lib/settingsRules";
  import { warnFor } from "../../lib/format";
  import { cardClass } from "../../lib/settings";
  import Card from "../ui/Card.svelte";
  import Field from "../ui/Field.svelte";

  let { form, hasError }: { form: ServerConfig; hasError: (section: string) => boolean } = $props();

  const warnings = $derived(settingsWarnings(form));
</script>

<Card id="section-custom-css" class={cardClass(hasError("customCss"))} heading="Custom CSS">
  <p class="hint">
    Loads after the app's own styles on your public estimate page (on screen) and on printed quotes and invoices
    (print only, never the on-screen admin UI). Target <code>#print-view.invoice</code>,
    <code>#print-view.quote</code>, or theme tokens such as <code>--accent</code> to restyle one surface.
  </p>
  <Field id="custom-css" label="CSS" error={warnFor(warnings, "customCss") || undefined} describedby="custom-css-count">
    {#snippet children(a)}
      <!-- No maxlength attribute: the limit is in BYTES (server/config.go's customCssMaxBytes) and HTML
           maxlength counts UTF-16 units, undercounting multi-byte scripts. The byte counter and warning
           measure as the server does (same as StorefrontSettings' intro text). -->
      <textarea
        id={a.id}
        rows="10"
        class="mono"
        bind:value={form.customCss}
        class:warn={warnFor(warnings, "customCss") !== ""}
        aria-invalid={a.invalid ?? "false"}
        aria-describedby={a.describedby}
      ></textarea>
      <small id="custom-css-count" class="count">{characterCountText(form.customCss, CUSTOM_CSS_MAX_BYTES)}</small>
    {/snippet}
  </Field>
</Card>

<style>
  /* Custom CSS is source code: monospace keeps braces, selectors and indentation legible. */
  .mono {
    font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace;
  }
  .count { color: var(--muted); font-size: var(--type-secondary); }
</style>
