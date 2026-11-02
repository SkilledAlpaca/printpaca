<script lang="ts">
  import { presetDisplayName } from "../../lib/format";
  import { nextKey, type ServerConfig } from "../../lib/settings";

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

<div id="section-presets" class="card roomy" class:danger-border={hasError("presets")}>
  <h2>Presets</h2>
  <p class="hint">Named bundles of failure rate, prep time, labor rate, and overhead that new quotes start from. Editing a rate on the New quote page changes that quote only. Built-in presets can't be removed.</p>
  {#each Object.entries(form.presets) as [key, preset] (key)}
    <div class="subcard">
      <div class="subhead">
        {#if builtinPresetKeys.has(key)}
          <h3>{presetDisplayName(key)}</h3>
          <span class="pill">built-in</span>
        {:else}
          <label class="field field-inline rename-field">
            <span>Name</span>
            <input value={key} onchange={(e) => renamePreset(key, e)} />
          </label>
          <button type="button" aria-label={`Remove preset ${key}`} onclick={() => removePreset(key)}>Remove</button>
        {/if}
      </div>
      {#if renameWarnings[key]}<small class="warn-text" role="alert">{renameWarnings[key]}</small>{/if}
      <div class="field-rows">
        <label class="field">
          <span>Failure rate (%)</span>
          <input type="number" step="any" min="0" max="90" aria-label={`${key} failure rate`} bind:value={preset.failureRatePct} />
        </label>
        <label class="field">
          <span>Prep time (min/batch)</span>
          <input type="number" step="any" aria-label={`${key} prep minutes`} bind:value={preset.prepMinutes} />
        </label>
        <label class="field">
          <span>Labor rate (per h)</span>
          <input type="number" step="any" aria-label={`${key} labor rate`} bind:value={preset.laborRate} />
        </label>
        <label class="field">
          <span>Monthly fixed costs</span>
          <input type="number" step="any" aria-label={`${key} monthly fixed`} bind:value={preset.monthlyFixed} />
        </label>
        <label class="field">
          <span>Monthly print hours</span>
          <input type="number" step="any" aria-label={`${key} monthly hours`} bind:value={preset.monthlyHours} />
        </label>
      </div>
    </div>
  {/each}
  <button type="button" onclick={addPreset}>Add preset</button>
</div>

<style>
  .rename-field { flex: 0 0 auto; }
  .rename-field input { min-height: 32px; padding: 0.25rem 0.5rem; }
</style>
