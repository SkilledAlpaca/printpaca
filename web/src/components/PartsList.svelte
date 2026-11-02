<script lang="ts">
  import { MAX_ORDER_PARTS, MAX_PART_LABEL_CHARS, type EffectiveConfig, type OrderBreakdown } from "../lib/engine/index";
  import { quote, addPart, duplicatePart, removePart, setActivePart, partLabel, type PartForm } from "../lib/state.svelte";
  import { money } from "../lib/format";

  let { cfg, order }: { cfg: EffectiveConfig; order: OrderBreakdown } = $props();

  const multi = $derived(quote.parts.length > 1);
  const full = $derived(quote.parts.length >= MAX_ORDER_PARTS);

  function add() {
    const part = addPart(cfg);
    setActivePart(part.id);
  }
  function duplicate(id: string) {
    const copy = duplicatePart(id);
    if (copy) setActivePart(copy.id);
  }

  // Line total: the part's item revenue once the order is priced, else its production cost. The server answer
  // can lag the editor by a debounce, so a part without one shows "-".
  function lineTotal(i: number): string {
    const answered = order.parts.length === quote.parts.length ? order.parts[i] : undefined;
    if (!answered) return "-";
    if (order.pricing) return order.pricing.valid ? money(answered.itemRevenue, cfg) : "-";
    return answered.breakdown.valid ? money(answered.breakdown.batchTotal, cfg) : "-";
  }
  function qty(part: PartForm): string {
    return part.quantity === null ? "?" : String(part.quantity);
  }
  // Removing a part drops its weight, time, and hardware with no undo, so it takes a second click.
  let confirmingRemove = $state<string | null>(null);
  function confirmRemove(id: string) {
    confirmingRemove = null;
    removePart(id);
  }
  const labelInputId = $props.id();
</script>

<div class="card parts" role="group" aria-label="Parts">
  {#if multi}
    <div class="head-row">
      <h2>Parts</h2>
      <button type="button" onclick={add} disabled={full}>Add part</button>
    </div>
    <ul class="plain-list part-rows">
      {#each quote.parts as part, i (part.id)}
        <li class:active-row={part.id === quote.activePartId}>
          <button
            type="button"
            class="quiet linkish part-select"
            aria-pressed={part.id === quote.activePartId}
            aria-label={`Edit ${partLabel(part, i)}`}
            onclick={() => setActivePart(part.id)}
          >
            <span class="part-name">{partLabel(part, i)}</span>
            <span class="part-meta">× {qty(part)}</span>
          </button>
          <span class="part-total">{lineTotal(i)}</span>
          <button type="button" class="quiet" aria-label={`Duplicate ${partLabel(part, i)}`} onclick={() => duplicate(part.id)} disabled={full}>Duplicate</button>
          {#if confirmingRemove === part.id}
            <button type="button" class="quiet danger" aria-label={`Confirm remove ${partLabel(part, i)}`} onclick={() => confirmRemove(part.id)}>Remove?</button>
            <button type="button" class="quiet" aria-label={`Keep ${partLabel(part, i)}`} onclick={() => (confirmingRemove = null)}>Cancel</button>
          {:else}
            <button type="button" class="quiet" aria-label={`Remove ${partLabel(part, i)}`} onclick={() => (confirmingRemove = part.id)}>Remove</button>
          {/if}
        </li>
      {/each}
    </ul>
    <label class="field part-name-field" for={`${labelInputId}-name`}>
      <span>Part name</span>
      <input
        id={`${labelInputId}-name`}
        maxlength={MAX_PART_LABEL_CHARS}
        value={quote.parts.find((p) => p.id === quote.activePartId)?.label ?? ""}
        oninput={(e) => {
          const p = quote.parts.find((x) => x.id === quote.activePartId);
          if (p) p.label = e.currentTarget.value;
        }}
      />
    </label>
  {:else}
    <button type="button" class="add-part" onclick={add}>Add part</button>
    <span class="hint">Quoting several printed parts together? Each gets its own weight, time, and quantity.</span>
  {/if}
</div>

<style>
  .parts { display: flex; flex-direction: column; gap: 0.5rem; }
  .head-row { display: flex; align-items: center; justify-content: space-between; gap: 0.5rem; }
  .head-row h2 { margin: 0; }
  .add-part { align-self: flex-start; }
  /* Wraps so the total and the Duplicate/Remove buttons drop to a second line when a phone (or a wide system
   * font) leaves no room, instead of pushing the row past the viewport. */
  .part-rows li { display: flex; flex-wrap: wrap; align-items: center; gap: 0.25rem 0.5rem; padding: 0.2rem 0; }
  .part-rows li.active-row { font-weight: 600; }
  .part-select { flex: 1 1 8rem; min-width: 0; display: flex; gap: 0.6rem; text-align: left; }
  .part-name { min-width: 0; overflow-wrap: anywhere; }
  .part-meta { color: var(--muted); }
  .part-total { font-variant-numeric: tabular-nums; min-width: 5rem; text-align: right; }
  .part-name-field { max-width: 24rem; }
</style>
