<script lang="ts">
  import {
    parseGcodeStream,
    parseStl,
    parseThreeMf,
    resolveGcode,
    clampSolidityPct,
    DEFAULT_SOLIDITY_PCT,
    type EffectiveConfig,
    type GcodeResult,
  } from "../lib/engine/index";
  import { MAX_ORDER_PARTS } from "../lib/engine/index";
  import { addPart, currentDensity, currentDiameter, quote, type PartForm } from "../lib/state.svelte";
  import { partLabelFromFile } from "../lib/partFiles";
  import {
    describeGcodeResult,
    describeModelParseFailure,
    describeModelTooLarge,
    describeReadError,
    describeVolumeEstimate,
    MAX_MODEL_BYTES,
    type GcodeMessage,
  } from "../lib/gcodeMessage";
  import { applyResolvedPrefill, clearPrefillIfOwnedBy, isPrefillStale, prefillFor } from "../lib/prefill.svelte";

  let { cfg, part }: { cfg: EffectiveConfig; part: PartForm } = $props();

  /** "gcode": a g-code text parse or a sliced 3MF's slicer prediction (both exact). "estimate": an STL or
   * unsliced 3MF's mesh volume, weight only. "parse-error": bytes that parsed as neither a slicer result
   * nor usable geometry. */
  type ParseOutcome =
    | { kind: "gcode"; result: GcodeResult }
    | { kind: "estimate"; volumeCm3: number }
    | { kind: "parse-error"; reason: "empty" | "malformed" | "too-large" };

  let outcome = $state<ParseOutcome | null>(null);
  let readError = $state<GcodeMessage | null>(null);
  let fileName = $state("");
  let busy = $state(false);
  let dragging = $state(false);
  let diameterUsed = $state(0);
  let densityUsed = $state(0);
  // Raw box contents (cleared or out-of-range while typing is fine); consumers read the clamped solidityPct
  // so a cleared box never derives 0 g or renders "null%".
  let solidityInput = $state<number | null>(DEFAULT_SOLIDITY_PCT);
  const solidityPct = $derived(clampSolidityPct(solidityInput));

  /** A new model file makes the part's old print time meaningless: it belonged to a different model, or was
   * typed by hand. A file that carries slicer time overwrites it through applyResolvedPrefill; one that
   * doesn't must leave the field blank so the price asks for the time instead of reusing a stale one. */
  function clearStaleTime(target: PartForm, resolved?: GcodeResult) {
    if (resolved?.timeSeconds !== undefined) return;
    target.timeHours = null;
    target.timeMinutes = null;
  }

  /** Monotonic request token per target part: only the latest handleFile() for a part may write it, so a
   * slower earlier parse can't clobber a later one. Per part so a multi-file drop parses concurrently. */
  const seqByPart = new Map<string, number>();

  const uid = $props.id();
  const inputId = `${uid}-file`;
  const solidityInputId = `${uid}-solidity`;

  // Recomputed when solidityPct changes, keeping slider and banner in lockstep; the single spot turning a
  // volume into a weight+message pair (describeVolumeEstimate).
  const estimate = $derived(
    outcome?.kind === "estimate" ? describeVolumeEstimate(fileName, outcome.volumeCm3, densityUsed, solidityPct) : null,
  );

  const baseMessage = $derived(
    readError
      ? readError
      : outcome === null
        ? null
        : outcome.kind === "gcode"
          ? describeGcodeResult(outcome.result, fileName, diameterUsed, densityUsed)
          : outcome.kind === "estimate"
            ? (estimate?.message ?? null)
            : describeModelParseFailure(fileName, outcome.reason),
  );

  // The form's weight/time may reflect this zone's last parse or a later Moonraker pick (RecentPrints, see
  // prefill.svelte.ts). Claim the banner and its staleness suffix only while this zone still owns the values.
  const owns = $derived(prefillFor(part)?.source === "gcode" || prefillFor(part)?.source === "model");
  const stale = $derived(owns && isPrefillStale(part));

  const message = $derived(
    !baseMessage
      ? null
      : baseMessage.kind !== "ok"
        ? baseMessage
        : !owns
          ? null
          : stale
            ? { ...baseMessage, text: `${baseMessage.text} (values edited since parse)` }
            : baseMessage,
  );

  const statusKind = $derived(busy ? "busy" : (message?.kind ?? ""));
  const statusText = $derived(busy ? `Parsing ${fileName}…` : (message?.text ?? ""));

  async function* chunksOf(file: File): AsyncIterable<string> {
    const reader = file.stream().getReader();
    const decoder = new TextDecoder();
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      yield decoder.decode(value, { stream: true });
    }
    yield decoder.decode();
  }

  /** Extension-based, not MIME-based: dropped files often arrive with an empty or generic type for
   * .stl/.3mf. Anything unrecognized takes the streamed-text g-code path. */
  function classify(name: string): "gcode" | "stl" | "3mf" {
    const lower = name.toLowerCase();
    if (lower.endsWith(".stl")) return "stl";
    if (lower.endsWith(".3mf")) return "3mf";
    return "gcode";
  }

  /** Parses one file into `target`. Banner state is written only when `target` is the part this zone shows;
   * other parts of a multi-file drop still fill their own. */
  async function handleFile(file: File, target: PartForm) {
    const token = (seqByPart.get(target.id) ?? 0) + 1;
    seqByPart.set(target.id, token);
    const current = () => seqByPart.get(target.id) === token;
    const shown = target.id === part.id;
    if (shown) {
      busy = true;
      fileName = file.name;
      outcome = null;
      readError = null;
      solidityInput = DEFAULT_SOLIDITY_PCT;
    }
    clearPrefillIfOwnedBy(target, "gcode");
    clearPrefillIfOwnedBy(target, "model");
    try {
      const kind = classify(file.name);
      const diameter = currentDiameter(cfg, target);
      const density = currentDensity(cfg, target);
      if (shown) {
        diameterUsed = diameter;
        densityUsed = density;
      }

      if (kind === "gcode") {
        const meta = await parseGcodeStream(chunksOf(file));
        if (!current()) return; // a newer drop/pick has since started, discard this result
        const r = resolveGcode(meta, diameter, density);
        if (shown) outcome = { kind: "gcode", result: r };
        clearStaleTime(target, r);
        applyResolvedPrefill(target, r, "gcode", file.name);
        return;
      }

      if (file.size > MAX_MODEL_BYTES) {
        if (shown) readError = describeModelTooLarge(file.name);
        return;
      }

      const buffer = await file.arrayBuffer();
      if (!current()) return;

      if (kind === "stl") {
        const stl = parseStl(buffer);
        if (stl.status === "ok" && stl.volumeCm3 !== undefined) {
          applyEstimate(target, shown, file.name, stl.volumeCm3, density);
        } else if (shown) {
          outcome = { kind: "parse-error", reason: stl.status === "empty" ? "empty" : "malformed" };
        }
        return;
      }

      // .3mf: a sliced archive resolves like g-code; an unsliced one falls back to the STL geometry estimate.
      const tmf = await parseThreeMf(buffer);
      if (!current()) return;
      if (tmf.status === "ok" && tmf.kind === "sliced" && tmf.meta) {
        const r = resolveGcode(tmf.meta, diameter, density);
        if (shown) outcome = { kind: "gcode", result: r };
        clearStaleTime(target, r);
        applyResolvedPrefill(target, r, "gcode", file.name);
      } else if (tmf.status === "ok" && tmf.kind === "geometry" && tmf.volumeCm3 !== undefined) {
        applyEstimate(target, shown, file.name, tmf.volumeCm3, density);
      } else if (shown) {
        outcome = {
          kind: "parse-error",
          reason: tmf.status === "empty" ? "empty" : tmf.status === "too-large" ? "too-large" : "malformed",
        };
      }
    } catch (err) {
      if (!current()) return;
      if (shown) readError = describeReadError(file.name, err instanceof Error ? err.message : String(err));
    } finally {
      if (shown && current()) busy = false;
    }
  }

  /** A mesh-volume estimate landing on `target`: the shown part goes through the solidity-aware banner
   * state; other parts of a multi-file drop use the default solidity. */
  function applyEstimate(target: PartForm, shown: boolean, name: string, volumeCm3: number, density: number) {
    clearStaleTime(target);
    if (shown) {
      outcome = { kind: "estimate", volumeCm3 };
      applyModelEstimate();
      return;
    }
    const e = describeVolumeEstimate(name, volumeCm3, density, DEFAULT_SOLIDITY_PCT);
    applyResolvedPrefill(target, { status: "ok", source: "reported-weight", weightG: e.weightG }, "model", name);
  }

  /** Several files at once: one part per file, labeled from the name. The shown part is reused for the
   * first file while blank; files beyond the order's part limit are ignored with a note. */
  async function handleFiles(files: File[]) {
    if (files.length === 0) return;
    if (files.length === 1) {
      await handleFile(files[0]!, part);
      return;
    }
    const blank = part.weightG === null && part.timeHours === null && part.timeMinutes === null;
    const room = MAX_ORDER_PARTS - quote.parts.length + (blank ? 1 : 0);
    const used = files.slice(0, Math.max(room, 0));
    const dropped = files.length - used.length;
    const jobs: Promise<void>[] = [];
    used.forEach((file, i) => {
      const label = partLabelFromFile(file.name);
      let target: PartForm;
      if (i === 0 && blank) {
        target = part;
        target.label = label;
      } else {
        target = addPart(cfg, { label });
      }
      jobs.push(handleFile(file, target));
    });
    if (dropped > 0) {
      readError = {
        kind: "read-error",
        text: `An order holds at most ${MAX_ORDER_PARTS} parts, so ${dropped} file${dropped === 1 ? " was" : "s were"} ignored.`,
      };
    }
    await Promise.all(jobs);
  }

  // Explicit apply for the "model" source, called only from real events (an estimate outcome landing, the
  // solidity input's oninput), never reactively. A reactive version read weightG/timeHours/printerId/...
  // inside an $effect, making them dependencies: any later edit re-applied the stale estimate, staleness
  // never tripped, and a NaN weight re-triggered itself (NaN !== NaN) until effect_update_depth_exceeded.
  // `estimate` stays a $derived.
  function applyModelEstimate() {
    if (!estimate) return;
    // `source: "reported-weight"` is a bridge value only: applyResolvedPrefill never reads
    // GcodeResult.source, and the wording comes from describeVolumeEstimate.
    applyResolvedPrefill(part, { status: "ok", source: "reported-weight", weightG: estimate.weightG }, "model", fileName);
  }

  // Reads and writes solidityInput from the DOM event instead of pairing bind:value with an oninput handler:
  // two listeners on one "input" event would make applyModelEstimate's timing depend on registration order.
  function onSolidityInput(e: Event) {
    const raw = (e.currentTarget as HTMLInputElement).value;
    solidityInput = raw === "" ? null : Number(raw);
    applyModelEstimate();
  }

  function onDrop(e: DragEvent) {
    e.preventDefault();
    dragging = false;
    const files = Array.from(e.dataTransfer?.files ?? []);
    if (files.length > 0) void handleFiles(files);
  }
  function onDragLeave(e: DragEvent) {
    // Moving over a child (label, input) re-fires dragleave on the container in some browsers; ignore it
    // unless the pointer actually left the zone, or the highlight flickers.
    if (e.currentTarget instanceof Node && e.currentTarget.contains(e.relatedTarget as Node | null)) return;
    dragging = false;
  }
  function onPick(e: Event) {
    const input = e.currentTarget as HTMLInputElement;
    const files = Array.from(input.files ?? []);
    input.value = ""; // allow re-picking the same file to re-parse it
    if (files.length > 0) void handleFiles(files);
  }

  // A drop outside the zone must not let the browser navigate to the raw file and discard the form's state.
  function preventWindowDrop(e: DragEvent) {
    e.preventDefault();
  }
</script>

<svelte:window ondragover={preventWindowDrop} ondrop={preventWindowDrop} />

<!-- The click handler is a mouse-only convenience (click the whole box). The keyboard- and
     screen-reader-accessible path is the labeled file input inside, so no keyboard handler belongs on
     this wrapper. -->
<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<!-- svelte-ignore a11y_click_events_have_key_events -->
<div
  class="card drop"
  class:dragging
  role="group"
  aria-label="Model file drop zone"
  onclick={(e) => {
    // Click anywhere in the box to choose a file. Skip clicks on self-handling controls (the <label>, which
    // would double-fire; the solidity input; buttons and links), so this covers only the dead space around
    // them.
    const t = e.target as HTMLElement;
    if (t.closest("label, input, button, a")) return;
    document.getElementById(inputId)?.click();
  }}
  ondragover={(e) => {
    e.preventDefault();
    dragging = true;
  }}
  ondragleave={onDragLeave}
  ondrop={onDrop}
>
  <label for={inputId}>
    <strong>Drop a .gcode, .stl, or .3mf file</strong> (or click or press Enter to choose one). Several files at once make one part each. A sliced
    file fills in weight and time. An unsliced STL or 3MF gets a weight estimate from its
    geometry. Files are parsed in your browser and never uploaded.
  </label>
  <input
    id={inputId}
    type="file"
    multiple
    accept=".gcode,.gco,.g,.nc,text/*,.stl,.3mf"
    onchange={onPick}
    class="visually-hidden"
  />

  {#if outcome?.kind === "estimate"}
    <label class="solidity-field" for={solidityInputId}>
      Infill (solidity)
      <input
        id={solidityInputId}
        type="number"
        min="5"
        max="100"
        step="1"
        value={solidityInput}
        oninput={onSolidityInput}
      />
      %
    </label>
  {/if}

  <!-- Single always-present live region: screen-reader announcements for freshly inserted live regions
       are unreliable, so swap text, not elements. -->
  <p
    class="status-line {statusKind}"
    role={statusKind === "not-found" || statusKind === "invalid-profile" || statusKind === "read-error" ? "alert" : "status"}
    aria-live={statusKind === "not-found" || statusKind === "invalid-profile" || statusKind === "read-error" ? "assertive" : "polite"}
  >{statusText}</p>
</div>

<style>
  .drop {
    border: 2px dashed var(--control-border);
    text-align: center;
    min-height: 44px;
  }
  .drop.dragging {
    border-color: var(--accent);
    background: var(--surface-2);
  }
  .drop:focus-within {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }
  .drop label {
    display: block;
    cursor: pointer;
    padding: 0.5rem 0;
    /* Capped measure, centered: the card spans the page, so uncapped this sentence ran ~110 characters and
     * the zone read as a paragraph with a border, not a target. */
    max-width: 60ch;
    margin: 0 auto;
  }
  .solidity-field {
    cursor: default;
    font-size: var(--text-sm);
    color: var(--muted);
  }
  .solidity-field input {
    width: 4rem;
    margin: 0 0.3rem;
  }
  /* Two lines tall even when empty, so a parse result replacing "Parsing…" (or clearing) doesn't reflow the
   * fields below. A longer message at a narrow width may still wrap onto a third line. */
  .status-line {
    margin: 0.5rem 0 0;
    min-height: 3em;
  }
  .status-line.ok {
    color: var(--ok);
  }
  /* .not-found and .read-error are blocking severity (--danger); .invalid-profile is advisory (--warn),
   * like .advisory-text. */
  .status-line.not-found,
  .status-line.read-error {
    color: var(--danger);
  }
  .status-line.invalid-profile {
    color: var(--warn);
  }
  .status-line.busy {
    color: var(--muted);
  }
</style>
