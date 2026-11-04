<script lang="ts">
  import { nextKey, type ServerConfig } from "../../lib/settings";
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
    void focusAfterRowRemoved(form.hardware.map((_, j) => `hardware-${j}`), index, "add-hardware");
  }
</script>

<Card id="section-hardware" class={`card${hasError("hardware") ? " danger-border" : ""}`}>
<div class="stack">
<h2>Hardware</h2>
  <p class="hint">Add-on parts (such as magnets, earring findings, and inserts) you can attach to a quote as an
    extra per-unit cost. This list replaces the built-in one. An empty list is fine; most shops attach no
    hardware.</p>
  {#if form.hardware.length > 0}
    <div class="rows">
      {#each form.hardware as h, i (i)}
        {@const nameWarn = warnFor(warnings, `hardware[${i}].name`)}
        {@const costWarn = warnFor(warnings, `hardware[${i}].unitCost`)}
        <RowEditor
          tone="inset"
          label={`Hardware ${i + 1}: ${h.name}`}
          rowKey={`hardware-${i}`}
          onremove={() => removeHardware(i)}
          removeLabel={`Remove hardware ${h.name}`}
        >
          {#snippet fields()}
            <FormGrid align="controls">
              <Field id={`hardware-${i}-name`} label="Name" size="m">
                {#snippet children(f)}
                  <input
                    id={f.id}
                    aria-label={`Hardware ${i + 1} name`}
                    bind:value={h.name}
                    class:warn={nameWarn !== ""}
                    aria-invalid={nameWarn !== ""}
                    aria-describedby={nameWarn !== "" ? warnId(`hardware[${i}].name`) : undefined}
                  />
                {/snippet}
              </Field>
              <Field id={`hardware-${i}-cost`} label="Unit cost" size="s">
                {#snippet children(f)}
                  <input
                    id={f.id}
                    type="number"
                    step="0.01"
                    aria-label={`Hardware ${i + 1} unit cost`}
                    bind:value={h.unitCost}
                    class:warn={costWarn !== ""}
                    aria-invalid={costWarn !== ""}
                    aria-describedby={costWarn !== "" ? warnId(`hardware[${i}].unitCost`) : undefined}
                  />
                {/snippet}
              </Field>
              <label class="toggle cell-end">
                <input
                  type="checkbox"
                  aria-label={`Hardware ${i + 1} lost on failed prints`}
                  checked={h.lostOnFailure}
                  onchange={(e) => (h.lostOnFailure = e.currentTarget.checked)}
                />
                <span>Lost on failed prints</span>
              </label>
            </FormGrid>
          {/snippet}
        </RowEditor>
      {/each}
    </div>
    {#each rowIssues as issue (issue.field)}
      <small id={warnId(issue.field)} class="warn-text">Row {issue.row}: {issue.message}</small>
    {/each}
  {/if}
  <div class="add-line">
    <button type="button" data-focus-key="add-hardware" onclick={addHardware}>Add hardware</button>
  </div>
</div>
</Card>

<style>
  .stack { display: flex; flex-direction: column; gap: var(--gap); min-width: 0; }
  .stack > :global(h2), .stack > :global(h3) { margin: 0; }
  .rows { display: flex; flex-direction: column; gap: var(--gap); }
  .add-line { display: flex; }
  .cell-end { align-self: end; }
</style>
