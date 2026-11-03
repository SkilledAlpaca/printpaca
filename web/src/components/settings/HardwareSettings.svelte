<script lang="ts">
  import { nextKey, type ServerConfig } from "../../lib/settings";
  import { settingsWarnings } from "../../lib/settingsRules";
  import { warnFor } from "../../lib/format";
  import { rowProblems, warnId } from "../../lib/rowIssues";
  import { focusAfterRowRemoval } from "../../lib/a11yFocus";

  let { form, hasError }: { form: ServerConfig; hasError: (section: string) => boolean } = $props();

  const warnings = $derived(settingsWarnings(form));

  // One .warn-text line per problem across the table, like MaterialsSettings' rowIssues: a row with a
  // duplicate name and a negative unit cost gets two lines.
  const rowIssues = $derived(rowProblems("hardware", form.hardware.length, ["name", "unitCost"], warnings));

  // Hardware: a wholesale array with no "at least one" floor (most shops attach none), so Remove is never
  // disabled.

  function addHardware() {
    const name = nextKey(form.hardware.map((h) => h.name), "New hardware");
    form.hardware.push({ name, unitCost: 0, lostOnFailure: false });
  }
  function removeHardware(index: number) {
    form.hardware = form.hardware.filter((_, i) => i !== index);
    void focusAfterRowRemoval("remove-hardware", index, form.hardware.length, "add-hardware");
  }
</script>

<div id="section-hardware" class="card roomy" class:danger-border={hasError("hardware")}>
  <h2>Hardware</h2>
  <p class="hint">Add-on parts (such as magnets, earring findings, and inserts) you can attach to a quote as an
    extra per-unit cost. This list replaces the built-in one. An empty list is fine; most shops attach no
    hardware.</p>
  {#if form.hardware.length > 0}
    <div class="table-scroll">
      <table>
        <thead>
          <tr><th>Name</th><th>Unit cost</th><th>Lost on failed prints</th><th><span class="visually-hidden">Actions</span></th></tr>
        </thead>
        <tbody>
          {#each form.hardware as h, i (i)}
            {@const nameWarn = warnFor(warnings, `hardware[${i}].name`)}
            {@const costWarn = warnFor(warnings, `hardware[${i}].unitCost`)}
            <tr>
              <td>
                <input
                  aria-label={`Hardware ${i + 1} name`}
                  bind:value={h.name}
                  class:warn={nameWarn !== ""}
                  aria-invalid={nameWarn !== ""}
                  aria-describedby={nameWarn !== "" ? warnId(`hardware[${i}].name`) : undefined}
                  title={nameWarn || undefined}
                />
              </td>
              <td>
                <input
                  type="number"
                  step="0.01"
                  aria-label={`Hardware ${i + 1} unit cost`}
                  bind:value={h.unitCost}
                  class:warn={costWarn !== ""}
                  aria-invalid={costWarn !== ""}
                  aria-describedby={costWarn !== "" ? warnId(`hardware[${i}].unitCost`) : undefined}
                  title={costWarn || undefined}
                />
              </td>
              <td>
                <input
                  type="checkbox"
                  aria-label={`Hardware ${i + 1} lost on failed prints`}
                  checked={h.lostOnFailure}
                  onchange={(e) => (h.lostOnFailure = e.currentTarget.checked)}
                />
              </td>
              <td>
                <button
                  type="button"
                  aria-label={`Remove hardware ${h.name}`}
                  data-focus-key={`remove-hardware-${i}`}
                  onclick={() => removeHardware(i)}
                >Remove</button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
    {#each rowIssues as issue (issue.field)}
      <small id={warnId(issue.field)} class="warn-text">Row {issue.row}: {issue.message}</small>
    {/each}
  {/if}
  <button type="button" data-focus-key="add-hardware" onclick={addHardware}>Add hardware</button>
</div>

<style>
  /* table/th baseline and .table-scroll come from theme.css; these grids only need cells on one line. */
  td, th { white-space: nowrap; }
</style>
