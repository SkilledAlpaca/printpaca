<script lang="ts">
  import type { EffectiveConfig } from "../lib/engine/index";
  import { quote, type PartForm } from "../lib/state.svelte";
  import Field from "./ui/Field.svelte";
  import { announce, focusAfterUpdate } from "../lib/a11y.svelte";
  import { isValidTimezone, parseWallStart } from "../lib/engine/schedule";
  import { useNextPrintStart } from "../lib/printStart";

  // Schedule energy mode only (QuoteTab renders this behind cfg.energy.mode === "schedule"): the order's
  // print start and an optional start for the active part. Both are wall-clock in the instance time zone and
  // are saved with the quote so a reprint prices the same.
  let { cfg, part, warn = "" }: { cfg: EffectiveConfig; part: PartForm; warn?: string } = $props();

  const uid = $props.id();
  const orderId = `${uid}-order-start`;
  const partId = `${uid}-part-start`;
  const zone = $derived(cfg.timezone || "UTC");
  const clock = $derived(cfg.energy.schedule.defaultStartTime);

  let orderTouched = $state(false);
  const orderProblem = $derived.by(() => {
    const v = quote.order.printStart;
    if (v === "") return "Enter the print start.";
    if (!isValidTimezone(zone) || parseWallStart(v, zone) === null) return "Enter a valid date and time.";
    return "";
  });

  // The field is seeded with the next default start, so a click usually changes nothing visible: the message
  // under the button says what happened. It disappears once the start is edited.
  let orderInput = $state<HTMLInputElement>();
  let nextNote = $state<{ text: string; value: string } | null>(null);
  const shownNote = $derived(nextNote && nextNote.value === quote.order.printStart ? nextNote.text : "");

  function useNext() {
    const r = useNextPrintStart(cfg, quote.order.printStart, part.printStart !== "");
    quote.order.printStart = r.value;
    // A datetime-local input that is half typed reports "" while the state may already equal the value, so write
    // the DOM too; otherwise the click would leave the half-typed text on screen.
    if (orderInput && orderInput.value !== r.value) orderInput.value = r.value;
    orderTouched = false;
    nextNote = { text: r.message, value: r.value };
    announce(r.message);
  }

  function useOrderStart() {
    part.printStart = "";
    announce("This part uses the order's print start.");
    void focusAfterUpdate(`#${CSS.escape(partId)}`);
  }
</script>

<Field id={orderId} label="Print start (order)" error={orderProblem !== "" && orderTouched ? orderProblem : undefined} describedby={`${orderId}-help`}>
  {#snippet children(a)}
    <input
      id={a.id}
      type="datetime-local"
      bind:this={orderInput}
      bind:value={quote.order.printStart}
      class:warn={orderProblem !== "" && orderTouched}
      onblur={() => (orderTouched = true)}
      aria-invalid={orderProblem !== "" && orderTouched}
      aria-describedby={a.describedby}
    />
    <small id="{orderId}-help" class="hint-text">
      Times are in your shop time zone ({zone}). Electricity is priced by the hours the print runs, so the start changes the cost.
      <button type="button" class="quiet" onclick={useNext}>Use the next {clock}</button>
    </small>
    {#if shownNote !== ""}<small class="hint-text next-note" data-testid="print-start-note">{shownNote}</small>{/if}
    {#if warn !== ""}<small class="warn-text">{warn}</small>{/if}
  {/snippet}
</Field>

<Field id={partId} label="Print start (this part)" describedby={`${partId}-help`}>
  {#snippet children(a)}
    <input id={a.id} type="datetime-local" bind:value={part.printStart} aria-describedby={a.describedby} />
    <small id="{partId}-help" class="hint-text">
      Optional. Leave it blank to use the order's start.
      {#if part.printStart !== ""}<button type="button" class="quiet" onclick={useOrderStart}>Use the order's start</button>{/if}
    </small>
  {/snippet}
</Field>

<style>
  .hint-text { color: var(--muted); font-size: var(--type-secondary); }
  .next-note { display: block; }
</style>
