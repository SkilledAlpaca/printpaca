<script lang="ts">
  import { escapeBelongsToField } from "../lib/unsavedGuard";
  import { onMount } from "svelte";
  import {
    parseGcodeText,
    resolveGcode,
    type EffectiveConfig,
    type QuoteInputs,
  } from "../lib/engine/index";
  import { computeOrder } from "../lib/quotesApi";
  import {
    loadRequests,
    loadRequest,
    finalizeRequest,
    deleteRequest,
    deleteAllNewRequests,
    fetchRequestGcode,
    fetchRequestsStorage,
    type QuoteRequest,
  } from "../lib/requestsApi";
  import { listClients, createClient } from "../lib/clientsApi";
  import { densityOrFallback } from "../lib/filamentDefaults";
  import { createInvoice } from "../lib/invoicesApi";
  import { invoiceInputFromRequest } from "../lib/convertRequest";
  import { INITIAL_PAGE_LIMIT, refetchLimit } from "../lib/pagination";
  import { startOnePartQuote, engineOptions, applyMaterial, pendingOpen, consumePendingOpen, quoteClient, quoteMeta } from "../lib/state.svelte";
  import { syncDetail } from "../lib/hashRoute";
  import { refreshUnread } from "../lib/requestsBadge.svelte";
  import { splitSeconds } from "../lib/gcodeTime";
  import { buildQuoteInputs, energyInputsFor } from "../lib/quoteInputs";
  import { nextStartWall } from "../lib/engine/schedule";
  import { findDuplicateRequestIds } from "../lib/requestDuplicates";
  import {
    classifyRequestFileState,
    classifyStoredFile,
    describeDroppedFileHint,
    describeStoredModelHint,
  } from "../lib/gcodeMessage";
  import { describeStorageBanner, type StorageBanner } from "../lib/storageBanner";
  import { money, formatDate, formatDateTime } from "../lib/format";
  import { mdiArrowLeft } from "@mdi/js";
  import Icon from "./Icon.svelte";
  import StatusBadge from "./StatusBadge.svelte";
  import AttributionLine from "./AttributionLine.svelte";
  import SettingsEditor from "./settings/SettingsEditor.svelte";
  import PublicQuotesSettings from "./settings/PublicQuotesSettings.svelte";
  import LoadMoreRow from "./LoadMoreRow.svelte";
  import DetailTitle from "./DetailTitle.svelte";
  import { announce, focusAfterUpdate } from "../lib/a11y.svelte";
  import { confirmFocus, focusRowOrHeading, keySelector, neighborId, refocusIfLost, rowSelector } from "../lib/a11yFocus";

  let {
    cfg,
    onOpenInQuote,
    onOpenInvoice,
  }: { cfg: EffectiveConfig; onOpenInQuote: () => void; onOpenInvoice: () => void } = $props();

  // List

  let requests = $state<QuoteRequest[]>([]);
  let loading = $state(true);
  // Same tolerance as ClientsTab/InvoicesTab: a load failure shows a muted note and an empty list.
  let unavailable = $state(false);

  // Storage-capacity banner: fetched once on mount, not per refresh(), since it is advisory info about
  // upload-directory capacity. null before the fetch resolves, on failure, and below the threshold
  // (fetchRequestsStorage/describeStorageBanner), so it never blocks the tab.
  let storageBanner = $state<StorageBanner | null>(null);

  async function loadStorageBanner() {
    const usage = await fetchRequestsStorage();
    storageBanner = usage ? describeStorageBanner(usage.usedBytes, usage.capBytes) : null;
  }

  // Server pagination (server/pagination.go's opt-in ?limit=&offset=): `requests` holds the loaded window and
  // `total` is the pre-slice count (X-Total-Count). This list has no filter box, so there is no backfill
  // concern, only the first page plus Load more.
  let total = $state(0);
  let loadingMore = $state(false);

  // The first load fetches 100; a refetch after a mutation (finalize/delete) asks for at least as many rows
  // as are loaded, so the window doesn't shrink.
  async function refresh() {
    loading = true;
    const limit = refetchLimit(requests.length);
    const result = await loadRequests({ limit, offset: 0 });
    if (result === null) {
      unavailable = true;
      requests = [];
      total = 0;
    } else {
      unavailable = false;
      requests = result.items;
      total = result.total;
    }
    loading = false;
  }

  async function loadMore() {
    loadingMore = true;
    const result = await loadRequests({ limit: INITIAL_PAGE_LIMIT, offset: requests.length });
    if (result !== null) {
      const firstNew = result.items[0];
      requests = [...requests, ...result.items];
      total = result.total;
      // The button that was focused goes away when this page exhausts the list: land on the first new row.
      if (firstNew) void refocusIfLost(rowSelector(firstNew.id));
    }
    loadingMore = false;
  }

  // "Delete all new" (admin recovery from a public-request flood, server/requests.go's handleDeleteNew):
  // visible only while the loaded window has a "new" row. Same two-step confirm as QuotesTab's per-row delete
  // and the same action-status live region for the outcome.
  const hasNewRequests = $derived(requests.some((r) => r.status === "new"));
  let confirmingDeleteAllNew = $state(false);
  let bulkDeleting = $state(false);
  let bulkStatus = $state("");
  let bulkFailed = $state(false);

  function askDeleteAllNew() {
    confirmingDeleteAllNew = true;
  }
  // Which row opened the detail view, so "Back to list" can return focus to it (WCAG 2.4.3).
  let openerId: string | null = null;
  function cancelDeleteAllNew() {
    confirmingDeleteAllNew = false;
  }
  /** After the confirm closes: the "Delete all new" trigger if it is still rendered, else the list heading. */
  function focusDeleteAllOrHeading(): Promise<void> {
    return focusAfterUpdate(
      () => document.querySelector<HTMLElement>(keySelector("delete-all-new")) ?? document.querySelector<HTMLElement>(keySelector("requests-heading")),
    );
  }
  async function removeAllNew() {
    if (bulkDeleting) return;
    // The confirm stays open while the request runs, so its "Deleting…" label shows; it closes when done.
    bulkDeleting = true;
    try {
      const count = await deleteAllNewRequests();
      bulkFailed = false;
      bulkStatus = count === 1 ? "Deleted 1 new request." : `Deleted ${count} new requests.`;
      await refresh();
      await refreshUnread();
      confirmingDeleteAllNew = false;
      void focusDeleteAllOrHeading();
    } catch (err) {
      bulkFailed = true;
      bulkStatus = err instanceof Error ? `Couldn't delete new requests: ${err.message}` : "Couldn't delete new requests.";
      confirmingDeleteAllNew = false;
      void focusDeleteAllOrHeading();
    } finally {
      bulkDeleting = false;
    }
  }

  // Ids of requests that look like a rapid duplicate submission (same email/filename/quantity as an earlier
  // request within 60 minutes); see requestDuplicates.ts.
  const duplicateRequestIds = $derived(findDuplicateRequestIds(requests));

  // Escape closes an open detail view like "Back to list", but cancels an armed two-click delete first.
  // Ignored while focus is in a text field or select, which use Escape themselves (dismissing autocomplete or a dropdown).
  function handleDetailKeydown(e: KeyboardEvent) {
    if (e.key !== "Escape") return;
    if (view !== "detail") return;
    if (escapeBelongsToField(e.target)) return;
    e.preventDefault();
    if (confirmingDelete) {
      cancelDelete();
      return;
    }
    if (confirmingFinalize) {
      cancelFinalize();
      return;
    }
    backToList();
  }

  // Async so the pending-id check runs after `requests` is populated: openDetail's `wasNew` lookup needs the
  // list to know whether to flip a badge count.
  onMount(async () => {
    void loadStorageBanner(); // advisory only, never awaited: must not delay the pending-open flow below
    await refresh();
    // A "#/requests#<id>" deep link (routePendingOpen) sets this before the tab mounts; open that request
    // instead of the list. silent: true so a stale bookmarked id falls back to the list quietly.
    const id = consumePendingOpen("requests");
    if (id) {
      // Set the hash before opening: a hashchange queued by this tab's mount (App's activate()) fires
      // asynchronously and would otherwise see a bare #/requests that doesn't match detail.id and close the
      // just-opened detail. replaceState, because this completes the navigation that brought us here.
      history.replaceState(null, "", `${REQUESTS_HASH}#${id}`);
      openDetail(id, { silent: true });
    }
  });

  // List <-> detail navigation like InvoicesTab: addressable via "#/requests#<id>" (App.svelte's tabFromHash
  // strips it for tab routing; handled locally). Opening pushes a history entry via a guarded location.hash
  // assignment; back/forward and manual edits are picked up by syncDetailFromHash.

  const REQUESTS_HASH = "#/requests";

  function setDetailHash(id: string) {
    const target = `${REQUESTS_HASH}#${id}`;
    if (location.hash !== target) location.hash = target; // pushes a history entry
  }

  // Resets the view state of "closing" without touching the URL: used by backToList() (which owns the URL)
  // and syncDetailFromHash (where location.hash already changed).
  function closeDetailView() {
    view = "list";
    detail = null;
    detailError = null;
    verifyOutcome = null;
    confirmingDelete = false;
    confirmingFinalize = false;
  }

  // Reacts to hash changes while mounted: back/forward between detail and list (or two ids), or a manual edit
  // of the id. A hashchange from this component's own setDetailHash()/backToList() is a no-op, so it never
  // double-fetches.
  function syncDetailFromHash() {
    syncDetail(location.hash, REQUESTS_HASH, detail?.id ?? null, view === "detail", (id) => openDetail(id, { silent: true }), () => {
      closeDetailView();
      void focusRowOrHeading(openerId);
    });
  }

  type View = "list" | "detail";
  let view = $state<View>("list");
  let detail = $state<QuoteRequest | null>(null);
  let detailLoading = $state(false);
  let detailError = $state<string | null>(null);
  let actionError = $state<string | null>(null);
  let convertError = $state<string | null>(null);
  let converting = $state(false);
  // Row awaiting a second click to delete (like confirmingId elsewhere).
  let confirmingDelete = $state(false);

  // Verification replays the visitor's computation, not the admin form's: the current config's public printer
  // and preset, and the material's density (same fallback rule as the public page) for length-derived weight.


  interface VerifyOutcome {
    status: "verified" | "mismatch" | "error";
    message?: string;
    recomputed?: { priceCents: number; weightG: number; timeSeconds: number };
  }
  let verifyOutcome = $state<VerifyOutcome | null>(null);
  let verifying = $state(false);

  // Drives the template's four-way file-section branch: "none", "dropped" (storage full at submit: filename
  // and size survive, no bytes), "pruned" (retention removed the bytes) or "stored". See
  // classifyRequestFileState in gcodeMessage.ts.
  const fileState = $derived(detail ? classifyRequestFileState(detail.gcodeFilename, detail.gcodePath, detail.gcodePrunedAt) : "none");

  // Sub-branch of "stored"/"pruned": g-code gets the full Verify/Retry subcard (click-to-run); STL/3MF gets a
  // neutral line and no verify UI (nothing for verifyRequest to confirm); an unrecognized extension keeps a
  // manual Verify button.
  const storedFileKind = $derived(detail ? classifyStoredFile(detail.gcodeFilename) : "unknown");

  // Staleness guard, same shape as openDetail's openSeq (declared earlier since this reads it): verifyRequest
  // runs from the Verify button and awaits fetchRequestGcode and computeQuote. Without a token, navigating
  // away mid-verify lets the abandoned call throw into the new request's card or land as its verdict, and its
  // `finally` would clear the new request's spinner. Every read below uses `record` (captured at entry),
  // never the live `detail`.
  async function verifyRequest() {
    if (!detail) return;
    const record = detail;
    const seq = openSeq;
    const isCurrent = () => seq === openSeq && detail?.id === record.id;
    verifying = true;
    verifyOutcome = null;
    try {
      const text = await fetchRequestGcode(record.id);
      if (!isCurrent()) return;
      const meta = parseGcodeText(text);

      const printer = cfg.printers.find((p) => p.id === cfg.publicQuotes.printer);
      const preset = cfg.presets[cfg.publicQuotes.preset];
      if (!printer || !preset) {
        verifyOutcome = { status: "error", message: "The public printer or preset in config no longer exists." };
        return;
      }
      const material = cfg.materials.find((m) => m.name === record.material);
      if (!material) {
        verifyOutcome = { status: "error", message: "Material no longer exists in config." };
        return;
      }

      const resolved = resolveGcode(meta, printer.filamentDiameterMm, densityOrFallback(material.density));
      if (resolved.weightG === undefined || resolved.timeSeconds === undefined) {
        verifyOutcome = {
          status: "error",
          message: "Couldn't recompute weight and print time from the stored file.",
        };
        return;
      }
      const weightG = Math.round(resolved.weightG * 100) / 100;
      const timeSeconds = Math.round(resolved.timeSeconds);

      // Same lib/quoteInputs.ts mapping as PublicQuotePage's live estimate, so the two can't drift. Shipping
      // is 0: the visitor's estimate never folds shipping into revenue.
      const inputs: QuoteInputs = buildQuoteInputs({
        weightG,
        pricePerKg: material.pricePerKg,
        wastePct: material.wastePct,
        timeSeconds,
        printer,
        preset,
        // A schedule-mode public quote prices from the next default start after it was submitted.
        energy: energyInputsFor(cfg.energy, undefined, undefined, {
          timezone: cfg.timezone || "UTC",
          start: nextStartWall(cfg.energy.schedule.defaultStartTime, cfg.timezone || "UTC", new Date(record.createdAt)),
        }),
        quantity: record.quantity,
        pricing: {
          strategy: cfg.pricing.strategy,
          marginPct: cfg.pricing.marginPct,
          markupMultiplier: cfg.pricing.markupMultiplier,
          taxRatePct: cfg.pricing.taxRatePct,
          shippingCharge: 0,
          shippingCost: 0,
        },
      });
      const options = engineOptions(cfg);
      // A network or server failure is "verification unavailable", distinct from a mismatch.
      const { pricing, ...partInputs } = inputs;
      const result = await computeOrder({ parts: [{ ...partInputs, label: "Part 1" }], pricing }, options);
      if (!isCurrent()) return;
      if (!result.ok) {
        verifyOutcome = { status: "error", message: "Verification unavailable: couldn't reach the server." };
        return;
      }
      // A one-part order prices exactly like the single quote it equals.
      const order = result.order;
      const recomputedPriceCents =
        order.valid && order.pricing?.valid ? order.pricing.resolvedPrice * order.parts[0]!.breakdown.quantity : null;

      if (recomputedPriceCents === null) {
        verifyOutcome = { status: "error", message: "The recomputed estimate is invalid against the current configuration." };
        return;
      }

      verifyOutcome = {
        status: recomputedPriceCents === record.priceCents ? "verified" : "mismatch",
        recomputed: { priceCents: recomputedPriceCents, weightG, timeSeconds },
      };
    } catch (err) {
      if (!isCurrent()) return;
      verifyOutcome = { status: "error", message: err instanceof Error ? err.message : "Couldn't verify this request." };
    } finally {
      if (isCurrent()) verifying = false;
    }
  }

  // Staleness token: openDetail can re-enter while a fetch is in flight (quick double-open, fast
  // back/forward). A superseded call bails before touching state or the address bar. Same pattern as
  // InvoicesTab.
  let openSeq = 0;

  async function openDetail(id: string, opts?: { silent?: boolean }) {
    const seq = ++openSeq;
    openerId = id;
    view = "detail";
    detail = null;
    detailLoading = true;
    detailError = null;
    actionError = null;
    convertError = null;
    verifyOutcome = null;
    // A prior request's verify can still be in flight (its staleness guard bails once this call bumps
    // openSeq). Reset here too so a newly opened, non-auto-verified request doesn't inherit a spinner nothing
    // drives.
    verifying = false;
    confirmingDelete = false;
    confirmingFinalize = false;
    const wasNew = requests.find((r) => r.id === id)?.status === "new";
    try {
      const loaded = await loadRequest(id); // GET flips new -> viewed server-side
      if (seq !== openSeq) return;
      detail = loaded;
      // No auto-verify: verification parses the visitor's uploaded G-code in this tab, so it runs only when the
      // admin clicks Verify.
      if (wasNew) {
        await refresh();
        if (seq !== openSeq) return;
        await refreshUnread();
        if (seq !== openSeq) return;
      }
      setDetailHash(id);
    } catch (err) {
      if (seq !== openSeq) return;
      if (opts?.silent) {
        // Stale or bookmarked id: fall back to the list quietly instead of an error banner over an empty
        // detail. replaceState, because this corrects a URL that was already wrong.
        closeDetailView();
        if (location.hash !== REQUESTS_HASH) history.replaceState(null, "", REQUESTS_HASH);
      } else {
        detailError = err instanceof Error ? err.message : "Couldn't load this request.";
      }
    } finally {
      if (seq === openSeq) detailLoading = false;
    }
  }

  function backToList(focusId: string | null = openerId) {
    closeDetailView();
    if (location.hash !== REQUESTS_HASH) location.hash = REQUESTS_HASH; // pushes a history entry
    void focusRowOrHeading(focusId);
  }

  // Actions

  let finalizing = $state(false);
  // Finalizing can't be undone (no way back to "viewed") and records a sale for gallery orders, so it asks first (3.3.4).
  let confirmingFinalize = $state(false);
  function askFinalize() {
    confirmingFinalize = true;
  }
  function cancelFinalize() {
    confirmingFinalize = false;
  }
  async function finalize() {
    if (!detail || finalizing) return;
    confirmingFinalize = false;
    finalizing = true;
    actionError = null;
    try {
      detail = await finalizeRequest(detail.id);
      announce("Request finalized.");
      // The Finalize button is gone now; keep focus in the action row.
      void refocusIfLost(".status-actions button");
      await refresh();
      await refreshUnread();
    } catch (err) {
      actionError = err instanceof Error ? err.message : "Couldn't finalize this request.";
    } finally {
      finalizing = false;
    }
  }

  function openInQuoteTab() {
    if (!detail) return;
    // A request opens as a new one-part quote, never into a quote being edited: parts, order block, customer
    // and meta all reset.
    const part = startOnePartQuote(cfg);
    quoteClient.id = "";
    quoteClient.name = "";
    quoteMeta.project = "";
    quoteMeta.reference = "";
    const material = cfg.materials.find((m) => m.name === detail!.material);
    if (material) applyMaterial(cfg, part, material.id);
    part.weightG = detail.weightG;
    const t = splitSeconds(detail.timeSeconds);
    part.timeHours = t.hours;
    part.timeMinutes = t.minutes;
    part.quantity = detail.quantity;
    onOpenInQuote();
  }

  async function convertToInvoice() {
    if (!detail || converting) return;
    convertError = null;
    converting = true;
    try {
      const existing = await listClients();
      const match = (existing ?? []).find((c) => c.email.toLowerCase() === detail!.email.toLowerCase());
      const clientId = match
        ? match.id
        : (await createClient({ name: detail.name, email: detail.email, phone: "", billingAddress: "", shippingAddress: "", notes: "" })).id;

      const input = invoiceInputFromRequest(detail, cfg, clientId);
      const invoice = await createInvoice(input);
      pendingOpen.current = { tab: "invoices", id: invoice.id };
      onOpenInvoice();
    } catch (err) {
      convertError = err instanceof Error ? err.message : "Couldn't convert this request to an invoice.";
    } finally {
      converting = false;
    }
  }

  function askDelete() {
    confirmingDelete = true;
  }

  function cancelDelete() {
    confirmingDelete = false;
  }

  let removing = $state(false);
  async function remove() {
    if (!detail || removing) return;
    removing = true;
    confirmingDelete = false;
    actionError = null;
    const { id, name } = detail;
    const next = neighborId(requests.map((x) => x.id), id);
    try {
      await deleteRequest(id);
      await refresh();
      await refreshUnread();
      backToList(next);
      announce(`Request from ${name} deleted.`);
    } catch (err) {
      actionError = err instanceof Error ? err.message : "Couldn't delete this request.";
      void refocusIfLost(keySelector("delete-request"));
    } finally {
      removing = false;
    }
  }

  // Formatting helpers

  function formatDuration(seconds: number): string {
    const t = splitSeconds(seconds);
    return `${t.hours} h ${t.minutes} min`;
  }

  function formatBytes(bytes: number): string {
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`;
    return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`;
  }
</script>

<svelte:window onhashchange={syncDetailFromHash} onkeydown={handleDetailKeydown} />

<div class="page">
  {#if view === "list"}
    <div class="head-row">
      <h1 tabindex="-1" data-focus-key="requests-heading">Requests</h1>
      {#if hasNewRequests}
        <div class="head-actions">
          {#if confirmingDeleteAllNew}
            <button
              type="button"
              class="danger"
              onclick={removeAllNew}
              aria-disabled={bulkDeleting}
              aria-label="Delete all new? Also deletes their stored files."
              use:confirmFocus={{ restore: keySelector("delete-all-new"), cancel: cancelDeleteAllNew }}
            >{bulkDeleting ? "Deleting…" : "Delete all new?"}</button>
            <button type="button" onclick={cancelDeleteAllNew} aria-label="Cancel deleting all new requests">Cancel</button>
            <span class="advisory-text">Also deletes their stored files.</span>
          {:else}
            <button type="button" data-focus-key="delete-all-new" onclick={askDeleteAllNew} aria-label="Delete all new requests">Delete all new</button>
          {/if}
        </div>
      {/if}
    </div>

    <!-- Live region for removeAllNew's outcome, like QuotesTab's action-status (collapsed while empty;
         alert on failure, status on success). -->
    <p
      class="status-line action-status"
      class:collapsed={bulkStatus === ""}
      role={bulkFailed ? "alert" : "status"}
      aria-live={bulkFailed ? "assertive" : "polite"}
    >
      {bulkStatus}
    </p>

    {#if unavailable}
      <p class="banner-warn" role="alert">Couldn't load requests. The server couldn't be reached.</p>
    {/if}

    {#if storageBanner}
      <!-- Advisory only: absent on fetch failure or under-threshold usage, and never blocks the list. -->
      <p class={storageBanner.level === "full" ? "banner-error" : "banner-warn"} role="alert">{storageBanner.text}</p>
    {/if}

    <div class="card">
      <ul class="plain-list">
        {#if loading}
          <li class="empty-state"><span role="status">Loading…</span></li>
        {:else}
          {#each requests as r (r.id)}
            <li>
              <button type="button" class="row-link" data-row-id={r.id} onclick={() => openDetail(r.id)}>
                <StatusBadge status={r.status} />
                <!-- Name and its optional pills share one grid cell (.r-who): as siblings of the metadata
                     spans they were extra tracks and pushed later columns out of line with rows that
                     lacked them. -->
                <span class="r-who">
                  <span class="r-name">{r.name}</span>
                  {#if r.kind === "item"}<span class="pill item-pill">item order</span>{/if}
                  {#if duplicateRequestIds.has(r.id)}<span class="pill dup-pill">possible duplicate</span>{/if}
                </span>
                <span class="r-material">{r.kind === "item" ? r.itemName : r.material}</span>
                <span class="r-qty">×{r.quantity}</span>
                <span class="r-price">{money(r.priceCents, cfg)}</span>
                <span class="r-date">{formatDate(r.createdAt)}</span>
              </button>
            </li>
          {:else}
            <li class="empty-state">No estimate requests yet. They arrive here when a visitor submits your public estimate page. Set that page up below.</li>
          {/each}
        {/if}
      </ul>
      {#if !loading}
        <LoadMoreRow shown={requests.length} {total} {loadingMore} onLoadMore={loadMore} />
      {/if}
    </div>

    <SettingsEditor>
      {#snippet children({ form, hasError })}
        <PublicQuotesSettings {form} {hasError} />
      {/snippet}
    </SettingsEditor>
  {:else}
    <div class="head-row">
      <button type="button" class="quiet back-link" onclick={() => backToList()}><Icon path={mdiArrowLeft} /> Back to list</button>
      {#if detail}<StatusBadge status={detail.status} />{/if}
    </div>

    {#if detailLoading}
      <p role="status">Loading…</p>
    {:else if detailError}
      <p class="banner-warn" role="alert">{detailError}</p>
    {:else if detail}
      <div class="card">
        <DetailTitle>{detail.kind === "item" ? `Order from ${detail.name}` : `Estimate request from ${detail.name}`}</DetailTitle>
        <AttributionLine updatedBy={detail.updatedBy} />

        <div class="readonly-grid">
          <div><span class="lbl">Name</span><span>{detail.name}</span></div>
          <div><span class="lbl">Email</span><span><a href={`mailto:${encodeURIComponent(detail.email)}`}>{detail.email}</a></span></div>
          {#if detail.kind === "item"}
            <div><span class="lbl">Item</span><span>{detail.itemName}</span></div>
            {#if detail.optionName}<div><span class="lbl">Option</span><span>{detail.optionName}</span></div>{/if}
            <div><span class="lbl">Quantity</span><span>{detail.quantity}</span></div>
            <div><span class="lbl">Total</span><span>{money(detail.priceCents, cfg)}</span></div>
          {:else}
            <div><span class="lbl">Material</span><span>{detail.material}</span></div>
            <div><span class="lbl">Quantity</span><span>{detail.quantity}</span></div>
            <div><span class="lbl">Weight</span><span>{detail.weightG} g</span></div>
            <div><span class="lbl">Print time</span><span>{formatDuration(detail.timeSeconds)}</span></div>
            <div><span class="lbl">Price</span><span>{money(detail.priceCents, cfg)}</span></div>
            {#if detail.shippingCents > 0}<div><span class="lbl">Shipping</span><span>{money(detail.shippingCents, cfg)}</span></div>{/if}
          {/if}
          <div><span class="lbl">Submitted</span><span>{formatDateTime(detail.createdAt)}</span></div>
        </div>

        {#if detail.note}<p class="notes"><span class="lbl">Note</span> {detail.note}</p>{/if}

        {#if detail.kind === "item"}
          <!-- Item orders never carry g-code (server/requests.go): weight, time and gcode are zero/empty,
               so nothing to verify or download. -->
        {:else if fileState === "none"}
          <p class="gcode-line hint">
            <span class="lbl">File</span>
            No file uploaded; weight and print time were entered manually.
          </p>
        {:else if fileState === "dropped"}
          <!-- The upload was accepted but CONFIG_DIR/uploads was at its cap (publicUploadsCapBytes), so
               the bytes never reached disk (gcodePath and gcodePrunedAt both empty). No Download, no
               Verify, and shouldAutoVerifyStoredFile refuses auto-fire. A plain hint, not an alert:
               nothing is actionable on this request; the storage banner is where capacity is surfaced. -->
          <p class="gcode-line hint">
            <span class="lbl">File</span>
            {detail.gcodeFilename} ({formatBytes(detail.gcodeBytes)})
          </p>
          <p class="hint">{describeDroppedFileHint()}</p>
        {:else}
        <p class="gcode-line">
          <span class="lbl">File</span>
          {detail.gcodeFilename} ({formatBytes(detail.gcodeBytes)})
          <a href={`api/requests/${detail.id}/gcode`}>Download</a>
        </p>

        {#if storedFileKind === "model"}
          <!-- An STL/3MF has no slicer comments for verifyRequest to replay, so no Verify button; only an
               explanation of where the weight came from, as a plain hint. -->
          <p class="hint">{describeStoredModelHint()}</p>
        {:else}
        <div class="subcard">
          <h3>Verification</h3>
          {#if verifying}
            <p class="status-line" role="status">Verifying…</p>
          {:else if verifyOutcome === null}
            <button type="button" onclick={verifyRequest}>Verify</button>
          {:else if verifyOutcome.status === "verified"}
            <StatusBadge status="verified" />
            <p class="hint">The recomputed price matches the stored estimate.</p>
          {:else if verifyOutcome.status === "mismatch" && verifyOutcome.recomputed}
            <StatusBadge status="mismatch" />
            <div class="table-scroll">
              <table>
                <thead>
                  <tr><th><span class="visually-hidden">Field</span></th><th class="num">Stored</th><th class="num">Recomputed</th></tr>
                </thead>
                <tbody>
                  <tr>
                    <td>Price</td>
                    <td class="num">{money(detail.priceCents, cfg)}</td>
                    <td class="num">{money(verifyOutcome.recomputed.priceCents, cfg)}</td>
                  </tr>
                  <tr>
                    <td>Weight</td>
                    <td class="num">{detail.weightG} g</td>
                    <td class="num">{verifyOutcome.recomputed.weightG} g</td>
                  </tr>
                  <tr>
                    <td>Time</td>
                    <td class="num">{formatDuration(detail.timeSeconds)}</td>
                    <td class="num">{formatDuration(verifyOutcome.recomputed.timeSeconds)}</td>
                  </tr>
                </tbody>
              </table>
            </div>
            <p class="hint">
              A mismatch can come from configuration changes made after the request was submitted (material price,
              printer, or preset). It isn't necessarily an error.
            </p>
          {:else if verifyOutcome.status === "error"}
            <p class="banner-warn" role="alert">{verifyOutcome.message}</p>
            <button type="button" onclick={verifyRequest}>Retry verification</button>
          {/if}
        </div>
        {/if}
        {/if}

        {#if actionError}<p class="banner-warn" role="alert">{actionError}</p>{/if}
        {#if convertError}<p class="banner-warn" role="alert">{convertError}</p>{/if}

        <p class="hint">
          {#if detail.status === "viewed"}Finalize marks this request as handled. Finalized gallery orders count toward Top items. {/if}Convert to invoice creates the client if this email isn't on file.
        </p>
        <div class="status-actions">
          {#if detail.status === "viewed"}
            {#if confirmingFinalize}
              <button type="button" class="primary" onclick={finalize} use:confirmFocus={{ restore: keySelector("finalize-request"), cancel: cancelFinalize }}>Finalize request from {detail.name}?</button>
              <button type="button" onclick={cancelFinalize}>Cancel</button>
            {:else}
              <button type="button" class="primary" data-focus-key="finalize-request" aria-disabled={finalizing} onclick={askFinalize}>Finalize</button>
            {/if}
          {/if}
          {#if detail.kind !== "item"}
            <button type="button" onclick={openInQuoteTab}>Open in new quote</button>
          {/if}
          <button
            type="button"
            class={detail.status === "viewed" ? "" : "primary"}
            onclick={convertToInvoice}
            aria-disabled={converting}
          >
            {converting ? "Converting…" : "Convert to invoice"}
          </button>
          {#if confirmingDelete}
            <button
              type="button"
              class="danger"
              onclick={remove}
              aria-label="Delete? Also deletes the stored file."
              use:confirmFocus={{ restore: keySelector("delete-request"), cancel: cancelDelete }}
            >Delete?</button>
            <button type="button" onclick={cancelDelete} aria-label="Cancel deleting this request">Cancel</button>
            <span class="advisory-text">Also deletes the stored file.</span>
          {:else}
            <button type="button" data-focus-key="delete-request" onclick={askDelete} aria-label="Delete this request">Delete</button>
          {/if}
        </div>
      </div>
    {/if}
  {/if}
</div>

<style>
  .back-link { display: inline-flex; align-items: center; gap: 0.35rem; }

  /* .head-actions and .action-status.collapsed mirror QuotesTab's rules (component-scoped styles don't
   * cascade); theme.css's .head-row supplies the flex layout, but the actions gap and collapse-when-empty
   * are local. */
  .head-actions { display: flex; align-items: center; gap: 0.4rem; flex-shrink: 0; }
  .action-status.collapsed { min-height: 0; margin-top: calc(var(--space-6) * -1); margin-bottom: calc(var(--space-6) * -1); }

  li { border-top: 1px solid var(--border); }
  li:first-child { border-top: none; }

  /* Grid, not flex: min-width on a flex item is a floor, not a track, so rows sized their metadata cells to
   * their own content and the columns zig-zagged. Fixed tracks align them; the name (minmax(0, 1fr)) is the
   * only part that truncates. Collapses to stacked flow below 700px. */
  .row-link {
    width: 100%;
    display: grid;
    grid-template-columns: auto minmax(0, 1fr) 8rem 3rem 5.5rem 6rem;
    align-items: center;
    gap: 0.75rem;
    background: none;
    border: none;
    padding: 0.5rem 0;
    text-align: left;
    min-height: 32px;
  }
  .row-link:hover { background: var(--surface-2); }
  .r-who { display: flex; align-items: center; gap: 0.5rem; min-width: 0; }
  .r-who .pill { flex-shrink: 0; white-space: nowrap; }
  .r-name { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 600; }
  .r-material { color: var(--muted); font-size: var(--text-sm); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .r-qty { color: var(--muted); font-size: var(--text-sm); text-align: right; }
  .r-price { font-variant-numeric: tabular-nums; text-align: right; }
  .r-date { color: var(--muted); font-size: var(--text-sm); text-align: right; font-variant-numeric: tabular-nums; }
  @media (max-width: 700px) {
    .row-link {
      display: flex;
      flex-wrap: wrap;
    }
  }
  /* .pill (theme) covers border, radius, font-size and padding; the muted color is local, like QuotesTab's
   * .invoiced-badge notes. */
  .dup-pill { color: var(--muted); }
  .item-pill { color: var(--muted); }

  .notes, .gcode-line { font-size: var(--text-sm); margin: 0.3rem 0; }
  .gcode-line a { margin-left: 0.5rem; }

  /* The verification subcard's hint keeps its own margin: the one outlier left local when theme.css's .hint
   * was promoted. */
  .hint { color: var(--muted); font-size: var(--text-sm); margin: 0.4rem 0 0; }
</style>

