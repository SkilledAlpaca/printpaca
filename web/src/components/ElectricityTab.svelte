<script lang="ts">
  import {
    computeElectricity,
    effectiveRate,
    nextStartWall,
    parseWallStart,
    scheduleEnergy,
    validateEnergySchedule,
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
  import { scheduleProblemUi } from "../lib/scheduleUi";

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

  // Schedule mode: the cost depends on when the print starts, so the calculator takes a start (wall-clock in
  // the instance time zone) and prices the span against the saved schedule (lib/engine/schedule.ts).
  const zone = $derived(cfg.timezone || "UTC");
  // svelte-ignore state_referenced_locally -- initial seed only; the field is the user's after that.
  let start = $state(nextStartWall(cfg.energy.schedule.defaultStartTime, cfg.timezone || "UTC"));
  const scheduleMode = $derived(cfg.energy.mode === "schedule");
  const scheduleProblem = $derived.by(() => {
    if (!scheduleMode) return "";
    const p = validateEnergySchedule(cfg.energy.schedule, true);
    if (p.length > 0) return `The saved schedule isn't usable yet. ${scheduleProblemUi(p[0]!).text}`;
    if (start === "" || parseWallStart(start, zone) === null) return "Enter the print start.";
    return "";
  });
  const sched = $derived(
    scheduleMode && scheduleProblem === "" && r.valid
      ? scheduleEnergy(cfg.energy.schedule, zone, start, hours ?? 0, (watts ?? 0) / 1000)
      : null,
  );
  const day = $derived(
    scheduleMode && scheduleProblem === "" && r.valid
      ? scheduleEnergy(cfg.energy.schedule, zone, start, 24, (watts ?? 0) / 1000)
      : null,
  );
  const warnings = $derived([...r.warnings, ...energyWarnings]);
  const hoursText = (h: number) => String(Math.round(h * 100) / 100);
</script>

<div class="page">
  <h1>Electricity</h1>

  <div class="card">
    <h2>Electricity calculator</h2>
    <div class="field-rows">
      <LabeledNumber label="Printer power" suffix="W" bind:value={watts} warn={warnFor(warnings, "watts")} />
      <LabeledNumber label="Print time" suffix="h" bind:value={hours} warn={warnFor(warnings, "hours")} />
      {#if scheduleMode}
        <div class="field">
          <label for="elec-start">Print start</label>
          <input id="elec-start" type="datetime-local" bind:value={start} aria-describedby="elec-start-help" aria-invalid={scheduleProblem !== ""} />
          <small id="elec-start-help" class="field-hint">Times are in your shop time zone ({zone}). Starts at the next {cfg.energy.schedule.defaultStartTime} until you change it.</small>
        </div>
      {:else if cfg.energy.mode === "tou"}
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
    {#if scheduleMode}
      <p class="rate-note" tabindex="-1">Using your saved time-of-use schedule. Change it under Saved energy settings below.</p>
      {#if scheduleProblem !== ""}<p class="warn-text" role="alert">{scheduleProblem}</p>{/if}
    {:else if rateOverridden}
      <p class="rate-note" tabindex="-1">
        Rate overridden for this calculation.
        <button type="button" class="quiet" onclick={useConfiguredRate}>
          {#if cfg.energy.mode === "tou"}Use configured rates{:else}Use configured rate ({moneyPrecise(cfg.energy.flatRate, cfg, 3)}/kWh){/if}
        </button>
      </p>
    {:else}
      <p class="rate-note" tabindex="-1">Using your configured energy rate. Change the rate above to try another one. The saved setting stays the same.</p>
    {/if}
    {#if scheduleMode}
      <dl class="out">
        <div><dt>This print</dt><dd>{sched ? moneyPrecise(sched.total, cfg, 3) : "-"}</dd></div>
        <div><dt>Average per hour</dt><dd>{sched && (hours ?? 0) > 0 ? moneyPrecise(sched.total / (hours ?? 1), cfg, 3) : "-"}</dd></div>
        <div><dt>24 h of continuous printing from this start</dt><dd>{day ? moneyAmount(day.total, cfg) : "-"}</dd></div>
      </dl>
      {#if sched && sched.periods.length > 0}
        <h3 class="split-title">Where the hours fall</h3>
        <table class="split">
          <caption class="visually-hidden">Hours and cost by tariff period</caption>
          <thead><tr><th scope="col">Period</th><th scope="col" class="num">Hours</th><th scope="col" class="num">Cost</th></tr></thead>
          <tbody>
            {#each sched.periods as p (p.name)}
              <tr><th scope="row">{p.name}</th><td class="num">{hoursText(p.hours)}</td><td class="num">{moneyPrecise(p.cost, cfg, 3)}</td></tr>
            {/each}
          </tbody>
        </table>
      {/if}
    {:else}
      <dl class="out">
        <div><dt>This print</dt><dd>{r.valid ? moneyPrecise(r.total, cfg, 3) : "-"}</dd></div>
        <div><dt>Per hour</dt><dd>{r.valid ? moneyPrecise(r.perHour, cfg, 3) : "-"}</dd></div>
        <div><dt>24 h of continuous printing</dt><dd>{r.valid ? moneyAmount(r.per24h, cfg) : "-"}</dd></div>
      </dl>
    {/if}
    <details>
      <summary>How this is calculated</summary>
      {#if scheduleMode}
        <code>cost = watts ÷ 1000 × Σ (hours in each window × that window's rate/kWh)</code>
      {:else}
        <code>cost = watts ÷ 1000 × hours × rate/kWh</code>
      {/if}
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
  .split-title { margin: 0.8rem 0 0.2rem; font-size: var(--text-md, 1rem); }
  .split { width: 100%; }
  .rate-note { margin: 0.4rem 0 0; font-size: var(--text-sm); color: var(--muted); }
  /* button.quiet sets no color at rest, so it would inherit .rate-note's --muted and not look actionable. */
  .rate-note button.quiet { color: var(--text); }
</style>
