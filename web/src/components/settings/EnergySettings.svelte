<script lang="ts">
  import type { ServerConfig } from "../../lib/settings";
  import ScheduleEditor from "./ScheduleEditor.svelte";

  let { form, hasError }: { form: ServerConfig; hasError: (section: string) => boolean } = $props();
</script>

<div id="section-energy" class="card roomy" class:danger-border={hasError("energy")}>
  <h2>Saved energy settings</h2>
  <p class="hint">Applies to every quote and is saved to the server. Choose one flat rate, a simple peak/off-peak split, or a full time-of-use schedule with seasons and windows.</p>
  <div class="field-rows">
    <label class="field">
      <span>Mode</span>
      <select bind:value={form.energy.mode}>
        <option value="flat">Flat rate</option>
        <option value="tou">Time-of-use (simple)</option>
        <option value="schedule">Time-of-use schedule</option>
      </select>
    </label>
    {#if form.energy.mode === "flat"}
      <label class="field">
        <span>Flat rate (per kWh)</span>
        <input type="number" min="0" step="0.01" bind:value={form.energy.flatRate} />
      </label>
    {:else if form.energy.mode === "tou"}
      <label class="field">
        <span>Peak rate (per kWh)</span>
        <input type="number" min="0" step="0.01" bind:value={form.energy.tou.peakRate} />
      </label>
      <label class="field">
        <span>Off-peak rate (per kWh)</span>
        <input type="number" min="0" step="0.01" bind:value={form.energy.tou.offPeakRate} />
      </label>
      <label class="field">
        <span>Default off-peak share (0 to 1)</span>
        <input type="number" min="0" max="1" step="0.05" bind:value={form.energy.tou.defaultOffPeakFrac} />
      </label>
    {/if}
  </div>
  {#if form.energy.mode === "schedule"}
    <ScheduleEditor {form} />
  {/if}
</div>
