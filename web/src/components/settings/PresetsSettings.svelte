<script lang="ts">
  import { presetDisplayName } from "../../lib/format";
  import { nextKey, type ServerConfig } from "../../lib/settings";
  import { focusAfterUpdate } from "../../lib/a11y.svelte";
  import { keySelector } from "../../lib/a11yFocus";
  import Card from "../ui/Card.svelte";
  import Field from "../ui/Field.svelte";
  import Fieldset from "../ui/Fieldset.svelte";
  import FormGrid from "../ui/FormGrid.svelte";

  let {
    form,
    builtin,
    hasError,
  }: { form: ServerConfig; builtin: ServerConfig; hasError: (section: string) => boolean } = $props();

  const builtinPresetKeys = $derived(new Set(Object.keys(builtin.presets)));

  // Rejected-rename warning, keyed by the preset's current key. The input is one-way (value={key}), so on a
  // rejected rename the DOM value is reset explicitly; `key` itself never changes on that path.
  let renameWarnings = $state<Record<string, string>>({});

  function clearRenameWarning(key: string) {
    if (!(key in renameWarnings)) return;
    const next = { ...renameWarnings };
    delete next[key];
    renameWarnings = next;
  }

  // Presets: a deep-merge map on the server; built-in keys can't be removed.

  function addPreset() {
    const key = nextKey(Object.keys(form.presets), "custom");
    const seed = builtin.presets["side-hustle"] ?? { failureRatePct: 5, prepMinutes: 5, laborRate: 12, monthlyFixed: 0, monthlyHours: 100 };
    form.presets = { ...form.presets, [key]: { ...seed } };
  }
  function removePreset(key: string) {
    const next = { ...form.presets };
    delete next[key];
    form.presets = next;
    clearRenameWarning(key);
    void focusAfterUpdate(keySelector("add-preset"));
  }
  function renamePreset(oldKey: string, e: Event & { currentTarget: HTMLInputElement }) {
    const newKey = e.currentTarget.value.trim();
    const value = form.presets[oldKey];
    if (newKey === oldKey) return; // unchanged: silent no-op, no warning
    if (!newKey || newKey in form.presets || value === undefined) {
      e.currentTarget.value = oldKey; // reject: the field must not keep showing a key that was never saved
      renameWarnings = { ...renameWarnings, [oldKey]: !newKey ? "Name can't be empty." : "That name is already in use." };
      return;
    }
    const next = { ...form.presets };
    delete next[oldKey];
    next[newKey] = value;
    form.presets = next;
    clearRenameWarning(oldKey);
  }
</script>

<Card id="section-presets" tone={hasError("presets") ? "danger" : "default"} heading="Presets">
  <p class="hint">Named bundles of failure rate, prep time, labor rate, and overhead that new quotes start from. Editing a rate on the New quote page changes that quote only. Built-in presets can't be removed.</p>
  <div class="stack">
    {#each Object.entries(form.presets) as [key, preset] (key)}
      <Card tone="inset">
        <Fieldset legend={presetDisplayName(key)}>
          {#if builtinPresetKeys.has(key)}
            <div class="preset-actions"><span class="pill">built-in</span></div>
          {:else}
            <FormGrid>
              <Field id={`preset-${key}-name`} label="Name" size="m" error={renameWarnings[key] || undefined}>
                {#snippet children(a)}
                  <input id={a.id} aria-describedby={a.describedby} aria-invalid={a.invalid ?? "false"} value={key} onchange={(e) => renamePreset(key, e)} />
                {/snippet}
              </Field>
            </FormGrid>
            <div class="preset-actions">
              <button type="button" aria-label={`Remove preset ${key}`} onclick={() => removePreset(key)}>Remove preset</button>
            </div>
          {/if}
          <FormGrid align="controls">
            <Field id={`preset-${key}-failure`} label="Failure rate (%)" size="s">
              {#snippet children(a)}
                <input id={a.id} aria-describedby={a.describedby} type="number" step="any" min="0" max="90" bind:value={preset.failureRatePct} />
              {/snippet}
            </Field>
            <Field id={`preset-${key}-prep`} label="Prep time (min/batch)" size="s">
              {#snippet children(a)}
                <input id={a.id} aria-describedby={a.describedby} type="number" step="any" bind:value={preset.prepMinutes} />
              {/snippet}
            </Field>
            <Field id={`preset-${key}-labor`} label="Labor rate (per h)" size="s">
              {#snippet children(a)}
                <input id={a.id} aria-describedby={a.describedby} type="number" step="any" bind:value={preset.laborRate} />
              {/snippet}
            </Field>
            <Field id={`preset-${key}-fixed`} label="Monthly fixed costs" size="s">
              {#snippet children(a)}
                <input id={a.id} aria-describedby={a.describedby} type="number" step="any" bind:value={preset.monthlyFixed} />
              {/snippet}
            </Field>
            <Field id={`preset-${key}-hours`} label="Monthly print hours" size="s">
              {#snippet children(a)}
                <input id={a.id} aria-describedby={a.describedby} type="number" step="any" bind:value={preset.monthlyHours} />
              {/snippet}
            </Field>
          </FormGrid>
        </Fieldset>
      </Card>
    {/each}
  </div>
  <div class="add-row">
    <button type="button" data-focus-key="add-preset" onclick={addPreset}>Add preset</button>
  </div>
</Card>

<style>
  .stack { display: flex; flex-direction: column; gap: var(--gap); margin-block-start: var(--space-3); }
  .preset-actions { display: flex; align-items: center; gap: var(--space-2); }
  .add-row { margin-block-start: var(--space-3); }
</style>
