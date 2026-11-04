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
  import { announce, focusAfterUpdate } from "../lib/a11y.svelte";
  import Field from "./ui/Field.svelte";
  import { resolveGcode, type EffectiveConfig } from "../lib/engine/index";
  import { currentDensity, currentDiameter, type PartForm } from "../lib/state.svelte";
  import { splitSeconds } from "../lib/gcodeTime";
  import { describeGcodeResult, type GcodeMessage } from "../lib/gcodeMessage";
  import { DEFAULT_FETCH_TIMEOUT_MS } from "../lib/fetchTimeouts";
  import { applyResolvedPrefill, clearPrefillIfOwnedBy, isPrefillStale, prefillFor } from "../lib/prefill.svelte";
  import {
    HISTORY_PAGE_SIZE,
    filterJobs,
    isValidMetadata,
    moonrakerHistoryUrl,
    moonrakerJobToRawMeta,
    moonrakerMetadataUrl,
    moonrakerTimeSource,
    parseHistoryPage,
    parseMetadata,
    type MoonrakerJob,
    type MoonrakerMetadata,
  } from "../lib/moonraker";

  let { cfg, part }: { cfg: EffectiveConfig; part: PartForm } = $props();

  type Status = "loading" | "ok" | "empty" | "error";
  type ApplyMessage = GcodeMessage | { kind: "error"; text: string };

  let jobs = $state<MoonrakerJob[]>([]);
  // Jobs of any status fetched so far: the `start` offset of the next page, since only completed ones are kept.
  let fetchedRaw = $state(0);
  let hasMore = $state(false);
  let loadingMore = $state(false);
  let filter = $state("");
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

  async function fetchPage(start: number) {
    const res = await fetch(moonrakerHistoryUrl(HISTORY_PAGE_SIZE, start), { signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS) });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const data: unknown = await res.json();
    return parseHistoryPage(data);
  }

  // A full page may be followed by more; a short page, or reaching Moonraker's reported total, is the end.
  function moreAfter(page: { rawCount: number; total: number | null }, fetchedTotal: number): boolean {
    if (page.rawCount < HISTORY_PAGE_SIZE) return false;
    return page.total === null || fetchedTotal < page.total;
  }

  async function load() {
    status = "loading";
    errorDetail = "";
    applyMessage = null;
    try {
      const page = await fetchPage(0);
      jobs = page.jobs;
      fetchedRaw = page.rawCount;
      hasMore = moreAfter(page, fetchedRaw);
      status = jobs.length > 0 || hasMore ? "ok" : "empty";
    } catch (e) {
      jobs = [];
      fetchedRaw = 0;
      hasMore = false;
      errorDetail = e instanceof Error ? e.message : "unknown error";
      status = "error";
    }
  }

  async function loadMore() {
    if (loadingMore) return;
    loadingMore = true;
    try {
      const page = await fetchPage(fetchedRaw);
      const seen = new Set(jobs.map((j) => j.job_id));
      jobs = [...jobs, ...page.jobs.filter((j) => !seen.has(j.job_id))];
      fetchedRaw += page.rawCount;
      hasMore = moreAfter(page, fetchedRaw);
      announce(`Loaded ${page.jobs.length} more ${page.jobs.length === 1 ? "print" : "prints"}.`);
      if (!hasMore) void focusAfterUpdate("#recent-prints-filter");
    } catch (e) {
      announce(`Couldn't load more prints: ${e instanceof Error ? e.message : "unknown error"}.`, "assertive");
    } finally {
      loadingMore = false;
    }
  }

  onMount(load);
  onDestroy(() => controller?.abort());

  function whenFinished(j: MoonrakerJob): string {
    // > 0, not just !== undefined: Moonraker's zero value is the Unix epoch, which would render as a real,
    // wrong date.
    if (j.end_time === undefined || j.end_time <= 0) return "end time unknown";
    return `ended ${formatDateTime(new Date(j.end_time * 1000).toISOString())}`;
  }

  // Labels use the same merged (job, inline metadata) figures moonrakerJobToRawMeta feeds resolveGcode, so
  // the list never advertises a figure (or a misleading "0") the apply step won't use.
  function mergedRaw(j: MoonrakerJob) {
    return moonrakerJobToRawMeta(j, j.metadata ?? {});
  }

  function filamentLabel(j: MoonrakerJob): string {
    const raw = mergedRaw(j);
    const result = resolveGcode(raw, currentDiameter(cfg, part), currentDensity(cfg, part));
    if (result.weightG === undefined) return "filament weight unknown";
    // Name where the figure comes from: the length-based guess here can differ from the slicer's weight that
    // picking the print fills in, and one unlabeled "9 g" next to a filled 42.5 g read as a bug.
    return result.source === "reported-weight"
      ? `${Math.round(result.weightG)} g filament (slicer)`
      : `about ${Math.round(result.weightG)} g filament (from length used)`;
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

  const visibleJobs = $derived(filterJobs(jobs, filter));
  // One always-present live region: the match count updates as the filter changes.
  const matchText = $derived(
    status !== "ok"
      ? ""
      : filter.trim() === ""
        ? `${jobs.length} completed ${jobs.length === 1 ? "print" : "prints"} loaded.`
        : `${visibleJobs.length} of ${jobs.length} loaded ${jobs.length === 1 ? "print matches" : "prints match"}.`,
  );

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
    <Field id="recent-prints-filter" label="Filter recent prints by file name" size="m">
      {#snippet children(a)}
        <input id={a.id} type="search" bind:value={filter} autocomplete="off" />
      {/snippet}
    </Field>
    <p class="advisory-text match-text" role="status" aria-live="polite">{matchText}</p>
    <Field id="recent-prints-select" label="Recent print (from Moonraker)">
      {#snippet children(a)}
        <select
          id={a.id}
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
          {#each visibleJobs as j (j.job_id)}
            <option value={j.job_id}>{j.filename} · {j.status} · {whenFinished(j)} · {filamentLabel(j)} · {durationLabel(j)}</option>
          {/each}
        </select>
      {/snippet}
    </Field>
    <p class="advisory-text weight-note">
      "From length used" is a rough guess from how much filament was fed. Picking a print fills in the slicer's own weight when Moonraker has it.
    </p>
    {#if hasMore}
      <button type="button" class="quiet load-more" onclick={loadMore} aria-disabled={loadingMore} aria-busy={loadingMore}>
        {loadingMore ? "Loading…" : `Load more prints (${jobs.length} loaded)`}
      </button>
    {/if}

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
  .recent-prints { display: flex; flex-direction: column; align-items: flex-start; gap: var(--space-2); }
  .recent-prints > :global(.ui-field) { align-self: stretch; }
  .match-text, .weight-note, .status-line { margin: 0; }
  .status-line.error { color: var(--danger); }
  .apply-status.ok { color: var(--ok); }
  .apply-status.not-found,
  .apply-status.error {
    color: var(--danger);
  }
  .apply-status.invalid-profile { color: var(--warn); }
</style>
