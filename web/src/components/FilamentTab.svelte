<script lang="ts">
  import { computeSpoolPrice, computeRemainingLength, type EffectiveConfig } from "../lib/engine/index";
  import { FALLBACK_DENSITY } from "../lib/filamentDefaults";
  import { warnFor, friendlyMessage, moneyAmount, moneyPrecise } from "../lib/format";
  import type { ServerConfig } from "../lib/settings";
  import LabeledNumber from "./LabeledNumber.svelte";
  import SettingsEditor from "./settings/SettingsEditor.svelte";
  import MaterialsSettings from "./settings/MaterialsSettings.svelte";
  import SpoolmanInventory from "./SpoolmanInventory.svelte";

  let { cfg }: { cfg: EffectiveConfig } = $props();

  let spoolPrice = $state<number | null>(19.99);
  let spoolWeightKg = $state<number | null>(1);
  const price = $derived(computeSpoolPrice(spoolPrice ?? NaN, spoolWeightKg ?? NaN));

  // The editable settings draft, bound to SettingsEditor's `form` so the spool-price card (outside its
  // children snippet) can write into it. Writing through the same object makes the edit dirty-track and show
  // the save bar, like typing into MaterialsSettings' price/kg.
  let settingsForm = $state<ServerConfig | null>(null);

  let applyMaterialId = $state("");
  // Seeds the selector once the draft loads: the configured default material if it still exists, else first.
  $effect(() => {
    if (settingsForm && applyMaterialId === "" && settingsForm.materials.length > 0) {
      applyMaterialId =
        settingsForm.materials.find((m) => m.id === cfg.defaults.material)?.id ?? settingsForm.materials[0]!.id;
    }
  });
  const targetMaterial = $derived(settingsForm?.materials.find((m) => m.id === applyMaterialId));
  const canApply = $derived(price.valid && targetMaterial !== undefined);

  let appliedNotice = $state("");
  function applyToMaterial() {
    if (!targetMaterial || !price.valid) return;
    targetMaterial.pricePerKg = Math.round(price.perKg * 100) / 100;
    appliedNotice = `Applied to ${targetMaterial.name}. Press Save changes to keep it.`;
  }

  let remainingG = $state<number | null>(300);
  let diameterMm = $state<number | null>(1.75);
  // svelte-ignore state_referenced_locally -- seed only: the select is
  // user-editable after mount; tracking cfg live would stomp edits.
  let materialId = $state(cfg.defaults.material);
  const density = $derived(cfg.materials.find((m) => m.id === materialId)?.density ?? FALLBACK_DENSITY);
  const length = $derived(computeRemainingLength(remainingG ?? NaN, diameterMm ?? NaN, density));

  // "density" comes from the material select, so there is no LabeledNumber to attach its warning to. A single
  // always-present region (never inserted or removed) lets screen readers pick up the change; role/aria-live
  // swap with severity.
  const densityWarning = $derived(length.warnings.find((w) => w.field === "density"));
</script>

<div class="page">
  <h1>Filament</h1>

  <SettingsEditor bind:form={settingsForm}>
    {#snippet children({ form, hasError })}
      <MaterialsSettings {form} {hasError} />
    {/snippet}
  </SettingsEditor>

  <SpoolmanInventory {cfg} />

  <div class="stack">
    <div class="card">
      <h2>Spool price</h2>
      <div class="field-rows">
        <LabeledNumber label="Spool price" suffix={cfg.currency.symbol} bind:value={spoolPrice} warn={warnFor(price.warnings, "spoolPrice")} />
        <LabeledNumber label="Spool weight" suffix="kg" step={0.25} bind:value={spoolWeightKg} warn={warnFor(price.warnings, "spoolWeightKg")} />
      </div>
      <dl class="out">
        <div><dt>Per gram</dt><dd>{price.valid ? moneyPrecise(price.perGram, cfg, 3) : "-"}</dd></div>
        <div><dt>Per kg</dt><dd>{price.valid ? moneyAmount(price.perKg, cfg) : "-"}</dd></div>
      </dl>
      {#if settingsForm && settingsForm.materials.length > 0}
        <div class="apply-row">
          <label class="field">
            <span>Apply to</span>
            <select bind:value={applyMaterialId}>
              {#each settingsForm.materials as m (m.id)}<option value={m.id}>{m.name}</option>{/each}
            </select>
          </label>
          <button type="button" disabled={!canApply} onclick={applyToMaterial}>
            Apply {price.valid ? moneyAmount(price.perKg, cfg) : ""} to {targetMaterial?.name || "material"}
          </button>
        </div>
        <p class="hint" role="status">{appliedNotice}</p>
      {/if}
    </div>

    <div class="card">
      <h2>Remaining length</h2>
      <div class="field-rows">
        <LabeledNumber label="Remaining" suffix="g" bind:value={remainingG} warn={warnFor(length.warnings, "grams")} />
        <LabeledNumber label="Diameter" suffix="mm" step={0.05} bind:value={diameterMm} warn={warnFor(length.warnings, "diameterMm")} />
        <label class="field">
          <span>Material (density {density} g/cm³)</span>
          <select bind:value={materialId}>
            {#each cfg.materials as m (m.id)}<option value={m.id}>{m.name}</option>{/each}
          </select>
        </label>
      </div>
      <dl class="out">
        <div><dt>Length</dt><dd>{length.valid ? length.lengthM.toFixed(1) + " m" : "-"}</dd></div>
      </dl>
      <p
        class="density-status status-line"
        class:visible={!!densityWarning}
        role={densityWarning ? "alert" : "status"}
        aria-live={densityWarning ? "assertive" : "polite"}
      >{densityWarning ? friendlyMessage(densityWarning) : ""}</p>
      <details>
        <summary>How this is calculated</summary>
        <code>length = (grams ÷ density) × 1000 ÷ (π × (⌀/2)²)</code>
      </details>
    </div>
  </div>
</div>

<style>
  .stack { display: grid; gap: 1rem; }
  .out div { display: flex; justify-content: space-between; padding: 0.3rem 0; border-top: 1px solid var(--border); }
  .out dd { margin: 0; font-variant-numeric: tabular-nums; }
  .density-status { margin: 0; }
  /* The density warning is advisory, so it reads in --warn; .status-line supplies the live-region
   * min-height. Two classes outrank either single-class theme rule regardless of declaration order. */
  .density-status.visible { color: var(--warn); margin: 0.4rem 0 0; }
  details { color: var(--muted); font-size: var(--text-sm); margin-top: 0.5rem; }
  /* Mirrors the Remaining length card's "Material (density ...)" field below: the same .field select paired
   * with a submit button. */
  .apply-row { display: flex; align-items: flex-end; flex-wrap: wrap; gap: 0.6rem; margin-top: 0.6rem; }
  .apply-row .field { flex: 1 1 12rem; }
</style>
