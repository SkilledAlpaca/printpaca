<script lang="ts">
  import { escapeBelongsToField } from "../lib/unsavedGuard";
  import { onMount, tick } from "svelte";
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
    setRequestPrintTime,
    fetchRequestGcode,
    fetchRequestsStorage,
    type QuoteRequest,
  } from "../lib/requestsApi";
  import { listClients, createClient, deleteClient } from "../lib/clientsApi";
  import { densityOrFallback } from "../lib/filamentDefaults";
  import { createInvoice } from "../lib/invoicesApi";
  import { convertRequestToInvoice } from "../lib/convertRequest";
  import { finalizeBlockedReason, splitBulkFinalize } from "../lib/requestFinalize";
  import { INITIAL_PAGE_LIMIT, refetchLimit } from "../lib/pagination";
  import { startOnePartQuote, engineOptions, applyMaterial, pendingOpen, consumePendingOpen, quoteClient, quoteMeta } from "../lib/state.svelte";
  import { syncDetail } from "../lib/hashRoute";
  import { refreshUnread, unreadRequests } from "../lib/requestsBadge.svelte";
  import { createBackfillController } from "../lib/backfillController.svelte";
  import { handleMenuKeydown } from "../lib/menuNav";
  import {
    REQUEST_FILTER_CHIPS,
    deleteAllNewConfirmLabel,
    deleteAllNewQuestion,
    isInvoiced,
    matchesRequestFilter,
    replyMailto,
    requestDisplayStatus,
    requestReference,
    type RequestFilter,
  } from "../lib/requestRow";
  import BackfillStatus from "./BackfillStatus.svelte";
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
  import { confirmFocus, focusOnMount, focusRowOrHeading, keySelector, neighborId, refocusIfLost, rowSelector } from "../lib/a11yFocus";

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
  // `total` is the pre-slice count (X-Total-Count). The filter backfills the rest of the list before it runs
  // (backfillController), so it never filters a partial window; Load more is shown only while unfiltered.
  let total = $state(0);
  let loadingMore = $state(false);

  let filterText = $state("");
  let filterChip = $state<RequestFilter>("all");
  const filterActive = $derived(filterText.trim() !== "" || filterChip !== "all");
  const filteredRequests = $derived(filterActive ? requests.filter((r) => matchesRequestFilter(r, filterText, filterChip)) : requests);
  const backfill = createBackfillController<QuoteRequest>({
    getItems: () => requests,
    getTotal: () => total,
    fetchPage: (page) => loadRequests(page),
    setItems: (items) => {
      requests = items;
      total = items.length;
    },
  });
  $effect(() => {
    backfill.ensureReady(filterActive ? `${filterChip}|${filterText}` : "");
  });

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

  // "Delete all new" (admin recovery from a public-request flood, server/requests.go's handleDeleteNew) lives
  // in the "More" menu below the heading, not beside it as a first Tab stop. Its confirm is a block of its own
  // that states the count and that the stored files go too, with the confirm button away from the menu that
  // opened it, and focus starting on Cancel so a double Enter can't delete anything.
  const loadedNewCount = $derived(requests.filter((r) => r.status === "new").length);
  // The badge count covers the whole list; the loaded window may hold fewer while a long list is paginated.
  const newCount = $derived(Math.max(unreadRequests.count, loadedNewCount));
  let confirmingDeleteAllNew = $state(false);
  let bulkDeleting = $state(false);
  let bulkStatus = $state("");
  let bulkFailed = $state(false);

  // "More" menu: one panel, closed by an outside click, Escape, or Tab. Like QuotesTab's row menu, a labeled
  // group of buttons with arrow-key navigation (menuNav.ts), not an ARIA menu.
  let moreOpen = $state(false);
  let moreToggleEl: HTMLElement | undefined = $state();
  async function toggleMore() {
    if (moreOpen) {
      closeMore();
      return;
    }
    moreOpen = true;
    await tick();
    document.querySelector<HTMLElement>("#requests-more-menu .menu-item:not([disabled])")?.focus();
  }
  function closeMore(restoreFocus = true) {
    moreOpen = false;
    if (restoreFocus) moreToggleEl?.focus();
  }
  function handleMoreFocusOut(e: FocusEvent) {
    const next = e.relatedTarget;
    if (!(next instanceof Node)) return;
    if (e.currentTarget instanceof Node && e.currentTarget.contains(next)) return;
    closeMore(false);
  }
  function handleWindowPointerDown(e: PointerEvent) {
    if (!moreOpen) return;
    if (e.target instanceof Element && e.target.closest("[data-more-menu]")) return;
    closeMore(false);
  }

  function askDeleteAllNew() {
    closeMore(false);
    confirmingDeleteAllNew = true;
  }
  // Which row opened the detail view, so "Back to list" can return focus to it (WCAG 2.4.3).
  let openerId: string | null = null;
  function cancelDeleteAllNew() {
    confirmingDeleteAllNew = false;
    void focusAfterUpdate(() => document.querySelector<HTMLElement>(keySelector("requests-more")));
  }
  /** After the confirm closes: the More button if it is still rendered, else the list heading. */
  function focusMoreOrHeading(): Promise<void> {
    return focusAfterUpdate(
      () => document.querySelector<HTMLElement>(keySelector("requests-more")) ?? document.querySelector<HTMLElement>(keySelector("requests-heading")),
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
      void focusMoreOrHeading();
    } catch (err) {
      bulkFailed = true;
      bulkStatus = err instanceof Error ? `Couldn't delete new requests: ${err.message}` : "Couldn't delete new requests.";
      confirmingDeleteAllNew = false;
      void focusMoreOrHeading();
    } finally {
      bulkDeleting = false;
    }
  }

  // Bulk finalize: row checkboxes on the Requests list only. Finalizing needs a viewed request (the server
  // wants the owner to have looked first), so only viewed rows have a checkbox; each selected request is
  // finalized with the same endpoint as the detail view's Finalize, one after another.
  let selectedIds = $state<string[]>([]);
  let confirmingBulkFinalize = $state(false);
  let bulkFinalizing = $state(false);
  const viewedRows = $derived(filteredRequests.filter((r) => r.status === "viewed"));
  // Drop ids that left the list or are no longer viewed (a refresh after finalizing, a filter change).
  $effect(() => {
    const ok = new Set(requests.filter((r) => r.status === "viewed").map((r) => r.id));
    if (selectedIds.some((id) => !ok.has(id))) selectedIds = selectedIds.filter((id) => ok.has(id));
  });
  function toggleSelected(id: string) {
    selectedIds = selectedIds.includes(id) ? selectedIds.filter((x) => x !== id) : [...selectedIds, id];
    confirmingBulkFinalize = false;
  }
  function selectAllViewed() {
    selectedIds = viewedRows.map((r) => r.id);
    confirmingBulkFinalize = false;
  }
  function clearSelection() {
    selectedIds = [];
    confirmingBulkFinalize = false;
  }
  async function finalizeSelected() {
    if (bulkFinalizing || selectedIds.length === 0) return;
    bulkFinalizing = true;
    // Rows still waiting for a print time can't be finalized (the server refuses them): skip and say so.
    const { ready: ids, skipped } = splitBulkFinalize([...selectedIds], requests);
    let done = 0;
    let firstError = "";
    for (const id of ids) {
      try {
        await finalizeRequest(id);
        done++;
      } catch (err) {
        firstError ||= err instanceof Error ? err.message : "Couldn't finalize a request.";
      }
    }
    bulkFailed = firstError !== "" || skipped.length > 0;
    bulkStatus =
      `Finalized ${done} ${done === 1 ? "request" : "requests"}.` +
      (skipped.length > 0 ? ` Skipped ${skipped.length} that still ${skipped.length === 1 ? "needs" : "need"} a print time.` : "") +
      (firstError ? ` ${ids.length - done} couldn't be finalized: ${firstError}` : "");
    confirmingBulkFinalize = false;
    selectedIds = [];
    await refresh();
    await refreshUnread();
    bulkFinalizing = false;
    void focusAfterUpdate(() => document.querySelector<HTMLElement>(keySelector("requests-filter-input")) ?? document.querySelector<HTMLElement>(keySelector("requests-heading")));
  }

  // Ids of requests that look like a rapid duplicate submission (same email/filename/quantity as an earlier
  // request within 60 minutes); see requestDuplicates.ts.
  const duplicateRequestIds = $derived(findDuplicateRequestIds(requests));

  // Escape closes an open detail view like "Back to list", but cancels an armed two-click delete first.
  // Ignored while focus is in a text field or select, which use Escape themselves (dismissing autocomplete or a dropdown).
  function handleDetailKeydown(e: KeyboardEvent) {
    if (moreOpen) {
      if (e.key === "Escape") {
        e.preventDefault();
        closeMore();
        return;
      }
      const panel = document.getElementById("requests-more-menu");
      if (panel && e.target instanceof Node && panel.contains(e.target)) handleMenuKeydown(e, panel, closeMore);
      return;
    }
    if (e.key !== "Escape") return;
    if (confirmingDeleteAllNew && view === "list") {
      e.preventDefault();
      cancelDeleteAllNew();
      return;
    }
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
    timeError = null;
    timeHours = 0;
    timeMinutes = 0;
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
  // Finalizing can't be undone (no way back to "viewed") and records a sale for shop item orders, so it asks first (3.3.4).
  let confirmingFinalize = $state(false);
  function askFinalize() {
    if (detail && finalizeBlockedReason(detail)) return;
    confirmingFinalize = true;
  }
  function cancelFinalize() {
    confirmingFinalize = false;
  }
  async function finalize() {
    if (!detail || finalizing || finalizeBlockedReason(detail)) return;
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
    // and meta all reset. A request still waiting for its print time opens with the time blank.
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

  // The print time of a public estimate submitted without one (needsPrintTime). Entered here, priced by the
  // server from the current settings, and required before Convert to invoice.
  let timeHours = $state(0);
  let timeMinutes = $state(0);
  let savingTime = $state(false);
  let timeError = $state<string | null>(null);
  async function savePrintTime() {
    if (!detail || savingTime) return;
    const seconds = Math.round((Number(timeHours) || 0) * 3600 + (Number(timeMinutes) || 0) * 60);
    if (seconds < 60) {
      timeError = "Enter the print time from your slicer, at least one minute.";
      return;
    }
    savingTime = true;
    timeError = null;
    try {
      detail = await setRequestPrintTime(detail.id, seconds);
      announce(`Print time saved. The price is ${money(detail.priceCents, cfg)}.`);
      await refresh();
      void focusAfterUpdate(() => document.querySelector<HTMLElement>(keySelector("convert-request")));
    } catch (err) {
      timeError = err instanceof Error ? err.message : "Couldn't save the print time.";
    } finally {
      savingTime = false;
    }
  }

  function openLinkedInvoice(id: string) {
    pendingOpen.current = { tab: "invoices", id };
    onOpenInvoice();
  }

  async function convertToInvoice() {
    if (!detail || converting) return;
    if (detail.needsPrintTime) {
      convertError = "Enter the print time first. Convert to invoice needs a price.";
      return;
    }
    convertError = null;
    converting = true;
    try {
      // The server links the request to the invoice and finalizes it in this same call (requestId); a client
      // created for a conversion that then fails is removed again.
      const invoice = await convertRequestToInvoice(detail, cfg, { listClients, createClient, deleteClient, createInvoice });
      void refreshUnread();
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

<svelte:window onhashchange={syncDetailFromHash} onkeydown={handleDetailKeydown} onpointerdown={handleWindowPointerDown} />

<div class="page">
  {#if view === "list"}
    <div class="head-row">
      <h1 tabindex="-1" data-focus-key="requests-heading">Requests</h1>
    </div>

    {#if requests.length > 0}
      <div class="toolbar">
        <input
          id="requests-filter"
          class="search-input"
          data-focus-key="requests-filter-input"
          placeholder="Filter by name, item, or file"
          aria-label="Filter requests by name, item, or file"
          bind:value={filterText}
        />
        {#if newCount > 0}
          <div class="more" data-more-menu onfocusout={handleMoreFocusOut}>
            <button
              type="button"
              class="quiet menu-toggle"
              bind:this={moreToggleEl}
              data-focus-key="requests-more"
              aria-expanded={moreOpen}
              aria-controls="requests-more-menu"
              aria-label="More actions for requests"
              onclick={toggleMore}
            >⋯</button>
            {#if moreOpen}
              <div class="menu-panel" id="requests-more-menu">
                <div class="menu-group" role="group" aria-label="More actions for requests">
                  <button type="button" class="quiet menu-item" onclick={askDeleteAllNew}>Delete all new…</button>
                </div>
              </div>
            {/if}
          </div>
        {/if}
      </div>
      <div class="chips" role="group" aria-label="Filter by status">
        {#each REQUEST_FILTER_CHIPS as chip (chip.key)}
          <button
            type="button"
            class:active={filterChip === chip.key}
            aria-pressed={filterChip === chip.key}
            onclick={() => (filterChip = chip.key)}
          >{chip.label}</button>
        {/each}
      </div>
      <BackfillStatus backfilling={backfill.backfilling} backfillFailed={backfill.backfillFailed} verb="filter" />
    {/if}

    {#if confirmingDeleteAllNew}
      <div class="banner-warn confirm-delete-all" role="group" aria-label="Confirm deleting all new requests">
        <p>{deleteAllNewQuestion(newCount)}</p>
        <div class="confirm-actions">
          <button type="button" use:focusOnMount onclick={cancelDeleteAllNew}>Cancel</button>
          <button type="button" class="danger" onclick={removeAllNew} aria-disabled={bulkDeleting}>{bulkDeleting ? "Deleting…" : deleteAllNewConfirmLabel(newCount)}</button>
        </div>
      </div>
    {/if}

    {#if viewedRows.length > 0}
      <div class="bulk-bar">
        {#if selectedIds.length === 0}
          <button type="button" class="quiet" onclick={selectAllViewed}>Select all viewed ({viewedRows.length})</button>
        {:else}
          <span class="bulk-count" role="status">{selectedIds.length} selected</span>
          {#if confirmingBulkFinalize}
            <button
              type="button"
              class="primary"
              aria-disabled={bulkFinalizing}
              onclick={finalizeSelected}
              use:confirmFocus={{ restore: keySelector("finalize-selected"), cancel: () => (confirmingBulkFinalize = false) }}
            >{bulkFinalizing ? "Finalizing…" : `Yes, finalize ${selectedIds.length} ${selectedIds.length === 1 ? "request" : "requests"}`}</button>
            <button type="button" onclick={() => (confirmingBulkFinalize = false)}>Cancel</button>
            <span class="advisory-text">Marks them handled. It doesn't email the customers.</span>
          {:else}
            <button type="button" class="primary" data-focus-key="finalize-selected" onclick={() => (confirmingBulkFinalize = true)}>Finalize selected</button>
            <button type="button" class="quiet" onclick={clearSelection}>Clear selection</button>
          {/if}
        {/if}
      </div>
    {/if}

    <!-- Live region for the bulk actions' outcome, like QuotesTab's action-status (collapsed while empty;
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
          {#each filteredRequests as r (r.id)}
            <li class="req-row">
              <span class="r-check">
                {#if r.status === "viewed"}
                  <label class="check-label">
                    <input
                      type="checkbox"
                      aria-label={`Select request from ${r.name}`}
                      checked={selectedIds.includes(r.id)}
                      onchange={() => toggleSelected(r.id)}
                    />
                  </label>
                {/if}
              </span>
              <button type="button" class="row-link" data-row-id={r.id} onclick={() => openDetail(r.id)}>
                <StatusBadge status={requestDisplayStatus(r)} />
                <!-- Name and its optional pills share one grid cell (.r-who): as siblings of the metadata
                     spans they were extra tracks and pushed later columns out of line with rows that
                     lacked them. -->
                <span class="r-who">
                  <span class="r-name">{r.name}</span>
                  {#if r.reference}<span class="pill ref-pill">{r.reference}</span>{/if}
                  {#if r.kind === "item"}<span class="pill item-pill">item order</span>{/if}
                  {#if r.needsPrintTime}<span class="pill time-pill">Needs print time</span>{/if}
                  {#if isInvoiced(r)}<span class="pill item-pill">{r.invoiceNumber}</span>{/if}
                  {#if duplicateRequestIds.has(r.id)}<span class="pill dup-pill">possible duplicate</span>{/if}
                </span>
                <span class="r-material">{r.kind === "item" ? r.itemName : r.material}</span>
                <span class="r-qty">×{r.quantity}</span>
                <span class="r-price">{r.needsPrintTime ? "To be confirmed" : money(r.priceCents, cfg)}</span>
                <span class="r-date">{formatDate(r.createdAt)}</span>
              </button>
            </li>
          {:else}
            <li class="empty-state">{requests.length === 0 ? "No estimate requests yet. They arrive here when a visitor submits your public estimate page. Set that page up below." : "No requests match the filter. Clear it, or choose All, to see every request."}</li>
          {/each}
        {/if}
      </ul>
      {#if !loading && !filterActive}
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
      {#if detail}<StatusBadge status={requestDisplayStatus(detail)} />{/if}
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
          <div><span class="lbl">Reference</span><span>{requestReference(detail)}</span></div>
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
            <div><span class="lbl">Print time</span><span>{detail.needsPrintTime ? "Not provided" : formatDuration(detail.timeSeconds)}</span></div>
            <div><span class="lbl">Price</span><span>{detail.needsPrintTime ? "To be confirmed" : money(detail.priceCents, cfg)}</span></div>
            {#if detail.shippingCents > 0}<div><span class="lbl">Shipping</span><span>{money(detail.shippingCents, cfg)}</span></div>{/if}
          {/if}
          <div><span class="lbl">Submitted</span><span>{formatDateTime(detail.createdAt)}</span></div>
        </div>

        {#if detail.note}<p class="notes"><span class="lbl">Note</span> {detail.note}</p>{/if}

        {#if isInvoiced(detail)}
          <p class="banner-muted invoiced-note">
            Invoiced as {detail.invoiceNumber} (request {requestReference(detail)}).
            <button type="button" class="quiet linkish" onclick={() => openLinkedInvoice(detail!.invoiceId)}>Open invoice</button>
          </p>
        {/if}

        {#if detail.needsPrintTime}
          <div class="subcard time-card" role="group" aria-labelledby="print-time-heading">
            <h3 id="print-time-heading">This estimate needs a print time</h3>
            <p class="hint">
              The customer's file had no print time, so there's no price yet. Enter the print time from your slicer and
              printpaca prices the request from your current settings. You need it before you can convert the request
              to an invoice.
            </p>
            <form
              class="time-form"
              onsubmit={(e) => {
                e.preventDefault();
                void savePrintTime();
              }}
            >
              <label class="field">
                <span>Hours</span>
                <input type="number" min="0" step="1" bind:value={timeHours} />
              </label>
              <label class="field">
                <span>Minutes</span>
                <input type="number" min="0" max="59" step="1" bind:value={timeMinutes} />
              </label>
              <button type="submit" class="primary" aria-disabled={savingTime} aria-busy={savingTime}>{savingTime ? "Saving…" : "Save print time"}</button>
            </form>
            {#if timeError}<p class="banner-warn" role="alert">{timeError}</p>{/if}
          </div>
        {/if}

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
          {#if detail.status === "viewed"}Finalize marks this request as handled in your list. It doesn't email the customer; use Reply by email to tell them. Finalized shop item orders count toward Top items.{" "}{/if}Convert to invoice creates the client if this email isn't on file, then marks the request invoiced.
        </p>
        <div class="status-actions">
          {#if detail.status === "viewed"}
            {#if confirmingFinalize}
              <button type="button" class="primary" onclick={finalize} use:confirmFocus={{ restore: keySelector("finalize-request"), cancel: cancelFinalize }}>Yes, finalize this request</button>
              <button type="button" onclick={cancelFinalize}>Cancel</button>
            {:else}
              <button
                type="button"
                class="primary"
                data-focus-key="finalize-request"
                aria-disabled={finalizing || detail.needsPrintTime}
                aria-describedby={detail.needsPrintTime ? "finalize-needs-time" : undefined}
                onclick={askFinalize}
              >Finalize</button>
            {/if}
          {/if}
          <a class="reply-link" href={replyMailto(detail)} aria-label={`Reply by email to ${detail.name}, reference ${requestReference(detail)}`}>Reply by email</a>
          {#if detail.kind !== "item"}
            <button type="button" onclick={openInQuoteTab}>Open in new quote</button>
          {/if}
          {#if !isInvoiced(detail)}
            <button
              type="button"
              class={detail.status === "viewed" ? "" : "primary"}
              data-focus-key="convert-request"
              onclick={convertToInvoice}
              aria-disabled={converting || detail.needsPrintTime}
              aria-describedby={detail.needsPrintTime ? "convert-needs-time" : undefined}
            >
              {converting ? "Converting…" : "Convert to invoice"}
            </button>
          {/if}
          {#if confirmingDelete}
            <button
              type="button"
              class="danger"
              onclick={remove}
              use:confirmFocus={{ restore: keySelector("delete-request"), cancel: cancelDelete }}
            >Yes, delete this request</button>
            <button type="button" onclick={cancelDelete}>Cancel</button>
            <span class="advisory-text">Also deletes the stored file.</span>
          {:else}
            <button type="button" data-focus-key="delete-request" onclick={askDelete} aria-label="Delete this request">Delete</button>
          {/if}
        </div>
        {#if detail.needsPrintTime}
          <p id="finalize-needs-time" class="hint">{finalizeBlockedReason(detail)}.</p>
          <p id="convert-needs-time" class="hint">Enter the print time above before converting this request to an invoice.</p>
        {/if}
      </div>
    {/if}
  {/if}
</div>

<style>
  .back-link { display: inline-flex; align-items: center; gap: 0.35rem; }

  .toolbar { display: flex; gap: 0.5rem; align-items: center; }
  .search-input { flex: 1; min-width: 0; }
  .more { position: relative; flex-shrink: 0; }
  .menu-toggle { min-width: var(--tap-sm); font-weight: 700; letter-spacing: 1px; }
  /* Not position: fixed like QuotesTab's row menu: this one sits beside the filter, outside any scroll
   * container, so an absolute panel can't be clipped. */
  .menu-panel {
    position: absolute;
    z-index: 5;
    right: 0;
    top: calc(100% + 4px);
    min-width: 11rem;
    padding: 0.3rem;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: calc(var(--radius) / 2);
    box-shadow: var(--shadow);
  }
  .menu-group { display: flex; flex-direction: column; gap: 0.1rem; }
  .menu-item { display: block; width: 100%; text-align: left; white-space: nowrap; }

  .confirm-delete-all p { margin: 0 0 var(--space-2); }
  .confirm-actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }
  .bulk-bar { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); }
  .bulk-count { font-size: var(--text-sm); font-weight: 600; }

  .req-row { display: grid; grid-template-columns: 2.75rem minmax(0, 1fr); align-items: center; }
  .r-check { display: flex; justify-content: center; }
  .check-label { display: flex; align-items: center; justify-content: center; min-width: 44px; min-height: 44px; cursor: pointer; }
  .check-label input { width: 1.25rem; height: 1.25rem; margin: 0; }
  .time-pill { color: var(--warn); }

  .invoiced-note { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); }
  .invoiced-note .linkish { border: none; background: none; color: var(--accent-text); font-weight: 600; text-decoration: underline; }
  .time-form { display: flex; flex-wrap: wrap; align-items: flex-end; gap: var(--space-3); }
  .time-form .field { margin: 0; }
  .time-form input { width: 6rem; }
  .time-card h3 { margin-top: 0; }

  /* "Reply by email" is a mailto: link, so it is an <a>; styled like the neighboring buttons. */
  .reply-link {
    display: inline-flex;
    align-items: center;
    min-height: 44px;
    padding: 0 var(--space-3);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    color: var(--text);
    text-decoration: none;
  }
  .reply-link:hover { background: var(--surface-2); }

  /* .action-status.collapsed mirrors QuotesTab's rule (component-scoped styles don't cascade): collapse the
   * live region while it is empty. */
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
  .ref-pill { color: var(--muted); font-variant-numeric: tabular-nums; }

  .notes, .gcode-line { font-size: var(--text-sm); margin: 0.3rem 0; }
  .gcode-line a { margin-left: 0.5rem; }

  /* The verification subcard's hint keeps its own margin: the one outlier left local when theme.css's .hint
   * was promoted. */
  .hint { color: var(--muted); font-size: var(--text-sm); margin: 0.4rem 0 0; }
</style>

