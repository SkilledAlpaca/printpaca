<script lang="ts">
  import { FALLBACK_DENSITY } from "../../lib/filamentDefaults";
  import { nextKey, type ServerConfig } from "../../lib/settings";
  import { settingsWarnings } from "../../lib/settingsRules";
  import { warnFor } from "../../lib/format";
  import { rowProblems, warnId } from "../../lib/rowIssues";
  import { focusAfterRowRemoval } from "../a11yFocus";

  let { form, hasError }: { form: ServerConfig; hasError: (section: string) => boolean } = $props();

  const warnings = $derived(settingsWarnings(form));

  // One .warn-text line per problem across the table (e.g. "Row 2: density must be greater than 0."), not one
  // per row. Each line's id (warnId) is what the offending cell's aria-describedby points at.
  const rowIssues = $derived(rowProblems("materials", form.materials.length, ["id", "density", "wastePct"], warnings));

  // Materials: a wholesale array, at least one row required.

  function addMaterial() {
    const id = nextKey(form.materials.map((m) => m.id), "new-material");
    form.materials.push({ id, name: "New material", density: FALLBACK_DENSITY, pricePerKg: 20, wastePct: 0, public: false });
  }
  function removeMaterial(index: number) {
    form.materials = form.materials.filter((_, i) => i !== index);
    void focusAfterRowRemoval("remove-material", index, form.materials.length, "add-material");
  }
</script>

<div id="section-materials" class="card roomy" class:danger-border={hasError("materials")}>
  <h2>Materials</h2>
  <p class="hint">This list replaces the built-in one entirely. At least one material is required. Materials
    checked "Public" are offered on the public estimate page.</p>
  <div class="table-scroll">
    <table>
      <thead>
        <tr><th>ID</th><th>Name</th><th>Density (g/cm³)</th><th>Price per kg</th><th>Waste (%)</th><th>Public</th><th><span class="visually-hidden">Actions</span></th></tr>
      </thead>
      <tbody>
        {#each form.materials as m, i (i)}
          {@const idWarn = warnFor(warnings, `materials[${i}].id`)}
          {@const densityWarn = warnFor(warnings, `materials[${i}].density`)}
          {@const wasteWarn = warnFor(warnings, `materials[${i}].wastePct`)}
          <tr>
            <td>
              <input
                aria-label={`Material ${i + 1} id`}
                bind:value={m.id}
                class:warn={idWarn !== ""}
                aria-invalid={idWarn !== ""}
                aria-describedby={idWarn !== "" ? warnId(`materials[${i}].id`) : undefined}
                title={idWarn || undefined}
              />
            </td>
            <td><input aria-label={`Material ${i + 1} name`} bind:value={m.name} /></td>
            <td>
              <input
                type="number"
                step="0.01"
                aria-label={`Material ${i + 1} density`}
                bind:value={m.density}
                class:warn={densityWarn !== ""}
                aria-invalid={densityWarn !== ""}
                aria-describedby={densityWarn !== "" ? warnId(`materials[${i}].density`) : undefined}
                title={densityWarn || undefined}
              />
            </td>
            <td><input type="number" step="0.01" aria-label={`Material ${i + 1} price per kg`} bind:value={m.pricePerKg} /></td>
            <td>
              <input
                type="number"
                step="0.1"
                aria-label={`Material ${i + 1} waste percent`}
                bind:value={m.wastePct}
                class:warn={wasteWarn !== ""}
                aria-invalid={wasteWarn !== ""}
                aria-describedby={wasteWarn !== "" ? warnId(`materials[${i}].wastePct`) : undefined}
                title={wasteWarn || undefined}
              />
            </td>
            <td>
              <input
                type="checkbox"
                aria-label={`Material ${i + 1} public`}
                checked={m.public}
                onchange={(e) => (m.public = e.currentTarget.checked)}
              />
            </td>
            <td>
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
  <button type="button" data-focus-key="add-material" onclick={addMaterial}>Add material</button>
</div>

<style>
  /* table/th baseline and .table-scroll come from theme.css; these grids only need cells on one line. */
  td, th { white-space: nowrap; }
</style>
