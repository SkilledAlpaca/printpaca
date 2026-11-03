<script lang="ts">
  import {
    computeElectricity,
    effectiveRate,
    validateField,
    type EffectiveConfig,
    type EnergyInputs,
    type FieldWarning,
  } from "../lib/engine/index";
  import { warnFor, moneyAmount, moneyPrecise } from "../lib/format";
  import LabeledNumber from "./LabeledNumber.svelte";
  import { focusAfterUpdate } from "../lib/a11y.svelte";
  import SettingsEditor from "./settings/SettingsEditor.svelte";
  import EnergySettings from "./settings/EnergySettings.svelte";

  let { cfg }: { cfg: EffectiveConfig } = $props();
  let watts = $state<number | null>(250);
  let hours = $state<number | null>(10);
  // Calculator rates follow the configured energy settings until the user edits one; editing latches
  // rateOverridden so the what-if isn't overwritten by config, and the reset button un-latches it.
  let rateOverridden = $state(false);
  // svelte-ignore state_referenced_locally -- initial seed; the $effect
  // below owns the follow-config behavior from then on.
  let rate = $state<number | null>(cfg.energy.flatRate);
  // svelte-ignore state_referenced_locally
  let peakRate = $state<number | null>(cfg.energy.tou.peakRate);
  // svelte-ignore state_referenced_locally
  let offPeakRate = $state<number | null>(cfg.energy.tou.offPeakRate);
  // svelte-ignore state_referenced_locally
  let offPeakFrac = $state<number>(cfg.energy.tou.defaultOffPeakFrac);

  $effect(() => {
    if (!rateOverridden) {
      rate = cfg.energy.flatRate;
      peakRate = cfg.energy.tou.peakRate;
      offPeakRate = cfg.energy.tou.offPeakRate;
    }
  });

  function useConfiguredRate() {
    rateOverridden = false;
    // The button went away with the "overridden" note; focus the note that replaces it.
    void focusAfterUpdate(".rate-note");
  }

  // Mirrors state.svelte.ts's toPartInputs: build EnergyInputs from the mode's fields and let the engine's helper
  // blend peak/off-peak into one rate.
  const energy = $derived<EnergyInputs>(
    cfg.energy.mode === "tou"
      ? { mode: "tou", peakRate: peakRate ?? NaN, offPeakRate: offPeakRate ?? NaN, offPeakFrac }
      : { mode: "flat", flatRate: rate ?? NaN },
  );
  // computeElectricity validates only a single "rate", so TOU's component fields get their own validateField
  // checks to drive per-field warnings.
  const energyWarnings = $derived<FieldWarning[]>(
    cfg.energy.mode === "tou"
      ? [
          ...validateField("peakRate", peakRate, { min: 0, required: true }),
          ...validateField("offPeakRate", offPeakRate, { min: 0, required: true }),
        ]
      : [],
  );

  const r = $derived(
    computeElectricity({ watts: watts ?? NaN, hours: hours ?? NaN, rate: effectiveRate(energy) }),
  );
  const warnings = $derived([...r.warnings, ...energyWarnings]);
</script>

<div class="page">
  <h1>Electricity</h1>

  <div class="card">
    <h2>Electricity calculator</h2>
    <div class="field-rows">
      <LabeledNumber label="Printer power" suffix="W" bind:value={watts} warn={warnFor(warnings, "watts")} />
      <LabeledNumber label="Print time" suffix="h" bind:value={hours} warn={warnFor(warnings, "hours")} />
      {#if cfg.energy.mode === "tou"}
        <LabeledNumber label="Peak rate" suffix="per kWh" step={0.01} bind:value={peakRate} warn={warnFor(warnings, "peakRate")} onchanged={() => (rateOverridden = true)} />
        <LabeledNumber label="Off-peak rate" suffix="per kWh" step={0.01} bind:value={offPeakRate} warn={warnFor(warnings, "offPeakRate")} onchanged={() => (rateOverridden = true)} />
        <label class="field">
          <span>Off-peak share: {Math.round(offPeakFrac * 100)}%</span>
          <input type="range" min="0" max="1" step="0.05" bind:value={offPeakFrac} />
        </label>
      {:else}
        <LabeledNumber label="Rate for this calculation" suffix="per kWh" step={0.01} bind:value={rate} warn={warnFor(warnings, "rate")} onchanged={() => (rateOverridden = true)} />
      {/if}
    </div>
    {#if rateOverridden}
      <p class="rate-note" tabindex="-1">
        Rate overridden for this calculation.
        <button type="button" class="quiet" onclick={useConfiguredRate}>
          {#if cfg.energy.mode === "tou"}Use configured rates{:else}Use configured rate ({moneyPrecise(cfg.energy.flatRate, cfg, 3)}/kWh){/if}
        </button>
      </p>
    {:else}
      <p class="rate-note" tabindex="-1">Using your configured energy rate. Change the rate above to try another one. The saved setting stays the same.</p>
    {/if}
    <dl class="out">
      <div><dt>This print</dt><dd>{r.valid ? moneyPrecise(r.total, cfg, 3) : "-"}</dd></div>
      <div><dt>Per hour</dt><dd>{r.valid ? moneyPrecise(r.perHour, cfg, 3) : "-"}</dd></div>
      <div><dt>24 h of continuous printing</dt><dd>{r.valid ? moneyAmount(r.per24h, cfg) : "-"}</dd></div>
    </dl>
    <details>
      <summary>How this is calculated</summary>
      <code>cost = watts ÷ 1000 × hours × rate/kWh</code>
    </details>
  </div>

  <SettingsEditor>
    {#snippet children({ form, hasError })}
      <EnergySettings {form} {hasError} />
    {/snippet}
  </SettingsEditor>
</div>

<style>
  .out div { display: flex; justify-content: space-between; padding: 0.3rem 0; border-top: 1px solid var(--border); }
  .out dd { margin: 0; font-variant-numeric: tabular-nums; }
  details { color: var(--muted); font-size: var(--text-sm); margin-top: 0.5rem; }
  .rate-note { margin: 0.4rem 0 0; font-size: var(--text-sm); color: var(--muted); }
  /* button.quiet sets no color at rest, so it would inherit .rate-note's --muted and not look actionable. */
  .rate-note button.quiet { color: var(--text); }
</style>
