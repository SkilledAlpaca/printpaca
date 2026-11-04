<script lang="ts">
  import { FALLBACK_DENSITY } from "../../lib/filamentDefaults";
  import { nextKey, type ServerConfig } from "../../lib/settings";
  import { settingsWarnings } from "../../lib/settingsRules";
  import { warnFor } from "../../lib/format";
  import { rowProblems, warnId } from "../../lib/rowIssues";
  import { focusAfterRowRemoval, keySelector } from "../../lib/a11yFocus";
  import { focusAfterUpdate } from "../../lib/a11y.svelte";
  import Card from "../ui/Card.svelte";

  let { form, hasError }: { form: ServerConfig; hasError: (section: string) => boolean } = $props();

  const warnings = $derived(settingsWarnings(form));

  // One .warn-text line per problem across the table (e.g. "Row 2: density must be greater than 0."), not one
  // per row. Each line's id (warnId) is what the offending cell's aria-describedby points at.
  const rowIssues = $derived(rowProblems("materials", form.materials.length, ["id", "density", "wastePct"], warnings));

  // Materials: a wholesale array, at least one row required.

  // Rows are read-only until Edit is pressed, so a stray keystroke can't change what every quote costs.
  // `editing` holds row indexes; a new row starts in edit mode, and a row with a problem always shows its
  // inputs so the message has something to point at. ID and density sit behind the advanced toggle: they are
  // rarely changed, and a wrong density skews every weight-based price.
  let editing = $state<number[]>([]);
  let showAdvanced = $state(false);
  // A problem in ID or density forces those columns on, or the message would name a field you can't see.
  const advancedVisible = $derived(showAdvanced || rowIssues.some((issue) => /\.(id|density)$/.test(issue.field)));
  const isEditing = (i: number) => editing.includes(i) || rowHasProblem(i);
  function rowHasProblem(i: number): boolean {
    return rowIssues.some((issue) => issue.row === i + 1);
  }
  function addMaterial() {
    const id = nextKey(form.materials.map((m) => m.id), "new-material");
    form.materials.push({ id, name: "New material", density: FALLBACK_DENSITY, pricePerKg: 20, wastePct: 0, public: false });
    editing = [...editing, form.materials.length - 1];
    void focusAfterUpdate(keySelector(`material-name-${form.materials.length - 1}`));
  }
  function removeMaterial(index: number) {
    form.materials = form.materials.filter((_, i) => i !== index);
    editing = editing.filter((i) => i !== index).map((i) => (i > index ? i - 1 : i));
    void focusAfterRowRemoval("remove-material", index, form.materials.length, "add-material");
  }
  function startEdit(i: number) {
    editing = [...editing, i];
    void focusAfterUpdate(keySelector(`material-name-${i}`));
  }
  function doneEdit(i: number) {
    editing = editing.filter((x) => x !== i);
    void focusAfterUpdate(keySelector(`edit-material-${i}`));
  }
  const num = (v: number | null | undefined, digits: number) => (typeof v === "number" && Number.isFinite(v) ? v.toFixed(digits) : "-");
</script>

<Card id="section-materials" tone={hasError("materials") ? "danger" : "default"} heading="Materials">
  <p class="hint">This list replaces the built-in one entirely. At least one material is required. Materials
    checked "Public" are offered on the public estimate page. Press Edit on a row to change it.</p>
  <label class="toggle">
    <input type="checkbox" bind:checked={showAdvanced} />
    <span>Show ID and density (advanced)</span>
  </label>
  <div class="table-scroll">
    <table>
      <thead>
        <tr>
          {#if advancedVisible}<th>ID</th>{/if}<th>Name</th>{#if advancedVisible}<th>Density (g/cm³)</th>{/if}<th>Price per kg</th><th>Waste (%)</th><th>Public</th><th><span class="visually-hidden">Actions</span></th>
        </tr>
      </thead>
      <tbody>
        {#each form.materials as m, i (i)}
          {@const idWarn = warnFor(warnings, `materials[${i}].id`)}
          {@const densityWarn = warnFor(warnings, `materials[${i}].density`)}
          {@const wasteWarn = warnFor(warnings, `materials[${i}].wastePct`)}
          {@const edit = isEditing(i)}
          <tr>
            {#if advancedVisible}
              <td>
                {#if edit}
                  <input
                    aria-label={`Material ${i + 1} id`}
                    bind:value={m.id}
                    class:warn={idWarn !== ""}
                    aria-invalid={idWarn !== ""}
                    aria-describedby={idWarn !== "" ? warnId(`materials[${i}].id`) : undefined}
                  />
                {:else}{m.id}{/if}
              </td>
            {/if}
            <td>
              {#if edit}
                <input aria-label={`Material ${i + 1} name`} bind:value={m.name} data-focus-key={`material-name-${i}`} />
              {:else}{m.name}{/if}
            </td>
            {#if advancedVisible}
              <td>
                {#if edit}
                  <input
                    type="number"
                    step="0.01"
                    aria-label={`Material ${i + 1} density`}
                    bind:value={m.density}
                    class:warn={densityWarn !== ""}
                    aria-invalid={densityWarn !== ""}
                    aria-describedby={densityWarn !== "" ? warnId(`materials[${i}].density`) : undefined}
                  />
                {:else}{num(m.density, 2)}{/if}
              </td>
            {/if}
            <td>
              {#if edit}
                <input type="number" step="0.01" aria-label={`Material ${i + 1} price per kg`} bind:value={m.pricePerKg} />
              {:else}{num(m.pricePerKg, 2)}{/if}
            </td>
            <td>
              {#if edit}
                <input
                  type="number"
                  step="0.1"
                  aria-label={`Material ${i + 1} waste percent`}
                  bind:value={m.wastePct}
                  class:warn={wasteWarn !== ""}
                  aria-invalid={wasteWarn !== ""}
                  aria-describedby={wasteWarn !== "" ? warnId(`materials[${i}].wastePct`) : undefined}
                />
              {:else}{num(m.wastePct, 1)}{/if}
            </td>
            <td>
              {#if edit}
                <input
                  type="checkbox"
                  aria-label={`Material ${i + 1} public`}
                  checked={m.public}
                  onchange={(e) => (m.public = e.currentTarget.checked)}
                />
              {:else}{m.public ? "Yes" : "No"}{/if}
            </td>
            <td class="actions">
              {#if edit}
                {#if !rowHasProblem(i)}
                  <button type="button" aria-label={`Done editing ${m.name || m.id}`} onclick={() => doneEdit(i)}>Done</button>
                {/if}
              {:else}
                <button type="button" aria-label={`Edit ${m.name || m.id}`} data-focus-key={`edit-material-${i}`} onclick={() => startEdit(i)}>Edit</button>
              {/if}
              <button
                type="button"
                aria-label={`Remove material ${m.name || m.id}`}
                data-focus-key={`remove-material-${i}`}
                disabled={form.materials.length <= 1}
                onclick={() => removeMaterial(i)}
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
  <div class="add-row">
    <button type="button" data-focus-key="add-material" onclick={addMaterial}>Add material</button>
  </div>
</Card>

<style>
  /* table/th baseline and .table-scroll come from theme.css; these grids only need cells on one line. */
  td, th { white-space: nowrap; }
  .actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }
  .add-row { margin-block-start: var(--space-3); }
</style>
