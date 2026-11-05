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
  import { money, formatDate, formatDateTime, materialDisplayName, presetDisplayName } from "../lib/format";
  import PageHeader from "./ui/PageHeader.svelte";
  import Card from "./ui/Card.svelte";
  import Field from "./ui/Field.svelte";
  import Fieldset from "./ui/Fieldset.svelte";
  import FormGrid from "./ui/FormGrid.svelte";
  import KeyValueTable, { type KeyValueRow } from "./ui/KeyValueTable.svelte";
  import InlineStatus from "./ui/InlineStatus.svelte";
  import ConfirmInline from "./ui/ConfirmInline.svelte";
  import StatusBadge from "./StatusBadge.svelte";
  import AttributionLine from "./AttributionLine.svelte";
  import SettingsEditor from "./settings/SettingsEditor.svelte";
  import PublicQuotesSettings from "./settings/PublicQuotesSettings.svelte";
  import DataTable, { type DataColumn } from "./ui/DataTable.svelte";
  import { canConfigureShop, canEditQuotes, hasTabAccess, type ConfigUser } from "../lib/permissions";
  import { announce, focusAfterUpdate } from "../lib/a11y.svelte";
  import { confirmFocus, focusOnMount, focusRowOrHeading, keySelector, neighborId, refocusIfLost, rowSelector } from "../lib/a11yFocus";

  let {
    cfg,
    onOpenInQuote,
    onOpenInvoice,
    user = null,
  }: { cfg: EffectiveConfig; onOpenInQuote: () => void; onOpenInvoice: () => void; user?: ConfigUser | null } = $props();

  // What the signed-in user may follow from a request (A13). The server refuses the rest with a 403, so the
  // buttons say so up front instead of failing. Convert creates the client and the invoice, so it needs both.
  const canQuote = $derived(canEditQuotes(user));
  const canInvoices = $derived(hasTabAccess("invoices", user));
  const canConvert = $derived(hasTabAccess("clients", user) && canInvoices);
  const convertReason = $derived(
    canConvert ? "" : `Converting a request creates the client and the invoice. It needs access to ${[hasTabAccess("clients", user) ? "" : "Clients", canInvoices ? "" : "Invoices"].filter(Boolean).join(" and ")}. Ask an admin.`,
  );
  const quoteReason = $derived(canQuote ? "" : "You don't have access to New quote. Ask an admin.");
  const canConfigure = $derived(canConfigureShop(user));

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
  // NEW rows are selectable too: finalizing one opens it first (which marks it viewed), then finalizes it.
  const newRows = $derived(filteredRequests.filter((r) => r.status === "new"));
  // Drop ids that left the list or were finalized (a refresh after finalizing, a filter change).
  $effect(() => {
    const ok = new Set(requests.filter((r) => r.status === "viewed" || r.status === "new").map((r) => r.id));
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
  function selectAllNew() {
    selectedIds = newRows.map((r) => r.id);
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
        // The server finalizes only a viewed request; opening a NEW one is what marks it viewed.
        if (requests.find((r) => r.id === id)?.status === "new") await loadRequest(id);
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
      void refocusIfLost(".detail-actions button");
      await refresh();
      await refreshUnread();
    } catch (err) {
      actionError = err instanceof Error ? err.message : "Couldn't finalize this request.";
    } finally {
      finalizing = false;
    }
  }

  function openInQuoteTab() {
    if (!detail || !canQuote) return;
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
    if (!detail || converting || !canConvert) return;
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

  const COLUMNS: DataColumn[] = [
    { key: "select", label: "Select", hideLabel: true },
    { key: "status", label: "Status" },
    { key: "who", label: "Requester" },
    { key: "what", label: "Item or material" },
    { key: "qty", label: "Qty", num: true },
    { key: "price", label: "Price", num: true },
    { key: "date", label: "Received", num: true },
  ];

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

  // The read-only facts of the open request, in the order the old grid showed them.
  const detailRows = $derived.by((): KeyValueRow[] => {
    const d = detail;
    if (!d) return [];
    const rows: KeyValueRow[] = [
      { label: "Reference", value: requestReference(d) },
      { label: "Name", value: d.name },
      { label: "Email", value: d.email, href: `mailto:${encodeURIComponent(d.email)}` },
    ];
    if (d.kind === "item") {
      rows.push({ label: "Item", value: d.itemName });
      if (d.optionName) rows.push({ label: "Option", value: d.optionName });
      rows.push({ label: "Quantity", value: String(d.quantity) });
      rows.push({ label: "Total", value: money(d.priceCents, cfg), strong: true });
    } else {
      rows.push({ label: "Material", value: materialDisplayName(d.material, cfg.materials) });
      rows.push({ label: "Quantity", value: String(d.quantity) });
      rows.push({ label: "Weight", value: `${d.weightG} g` });
      rows.push({ label: "Print time", value: d.needsPrintTime ? "Not provided" : formatDuration(d.timeSeconds) });
      rows.push({ label: "Price", value: d.needsPrintTime ? "To be confirmed" : money(d.priceCents, cfg), strong: true });
      if (d.shippingCents > 0) rows.push({ label: "Shipping", value: money(d.shippingCents, cfg) });
    }
    rows.push({ label: "Submitted", value: formatDateTime(d.createdAt) });
    return rows;
  });
</script>

<svelte:window onhashchange={syncDetailFromHash} onkeydown={handleDetailKeydown} onpointerdown={handleWindowPointerDown} />

<div class="page">
  {#if view === "list"}
    <h1 tabindex="-1" data-focus-key="requests-heading">Requests</h1>

    {#if confirmingDeleteAllNew}
      <div class="banner-warn confirm-delete-all" role="group" aria-label="Confirm deleting all new requests">
        <p>{deleteAllNewQuestion(newCount)}</p>
        <div class="confirm-actions">
          <button type="button" use:focusOnMount onclick={cancelDeleteAllNew}>Cancel</button>
          <button type="button" class="danger" onclick={removeAllNew} aria-disabled={bulkDeleting}>{bulkDeleting ? "Deleting…" : deleteAllNewConfirmLabel(newCount)}</button>
        </div>
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

    <DataTable
      label="Requests"
      columns={COLUMNS}
      loadMore={!loading && !filterActive ? { shown: requests.length, total, loadingMore, onLoadMore: loadMore } : null}
    >
      {#snippet filters()}
    {#if requests.length > 0}
        <div class="toolbar">
          <Field id="requests-filter" label="Filter requests by name, item, or file" size="l">
            {#snippet children(f)}
              <input
                id={f.id}
                type="search"
                data-focus-key="requests-filter-input"
                placeholder="Name, item, or file"
                bind:value={filterText}
              />
            {/snippet}
          </Field>
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
      {#if viewedRows.length > 0 || newRows.length > 0}
          <div class="bulk-bar">
            {#if selectedIds.length === 0}
              {#if newRows.length > 0}<button type="button" class="quiet" onclick={selectAllNew}>Select all new ({newRows.length})</button>{/if}
              {#if viewedRows.length > 0}<button type="button" class="quiet" onclick={selectAllViewed}>Select all viewed ({viewedRows.length})</button>{/if}
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
                <span class="advisory-text">Marks them handled (new ones are marked viewed first). It doesn't email the customers.</span>
              {:else}
                <button type="button" class="primary" data-focus-key="finalize-selected" onclick={() => (confirmingBulkFinalize = true)}>Finalize selected</button>
                <button type="button" class="quiet" onclick={clearSelection}>Clear selection</button>
              {/if}
            {/if}
          </div>
        {/if}

      {/snippet}
      {#if loading}
        <tr><td colspan="7" class="empty-state"><span role="status">Loading…</span></td></tr>
      {:else}
        {#each filteredRequests as r (r.id)}
          <tr class="req-row clickable-row" onclick={() => openDetail(r.id)}>
            <td class="c-check" onclick={(e) => e.stopPropagation()}>
              {#if r.status === "viewed" || r.status === "new"}
                <label class="check-label">
                  <input
                    type="checkbox"
                    aria-label={`Select request from ${r.name}`}
                    checked={selectedIds.includes(r.id)}
                    onchange={() => toggleSelected(r.id)}
                  />
                </label>
              {/if}
            </td>
            <td class="c-status"><StatusBadge status={requestDisplayStatus(r)} /></td>
            <td class="c-who">
              <span class="r-who">
                <button type="button" class="row-link" data-row-id={r.id} onclick={(e) => { e.stopPropagation(); openDetail(r.id); }}>{r.name}</button>
                {#if r.reference}<span class="pill ref-pill">{r.reference}</span>{/if}
                {#if r.kind === "item"}<span class="pill item-pill">item order</span>{/if}
                {#if r.needsPrintTime}<span class="pill time-pill">Needs print time</span>{/if}
                {#if isInvoiced(r)}<span class="pill item-pill">{r.invoiceNumber}</span>{/if}
                {#if duplicateRequestIds.has(r.id)}<span class="pill dup-pill">possible duplicate</span>{/if}
              </span>
            </td>
            <td class="c-material">{r.kind === "item" ? r.itemName : materialDisplayName(r.material, cfg.materials)}</td>
            <td class="num c-qty">×{r.quantity}</td>
            <td class="num c-price">{r.needsPrintTime ? "To be confirmed" : money(r.priceCents, cfg)}</td>
            <td class="num c-date">{formatDate(r.createdAt)}</td>
          </tr>
        {:else}
          <tr><td colspan="7" class="empty-state">{requests.length === 0 ? "No estimate requests yet. They arrive here when a visitor submits your public estimate page. Set that page up below." : "No requests match the filter. Clear it, or choose All, to see every request."}</td></tr>
        {/each}
      {/if}
    </DataTable>

    {#if canConfigure}
      <SettingsEditor>
        {#snippet children({ form, hasError })}
          <PublicQuotesSettings {form} {hasError} />
        {/snippet}
      </SettingsEditor>
    {:else}
      <!-- Only an admin can save these (the server refuses the rest), so everyone else gets the values to read. -->
      <Card heading="Public estimates">
        <p class="hint">Visitor estimates are priced with the {cfg.printers.find((p) => p.id === cfg.publicQuotes.printer)?.name ?? cfg.publicQuotes.printer} printer and the {presetDisplayName(cfg.publicQuotes.preset)} preset. Only an admin can change this.</p>
      </Card>
    {/if}
  {:else}
    <PageHeader
      record
      title={detail ? (detail.kind === "item" ? `Order from ${detail.name}` : `Estimate request from ${detail.name}`) : undefined}
      onback={() => backToList()}
    >
      {#snippet status()}{#if detail}<StatusBadge status={requestDisplayStatus(detail)} />{/if}{/snippet}
      {#snippet subtitle()}{#if detail}<AttributionLine updatedBy={detail.updatedBy} />{/if}{/snippet}
    </PageHeader>

    {#if detailLoading}
      <InlineStatus kind="saving">Loading…</InlineStatus>
    {:else if detailError}
      <InlineStatus kind="error">{detailError}</InlineStatus>
    {:else if detail}
      <Card>
        <KeyValueTable rows={detailRows} label="Request details" />

        {#if detail.note}<p class="notes"><span class="lbl">Note</span> {detail.note}</p>{/if}

        {#if isInvoiced(detail)}
          <p class="invoiced-note">
            Invoiced as {detail.invoiceNumber} (request {requestReference(detail)}).
            {#if canInvoices}
              <button type="button" class="quiet linkish" onclick={() => openLinkedInvoice(detail!.invoiceId)}>Open invoice</button>
            {:else}
              <span class="hint">You don't have access to Invoices, so it can't be opened here.</span>
            {/if}
          </p>
        {/if}

        {#if detail.needsPrintTime}
          <!-- One compact notice: a heading line, one sentence that also says why Finalize and Convert to
               invoice are off, and the print time form in one row. -->
          <Card tone="inset" class="time-notice" role="group" aria-labelledby="print-time-heading">
            <h3 id="print-time-heading">This estimate needs a print time</h3>
            <p id="print-time-note" class="hint">
              The customer's file had no print time, so there's no price yet; enter it from your slicer, because Finalize and Convert to invoice need it.
            </p>
            <form
              class="time-form"
              aria-label="Print time"
              onsubmit={(e) => {
                e.preventDefault();
                void savePrintTime();
              }}
            >
              <Field id="print-time-hours" label="Hours" size="xs">
                {#snippet children(f)}
                  <input id={f.id} type="number" min="0" step="1" bind:value={timeHours} aria-describedby={f.describedby} />
                {/snippet}
              </Field>
              <Field id="print-time-minutes" label="Minutes" size="xs">
                {#snippet children(f)}
                  <input id={f.id} type="number" min="0" max="59" step="1" bind:value={timeMinutes} aria-describedby={f.describedby} />
                {/snippet}
              </Field>
              <button type="submit" class="primary" aria-disabled={savingTime} aria-busy={savingTime}>{savingTime ? "Saving…" : "Save print time"}</button>
            </form>
            {#if timeError}<InlineStatus kind="error">{timeError}</InlineStatus>{/if}
          </Card>
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
            <InlineStatus kind="error">{verifyOutcome.message}</InlineStatus>
            <button type="button" onclick={verifyRequest}>Retry verification</button>
          {/if}
        </div>
        {/if}
        {/if}

        {#if actionError}<InlineStatus kind="error">{actionError}</InlineStatus>{/if}
        {#if convertError}<InlineStatus kind="error">{convertError}</InlineStatus>{/if}

        {#if detail.status === "viewed" || !isInvoiced(detail)}
          <p class="hint">
            {#if detail.status === "viewed"}Finalize marks this request as handled in your list. It doesn't email the customer; use Reply by email to tell them. Finalized shop item orders count toward Top items.{" "}{/if}{#if !isInvoiced(detail)}Convert to invoice creates the client if this email isn't on file, then marks the request invoiced.{/if}
          </p>
        {/if}
        <div class="detail-actions">
          {#if detail.status === "viewed"}
            {#if confirmingFinalize}
              <ConfirmInline
                confirmLabel="Yes, finalize this request"
                restore={keySelector("finalize-request")}
                onconfirm={finalize}
                oncancel={cancelFinalize}
              >Finalizing can't be undone.</ConfirmInline>
            {:else}
              <button
                type="button"
                class="primary"
                data-focus-key="finalize-request"
                aria-disabled={finalizing || detail.needsPrintTime}
                aria-describedby={detail.needsPrintTime ? "print-time-note" : undefined}
                onclick={askFinalize}
              >Finalize</button>
            {/if}
          {/if}
          <a class="reply-link" href={replyMailto(detail)} aria-label={`Reply by email to ${detail.name}, reference ${requestReference(detail)}`}>Reply by email</a>
          {#if detail.kind !== "item"}
            <button
              type="button"
              onclick={openInQuoteTab}
              aria-disabled={!canQuote}
              aria-describedby={canQuote ? undefined : "open-quote-reason"}
            >Open in new quote</button>
          {/if}
          {#if !isInvoiced(detail)}
            <button
              type="button"
              class={detail.status === "viewed" ? "" : "primary"}
              data-focus-key="convert-request"
              onclick={convertToInvoice}
              aria-disabled={converting || detail.needsPrintTime || !canConvert}
              aria-describedby={!canConvert ? "convert-no-access" : detail.needsPrintTime ? "print-time-note" : undefined}
            >
              {converting ? "Converting…" : "Convert to invoice"}
            </button>
          {/if}
          {#if confirmingDelete}
            <ConfirmInline
              danger
              confirmLabel="Yes, delete this request"
              restore={keySelector("delete-request")}
              onconfirm={remove}
              oncancel={cancelDelete}
            >Also deletes the stored file. This can't be undone.</ConfirmInline>
          {:else}
            <button type="button" data-focus-key="delete-request" onclick={askDelete} aria-label="Delete this request">Delete</button>
          {/if}
        </div>
        {#if !canQuote && detail.kind !== "item"}<p id="open-quote-reason" class="hint">{quoteReason}</p>{/if}
        {#if !canConvert && !isInvoiced(detail)}<p id="convert-no-access" class="hint">{convertReason}</p>{/if}
      </Card>
    {/if}
  {/if}
</div>

<style>
  .toolbar { display: flex; gap: var(--space-2); align-items: end; }
  .toolbar > :global(.ui-field) { flex: 1; }
  .more { position: relative; flex-shrink: 0; }
  .menu-toggle { min-width: var(--tap-sm); font-weight: 700; letter-spacing: 1px; }
  /* Not position: fixed like QuotesTab's row menu: this one sits beside the filter, outside any scroll
   * container, so an absolute panel can't be clipped. */
  .menu-panel {
    position: absolute;
    z-index: 5;
    right: 0;
    top: calc(100% + 4px);
    min-width: var(--field-s);
    padding: var(--space-1);
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: calc(var(--radius) / 2);
    box-shadow: var(--shadow);
  }
  .menu-group { display: flex; flex-direction: column; gap: var(--space-1); }
  .menu-item { display: block; width: 100%; text-align: left; white-space: nowrap; }

  .confirm-delete-all p { margin: 0 0 var(--space-2); }
  .confirm-actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }
  .bulk-bar { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); }
  .bulk-count { font-size: var(--text-sm); font-weight: 600; }

  .check-label { display: flex; align-items: center; justify-content: center; min-width: 44px; min-height: 44px; cursor: pointer; }
  .check-label input { width: var(--space-5); height: var(--space-5); margin: 0; }
  .time-pill { color: var(--warn); }

  .invoiced-note {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-2);
    margin: var(--gap) 0 0;
    padding: var(--space-2) var(--space-3);
    border-radius: calc(var(--radius) / 2);
    background: var(--inset-bg);
    color: var(--muted);
    font-size: var(--text-sm);
  }
  .invoiced-note .linkish { border: none; background: none; color: var(--accent-text); font-weight: 600; text-decoration: underline; }
  /* The compact print-time notice: hours, minutes and Save in one row, wrapping on a phone. */
  .time-form { display: flex; flex-wrap: wrap; align-items: flex-end; gap: var(--space-2) var(--gap); }
  :global(.time-notice > .hint) { margin: 0 0 var(--space-2); }
  :global(.time-notice > h3) { margin: 0 0 var(--space-1); }
  h3 { margin-top: 0; }

  /* The record's actions sit under the facts with spacing only (dividers belong to tables and lists). */
  .detail-actions { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); margin-top: var(--gap); }

  /* "Reply by email" is a mailto: link, so it is an <a>; styled like the neighboring buttons. */
  .reply-link {
    display: inline-flex;
    align-items: center;
    min-height: var(--control-h);
    padding: 0 var(--space-3);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    color: var(--text);
    text-decoration: none;
  }
  .reply-link:hover { background: var(--surface-2); }

  /* .action-status.collapsed mirrors QuotesTab's rule (component-scoped styles don't cascade): collapse the
   * live region while it is empty. */
  .action-status.collapsed { min-height: 0; margin-top: calc(var(--gap-section) * -1); margin-bottom: calc(var(--gap-section) * -1); }

  .clickable-row { cursor: pointer; }
  td.empty-state { text-align: center; }
  .c-check { width: 1%; padding-block: 0; }
  .c-status, .c-material { white-space: nowrap; }
  .r-who { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); min-width: 0; }
  .r-who .pill { flex-shrink: 0; white-space: nowrap; }
  /* The requester's name is the row's primary link. min-height: 0 keeps the button from inflating the row. */
  .row-link { background: none; border: none; padding: 0; min-height: 0; font-weight: 600; color: var(--accent-text); text-align: left; overflow-wrap: anywhere; }
  .row-link:hover { text-decoration: underline; }
  .c-material, .c-qty, .c-date { color: var(--muted); font-size: var(--text-sm); }
  .c-material { max-width: var(--field-s); overflow: hidden; text-overflow: ellipsis; }

  /* Phones: each request becomes a card: select, requester and price on top, then status and date, then what
   * was asked for and the quantity. DataTable hides the header row and makes the table a block list. */
  @media (max-width: 600px) {
    tr.req-row {
      display: grid;
      grid-template-columns: auto minmax(0, 1fr) auto;
      grid-template-areas: "check who price" "check status date" "check material qty";
      align-items: center;
      gap: var(--space-1) var(--space-3);
      padding: var(--space-2) 0;
      border-bottom: 1px solid var(--border);
    }
    .c-check { grid-area: check; width: auto; }
    .c-who { grid-area: who; }
    td.c-price { grid-area: price; text-align: right; font-weight: 600; }
    td.c-status { grid-area: status; }
    td.c-date { grid-area: date; text-align: right; }
    .c-material { grid-area: material; max-width: none; white-space: normal; }
    .c-qty { grid-area: qty; text-align: right; }
    .empty-state { grid-column: 1 / -1; }
  }
  /* .pill (theme) covers border, radius, font-size and padding; the muted color is local, like QuotesTab's
   * .invoiced-badge notes. */
  .dup-pill { color: var(--muted); }
  .item-pill { color: var(--muted); }
  .ref-pill { color: var(--muted); font-variant-numeric: tabular-nums; }

  .notes, .gcode-line { font-size: var(--text-sm); margin: var(--space-2) 0; }
  .gcode-line a { margin-left: var(--space-2); }

  /* The verification subcard's hint keeps its own margin: the one outlier left local when theme.css's .hint
   * was promoted. */
  .hint { color: var(--muted); font-size: var(--text-sm); margin: var(--space-3) 0 0; }
</style>

