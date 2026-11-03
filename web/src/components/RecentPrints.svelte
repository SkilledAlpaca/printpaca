<script module lang="ts">
  // pick()'s metadata fetch is the only fetch here without a deadline on the response body (load()'s
  // AbortSignal.timeout covers up through headers). Combining the caller's abort with a timeout signal means
  // a stalled body still settles the promise instead of wedging `applying`. Exported for
  // RecentPrints.test.ts.
  export function metadataFetchSignal(callerSignal: AbortSignal, timeoutMs: number): AbortSignal {
    return AbortSignal.any([callerSignal, AbortSignal.timeout(timeoutMs)]);
  }
</script>

<script lang="ts">
  import { formatDateTime } from "../lib/format";
  import { onDestroy, onMount } from "svelte";
  import { resolveGcode, type EffectiveConfig } from "../lib/engine/index";
  import { currentDensity, currentDiameter, type PartForm } from "../lib/state.svelte";
  import { splitSeconds } from "../lib/gcodeTime";
  import { describeGcodeResult, type GcodeMessage } from "../lib/gcodeMessage";
  import { DEFAULT_FETCH_TIMEOUT_MS } from "../lib/fetchTimeouts";
  import { applyResolvedPrefill, clearPrefillIfOwnedBy, isPrefillStale, prefillFor } from "../lib/prefill.svelte";
  import {
    isValidMetadata,
    moonrakerHistoryUrl,
    moonrakerJobToRawMeta,
    moonrakerMetadataUrl,
    moonrakerTimeSource,
    parseCompletedJobs,
    parseMetadata,
    type MoonrakerJob,
    type MoonrakerMetadata,
  } from "../lib/moonraker";

  let { cfg, part }: { cfg: EffectiveConfig; part: PartForm } = $props();

  type Status = "loading" | "ok" | "empty" | "error";
  type ApplyMessage = GcodeMessage | { kind: "error"; text: string };

  let jobs = $state<MoonrakerJob[]>([]);
  let status = $state<Status>("loading");
  let errorDetail = $state("");
  let applying = $state(false);
  // Separate from the load status line: picking a job does its own metadata fetch, which can fail after the
  // list loaded fine.
  let applyMessage = $state<ApplyMessage | null>(null);
  // Controlled so re-picking the same job re-fires: onchange fires only on a value change, so pick() resets
  // this to "" once its apply settles.
  let selectedId = $state("");

  // Guards the in-flight metadata fetch: aborted on destroy and when a newer pick supersedes it, so a slow
  // earlier response can't land after a later one.
  let controller: AbortController | null = null;

  async function load() {
    status = "loading";
    errorDetail = "";
    applyMessage = null;
    try {
      const res = await fetch(moonrakerHistoryUrl(), { signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS) });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data: unknown = await res.json();
      jobs = parseCompletedJobs(data);
      status = jobs.length > 0 ? "ok" : "empty";
    } catch (e) {
      jobs = [];
      errorDetail = e instanceof Error ? e.message : "unknown error";
      status = "error";
    }
  }

  onMount(load);
  onDestroy(() => controller?.abort());

  function whenFinished(j: MoonrakerJob): string {
    // > 0, not just !== undefined: Moonraker's zero value is the Unix epoch, which would render as a real,
    // wrong date.
    if (j.end_time === undefined || j.end_time <= 0) return "unknown finish time";
    return formatDateTime(new Date(j.end_time * 1000).toISOString());
  }

  // Labels use the same merged (job, inline metadata) figures moonrakerJobToRawMeta feeds resolveGcode, so
  // the list never advertises a figure (or a misleading "0") the apply step won't use.
  function mergedRaw(j: MoonrakerJob) {
    return moonrakerJobToRawMeta(j, j.metadata ?? {});
  }

  function filamentLabel(j: MoonrakerJob): string {
    const raw = mergedRaw(j);
    const result = resolveGcode(raw, currentDiameter(cfg, part), currentDensity(cfg, part));
    return result.weightG !== undefined ? `${Math.round(result.weightG)} g filament` : "filament weight unknown";
  }

  function durationLabel(j: MoonrakerJob): string {
    const raw = mergedRaw(j);
    if (raw.timeSeconds === undefined) return "duration unknown";
    const { hours, minutes } = splitSeconds(raw.timeSeconds);
    return `${hours} h ${minutes} min`;
  }

  async function pick(jobId: string) {
    controller?.abort();
    controller = null;
    applyMessage = null;

    if (jobId === "") {
      applying = false;
      selectedId = "";
      return;
    }
    const job = jobs.find((j) => j.job_id === jobId);
    if (!job) {
      applying = false;
      selectedId = "";
      return;
    }

    clearPrefillIfOwnedBy(part, "moonraker");
    const myController = new AbortController();
    controller = myController;
    applying = true;
    try {
      let metadata: MoonrakerMetadata;
      if (job.metadata !== undefined && isValidMetadata(job.metadata)) {
        metadata = job.metadata;
      } else {
        const res = await fetch(moonrakerMetadataUrl(job.filename), {
          signal: metadataFetchSignal(myController.signal, DEFAULT_FETCH_TIMEOUT_MS),
        });
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const data: unknown = await res.json();
        metadata = parseMetadata(data);
      }
      if (myController.signal.aborted) return; // superseded while awaiting; discard

      const diameterMm = currentDiameter(cfg, part);
      const density = currentDensity(cfg, part);
      const raw = moonrakerJobToRawMeta(job, metadata);
      const result = resolveGcode(raw, diameterMm, density);
      applyResolvedPrefill(part, result, "moonraker", job.filename);
      const base = describeGcodeResult(result, job.filename, diameterMm, density);
      // describeGcodeResult never names a time source (a dropped file only has the slicer estimate), but
      // Moonraker can feed either print_duration or estimated_time, which can diverge. Named here rather than
      // widening the engine's shared message.
      const timeSource = result.timeSeconds !== undefined ? moonrakerTimeSource(job, metadata) : undefined;
      const timeNote = timeSource === "actual" ? ", actual print time" : timeSource === "slicer-estimate" ? ", slicer-estimated time" : "";
      applyMessage = { ...base, text: `${base.text} (from Moonraker history${timeNote})` };
    } catch (e) {
      if (myController.signal.aborted) return;
      applyMessage = {
        kind: "error",
        text: `Couldn't fetch metadata for ${job.filename}: ${e instanceof Error ? e.message : "unknown error"}.`,
      };
    } finally {
      // Only the still-current attempt may clear `applying` and reset the select; an aborted, superseded
      // call's finally must not clobber a newer pick.
      if (controller === myController) {
        applying = false;
        selectedId = "";
      }
    }
  }

  const statusText = $derived(
    status === "loading"
      ? "Loading recent prints…"
      : status === "error"
        ? `Moonraker unreachable at ${moonrakerHistoryUrl()} (${errorDetail}).`
        : status === "empty"
          ? "No completed prints found in Moonraker's history."
          : "",
  );
  const assertive = $derived(status === "error");

  // The form's weight/time may reflect this picker's last apply or a later GcodeDrop parse
  // (prefill.svelte.ts). Claim the apply message and its staleness suffix only while this picker still owns
  // the values.
  const owns = $derived(prefillFor(part)?.source === "moonraker");
  const stale = $derived(owns && isPrefillStale(part));
  const displayedApplyMessage = $derived(
    !applyMessage
      ? null
      : applyMessage.kind !== "ok"
        ? applyMessage
        : !owns
          ? null
          : stale
            ? { ...applyMessage, text: `${applyMessage.text} (values edited since pick)` }
            : applyMessage,
  );
</script>

<div class="recent-prints">
  <p
    class="status-line {status}"
    role={assertive ? "alert" : "status"}
    aria-live={assertive ? "assertive" : "polite"}
  >{statusText}</p>

  {#if status === "error"}
    <button class="quiet" onclick={load}>Retry</button>
  {/if}

  {#if status === "ok"}
    <label class="field field-inline recent">
      <span>Recent print (from Moonraker)</span>
      <select
        bind:value={selectedId}
        aria-disabled={applying} aria-busy={applying}
        onchange={(e) => {
          // A pick is applying: keep the select focusable (aria-disabled, not disabled) but ignore changes.
          if (applying) {
            e.currentTarget.value = selectedId;
            return;
          }
          pick(e.currentTarget.value);
        }}
      >
        <option value="">Pick a completed print…</option>
        {#each jobs as j (j.job_id)}
          <option value={j.job_id}>{j.filename} · {whenFinished(j)} · {filamentLabel(j)} · {durationLabel(j)}</option>
        {/each}
      </select>
    </label>

    {#if displayedApplyMessage}
      <p
        class="status-line apply-status {displayedApplyMessage.kind}"
        role={displayedApplyMessage.kind === "not-found" || displayedApplyMessage.kind === "error" ? "alert" : "status"}
        aria-live={displayedApplyMessage.kind === "not-found" || displayedApplyMessage.kind === "error" ? "assertive" : "polite"}
      >{displayedApplyMessage.text}</p>
    {/if}
  {/if}
</div>

<style>
  .recent-prints { margin-top: 0.6rem; }
  .recent { margin-top: 0.4rem; }
  .status-line { margin: 0 0 0.4rem; }
  .status-line.error { color: var(--danger); }
  .apply-status.ok { color: var(--ok); }
  .apply-status.not-found,
  .apply-status.error {
    color: var(--danger);
  }
  .apply-status.invalid-profile { color: var(--warn); }
</style>
