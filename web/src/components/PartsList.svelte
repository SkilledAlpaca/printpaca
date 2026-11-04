<script lang="ts">
  import type { Snippet } from "svelte";
  import { MAX_ORDER_PARTS, MAX_PART_LABEL_CHARS, type EffectiveConfig, type OrderBreakdown } from "../lib/engine/index";
  import { quote, addPart, duplicatePart, removePart, setActivePart, partLabel, type PartForm } from "../lib/state.svelte";
  import { money } from "../lib/format";
  import { focusAfterUpdate } from "../lib/a11y.svelte";
  import { keySelector, neighborId, rowSelector } from "../lib/a11yFocus";
  import Tabs from "./ui/Tabs.svelte";
  import Field from "./ui/Field.svelte";
  import ConfirmInline from "./ui/ConfirmInline.svelte";

  // The parts of the order as tabs, with the editor for the active part (`children`) as the tab panel. A
  // one-part quote keeps the compact editor: just the "Add part" action and a hint, no tabs.
  let { cfg, order, children }: { cfg: EffectiveConfig; order: OrderBreakdown; children: Snippet } = $props();

  const multi = $derived(quote.parts.length > 1);
  const active = $derived(quote.parts.find((p) => p.id === quote.activePartId));
  const activeIndex = $derived(Math.max(0, quote.parts.findIndex((p) => p.id === quote.activePartId)));
  const full = $derived(quote.parts.length >= MAX_ORDER_PARTS);

  function add() {
    if (full) return;
    const part = addPart(cfg);
    setActivePart(part.id);
  }
  function duplicate(id: string) {
    if (full) return;
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
    const next = neighborId(quote.parts.map((p) => p.id), id);
    removePart(id);
    // The confirmed row is gone: focus the next part, else the previous; with one part left the list
    // collapses to the Add part button.
    void focusAfterUpdate(() => (next ? document.querySelector<HTMLElement>(rowSelector(next)) : null) ?? document.querySelector<HTMLElement>(keySelector("add-part")));
  }
  const labelInputId = $props.id();
</script>

{#snippet addButton(label: string)}
  <button type="button" data-focus-key="add-part" onclick={add} aria-disabled={full}>{label}</button>
{/snippet}

{#if multi}
  <div class="parts">
    <Tabs
      tabs={quote.parts.map((p, i) => ({ id: p.id, label: partLabel(p, i), meta: `× ${qty(p)}` }))}
      selected={quote.activePartId}
      onselect={setActivePart}
      label="Parts"
      idPrefix={labelInputId}
    >
      {#snippet after()}{@render addButton("+ Add part")}{/snippet}
      <div class="part-bar">
        <Field id={`${labelInputId}-name`} label="Part name" size="m">
          {#snippet children(a)}
            <input
              id={a.id}
              maxlength={MAX_PART_LABEL_CHARS}
              value={active?.label ?? ""}
              oninput={(e) => {
                if (active) active.label = e.currentTarget.value;
              }}
            />
          {/snippet}
        </Field>
        <span class="part-total" aria-label="Line total">{lineTotal(activeIndex)}</span>
        {#if active}
          <div class="part-actions">
            <button type="button" class="quiet" aria-label={`Duplicate ${partLabel(active, activeIndex)}`} onclick={() => duplicate(active.id)} aria-disabled={full}>Duplicate</button>
            {#if confirmingRemove === active.id}
              <ConfirmInline
                confirmLabel={`Remove ${partLabel(active, activeIndex)}?`}
                danger
                restore={keySelector(`remove-part-${active.id}`)}
                onconfirm={() => confirmRemove(active.id)}
                oncancel={() => (confirmingRemove = null)}
              >
                Its weight, time, and hardware are lost.
              </ConfirmInline>
            {:else}
              <button type="button" class="quiet" data-focus-key={`remove-part-${active.id}`} aria-label={`Remove ${partLabel(active, activeIndex)}`} onclick={() => (confirmingRemove = active.id)}>Remove</button>
            {/if}
          </div>
        {/if}
      </div>
      {@render children()}
    </Tabs>
  </div>
{:else}
  <div class="parts single">
    {@render addButton("Add part")}
    <span class="hint">Quoting several printed parts together? Each gets its own weight, time, and quantity.</span>
  </div>
  {@render children()}
{/if}

<style>
  .parts { display: flex; flex-direction: column; gap: var(--gap); min-width: 0; }
  .parts.single { flex-direction: row; flex-wrap: wrap; align-items: center; gap: var(--space-2) var(--space-4); }
  .part-bar { display: flex; flex-wrap: wrap; align-items: flex-end; gap: var(--space-2) var(--gap); }
  .part-bar :global(.ui-field) { flex: 1 1 var(--field-s); max-width: var(--field-m); }
  .part-total { font-variant-numeric: tabular-nums; font-weight: 600; min-height: var(--control-h); display: inline-flex; align-items: center; }
  .part-actions { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); }
</style>
