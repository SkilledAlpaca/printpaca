<script lang="ts">
  import { computeSpoolPrice, computeRemainingLength, type EffectiveConfig } from "../lib/engine/index";
  import { FALLBACK_DENSITY } from "../lib/filamentDefaults";
  import { warnFor, friendlyMessage, moneyAmount, moneyPrecise } from "../lib/format";
  import type { ServerConfig } from "../lib/settings";
  import LabeledNumber from "./LabeledNumber.svelte";
  import PageHeader from "./ui/PageHeader.svelte";
  import Card from "./ui/Card.svelte";
  import Field from "./ui/Field.svelte";
  import FormGrid from "./ui/FormGrid.svelte";
  import KeyValueTable from "./ui/KeyValueTable.svelte";
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
  <PageHeader title="Filament" />

  <SettingsEditor bind:form={settingsForm}>
    {#snippet children({ form, hasError })}
      <MaterialsSettings {form} {hasError} />
    {/snippet}
  </SettingsEditor>

  <SpoolmanInventory {cfg} />

  <div class="cards">
    <Card>
<div class="stack">
<h2>Spool price</h2>
      <FormGrid align="controls">
        <LabeledNumber label="Spool price" suffix={cfg.currency.symbol} bind:value={spoolPrice} warn={warnFor(price.warnings, "spoolPrice")} />
        <LabeledNumber label="Spool weight" suffix="kg" step={0.25} bind:value={spoolWeightKg} warn={warnFor(price.warnings, "spoolWeightKg")} />
      </FormGrid>
      <KeyValueTable
        label="Spool price results"
        rows={[
          { label: "Per gram", value: price.valid ? moneyPrecise(price.perGram, cfg, 3) : "-" },
          { label: "Per kg", value: price.valid ? moneyAmount(price.perKg, cfg) : "-" },
        ]}
      />
      {#if settingsForm && settingsForm.materials.length > 0}
        <FormGrid align="controls">
          <Field id="apply-material" label="Apply to" size="m">
            {#snippet children(f)}
              <select id={f.id} bind:value={applyMaterialId}>
                {#each settingsForm!.materials as m (m.id)}<option value={m.id}>{m.name}</option>{/each}
              </select>
            {/snippet}
          </Field>
        </FormGrid>
        <div class="apply-row">
          <button type="button" disabled={!canApply} onclick={applyToMaterial}>
            Apply {price.valid ? moneyAmount(price.perKg, cfg) : ""} to {targetMaterial?.name || "material"}
          </button>
        </div>
        <p class="hint" role="status">{appliedNotice}</p>
      {/if}
    </div>
</Card>

    <Card>
<div class="stack">
<h2>Remaining length</h2>
      <FormGrid align="controls">
        <LabeledNumber label="Remaining" suffix="g" bind:value={remainingG} warn={warnFor(length.warnings, "grams")} />
        <LabeledNumber label="Diameter" suffix="mm" step={0.05} bind:value={diameterMm} warn={warnFor(length.warnings, "diameterMm")} />
        <Field id="remaining-material" label={`Material (density ${density} g/cm³)`} size="m">
          {#snippet children(f)}
            <select id={f.id} bind:value={materialId}>
              {#each cfg.materials as m (m.id)}<option value={m.id}>{m.name}</option>{/each}
            </select>
          {/snippet}
        </Field>
      </FormGrid>
      <KeyValueTable label="Remaining length results" rows={[{ label: "Length", value: length.valid ? length.lengthM.toFixed(1) + " m" : "-" }]} />
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
</Card>
  </div>
</div>

<style>
  .cards { display: grid; gap: var(--gap-section); }
  .stack { display: flex; flex-direction: column; gap: var(--gap); min-width: 0; }
  .stack > :global(h2), .stack > :global(h3) { margin: 0; }
  .density-status { margin: 0; }
  /* The density warning is advisory, so it reads in --warn; .status-line supplies the live-region
   * min-height. Two classes outrank either single-class theme rule regardless of declaration order. */
  .density-status.visible { color: var(--warn); }
  details { color: var(--muted); font-size: var(--type-secondary); }
  .apply-row { display: flex; flex-wrap: wrap; gap: var(--space-2); }
</style>
