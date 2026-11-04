import {
  MAX_ORDER_PARTS,
  MAX_PART_LABEL_CHARS,
  defaultEngineOptions,
  type EffectiveConfig,
  type EnergyInputs,
  type EngineOptions,
  type OrderBreakdown,
  type OrderInputs,
  type PartInputs,
} from "./engine/index";
import { localId } from "./api";
import { FALLBACK_DENSITY } from "./filamentDefaults";
import type { Invoice } from "./invoicesApi";
import { energyInputsFor } from "./quoteInputs";
import { nextStartWall } from "./engine/schedule";
import { demoLocal, resolveCurrencyPreset } from "./demoLocal.svelte";
import { prefill } from "./prefill.svelte";

interface SpoolOverrideState {
  label: string;
  pricePerKg: number | null; // null = Spoolman gave no usable price; never coerce to 0
  density: number;
  remainingG: number | null; // null = Spoolman doesn't report remaining weight for this spool
  spoolId?: number; // Spoolman spool id, so the picker can re-select it without duplicate local state
}

/** One part as persisted in formState (no id: ids are per editor session and never survive a save or
 * export). Hardware rows reference cfg.hardware by name plus a per-part qtyPerUnit (like feeScheduleName);
 * toPartInputs resolves them into the engine's HardwareLine. */
export interface PartFormState {
  label: string;
  weightG: number | null;
  timeHours: number | null;
  timeMinutes: number | null;
  pricePerKg: number | null;
  wastePct: number | null;
  printerId: string;
  materialId: string;
  watts: number | null;
  machineCost: number | null;
  machineLifeHours: number | null;
  flatRate: number | null;
  offPeakFrac: number;
  /** Schedule energy mode: this part's print start as wall-clock "YYYY-MM-DDTHH:MM" in the instance time
   * zone. "" uses the order's start. Saved with the quote so repricing is deterministic. */
  printStart: string;
  failureRatePct: number | null;
  prepMinutes: number | null;
  laborRate: number | null;
  monthlyFixed: number | null;
  monthlyHours: number | null;
  quantity: number | null;
  hardware: { name: string; qtyPerUnit: number | null }[];
  presetName: string;
  presetModified: boolean;
  salePriceOverride: number | null;
  spoolOverride: SpoolOverrideState | null;
}

/** A part in the live editor: the persisted fields plus a stable id (key for the parts list, the
 * active-part pointer and prefill.svelte.ts's records). */
export interface PartForm extends PartFormState {
  id: string;
}

/** Order-level editor state (selling block, shipping, tax, fee schedule); applies once to the whole quote
 * (server/pricing_order.go). */
export interface OrderForm {
  sellingEnabled: boolean;
  strategy: "margin" | "markup";
  marginPct: number | null;
  markupMultiplier: number | null;
  taxRatePct: number | null;
  shippingCharge: number | null;
  shippingCost: number | null;
  feeScheduleName: string;
  /** Schedule energy mode: the order's print start (wall-clock "YYYY-MM-DDTHH:MM" in the instance time
   * zone). Seeded with the next occurrence of the schedule's default start time. "" in other modes. */
  printStart: string;
}

/** The live editor state. */
export interface QuoteState {
  parts: PartForm[];
  order: OrderForm;
  activePartId: string;
}

/** What a quote's `formState` holds. */
export interface FormState {
  version: 1;
  parts: PartFormState[];
  order: OrderForm;
}

/** Fresh defaults for a part; a function so every caller gets its own hardware array. */
export function defaultPartState(label = "Part 1"): PartFormState {
  return {
    label,
    weightG: null,
    timeHours: null,
    timeMinutes: null,
    pricePerKg: 20,
    wastePct: 0,
    printerId: "",
    materialId: "",
    watts: 250,
    machineCost: 400,
    machineLifeHours: 4000,
    flatRate: 0.15,
    offPeakFrac: 0.7,
    printStart: "",
    failureRatePct: 5,
    prepMinutes: 5,
    laborRate: 12,
    monthlyFixed: 0,
    monthlyHours: 100,
    quantity: 1,
    hardware: [],
    presetName: "",
    presetModified: false,
    salePriceOverride: null,
    spoolOverride: null,
  };
}

export function defaultOrderForm(): OrderForm {
  return {
    sellingEnabled: false,
    strategy: "margin",
    marginPct: 30,
    markupMultiplier: 2.5,
    taxRatePct: 0,
    shippingCharge: 0,
    shippingCost: 0,
    feeScheduleName: "none",
    printStart: "",
  };
}

const PART_KEYS = Object.keys(defaultPartState()) as (keyof PartFormState)[];
const ORDER_KEYS = Object.keys(defaultOrderForm()) as (keyof OrderForm)[];

function newPart(label: string, base: PartFormState = defaultPartState(label)): PartForm {
  return { ...base, label, id: localId() };
}

function initialState(): QuoteState {
  const first = newPart("Part 1");
  return { parts: [first], order: defaultOrderForm(), activePartId: first.id };
}

export const quote: QuoteState = $state(initialState());

/** The part the editor cards show. Always defined: removePart keeps at least one and restore/reset re-point
 * activePartId. */
export function activePart(): PartForm {
  return quote.parts.find((p) => p.id === quote.activePartId) ?? quote.parts[0]!;
}

export function setActivePart(id: string): void {
  if (quote.parts.some((p) => p.id === id)) quote.activePartId = id;
}

/** The smallest "Part N" not already used, so removing Part 2 of three and adding again yields Part 2, not
 * a duplicate Part 3. */
export function nextPartLabel(parts: readonly { label: string }[]): string {
  const used = new Set(parts.map((p) => p.label));
  for (let n = 1; ; n++) {
    const candidate = `Part ${n}`;
    if (!used.has(candidate)) return candidate;
  }
}

/** Appends a part. Printer, material and shop-rate fields start from the active part (a counter order is
 * usually one machine and filament); weight, time, quantity, hardware, override and spool start clean.
 * With `seed` the part is built from it instead. Returns the new part without activating it. */
export function addPart(cfg: EffectiveConfig, seed?: Partial<PartFormState>): PartForm {
  if (quote.parts.length >= MAX_ORDER_PARTS) return activePart();
  const base = $state.snapshot(activePart()) as PartForm;
  const part = newPart(seed?.label ?? nextPartLabel(quote.parts), {
    ...defaultPartState(),
    printerId: base.printerId,
    materialId: base.materialId,
    pricePerKg: base.pricePerKg,
    wastePct: base.wastePct,
    watts: base.watts,
    machineCost: base.machineCost,
    machineLifeHours: base.machineLifeHours,
    flatRate: base.flatRate,
    offPeakFrac: base.offPeakFrac,
    failureRatePct: base.failureRatePct,
    prepMinutes: base.prepMinutes,
    laborRate: base.laborRate,
    monthlyFixed: base.monthlyFixed,
    monthlyHours: base.monthlyHours,
    presetName: base.presetName,
    presetModified: base.presetModified,
  });
  // A spool's price is specific to that spool; the new part prices from the material catalog.
  if (base.spoolOverride) applyMaterial(cfg, part, part.materialId);
  Object.assign(part, seed, { label: seed?.label ?? part.label });
  quote.parts.push(part);
  return quote.parts[quote.parts.length - 1]!;
}

/** Inserts a copy (new id, label "<label> (copy)" trimmed to the label limit) right after the original and
 * returns it. */
export function duplicatePart(id: string): PartForm | null {
  const i = quote.parts.findIndex((p) => p.id === id);
  if (i === -1 || quote.parts.length >= MAX_ORDER_PARTS) return null;
  const src = $state.snapshot(quote.parts[i]!) as PartForm;
  const copy: PartForm = { ...src, id: localId(), label: `${src.label} (copy)`.slice(0, MAX_PART_LABEL_CHARS) };
  quote.parts.splice(i + 1, 0, copy);
  return quote.parts[i + 1]!;
}

/** Removes a part (the last can't be removed); re-points the active part to a neighbor if it was removed. */
export function removePart(id: string): void {
  if (quote.parts.length <= 1) return;
  const i = quote.parts.findIndex((p) => p.id === id);
  if (i === -1) return;
  quote.parts.splice(i, 1);
  if (quote.activePartId === id) quote.activePartId = quote.parts[Math.min(i, quote.parts.length - 1)]!.id;
}

/** Replaces the editor state with a new one-part quote seeded from shop defaults; the Requests tab's "Open
 * in quote" starts here. */
export function startOnePartQuote(cfg: EffectiveConfig): PartForm {
  const part = newPart("Part 1");
  Object.assign(quote.order, orderBase(cfg));
  quote.parts = [part];
  quote.activePartId = part.id;
  seedPart(cfg, quote.parts[0]!);
  clearQuoteSession();
  return quote.parts[0]!;
}

/** Empties everything that belongs to the quote being edited but isn't a part or order field: the saved
 * quote it is bound to, its label, client and meta, the picker records, the pinned rates. */
function clearQuoteSession(): void {
  prefill.byPart = {};
  quoteClient.id = "";
  quoteClient.name = "";
  quoteMeta.project = "";
  quoteMeta.reference = "";
  quoteSession.loadedId = null;
  quoteSession.version = "";
  quoteSession.label = "";
  quoteSession.pin = null;
  quoteSession.conflict = false;
  quoteSession.status = { text: "", failed: false };
}

/** A blank quote: one part and the order reseeded from shop defaults, no label, client, meta or bound saved
 * quote. Every "New quote" entry point goes through here (see startNewQuote), so nothing carries over from
 * the last quote; "Duplicate" on a saved quote is the explicit way to carry values over. */
export function resetQuote(cfg: EffectiveConfig): void {
  const part = newPart("Part 1");
  seedPart(cfg, part);
  quote.parts = [part];
  quote.activePartId = part.id;
  Object.assign(quote.order, orderBase(cfg));
  clearQuoteSession();
  captureQuoteBaseline();
}

/** Starts a blank quote after asking when there are unsaved edits. Returns false, changing nothing, when
 * the user declines. */
export function startNewQuote(cfg: EffectiveConfig): boolean {
  if (quoteIsDirty() && !confirm("You have unsaved changes to this quote. Start a new quote anyway?")) return false;
  resetQuote(cfg);
  return true;
}

let seeded = false;

function seedPart(cfg: EffectiveConfig, part: PartFormState): void {
  part.printerId = cfg.defaults.printer;
  part.materialId = cfg.defaults.material;
  part.quantity = cfg.defaults.quantity;
  part.failureRatePct = cfg.defaults.failureRatePct;
  part.flatRate = cfg.energy.flatRate;
  part.offPeakFrac = cfg.energy.tou.defaultOffPeakFrac;
  part.printStart = "";
  applyPrinter(cfg, part, cfg.defaults.printer);
  applyMaterial(cfg, part, cfg.defaults.material);
  applyPreset(cfg, part, cfg.defaults.preset);
}

function seedOrder(cfg: EffectiveConfig, order: OrderForm): void {
  order.strategy = cfg.pricing.strategy;
  order.marginPct = cfg.pricing.marginPct;
  order.markupMultiplier = cfg.pricing.markupMultiplier;
  order.taxRatePct = cfg.pricing.taxRatePct;
  order.shippingCharge = cfg.pricing.defaultShippingCharge;
  order.shippingCost = cfg.pricing.defaultShippingCost;
  order.feeScheduleName = cfg.pricing.defaultFeeSchedule;
  order.printStart = defaultPrintStart(cfg);
}

/** The next occurrence of the schedule's default start time in the instance time zone, or "" outside
 * schedule mode. */
export function defaultPrintStart(cfg: EffectiveConfig, now: Date = new Date()): string {
  if (cfg.energy.mode !== "schedule") return "";
  return nextStartWall(cfg.energy.schedule.defaultStartTime, cfg.timezone || "UTC", now);
}

/** The base a stored part is overlaid on: shop defaults when a config is available, else the constants. */
function partBase(cfg: EffectiveConfig | undefined, label: string): PartFormState {
  const base = defaultPartState(label);
  if (cfg) seedPart(cfg, base);
  return base;
}

function orderBase(cfg: EffectiveConfig | undefined): OrderForm {
  const base = defaultOrderForm();
  if (cfg) seedOrder(cfg, base);
  return base;
}

export function applyConfig(cfg: EffectiveConfig): void {
  if (seeded) return;
  seeded = true;
  seedOrder(cfg, quote.order);
  for (const part of quote.parts) seedPart(cfg, part);
}

export function applyPrinter(cfg: EffectiveConfig, part: PartFormState, id: string): void {
  const p = cfg.printers.find((x) => x.id === id);
  if (!p) return;
  part.printerId = id;
  part.watts = p.watts;
  part.machineCost = p.purchaseCost;
  part.machineLifeHours = p.lifespanHours;
}

export function applyMaterial(cfg: EffectiveConfig, part: PartFormState, id: string): void {
  const m = cfg.materials.find((x) => x.id === id);
  if (!m) return;
  part.materialId = id;
  part.pricePerKg = m.pricePerKg;
  part.wastePct = m.wastePct;
  part.spoolOverride = null;
}

export function applyPreset(cfg: EffectiveConfig, part: PartFormState, name: string): void {
  const p = cfg.presets[name];
  if (!p) return;
  part.presetName = name;
  part.presetModified = false;
  part.failureRatePct = p.failureRatePct;
  part.prepMinutes = p.prepMinutes;
  part.laborRate = p.laborRate;
  part.monthlyFixed = p.monthlyFixed;
  part.monthlyHours = p.monthlyHours;
}

export function currentDensity(cfg: EffectiveConfig, part: PartFormState): number {
  if (part.spoolOverride) return part.spoolOverride.density;
  return cfg.materials.find((m) => m.id === part.materialId)?.density ?? FALLBACK_DENSITY;
}

export function currentDiameter(cfg: EffectiveConfig, part: PartFormState): number {
  return cfg.printers.find((p) => p.id === part.printerId)?.filamentDiameterMm ?? 1.75;
}

/** null (cleared input) becomes NaN so engine validation flags it, never a silent 0. */
function num(v: number | null): number {
  return v === null ? NaN : v;
}

/** Blank-half defaulting for the compound time field: a lone blank hours or minutes is genuinely 0; only
 * when both are blank is the time unknown (both NaN, so the required-field check flags it). Pure and
 * exported for testing. */
export function timePair(hours: number | null, minutes: number | null): { hours: number; minutes: number } {
  if (hours === null && minutes === null) return { hours: NaN, minutes: NaN };
  return { hours: hours ?? 0, minutes: minutes ?? 0 };
}

/** Drops hardware rows whose name no longer matches a cfg.hardware entry. The primary cleanup, called from
 * reconcileWithConfig on restore; toPartInputs also drops such rows, but only from the engine bag (defense
 * in depth). */
export function pruneMissingHardware<Row extends { name: string }>(
  hardware: Row[],
  catalog: { name: string }[],
): { kept: Row[]; missing: Row[] } {
  const missing = hardware.filter((h) => !catalog.some((c) => c.name === h.name));
  if (missing.length === 0) return { kept: hardware, missing: [] };
  return { kept: hardware.filter((h) => catalog.some((c) => c.name === h.name)), missing };
}

/** The label sent to the engine: trimmed, "Part N" when blank, capped at the server's limit. */
export function partLabel(part: { label: string }, index: number): string {
  const trimmed = part.label.trim();
  return (trimmed === "" ? `Part ${index + 1}` : trimmed).slice(0, MAX_PART_LABEL_CHARS);
}

type PartSource = PartFormState & { id?: string };

/** One part's engine inputs. `selling` gates the per-part sale price override, sent only with an order
 * pricing block (a costed-only quote has no price to override). */
function toPartInputs(
  cfg: EffectiveConfig,
  src: PartSource,
  index: number,
  selling: boolean,
  orderStart: string,
  pin: EnergyPin | null,
): PartInputs {
  const time = timePair(src.timeHours, src.timeMinutes);
  const inputs: PartInputs = {
    label: partLabel(src, index),
    weightG: num(src.weightG),
    pricePerKg: num(src.pricePerKg),
    wastePct: num(src.wastePct),
    timeHours: time.hours,
    timeMinutes: time.minutes,
    watts: num(src.watts),
    energy:
      pin?.energy[index] !== undefined
        ? (structuredClone($state.snapshot(pin.energy[index]!)) as EnergyInputs)
        : energyInputsFor(cfg.energy, src.offPeakFrac, num(src.flatRate), {
            timezone: cfg.timezone || "UTC",
            start: src.printStart !== "" ? src.printStart : orderStart,
          }),
    failureRatePct: num(src.failureRatePct),
    prepMinutes: num(src.prepMinutes),
    laborRate: num(src.laborRate),
    machineCost: num(src.machineCost),
    machineLifeHours: num(src.machineLifeHours),
    monthlyFixed: num(src.monthlyFixed),
    monthlyHours: num(src.monthlyHours),
    quantity: num(src.quantity),
  };
  // Resolve each row's catalog reference into the engine's HardwareLine (name/unitCost/lostOnFailure); rows
  // with no matching cfg.hardware entry are dropped.
  if (src.hardware.length > 0) {
    const resolved = src.hardware
      .map((row) => {
        const catalog = cfg.hardware.find((h) => h.name === row.name);
        if (!catalog) return null;
        return { name: catalog.name, unitCost: catalog.unitCost, qtyPerUnit: num(row.qtyPerUnit), lostOnFailure: catalog.lostOnFailure };
      })
      .filter((h): h is NonNullable<typeof h> => h !== null);
    if (resolved.length > 0) inputs.hardware = resolved;
  }
  if (selling && src.salePriceOverride !== null && src.salePriceOverride !== undefined) {
    inputs.salePriceOverride = src.salePriceOverride;
  }
  return inputs;
}

/** The engine's OrderInputs for an editor snapshot. `src` defaults to the live state but accepts any
 * {parts, order} (QuotesTab's import builds the bag from a stored formState). The pricing block is present
 * only when selling is on. */
export function toOrderInputs(
  cfg: EffectiveConfig,
  src: { parts: PartSource[]; order: OrderForm } = quote,
): OrderInputs {
  const { order } = src;
  // A reopened saved quote keeps its stored rates; imports and other bags never do.
  const pin = src === quote ? activeEnergyPin() : null;
  const out: OrderInputs = {
    parts: src.parts.map((p, i) => toPartInputs(cfg, p, i, order.sellingEnabled, order.printStart, pin)),
  };
  if (order.sellingEnabled) {
    out.pricing = {
      strategy: order.strategy,
      marginPct: num(order.marginPct),
      markupMultiplier: num(order.markupMultiplier),
      taxRatePct: num(order.taxRatePct),
      shippingCharge: num(order.shippingCharge),
      shippingCost: num(order.shippingCost),
      feeSchedule: order.feeScheduleName !== "none" ? cfg.feeSchedules[order.feeScheduleName] : undefined,
      feeScheduleName: order.feeScheduleName !== "none" ? order.feeScheduleName : undefined,
    };
  }
  return out;
}

/** A plain-data formState snapshot of the editor (ids dropped); what save, update and dirty tracking
 * persist and compare. */
export function toFormState(src: { parts: PartSource[]; order: OrderForm } = quote): FormState {
  const snap = $state.snapshot(src) as { parts: PartSource[]; order: OrderForm };
  return {
    version: 1,
    parts: snap.parts.map((p) => {
      const { id: _id, ...rest } = p;
      return rest as PartFormState;
    }),
    order: { ...snap.order },
  };
}

function overlay<T extends object>(base: T, src: Record<string, unknown>, keys: (keyof T)[]): T {
  const out = base as Record<string, unknown>;
  for (const key of keys) {
    if (Object.prototype.hasOwnProperty.call(src, key)) out[key as string] = src[key as string];
  }
  return base;
}

/** Reads a stored formState into {parts, order}. Missing keys fall back to defaults. Returns null for
 * anything that isn't a version-1 object with at least one part, so an unknown version is refused rather
 * than guessed at. Pure, shared by restore, import and tests. */
export function parseFormState(raw: unknown, cfg?: EffectiveConfig): { parts: PartFormState[]; order: OrderForm } | null {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) return null;
  // Deep copy first: restored hardware rows and spool overrides must not alias the caller's objects.
  const src = structuredClone($state.snapshot(raw)) as Record<string, unknown>;
  if (src.version !== 1 || !Array.isArray(src.parts) || src.parts.length === 0) return null;
  const parts: PartFormState[] = [];
  for (const p of src.parts) {
    if (!p || typeof p !== "object") return null;
    parts.push(overlay(partBase(cfg, "Part 1"), p as Record<string, unknown>, PART_KEYS));
  }
  const order = overlay(orderBase(cfg), (src.order && typeof src.order === "object" ? src.order : {}) as Record<string, unknown>, ORDER_KEYS);
  // A quote saved outside schedule mode has no start; give it the default so it prices once the mode is on.
  if (cfg && order.printStart === "") order.printStart = defaultPrintStart(cfg);
  return { parts, order };
}

/** Replaces the editor state with a migrated formState (fresh part ids, first part active). Returns false,
 * leaving the editor untouched, when the stored value is unusable. */
export function restoreFormState(raw: unknown, cfg?: EffectiveConfig): boolean {
  const m = parseFormState(raw, cfg);
  if (!m) return false;
  const parts = m.parts.map((p) => newPart(p.label, p));
  quote.parts = parts;
  // In place: components hold a reference to quote.order.
  Object.assign(quote.order, m.order);
  quote.activePartId = parts[0]!.id;
  return true;
}

/** Fixes a just-restored quote against the current config: a missing printer, material or fee schedule
 * falls back to the first printer/material or "none", and hardware rows whose catalog entry is gone are
 * removed. Returns one note per fix, prefixed with the part's label when there are several parts. */
export function reconcileWithConfig(cfg: EffectiveConfig): string[] {
  const notes: string[] = [];
  const multi = quote.parts.length > 1;
  quote.parts.forEach((part, i) => {
    const prefix = multi ? `${partLabel(part, i)}: ` : "";
    if (!cfg.printers.some((pr) => pr.id === part.printerId)) {
      const missing = part.printerId;
      applyPrinter(cfg, part, cfg.printers[0]?.id ?? "");
      notes.push(`${prefix}Printer "${missing}" no longer exists, so the first printer was selected instead.`);
    }
    if (!cfg.materials.some((m) => m.id === part.materialId)) {
      const missing = part.materialId;
      applyMaterial(cfg, part, cfg.materials[0]?.id ?? "");
      notes.push(`${prefix}Material "${missing}" no longer exists, so the first material was selected instead.`);
    }
    const { kept, missing } = pruneMissingHardware(part.hardware, cfg.hardware);
    if (missing.length > 0) {
      part.hardware = kept;
      const names = missing.map((h) => `"${h.name}"`).join(", ");
      notes.push(`${prefix}Hardware ${names} no longer exists, so it was removed from this quote.`);
    }
  });
  const order = quote.order;
  if (order.feeScheduleName !== "none" && !(order.feeScheduleName in cfg.feeSchedules)) {
    const missing = order.feeScheduleName;
    order.feeScheduleName = "none";
    notes.push(`Fee schedule "${missing}" no longer exists, so "None" was selected instead.`);
  }
  return notes;
}

export function engineOptions(cfg: EffectiveConfig): EngineOptions {
  return { ...defaultEngineOptions, laborPerUnit: cfg.pricing.laborPerUnit };
}

/** The engine options for the quote being edited: the stored ones while a reopened quote's rates are
 * pinned, else the shop's current ones. */
export function quoteEngineOptions(cfg: EffectiveConfig): EngineOptions {
  const pin = activeEnergyPin();
  return pin ? (structuredClone($state.snapshot(pin.options)) as EngineOptions) : engineOptions(cfg);
}

export interface PrintJob {
  mode: "client" | "internal";
  meta: { customer: string; project: string; reference: string };
  order: OrderBreakdown;
}

/** The client linked to the quote being edited (id + name), shared by HistoryPanel's save-row picker and
 * QuoteTab's print toolbar (linked name vs. free-text walk-in field). Its fields are mutated, never
 * reassigned. Both live here so QuoteTab can show the name without loading the client list. Empty id =
 * none. HistoryPanel's restore() seeds it; save() and update() read it back. */
export const quoteClient: { id: string; name: string } = $state({ id: "", name: "" });

/** Falls back to the id when a client's name is blank, so a linked client never renders as blank text
 * (QuoteTab's effectiveCustomer needs this; HistoryPanel's picker shows quoteClient.name directly). */
export function nameOrId(name: string, id: string): string {
  return name || id;
}

/** Project and quote-reference text for the quote being edited, shared like quoteClient: QuoteTab's print
 * toolbar binds to it, HistoryPanel's save()/update() persist it and restore() seeds it. A quote saved
 * without them restores to empty strings. */
export const quoteMeta: { project: string; reference: string } = $state({ project: "", reference: "" });

/** The app's config as last loaded from /api/config. Boxed so an embedded SettingsEditor can refresh the
 * whole app's view after a save without prop-drilling a callback through every tab. */
export const appConfig: { current: EffectiveConfig | null } = $state({ current: null });

/** The single choke point for currency: format.ts's money helpers read `cfg.currency`, and every component
 * gets `cfg` from App.svelte's derived config, so overriding here applies the demo-local currency
 * (demoLocal.svelte.ts) everywhere. The override needs both demoMode === true (server-asserted) and a
 * chosen preset; otherwise `cfg` passes through with the same identity. Cosmetic only (symbol/decimals, no
 * conversion), so a shallow copy replacing `currency` suffices. */
export function effectiveConfig(cfg: EffectiveConfig | null): EffectiveConfig | null {
  if (!cfg || cfg.demoMode !== true) return cfg;
  const preset = resolveCurrencyPreset(demoLocal.currency);
  if (!preset) return cfg;
  return { ...cfg, currency: preset };
}

/** A print request from QuoteTab, rendered by PrintView at the App root (outside header/main so the print
 * stylesheet can hide chrome). null = none in flight. Boxed because an exported `let` can't be reassigned
 * from an importing module. */
export const printJob: { current: PrintJob | null } = $state({ current: null });

/** An invoice print request from InvoicesTab, rendered by InvoicePrintView at the App root like `printJob`.
 * The two are mutually exclusive (App clears both on afterprint; each path nulls the other), so one
 * #print-view mounts at a time. */
export const invoicePrintJob: { current: Invoice | null } = $state({ current: null });

/** One-shot cross-tab handoff: a caller (deep link, "Convert to invoice", the Quotes ledger's "Open in
 * editor", ...) sets `pendingOpen.current = { tab, id }` before navigating; the destination tab calls
 * consumePendingOpen(tab) on mount. One mechanism for every destination: a new destination needs a new
 * `tab` id, not a new box. */
export const pendingOpen: { current: { tab: string; id: string } | null } = $state({ current: null });

/** Reads and clears pendingOpen if addressed to `tab`; otherwise leaves it untouched and returns null so
 * another tab's handoff isn't consumed. */
export function consumePendingOpen(tab: string): string | null {
  if (pendingOpen.current?.tab !== tab) return null;
  const id = pendingOpen.current.id;
  pendingOpen.current = null;
  return id;
}

/** The quote tab's save actions, registered by HistoryPanel on mount and cleared on unmount. App's
 * Ctrl+S/Cmd+S handler calls `save` so the shortcut shares the loaded-quote logic and disabled guard; the
 * editor's top controls call `update`/`saveAsNew`/`reload`/`overwrite`. All null while no HistoryPanel is
 * mounted, when the shortcut is a no-op beyond preventDefault. */
export const quoteActions: {
  save: (() => void) | null;
  update: (() => void) | null;
  saveAsNew: (() => void) | null;
  reload: (() => void) | null;
  overwrite: (() => void) | null;
  /** True while a save is running or the quote can't be saved yet; mirrors the save row's disabled state. */
  busy: boolean;
  blocked: boolean;
} = $state({ save: null, update: null, saveAsNew: null, reload: null, overwrite: null, busy: false, blocked: false });

/** What the editor knows about the saved quote it is bound to, plus its save feedback. One box, shared by
 * the heading, the sidebar highlight, the top save controls and HistoryPanel's save row, none of which can
 * reach HistoryPanel's local state. `loadedId` null = a new, unsaved quote. */
export const quoteSession: {
  loadedId: string | null;
  /** The loaded quote's savedAt as last read or written; sent as If-Match so a stale save gets a 409. */
  version: string;
  /** The Label field's text; shown in the heading while a quote is loaded. */
  label: string;
  pin: EnergyPin | null;
  /** Set when the last save was refused because someone else saved first. */
  conflict: boolean;
  status: { text: string; failed: boolean };
  /** How many saved quotes exist, or null until the saved-quote list has loaded. */
  savedCount: number | null;
  /** JSON of the editor as of the last clean point (open, save, reset); null before the first capture. */
  baseline: string | null;
} = $state({ loadedId: null, version: "", label: "", pin: null, conflict: false, status: { text: "", failed: false }, savedCount: null, baseline: null });

/** The stored rates of a reopened saved quote. Its price must match the list, so the editor sends these
 * (the energy block of each part, and the engine options) instead of the shop's current rates, until the
 * user edits an energy field or picks "Use current rates". */
export interface EnergyPin {
  /** Each part's stored energy inputs, by part index. */
  energy: EnergyInputs[];
  options: EngineOptions;
  /** The energy-related form values at restore; the pin lapses as soon as any of them changes. */
  watch: string;
}

function energyWatch(src: { parts: PartSource[]; order: OrderForm }): string {
  return JSON.stringify([src.order.printStart, src.parts.map((p) => [p.flatRate, p.offPeakFrac, p.printStart])]);
}

/** Pins the current form to a saved quote's stored rates. A part-count mismatch (an unusable stored bag)
 * pins nothing. */
export function pinStoredRates(stored: { inputs?: OrderInputs; options?: EngineOptions } | null): void {
  const energy = stored?.inputs?.parts?.map((p) => p.energy);
  if (!stored || !energy || !stored.options || energy.length !== quote.parts.length || energy.some((e) => !e)) {
    quoteSession.pin = null;
    return;
  }
  quoteSession.pin = { energy: structuredClone(energy) as EnergyInputs[], options: structuredClone(stored.options), watch: energyWatch(quote) };
}

/** The pin, or null once it has lapsed (the user edited a flat rate, off-peak share or print start, or the
 * part count changed). */
export function activeEnergyPin(): EnergyPin | null {
  const pin = quoteSession.pin;
  if (!pin || pin.energy.length !== quote.parts.length || pin.watch !== energyWatch(quote)) return null;
  return pin;
}

/** Key-sorted JSON, so two energy blocks compare by content, not property order. */
function stableJson(v: unknown): string {
  return JSON.stringify(v, (_k, val) =>
    val && typeof val === "object" && !Array.isArray(val)
      ? Object.fromEntries(Object.entries(val as Record<string, unknown>).sort(([a], [b]) => (a < b ? -1 : 1)))
      : val,
  );
}

/** True while the editor prices from stored rates that differ from what the shop's settings would give
 * now: the cue for the "Shop electricity rates changed" note. */
export function storedRatesDiffer(cfg: EffectiveConfig): boolean {
  const pin = activeEnergyPin();
  if (!pin) return false;
  const { order } = quote;
  const current = quote.parts.map((p, i) => toPartInputs(cfg, p, i, order.sellingEnabled, order.printStart, null));
  return (
    pin.energy.some((e, i) => stableJson(e) !== stableJson(current[i]?.energy)) ||
    stableJson(pin.options) !== stableJson(engineOptions(cfg))
  );
}

/** Drops the pin so the quote reprices at the shop's current rates. */
export function useCurrentRates(): void {
  quoteSession.pin = null;
}

function quoteSnapshot(): string {
  return JSON.stringify({
    form: toFormState(),
    clientId: quoteClient.id,
    clientName: quoteClient.name,
    project: quoteMeta.project,
    reference: quoteMeta.reference,
    label: quoteSession.label,
    pinned: activeEnergyPin() !== null,
  });
}

/** Marks the editor clean: the point later edits are measured from. */
export function captureQuoteBaseline(): void {
  quoteSession.baseline = quoteSnapshot();
}

/** True once the editor differs from its last clean point. Reactive, so it can feed a $derived. */
export function quoteIsDirty(): boolean {
  return quoteSession.baseline !== null && quoteSnapshot() !== quoteSession.baseline;
}

/** The URL for the quote being edited: `#/quote/<id>` when a saved quote is open, `#/quote` when new.
 * Rewrites the address in place (no history entry) and only while the address is on the editor. */
export function syncQuoteUrl(): void {
  if (typeof location === "undefined" || !/^#\/quote(\/|$)/.test(location.hash)) return;
  const want = quoteSession.loadedId ? `#/quote/${encodeURIComponent(quoteSession.loadedId)}` : "#/quote";
  if (location.hash !== want) history.replaceState(null, "", want);
}

// Reload and tab close discard the draft, which lives only in memory. beforeunload only prompts when the
// page has been interacted with, which is the case that matters.
if (typeof window !== "undefined") {
  window.addEventListener("beforeunload", (e) => {
    if (!quoteIsDirty()) return;
    e.preventDefault();
    e.returnValue = "";
  });
}
