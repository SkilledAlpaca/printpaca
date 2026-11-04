<script module lang="ts">
  import type { GcodeResult, RawGcodeMeta } from "../lib/engine/index";

  // This page's own status, not GcodeResult["status"]: resolveGcode reports "ok" for a weight-only result, so
  // "weight parsed, print time missing" is tracked here as "incomplete" to drive the status line's style and
  // keep it out of the green success state.
  export type GcodeStatus = "idle" | "ok" | "not-found" | "invalid-profile" | "incomplete" | "error";

  /** Maps a GcodeResult to this page's status, downgrading "ok" to "incomplete" when print time is missing.
   * weightG is defined whenever this fires (resolveGcode returns "ok" without weight only for time-only
   * results, which describeGcode messages separately). */
  export function gcodeStatusFor(result: GcodeResult): GcodeStatus {
    if (result.status === "ok" && result.weightG !== undefined && result.timeSeconds === undefined) return "incomplete";
    return result.status;
  }

  /** Message text for the drop zone's status line. Local rather than gcodeMessage.ts's describeGcodeResult
   * (built for the admin form's always-visible fields) so the wording can point at the inline print-time
   * inputs below. */
  /** The intro shown when the shop wrote none. "Estimate", not "instant estimate": an unsliced file gets no
   * number until the shop has its print time. */
  export const DEFAULT_INTRO_TEXT = "Order a ready-made 3D print, or upload your own 3D model for a price estimate.";

  export function describeGcode(result: GcodeResult, fname: string): string {
    if (result.status === "not-found") {
      return `No slicer metadata found in ${fname}. Try re-slicing with metadata comments enabled.`;
    }
    if (result.status === "invalid-profile") {
      return `Filament length found in ${fname}, but weight couldn't be derived. Try a slicer that reports weight directly.`;
    }
    if (result.weightG === undefined) {
      return `${fname}: print time found, but no weight data. Try a slicer that reports filament weight.`;
    }
    if (result.timeSeconds === undefined) {
      return `${fname}: weight found, but no print time in the file. Enter the print time below, or remove the file.`;
    }
    return `${fname}: parsed.`;
  }
</script>

<script lang="ts">
  import { onDestroy, onMount, tick } from "svelte";
  import { announce, focusAfterUpdate } from "../lib/a11y.svelte";
  import { clearVisitor, visitor } from "./visitor.svelte";
  import {
    estimateWeightFromVolume,
    formatMoney,
    parseGcodeStream,
    parseStl,
    parseThreeMf,
    resolveGcode,
    clampSolidityPct,
  } from "../lib/engine/index";
  import {
    classifyFileKind,
    describeFileStoredOutcome,
    describeModelAttachment,
    describeModelParseFailure,
    describeModelTooLarge,
    MAX_MODEL_BYTES,
    MULTIPART_OVERHEAD_BYTES,
  } from "../lib/gcodeMessage";
  import { densityOrFallback } from "../lib/filamentDefaults";
  import { splitSeconds } from "../lib/gcodeTime";
  import { TriangleCollector, type Triangle } from "../lib/modelPreview";
  import { createQuotePreview, type QuotePreviewState } from "../lib/quotePreview";
  import {
    EMAIL_MAX_BYTES,
    NAME_MAX_BYTES,
    NOTE_MAX_BYTES,
    noteByteBudget,
    utf8ByteLength,
    isPlausibleEmail,
    EMAIL_FORMAT_ERROR,
  } from "../lib/publicFormLimits";
  import ItemGallery from "./ItemGallery.svelte";
  import PublicFooter from "./PublicFooter.svelte";
  import projectLogoUrl from "../assets/alpaca-logo.svg";
  import ItemOrderPanel, { type ItemOrderConfirmation } from "./ItemOrderPanel.svelte";
  import ModelPreview from "./ModelPreview.svelte";
  import { clearConfirmation, loadConfirmation, saveConfirmation } from "./confirmationStorage";
  import { BoundsCollector, boundsWarning, formatBounds, type ModelBounds } from "./modelBounds";
  import { materialDescription } from "./materialInfo";
  import {
    DEFAULT_MAX_ORDER_QTY,
    isValidQuantity,
    normalizedQuantityText,
    overCapMessage,
    perUnitCents,
  } from "./publicPricing";
  import FormErrorSummary from "./FormErrorSummary.svelte";
  import RateLimitNotice from "./RateLimitNotice.svelte";
  import { parseRoute, routeHash, type Route } from "./route";
  import {
    fetchQuotePreview,
    loadPublicConfig,
    loadPublicItems,
    submitQuoteRequest,
    type PublicConfig,
    type PublicItem,
    type QuotePreviewResult,
  } from "./api";

  // Post-submit confirmations survive a reload (sessionStorage, see confirmationStorage.ts) until dismissed.
  // Two flows, two keys.
  const QUOTE_CONFIRMATION_KEY = "quote";
  const ITEM_ORDER_CONFIRMATION_KEY = "item-order";

  /** What the estimate confirmation shows, self-contained so it renders after a reload. Fields added after
   * the first release are optional: an older saved confirmation still renders. */
  interface QuoteConfirmation {
    id: string;
    reference?: string;
    priceCents: number;
    /** True when no print time was sent: the price is "to be confirmed" and priceCents is 0. */
    needsPrintTime?: boolean;
    material: string;
    quantity: number;
    email: string;
    fileName?: string;
    printTimeLabel?: string;
    fileStoredCaveat: string | null;
  }

  // Config load

  let phase = $state<"loading" | "failed" | "ready">("loading");
  let cfg = $state.raw<PublicConfig | null>(null);

  // Public items load alongside config. A failed or empty items fetch is not an error: it just means no
  // gallery. Only a failed config fetch (phase "failed") is a real failure.
  let items = $state.raw<PublicItem[] | null>(null);
  // Restored from sessionStorage on mount: a self-contained summary of the last gallery order, independent of
  // the live `items` (reset on load). Shown on the #/order/received route; dismissed via
  // dismissItemOrderConfirmation.
  let itemOrderConfirmation = $state<ItemOrderConfirmation | null>(null);

  // Routing. The location hash is the source of truth for which view shows (see route.ts), so Back, reload and
  // a shared link all work. Navigation pushes a history entry; a redirect replaces the current one.
  let route = $state<Route>(parseRoute(location.hash));
  // A short explanation after a route couldn't be shown ("That item is no longer available."), cleared by the
  // next navigation.
  let routeNote = $state("");
  function syncRoute() {
    route = parseRoute(location.hash);
  }
  function navigate(next: Route, opts: { replace?: boolean; keepNote?: boolean } = {}) {
    if (!opts.keepNote) routeNote = "";
    const hash = routeHash(next);
    if (opts.replace) history.replaceState(null, "", hash);
    else if (location.hash !== hash) location.hash = hash;
    route = next;
  }

  const hasGallery = $derived(items !== null && items.length > 0);
  const orderItem = $derived.by(() => {
    const r = route;
    return r.name === "item" && items ? (items.find((i) => i.id === r.id) ?? null) : null;
  });
  // "gallery" is the item grid, an item panel or the order confirmation; "quote" is the estimate form or its
  // confirmation. A shop with no items has only the estimate form, whatever the hash says.
  const view = $derived.by<"gallery" | "quote">(() => {
    if (route.name === "estimate" || route.name === "estimate-received") return "quote";
    if (route.name === "order-received" && itemOrderConfirmation) return "gallery";
    return hasGallery ? "gallery" : "quote";
  });

  onMount(async () => {
    itemOrderConfirmation = loadConfirmation<ItemOrderConfirmation>(ITEM_ORDER_CONFIRMATION_KEY);
    const restoredQuote = loadConfirmation<QuoteConfirmation>(QUOTE_CONFIRMATION_KEY);
    if (restoredQuote) {
      submitted = restoredQuote;
      fileStoredCaveat = restoredQuote.fileStoredCaveat;
    }

    const [cfgResult, itemsResult] = await Promise.all([loadPublicConfig(), loadPublicItems()]);
    if (cfgResult) {
      cfg = cfgResult;
      phase = "ready";
    } else {
      phase = "failed";
    }
    items = itemsResult;
  });

  // A route whose data is gone returns to the gallery with a note, replacing the entry so Back doesn't land on
  // the dead route again.
  $effect(() => {
    if (phase !== "ready") return;
    let note = "";
    const r = route;
    if (r.name === "order-received" && !itemOrderConfirmation) note = "That order confirmation is no longer available.";
    else if (r.name === "estimate-received" && !submitted) note = "That estimate confirmation is no longer available.";
    else if (r.name === "item" && items !== null && !items.some((i) => i.id === r.id)) note = "That item is no longer available.";
    if (note) {
      routeNote = note;
      navigate({ name: "gallery" }, { replace: true, keepNote: true });
    }
  });

  /** Dismisses a gallery item-order confirmation and returns to the item grid. */
  function dismissItemOrderConfirmation() {
    itemOrderConfirmation = null;
    clearConfirmation(ITEM_ORDER_CONFIRMATION_KEY);
    // Replace, so Back doesn't return to a confirmation that no longer exists.
    navigate({ name: "gallery" }, { replace: true });
    // The Back button that held focus is about to unmount (WCAG 2.4.3).
    void focusAfterUpdate("#gallery-heading");
  }

  // Focus management for the gallery <-> item panel swap (WCAG 2.4.3): opening a card focuses the panel's
  // heading (ItemOrderPanel does that itself on mount); going back refocuses the card that was opened, or the
  // gallery heading when that card is gone.
  let lastSelectedId: string | null = null;
  let itemOpenedInApp = false;
  function openItem(item: PublicItem) {
    lastSelectedId = item.id;
    itemOpenedInApp = true;
    navigate({ name: "item", id: item.id });
  }
  function closeItem() {
    if (itemOpenedInApp) {
      // The item was opened from the gallery in this tab, so the previous history entry is the gallery.
      itemOpenedInApp = false;
      history.back();
    } else {
      navigate({ name: "gallery" }, { replace: true });
    }
  }
  // Returning from an item to the gallery by any means (Back to gallery, Escape, the browser's Back button)
  // refocuses the card that was open.
  let previousRouteName: Route["name"] = "gallery";
  $effect(() => {
    const name = route.name;
    const wasItem = previousRouteName === "item";
    previousRouteName = name;
    if (name !== "gallery" || !wasItem || phase !== "ready") return;
    const id = lastSelectedId;
    void focusAfterUpdate(() => {
      const card = id === null ? null : document.querySelector<HTMLElement>(`[data-item-id="${CSS.escape(id)}"]`);
      return card ?? document.getElementById("gallery-heading");
    });
  });

  // Confirmation headings take focus after a successful submit: the form (and the focused submit button) is
  // replaced, so focus would otherwise drop to <body> and nothing would be announced (WCAG 2.4.3, 4.1.3).
  let quoteConfirmationHeading: HTMLElement | undefined = $state();
  let itemConfirmationHeading: HTMLElement | undefined = $state();
  let estimateFormHeading: HTMLElement | undefined = $state();
  let formEl: HTMLFormElement | undefined = $state();
  let fileInputEl: HTMLInputElement | undefined = $state();

  // Mirror server/public.go's bounds on weightG/timeSeconds so a manual entry the server would reject is
  // flagged first.
  const MAX_WEIGHT_G = 100000;
  const MAX_TIME_SECONDS = 30 * 24 * 3600;
  // The per-order cap is shop.maxOrderQty, served with the public config.
  const maxQty = $derived(cfg?.maxOrderQty ?? DEFAULT_MAX_ORDER_QTY);
  // The Solidity hint says "most prints are 15 to 25%", so the default sits inside that range. (The admin
  // quote form keeps the engine's own default.)
  const PUBLIC_DEFAULT_SOLIDITY_PCT = 20;

  // Form state

  let materialName = $state("");
  // Falls back to PLA's density while no material is chosen, so an early file drop still shows an estimate.
  const density = $derived(densityOrFallback(cfg?.materials.find((m) => m.name === materialName)?.density));
  let quantity = $state<number | null>(1);
  let note = $state("");
  // Name and email live in the shared visitor store (see visitor.svelte.ts) so they carry over to an item order.
  const name = $derived(visitor.name);
  const email = $derived(visitor.email);
  // A quantity above the cap stays as typed: this message replaces the total and the submit is blocked.
  const quantityCapMessage = $derived(overCapMessage(quantity, maxQty));

  let fileName = $state("");
  let file = $state<File | null>(null);
  let busy = $state(false);
  let dragging = $state(false);
  let weightG = $state<number | null>(null);
  // True once the visitor typed their own weight over a geometry estimate; the estimate then stops
  // overwriting it until "Use the estimate" resets this.
  let weightEdited = $state(false);
  // Set only when the current parse's weight was derived from filament length (not slicer-reported), so a
  // material change re-derives it; null otherwise, which leaves weightG alone.
  let lengthMeta = $state.raw<RawGcodeMeta | null>(null);
  // The print time a slicer file reported, or null. The effective time (timeSeconds) is this when present,
  // else the hours and minutes the visitor typed, so replacing the file never discards typed values.
  let parsedTimeSeconds = $state<number | null>(null);
  let gcodeStatus = $state<GcodeStatus>("idle");
  let gcodeMessage = $state("");

  // Set for every STL/3MF drop that parses, whether or not its File is attached (an oversized one keeps its
  // estimate). "sliced" fills weight and time like a g-code file; "estimate" fills weight only, time stays
  // manual. hasExactParse uses this rather than `file`, since a geometry estimate can also carry a File.
  type ModelSource = { fileName: string; kind: "sliced" } | { fileName: string; kind: "estimate"; volumeCm3: number };
  let modelSource = $state<ModelSource | null>(null);
  // True when weightG should come from the parse itself (a real g-code file or sliced 3MF) rather than a
  // geometry-only estimate that still needs a manual time. Not `file !== null`: modelSource === null marks a
  // plain g-code drop (STL/3MF drops always set modelSource). It does not guarantee timeSeconds is filled;
  // the template shows manual print-time inputs whenever timeSeconds is null.
  const hasExactParse = $derived((file !== null && modelSource === null) || modelSource?.kind === "sliced");
  // A model or slicer file is attached to the request, so a missing print time is something the shop can fill
  // in later. Without any file, the visitor must give the time.
  const hasModelFile = $derived(file !== null || modelSource !== null);
  // Raw box contents (cleared or out-of-range while typing is fine); the estimate reads the clamped
  // solidityPct, so a cleared box never derives 0 g or renders "null%".
  let solidityInput = $state<number | null>(PUBLIC_DEFAULT_SOLIDITY_PCT);
  const solidityValid = $derived(solidityInput !== null && solidityInput >= 5 && solidityInput <= 100);
  const solidityPct = $derived(clampSolidityPct(solidityInput ?? PUBLIC_DEFAULT_SOLIDITY_PCT));
  // Appended to the note at submit time, never shown in the textarea, so the owner knows a model file
  // produced the weight/time. Always describeModelAttachment's third-person ownerText.
  let modelFileNote = $state("");
  // Shown next to the drop-zone status line only when the parsed model's File wasn't kept for upload
  // (describeModelAttachment's visitorText, else ""). It is for the visitor; modelFileNote only reaches the
  // owner's note.
  let modelAttachmentAdvisory = $state("");

  // Decimated triangles from the current STL/3MF drop (sliced too, via threeMf.ts's feedSlicedPreview) for
  // ModelPreview's canvas; empty for g-code or a sliced 3MF without a readable geometry entry. $state.raw
  // because the array is only replaced wholesale. Purely a rendering input: it never changes the estimate,
  // upload or submit.
  let previewTriangles = $state.raw<Triangle[]>([]);
  // The model's extents in mm, read from the same triangle stream; null for g-code or when unreadable.
  let modelBounds = $state.raw<ModelBounds | null>(null);
  const modelBoundsWarning = $derived(modelBounds ? boundsWarning(modelBounds) : null);

  // Manual print time: hours and minutes combine into the same timeSeconds a parse would give.
  let manualHours = $state<number | null>(null);
  let manualMinutes = $state<number | null>(null);
  const manualTimeSeconds = $derived(
    manualHours === null && manualMinutes === null ? null : (manualHours ?? 0) * 3600 + (manualMinutes ?? 0) * 60,
  );
  const timeSeconds = $derived(parsedTimeSeconds ?? manualTimeSeconds);

  // Single source of truth for an "estimate" modelSource's weight: a moved solidity slider or a material
  // change recomputes it, unless the visitor typed their own weight. Safe as an $effect: it writes weightG and
  // gcodeStatus/gcodeMessage, none of which it reads.
  $effect(() => {
    const src = modelSource;
    const pct = solidityPct;
    const d = density;
    if (!src || src.kind !== "estimate" || weightEdited) return;
    weightG = Math.round(estimateWeightFromVolume(src.volumeCm3, d, pct) * 100) / 100;
    gcodeStatus = "ok";
    gcodeMessage = `${src.fileName}: weight estimated from the model's shape.`;
  });

  // Re-derives a length-based parse's weight when the material changes. Safe as an $effect: it writes
  // weightG/gcodeStatus/gcodeMessage and reads none of them back. lengthMeta is only set for exact parses,
  // whose weight has no manual input, so no typed value can be overwritten.
  $effect(() => {
    if (!lengthMeta || !cfg) return;
    const result = resolveGcode(lengthMeta, cfg.filamentDiameterMm, density);
    if (result.weightG !== undefined) weightG = Math.round(result.weightG * 100) / 100;
    gcodeStatus = gcodeStatusFor(result);
    gcodeMessage = describeGcode(result, fileName);
  });

  let submitting = $state(false);
  // The submit is waiting for the price preview that was still updating when the visitor pressed the button.
  let waitingForPrice = $state(false);
  let submitted = $state<QuoteConfirmation | null>(null);
  let rateLimited = $state(false);
  // Errors from the server describe the form as it was sent, so they go away as soon as the visitor changes
  // anything: they show only while serverErrorsFor still matches the current form.
  let serverErrors = $state<string[]>([]);
  let serverErrorsFor = $state("");
  // Set on a successful submit whose response had fileStored: false (storage-full path): the request went
  // through but the file wasn't stored. null otherwise, including manual entries.
  let fileStoredCaveat = $state<string | null>(null);

  // Server-side price preview: the price and shipping come from POST /api/public/quote/preview; the cost
  // model never ships to the visitor's browser. lib/quotePreview.ts owns the debounce (~350ms) and staleness
  // machine; this wiring feeds it inputs and mirrors its state into previewState. A submit with a print time
  // waits for a resolved preview, since the same server prices preview and submission.

  // The extra fields a resolved preview carries and the fetcher-result to state mapping createQuotePreview
  // needs. ok:true with missing priceCents/shippingCents is a failure (fetchQuotePreview already normalizes
  // that; defense in depth).
  type PreviewResolved = { priceCents: number; shippingCents: number };
  function previewToState(
    result: QuotePreviewResult,
  ): ({ status: "resolved" } & PreviewResolved) | { status: "error"; errors: string[] } {
    if (result.ok && typeof result.priceCents === "number" && typeof result.shippingCents === "number") {
      return { status: "resolved", priceCents: result.priceCents, shippingCents: result.shippingCents };
    }
    return { status: "error", errors: result.errors.length > 0 ? result.errors : ["Couldn't estimate this request."] };
  }

  let previewState = $state<QuotePreviewState<PreviewResolved>>({ status: "idle" });
  const preview = createQuotePreview({ fetcher: fetchQuotePreview, toState: previewToState, onChange: (s) => (previewState = s) });
  onDestroy(() => preview.dispose());

  // Mirrors server/public.go's bounds (MAX_WEIGHT_G/MAX_TIME_SECONDS, quantity 1-10000, a chosen material):
  // previews only fire once every field the server requires is in place, so a request it would reject spends
  // no round trip. No print time means no preview: the shop confirms that price, nothing is estimated from volume.
  const previewInput = $derived.by(() => {
    if (materialName === "") return null;
    // Nothing is priced while a field is invalid (solidity, minutes, weight, quantity).
    if (issues.length > 0) return null;
    if (!isValidQuantity(quantity, maxQty)) return null;
    if (weightG === null || weightG < 0 || weightG > MAX_WEIGHT_G) return null;
    if (timeSeconds === null || timeSeconds < 0 || timeSeconds > MAX_TIME_SECONDS) return null;
    return { material: materialName, quantity, weightG, timeSeconds };
  });

  $effect(() => {
    preview.update(previewInput);
  });

  const gcodeIsProblem = $derived(
    gcodeStatus === "not-found" || gcodeStatus === "invalid-profile" || gcodeStatus === "incomplete" || gcodeStatus === "error",
  );

  const printTimeLabel = $derived.by(() => {
    if (timeSeconds === null) return "";
    const t = splitSeconds(timeSeconds);
    return `${t.hours} h ${t.minutes} min`;
  });

  // G-code drop zone: parses with parseGcodeStream/resolveGcode like GcodeDrop.svelte, minus the admin form
  // wiring and stale tracking.

  async function* chunksOf(f: File): AsyncIterable<string> {
    const reader = f.stream().getReader();
    const decoder = new TextDecoder();
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      yield decoder.decode(value, { stream: true });
    }
    yield decoder.decode();
  }

  /** Extension-based, not MIME-based: dropped files often arrive with an empty or generic type for
   * .stl/.3mf. Thin wrapper over gcodeMessage.ts's classifyFileKind. */
  function classifyFile(name: string): "gcode" | "stl" | "3mf" | "unknown" {
    return classifyFileKind(name);
  }

  let seq = 0;
  async function handleFile(f: File) {
    if (!cfg) return;
    const kind = classifyFile(f.name);
    // Checked before touching any form state: an unrelated file type (e.g. a .txt, which drag-and-drop
    // bypasses the picker's accept list for) must not enter the g-code path, give nonsense "try re-slicing"
    // advice, or clear manually entered weight/time.
    if (kind === "unknown") {
      gcodeStatus = "error";
      gcodeMessage = `${f.name}: unrecognized file type. Use a .gcode, .stl, or .3mf file.`;
      return;
    }
    const token = ++seq;
    busy = true;
    fileName = f.name;
    file = null;
    modelSource = null;
    modelFileNote = "";
    modelAttachmentAdvisory = "";
    weightG = null;
    lengthMeta = null;
    parsedTimeSeconds = null;
    weightEdited = false;
    gcodeStatus = "idle";
    gcodeMessage = "";
    // The typed print time, Solidity and weight override survive a file swap (only a file that reports its own
    // values replaces them), so choosing a different model doesn't make the visitor start over.
    previewTriangles = [];
    modelBounds = null;
    // Whatever parses out of this drop is attached for upload only if the File fits the server's real body
    // cap, independent of MAX_MODEL_BYTES' larger parse limit.
    const maxUploadBytes = cfg.maxUploadBytes;
    try {
      if (kind === "gcode") {
        // Checked before parsing so an over-limit file is rejected instantly. Unlike STL/3MF, an oversized
        // g-code drop has no estimate to fall back on, so this blocks. Compared against maxUploadBytes minus
        // MULTIPART_OVERHEAD_BYTES (as describeModelAttachment does) so a file that barely fits alone can't
        // 413 once the other form fields ride along.
        if (f.size > maxUploadBytes - MULTIPART_OVERHEAD_BYTES) {
          gcodeStatus = "error";
          gcodeMessage = `${f.name} is larger than ${Math.round(maxUploadBytes / (1024 * 1024))} MiB, which is more than this page can upload. Try a smaller file, or enter weight and print time manually.`;
          return;
        }
        file = f; // a plain g-code drop always keeps the File reference (modelSource stays null, so hasExactParse reads this as "exact parse")
        const meta = await parseGcodeStream(chunksOf(f));
        if (token !== seq) return; // a newer drop/pick has since started, discard this result
        const result = resolveGcode(meta, cfg.filamentDiameterMm, density);
        if (result.source === "derived-from-length") lengthMeta = meta;
        if (result.weightG !== undefined) weightG = Math.round(result.weightG * 100) / 100;
        if (result.timeSeconds !== undefined) parsedTimeSeconds = Math.round(result.timeSeconds);
        gcodeStatus = gcodeStatusFor(result);
        gcodeMessage = describeGcode(result, f.name);
        return;
      }

      if (f.size > MAX_MODEL_BYTES) {
        gcodeStatus = "error";
        gcodeMessage = describeModelTooLarge(f.name).text;
        return;
      }

      const buffer = await f.arrayBuffer();
      if (token !== seq) return;

      if (kind === "stl") {
        // The sinks collect triangles during the same arrayBuffer pass parseStl already makes (no second
        // read). Cleared at the top of handleFile; read only by ModelPreview and the size line, never feeding
        // the upload or submit.
        const collector = new TriangleCollector();
        const bounds = new BoundsCollector();
        const stl = parseStl(buffer, {
          onTriangle: (...tri) => {
            collector.add(...tri);
            bounds.add(...tri);
          },
        });
        if (stl.status === "ok" && stl.volumeCm3 !== undefined) {
          // weightG/gcodeStatus/gcodeMessage come from the solidity effect above once modelSource is set.
          modelSource = { fileName: f.name, kind: "estimate", volumeCm3: stl.volumeCm3 };
          previewTriangles = collector.result();
          modelBounds = bounds.result();
          const attachment = describeModelAttachment(f.name, f.size, maxUploadBytes, "weight estimated from geometry");
          if (attachment.attach) file = f;
          modelFileNote = attachment.ownerText;
          modelAttachmentAdvisory = attachment.visitorText;
        } else {
          const msg = describeModelParseFailure(f.name, stl.status === "empty" ? "empty" : "malformed");
          gcodeStatus = "not-found";
          gcodeMessage = msg.text;
        }
        return;
      }

      // .3mf: a sliced archive resolves like g-code (exact weight and time); an unsliced one falls back to
      // the STL-style geometry estimate (weight only). The sink fires on both branches (feedSlicedPreview
      // walks the geometry best-effort after a sliced result), but weightG/timeSeconds come only from
      // tmf.meta, so a missing, corrupt or oversized geometry entry degrades to "no preview", not a worse
      // quote.
      const collector = new TriangleCollector();
      const bounds = new BoundsCollector();
      const tmf = await parseThreeMf(buffer, {
        onTriangle: (...tri) => {
          collector.add(...tri);
          bounds.add(...tri);
        },
      });
      if (token !== seq) return;
      if (tmf.status === "ok" && tmf.kind === "sliced" && tmf.meta) {
        const result = resolveGcode(tmf.meta, cfg.filamentDiameterMm, density);
        if (result.source === "derived-from-length") lengthMeta = tmf.meta;
        if (result.weightG !== undefined) weightG = Math.round(result.weightG * 100) / 100;
        if (result.timeSeconds !== undefined) parsedTimeSeconds = Math.round(result.timeSeconds);
        modelSource = { fileName: f.name, kind: "sliced" };
        previewTriangles = collector.result();
        modelBounds = bounds.result();
        gcodeStatus = gcodeStatusFor(result);
        gcodeMessage = describeGcode(result, f.name);
        const attachment = describeModelAttachment(f.name, f.size, maxUploadBytes, "sliced 3MF, weight/time from the slicer's own prediction");
        if (attachment.attach) file = f;
        modelFileNote = attachment.ownerText;
        modelAttachmentAdvisory = attachment.visitorText;
      } else if (tmf.status === "ok" && tmf.kind === "geometry" && tmf.volumeCm3 !== undefined) {
        modelSource = { fileName: f.name, kind: "estimate", volumeCm3: tmf.volumeCm3 };
        previewTriangles = collector.result();
        modelBounds = bounds.result();
        const attachment = describeModelAttachment(f.name, f.size, maxUploadBytes, "weight estimated from geometry");
        if (attachment.attach) file = f;
        modelFileNote = attachment.ownerText;
        modelAttachmentAdvisory = attachment.visitorText;
      } else {
        const msg = describeModelParseFailure(
          f.name,
          tmf.status === "empty" ? "empty" : tmf.status === "too-large" ? "too-large" : "malformed",
        );
        gcodeStatus = "not-found";
        gcodeMessage = msg.text;
      }
    } catch (err) {
      if (token !== seq) return;
      gcodeStatus = "error";
      gcodeMessage = `Couldn't read ${f.name}: ${err instanceof Error ? err.message : String(err)}. Try a different file.`;
    } finally {
      if (token === seq) busy = false;
    }
  }

  /** Drops the current file/estimate and returns to manual entry, for a visitor who changed their mind or
   * whose file failed to parse (not-found, invalid-profile, error). */
  function clearFile() {
    seq++; // discard any parse still in flight for the file being cleared
    file = null;
    modelSource = null;
    modelFileNote = "";
    modelAttachmentAdvisory = "";
    solidityInput = PUBLIC_DEFAULT_SOLIDITY_PCT;
    previewTriangles = [];
    modelBounds = null;
    fileName = "";
    weightG = null;
    weightEdited = false;
    lengthMeta = null;
    parsedTimeSeconds = null;
    manualHours = null;
    manualMinutes = null;
    gcodeStatus = "idle";
    gcodeMessage = "";
    busy = false;
  }

  function onDrop(e: DragEvent) {
    e.preventDefault();
    dragging = false;
    const f = e.dataTransfer?.files?.[0];
    if (f) void handleFile(f);
  }
  function onDragLeave(e: DragEvent) {
    if (e.currentTarget instanceof Node && e.currentTarget.contains(e.relatedTarget as Node | null)) return;
    dragging = false;
  }
  function onPick(e: Event) {
    const input = e.currentTarget as HTMLInputElement;
    const f = input.files?.[0];
    input.value = ""; // allow re-picking the same file to re-parse it
    if (f) void handleFile(f);
  }
  function preventWindowDrop(e: DragEvent) {
    e.preventDefault();
  }

  // Client-side checks mirroring server/public.go's validation limits, so a 422 round trip is the rare case.

  // Folds any model-file note into the note at submit time so the owner knows where the numbers came from;
  // the textarea keeps only what the visitor typed.
  const combinedNote = $derived([note.trim(), modelFileNote].filter((s) => s.length > 0).join("\n\n"));

  // The textarea's maxlength=2000 counts UTF-16 units and is only a rough guard; modelFileNote is folded in
  // at submit, so a full box could exceed the server's BYTE cap with no visible cause. Budget the visible
  // note against NOTE_MAX_BYTES minus the pending model note's bytes (noteByteBudget) so the error names the
  // actual reason.
  const noteBudget = $derived(noteByteBudget(modelFileNote));

  /** The form fields an issue can point at; each has an input the submit handler can mark and focus. */
  type IssueField = "material" | "quantity" | "weight" | "hours" | "minutes" | "solidity" | "file";
  interface Issue {
    field: IssueField;
    message: string;
  }
  const ISSUE_TARGET: Record<IssueField, string> = {
    material: "quote-material",
    quantity: "quote-qty",
    weight: "quote-weight",
    hours: "quote-hours",
    minutes: "quote-minutes",
    solidity: "quote-solidity",
    file: "choose-file",
  };

  // Everything that blocks pricing or submitting, in the order the fields appear. Computed live, so the price
  // panel can name the actual blocker; the messages show as errors only after the first submit attempt.
  const issues = $derived.by<Issue[]>(() => {
    const out: Issue[] = [];
    if (materialName === "") out.push({ field: "material", message: "Choose a material." });
    if (!isValidQuantity(quantity, maxQty)) {
      out.push({
        field: "quantity",
        message: quantityCapMessage !== "" ? quantityCapMessage : `Quantity must be a whole number from 1 to ${maxQty.toLocaleString("en-US")}.`,
      });
    }
    // A parsed g-code or sliced 3MF supplies the weight (and usually the time); an STL or geometry-3MF
    // estimate supplies a weight the visitor can override. A model file without a print time is allowed: the
    // shop confirms that price.
    if (weightG === null) {
      out.push(
        hasExactParse
          ? { field: "file", message: "This file doesn't include a weight. Try another file, or remove it and enter the weight yourself." }
          : { field: "weight", message: "Enter the model weight, or upload a 3D file." },
      );
    } else if (weightG < 0 || weightG > MAX_WEIGHT_G) {
      out.push({
        field: hasExactParse ? "file" : "weight",
        message:
          modelSource?.kind === "estimate" && !weightEdited
            ? `The estimated weight is over ${MAX_WEIGHT_G.toLocaleString("en-US")} g. Check the model's size, or type your own weight.`
            : `Weight must be from 0 to ${MAX_WEIGHT_G.toLocaleString("en-US")} g.`,
      });
    }
    if (modelSource?.kind === "estimate" && !solidityValid) {
      out.push({ field: "solidity", message: "Solidity must be from 5 to 100%." });
    }
    if (parsedTimeSeconds === null) {
      if (manualTimeSeconds === null) {
        if (!hasModelFile) out.push({ field: "hours", message: "Enter the print time, or upload a sliced file." });
      } else if (manualHours !== null && (manualHours < 0 || !Number.isFinite(manualHours))) {
        out.push({ field: "hours", message: "Hours must be 0 or more." });
      } else if (manualMinutes !== null && (manualMinutes < 0 || manualMinutes > 59)) {
        out.push({ field: "minutes", message: "Minutes must be from 0 to 59." });
      } else if (manualTimeSeconds > MAX_TIME_SECONDS) {
        out.push({ field: "hours", message: "Print time must be 30 days or less." });
      }
    }
    return out;
  });
  const issueFields = $derived(new Set(issues.map((i) => i.field)));

  // Errors for the three text fields are shown inline (aria-invalid + aria-describedby, WCAG 3.3.1); the rest
  // go to the summary list.
  const fieldErrors = $derived.by(() => {
    const out: { name?: string; email?: string; note?: string } = {};
    // Measured in bytes (utf8ByteLength) to match server/public.go's len() caps; a .length check lets
    // multi-byte text pass and 422 server-side. The copy names the limit as characters, never "bytes".
    if (name.trim() === "") out.name = "Name is required.";
    else if (utf8ByteLength(name) > NAME_MAX_BYTES) out.name = `Name must be ${NAME_MAX_BYTES} characters or fewer.`;
    if (email.trim() === "") out.email = "Email is required.";
    else if (utf8ByteLength(email) > EMAIL_MAX_BYTES) out.email = `Email must be ${EMAIL_MAX_BYTES} characters or fewer.`;
    else if (!isPlausibleEmail(email.trim())) out.email = EMAIL_FORMAT_ERROR;
    if (utf8ByteLength(note.trim()) > noteBudget) {
      out.note =
        modelFileNote.length > 0
          ? `Note is too long. Attaching a model file uses part of the limit; shorten your note to about ${noteBudget} characters.`
          : `Note is too long. Keep it under ${NOTE_MAX_BYTES} characters.`;
    }
    return out;
  });
  const formErrorCount = $derived(
    (fieldErrors.name ? 1 : 0) + (fieldErrors.email ? 1 : 0) + (fieldErrors.note ? 1 : 0) + issues.length,
  );
  // Flipped by the first submit attempt, so a visitor isn't told "Name is required." before they've tried.
  let showErrors = $state(false);
  const nameError = $derived(showErrors ? fieldErrors.name : undefined);
  const emailError = $derived(showErrors ? fieldErrors.email : undefined);
  const noteError = $derived(showErrors ? fieldErrors.note : undefined);
  /** True when `field` has an issue the visitor should now see (after the first submit attempt). */
  const marked = (field: IssueField): boolean => showErrors && issueFields.has(field);
  /** The message to show under `field` once the first submit was attempted. Each error appears here and
   * nowhere else; the line above the button only counts them and links to the first. */
  const issueMessage = (field: IssueField): string => (showErrors ? (issues.find((i) => i.field === field)?.message ?? "") : "");
  // Hours and minutes share one message line under the pair.
  const timeIssueMessage = $derived(issueMessage("hours") || issueMessage("minutes"));
  const FIELD_LABEL: Record<IssueField, string> = {
    material: "Material",
    quantity: "Quantity",
    weight: "Model weight",
    hours: "Print time",
    minutes: "Print time",
    solidity: "Solidity",
    file: "Model file",
  };
  const summaryProblems = $derived.by(() => {
    if (!showErrors) return [];
    const out: { id: string; label: string }[] = [];
    for (const i of issues) {
      const id = ISSUE_TARGET[i.field];
      if (!out.some((p) => p.id === id || (p.label === FIELD_LABEL[i.field]))) out.push({ id, label: FIELD_LABEL[i.field] });
    }
    if (fieldErrors.name) out.push({ id: "quote-name", label: "Name" });
    if (fieldErrors.email) out.push({ id: "quote-email", label: "Email" });
    if (fieldErrors.note) out.push({ id: "quote-note", label: "Note" });
    return out;
  });
  // The text under the Quantity field: the cap message live, or the validation message after a submit attempt.
  const quantityMessage = $derived(quantityCapMessage !== "" ? quantityCapMessage : issueMessage("quantity"));
  // Whether "How we estimated this" is open; a blocked submit opens it when the first problem is inside.
  let howOpen = $state(false);
  const formSnapshot = $derived(
    JSON.stringify([materialName, quantity, weightG, timeSeconds, visitor.name, visitor.email, note, fileName]),
  );
  const visibleServerErrors = $derived(serverErrorsFor === formSnapshot ? serverErrors : []);
  const visibleRateLimited = $derived(rateLimited && serverErrorsFor === formSnapshot);

  // What the price panel says while there is no price: the first thing standing in the way, or the promise that
  // the shop will price a file that has no print time.
  // "Estimated price" only when a price can be computed; otherwise it would promise a number that isn't there.
  const priceHeading = $derived(previewInput !== null ? "Estimated price" : "Price");
  const priceWaitingOnShop = $derived(issues.length === 0 && timeSeconds === null && hasModelFile);

  /** Moves focus to the field of the first issue, or to the first invalid text field. */
  function focusFirstProblem() {
    const first = issues[0];
    const target = first ? document.getElementById(ISSUE_TARGET[first.field]) : null;
    (target ?? formEl?.querySelector<HTMLElement>("[aria-invalid='true']"))?.focus();
  }

  async function submit() {
    // aria-disabled (not disabled) while in flight, so the focused button keeps focus (WCAG 2.4.3).
    if (submitting) return;
    serverErrors = [];
    rateLimited = false;
    // Validation runs first, even while the price is still resolving, so name and email problems surface
    // right away (WCAG 3.3.1).
    if (formErrorCount > 0) {
      showErrors = true;
      if (issues[0]?.field === "solidity") howOpen = true;
      await tick();
      focusFirstProblem();
      return;
    }
    if (weightG === null || quantity === null) return;

    submitting = true;
    // With a print time, the stored price must match what the visitor saw, so wait for the preview if it is
    // still updating (the debounce plus the round trip) instead of bouncing them with "try again".
    if (timeSeconds !== null) {
      await tick();
      if (previewState.status === "pending" || (previewState.status === "idle" && previewInput !== null)) {
        waitingForPrice = true;
        const settled = await preview.settled();
        waitingForPrice = false;
        if (settled.status !== "resolved") {
          submitting = false;
          serverErrorsFor = formSnapshot;
          serverErrors = settled.status === "error" ? settled.errors : ["Couldn't estimate this request. Try again."];
          return;
        }
      } else if (previewState.status === "error") {
        submitting = false;
        serverErrorsFor = formSnapshot;
        serverErrors = previewState.errors;
        return;
      }
    }

    announce("Sending your request. Large files can take a few minutes.");
    const body = new FormData();
    body.set("name", name.trim());
    body.set("email", email.trim());
    body.set("note", combinedNote);
    body.set("material", materialName);
    body.set("quantity", String(quantity));
    body.set("weightG", String(weightG));
    // An unsliced model has no print time to send; the server stores the request as "needs print time".
    if (timeSeconds !== null) body.set("timeSeconds", String(timeSeconds));
    // No priceCents field: the server computes and stores price_cents itself at insert; the browser's idea of
    // the price was never trustworthy.
    if (file) body.set("gcode", file, file.name); // optional: absent for manual entry, a failed parse, and a model file too large to attach (describeModelAttachment)

    const sentFileName = fileName;
    const sentTimeLabel = timeSeconds !== null ? printTimeLabel : "";
    const result = await submitQuoteRequest(body);
    submitting = false;
    // "Sending your request…" no longer applies, whatever the outcome.
    announce("");
    if (result.ok && result.id !== undefined && result.priceCents !== undefined) {
      const caveat = describeFileStoredOutcome(file !== null, result.fileStored)?.text ?? null;
      submitted = {
        id: result.id,
        reference: result.reference,
        priceCents: result.priceCents,
        needsPrintTime: result.needsPrintTime === true,
        material: materialName,
        quantity,
        email: email.trim(),
        fileName: sentFileName,
        printTimeLabel: sentTimeLabel,
        fileStoredCaveat: caveat,
      };
      fileStoredCaveat = caveat;
      saveConfirmation(QUOTE_CONFIRMATION_KEY, submitted);
      // The form resets so Back from the confirmation can't send the same request twice. Name and email stay in
      // the in-memory store for "Request another estimate" or "Order another item"; "Not you? Clear" and a
      // reload empty them.
      resetEstimateForm();
      navigate({ name: "estimate-received" });
      await focusAfterUpdate(() => quoteConfirmationHeading);
      return;
    }
    serverErrorsFor = formSnapshot;
    if (result.status === 429) {
      rateLimited = true;
      return;
    }
    serverErrors = result.errors;
  }

  // Page title (WCAG 2.4.2): tracks the state the visitor is in, so a title list or history entry says whether
  // this is the form or the confirmation.
  const pageTitle = $derived.by(() => {
    const biz = cfg?.branding.businessName;
    if (route.name === "estimate-received" && submitted) return biz ? `Request received · ${biz}` : "Request received";
    if (route.name === "order-received" && itemOrderConfirmation) return biz ? `Order received · ${biz}` : "Order received";
    if (orderItem) return biz ? `${orderItem.name} · ${biz}` : orderItem.name;
    return biz ? `${biz} — get an estimate` : "Get an estimate";
  });

  /** Clears every estimate-form field. Does not touch the confirmation. */
  function resetEstimateForm() {
    materialName = "";
    quantity = 1;
    note = "";
    showErrors = false;
    clearFile();
  }

  /** Dismisses the custom-estimate confirmation and shows an empty form for a new request; `submitted` survives
   * reloads via sessionStorage, so this is how it ends. */
  function dismissQuoteConfirmation() {
    submitted = null;
    fileStoredCaveat = null;
    clearConfirmation(QUOTE_CONFIRMATION_KEY);
    resetEstimateForm();
    navigate({ name: "estimate" }, { replace: true });
    // The button that held focus unmounts with the confirmation (WCAG 2.4.3).
    void focusAfterUpdate(() => estimateFormHeading);
  }

  /** "Order received" next-step copy shared by both confirmations. */
  const WHAT_HAPPENS_NEXT = "We'll review it and email you to confirm the details and arrange payment. You won't be charged until then.";

  /** "Hot Layer · Color: Red": built in script because a template would trim the space before the bullet. */
  const orderSummaryLine = $derived(
    itemOrderConfirmation
      ? [`${itemOrderConfirmation.quantity} × ${itemOrderConfirmation.itemName}`, itemOrderConfirmation.optionName]
          .filter((s) => s !== "")
          .join(" · ")
      : "",
  );

  const introText = $derived(
    cfg?.introText ? cfg.introText : DEFAULT_INTRO_TEXT,
  );
</script>

<svelte:window ondragover={preventWindowDrop} ondrop={preventWindowDrop} onhashchange={syncRoute} />

<svelte:head>
  <title>{pageTitle}</title>
</svelte:head>

{#snippet timeFields(describedBy: string)}
  <div class="field">
    <span id="quote-time-label">Print time</span>
    <div
      class="time-pair"
      role="group"
      aria-labelledby="quote-time-label"
      aria-describedby={[describedBy, timeIssueMessage ? "quote-time-err" : ""].filter(Boolean).join(" ") || undefined}
    >
      <span class="time-input">
        <input
          id="quote-hours"
          type="number"
          min="0"
          aria-label="Print time (hours)"
          bind:value={manualHours}
          aria-invalid={marked("hours") ? "true" : undefined}
        />
        <span class="suffix">h</span>
      </span>
      <span class="time-input">
        <input
          id="quote-minutes"
          type="number"
          min="0"
          max="59"
          aria-label="Print time (minutes)"
          bind:value={manualMinutes}
          aria-invalid={marked("minutes") ? "true" : undefined}
        />
        <span class="suffix">min</span>
      </span>
    </div>
    {#if timeIssueMessage}<small class="warn-text" id="quote-time-err">{timeIssueMessage}</small>{/if}
  </div>
{/snippet}

<main class="page">
  {#if phase === "loading"}
    <p class="muted center" role="status">Loading…</p>
  {:else if phase === "failed"}
    <p class="muted center">Estimates are temporarily unavailable.</p>
    <p class="center"><button type="button" onclick={() => location.reload()}>Try again</button></p>
  {:else if cfg}
    <header class="brand">
      <!-- Custom logo (quote.logoUrl) when set, else the project mark. Decorative next to the <h1>
           business name, so alt is empty. -->
      <img class="logo" src={cfg.branding.logoUrl || projectLogoUrl} alt="" />
      <h1>{cfg.branding.businessName || "printpaca"}</h1>
    </header>
    {#if cfg.demoMode}
      <p class="banner-muted demo-banner">
        Demo mode: this walks through the real customer flow, but your request isn't submitted.
      </p>
    {/if}
    <!-- The shop's own intro when it wrote one; otherwise a default that says what this page is. -->
    <p class="intro">{introText}</p>
    <!-- Persistent status region (text swapped in) for "That item is no longer available." and similar. -->
    <p class="banner-muted route-note" role="status">{routeNote}</p>

    {#if items && items.length > 0 && cfg.materials.length > 0}
      <div class="view-toggle" role="group" aria-label="View">
        <button
          type="button"
          class:active={view === "gallery"}
          aria-pressed={view === "gallery"}
          onclick={() => navigate({ name: "gallery" })}
        >
          Gallery
        </button>
        <button
          type="button"
          class:active={view === "quote"}
          aria-pressed={view === "quote"}
          onclick={() => navigate({ name: "estimate" })}
        >
          Custom estimate
        </button>
      </div>
    {/if}

    {#if view === "gallery"}
      {#if route.name === "order-received" && itemOrderConfirmation}
        <div class="panel">
          <button type="button" class="quiet back" onclick={dismissItemOrderConfirmation}>&larr; Back to gallery</button>
          <div class="card confirmation">
            <h2 class="section-label" tabindex="-1" bind:this={itemConfirmationHeading}>Order received</h2>
            <p class="price">{formatMoney(itemOrderConfirmation.priceCents, cfg.currency)}</p>
            <p class="ref">Reference: <strong>{itemOrderConfirmation.reference || itemOrderConfirmation.id}</strong></p>
            <p class="ref-hint">Save this reference. Quote it if you contact us about your order.</p>
            <p class="order-summary">{orderSummaryLine}</p>
            <p>We'll follow up at {itemOrderConfirmation.email}.</p>
            <p class="next-steps">{WHAT_HAPPENS_NEXT}</p>
            <button type="button" class="primary" onclick={dismissItemOrderConfirmation}>Order another item</button>
          </div>
        </div>
      {:else if orderItem}
        {#key orderItem.id}
          <div class="panel">
            <ItemOrderPanel
              item={orderItem}
              currency={cfg.currency}
              branding={cfg.branding}
              maxOrderQty={maxQty}
              onback={closeItem}
              onordered={(confirmation) => {
                itemOrderConfirmation = confirmation;
                saveConfirmation(ITEM_ORDER_CONFIRMATION_KEY, confirmation);
                navigate({ name: "order-received" });
                void focusAfterUpdate(() => itemConfirmationHeading);
              }}
            />
          </div>
        {/key}
      {:else}
        <h2 id="gallery-heading" tabindex="-1" class="view-heading">Items</h2>
        <ItemGallery
          items={items ?? []}
          currency={cfg.currency}
          onselect={openItem}
          oncustomestimate={cfg.materials.length > 0 ? () => navigate({ name: "estimate" }) : undefined}
        />
      {/if}
    {:else if cfg.materials.length === 0}
      <p class="muted center">No materials are currently offered.</p>
    {:else if route.name === "estimate-received" && submitted}
      <div class="card confirmation">
        <h2 class="section-label" tabindex="-1" bind:this={quoteConfirmationHeading}>Request received</h2>
        {#if submitted.needsPrintTime}
          <p class="price">Price to be confirmed</p>
          <p class="meta">We'll confirm the price after we prepare your file for printing.</p>
        {:else}
          <p class="meta">Estimated price</p>
          <p class="price">{formatMoney(submitted.priceCents, cfg.currency)}</p>
        {/if}
        <p class="ref">Reference: <strong>{submitted.reference || submitted.id}</strong></p>
        <p class="ref-hint">Save this reference. Quote it if you contact us about your request.</p>
        <p class="order-summary">{submitted.quantity} &times; {submitted.material}</p>
        {#if submitted.fileName}<p class="meta">File: {submitted.fileName}</p>{/if}
        {#if submitted.printTimeLabel}<p class="meta">Print time used: {submitted.printTimeLabel}</p>{/if}
        <p>We'll follow up at {submitted.email}.</p>
        <p class="next-steps">
          {submitted.needsPrintTime
            ? "We'll prepare your file, then email you the price to approve. You won't be charged until you agree to it."
            : "We'll review your request and email you to confirm the price. You won't be charged until you agree to it."}
        </p>
        {#if fileStoredCaveat}<p class="advisory-text">{fileStoredCaveat}</p>{/if}
        <button type="button" class="quiet" onclick={dismissQuoteConfirmation}>Request another estimate</button>
      </div>
    {:else}
      <!-- novalidate: the page validates itself and shows the messages inline, instead of the browser's
           transient bubble, which is announced inconsistently and disappears (WCAG 3.3.1). -->
      <form
        bind:this={formEl}
        class="card"
        novalidate
        onsubmit={(e) => {
          e.preventDefault();
          void submit();
        }}
      >
        <h2 class="view-heading" tabindex="-1" bind:this={estimateFormHeading}>Custom estimate</h2>
        <div class="field">
          <label for="quote-material">Material</label>
          <select
            id="quote-material"
            bind:value={materialName}
            required
            aria-invalid={marked("material") ? "true" : undefined}
            aria-describedby={marked("material") ? "quote-material-desc quote-material-err" : "quote-material-desc"}
          >
            <option value="" disabled>Choose a material</option>
            {#each cfg.materials as m (m.name)}<option value={m.name}>{m.name}</option>{/each}
          </select>
          <small class="field-hint" id="quote-material-desc">{materialName ? materialDescription(materialName) : ""}</small>
          {#if marked("material")}<small class="warn-text" id="quote-material-err">{issueMessage("material")}</small>{/if}
        </div>

        <div class="field">
          <label for="quote-qty">Quantity</label>
          <input
            id="quote-qty"
            type="number"
            min="1"
            max={maxQty}
            step="1"
            bind:value={quantity}
            onchange={(e) => {
              // "1e3" stays on screen as typed; show the number it prices.
              const plain = normalizedQuantityText(e.currentTarget.value);
              if (plain !== null) e.currentTarget.value = plain;
            }}
            required
            aria-invalid={quantityMessage !== "" ? "true" : undefined}
            aria-describedby="quote-qty-notice"
          />
        </div>
        <!-- Outside the field so the message isn't part of its name, and always mounted so the live region
             announces its text changes (WCAG 1.3.1, 4.1.3). The only place the quantity message appears. -->
        <p class="clamp-notice" id="quote-qty-notice" role="status">{quantityMessage}</p>

        <div
          class="card drop"
          class:dragging
          role="group"
          aria-label="Model file"
          ondragover={(e) => {
            e.preventDefault();
            dragging = true;
          }}
          ondragleave={onDragLeave}
          ondrop={onDrop}
        >
          <label for="public-gcode-file">
            <strong id="drop-lead">Upload your 3D file (STL, 3MF, or G-code)</strong>
            <span id="drop-help">
              Optional. Drag a file here or choose one. We'll estimate the weight from its shape. A file from your slicer (the
              program that prepares a model for printing, such as Cura or PrusaSlicer) also gives the print time.
              No file? Enter the weight and print time below.
            </span>
          </label>
          <!-- The visible control is the button; the real input stays in the DOM (visually hidden, out of the
               tab order) so a drop, a label click and tests all reach the same handler. -->
          <div class="choose-row">
            <button
              type="button"
              id="choose-file"
              class="choose-file"
              class:invalid={marked("file")}
              aria-describedby={marked("file") ? "drop-help file-err" : "drop-help"}
              onclick={() => fileInputEl?.click()}
            >
              Choose file
            </button>
            <span class="chosen-name">{fileName || "No file chosen"}</span>
          </div>
          <input
            id="public-gcode-file"
            bind:this={fileInputEl}
            type="file"
            accept=".gcode,.gco,.g,.nc,text/*,.stl,.3mf"
            onchange={onPick}
            class="visually-hidden"
            tabindex="-1"
            aria-labelledby="drop-lead"
            aria-describedby="drop-help"
          />
          <!-- Two persistent regions, text swapped in: a live region whose role changes (or that is inserted
               already populated) is announced unreliably (WCAG 4.1.3). Progress and success are polite;
               problems are assertive. -->
          <p class="status {gcodeStatus}" aria-live="polite">{gcodeIsProblem && !busy ? "" : busy ? `Parsing ${fileName}…` : gcodeMessage}</p>
          <p class="status {gcodeStatus}" role="alert">{gcodeIsProblem && !busy ? gcodeMessage : ""}</p>
          <p class="advisory-text" role="status">{modelAttachmentAdvisory}</p>
          {#if marked("file")}<small class="warn-text" id="file-err">{issueMessage("file")}</small>{/if}
        </div>

        {#if previewTriangles.length > 0}
          <ModelPreview triangles={previewTriangles} {fileName} />
        {/if}
        {#if modelBounds}
          <p class="model-size">Size: {formatBounds(modelBounds)}</p>
        {/if}
        {#if modelBoundsWarning}
          <p class="warn-text" role="status">{modelBoundsWarning}</p>
        {/if}

        {#if hasExactParse}
          {#if weightG !== null && timeSeconds !== null}
            <p class="parsed-meta">Weight: {weightG} g · Print time: {printTimeLabel}</p>
          {:else if weightG !== null}
            <p class="parsed-meta">Weight: {weightG} g</p>
          {/if}
          {#if parsedTimeSeconds === null}
            <!-- Weight came from the parse but no print-time comment was found (gcodeStatus
                 "incomplete"): the visitor can add the time here, or leave it for the shop. -->
            <div class="manual-entry">
              {@render timeFields("quote-time-hint")}
              <small class="field-hint" id="quote-time-hint">
                Optional. If this file didn't include a print time, leave this blank and we'll confirm the price by email.
              </small>
            </div>
          {/if}
          <button type="button" class="quiet remove-file" onclick={clearFile}>Remove file and enter values manually</button>
        {:else if modelSource?.kind === "estimate"}
          <div class="manual-entry">
            <div class="field">
              <label for="quote-weight">Model weight (g)</label>
              <input
                id="quote-weight"
                type="number"
                min="0"
                max={MAX_WEIGHT_G}
                step="0.01"
                bind:value={weightG}
                oninput={() => (weightEdited = true)}
                aria-invalid={marked("weight") ? "true" : undefined}
                aria-describedby={marked("weight") ? "quote-weight-hint quote-weight-err" : "quote-weight-hint"}
              />
              <small class="field-hint" id="quote-weight-hint">
                {#if weightEdited}
                  Using your weight.
                  <button type="button" class="quiet inline-link" onclick={() => (weightEdited = false)}>Use the estimate again</button>
                {:else}
                  Estimated from the model's shape. If you already know the weight, type it here.
                {/if}
              </small>
              {#if marked("weight")}<small class="warn-text" id="quote-weight-err">{issueMessage("weight")}</small>{/if}
            </div>
            <details class="how-estimated" bind:open={howOpen}>
              <summary>How we estimated this</summary>
              <p>
                Weight = model volume × material density × solidity.
                {#if modelSource.kind === "estimate"}
                  For your file that is {modelSource.volumeCm3.toFixed(2)} cm³ × {density.toFixed(2)} g/cm³ × {solidityPct}% =
                  {Math.round(estimateWeightFromVolume(modelSource.volumeCm3, density, solidityPct) * 100) / 100} g.
                {/if}
              </p>
              <p>Solidity is how solid the print is inside: 100% is fully solid, and most prints are 15 to 25%.</p>
              <div class="field">
                <label for="quote-solidity">Solidity (%)</label>
                <input
                  id="quote-solidity"
                  type="number"
                  min="5"
                  max="100"
                  step="1"
                  bind:value={solidityInput}
                  aria-invalid={marked("solidity") ? "true" : undefined}
                  aria-describedby={marked("solidity") ? "quote-solidity-err" : undefined}
                />
                {#if marked("solidity")}<small class="warn-text" id="quote-solidity-err">{issueMessage("solidity")}</small>{/if}
              </div>
            </details>
            {@render timeFields("quote-time-hint")}
            <small class="field-hint" id="quote-time-hint">
              Optional. If you don't know the print time, leave this blank and we'll confirm the price by email.
            </small>
          </div>
          <button type="button" class="quiet remove-file" onclick={clearFile}>Remove file and enter values manually</button>
        {:else}
          <div class="manual-entry">
            <div class="field">
              <label for="quote-weight">Model weight (g)</label>
              <input
                id="quote-weight"
                type="number"
                min="0"
                max={MAX_WEIGHT_G}
                step="0.01"
                bind:value={weightG}
                aria-invalid={marked("weight") ? "true" : undefined}
                aria-describedby={marked("weight") ? "quote-weight-err" : undefined}
              />
              {#if marked("weight")}<small class="warn-text" id="quote-weight-err">{issueMessage("weight")}</small>{/if}
            </div>
            {@render timeFields("")}
          </div>
        {/if}

        <div class="field">
          <label for="quote-name">Name</label>
          <input
            id="quote-name"
            type="text"
            autocomplete="name"
            bind:value={visitor.name}
            maxlength={NAME_MAX_BYTES}
            required
            aria-invalid={nameError ? "true" : undefined}
            aria-describedby={nameError ? "quote-name-err" : undefined}
          />
          {#if nameError}<small class="warn-text" id="quote-name-err">{nameError}</small>{/if}
        </div>
        <div class="field">
          <label for="quote-email">Email</label>
          <input
            id="quote-email"
            type="email"
            autocomplete="email"
            inputmode="email"
            bind:value={visitor.email}
            maxlength={EMAIL_MAX_BYTES}
            required
            aria-invalid={emailError ? "true" : undefined}
            aria-describedby={emailError ? "quote-email-err" : undefined}
          />
          {#if emailError}<small class="warn-text" id="quote-email-err">{emailError}</small>{/if}
        </div>
        {#if visitor.name !== "" || visitor.email !== ""}
          <button type="button" class="quiet inline-link not-you" onclick={clearVisitor}>Not you? Clear name and email</button>
        {/if}
        <div class="field">
          <label for="quote-note">Note (optional)</label>
          <textarea
            id="quote-note"
            bind:value={note}
            maxlength={NOTE_MAX_BYTES}
            rows="3"
            aria-invalid={noteError ? "true" : undefined}
            aria-describedby={noteError ? "quote-note-err" : undefined}
          ></textarea>
          {#if noteError}<small class="warn-text" id="quote-note-err">{noteError}</small>{/if}
        </div>

        <div class="card estimate" role="group" aria-labelledby="estimate-label">
          <h2 class="section-label" id="estimate-label">{priceHeading}</h2>
          <!-- One persistent live region whose content changes, announced as a whole, so the settled price
               is spoken (WCAG 4.1.3). "Updating" is aria-hidden: it would chatter on every keystroke. -->
          <div aria-live="polite" aria-atomic="true">
            {#if previewState.status === "resolved"}
              <p class="price">
                {formatMoney(previewState.priceCents, cfg.currency)}
                {#if quantity !== null && quantity > 1}
                  {@const unitCents = perUnitCents(previewState.priceCents, quantity)}
                  {#if unitCents !== null}
                    <span class="per-unit"> &middot; {formatMoney(unitCents, cfg.currency)} each</span>
                  {/if}
                {/if}
              </p>
              <p class="meta">
                {printTimeLabel} print time{#if previewState.shippingCents > 0}
                  · + {formatMoney(previewState.shippingCents, cfg.currency)} shipping{/if}
              </p>
            {:else if previewState.status === "pending"}
              <!-- Quiet, not alarming: covers both the ~350ms debounce wait and the round trip, so it shows
                   for most of a keystroke's lifetime (one state in quotePreview.ts). -->
              <p class="muted" aria-hidden="true">Updating estimate…</p>
            {:else if previewState.status === "error"}
              {#each previewState.errors as e (e)}<p class="warn-text">{e}</p>{/each}
            {:else if priceWaitingOnShop}
              <p class="price-later">We'll confirm the price after we prepare your file for printing.</p>
              <p class="muted">Add a print time above if you'd like an estimate now.</p>
            {:else if issues.length > 0}
              <!-- Before the first submit attempt this names the blocker; after it, the message sits under the
                   field, so the panel does not repeat it. -->
              <p class="muted">{showErrors ? "Fix the fields marked above to see a price." : issues[0]?.message}</p>
            {:else}
              <p class="muted">Choose a material and add a weight and print time to see a price.</p>
            {/if}
          </div>
          <p class="muted shipping-note">Shipping, if any, is confirmed by email.</p>
        </div>

        <!-- Persistent alert container (text swapped in), with a plain list inside: role="alert" on a <ul>
             would remove its list semantics (WCAG 1.3.1, 4.1.3). -->
        <div role="alert">
          {#if visibleRateLimited}
            <RateLimitNotice branding={cfg.branding} />
          {/if}
          <FormErrorSummary problems={summaryProblems} />
          {#if visibleServerErrors.length > 0}
            <ul class="error-list">
              {#each new Set(visibleServerErrors) as e (e)}<li class="warn-text">{e}</li>{/each}
            </ul>
          {/if}
        </div>

        <p id="estimate-submit-note" class="submit-note">You won't be charged now. We'll review your request and follow up by email.</p>
        <button type="submit" class="primary" class:busy={submitting} aria-disabled={submitting ? "true" : undefined} aria-describedby="estimate-submit-note">
          {waitingForPrice ? "Updating price…" : submitting ? "Submitting…" : "Request an estimate"}
        </button>
      </form>
    {/if}

    <PublicFooter branding={cfg.branding} staffUrl={cfg.staffUrl} />
  {/if}
</main>

<style>
  .page {
    /* 760px, centered: 40rem read as wasted space on desktop with the gallery grid above the form.
     * Narrower than 1440px, and at 390px the padding constrains it (no horizontal scroll). */
    max-width: 760px;
    margin: 0 auto;
    padding: var(--space-4);
    display: flex;
    flex-direction: column;
    gap: var(--space-4);
  }
  .center { text-align: center; margin-top: var(--space-8); }
  .muted { color: var(--muted); }

  .brand {
    display: flex;
    align-items: center;
    gap: var(--space-3);
  }
  .brand .logo { height: 2.5rem; width: auto; }
  .brand h1 { margin: 0; }

  .intro { color: var(--muted); margin: 0; }

  /* Segmented Gallery/Custom estimate toggle. Rendered only when items is non-empty and at least one
   * material is public, so a server with no public items gets no extra layout and no Custom tab that
   * dead-ends on "No materials are currently offered." */
  .view-toggle {
    display: flex;
    gap: var(--space-1);
    background: var(--surface-2);
    border-radius: var(--radius);
    padding: var(--space-1);
  }
  .view-toggle button {
    flex: 1;
    border: none;
    background: transparent;
    border-radius: calc(var(--radius) / 2);
  }
  .view-toggle button.active {
    background: var(--surface);
    box-shadow: var(--shadow);
    font-weight: 600;
  }

  form.card, .confirmation {
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
  }

  .drop {
    border: 2px dashed var(--control-border);
    text-align: center;
    min-height: 44px;
    padding: var(--space-3);
  }
  .drop:focus-within {
    outline: 2px solid var(--focus-ring);
    outline-offset: 2px;
  }
  .drop.dragging {
    border-color: var(--accent);
    background: var(--surface-2);
  }
  /* Left-aligned at hint size, unlike the zone's short centered lines: this label is a five-line
   * explanation, and centered full-size body copy is hard to read on the customer-facing page. The bold
   * lead-in still carries the instruction. */
  .drop label {
    display: block;
    cursor: pointer;
    text-align: left;
    font-size: var(--text-sm);
    color: var(--muted);
    line-height: 1.45;
  }
  .drop label strong {
    color: var(--text);
    font-size: var(--text-base);
  }
  .status {
    margin: var(--space-2) 0 0;
    font-size: var(--text-sm);
    min-height: 1.2em;
  }
  .status.ok { color: var(--ok); }
  .status.not-found, .status.error { color: var(--danger); }
  .status.invalid-profile, .status.incomplete { color: var(--warn); }

  .parsed-meta {
    font-size: var(--text-sm);
    color: var(--muted);
    font-variant-numeric: tabular-nums;
    margin: 0;
  }
  .remove-file { align-self: flex-start; }

  .manual-entry {
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
  }
  .time-pair { display: flex; align-items: center; gap: 0.6rem; }
  .time-input { display: inline-flex; align-items: center; gap: 0.3rem; }
  .time-pair input { width: 4.5rem; }

  textarea { resize: vertical; }

  .estimate { text-align: center; }
  .estimate .price, .confirmation .price {
    font-size: var(--text-xl);
    font-weight: 700;
    font-variant-numeric: tabular-nums;
    margin: var(--space-1) 0;
  }
  .estimate .meta { color: var(--muted); font-size: var(--text-sm); margin: 0; }
  .confirmation { text-align: center; }
  .confirmation .ref { margin: 0; font-variant-numeric: tabular-nums; }
  .confirmation .ref strong { display: block; font-size: var(--text-xl); letter-spacing: 0.04em; }
  .confirmation .ref-hint { color: var(--muted); font-size: var(--text-sm); margin: 0; }
  .not-you { align-self: flex-start; }
  .confirmation .order-summary { font-weight: 600; margin: 0; }

  /* Wraps the restored (post-reload) gallery item-order confirmation; same "back" affordance as
   * ItemOrderPanel's .panel/.back, scoped here because a restored confirmation has no live PublicItem to
   * hand ItemOrderPanel. */
  .panel {
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
  }
  .panel .back { align-self: flex-start; }

  /* Shop-identity footer, shown only when config carries email, phone or address (all optional, see
   * api.ts's PublicConfig.branding). Quiet: a courtesy footer, not another call to action. */

  .clamp-notice { color: var(--warn); font-size: var(--text-sm); margin: 0.3rem 0 0; }
  /* The persistent live regions render empty most of the time; drop their margins then so they take no room. */
  .clamp-notice:empty, .status:empty, .advisory-text:empty { margin: 0; min-height: 0; }
  .error-list { list-style: none; margin: 0; padding: 0; }
  .error-list li { margin: 0.2rem 0; }
  .view-heading { margin: 0; }
  /* Long unbroken strings (an email in "We'll follow up at ...", a reference id) must wrap at 320px. */
  .confirmation p { overflow-wrap: anywhere; }
  /* In flight: aria-disabled keeps focus on the button, so the busy look is visual only. */
  button.busy { opacity: 0.6; cursor: progress; }
  .submit-note { color: var(--muted); font-size: var(--text-sm); margin: 0; }
  .per-unit { color: var(--muted); font-weight: 400; }
  .route-note:empty { display: none; }
  .meta { color: var(--muted); font-size: var(--text-sm); margin: 0; }
  .next-steps { color: var(--muted); font-size: var(--text-sm); margin: 0; }
  .price-later { margin: var(--space-1) 0; font-weight: 600; }
  .shipping-note { margin: var(--space-1) 0 0; font-size: var(--text-sm); }
  .field-hint { color: var(--muted); font-size: var(--text-sm); }
  .field-hint:empty { display: none; }
  .model-size { color: var(--muted); font-size: var(--text-sm); margin: 0; font-variant-numeric: tabular-nums; }
  .choose-file.invalid { border-color: var(--danger); }
  .choose-row { display: flex; align-items: center; gap: var(--space-2); margin-top: var(--space-2); flex-wrap: wrap; text-align: left; }
  .chosen-name { color: var(--muted); font-size: var(--text-sm); overflow-wrap: anywhere; min-width: 0; }
  .inline-link { min-height: 0; padding: 0 0.25rem; display: inline; }
  .how-estimated { font-size: var(--text-sm); }
  .how-estimated summary { cursor: pointer; padding: 0.4rem 0; min-height: 24px; }
  .how-estimated p { margin: var(--space-1) 0; color: var(--muted); }
  .how-estimated .field { margin-top: var(--space-2); }
  @media (max-width: 600px) {
    /* 44 px tap targets on phones (the app-wide quiet button is 32 px). */
    .back, .remove-file, .inline-link, .confirmation .quiet, .not-you { min-height: 44px; }
    .how-estimated summary { min-height: 44px; display: flex; align-items: center; }
  }
</style>
