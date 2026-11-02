<script lang="ts">
  import { nextKey, type ServerConfig } from "../../lib/settings";
  import { settingsWarnings } from "../../lib/settingsRules";
  import { warnFor } from "../../lib/format";
  import { rowProblems, warnId } from "../../lib/rowIssues";

  let { form, hasError }: { form: ServerConfig; hasError: (section: string) => boolean } = $props();

  const warnings = $derived(settingsWarnings(form));

  // One .warn-text line per problem across the table, as in MaterialsSettings.svelte.
  const rowIssues = $derived(rowProblems("printers", form.printers.length, ["id", "filamentDiameterMm"], warnings));

  // Printers: a wholesale array, at least one row required.

  function addPrinter() {
    const id = nextKey(form.printers.map((p) => p.id), "new-printer");
    form.printers.push({ id, name: "New printer", watts: 250, purchaseCost: 400, lifespanHours: 4000, filamentDiameterMm: 1.75 });
  }
  function removePrinter(index: number) {
    form.printers = form.printers.filter((_, i) => i !== index);
  }
</script>

<div id="section-printers" class="card roomy" class:danger-border={hasError("printers")}>
  <h2>Printer list</h2>
  <p class="hint">This list replaces the built-in one entirely. At least one printer is required.</p>
  <div class="table-scroll">
    <table>
      <thead>
        <tr><th>ID</th><th>Name</th><th>Power (W)</th><th>Purchase cost</th><th>Lifespan (h)</th><th>Filament ⌀ (mm)</th><th></th></tr>
      </thead>
      <tbody>
        {#each form.printers as p, i (i)}
          {@const idWarn = warnFor(warnings, `printers[${i}].id`)}
          {@const diameterWarn = warnFor(warnings, `printers[${i}].filamentDiameterMm`)}
          <tr>
            <td>
              <input
                aria-label={`Printer ${i + 1} id`}
                bind:value={p.id}
                class:warn={idWarn !== ""}
                aria-invalid={idWarn !== ""}
                aria-describedby={idWarn !== "" ? warnId(`printers[${i}].id`) : undefined}
                title={idWarn || undefined}
              />
            </td>
            <td><input aria-label={`Printer ${i + 1} name`} bind:value={p.name} /></td>
            <td><input type="number" step="1" aria-label={`Printer ${i + 1} watts`} bind:value={p.watts} /></td>
            <td><input type="number" step="0.01" aria-label={`Printer ${i + 1} purchase cost`} bind:value={p.purchaseCost} /></td>
            <td><input type="number" step="1" aria-label={`Printer ${i + 1} lifespan hours`} bind:value={p.lifespanHours} /></td>
            <td>
              <input
                type="number"
                step="0.01"
                aria-label={`Printer ${i + 1} filament diameter`}
                bind:value={p.filamentDiameterMm}
                class:warn={diameterWarn !== ""}
                aria-invalid={diameterWarn !== ""}
                aria-describedby={diameterWarn !== "" ? warnId(`printers[${i}].filamentDiameterMm`) : undefined}
                title={diameterWarn || undefined}
              />
            </td>
            <td>
              <button
                type="button"
                aria-label={`Remove printer ${p.name || p.id}`}
                disabled={form.printers.length <= 1}
                onclick={() => removePrinter(i)}
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
  <button type="button" onclick={addPrinter}>Add printer</button>
</div>

<style>
  /* table/th baseline and .table-scroll come from theme.css; these grids only need cells on one line. */
  td, th { white-space: nowrap; }
</style>
