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
  import { warnFor, moneyAmount, moneyPrecise, rateText } from "../lib/format";
  import LabeledNumber from "./LabeledNumber.svelte";
  import PageHeader from "./ui/PageHeader.svelte";
  import Card from "./ui/Card.svelte";
  import Field from "./ui/Field.svelte";
  import FormGrid from "./ui/FormGrid.svelte";
  import KeyValueTable, { type KeyValueRow } from "./ui/KeyValueTable.svelte";
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

  // The results, as rows of a KeyValueTable (D28): flat and time-of-use modes, then the schedule mode.
  const resultRows = $derived.by<KeyValueRow[]>(() =>
    scheduleMode
      ? [
          { label: "This print", value: sched ? moneyPrecise(sched.total, cfg, 3) : "-" },
          { label: "Average per hour", value: sched && (hours ?? 0) > 0 ? moneyPrecise(sched.total / (hours ?? 1), cfg, 3) : "-" },
          { label: "24 h of continuous printing from this start", value: day ? moneyAmount(day.total, cfg) : "-" },
        ]
      : [
          { label: "This print", value: r.valid ? moneyPrecise(r.total, cfg, 3) : "-" },
          { label: "Per hour", value: r.valid ? moneyPrecise(r.perHour, cfg, 3) : "-" },
          { label: "24 h of continuous printing", value: r.valid ? moneyAmount(r.per24h, cfg) : "-" },
        ],
  );
</script>

<div class="page">
  <PageHeader title="Electricity" />

  <Card>
<div class="stack">
<h2>Electricity calculator</h2>
    <p class="hint">A kilowatt-hour (kWh) is the unit on your power bill: one kilowatt (1,000 W) running for one hour. Rates are dollars per kWh, such as {rateText(0.15, cfg)}. Times use the 24-hour clock, so 22:00 is 10 PM.</p>
    <FormGrid align="controls">
      <LabeledNumber label="Printer power" suffix="W" bind:value={watts} warn={warnFor(warnings, "watts")} />
      <LabeledNumber label="Print time" suffix="h" bind:value={hours} warn={warnFor(warnings, "hours")} />
      {#if scheduleMode}
        <Field id="elec-start" label="Print start" size="m" describedby="elec-start-help">
          {#snippet children(f)}
            <input id={f.id} type="datetime-local" bind:value={start} aria-describedby={f.describedby} aria-invalid={scheduleProblem !== ""} />
            <small id="elec-start-help" class="hint-text">Times are in your shop time zone ({zone}). Starts at the next {cfg.energy.schedule.defaultStartTime} until you change it.</small>
          {/snippet}
        </Field>
      {:else if cfg.energy.mode === "tou"}
        <LabeledNumber label="Peak rate" suffix="per kWh" step={0.01} bind:value={peakRate} warn={warnFor(warnings, "peakRate")} onchanged={() => (rateOverridden = true)} />
        <LabeledNumber label="Off-peak rate" suffix="per kWh" step={0.01} bind:value={offPeakRate} warn={warnFor(warnings, "offPeakRate")} onchanged={() => (rateOverridden = true)} />
        <Field id="elec-offpeak-share" label={`Off-peak share: ${Math.round(offPeakFrac * 100)}%`} size="m">
          {#snippet children(f)}
            <input id={f.id} type="range" min="0" max="1" step="0.05" bind:value={offPeakFrac} />
          {/snippet}
        </Field>
      {:else}
        <LabeledNumber label="Rate for this calculation" suffix="per kWh" step={0.01} bind:value={rate} warn={warnFor(warnings, "rate")} onchanged={() => (rateOverridden = true)} />
      {/if}
    </FormGrid>
    {#if scheduleMode}
      <p class="rate-note" tabindex="-1">Using your saved time-of-use schedule. Change it under Saved energy settings below.</p>
      {#if scheduleProblem !== ""}<p class="warn-text" role="alert">{scheduleProblem}</p>{/if}
    {:else if rateOverridden}
      <p class="rate-note" tabindex="-1">
        Rate overridden for this calculation.
        <button type="button" class="quiet" onclick={useConfiguredRate}>
          {#if cfg.energy.mode === "tou"}Use configured rates{:else}Use configured rate ({rateText(cfg.energy.flatRate, cfg)}){/if}
        </button>
      </p>
    {:else}
      <p class="rate-note" tabindex="-1">Using your configured energy rate. Change the rate above to try another one. The saved setting stays the same.</p>
    {/if}
    <KeyValueTable label="Electricity results" rows={resultRows} />
    {#if scheduleMode && sched && sched.periods.length > 0}
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
    <details>
      <summary>How this is calculated</summary>
      {#if scheduleMode}
        <code>cost = watts ÷ 1000 × Σ (hours in each window × that window's rate/kWh)</code>
      {:else}
        <code>cost = watts ÷ 1000 × hours × rate/kWh</code>
      {/if}
    </details>
  </div>
</Card>

  <SettingsEditor>
    {#snippet children({ form, hasError })}
      <EnergySettings {form} {hasError} />
    {/snippet}
  </SettingsEditor>
</div>

<style>
  details { color: var(--muted); font-size: var(--type-secondary); }
  .hint-text { color: var(--muted); font-size: var(--type-secondary); }
  .split-title { margin: 0; font-size: var(--type-heading); }
  .split { width: 100%; }
  .rate-note { margin: 0; font-size: var(--type-secondary); color: var(--muted); }
  /* button.quiet sets no color at rest, so it would inherit .rate-note's --muted and not look actionable. */
  .rate-note button.quiet { color: var(--text); }
</style>
