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
    formatMoney,
    parseGcodeStream,
    parseStl,
    parseThreeMf,
    resolveGcode,
    clampSolidityPct,
    DEFAULT_SOLIDITY_PCT,
  } from "../lib/engine/index";
  import {
    classifyFileKind,
    describeFileStoredOutcome,
    describeModelAttachment,
    describeModelParseFailure,
    describeModelTooLarge,
    describeVolumeEstimate,
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
  import { clampQuantity, perUnitCents } from "./publicPricing";
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

  interface QuoteConfirmation {
    id: string;
    priceCents: number;
    material: string;
    quantity: number;
    email: string;
    fileStoredCaveat: string | null;
  }

  // Config load

  let phase = $state<"loading" | "failed" | "ready">("loading");
  let cfg = $state.raw<PublicConfig | null>(null);

  // Public items load alongside config. A failed or empty items fetch is not an error: it just means no
  // gallery. Only a failed config fetch (phase "failed") is a real failure.
  let items = $state.raw<PublicItem[] | null>(null);
  let view = $state<"gallery" | "quote">("quote");
  let orderItem = $state.raw<PublicItem | null>(null);
  // Restored from sessionStorage on mount: a self-contained summary of the last gallery order, independent of
  // the live `items`/orderItem (both reset on load). Takes priority over orderItem/ItemGallery in the
  // template; dismissed via dismissItemOrderConfirmation.
  let itemOrderConfirmation = $state<ItemOrderConfirmation | null>(null);

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
    // A restored item-order confirmation implies the gallery flow; a restored quote confirmation renders
    // under the existing `submitted` branch (view === "quote"), so only the former forces the gallery view.
    if (itemOrderConfirmation) view = "gallery";
    else if (items && items.length > 0) view = "gallery";
  });

  /** Dismisses a gallery item-order confirmation and returns to the item grid; the only way `submitted`
   * clears once sessionStorage persistence is in play. */
  function dismissItemOrderConfirmation() {
    itemOrderConfirmation = null;
    clearConfirmation(ITEM_ORDER_CONFIRMATION_KEY);
    // The Back button that held focus is about to unmount (WCAG 2.4.3).
    void focusAfterUpdate("#gallery-heading");
  }

  // Focus management for the gallery <-> item panel swap (WCAG 2.4.3): opening a card focuses the panel's
  // heading (ItemOrderPanel does that itself on mount); going back refocuses the card that was opened, or the
  // gallery heading when that card is gone.
  let lastSelectedId: string | null = null;
  function openItem(item: PublicItem) {
    lastSelectedId = item.id;
    orderItem = item;
  }
  function closeItem() {
    orderItem = null;
    const id = lastSelectedId;
    void focusAfterUpdate(() => {
      const card = id === null ? null : document.querySelector<HTMLElement>(`[data-item-id="${CSS.escape(id)}"]`);
      return card ?? document.getElementById("gallery-heading");
    });
  }

  // Confirmation headings take focus after a successful submit: the form (and the focused submit button) is
  // replaced, so focus would otherwise drop to <body> and nothing would be announced (WCAG 2.4.3, 4.1.3).
  let quoteConfirmationHeading: HTMLElement | undefined = $state();
  let itemConfirmationHeading: HTMLElement | undefined = $state();
  let estimateFormHeading: HTMLElement | undefined = $state();
  let formEl: HTMLFormElement | undefined = $state();

  // Mirror server/public.go's bounds on weightG/timeSeconds so a manual entry the server would reject is
  // flagged first.
  const MAX_WEIGHT_G = 100000;
  const MAX_TIME_SECONDS = 30 * 24 * 3600;
  const MAX_QUOTE_QUANTITY = 1000;

  // Form state

  let materialName = $state("");
  // Falls back to PLA's density while no material is chosen, so an early file drop still shows an estimate.
  const density = $derived(densityOrFallback(cfg?.materials.find((m) => m.name === materialName)?.density));
  let quantity = $state<number | null>(1);
  let note = $state("");
  // Name and email live in the shared visitor store (see visitor.svelte.ts) so they carry over to an item order.
  const name = $derived(visitor.name);
  const email = $derived(visitor.email);
  // Set only when a typed quantity exceeded MAX_QUOTE_QUANTITY and was clamped. Cleared on the next edit (the
  // input's oninput), not by the effect, which would clear it in the same reactive pass that set it.
  let quantityClampNotice = $state("");
  $effect(() => {
    if (quantity === null) return;
    const clamped = clampQuantity(quantity, MAX_QUOTE_QUANTITY);
    if (clamped.message) {
      quantity = clamped.value;
      quantityClampNotice = clamped.message;
    }
  });

  let fileName = $state("");
  let file = $state<File | null>(null);
  let busy = $state(false);
  let dragging = $state(false);
  let weightG = $state<number | null>(null);
  // Set only when the current parse's weight was derived from filament length (not slicer-reported), so a
  // material change re-derives it; null otherwise, which leaves weightG alone.
  let lengthMeta = $state.raw<RawGcodeMeta | null>(null);
  let timeSeconds = $state<number | null>(null);
  let gcodeStatus = $state<GcodeStatus>("idle");
  let gcodeMessage = $state("");
  // True once the current drop's parse supplied a timeSeconds (g-code or sliced 3MF with a duration). False
  // for a weight-only parse, so the manual hour/minute effect still fills timeSeconds; hasExactParse can't
  // drive that guard since it stays true for weight-only parses.
  let timeFromParse = $state(false);

  // Set for every STL/3MF drop that parses, whether or not its File is attached (an oversized one keeps its
  // estimate). "sliced" fills weight and time like a g-code file; "estimate" fills weight only, time stays
  // manual. hasExactParse uses this rather than `file`, since a geometry estimate can also carry a File.
  type ModelSource = { fileName: string; kind: "sliced" } | { fileName: string; kind: "estimate"; volumeCm3: number };
  let modelSource = $state<ModelSource | null>(null);
  // True when weightG should come from the parse itself (a real g-code file or sliced 3MF) rather than a
  // geometry-only estimate that still needs a manual time. Not `file !== null`: modelSource === null marks a
  // plain g-code drop (STL/3MF drops always set modelSource). It does not guarantee timeSeconds is filled;
  // the template shows manual print-time inputs whenever timeSeconds is null (see `timeFromParse`).
  const hasExactParse = $derived((file !== null && modelSource === null) || modelSource?.kind === "sliced");
  // Raw box contents (cleared or out-of-range while typing is fine); the estimate effect reads the clamped
  // solidityPct, so a cleared box never derives 0 g or renders "null%" (same as GcodeDrop's solidity input).
  let solidityInput = $state<number | null>(DEFAULT_SOLIDITY_PCT);
  const solidityPct = $derived(clampSolidityPct(solidityInput));
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

  // Manual fallback when there is no sliced file (or an STL/unsliced 3MF, which never provides time): hours
  // and minutes combine into the same timeSeconds a parse would give. weightG needs no manual variable; its
  // input binds directly.
  let manualHours = $state<number | null>(null);
  let manualMinutes = $state<number | null>(null);

  $effect(() => {
    if (timeFromParse) return; // the parse itself already supplied timeSeconds
    timeSeconds = manualHours === null && manualMinutes === null ? null : (manualHours ?? 0) * 3600 + (manualMinutes ?? 0) * 60;
  });

  // Single source of truth for an "estimate" modelSource's weight and status line: a moved solidity slider
  // recomputes both together. Safe as an $effect: it reads modelSource/solidityPct and writes
  // weightG/gcodeStatus/gcodeMessage, none of which it reads back. weightG has no manual input while the kind
  // is "estimate", so there is no edit to revert.
  $effect(() => {
    if (!modelSource || modelSource.kind !== "estimate") return;
    const est = describeVolumeEstimate(modelSource.fileName, modelSource.volumeCm3, density, solidityPct);
    weightG = Math.round(est.weightG * 100) / 100;
    gcodeStatus = "ok";
    gcodeMessage = est.message.text;
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
  let submitted = $state<QuoteConfirmation | null>(null);
  let submitErrors = $state<string[]>([]);
  let rateLimited = $state(false);
  // Set on a successful submit whose response had fileStored: false (storage-full path): the request went
  // through but the file wasn't stored. null otherwise, including manual entries.
  let fileStoredCaveat = $state<string | null>(null);

  // Server-side price preview: the price and shipping come from POST /api/public/quote/preview; the cost
  // model never ships to the visitor's browser. lib/quotePreview.ts owns the debounce (~350ms) and staleness
  // machine; this wiring feeds it inputs and mirrors its state into previewState. Submit stays gated on
  // previewState.status === "resolved", since the same server prices preview and submission.

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

  // Mirrors server/public.go's bounds (MAX_WEIGHT_G/MAX_TIME_SECONDS, quantity 1-1000, a chosen material):
  // previews only fire once every field the server requires is in place, so a request it would reject spends
  // no round trip.
  const previewInput = $derived.by(() => {
    if (materialName === "") return null;
    if (quantity === null || !Number.isInteger(quantity) || quantity < 1 || quantity > MAX_QUOTE_QUANTITY) return null;
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
    timeSeconds = null;
    timeFromParse = false;
    gcodeStatus = "idle";
    gcodeMessage = "";
    solidityInput = DEFAULT_SOLIDITY_PCT;
    previewTriangles = [];
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
        if (result.timeSeconds !== undefined) {
          timeSeconds = Math.round(result.timeSeconds);
          timeFromParse = true;
        }
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
        // The sink collects triangles during the same arrayBuffer pass parseStl already makes (no second
        // read). Cleared at the top of handleFile; read only by ModelPreview, never feeding
        // weightG/timeSeconds/upload/submit.
        const collector = new TriangleCollector();
        const stl = parseStl(buffer, { onTriangle: (...tri) => collector.add(...tri) });
        if (stl.status === "ok" && stl.volumeCm3 !== undefined) {
          // weightG/gcodeStatus/gcodeMessage come from the solidity effect above once modelSource is set.
          modelSource = { fileName: f.name, kind: "estimate", volumeCm3: stl.volumeCm3 };
          previewTriangles = collector.result();
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
      const tmf = await parseThreeMf(buffer, { onTriangle: (...tri) => collector.add(...tri) });
      if (token !== seq) return;
      if (tmf.status === "ok" && tmf.kind === "sliced" && tmf.meta) {
        const result = resolveGcode(tmf.meta, cfg.filamentDiameterMm, density);
        if (result.source === "derived-from-length") lengthMeta = tmf.meta;
        if (result.weightG !== undefined) weightG = Math.round(result.weightG * 100) / 100;
        if (result.timeSeconds !== undefined) {
          timeSeconds = Math.round(result.timeSeconds);
          timeFromParse = true;
        }
        modelSource = { fileName: f.name, kind: "sliced" };
        previewTriangles = collector.result();
        gcodeStatus = gcodeStatusFor(result);
        gcodeMessage = describeGcode(result, f.name);
        const attachment = describeModelAttachment(f.name, f.size, maxUploadBytes, "sliced 3MF, weight/time from the slicer's own prediction");
        if (attachment.attach) file = f;
        modelFileNote = attachment.ownerText;
        modelAttachmentAdvisory = attachment.visitorText;
      } else if (tmf.status === "ok" && tmf.kind === "geometry" && tmf.volumeCm3 !== undefined) {
        modelSource = { fileName: f.name, kind: "estimate", volumeCm3: tmf.volumeCm3 };
        previewTriangles = collector.result();
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
    solidityInput = DEFAULT_SOLIDITY_PCT;
    previewTriangles = [];
    fileName = "";
    weightG = null;
    lengthMeta = null;
    timeSeconds = null;
    timeFromParse = false;
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

  // Errors for the three text fields are shown inline (aria-invalid + aria-describedby, WCAG 3.3.1); the rest
  // go to the summary list. Computed live, but shown only after the first submit attempt.
  const fieldErrors = $derived.by(() => {
    const out: { name?: string; email?: string; note?: string; other: string[] } = { other: [] };
    const errors = out.other;
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
    if (materialName === "") errors.push("Choose a material.");
    if (quantity === null || !Number.isInteger(quantity) || quantity < 1 || quantity > MAX_QUOTE_QUANTITY) {
      errors.push(`Quantity must be a whole number from 1 to ${MAX_QUOTE_QUANTITY}.`);
    }
    // A parsed g-code or sliced 3MF should supply both figures, like a manual estimate; an STL or
    // geometry-3MF estimate supplies only weight, so print time is manual regardless of whether its File was
    // attached.
    if (hasExactParse) {
      if (weightG === null || timeSeconds === null) {
        errors.push("This file doesn't include both weight and print time. Try another file, or remove it and enter them yourself.");
      }
    } else {
      if (weightG === null) errors.push("Enter the model weight, or drop a G-code, STL, or 3MF file.");
      else if (weightG < 0 || weightG > MAX_WEIGHT_G) errors.push(`Weight must be from 0 to ${MAX_WEIGHT_G.toLocaleString("en-US")} g.`);
      if (manualHours === null && manualMinutes === null) errors.push("Enter the print time, or drop a sliced G-code/3MF file.");
      else if (manualMinutes !== null && (manualMinutes < 0 || manualMinutes > 59)) {
        errors.push("Minutes must be from 0 to 59.");
      } else if (timeSeconds !== null && (timeSeconds < 0 || timeSeconds > MAX_TIME_SECONDS)) {
        errors.push("Print time must be 30 days or less.");
      }
    }
    return out;
  });
  const formErrorCount = $derived(
    (fieldErrors.name ? 1 : 0) + (fieldErrors.email ? 1 : 0) + (fieldErrors.note ? 1 : 0) + fieldErrors.other.length,
  );
  // Flipped by the first submit attempt, so a visitor isn't told "Name is required." before they've tried.
  let showErrors = $state(false);
  const nameError = $derived(showErrors ? fieldErrors.name : undefined);
  const emailError = $derived(showErrors ? fieldErrors.email : undefined);
  const noteError = $derived(showErrors ? fieldErrors.note : undefined);

  async function submit() {
    // aria-disabled (not disabled) while in flight, so the focused button keeps focus (WCAG 2.4.3).
    if (submitting) return;
    submitErrors = [];
    rateLimited = false;
    // Validation runs first, even while the price is still resolving, so name and email problems surface
    // right away (WCAG 3.3.1).
    if (formErrorCount > 0) {
      showErrors = true;
      submitErrors = fieldErrors.other;
      await tick();
      formEl?.querySelector<HTMLElement>("[aria-invalid='true']")?.focus();
      return;
    }
    // Gated on a resolved preview: a form submit (Enter in a text field) must not fire before the server has
    // priced the request; there is no local price to fall back on.
    if (weightG === null || timeSeconds === null || quantity === null) return;
    if (previewState.status !== "resolved") {
      submitErrors = [
        previewState.status === "error"
          ? "The estimate has a problem. Fix it above, then request again."
          : "The estimate is still updating. Try again in a moment.",
      ];
      return;
    }

    submitting = true;
    announce("Sending your request. Large files can take a few minutes.");
    const body = new FormData();
    body.set("name", name.trim());
    body.set("email", email.trim());
    body.set("note", combinedNote);
    body.set("material", materialName);
    body.set("quantity", String(quantity));
    body.set("weightG", String(weightG));
    body.set("timeSeconds", String(timeSeconds));
    // No priceCents field: the server computes and stores price_cents itself at insert; the browser's idea of
    // the price was never trustworthy.
    if (file) body.set("gcode", file, file.name); // optional: absent for manual entry, a failed parse, and a model file too large to attach (describeModelAttachment)

    const result = await submitQuoteRequest(body);
    submitting = false;
    if (result.ok && result.id !== undefined && result.priceCents !== undefined) {
      const caveat = describeFileStoredOutcome(file !== null, result.fileStored)?.text ?? null;
      submitted = {
        id: result.id,
        priceCents: result.priceCents,
        material: materialName,
        quantity,
        email: email.trim(),
        fileStoredCaveat: caveat,
      };
      fileStoredCaveat = caveat;
      saveConfirmation(QUOTE_CONFIRMATION_KEY, submitted);
      showErrors = false;
      // The request is sent: a shared device shouldn't keep the visitor's name and email around.
      clearVisitor();
      await focusAfterUpdate(() => quoteConfirmationHeading);
      return;
    }
    if (result.status === 429) {
      rateLimited = true;
      return;
    }
    submitErrors = result.errors;
  }

  // Page title (WCAG 2.4.2): tracks the state the visitor is in, so a title list or history entry says whether
  // this is the form or the confirmation.
  const pageTitle = $derived.by(() => {
    const biz = cfg?.branding.businessName;
    if (view === "quote" && submitted) return biz ? `Request received · ${biz}` : "Request received";
    if (itemOrderConfirmation) return biz ? `Order received · ${biz}` : "Order received";
    if (orderItem) return biz ? `${orderItem.name} · ${biz}` : orderItem.name;
    return biz ? `${biz} — get an estimate` : "Get an estimate";
  });

  /** Dismisses the custom-estimate confirmation and resets the form for a new request; the only way back
   * once `submitted` is set (it survives reloads via sessionStorage). */
  function dismissQuoteConfirmation() {
    submitted = null;
    fileStoredCaveat = null;
    clearConfirmation(QUOTE_CONFIRMATION_KEY);
    materialName = "";
    quantity = 1;
    note = "";
    showErrors = false;
    clearFile();
    // The button that held focus unmounts with the confirmation (WCAG 2.4.3).
    void focusAfterUpdate(() => estimateFormHeading);
  }
</script>

<svelte:window ondragover={preventWindowDrop} ondrop={preventWindowDrop} />

<svelte:head>
  <title>{pageTitle}</title>
</svelte:head>

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
    {#if cfg.introText}<p class="intro">{cfg.introText}</p>{/if}

    {#if items && items.length > 0 && cfg.materials.length > 0}
      <div class="view-toggle" role="group" aria-label="View">
        <button
          type="button"
          class:active={view === "gallery"}
          aria-pressed={view === "gallery"}
          onclick={() => {
            view = "gallery";
            orderItem = null;
          }}
        >
          Gallery
        </button>
        <button type="button" class:active={view === "quote"} aria-pressed={view === "quote"} onclick={() => (view = "quote")}>
          Custom estimate
        </button>
      </div>
    {/if}

    {#if view === "gallery"}
      {#if itemOrderConfirmation}
        <div class="panel">
          <button type="button" class="quiet back" onclick={dismissItemOrderConfirmation}>&larr; Back to gallery</button>
          <div class="card confirmation">
            <h2 class="section-label" tabindex="-1" bind:this={itemConfirmationHeading}>Order received</h2>
            <p class="price">{formatMoney(itemOrderConfirmation.priceCents, cfg.currency)}</p>
            <p class="ref">Reference: {itemOrderConfirmation.id}</p>
            <p class="order-summary">
              {itemOrderConfirmation.quantity} &times; {itemOrderConfirmation.itemName}{#if itemOrderConfirmation.optionName}
                &middot; {itemOrderConfirmation.optionLabel || "Option"}: {itemOrderConfirmation.optionName}{/if}
            </p>
            <p>We'll follow up at {itemOrderConfirmation.email}.</p>
          </div>
        </div>
      {:else if orderItem}
        {#key orderItem.id}
          <ItemOrderPanel
            item={orderItem}
            currency={cfg.currency}
            onback={closeItem}
            onordered={(confirmation) => {
              orderItem = null;
              itemOrderConfirmation = confirmation;
              saveConfirmation(ITEM_ORDER_CONFIRMATION_KEY, confirmation);
              void focusAfterUpdate(() => itemConfirmationHeading);
            }}
          />
        {/key}
      {:else}
        <h2 id="gallery-heading" tabindex="-1" class="view-heading">Items</h2>
        <ItemGallery
          items={items ?? []}
          currency={cfg.currency}
          onselect={openItem}
          oncustomestimate={cfg.materials.length > 0 ? () => (view = "quote") : undefined}
        />
      {/if}
    {:else if cfg.materials.length === 0}
      <p class="muted center">No materials are currently offered.</p>
    {:else if submitted}
      <div class="card confirmation">
        <h2 class="section-label" tabindex="-1" bind:this={quoteConfirmationHeading}>Request received</h2>
        <p class="price">{formatMoney(submitted.priceCents, cfg.currency)}</p>
        <p class="ref">Reference: {submitted.id}</p>
        <p class="order-summary">{submitted.quantity} &times; {submitted.material}</p>
        <p>We'll follow up at {submitted.email}.</p>
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
        <label class="field">
          <span>Material</span>
          <select
            bind:value={materialName}
            required
            aria-invalid={showErrors && materialName === "" ? "true" : undefined}
          >
            <option value="" disabled>Choose a material</option>
            {#each cfg.materials as m (m.name)}<option value={m.name}>{m.name}</option>{/each}
          </select>
        </label>

        <label class="field">
          <span>Quantity</span>
          <input
            type="number"
            min="1"
            max={MAX_QUOTE_QUANTITY}
            step="1"
            bind:value={quantity}
            oninput={() => (quantityClampNotice = "")}
            required
            aria-describedby="quote-qty-notice"
          />
        </label>
        <!-- Outside the <label> so the notice isn't part of the field's name, and always mounted so the
             live region announces its text changes (WCAG 1.3.1, 4.1.3). -->
        <p class="clamp-notice" id="quote-qty-notice" role="status">{quantityClampNotice}</p>

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
            <strong id="drop-lead">Drop a .gcode, .stl, or .3mf file</strong>
            <span id="drop-help">
              (or click or press Enter to choose one). Optional. A sliced file fills in weight and print time; an
              unsliced STL or 3MF estimates weight from its shape, so you still enter print time. No file? Enter
              both below.
            </span>
          </label>
          <!-- The name is the short lead-in; the long explanation is the description (WCAG 2.4.6). -->
          <input
            id="public-gcode-file"
            type="file"
            accept=".gcode,.gco,.g,.nc,text/*,.stl,.3mf"
            onchange={onPick}
            class="visually-hidden"
            aria-labelledby="drop-lead"
            aria-describedby="drop-help"
          />
          <!-- Two persistent regions, text swapped in: a live region whose role changes (or that is inserted
               already populated) is announced unreliably (WCAG 4.1.3). Progress and success are polite;
               problems are assertive. -->
          <p class="status {gcodeStatus}" aria-live="polite">{gcodeIsProblem && !busy ? "" : busy ? `Parsing ${fileName}…` : gcodeMessage}</p>
          <p class="status {gcodeStatus}" role="alert">{gcodeIsProblem && !busy ? gcodeMessage : ""}</p>
          <p class="advisory-text" role="status">{modelAttachmentAdvisory}</p>
        </div>

        {#if previewTriangles.length > 0}
          <ModelPreview triangles={previewTriangles} {fileName} />
        {/if}

        {#if hasExactParse}
          {#if weightG !== null && timeSeconds !== null}
            <p class="parsed-meta">Weight: {weightG} g · Print time: {printTimeLabel}</p>
          {:else if weightG !== null}
            <p class="parsed-meta">Weight: {weightG} g</p>
          {/if}
          {#if timeSeconds === null}
            <!-- Weight came from the parse but no print-time comment was found (gcodeStatus
                 "incomplete"): fill in just the missing time here instead of forcing a remove-file round
                 trip. -->
            <div class="manual-entry">
              <div class="field">
                <span>Print time</span>
                <div class="time-pair" role="group" aria-label="Print time">
                  <span class="time-input">
                    <input type="number" min="0" aria-label="Print time (hours)" bind:value={manualHours} />
                    <span class="suffix">h</span>
                  </span>
                  <span class="time-input">
                    <input type="number" min="0" max="59" aria-label="Print time (minutes)" bind:value={manualMinutes} />
                    <span class="suffix">min</span>
                  </span>
                </div>
              </div>
            </div>
          {/if}
          <button type="button" class="quiet remove-file" onclick={clearFile}>Remove file and enter values manually</button>
        {:else if modelSource?.kind === "estimate"}
          <div class="manual-entry">
            {#if weightG !== null}<p class="parsed-meta">Estimated weight: {weightG} g</p>{/if}
            <div class="field">
              <label for="quote-solidity">Solidity (%)</label>
              <input id="quote-solidity" type="number" min="5" max="100" step="1" bind:value={solidityInput} aria-describedby="quote-solidity-hint" />
              <small class="field-hint" id="quote-solidity-hint">How solid the print is. 100% is fully solid; most prints are 15 to 25%.</small>
            </div>
            <div class="field">
              <span>Print time</span>
              <div class="time-pair" role="group" aria-label="Print time">
                <span class="time-input">
                  <input type="number" min="0" aria-label="Print time (hours)" bind:value={manualHours} />
                  <span class="suffix">h</span>
                </span>
                <span class="time-input">
                  <input type="number" min="0" max="59" aria-label="Print time (minutes)" bind:value={manualMinutes} />
                  <span class="suffix">min</span>
                </span>
              </div>
            </div>
          </div>
          <button type="button" class="quiet remove-file" onclick={clearFile}>Remove file and enter values manually</button>
        {:else}
          <div class="manual-entry">
            <label class="field">
              <span>Model weight (g)</span>
              <input type="number" min="0" max={MAX_WEIGHT_G} step="0.01" bind:value={weightG} />
            </label>
            <div class="field">
              <span>Print time</span>
              <div class="time-pair" role="group" aria-label="Print time">
                <span class="time-input">
                  <input type="number" min="0" aria-label="Print time (hours)" bind:value={manualHours} />
                  <span class="suffix">h</span>
                </span>
                <span class="time-input">
                  <input type="number" min="0" max="59" aria-label="Print time (minutes)" bind:value={manualMinutes} />
                  <span class="suffix">min</span>
                </span>
              </div>
            </div>
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
          <h2 class="section-label" id="estimate-label">Estimated price</h2>
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
            {:else}
              <p class="muted">Choose a material and add a weight and print time to see a price.</p>
            {/if}
          </div>
        </div>

        <!-- Persistent alert container (text swapped in), with a plain list inside: role="alert" on a <ul>
             would remove its list semantics (WCAG 1.3.1, 4.1.3). -->
        <div role="alert">
          {#if rateLimited}
            <p class="warn-text">Too many requests from this network. Try again in an hour.</p>
          {/if}
          {#if submitErrors.length > 0}
            <ul class="error-list">
              {#each submitErrors as e (e)}<li class="warn-text">{e}</li>{/each}
            </ul>
          {/if}
        </div>

        <p id="estimate-submit-note" class="submit-note">You won't be charged now. We'll review your request and follow up by email.</p>
        <button type="submit" class="primary" class:busy={submitting} aria-disabled={submitting ? "true" : undefined} aria-describedby="estimate-submit-note">
          {submitting ? "Submitting…" : "Request an estimate"}
        </button>
      </form>
    {/if}

    <PublicFooter branding={cfg.branding} />
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
  .confirmation .ref { color: var(--muted); font-size: var(--text-sm); font-variant-numeric: tabular-nums; }
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
</style>
