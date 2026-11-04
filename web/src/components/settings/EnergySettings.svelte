<script lang="ts">
  import type { ServerConfig } from "../../lib/settings";
  import Card from "../ui/Card.svelte";
  import Field from "../ui/Field.svelte";
  import FormGrid from "../ui/FormGrid.svelte";
  import ScheduleEditor from "./ScheduleEditor.svelte";

  let { form, hasError }: { form: ServerConfig; hasError: (section: string) => boolean } = $props();
</script>

<Card id="section-energy" tone={hasError("energy") ? "danger" : "default"}>
<div class="stack">
<h2>Saved energy settings</h2>
  <p class="hint">Applies to every quote and is saved to the server. Choose one flat rate, a simple peak/off-peak split, or a full time-of-use schedule with seasons and windows.</p>
  <FormGrid align="controls">
    <Field id="energy-mode" label="Mode" size="m">
      {#snippet children(f)}
        <select id={f.id} bind:value={form.energy.mode}>
          <option value="flat">Flat rate</option>
          <option value="tou">Time-of-use (simple)</option>
          <option value="schedule">Time-of-use schedule</option>
        </select>
      {/snippet}
    </Field>
    {#if form.energy.mode === "flat"}
      <Field id="energy-flat-rate" label="Flat rate (per kWh)" size="s">
        {#snippet children(f)}
          <input id={f.id} type="number" min="0" step="0.01" bind:value={form.energy.flatRate} />
        {/snippet}
      </Field>
    {:else if form.energy.mode === "tou"}
      <Field id="energy-peak-rate" label="Peak rate (per kWh)" size="s">
        {#snippet children(f)}
          <input id={f.id} type="number" min="0" step="0.01" bind:value={form.energy.tou.peakRate} />
        {/snippet}
      </Field>
      <Field id="energy-offpeak-rate" label="Off-peak rate (per kWh)" size="s">
        {#snippet children(f)}
          <input id={f.id} type="number" min="0" step="0.01" bind:value={form.energy.tou.offPeakRate} />
        {/snippet}
      </Field>
      <Field id="energy-offpeak-frac" label="Default off-peak share (0 to 1)" size="s">
        {#snippet children(f)}
          <input id={f.id} type="number" min="0" max="1" step="0.05" bind:value={form.energy.tou.defaultOffPeakFrac} />
        {/snippet}
      </Field>
    {/if}
  </FormGrid>
  {#if form.energy.mode === "schedule"}
    <ScheduleEditor {form} />
  {/if}
</div>
</Card>

<style>
  .stack { display: flex; flex-direction: column; gap: var(--gap); min-width: 0; }
  .stack > :global(h2), .stack > :global(h3) { margin: 0; }
</style>
