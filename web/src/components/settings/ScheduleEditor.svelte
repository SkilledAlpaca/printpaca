<script lang="ts">
  import type { ServerConfig } from "../../lib/settings";
  import { blankSchedule, sceExampleSchedule } from "../../lib/scheduleExamples";
  import type { TouDay, TouSeason } from "../../lib/engine/schedule";
  import { describePath, marksPath, overnightWindowPaths, scheduleProblems } from "../../lib/scheduleUi";
  import { announce, focusAfterUpdate } from "../../lib/a11y.svelte";
  import { keySelector } from "../../lib/a11yFocus";
  import Field from "../ui/Field.svelte";
  import FormGrid from "../ui/FormGrid.svelte";
  import ConfirmInline from "../ui/ConfirmInline.svelte";
  import RowEditor from "../ui/RowEditor.svelte";
  import { focusAfterRowRemoved } from "../ui/rowEditor";

  // The editor for energy.schedule (server/energy_schedule.go validates the same rules). Times are wall-clock
  // in the instance time zone from Settings.
  let { form }: { form: ServerConfig } = $props();

  const schedule = $derived(form.energy.schedule);
  // Overnight windows are not a problem: they are split at midnight when you save (scheduleUi.ts).
  const problems = $derived(scheduleProblems(schedule, form.energy.mode === "schedule"));
  const overnight = $derived(overnightWindowPaths(schedule));
  // aria-invalid + the warn style for the input at `path` (a server path under energy.schedule).
  const bad = (path: string) => marksPath(problems, path);
  const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
  const DAY_TYPES = [
    { key: "weekday", label: "Weekday" },
    { key: "weekend", label: "Weekend and holidays" },
  ] as const;

  const holidayText = $derived(schedule.holidays.join("\n"));
  function setHolidays(text: string) {
    schedule.holidays = text.split(/[\s,]+/).filter((s) => s !== "");
  }

  function toggleMonth(season: TouSeason, month: number, on: boolean) {
    const set = new Set(season.months);
    if (on) set.add(month);
    else set.delete(month);
    season.months = [...set].sort((a, b) => a - b);
  }

  // Row keys for RowEditor's ⋯ buttons: by position, since seasons and windows are not reordered here.
  const seasonKey = (si: number) => `season-${si}`;

  function addSeason() {
    if (schedule.seasons.length >= 6) return;
    const n = schedule.seasons.length + 1;
    const day = (): TouDay => ({ baseName: "Standard", baseRate: 0.15, windows: [] });
    schedule.seasons.push({ name: `Season ${n}`, months: [], weekday: day(), weekend: day() });
    announce(`Season ${n} added.`);
  }
  function removeSeason(i: number) {
    schedule.seasons = schedule.seasons.filter((_, j) => j !== i);
    announce("Season removed.");
    void focusAfterRowRemoved(schedule.seasons.map((_, j) => seasonKey(j)), i, "add-season");
  }
  function addWindow(day: TouDay, prefix: string) {
    if (day.windows.length >= 8) return;
    day.windows.push({ name: "Peak", start: "16:00", end: "21:00", rate: day.baseRate });
    announce("Window added.");
    void focusAfterUpdate(`[data-win="${prefix}-${day.windows.length - 1}"]`);
  }
  function removeWindow(day: TouDay, i: number, prefix: string) {
    day.windows = day.windows.filter((_, j) => j !== i);
    announce("Window removed.");
    void focusAfterRowRemoved(day.windows.map((_, j) => `${prefix}-${j}`), i, `${prefix}-add`);
  }

  // Loading the example replaces the whole schedule, so it asks first when one exists (two-step confirm).
  let confirmingExample = $state(false);
  function loadExample() {
    const keepClock = schedule.defaultStartTime;
    form.energy.schedule = { ...sceExampleSchedule(), defaultStartTime: keepClock || "22:00" };
    confirmingExample = false;
    announce("Example schedule loaded. These are example rates. Check them against your bill.");
  }
  function askExample() {
    if (schedule.seasons.length === 0) loadExample();
    else confirmingExample = true;
  }
  function useBlank() {
    form.energy.schedule = { ...blankSchedule(form.energy.flatRate), defaultStartTime: schedule.defaultStartTime };
    announce("Blank schedule added with one season.");
  }
</script>

<div class="schedule">
  <p class="hint">
    Each print is priced by the hours it runs. It starts at the print start you enter on the quote, runs back to back for the quantity, and pays the rate of whichever window each hour falls in.
    Times are 24-hour clock times in your time zone ({form.timezone || "UTC"}). Hours outside every window use the base rate. Saturdays, Sundays, and the holidays you list use the weekend schedule.
  </p>

  <FormGrid align="controls">
    <Field id="sched-start" label="Default print start (24-hour time, such as 22:00)" size="s" describedby="sched-start-help">
      {#snippet children(f)}
        <input id={f.id} type="text" inputmode="numeric" placeholder="22:00" aria-describedby={f.describedby} bind:value={schedule.defaultStartTime} class:warn={bad("defaultStartTime")} aria-invalid={bad("defaultStartTime")} />
        <small id="sched-start-help" class="hint-text">Public quotes and new orders start at the next time this clock time comes round.</small>
      {/snippet}
    </Field>
    <Field id="sched-holidays" label="Holidays (one date per line, YYYY-MM-DD)" size="m" describedby="sched-holidays-help">
      {#snippet children(f)}
        <textarea id={f.id} rows="3" aria-describedby={f.describedby} value={holidayText} oninput={(e) => setHolidays(e.currentTarget.value)}></textarea>
        <small id="sched-holidays-help" class="hint-text">Up to 100 dates. Each uses the weekend schedule.</small>
      {/snippet}
    </Field>
  </FormGrid>

  <div class="example">
    {#if confirmingExample}
      <p id="sched-example-confirm" class="warn-text">Replace your seasons, windows, and rates with the example?</p>
      <div class="form-actions start">
        <ConfirmInline
          confirmLabel="Yes, replace with example"
          danger
          restore={keySelector("load-example")}
          onconfirm={loadExample}
          oncancel={() => (confirmingExample = false)}
        />
      </div>
    {:else}
      <div class="form-actions start">
        <button type="button" data-focus-key="load-example" onclick={askExample}>Load SCE TOU-D-4-9PM example</button>
        {#if schedule.seasons.length === 0}<button type="button" onclick={useBlank}>Start with one season</button>{/if}
      </div>
      <p class="hint">The example shows the structure of Southern California Edison's TOU-D-4-9PM plan: summer June to September and winter October to May, with weekday and weekend hours, an on-peak 4 to 9 pm window, and a winter super off-peak 8 am to 4 pm window. The rates are examples, not SCE's published rates. Check every rate against your own bill before you use it.</p>
    {/if}
  </div>

  {#if problems.length > 0}
    <ul class="problems warn-text" id="sched-problems" aria-label="Schedule problems">
      {#each problems as p, pi (pi)}<li>{p.text}</li>{/each}
    </ul>
  {/if}
  {#if overnight.length > 0}
    <p class="hint" id="sched-overnight">
      {overnight.map(describePath).join("; ")} run{overnight.length === 1 ? "s" : ""} past midnight. A window can't cross midnight, so when you save,
      each one is split into two windows with the same name and rate: one up to 24:00 and one from 00:00.
    </p>
  {/if}

  {#each schedule.seasons as season, si (si)}
    <RowEditor
      tone="inset"
      label={`Season ${si + 1}: ${season.name}`}
      rowKey={seasonKey(si)}
      onremove={() => removeSeason(si)}
      removeLabel={`Remove season ${si + 1}`}
    >
      {#snippet fields()}
        <FormGrid>
          <Field id={`season-${si}-name`} label="Season name" size="m">
            {#snippet children(f)}
              <input id={f.id} aria-label={`Season ${si + 1} name`} bind:value={season.name} class:warn={bad(`seasons[${si}].name`)} aria-invalid={bad(`seasons[${si}].name`)} />
            {/snippet}
          </Field>
        </FormGrid>
        <fieldset class="months" class:danger-border={bad(`seasons[${si}].months`) || bad("seasons")} aria-describedby={problems.length > 0 ? "sched-problems" : undefined}>
          <legend>Months in season {si + 1}</legend>
          <div class="month-list">
            {#each MONTHS as name, mi (mi)}
              <label class="month"><input type="checkbox" checked={season.months.includes(mi + 1)} onchange={(e) => toggleMonth(season, mi + 1, e.currentTarget.checked)} />{name}</label>
            {/each}
          </div>
        </fieldset>
      {/snippet}
      {#snippet below()}
        {#each DAY_TYPES as dt (dt.key)}
          {@const day = season[dt.key]}
          {@const prefix = `s${si}-${dt.key}`}
          <div class="day" role="group" aria-label={`Season ${si + 1} ${dt.label}`}>
            <h4>{dt.label}</h4>
            <FormGrid>
              <Field id={`season-${si}-${dt.key}-base-name`} label="Base period name" size="m">
                {#snippet children(f)}
                  <input id={f.id} aria-label={`Season ${si + 1} ${dt.label} base name`} bind:value={day.baseName} class:warn={bad(`seasons[${si}].${dt.key}.baseName`)} aria-invalid={bad(`seasons[${si}].${dt.key}.baseName`)} />
                {/snippet}
              </Field>
              <Field id={`season-${si}-${dt.key}-base-rate`} label="Base rate (per kWh)" size="s">
                {#snippet children(f)}
                  <input id={f.id} type="number" min="0" step="0.001" aria-label={`Season ${si + 1} ${dt.label} base rate`} bind:value={day.baseRate} class:warn={bad(`seasons[${si}].${dt.key}.baseRate`)} aria-invalid={bad(`seasons[${si}].${dt.key}.baseRate`)} />
                {/snippet}
              </Field>
            </FormGrid>
            {#each day.windows as w, wi (wi)}
              {@const wp = `seasons[${si}].${dt.key}.windows[${wi}]`}
              <RowEditor
                label={`Season ${si + 1} ${dt.label} window ${wi + 1}: ${w.name}`}
                rowKey={`${prefix}-${wi}`}
                onremove={() => removeWindow(day, wi, prefix)}
                removeLabel="Remove window"
              >
                {#snippet fields()}
                  <FormGrid>
                    <Field id={`${prefix}-${wi}-name`} label="Window name" size="m">
                      {#snippet children(f)}
                        <input id={f.id} data-win={`${prefix}-${wi}`} aria-label={`Season ${si + 1} ${dt.label} window ${wi + 1} name`} bind:value={w.name} class:warn={bad(`${wp}.name`)} aria-invalid={bad(`${wp}.name`)} />
                      {/snippet}
                    </Field>
                    <Field id={`${prefix}-${wi}-start`} label="Start" size="xs">
                      {#snippet children(f)}
                        <input id={f.id} inputmode="numeric" placeholder="16:00" aria-label={`Season ${si + 1} ${dt.label} window ${wi + 1} start`} bind:value={w.start} class:warn={bad(`${wp}.start`)} aria-invalid={bad(`${wp}.start`)} />
                      {/snippet}
                    </Field>
                    <Field id={`${prefix}-${wi}-end`} label="End" size="xs">
                      {#snippet children(f)}
                        <input id={f.id} inputmode="numeric" placeholder="21:00" aria-label={`Season ${si + 1} ${dt.label} window ${wi + 1} end`} bind:value={w.end} class:warn={bad(`${wp}.end`)} aria-invalid={bad(`${wp}.end`)} />
                      {/snippet}
                    </Field>
                    <Field id={`${prefix}-${wi}-rate`} label="Rate (per kWh)" size="s">
                      {#snippet children(f)}
                        <input id={f.id} type="number" min="0" step="0.001" aria-label={`Season ${si + 1} ${dt.label} window ${wi + 1} rate`} bind:value={w.rate} class:warn={bad(`${wp}.rate`)} aria-invalid={bad(`${wp}.rate`)} />
                      {/snippet}
                    </Field>
                  </FormGrid>
                {/snippet}
              </RowEditor>
            {/each}
            <button type="button" data-focus-key={`${prefix}-add`} onclick={() => addWindow(day, prefix)} aria-disabled={day.windows.length >= 8}>Add window to {dt.label.toLowerCase()}</button>
          </div>
        {/each}
      {/snippet}
    </RowEditor>
  {/each}
  <button type="button" class="add-season" data-focus-key="add-season" onclick={addSeason} aria-disabled={schedule.seasons.length >= 6}>Add season</button>
</div>

<style>
  .schedule { display: flex; flex-direction: column; gap: var(--gap); }
  .hint-text { color: var(--muted); font-size: var(--type-secondary); }
  .start { justify-content: flex-start; margin-top: 0; }
  .problems { margin: 0; padding-inline-start: var(--space-5); }
  .months { min-width: 0; border: 0; padding: 0; margin: 0; }
  .months > legend { padding: 0; font-size: var(--type-secondary); font-weight: 600; margin-bottom: var(--space-1); }
  .month-list { display: flex; flex-wrap: wrap; gap: var(--space-1) var(--space-4); }
  .month { display: inline-flex; align-items: center; gap: var(--space-1); min-height: 24px; }
  .months.danger-border { border-inline-start: 3px solid var(--danger); padding-inline-start: var(--space-3); }
  .day { display: flex; flex-direction: column; align-items: stretch; gap: var(--gap); }
  .day h4 { margin: 0; }
  .day > button, .add-season { align-self: flex-start; }
  .example .hint { margin-top: var(--space-2); }
</style>
