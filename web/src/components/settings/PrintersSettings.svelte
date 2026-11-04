<script lang="ts">
  import { newPrinter, type ServerConfig } from "../../lib/settings";
  import { settingsWarnings } from "../../lib/settingsRules";
  import { warnFor } from "../../lib/format";
  import { rowProblems, warnId } from "../../lib/rowIssues";
  import Card from "../ui/Card.svelte";
  import Field from "../ui/Field.svelte";
  import FormGrid from "../ui/FormGrid.svelte";
  import RowEditor from "../ui/RowEditor.svelte";
  import { focusAfterRowRemoved } from "../ui/rowEditor";

  let { form, hasError }: { form: ServerConfig; hasError: (section: string) => boolean } = $props();

  const warnings = $derived(settingsWarnings(form));

  // One .warn-text line per problem across the table, as in MaterialsSettings.svelte.
  const rowIssues = $derived(rowProblems("printers", form.printers.length, ["id", "filamentDiameterMm"], warnings));

  // Printers: a wholesale array, at least one row required.

  function addPrinter() {
    const printer = newPrinter(form.printers.map((p) => p.id), "New printer");
    form.printers.push(printer);
  }
  function removePrinter(index: number) {
    form.printers = form.printers.filter((_, i) => i !== index);
    void focusAfterRowRemoved(form.printers.map((_, j) => `printer-${j}`), index, "add-printer");
  }
</script>

<Card id="section-printers" tone={hasError("printers") ? "danger" : "default"}>
<div class="stack">
<h2>Printer list</h2>
  <p class="hint">This list replaces the built-in one entirely. At least one printer is required.</p>
  <div class="rows">
    {#each form.printers as p, i (i)}
      {@const idWarn = warnFor(warnings, `printers[${i}].id`)}
      {@const diameterWarn = warnFor(warnings, `printers[${i}].filamentDiameterMm`)}
      <RowEditor
        tone="inset"
        label={`Printer ${i + 1}: ${p.name || p.id}`}
        rowKey={`printer-${i}`}
        onremove={() => removePrinter(i)}
        removeLabel={`Remove printer ${p.name || p.id}`}
        removeDisabled={form.printers.length <= 1}
      >
        {#snippet fields()}
          <FormGrid align="controls">
            <Field id={`printer-${i}-id`} label="ID" size="s">
              {#snippet children(f)}
                <input
                  id={f.id}
                  aria-label={`Printer ${i + 1} id`}
                  bind:value={p.id}
                  class:warn={idWarn !== ""}
                  aria-invalid={idWarn !== ""}
                  aria-describedby={idWarn !== "" ? warnId(`printers[${i}].id`) : undefined}
                />
              {/snippet}
            </Field>
            <Field id={`printer-${i}-name`} label="Name" size="m">
              {#snippet children(f)}
                <input id={f.id} aria-label={`Printer ${i + 1} name`} bind:value={p.name} />
              {/snippet}
            </Field>
            <Field id={`printer-${i}-watts`} label="Power (W)" size="s">
              {#snippet children(f)}
                <input id={f.id} type="number" step="1" aria-label={`Printer ${i + 1} watts`} bind:value={p.watts} />
              {/snippet}
            </Field>
            <Field id={`printer-${i}-cost`} label="Purchase cost" size="s">
              {#snippet children(f)}
                <input id={f.id} type="number" step="0.01" aria-label={`Printer ${i + 1} purchase cost`} bind:value={p.purchaseCost} />
              {/snippet}
            </Field>
            <Field id={`printer-${i}-lifespan`} label="Lifespan (h)" size="s">
              {#snippet children(f)}
                <input id={f.id} type="number" step="1" aria-label={`Printer ${i + 1} lifespan hours`} bind:value={p.lifespanHours} />
              {/snippet}
            </Field>
            <Field id={`printer-${i}-diameter`} label="Filament ⌀ (mm)" size="s">
              {#snippet children(f)}
                <input
                  id={f.id}
                  type="number"
                  step="0.01"
                  aria-label={`Printer ${i + 1} filament diameter`}
                  bind:value={p.filamentDiameterMm}
                  class:warn={diameterWarn !== ""}
                  aria-invalid={diameterWarn !== ""}
                  aria-describedby={diameterWarn !== "" ? warnId(`printers[${i}].filamentDiameterMm`) : undefined}
                />
              {/snippet}
            </Field>
          </FormGrid>
        {/snippet}
      </RowEditor>
    {/each}
  </div>
  {#each rowIssues as issue (issue.field)}
    <small id={warnId(issue.field)} class="warn-text">Row {issue.row}: {issue.message}</small>
  {/each}
  <div class="add-line">
    <button type="button" data-focus-key="add-printer" onclick={addPrinter}>Add printer</button>
  </div>
</div>
</Card>

<style>
  .rows { display: flex; flex-direction: column; gap: var(--gap); }
  .add-line { display: flex; }
</style>
