<script lang="ts">
  import type { ServerConfig } from "../../lib/settings";
  import { presetDisplayName } from "../../lib/format";
  import Card from "../ui/Card.svelte";
  import Field from "../ui/Field.svelte";
  import FormGrid from "../ui/FormGrid.svelte";

  // The Requests-specific half of the old "Public estimates" card: which printer and preset price every visitor
  // estimate. Everything the customer sees (the intro text, the shop switches, the wording, the hero image, the
  // accent color) moved to Settings > Storefront in v1.2 (StorefrontSettings.svelte).
  let { form, hasError }: { form: ServerConfig; hasError: (section: string) => boolean } = $props();
</script>

<Card id="section-public-quotes" tone={hasError("publicQuotes") ? "danger" : "default"} heading="Public estimates">
  <p class="hint">The printer and preset that price every visitor estimate. The shop's wording, switches, and look are in <a href="#/settings">Settings</a> under Storefront.</p>
  <FormGrid align="controls">
    <Field id="public-estimate-printer" label="Printer" size="m">
      {#snippet children(a)}
        <select id={a.id} aria-describedby={a.describedby} bind:value={form.publicQuotes.printer}>
          {#each form.printers as p (p.id)}<option value={p.id}>{p.name}</option>{/each}
        </select>
      {/snippet}
    </Field>
    <Field id="public-estimate-preset" label="Preset" size="m">
      {#snippet children(a)}
        <select id={a.id} aria-describedby={a.describedby} bind:value={form.publicQuotes.preset}>
          {#each Object.keys(form.presets) as key (key)}<option value={key}>{presetDisplayName(key)}</option>{/each}
        </select>
      {/snippet}
    </Field>
  </FormGrid>
</Card>
