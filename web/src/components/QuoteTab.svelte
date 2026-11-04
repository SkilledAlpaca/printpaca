<script lang="ts">
  import { onDestroy, tick } from "svelte";
  import type { EffectiveConfig, EngineOptions, OrderBreakdown, OrderInputs } from "../lib/engine/index";
  import { quote, activePart, partLabel, toOrderInputs, quoteEngineOptions, applyPrinter, applyMaterial, printJob, quoteClient, quoteMeta, nameOrId, quoteSession, quoteActions, storedRatesDiffer, useCurrentRates } from "../lib/state.svelte";
  import { computeOrder, type ComputeOrderResult } from "../lib/quotesApi";
  import { EMPTY_ORDER, orderPrintBlockerHint, partBreakdown } from "../lib/orderView";
  import PartsList from "./PartsList.svelte";
  import PartsTable from "./PartsTable.svelte";
  import { createQuotePreview, type QuotePreviewState } from "../lib/quotePreview";
  import { fieldLabel, warnFor, moneyAmount, moneyPrecise } from "../lib/format";
  import LabeledNumber from "./LabeledNumber.svelte";
  import Card from "./ui/Card.svelte";
  import Fieldset from "./ui/Fieldset.svelte";
  import Field from "./ui/Field.svelte";
  import FormGrid from "./ui/FormGrid.svelte";
  import PresetChips from "./PresetChips.svelte";
  import BreakdownTable from "./BreakdownTable.svelte";
  import PricingPanel from "./PricingPanel.svelte";
  import GcodeDrop from "./GcodeDrop.svelte";
  import HistoryPanel from "./HistoryPanel.svelte";
  import SpoolPicker from "./SpoolPicker.svelte";
  import RecentPrints from "./RecentPrints.svelte";
  import PrintStartFields from "./PrintStartFields.svelte";
  import { moonrakerEnabled } from "../lib/moonraker";
  import { prefillFor } from "../lib/prefill.svelte";
  import { saveAttempt } from "../lib/saveAttempt.svelte";
  import QuoteSummaryBar from "./QuoteSummaryBar.svelte";
  import CardHelp from "./CardHelp.svelte";
  import { helpState, setAllHelp } from "../lib/help.svelte";
  import { TILE_HELP, TILE_IDS, type TileId } from "../lib/guideContent";
  import { focusAfterRowRemoval } from "../lib/a11yFocus";
  import { findFieldByLabel } from "../lib/fieldErrors";
  import { presetDisplayName } from "../lib/format";
  import SlicerHelp from "./SlicerHelp.svelte";

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
    preview.update({ inputs: toOrderInputs(cfg), options: quoteEngineOptions(cfg) });
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

  // The client is one field, in the Saved quotes panel beside the label; the print toolbar only shows it.
  function focusClientField(e: MouseEvent) {
    e.preventDefault();
    const el = document.getElementById("history-client");
    el?.scrollIntoView({ block: "center" });
    el?.focus();
  }

  const ratesChanged = $derived(storedRatesDiffer(cfg));

  // The print fields sit below the model drop zone, Recent prints and the part list; keyboard users get
  // there in one Tab instead of dozens.
  function skipToPrintFields(e: MouseEvent) {
    e.preventDefault();
    findFieldByLabel("Model weight")?.focus();
  }

  // Accessible group labels for each tile's role="group" div, and the h2 text for the three tiles rendered
  // here (gcode, print, costs); the others' h2 and CardHelp live in their own components, so it is only their
  // aria-label.
  const TILE_LABELS: Record<TileId, string> = {
    gcode: "Model file",
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

  // Only on a shop with no saved quote: once one exists the steps are old news. savedCount is null until
  // the saved-quote list has loaded, which keeps the panel from flashing in for a returning user.
  const showFirstRun = $derived(
    quoteSession.savedCount === 0 &&
      quote.parts.length === 1 &&
      part.weightG === null &&
      part.timeHours === null &&
      part.timeMinutes === null &&
      !onboarded,
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
  let timeTouchedByUser = $state(false);
  // Errors stay quiet until the user leaves the field, or asks to save, or jumps here from the pricing panel's
  // list of what is missing (all three bump saveAttempt).
  const timeTouched = $derived(timeTouchedByUser || saveAttempt.count > 0);
  function markTimeTouched() {
    timeTouchedByUser = true;
  }
  function timeBlur(value: number | null) {
    if (value !== null) timeTouchedByUser = true;
  }
  // A geometry-only STL/3MF estimate fills the weight but has no slicer time. While both halves are blank the
  // note says so, and leaving the pair (not just tabbing between the halves) counts as touched.
  const noSlicerTime = $derived(prefillFor(part)?.source === "model" && part.timeHours === null && part.timeMinutes === null);
  function timeGroupFocusOut(e: FocusEvent) {
    if (!noSlicerTime) return;
    const next = e.relatedTarget;
    if (next instanceof Node && e.currentTarget instanceof Node && e.currentTarget.contains(next)) return;
    timeTouchedByUser = true;
  }
  const timeDescribedBy = $derived([noSlicerTime ? "print-time-note" : "", timeWarn !== "" && timeTouched ? "print-time-warn" : ""].filter(Boolean).join(" ") || undefined);

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
    `${part.presetName ? presetDisplayName(part.presetName) : "Custom"}${part.presetModified ? " (modified)" : ""}: ${
      cfg.energy.mode === "schedule"
        ? "time-of-use schedule electricity"
        : cfg.energy.mode === "tou"
          ? `time-of-use electricity (${Math.round(part.offPeakFrac * 100)}% off-peak)`
          : `${part.flatRate === null ? "?" : moneyPrecise(part.flatRate, cfg, 3)} per kWh electricity`
    }, ${part.laborRate === null ? "?" : moneyAmount(part.laborRate, cfg)} per hour labor, ${part.failureRatePct ?? "?"}% failure`,
  );
</script>

<div class="page">
  <a class="local-skip" href="#main" onclick={skipToPrintFields}>Skip to model weight and print time</a>
  <div class="head-row">
    <h1>{quoteSession.loadedId ? `Edit quote: ${quoteSession.label.trim() || "Untitled"}` : "New quote"}</h1>
    <button type="button" class="explain-toggle" aria-pressed={allHelpOpen} onclick={toggleAllHelp}>
      Explain this page
    </button>
  </div>

  {#if quoteSession.conflict}
    <div class="card banner-warn" role="alert">
      <p>This was changed in another tab or by another user.</p>
      <div class="edit-actions">
        <button type="button" onclick={() => quoteActions.reload?.()}>Reload</button>
        <button type="button" onclick={() => quoteActions.overwrite?.()}>Overwrite</button>
      </div>
      <p class="hint">Reload discards your edits and shows the saved version. Overwrite saves your edits over it.</p>
    </div>
  {/if}

  {#if ratesChanged}
    <div class="card banner-warn" role="status">
      <p>
        Shop electricity rates changed since this quote was saved. This quote still uses the rates it was saved with.
        <button type="button" class="quiet" onclick={useCurrentRates}>Use current rates</button>
      </p>
    </div>
  {/if}

  {#if showFirstRun}
    <div class="card first-run">
      <h2>Three steps to your first quote</h2>
      <ol>
        <li>Drop a sliced .gcode file below, or type the model weight and print time.</li>
        <li>Your cost appears in the summary beside the form, and in its Breakdown.</li>
        <li>Selling it? Press Set a price.</li>
      </ol>
      <p class="hint">Costs use starter rates. Set your filament price on Filament and your energy rate on Electricity.</p>
      <button type="button" onclick={dismissFirstRun}>Got it</button>
    </div>
  {/if}

  <div class="layout">
    <div class="main-col">
      <PartsList {cfg} {order}>
        <div class="tile" role="group" aria-label={TILE_LABELS.gcode} aria-describedby="help-gcode">
          <Card>
            <Fieldset legend="1. Model and print time">
              <div class="section-intro">
                <p class="hint">Drop a sliced file, or type the weight and time from your slicer.</p>
                <CardHelp id="gcode" title={TILE_LABELS.gcode} text={TILE_HELP.gcode} />
              </div>
              {#key part.id}
                <GcodeDrop {cfg} {part} />
                {#if moonrakerEnabled(cfg)}
                  <RecentPrints {cfg} {part} />
                {/if}
              {/key}
              <FormGrid>
                <LabeledNumber label="Model weight" suffix="g" bind:value={part.weightG} warn={warnFor(w, "weightG")} help="The weight of the model from your slicer, without supports or waste." />
                <Field id={timeHoursId} label="Print time" error={timeWarn !== "" && timeTouched ? timeWarn : undefined} describedby={noSlicerTime ? "print-time-note" : undefined}>
                  {#snippet children(a)}
                    <div class="time-pair" role="group" aria-label="Print time" onfocusout={timeGroupFocusOut}>
                      <span class="time-input">
                        <input
                          id={a.id}
                          type="number"
                          min="0"
                          aria-label="Print time (hours)"
                          bind:value={part.timeHours}
                          class:warn={hoursWarn !== "" && timeTouched}
                          oninput={markTimeTouched}
                          onblur={() => timeBlur(part.timeHours)}
                          aria-invalid={hoursWarn !== "" && timeTouched}
                          aria-describedby={a.describedby}
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
                          aria-describedby={a.describedby}
                        />
                        <span class="suffix">min</span>
                      </span>
                    </div>
                    {#if noSlicerTime}
                      <small id="print-time-note" class="hint-text">This file has no slicer time. Enter the print time from your slicer.</small>
                      <SlicerHelp />
                    {/if}
                  {/snippet}
                </Field>
              </FormGrid>
            </Fieldset>
          </Card>
        </div>

        <div class="tile" role="group" aria-label={TILE_LABELS.print} aria-describedby="help-print">
          <Card>
            <Fieldset legend="2. Material and printer">
              <div class="section-intro">
                <p class="hint">What you print on, and how many.</p>
                <CardHelp id="print" title={TILE_LABELS.print} text={TILE_HELP.print} />
              </div>
              <FormGrid>
                <Field id={`${uid}-material`} label="Material" size="m">
                  {#snippet children(a)}
                    <select id={a.id} aria-describedby={a.describedby} value={part.materialId} onchange={(e) => applyMaterial(cfg, part, e.currentTarget.value)}>
                      {#each cfg.materials as m (m.id)}<option value={m.id}>{m.name}</option>{/each}
                    </select>
                  {/snippet}
                </Field>
                <LabeledNumber label="Filament price" suffix={`${cfg.currency.symbol} per kg`} bind:value={part.pricePerKg} warn={warnFor(w, "pricePerKg")} help="What you pay per kilogram of this filament." />
                <LabeledNumber label="Waste" suffix="%" bind:value={part.wastePct} warn={warnFor(w, "wastePct")} help="Extra filament lost to purging, supports, and skirts, as a percentage of the model weight." />
                <Field id={`${uid}-printer`} label="Printer" size="m">
                  {#snippet children(a)}
                    <select id={a.id} aria-describedby={a.describedby} value={part.printerId} onchange={(e) => applyPrinter(cfg, part, e.currentTarget.value)}>
                      {#each cfg.printers as p (p.id)}<option value={p.id}>{p.name}</option>{/each}
                    </select>
                  {/snippet}
                </Field>
                <LabeledNumber label="Quantity" suffix="units" bind:value={part.quantity} min={1} step={1} warn={warnFor(w, "quantity")} help="How many copies you are printing. Costs are shown per unit and as an order total." />
                {#if cfg.energy.mode === "schedule"}
                  <PrintStartFields {cfg} {part} warn={warnFor(w, "printStart") || warnFor(w, "schedule")} />
                {/if}
              </FormGrid>
              {#if cfg.integrations?.spoolman}
                <SpoolPicker {cfg} {part} />
              {/if}

              <div class="costs tile" role="group" aria-label={TILE_LABELS.costs} aria-describedby="help-costs">
                <div class="section-intro">
                  <h3 class="sub-heading">Costs</h3>
                  <CardHelp id="costs" title={TILE_LABELS.costs} text={TILE_HELP.costs} guideAnchor="configuring-printpaca" {guideEnabled} />
                </div>
                <PresetChips {cfg} {part} />
                <p class="rates-summary">
                  {ratesSummary}
                  {#if costsHasWarning}<span class="advisory-text rates-warn-hint">{fieldLabel(firstCostsWarnField ?? "")} needs a value.</span>{/if}
                </p>
                <details class="rates" bind:open={costsOpen} ontoggle={(e) => persistCostsOpen(e.currentTarget.open)}>
                  <summary>Edit rates</summary>
                  <FormGrid>
                    {#if cfg.energy.mode === "tou"}
                      <Field id={`${uid}-offpeak`} label={`Off-peak share: ${Math.round(part.offPeakFrac * 100)}%`}>
                        {#snippet children(a)}
                          <input id={a.id} type="range" min="0" max="1" step="0.05" bind:value={part.offPeakFrac} />
                        {/snippet}
                      </Field>
                    {:else if cfg.energy.mode !== "schedule"}
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
                  </FormGrid>
                </details>
                {#if cfg.hardware.length > 0}
                  <div class="hardware-section">
                    <h4 class="section-label">Hardware</h4>
                    {#each part.hardware as hw, i (i)}
                      <div class="hardware-row">
                        <Field id={`${uid}-hw-${i}-item`} label="Item" size="m">
                          {#snippet children(a)}
                            <select id={a.id} aria-label={`Hardware ${i + 1} item`} bind:value={hw.name}>
                              {#each cfg.hardware as h (h.name)}<option value={h.name}>{h.name}</option>{/each}
                            </select>
                          {/snippet}
                        </Field>
                        <Field id={`${uid}-hw-${i}-qty`} label="Quantity per unit" size="s">
                          {#snippet children(a)}
                            <input
                              id={a.id}
                              type="number"
                              min="0"
                              step="1"
                              aria-label={`Hardware ${i + 1} quantity per unit`}
                              bind:value={hw.qtyPerUnit}
                            />
                          {/snippet}
                        </Field>
                        <span class="hardware-extended">{hardwareRowExtended(hw)}</span>
                        <button type="button" aria-label={`Remove hardware row ${i + 1}`} data-focus-key={`remove-hardware-row-${i}`} onclick={() => removeHardwareRow(i)}>Remove</button>
                      </div>
                    {/each}
                    <button type="button" data-focus-key="add-hardware-row" onclick={addHardwareRow}>Add hardware</button>
                  </div>
                {/if}
              </div>
            </Fieldset>
          </Card>
        </div>
      </PartsList>

      <div class="tile" role="group" aria-label={TILE_LABELS.pricing} aria-describedby="help-pricing">
        <PricingPanel {cfg} {order} {part} {partIndex} />
      </div>

      {#if sellingEnabled}
        <div class="tile" role="group" aria-label="Print the quote">
          <Card>
            <h2 class="card-heading">Print the quote</h2>
            <FormGrid>
              <div class="linked-client">
                <span class="linked-label">Client on the quote</span>
                <span class="linked-customer">{quoteClient.name || (quoteClient.id ? "Linked client" : "No client")}</span>
              </div>
              <Field id={`${uid}-project`} label="Project (optional)" size="m">
                {#snippet children(a)}
                  <input id={a.id} placeholder="Project name" bind:value={quoteMeta.project} />
                {/snippet}
              </Field>
              <Field id={`${uid}-reference`} label="Quote number (optional)" size="m">
                {#snippet children(a)}
                  <input id={a.id} placeholder="Quote #" bind:value={quoteMeta.reference} />
                {/snippet}
              </Field>
            </FormGrid>
            <p class="hint">Set the client in the summary, under Client for this quote. <a href="#main" onclick={focusClientField}>Go to Client for this quote</a></p>
            <div class="print-actions">
              <button class="primary" onclick={() => requestPrint("client")} disabled={!canPrint}>Print client quote</button>
              <button onclick={() => requestPrint("internal")} disabled={!canPrint}>Print cost audit</button>
            </div>
            {#if printHint}<p class="hint">{printHint}</p>{/if}
            {#if cfg.quote.businessName === ""}
              <p class="hint">Printed quotes have no business name yet. <a href="#/settings">Add yours in Settings &gt; Business identity.</a></p>
            {/if}
          </Card>
        </div>
      {/if}
    </div>

    <HistoryPanel {cfg} {order} {pricingPending} {settledOrder}>
      {#snippet summary()}
        <QuoteSummaryBar
          {cfg}
          {order}
          previewStatus={previewState.status}
          previewError={previewState.status === "error" ? `${previewState.errors[0]} Showing the last known price. It retries on the next change.` : ""}
        />
        <div class="tile" role="group" aria-label={TILE_LABELS.breakdown} aria-describedby="help-breakdown">
          <details class="breakdown-disclosure">
            <summary>Breakdown</summary>
            <div class="breakdown-body">
              {#if multi}<PartsTable {cfg} {order} />{/if}
              <BreakdownTable {breakdown} {cfg} partName={multi ? labels[partIndex] : ""} />
            </div>
          </details>
        </div>
      {/snippet}
      {#snippet compactStats()}
        <QuoteSummaryBar {cfg} {order} variant="compact" />
      {/snippet}
    </HistoryPanel>
  </div>
</div>

<style>
  .head-row h1 { margin: 0; }

  .explain-toggle[aria-pressed="true"] {
    background: var(--surface-2);
    border-color: var(--accent);
    color: var(--accent-text);
  }

  .local-skip {
    position: absolute;
    left: -9999px;
    top: 0;
    z-index: 5;
    padding: var(--space-2) var(--space-4);
    background: var(--accent);
    color: var(--accent-contrast);
    border-radius: var(--radius);
  }
  .local-skip:focus { left: var(--space-2); top: var(--space-2); }

  .edit-actions { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); }

  :global(.first-run) ol { margin: 0 0 var(--space-3); padding-inline-start: var(--space-5); display: flex; flex-direction: column; gap: var(--space-1); }

  /* One column with the summary pinned to the bottom below 1100px; two columns from 1100px, the form on the
   * left and the sticky summary on the right (D23). The summary and the recent saves place themselves with
   * grid areas declared in HistoryPanel. */
  .layout { display: flex; flex-direction: column; gap: var(--gap-section); min-width: 0; }
  .main-col { display: flex; flex-direction: column; gap: var(--gap-section); min-width: 0; }
  @media (min-width: 1100px) {
    .layout {
      display: grid;
      grid-template-columns: minmax(0, 1fr) minmax(var(--field-m), 24rem);
      grid-template-areas: "main summary" "recent summary";
      grid-template-rows: auto 1fr;
      column-gap: var(--gap-section);
      align-items: start;
    }
    .main-col { grid-area: main; }
  }

  .tile { display: flex; flex-direction: column; gap: var(--space-2); min-width: 0; }
  .tile :global(.card) { display: flex; flex-direction: column; gap: var(--gap); }
  /* The sentence at the left of a section and its "?" help toggle at the right; the help note drops onto its
   * own row below (CardHelp sets grid-column: 1 / -1 on it). */
  .section-intro { display: grid; grid-template-columns: 1fr auto; align-items: start; gap: var(--space-2); }
  .section-intro p { margin: 0; }
  .sub-heading { margin: 0; font-size: var(--type-heading); }
  :global(.card-heading) { margin-block: 0; }
  .costs { margin-block-start: var(--gap); padding-block-start: var(--gap); border-top: 1px solid var(--border); }
  .hint-text { color: var(--muted); font-size: var(--type-secondary); }

  /* Static replacement for the customer input once a client is linked; matches the input's box so swapping
   * doesn't shift the row's height. */
  .linked-client { display: flex; flex-direction: column; gap: var(--space-1); min-width: 0; }
  .linked-label { font-size: var(--type-secondary); font-weight: 600; }
  .linked-customer { display: flex; align-items: center; min-height: var(--control-h); overflow-wrap: anywhere; }
  .print-actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }

  /* Compound print-time field: hours and minutes in one grid cell, so the label/control grid can't split
   * them onto separate rows. */
  .time-pair { display: flex; align-items: center; gap: var(--space-3); }
  .time-input { display: inline-flex; align-items: center; gap: var(--space-1); }
  .time-input input { flex: 0 1 var(--field-xs); min-width: 0; }

  /* Shop rates: a plain-text summary of the active preset stays visible; the fields collapse behind
   * <details> since they're rarely touched once a preset is picked. */
  .rates-summary { color: var(--muted); font-size: var(--type-secondary); margin: var(--space-2) 0; }
  .rates-warn-hint { display: inline-block; margin-inline-start: var(--space-2); }
  .rates summary, .breakdown-disclosure summary { cursor: pointer; display: list-item; list-style-position: inside; padding-block: var(--space-2); font-weight: 600; }
  .rates > :global(.form-grid) { margin-block-start: var(--space-2); }
  .breakdown-body { display: flex; flex-direction: column; gap: var(--gap); padding-block-start: var(--space-2); }

  /* Hardware rows: one add-row picker (catalog select + qty) per entry. The section is absent from the DOM
   * when cfg.hardware is empty. */
  .hardware-section { display: flex; flex-direction: column; align-items: flex-start; gap: var(--space-2); margin-block-start: var(--gap); }
  .hardware-section h4 { margin: 0; }
  .hardware-row { display: flex; align-items: flex-end; gap: var(--space-2) var(--gap); flex-wrap: wrap; }
  .hardware-row :global(.ui-field) { flex: 1 1 var(--field-s); }
  .hardware-extended {
    flex: 0 0 auto;
    min-height: var(--control-h);
    display: flex;
    align-items: center;
    font-variant-numeric: tabular-nums;
    color: var(--muted);
  }
</style>
