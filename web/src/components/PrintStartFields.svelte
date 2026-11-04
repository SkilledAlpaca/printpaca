<script lang="ts">
  import type { EffectiveConfig } from "../lib/engine/index";
  import { quote, defaultPrintStart, type PartForm } from "../lib/state.svelte";
  import { announce, focusAfterUpdate } from "../lib/a11y.svelte";
  import { isValidTimezone, parseWallStart } from "../lib/engine/schedule";

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

  function useNext() {
    quote.order.printStart = defaultPrintStart(cfg);
    announce(`Print start set to ${quote.order.printStart.replace("T", " ")}.`);
  }

  function useOrderStart() {
    part.printStart = "";
    announce("This part uses the order's print start.");
    void focusAfterUpdate(`#${CSS.escape(partId)}`);
  }
</script>

<div class="field">
  <label for={orderId}>Print start (order)</label>
  <input
    id={orderId}
    type="datetime-local"
    bind:value={quote.order.printStart}
    class:warn={orderProblem !== "" && orderTouched}
    onblur={() => (orderTouched = true)}
    aria-invalid={orderProblem !== "" && orderTouched}
    aria-describedby={orderProblem !== "" && orderTouched ? `${orderId}-help ${orderId}-warn` : `${orderId}-help`}
  />
  <small id="{orderId}-help" class="field-hint">
    Times are in your shop time zone ({zone}). Electricity is priced by the hours the print runs, so the start changes the cost.
    <button type="button" class="quiet" onclick={useNext}>Use the next {clock}</button>
  </small>
  {#if orderProblem !== "" && orderTouched}<small id="{orderId}-warn" class="warn-text">{orderProblem}</small>{/if}
  {#if warn !== ""}<small class="warn-text">{warn}</small>{/if}
</div>

<div class="field">
  <label for={partId}>Print start (this part)</label>
  <input id={partId} type="datetime-local" bind:value={part.printStart} aria-describedby="{partId}-help" />
  <small id="{partId}-help" class="field-hint">
    Optional. Leave it blank to use the order's start.
    {#if part.printStart !== ""}<button type="button" class="quiet" onclick={useOrderStart}>Use the order's start</button>{/if}
  </small>
</div>
