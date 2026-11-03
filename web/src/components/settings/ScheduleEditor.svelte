<script lang="ts">
  import type { ServerConfig } from "../../lib/settings";
  import { blankSchedule, sceExampleSchedule } from "../../lib/scheduleExamples";
  import type { TouDay, TouSeason } from "../../lib/engine/schedule";
  import { describePath, marksPath, overnightWindowPaths, scheduleProblems } from "../../lib/scheduleUi";
  import { announce, focusAfterUpdate } from "../../lib/a11y.svelte";
  import { confirmFocus, focusAfterRowRemoval, keySelector } from "../../lib/a11yFocus";

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
    void focusAfterRowRemoval("remove-season", i, schedule.seasons.length, "add-season");
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
    void focusAfterRowRemoval(prefix, i, day.windows.length, `${prefix}-add`);
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

  <div class="field-rows">
    <label class="field">
      <span>Default print start (HH:MM)</span>
      <input type="text" inputmode="numeric" placeholder="22:00" aria-describedby="sched-start-help" bind:value={schedule.defaultStartTime} class:warn={bad("defaultStartTime")} aria-invalid={bad("defaultStartTime")} />
      <small id="sched-start-help" class="field-hint">Public quotes and new orders start at the next time this clock time comes round.</small>
    </label>
    <label class="field">
      <span>Holidays (one date per line, YYYY-MM-DD)</span>
      <textarea rows="3" aria-describedby="sched-holidays-help" value={holidayText} oninput={(e) => setHolidays(e.currentTarget.value)}></textarea>
      <small id="sched-holidays-help" class="field-hint">Up to 100 dates. Each uses the weekend schedule.</small>
    </label>
  </div>

  <div class="example">
    {#if confirmingExample}
      <p id="sched-example-confirm" class="warn-text">Replace your seasons, windows, and rates with the example?</p>
      <div class="form-actions">
        <button type="button" class="danger" aria-describedby="sched-example-confirm" onclick={loadExample} use:confirmFocus={{ restore: keySelector("load-example"), cancel: () => (confirmingExample = false) }}>Replace with example</button>
        <button type="button" onclick={() => (confirmingExample = false)}>Cancel</button>
      </div>
    {:else}
      <div class="form-actions">
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
    <fieldset class="season">
      <legend>Season {si + 1}: {season.name}</legend>
      <div class="field-rows">
        <label class="field">
          <span>Season name</span>
          <input aria-label={`Season ${si + 1} name`} bind:value={season.name} class:warn={bad(`seasons[${si}].name`)} aria-invalid={bad(`seasons[${si}].name`)} />
        </label>
      </div>
      <fieldset class="months" class:danger-border={bad(`seasons[${si}].months`) || bad("seasons")} aria-describedby={problems.length > 0 ? "sched-problems" : undefined}>
        <legend>Months in season {si + 1}</legend>
        {#each MONTHS as name, mi (mi)}
          <label class="month"><input type="checkbox" checked={season.months.includes(mi + 1)} onchange={(e) => toggleMonth(season, mi + 1, e.currentTarget.checked)} />{name}</label>
        {/each}
      </fieldset>

      {#each DAY_TYPES as dt (dt.key)}
        {@const day = season[dt.key]}
        {@const prefix = `s${si}-${dt.key}`}
        <div class="day" role="group" aria-label={`Season ${si + 1} ${dt.label}`}>
          <h4>{dt.label}</h4>
          <div class="field-rows">
            <label class="field">
              <span>Base period name</span>
              <input aria-label={`Season ${si + 1} ${dt.label} base name`} bind:value={day.baseName} class:warn={bad(`seasons[${si}].${dt.key}.baseName`)} aria-invalid={bad(`seasons[${si}].${dt.key}.baseName`)} />
            </label>
            <label class="field">
              <span>Base rate (per kWh)</span>
              <input type="number" min="0" step="0.001" aria-label={`Season ${si + 1} ${dt.label} base rate`} bind:value={day.baseRate} class:warn={bad(`seasons[${si}].${dt.key}.baseRate`)} aria-invalid={bad(`seasons[${si}].${dt.key}.baseRate`)} />
            </label>
          </div>
          {#if day.windows.length > 0}
            <div class="table-scroll windows">
              <table>
                <thead><tr><th>Window name</th><th>Start</th><th>End</th><th>Rate (per kWh)</th><th><span class="visually-hidden">Actions</span></th></tr></thead>
                <tbody>
                  {#each day.windows as w, wi (wi)}
                    {@const wp = `seasons[${si}].${dt.key}.windows[${wi}]`}
                    <tr>
                      <td data-label="Window name"><input data-win={`${prefix}-${wi}`} aria-label={`Season ${si + 1} ${dt.label} window ${wi + 1} name`} bind:value={w.name} class:warn={bad(`${wp}.name`)} aria-invalid={bad(`${wp}.name`)} /></td>
                      <td data-label="Start"><input class="time" inputmode="numeric" placeholder="16:00" aria-label={`Season ${si + 1} ${dt.label} window ${wi + 1} start`} bind:value={w.start} class:warn={bad(`${wp}.start`)} aria-invalid={bad(`${wp}.start`)} /></td>
                      <td data-label="End"><input class="time" inputmode="numeric" placeholder="21:00" aria-label={`Season ${si + 1} ${dt.label} window ${wi + 1} end`} bind:value={w.end} class:warn={bad(`${wp}.end`)} aria-invalid={bad(`${wp}.end`)} /></td>
                      <td data-label="Rate (per kWh)"><input type="number" min="0" step="0.001" aria-label={`Season ${si + 1} ${dt.label} window ${wi + 1} rate`} bind:value={w.rate} class:warn={bad(`${wp}.rate`)} aria-invalid={bad(`${wp}.rate`)} /></td>
                      <td data-label=""><button type="button" aria-label={`Remove window ${w.name}`} data-focus-key={`${prefix}-${wi}`} onclick={() => removeWindow(day, wi, prefix)}>Remove</button></td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            </div>
          {/if}
          <button type="button" data-focus-key={`${prefix}-add`} onclick={() => addWindow(day, prefix)} aria-disabled={day.windows.length >= 8}>Add window to {dt.label.toLowerCase()}</button>
        </div>
      {/each}

      <button type="button" data-focus-key={`remove-season-${si}`} onclick={() => removeSeason(si)}>Remove season {si + 1}</button>
    </fieldset>
  {/each}
  <button type="button" data-focus-key="add-season" onclick={addSeason} aria-disabled={schedule.seasons.length >= 6}>Add season</button>
</div>

<style>
  .schedule { display: flex; flex-direction: column; gap: 0.9rem; margin-top: 0.8rem; }
  .season { min-width: 0; border: 1px solid var(--border); border-radius: var(--radius); padding: 0.8rem; display: flex; flex-direction: column; gap: 0.8rem; }
  .season > legend { font-weight: 600; padding: 0 0.3rem; }
  .months { min-width: 0; border: 0; padding: 0; margin: 0; display: flex; flex-wrap: wrap; gap: 0.4rem 0.9rem; }
  .months > legend { padding: 0; font-size: var(--text-sm); color: var(--muted); }
  .month { display: inline-flex; align-items: center; gap: 0.3rem; min-height: 24px; }
  .day { display: flex; flex-direction: column; align-items: flex-start; gap: 0.5rem; }
  .day h4 { margin: 0; }
  .problems { margin: 0; padding-left: 1.2rem; }
  .time { width: 6rem; }
  .months.danger-border { border-left: 3px solid var(--danger); padding-left: 0.6rem; }
  td, th { white-space: nowrap; }
  /* Phones: a window's fields stack inside the card instead of scrolling sideways out of sight. */
  @media (max-width: 600px) {
    .windows :global(table), .windows :global(tbody), .windows :global(tr), .windows :global(td) { display: block; }
    .windows :global(thead) { display: none; }
    .windows :global(tr) { border: 1px solid var(--border); border-radius: var(--radius); padding: 0.5rem; margin-bottom: 0.6rem; }
    .windows :global(td) { display: flex; align-items: center; justify-content: space-between; gap: 0.5rem; white-space: normal; border: 0; padding: 0.2rem 0; }
    .windows :global(td[data-label])::before { content: attr(data-label); color: var(--muted); font-size: var(--text-sm); }
    .windows :global(td input) { max-width: 60%; }
  }
  .example .hint { margin-top: 0.5rem; }
</style>
