<script lang="ts">
  import { onDestroy, tick } from "svelte";
  import type { EffectiveConfig, EngineOptions, OrderBreakdown, OrderInputs } from "../lib/engine/index";
  import { quote, activePart, partLabel, toOrderInputs, engineOptions, applyPrinter, applyMaterial, printJob, quoteClient, quoteMeta, nameOrId } from "../lib/state.svelte";
  import { computeOrder, type ComputeOrderResult } from "../lib/quotesApi";
  import { EMPTY_ORDER, orderPrintBlockerHint, partBreakdown } from "../lib/orderView";
  import PartsList from "./PartsList.svelte";
  import PartsTable from "./PartsTable.svelte";
  import { createQuotePreview, type QuotePreviewState } from "../lib/quotePreview";
  import { fieldLabel, warnFor, moneyAmount, moneyPrecise } from "../lib/format";
    import LabeledNumber from "./LabeledNumber.svelte";
  import PresetChips from "./PresetChips.svelte";
  import BreakdownTable from "./BreakdownTable.svelte";
  import PricingPanel from "./PricingPanel.svelte";
  import GcodeDrop from "./GcodeDrop.svelte";
  import HistoryPanel from "./HistoryPanel.svelte";
  import SpoolPicker from "./SpoolPicker.svelte";
  import RecentPrints from "./RecentPrints.svelte";
  import { moonrakerEnabled } from "../lib/moonraker";
  import QuoteSummaryBar from "./QuoteSummaryBar.svelte";
  import CardHelp from "./CardHelp.svelte";
  import { helpState, setAllHelp } from "../lib/help.svelte";
  import { TILE_HELP, TILE_IDS, type TileId } from "../lib/guideContent";
  import { focusAfterRowRemoval } from "../lib/a11yFocus";

  let { cfg }: { cfg: EffectiveConfig } = $props();

  // Server-computed (POST /api/quote/compute): every input change is a debounced round trip, owned by
  // createQuotePreview (shared with the public quote page), which keeps the last resolved breakdown on screen
  // while a newer one is pending. `order` always holds the last resolved answer (EMPTY_ORDER before the
  // first), so the cards never see a hole; only canPrint reacts to previewState directly.
  let order = $state<OrderBreakdown>(EMPTY_ORDER);
  let previewState = $state<QuotePreviewState<{ order: OrderBreakdown }>>({ status: "idle" });

  function previewToState(
    result: ComputeOrderResult,
  ): { status: "resolved"; order: OrderBreakdown } | { status: "error"; errors: string[] } {
    if (result.ok) return { status: "resolved", order: result.order };
    return { status: "error", errors: ["Couldn't reach the server to price this quote."] };
  }

  const preview = createQuotePreview({
    fetcher: (input: { inputs: OrderInputs; options: EngineOptions }) => computeOrder(input.inputs, input.options),
    toState: previewToState,
    onChange: (s) => {
      previewState = s;
      if (s.status === "resolved") order = s.order;
    },
  });
  onDestroy(() => preview.dispose());

  $effect(() => {
    preview.update({ inputs: toOrderInputs(cfg), options: engineOptions(cfg) });
  });

  // HistoryPanel's save gate must tell "waiting on a recompute" from "the last answer is invalid". Passed as
  // props (pricingPending/settledOrder) so this component stays the only owner of the preview controller.
  const pricingPending = $derived(previewState.status === "pending");

  // Waits out any in-flight or queued recompute (preview.settled()) instead of acting on the stale
  // placeholder Ctrl+S can catch mid-debounce. Resolves null if the round trip produced no answer (dispose()
  // or an error).
  async function settledOrder(): Promise<OrderBreakdown | null> {
    const s = await preview.settled();
    return s.status === "resolved" ? s.order : null;
  }

  // The part the editor cards show and its breakdown from the order answer. The answer can lag the editor by
  // a debounce (a new part has no entry yet), so the breakdown falls back to an empty placeholder.
  const part = $derived(activePart());
  const partIndex = $derived(Math.max(0, quote.parts.findIndex((p) => p.id === part.id)));
  const breakdown = $derived(partBreakdown(order, partIndex));
  const labels = $derived(quote.parts.map((p, i) => partLabel(p, i)));
  const multi = $derived(quote.parts.length > 1);
  const sellingEnabled = $derived(quote.order.sellingEnabled);

  const w = $derived(breakdown.warnings);
  // Guide is a Tab like any other: when disabled, App.svelte's hashchange fallback rewrites the link off the
  // guide, so don't render it as if it went somewhere.
  const guideEnabled = $derived(!cfg.ui.disabledTabs.includes("guide"));
  const touched = () => (part.presetModified = true);

  // breakdown.valid doesn't cover pricing validity, but printing needs both or a marginPct of 150 prints a
  // confident $0.00 quote. printHint names the blocker (shared with BreakdownTable, see lib/quoteRow.ts) and
  // is the source of truth for canPrint.
  const printHint = $derived(orderPrintBlockerHint(order, labels, sellingEnabled));
  // Also requires a current resolved breakdown: printing stays blocked while a recompute is pending or
  // errored, so an edit is never printed at its pre-edit price.
  const canPrint = $derived(printHint === "" && previewState.status === "resolved" && order.parts.length === quote.parts.length);

  // One customer value: the print line reads quoteClient.name (the box the save row's picker binds to). With
  // a client linked, nameOrId falls back to the id so a blank name never prints a blank customer line.
  const effectiveCustomer = $derived(quoteClient.id ? nameOrId(quoteClient.name, quoteClient.id) : quoteClient.name.trim());

  // PrintView renders from the App root (see printJob in state.svelte.ts) so the print stylesheet can hide
  // the app shell. tick() flushes the printJob into the DOM before the dialog opens; App clears it on
  // "afterprint" (fires on print and cancel), since QuoteTab may be unmounted by then.
  async function requestPrint(mode: "client" | "internal") {
    if (!canPrint) return;
    printJob.current = {
      mode,
      meta: { customer: effectiveCustomer, project: quoteMeta.project, reference: quoteMeta.reference },
      order,
    };
    await tick();
    window.print();
  }

  // Accessible group labels for each tile's role="group" div, and the h2 text for the three tiles rendered
  // here (gcode, print, costs); the others' h2 and CardHelp live in their own components, so it is only their
  // aria-label.
  const TILE_LABELS: Record<TileId, string> = {
    gcode: "G-code",
    print: "Print",
    costs: "Costs",
    breakdown: "Cost breakdown",
    pricing: "Price and profit",
    history: "Saved quotes",
  };

  const allHelpOpen = $derived(TILE_IDS.every((id) => helpState.open[id] === true));
  function toggleAllHelp() {
    setAllHelp([...TILE_IDS], !allHelpOpen);
  }

  // First-run panel: shown once, before the visitor has entered anything or produced a valid quote. Dismissed
  // explicitly ("Got it") or when a quote first computes successfully.
  const ONBOARDED_KEY = "printpaca-onboarded";

  function readOnboarded(): boolean {
    try {
      return localStorage.getItem(ONBOARDED_KEY) !== null;
    } catch {
      return false;
    }
  }
  function writeOnboarded(): void {
    try {
      localStorage.setItem(ONBOARDED_KEY, "1");
    } catch {
      /* Safari private mode / quota exceeded: session-only dismissal still works. */
    }
  }

  let onboarded = $state(readOnboarded());

  const showFirstRun = $derived(
    quote.parts.length === 1 && part.weightG === null && part.timeHours === null && part.timeMinutes === null && !onboarded,
  );

  function dismissFirstRun() {
    onboarded = true;
    writeOnboarded();
  }

  $effect(() => {
    // Gated on a resolved valid breakdown: `order.valid` can read true from a stale pre-edit answer while a
    // fresh (possibly invalid) recompute is in flight or errored.
    if (previewState.status === "resolved" && order.valid && !onboarded) {
      onboarded = true;
      writeOnboarded();
    }
  });

  // Compound print-time field: each half flags its own warning, but they share one warning line (first
  // non-empty) and one touched flag (set on input into either half; on blur only when that half is non-null),
  // like LabeledNumber's rule applied jointly.
  const uid = $props.id();
  const timeHoursId = `${uid}-time-hours`;
  const hoursWarn = $derived(warnFor(w, "timeHours"));
  const minutesWarn = $derived(warnFor(w, "timeMinutes"));
  const timeWarn = $derived(hoursWarn || minutesWarn);
  let timeTouched = $state(false);
  function markTimeTouched() {
    timeTouched = true;
  }
  function timeBlur(value: number | null) {
    if (value !== null) timeTouched = true;
  }

  // Costs card: collapsed shop rates
  const COSTS_OPEN_KEY = "printpaca-costs-open";

  function readCostsOpen(): boolean {
    try {
      return localStorage.getItem(COSTS_OPEN_KEY) === "1";
    } catch {
      return false;
    }
  }
  function writeCostsOpen(v: boolean): void {
    try {
      localStorage.setItem(COSTS_OPEN_KEY, v ? "1" : "0");
    } catch {
      /* Safari private mode / quota exceeded: session-only state still works. */
    }
  }

  let costsOpen = $state(readCostsOpen());

  const COSTS_WARN_FIELDS = [
    "flatRate",
    "watts",
    "failureRatePct",
    "prepMinutes",
    "laborRate",
    "machineCost",
    "machineLifeHours",
    "monthlyFixed",
    "monthlyHours",
  ];
  const costsHasWarning = $derived(COSTS_WARN_FIELDS.some((f) => warnFor(w, f) !== ""));

  // Costs card: attached hardware rows. A row is a catalog reference (name) plus a per-quote qtyPerUnit,
  // which toPartInputs resolves against cfg.hardware on every recompute. Duplicate rows are allowed (e.g.
  // two magnet rows for clarity).
  function addHardwareRow() {
    const first = cfg.hardware[0];
    if (!first) return;
    part.hardware.push({ name: first.name, qtyPerUnit: 1 });
  }
  function removeHardwareRow(index: number) {
    part.hardware = part.hardware.filter((_, i) => i !== index);
    void focusAfterRowRemoval("remove-hardware-row", index, part.hardware.length, "add-hardware-row");
  }
  // Per-row extended cost (unitCost x qtyPerUnit), display only, so the card shows a live number without a
  // round trip; the authoritative amount is the server's breakdown line. "-" for a blank qty or a stale name.
  function hardwareRowExtended(row: { name: string; qtyPerUnit: number | null }): string {
    const catalog = cfg.hardware.find((h) => h.name === row.name);
    if (!catalog || row.qtyPerUnit === null) return "-";
    return moneyAmount(catalog.unitCost * row.qtyPerUnit, cfg);
  }
  const firstCostsWarnField = $derived(COSTS_WARN_FIELDS.find((f) => warnFor(w, f) !== ""));

  // Force the details open on a new incoming warning, not on every render: the effect depends only on
  // costsHasWarning, so a deliberate re-collapse while it stays true is respected for the session.
  // persistCostsOpen skips its localStorage write while costsHasWarning is true, so the collapse never
  // overwrites the persisted state and a transient warning can't stick the section open later; a fresh mount
  // re-opens it if the warning is still live.
  $effect(() => {
    if (costsHasWarning) costsOpen = true;
  });

  // Takes the open state as a parameter: bind:open's listener and this ontoggle handler fire off the same
  // "toggle" event in registration order, so reading the bound $state would see its pre-update value.
  // e.currentTarget.open is the DOM's post-toggle truth.
  function persistCostsOpen(open: boolean) {
    if (!costsHasWarning) writeCostsOpen(open);
  }

  // Money values carry the currency symbol, like PresetChips' tooltip: "12 per h labor" reads as a unitless
  // number next to the percentages.
  const ratesSummary = $derived(
    `${part.presetName || "custom"}${part.presetModified ? " (modified)" : ""}: ${
      cfg.energy.mode === "tou"
        ? `time-of-use electricity (${Math.round(part.offPeakFrac * 100)}% off-peak)`
        : `${part.flatRate === null ? "?" : moneyPrecise(part.flatRate, cfg, 3)} per kWh electricity`
    }, ${part.laborRate === null ? "?" : moneyAmount(part.laborRate, cfg)} per hour labor, ${part.failureRatePct ?? "?"}% failure`,
  );
</script>

<div class="page">
  <div class="head-row">
    <h1>New quote</h1>
    <button type="button" class="explain-toggle" aria-pressed={allHelpOpen} onclick={toggleAllHelp}>
      Explain this page
    </button>
  </div>

  <QuoteSummaryBar {cfg} {order} />
  {#if previewState.status === "pending"}
    <p class="muted preview-status" role="status">Updating price…</p>
  {:else if previewState.status === "error"}
    <p class="warn-text preview-status" role="alert">
      {previewState.errors[0]} Showing the last known price. It retries on the next change.
    </p>
  {/if}

  {#if showFirstRun}
    <div class="card first-run">
      <h2>Three steps to your first quote</h2>
      <ol>
        <li>Drop a sliced .gcode file below, or type the model weight and print time.</li>
        <li>Your cost appears in the bar above and in Cost breakdown.</li>
        <li>Selling it? Press Set a price.</li>
      </ol>
      <p class="hint">Costs use starter rates. Set your filament price on Filament and your energy rate on Electricity.</p>
      <button type="button" onclick={dismissFirstRun}>Got it</button>
    </div>
  {/if}

  <PartsList {cfg} {order} />

  <div class="tile" role="group" aria-label={TILE_LABELS.gcode} aria-describedby="help-gcode">
    <div class="card">
      <div class="head-row with-help">
        <h2>G-code</h2>
        <CardHelp id="gcode" title={TILE_LABELS.gcode} text={TILE_HELP.gcode} />
      </div>
      {#key part.id}
        <GcodeDrop {cfg} {part} />
        {#if moonrakerEnabled(cfg)}
          <RecentPrints {cfg} {part} />
        {/if}
      {/key}
    </div>
  </div>

  <div class="tile" role="group" aria-label={TILE_LABELS.print} aria-describedby="help-print">
    <div class="card">
      <div class="head-row with-help">
        <h2>Print</h2>
        <CardHelp id="print" title={TILE_LABELS.print} text={TILE_HELP.print} />
      </div>
      <div class="field-rows">
        <LabeledNumber label="Model weight" suffix="g" bind:value={part.weightG} warn={warnFor(w, "weightG")} help="The weight of the model from your slicer, without supports or waste." />
        <div class="field">
          <label for={timeHoursId}>Print time</label>
          <div class="time-pair" role="group" aria-label="Print time">
            <span class="time-input">
              <input
                id={timeHoursId}
                type="number"
                min="0"
                aria-label="Print time (hours)"
                bind:value={part.timeHours}
                class:warn={hoursWarn !== "" && timeTouched}
                oninput={markTimeTouched}
                onblur={() => timeBlur(part.timeHours)}
                aria-invalid={hoursWarn !== "" && timeTouched}
                aria-describedby={timeWarn !== "" && timeTouched ? "print-time-warn" : undefined}
              />
              <span class="suffix">h</span>
            </span>
            <span class="time-input">
              <input
                type="number"
                min="0"
                max="59"
                aria-label="Print time (minutes)"
                bind:value={part.timeMinutes}
                class:warn={minutesWarn !== "" && timeTouched}
                oninput={markTimeTouched}
                onblur={() => timeBlur(part.timeMinutes)}
                aria-invalid={minutesWarn !== "" && timeTouched}
                aria-describedby={timeWarn !== "" && timeTouched ? "print-time-warn" : undefined}
              />
              <span class="suffix">min</span>
            </span>
          </div>
          {#if timeWarn !== "" && timeTouched}<small id="print-time-warn" class="warn-text">{timeWarn}</small>{/if}
        </div>
        <LabeledNumber label="Quantity" suffix="units" bind:value={part.quantity} min={1} step={1} warn={warnFor(w, "quantity")} help="How many copies you are printing. Costs are shown per unit and as an order total." />
        <label class="field">
          <span>Printer</span>
          <select
            value={part.printerId}
            onchange={(e) => applyPrinter(cfg, part, e.currentTarget.value)}
          >
            {#each cfg.printers as p (p.id)}<option value={p.id}>{p.name}</option>{/each}
          </select>
        </label>
        <label class="field">
          <span>Material</span>
          <select
            value={part.materialId}
            onchange={(e) => applyMaterial(cfg, part, e.currentTarget.value)}
          >
            {#each cfg.materials as m (m.id)}<option value={m.id}>{m.name}</option>{/each}
          </select>
        </label>
        <LabeledNumber label="Filament price" suffix={`${cfg.currency.symbol} per kg`} bind:value={part.pricePerKg} warn={warnFor(w, "pricePerKg")} help="What you pay per kilogram of this filament." />
        <LabeledNumber label="Waste" suffix="%" bind:value={part.wastePct} warn={warnFor(w, "wastePct")} help="Extra filament lost to purging, supports, and skirts, as a percentage of the model weight." />
      </div>
      {#if cfg.integrations?.spoolman}
        <SpoolPicker {cfg} {part} />
      {/if}
    </div>
  </div>

  <div class="tile" role="group" aria-label={TILE_LABELS.costs} aria-describedby="help-costs">
    <div class="card">
      <div class="head-row with-help">
        <h2>Costs</h2>
        <CardHelp id="costs" title={TILE_LABELS.costs} text={TILE_HELP.costs} guideAnchor="configuring-printpaca" {guideEnabled} />
      </div>
      <PresetChips {cfg} {part} />
      <p class="rates-summary">
        {ratesSummary}
        {#if costsHasWarning}<span class="advisory-text rates-warn-hint">{fieldLabel(firstCostsWarnField ?? "")} needs a value.</span>{/if}
      </p>
      <details class="rates" bind:open={costsOpen} ontoggle={(e) => persistCostsOpen(e.currentTarget.open)}>
        <summary>Edit rates</summary>
        <div class="field-rows">
          {#if cfg.energy.mode === "tou"}
            <label class="field">
              <span>Off-peak share: {Math.round(part.offPeakFrac * 100)}%</span>
              <input type="range" min="0" max="1" step="0.05" bind:value={part.offPeakFrac} />
            </label>
          {:else}
            <LabeledNumber label="Electricity rate" suffix={`${cfg.currency.symbol} per kWh`} step={0.01} bind:value={part.flatRate} warn={warnFor(w, "flatRate")} help="What your utility charges per kilowatt-hour." />
          {/if}
          <LabeledNumber label="Printer power" suffix="W" bind:value={part.watts} warn={warnFor(w, "watts")} help="Average power draw while printing, in watts." />
          <LabeledNumber label="Failure rate" suffix="%" max={90} bind:value={part.failureRatePct} warn={warnFor(w, "failureRatePct")} onchanged={touched} help="The share of prints that fail. Adds the cost of reprinting to every quote." />
          <LabeledNumber label="Prep time" suffix="min per batch" bind:value={part.prepMinutes} warn={warnFor(w, "prepMinutes")} onchanged={touched} help="Hands-on time to slice, load, and remove the print. It is billed as labor." />
          <LabeledNumber label="Labor rate" suffix={`${cfg.currency.symbol} per hour`} bind:value={part.laborRate} warn={warnFor(w, "laborRate")} onchanged={touched} help="What you charge for your hands-on time." />
          <LabeledNumber label="Printer cost" suffix={cfg.currency.symbol} bind:value={part.machineCost} warn={warnFor(w, "machineCost")} help="What the printer cost to buy. Spread over its lifespan to price wear." />
          <LabeledNumber label="Printer lifespan" suffix="h" bind:value={part.machineLifeHours} warn={warnFor(w, "machineLifeHours")} help="How many print hours you expect the printer to last." />
          <LabeledNumber label="Monthly fixed costs" suffix={cfg.currency.symbol} bind:value={part.monthlyFixed} warn={warnFor(w, "monthlyFixed")} onchanged={touched} help="Rent, software, and other overhead per month. Spread across your monthly print hours." />
          <LabeledNumber label="Monthly print hours" suffix="h" bind:value={part.monthlyHours} warn={warnFor(w, "monthlyHours")} onchanged={touched} help="How many hours per month your printers run. Divides the monthly fixed costs." />
        </div>
      </details>
      {#if cfg.hardware.length > 0}
        <div class="hardware-section">
          <h3 class="section-label">Hardware</h3>
          {#each part.hardware as hw, i (i)}
            <div class="hardware-row">
              <label class="field field-inline">
                <span>Item</span>
                <select aria-label={`Hardware ${i + 1} item`} bind:value={hw.name}>
                  {#each cfg.hardware as h (h.name)}<option value={h.name}>{h.name}</option>{/each}
                </select>
              </label>
              <label class="field field-inline">
                <span>Quantity per unit</span>
                <input
                  type="number"
                  min="0"
                  step="1"
                  aria-label={`Hardware ${i + 1} quantity per unit`}
                  bind:value={hw.qtyPerUnit}
                />
              </label>
              <span class="hardware-extended">{hardwareRowExtended(hw)}</span>
              <button type="button" aria-label={`Remove hardware row ${i + 1}`} data-focus-key={`remove-hardware-row-${i}`} onclick={() => removeHardwareRow(i)}>Remove</button>
            </div>
          {/each}
          <button type="button" data-focus-key="add-hardware-row" onclick={addHardwareRow}>Add hardware</button>
        </div>
      {/if}
    </div>
  </div>

  <div class="tile" role="group" aria-label={TILE_LABELS.breakdown} aria-describedby="help-breakdown">
    {#if sellingEnabled}
      <div class="card toolbar">
        {#if quoteClient.id}
          <div class="field field-inline">
            <span>Client</span>
            <span class="linked-customer">{quoteClient.name || "Linked client"}</span>
            <small class="linked-customer-hint">From the Saved quotes panel's client picker below.</small>
          </div>
        {:else}
          <label class="field field-inline">
            <span>Client (optional)</span>
            <input placeholder="Client name" bind:value={quoteClient.name} />
          </label>
        {/if}
        <label class="field field-inline">
          <span>Project (optional)</span>
          <input placeholder="Project name" bind:value={quoteMeta.project} />
        </label>
        <label class="field field-inline">
          <span>Quote number (optional)</span>
          <input placeholder="Quote #" bind:value={quoteMeta.reference} />
        </label>
        <button class="primary" onclick={() => requestPrint("client")} disabled={!canPrint}>Print client quote</button>
        <button onclick={() => requestPrint("internal")} disabled={!canPrint}>Print cost audit</button>
      </div>
      {#if printHint}<p class="hint">{printHint}</p>{/if}
      {#if cfg.quote.businessName === ""}
        <p class="hint">Printed quotes have no business name yet. <a href="#/settings">Add yours in Settings &gt; Business identity.</a></p>
      {/if}
    {/if}
    {#if multi}<PartsTable {cfg} {order} />{/if}
    <BreakdownTable {breakdown} {cfg} partName={multi ? labels[partIndex] : ""} />
  </div>

  <div class="tile" role="group" aria-label={TILE_LABELS.pricing} aria-describedby="help-pricing">
    <PricingPanel {cfg} {order} {part} {partIndex} />
  </div>

  <div class="tile" role="group" aria-label={TILE_LABELS.history} aria-describedby="help-history">
    <HistoryPanel {cfg} {order} {pricingPending} {settledOrder} />
  </div>
</div>

<style>
  .head-row h1 { margin: 0; }

  .explain-toggle[aria-pressed="true"] {
    background: var(--surface-2);
    border-color: var(--accent);
    color: var(--accent-text);
  }

  .first-run ol { margin: 0 0 0.75rem; padding-left: 1.25rem; display: flex; flex-direction: column; gap: 0.3rem; }

  .muted { color: var(--muted); }
  /* Quiet, not alarming: covers the debounce wait and the round trip (same rationale as PublicQuotePage's
   * preview status line). */
  .preview-status { margin: -0.4rem 0 0; font-size: var(--text-sm); }

  .tile { display: flex; flex-direction: column; gap: 0.4rem; }

  /* Deliberate exception: this compact toolbar keeps its fields side by side (see .field-inline); a wide
   * label/control row would be wrong for a print-metadata strip. */
  .toolbar { display: flex; flex-wrap: wrap; align-items: end; gap: 0.6rem; }
  .toolbar .field { flex: 1 1 160px; min-width: 140px; }
  .toolbar button { white-space: nowrap; }

  /* Static replacement for the customer input once a client is linked; matches the input's box (theme.css
   * padding/min-height) so swapping doesn't shift the toolbar's height. */
  .linked-customer { display: flex; align-items: center; min-height: 44px; padding: 0.4rem 0.6rem; }
  .linked-customer-hint { color: var(--muted); font-size: var(--text-xs); }

  /* Compound print-time field: hours and minutes in one grid cell, so the label/control grid can't split
   * them onto separate rows. */
  .time-pair { display: flex; align-items: center; gap: 0.6rem; }
  .time-input { display: inline-flex; align-items: center; gap: 0.3rem; }
  .field .time-pair input { width: 4.5rem; }

  /* Shop rates: a plain-text summary of the active preset stays visible; the fields collapse behind
   * <details> since they're rarely touched once a preset is picked. */
  .rates-summary { color: var(--muted); font-size: var(--text-sm); margin: 0.4rem 0; }
  .rates-warn-hint { display: inline-block; margin-left: 0.5rem; }
  .rates summary { cursor: pointer; margin: 0.4rem 0; }

  /* Hardware rows: one add-row picker (catalog select + qty) per entry, same layout as SettingsTab's
   * fee-schedule charges. The section is absent from the DOM when cfg.hardware is empty. */
  .hardware-section { margin-top: 0.6rem; }
  .hardware-row { display: flex; align-items: end; gap: 0.5rem; margin-bottom: 0.4rem; flex-wrap: wrap; }
  .hardware-row .field { flex: 1 1 140px; min-width: 120px; }
  .hardware-extended {
    flex: 0 0 auto;
    min-height: 44px;
    display: flex;
    align-items: center;
    font-variant-numeric: tabular-nums;
    color: var(--muted);
  }
</style>
