<script lang="ts">
  import { onMount, tick } from "svelte";
  import type { EffectiveConfig } from "../lib/engine/index";
  import { type SavedQuote } from "../lib/api";
  import {
    listQuotes,
    createQuote,
    updateQuoteGuarded,
    deleteQuote,
    toSavedQuote,
    orderForQuote,
    type ServerQuote,
    type QuoteInput,
  } from "../lib/quotesApi";
  import { toOrderInputs, toFormState, parseFormState, engineOptions, pendingOpen, startNewQuote } from "../lib/state.svelte";
  import { buildQuotesFile, parseQuotesFile, sortQuotes, type QuoteSortOrder } from "../lib/quotesFile";
  import { formatSavedAt, quoteRowStats } from "../lib/quoteRow";
  import { money } from "../lib/format";
  import { createInvoice, listInvoices } from "../lib/invoicesApi";
  import { listClients, createClient, updateClient, type Client } from "../lib/clientsApi";
  import { quoteHasPrice, convertTaxBp, convertPreview, extractQuantity, convertClientName, isMultiPart } from "../lib/convertQuote";
  import {
    applyTransition,
    displayStatus,
    TRANSITION_LABELS,
    TRANSITION_HINTS,
    STATUS_MARKED_MESSAGE,
    type QuoteStatus,
  } from "../lib/quoteStatus";
  import { buildQuoteSendPayload, sendQuoteEmail } from "../lib/sendApi";
  import { sendWithEnteredEmail, type CreatedClients } from "../lib/walkInSend";
  import { clientEmailFor, emailDisabledReason, needsEmailEntry, validEntryEmail, canManageClientsFor } from "../lib/quoteEmail";
  import { canConfigureShop, canEditQuotes, type ConfigUser } from "../lib/permissions";
  import { computeMenuAnchor } from "../lib/menuAnchor";
  import { handleMenuKeydown } from "../lib/menuNav";
  import { matchesQuoteFilter, QUOTE_STATUS_CHIPS, type QuoteStatusFilter } from "../lib/quoteFilter";
  import type { OrderBreakdown } from "../lib/engine/index";
  import { INITIAL_PAGE_LIMIT } from "../lib/pagination";
  import { createBackfillController } from "../lib/backfillController.svelte";
  import StatusBadge from "./StatusBadge.svelte";
  import AttributionLine from "./AttributionLine.svelte";
  import DataTable, { type DataColumn } from "./ui/DataTable.svelte";
import Field from "./ui/Field.svelte";
import FormGrid from "./ui/FormGrid.svelte";
  import BackfillStatus from "./BackfillStatus.svelte";
  import { focusAfterUpdate } from "../lib/a11y.svelte";
  import { confirmFocus, focusOnMount, focusRowOrHeading, keySelector, neighborId, refocusIfLost, rowSelector } from "../lib/a11yFocus";

  let { cfg, user = null }: { cfg: EffectiveConfig; user?: ConfigUser | null } = $props();
  const canManageClients = $derived(canManageClientsFor(user));
  // Staff without the quote grant can list and read saved quotes but not edit or create them (permissions.ts).
  const canEdit = $derived(canEditQuotes(user));
  const canConfigure = $derived(canConfigureShop(user));

  // The full quote list with filter, sort, export/import, convert, duplicate and delete. `quotes` holds the
  // raw server shape because status-only mutations re-PUT the quote with inputs/options/formState echoed back
  // unchanged (the server recomputes outputs), which needs the exact stored bytes; toSavedQuote's `inputs` is
  // the form snapshot, not that engine bag. `mappedQuotes` derives the SavedQuote shape for display, filter,
  // sort and read.
  let quotes = $state<ServerQuote[]>([]);
  const mappedQuotes = $derived(quotes.map(toSavedQuote));
  // Set once from a null listQuotes() (load failure); a muted note, not a blocking error, like ClientsTab.
  let quotesUnavailable = $state(false);
  let loading = $state(true);

  // Server pagination (server/pagination.go's opt-in ?limit=&offset=): `quotes` holds the loaded window and
  // `total` is the pre-slice count (X-Total-Count).
  let total = $state(0);
  const fullyLoaded = $derived(quotes.length >= total);
  let loadingMore = $state(false);

  let filterText = $state("");
  // Status chips, composing (AND) with the text filter like the Invoices list.
  let statusFilter = $state<QuoteStatusFilter>("all");
  // Session-only: never persisted, applied after filtering for display, and never reorders `quotes`.
  let sortOrder = $state<QuoteSortOrder>("newest");
  // The filter appears only once the list is long enough to need it.
  const showFilter = $derived(quotes.length > 5);
  const filterActive = $derived(filterText.trim() !== "" || statusFilter !== "all");
  // The filter matches the label or the client name, so "Casey" finds Casey's quotes.
  const filteredQuotes = $derived(
    filterActive
      ? mappedQuotes.filter((q) => matchesQuoteFilter(q, filterText, statusFilter, cfg.quote.validDays, Date.now()))
      : mappedQuotes,
  );
  const sortedQuotes = $derived(sortQuotes(filteredQuotes, sortOrder));

  // The filter must never run over a partially-loaded list: with text in the box and the list not fully
  // loaded, backfill every remaining row first (shared latch, see backfillController.svelte.ts). Re-runs per
  // keystroke via the $effect; ensureReady is a no-op once fullyLoaded, backfilling, failed or blank.
  const backfill = createBackfillController<ServerQuote>({
    getItems: () => quotes,
    getTotal: () => total,
    fetchPage: (page) => listQuotes(page),
    setItems: (items) => {
      quotes = items;
      total = items.length;
    },
  });
  $effect(() => {
    backfill.ensureReady(filterActive ? `${statusFilter}|${filterText}` : "");
  });

  async function loadMore() {
    loadingMore = true;
    const result = await listQuotes({ limit: INITIAL_PAGE_LIMIT, offset: quotes.length });
    if (result !== null) {
      const firstNew = result.items[0];
      quotes = [...quotes, ...result.items];
      total = result.total;
      // The button that was focused goes away when this page exhausts the list: land on the first new row.
      if (firstNew) void refocusIfLost(rowSelector(firstNew.id));
    }
    loadingMore = false;
  }

  // This surface's own action-status live region, separate from HistoryPanel's: the two never render
  // together, but each needs its own .action-status hook so e2e/history.spec.ts can select the surface that
  // owns the action.
  let actionStatus = $state("");
  let actionFailed = $state(false);

  // Row awaiting a second click to delete (null = none). Declared with the other state because openConvert()
  // and duplicate() also clear it: starting a convert or duplicate dismisses a pending delete confirmation on
  // another row.
  let confirmingId = $state<string | null>(null);

  // Row overflow menu, at most one open: Email quote, Duplicate, the non-primary status transition and Delete
  // live here; Open in editor, the primary transition and Convert to invoice stay inline. Closed by an
  // outside click (data-row-menu), any scroll or Escape (Escape first cancels an armed delete confirmation).
  // Positioned with position: fixed from a captured viewport rect (menuAnchor), not absolute: .table-scroll
  // (overflow-x: auto) computes overflow-y to auto too, so an absolute dropdown near the table's bottom would
  // be clipped. No ancestor sets transform/filter/will-change/contain, so nothing traps the fixed panel.
  let menuOpenId = $state<string | null>(null);
  // top is set when the panel opens downward; bottom when flipped upward near the viewport bottom. Exactly
  // one is present, matched by the template.
  let menuAnchor = $state<{ top?: number; bottom?: number; right: number } | null>(null);
  // The button that opened the menu, so closeMenu() returns focus to it instead of a hidden element.
  let menuToggleEl: HTMLElement | null = null;
  // Close-on-scroll must ignore scroll events queued before the menu opened (a scroll bringing the toggle
  // into view is delivered after toggleMenu ran). Armed one rAF after opening: scroll events fire before rAF
  // callbacks within a frame, so the stale one is swallowed.
  let menuScrollArmed = false;

  // Rough worst-case panel size (~5 buttons plus the email-reason line), used only to decide whether to flip
  // upward or nudge from the right edge. Overestimating flips a little early; underestimating would clip at a
  // viewport edge.
  const MENU_ESTIMATED_HEIGHT = 260;
  const MENU_ESTIMATED_WIDTH = 200;
  const MENU_VIEWPORT_MARGIN = 8;

  async function toggleMenu(id: string, toggleEl: HTMLElement) {
    if (menuOpenId === id) {
      closeMenu();
      return;
    }
    const rect = toggleEl.getBoundingClientRect();
    // clientWidth (not window.innerWidth) excludes the scrollbar gutter, so the right offset lines up with
    // the rendered viewport.
    const viewportWidth = document.documentElement.clientWidth;
    const viewportHeight = window.innerHeight;

    menuAnchor = computeMenuAnchor(
      rect,
      { width: viewportWidth, height: viewportHeight },
      { estimatedHeight: MENU_ESTIMATED_HEIGHT, estimatedWidth: MENU_ESTIMATED_WIDTH, margin: MENU_VIEWPORT_MARGIN },
    );
    menuOpenId = id;
    menuToggleEl = toggleEl;
    menuScrollArmed = false;
    requestAnimationFrame(() => {
      menuScrollArmed = true;
    });

    // Focus the first enabled item once the panel has rendered (tick() flushes the state writes above).
    await tick();
    const firstItem = document.querySelector<HTMLElement>(`[data-row-menu="${id}"] .menu-group .menu-item:not([disabled])`);
    firstItem?.focus();
  }
  function closeMenu(restoreFocus = true) {
    const toggle = menuToggleEl;
    menuOpenId = null;
    menuAnchor = null;
    menuToggleEl = null;
    if (restoreFocus) toggle?.focus();
  }
  // Keyboard focus tabbing out of an open panel closes it without stealing focus back (WCAG 2.4.11: a fixed
  // panel left open could cover the next focused row). relatedTarget is null when the focused item was just
  // removed (a confirm swap), which must not close the menu.
  function handleMenuFocusOut(e: FocusEvent) {
    const next = e.relatedTarget;
    if (!(next instanceof Node)) return;
    if (e.currentTarget instanceof Node && e.currentTarget.contains(next)) return;
    closeMenu(false);
  }
  function handleWindowPointerDown(e: PointerEvent) {
    if (menuOpenId === null) return;
    if (e.target instanceof Element && e.target.closest(`[data-row-menu="${menuOpenId}"]`)) return;
    closeMenu();
  }
  function handleWindowScroll(e: Event) {
    if (!menuScrollArmed) return;
    // The browser scrolls the panel itself to reveal a focused item; that must not close it.
    if (e.target instanceof Element && e.target.closest(".menu-panel")) return;
    closeMenu();
  }
  function handleWindowKeydown(e: KeyboardEvent) {
    // Arrow keys, Home and End move between the open panel's items; Tab closes it with focus on the toggle.
    if (menuOpenId !== null && e.key !== "Escape") {
      const panel = document.getElementById(`row-menu-${menuOpenId}`);
      if (panel && e.target instanceof Node && panel.contains(e.target)) handleMenuKeydown(e, panel, closeMenu);
      return;
    }
    if (e.key !== "Escape") return;
    if (menuOpenId === null) return;
    // Scoped to the open menu: confirmingId and menuOpenId are independent (toggleMenu/closeMenu never touch
    // confirmingId), so a confirmation armed in a menu since dismissed by an outside click must not intercept
    // Escape meant for another open menu. The confirm button exists only in the open panel.
    if (confirmingId !== null && confirmingId === menuOpenId) {
      cancelDelete();
      return;
    }
    closeMenu();
  }

  // Client list shared by every row's convert picker: loaded lazily on first open. A failed load degrades to
  // walk-in-only rather than blocking conversion.
  let clients = $state<Client[] | null>(null);
  let clientsLoaded = $state(false);
  let clientsPromise: Promise<Client[] | null> | null = null;
  async function ensureClientsLoaded(): Promise<void> {
    clientsPromise ??= listClients();
    clients = await clientsPromise;
    clientsLoaded = true;
  }

  // Invoice numbers, loaded with the quotes list, so a row's invoicedAs (an invoice id) shows "Invoiced
  // <number>" without a per-row fetch. A row whose id isn't in the map (load failed, invoice deleted) falls
  // back to a plain "Invoiced" badge.
  let invoiceNumberById = $state<Map<string, string>>(new Map());
  function invoicedLabel(invoiceId: string): string {
    const number = invoiceNumberById.get(invoiceId);
    return number ? `Invoiced ${number}` : "Invoiced";
  }

  onMount(async () => {
    // Not awaited: the ledger's "Email quote" disabled-reason needs every row's linked-client email up front,
    // unlike the convert picker which loads clients when a convert opens. It runs alongside the
    // quotes/invoices fetch.
    ensureClientsLoaded();
    // The primary list fetches its first page of 100; the invoice-number lookup stays full-fetch (no page
    // argument) since it only resolves invoicedAs to a number for loaded quotes.
    const [list, invoices] = await Promise.all([listQuotes({ limit: INITIAL_PAGE_LIMIT, offset: 0 }), listInvoices()]);
    if (list === null) {
      quotesUnavailable = true;
    } else {
      quotes = list.items;
      total = list.total;
    }
    invoiceNumberById = new Map((invoices ?? []).map((inv) => [inv.id, inv.number]));
    loading = false;
  });

  /** Restores a saved quote into the Quote tab's form like HistoryPanel's "Load", then switches tabs.
   * HistoryPanel owns the restore (allowlisted copy onto `form`, printer/material/fee-schedule fallbacks);
   * this hands off the id via pendingOpen (tab: "quote") so that logic isn't forked. */
  function openInEditor(q: SavedQuote) {
    pendingOpen.current = { tab: "quote", id: q.id };
    location.hash = "#/quote";
  }

  function openInvoiced(q: SavedQuote) {
    if (!q.invoicedAs) return;
    pendingOpen.current = { tab: "invoices", id: q.invoicedAs };
    location.hash = "#/invoices";
  }

  // Transitions offered per row: draft gets Mark sent and Mark accepted; sent gets Mark accepted and Mark
  // declined; accepted/declined get none. Keyed on stored status, not the derived "expired", since
  // acceptance/decline beats expiry (quoteStatus.ts).
  const TRANSITIONS: Record<QuoteStatus, QuoteStatus[]> = {
    draft: ["sent", "accepted"],
    sent: ["accepted", "declined"],
    accepted: [],
    declined: [],
  };
  function transitionsFor(q: SavedQuote): QuoteStatus[] {
    return TRANSITIONS[q.status];
  }
  // The row keeps only the most relevant transition inline; TRANSITIONS lists options most relevant first, so
  // this is where the array is sliced.
  function primaryTransition(q: SavedQuote): QuoteStatus | null {
    return transitionsFor(q)[0] ?? null;
  }
  function overflowTransitions(q: SavedQuote): QuoteStatus[] {
    return transitionsFor(q).slice(1);
  }

  /** Everything client-writable in a raw server quote as a QuoteInput ready to PUT: the common start for
   * every re-PUT, echoing inputs/options/formState unchanged unless overridden. */
  function inputFromRaw(raw: ServerQuote): QuoteInput {
    return {
      label: raw.label,
      clientId: raw.clientId ?? undefined,
      clientName: raw.clientName,
      project: raw.project,
      reference: raw.reference,
      status: raw.status,
      statusLog: raw.statusLog,
      invoicedAs: raw.invoicedAs ?? undefined,
      inputs: raw.inputs,
      options: raw.options,
      formState: raw.formState,
    };
  }

  /** The `build` callback for updateQuoteGuarded for a status-only change: reapplied on a fresh row only while
   * its status is still the one the change was made from; a quote that moved on elsewhere is a conflict. */
  function statusChange(change: (cur: ServerQuote) => ServerQuote, basis: ServerQuote, extra?: Partial<QuoteInput>) {
    return (cur: ServerQuote): QuoteInput | null =>
      cur.status === basis.status ? { ...inputFromRaw(change(cur)), ...extra } : null;
  }

  async function markStatus(q: SavedQuote, status: QuoteStatus) {
    closeMenu();
    const idx = quotes.findIndex((x) => x.id === q.id);
    const raw = idx === -1 ? undefined : quotes[idx];
    if (!raw) return;
    const at = new Date().toISOString();
    try {
      const updated = await updateQuoteGuarded(raw, statusChange((cur) => applyTransition(cur, status, at), raw));
      const next = [...quotes];
      next[idx] = updated;
      quotes = next;
      actionFailed = false;
      actionStatus = `"${q.label}" ${STATUS_MARKED_MESSAGE[status]}.`;
      // The inline transition button may disappear (accepted and declined have none); keep focus on the row.
      void refocusIfLost(rowSelector(q.id));
    } catch (err) {
      actionFailed = true;
      actionStatus =
        err instanceof Error
          ? `Couldn't update "${q.label}": ${err.message}`
          : `Couldn't update "${q.label}".`;
    }
  }

  // Two-step confirm (WCAG 3.3.4) before the row actions that can't be taken back: every status transition (the
  // row offers no way back to an earlier status) and emailing a quote to its client. `restore` is the focus key
  // of the control that asked, so focus returns there on Cancel or Escape.
  let actionConfirm = $state<{ id: string; kind: QuoteStatus | "email"; restore: string } | null>(null);
  function askAction(q: SavedQuote, kind: QuoteStatus | "email", restore: string) {
    closeMenu();
    confirmingId = null;
    convertFor = null;
    emailEntryFor = null;
    actionConfirm = { id: q.id, kind, restore };
    // The row's trigger is still mounted (the confirm is a separate row), so confirmFocus takes focus from it.
  }
  function cancelAction() {
    const c = actionConfirm;
    actionConfirm = null;
    if (c) void focusAfterUpdate(keySelector(c.restore));
  }
  function runAction(q: SavedQuote) {
    const c = actionConfirm;
    actionConfirm = null;
    if (!c || emailingId !== null) return;
    if (c.kind === "email") void emailQuote(q);
    else void markStatus(q, c.kind);
  }
  function actionConfirmText(q: SavedQuote, c: { kind: QuoteStatus | "email" }): string {
    if (c.kind === "email") return `Yes, send to ${clientEmailFor(q, clients) ?? "the client"}`;
    return `Yes, mark as ${c.kind}`;
  }

  let emailingId = $state<string | null>(null);

  // Inline email entry for a quote with no deliverable address: the asked row, its draft address and error.
  let emailEntryFor = $state<string | null>(null);
  let emailDraft = $state("");
  let emailEntryError = $state("");

  function openEmailEntry(q: SavedQuote) {
    closeMenu();
    confirmingId = null;
    convertFor = null;
    actionConfirm = null;
    emailEntryFor = q.id;
    emailDraft = "";
    emailEntryError = "";
  }

  function cancelEmailEntry() {
    if (emailingId !== null) return;
    const id = emailEntryFor;
    emailEntryFor = null;
    if (id) void focusAfterUpdate(keySelector(`menu-toggle-${id}`));
  }

  async function submitEmailEntry(q: SavedQuote) {
    if (emailingId !== null) return;
    const email = validEntryEmail(emailDraft);
    if (!email) {
      emailEntryError = "Enter a valid email address.";
      return;
    }
    emailEntryError = "";
    await emailQuote(q, email);
  }

  const createdClients: CreatedClients = new Map();

  async function linkQuoteToClient(quoteId: string, client: Client): Promise<void> {
    const i = quotes.findIndex((x) => x.id === quoteId);
    const rawQ = i === -1 ? undefined : quotes[i];
    if (!rawQ) return;
    // Only the client changes, so it is safe to reapply on a fresher row.
    const relinked = await updateQuoteGuarded(rawQ, (cur) => ({ ...inputFromRaw(cur), clientId: client.id, clientName: client.name }));
    const nextQ = [...quotes];
    nextQ[i] = relinked;
    quotes = nextQ;
  }

  async function emailQuote(q: SavedQuote, entered?: string) {
    if (emailingId !== null) return;
    closeMenu();
    let email = entered ?? clientEmailFor(q, clients);
    if (!email) return;
    emailingId = q.id;
    try {
      // A multi-part order's per-part prices come from the server's answer for the quote's stored inputs.
      const rawForOrder = quotes.find((x) => x.id === q.id);
      let orderAnswer: OrderBreakdown | null = null;
      if (isMultiPart(q)) {
        const checked = rawForOrder ? await checkedOrderForQuote(q, rawForOrder) : { error: ORDER_PRICE_FAILED };
        if ("error" in checked) {
          actionFailed = true;
          actionStatus = `Couldn't email "${q.label}": ${checked.error}`;
          return;
        }
        orderAnswer = checked.order;
      }
      const payload = buildQuoteSendPayload(q, orderAnswer);
      if (!payload) {
        actionFailed = true;
        actionStatus = `Couldn't email "${q.label}": no price set on this quote.`;
        return;
      }
      const sendIt = () => sendQuoteEmail(payload, email);
      const result =
        entered === undefined
          ? await sendIt()
          : await sendWithEnteredEmail({
              quote: q,
              email,
              clients: clients ?? [],
              created: createdClients,
              deps: {
                createClient,
                updateClient,
                linkQuote: linkQuoteToClient,
                send: sendIt,
                onClients: (list) => (clients = list),
              },
            });
      if (entered !== undefined) emailEntryFor = null;
      if (!result.ok) {
        actionFailed = true;
        actionStatus = `Couldn't email "${q.label}": ${result.error}`;
        return;
      }

      const idx = quotes.findIndex((x) => x.id === q.id);
      const raw = idx === -1 ? undefined : quotes[idx];
      if (!raw) {
        actionFailed = false;
        actionStatus = `Emailed "${q.label}" to ${email}.`;
        return;
      }
      // Guarded so re-emailing never downgrades a later status (like the invoice send endpoint): an
      // accepted/declined quote keeps its status, and with nothing changed no PUT is issued (the email goes
      // out unrecorded). A draft/sent quote records the send and recipient via the "Mark sent" transition
      // shape.
      if (raw.status === "accepted" || raw.status === "declined") {
        actionFailed = false;
        actionStatus = `Emailed "${q.label}" to ${email}. (Status unchanged: already ${raw.status}.)`;
        return;
      }
      const sentAt = result.data.sentAt;
      const updated = await updateQuoteGuarded(raw, statusChange((cur) => applyTransition(cur, "sent", sentAt, email), raw));
      const next = [...quotes];
      next[idx] = updated;
      quotes = next;
      actionFailed = false;
      actionStatus = `Emailed "${q.label}" to ${email}.`;
    } catch (err) {
      actionFailed = true;
      actionStatus = err instanceof Error ? err.message : "Couldn't reach the server.";
    } finally {
      emailingId = null;
    }
  }

  // Convert flow: like HistoryPanel's former convert row (an inline client picker; the invoice is created
  // only on confirm), plus recording the quote's acceptance with the invoice link.
  let convertFor = $state<string | null>(null);
  let clientChoice = $state("");
  let converting = $state(false);
  // The server's per-part prices for the quote being converted (multi-part only); null while loading or for a
  // one-part quote.
  let convertOrder = $state<OrderBreakdown | null>(null);
  let convertOrderError = $state("");

  const ORDER_PRICE_FAILED = "Couldn't price this order's parts. Try again.";
  const PRICES_CHANGED = "Prices changed since this quote was saved. Open it in the editor and save it again.";

  /** The server's per-part prices for a stored multi-part quote, or an error message. The answer is
   * recomputed from stored inputs; if its item revenue no longer equals the saved price (a server pricing
   * change since), the lines wouldn't add up to the quoted total, so refuse. */
  async function checkedOrderForQuote(
    q: SavedQuote,
    raw: ServerQuote,
  ): Promise<{ order: OrderBreakdown } | { error: string }> {
    const answer = await orderForQuote(raw);
    if (answer === null) return { error: ORDER_PRICE_FAILED };
    if (answer.itemRevenue !== q.resolvedPrice) return { error: PRICES_CHANGED };
    return { order: answer };
  }

  async function openConvert(q: SavedQuote) {
    if (convertFor === q.id) return;
    confirmingId = null;
    closeMenu();
    emailEntryFor = null;
    actionConfirm = null;
    convertFor = q.id;
    clientChoice = "";
    convertOrder = null;
    convertOrderError = "";
    const id = q.id;
    if (isMultiPart(q)) {
      const raw = quotes.find((x) => x.id === q.id);
      const checked = raw ? await checkedOrderForQuote(q, raw) : { error: ORDER_PRICE_FAILED };
      if (convertFor !== id) return;
      convertOrder = "order" in checked ? checked.order : null;
      convertOrderError = "error" in checked ? checked.error : "";
    }
    await ensureClientsLoaded();
    // Another row's "Convert to invoice" (or Cancel) may have run while this load was in flight; apply the
    // preselect only if this row is still the open one.
    if (convertFor !== id) return;
    if (q.clientId && clients?.some((c) => c.id === q.clientId)) {
      clientChoice = q.clientId;
    }
  }

  function cancelConvert() {
    if (converting) return;
    const id = convertFor;
    convertFor = null;
    if (id) void focusAfterUpdate(keySelector(`convert-${id}`));
  }

  /** The status update for a raw quote converted to an invoice: `accepted` with a log entry unless already
   * accepted ("Invoice again" adds no duplicate entry). Mirrors convertQuote.ts's applyConvertStatus,
   * which works on the mapped SavedQuote and can't be reused on the raw server quote. */
  function acceptedTransition(raw: ServerQuote, at: string): ServerQuote {
    return raw.status === "accepted" ? raw : applyTransition(raw, "accepted", at);
  }

  async function convertToInvoice(q: SavedQuote) {
    if (converting || (isMultiPart(q) && convertOrder === null)) return;
    converting = true;
    try {
      const chosen = clients?.find((c) => c.id === clientChoice);
      const preview = convertPreview(q, cfg.invoicing.salesTaxPct, convertOrder);
      const invoice = await createInvoice({
        clientId: chosen?.id ?? "",
        clientName: convertClientName(chosen?.name, q),
        lines: preview.lines,
        taxBp: convertTaxBp(q) ?? undefined,
      });
      invoiceNumberById = new Map(invoiceNumberById).set(invoice.id, invoice.number);

      const idx = quotes.findIndex((x) => x.id === q.id);
      const raw = idx === -1 ? undefined : quotes[idx];
      if (raw) {
        // Status (accepted, with a log entry unless already accepted) and the invoicedAs link land in the
        // same PUT, so a reader never sees the link without the acceptance.
        const acceptedAt = new Date().toISOString();
        try {
          const updated = await updateQuoteGuarded(
            raw,
            statusChange((cur) => acceptedTransition(cur, acceptedAt), raw, { invoicedAs: invoice.id }),
          );
          const next = [...quotes];
          next[idx] = updated;
          quotes = next;
          actionFailed = false;
          actionStatus = `Converted "${q.label}" to invoice ${invoice.number}.`;
        } catch (err) {
          actionFailed = true;
          actionStatus = `Converted "${q.label}" to invoice ${invoice.number}, but couldn't save the invoice link on this quote${
            err instanceof Error ? `: ${err.message}` : "."
          }`;
        }
      } else {
        // The quote vanished (deleted in another tab or session between opening the picker and confirming):
        // the invoice still exists, with no quote to link back to.
        actionFailed = false;
        actionStatus = `Converted "${q.label}" to invoice ${invoice.number}.`;
      }

      pendingOpen.current = { tab: "invoices", id: invoice.id };
      location.hash = "#/invoices";
      convertFor = null;
    } catch (err) {
      actionFailed = true;
      actionStatus =
        err instanceof Error ? `Couldn't convert "${q.label}": ${err.message}` : `Couldn't convert "${q.label}" to an invoice.`;
    } finally {
      converting = false;
    }
  }

  async function duplicate(q: SavedQuote) {
    confirmingId = null;
    closeMenu();
    const raw = quotes.find((x) => x.id === q.id);
    if (!raw) return;
    try {
      const label = `Copy of ${raw.label}`;
      const created = await createQuote({
        ...inputFromRaw(raw),
        label,
        status: "draft",
        statusLog: [],
        invoicedAs: undefined,
      });
      quotes = [created, ...quotes];
      total += 1; // a new quote now exists server-side too, so the loaded window still equals total up to it
      actionFailed = false;
      actionStatus = `Duplicated "${q.label}" as "${created.label}".`;
    } catch (err) {
      actionFailed = true;
      actionStatus = err instanceof Error ? `Couldn't duplicate "${q.label}": ${err.message}` : `Couldn't duplicate "${q.label}".`;
    }
  }

  function askDelete(id: string) {
    confirmingId = id;
  }
  function cancelDelete() {
    confirmingId = null;
  }
  let removingId: string | null = null;
  async function remove(q: SavedQuote) {
    if (removingId !== null) return;
    removingId = q.id;
    confirmingId = null;
    closeMenu();
    const next = neighborId(sortedQuotes.map((x) => x.id), q.id);
    try {
      await deleteQuote(q.id);
      quotes = quotes.filter((x) => x.id !== q.id);
      total = Math.max(0, total - 1);
      actionFailed = false;
      actionStatus = `Deleted "${q.label}".`;
      // The toggle that closeMenu() refocused went with the row: next row, else previous, else the heading.
      void focusRowOrHeading(next);
    } catch (err) {
      actionFailed = true;
      actionStatus = err instanceof Error ? `Couldn't delete "${q.label}": ${err.message}` : `Couldn't delete "${q.label}".`;
    } finally {
      removingId = null;
    }
  }

  // The hidden file input, clicked imperatively by the visible "Import" button.
  let fileInput: HTMLInputElement;

  /** Exports every saved quote, not just the loaded window: the file is the user's backup, so a
   * partially-loaded list (`quotes.length < total`) is backfilled first via the controller's runNow()
   * (bypassing the filter latch), showing the "Loading the rest of the list…" status/failure banner. A
   * failed backfill writes no file; the banner is the only feedback. */
  async function exportQuotes() {
    if (quotes.length === 0 || backfill.backfilling) return;
    if (!fullyLoaded) {
      await backfill.runNow();
      if (backfill.backfillFailed) return;
    }
    const payload = buildQuotesFile(mappedQuotes);
    const blob = new Blob([JSON.stringify(payload, null, 2)], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `printpaca-quotes-${new Date().toISOString().slice(0, 10)}.json`;
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
  }

  // Import creates quotes one request at a time; the button stays disabled meanwhile so a second click can't
  // import the same file twice.
  let importing = $state(false);

  async function handleImportFile(e: Event) {
    const input = e.currentTarget as HTMLInputElement;
    const file = input.files?.[0];
    if (!file || importing) return;
    importing = true;
    try {
      const result = parseQuotesFile(await file.text());
      if (!result) {
        actionFailed = true;
        actionStatus = `Couldn't import "${file.name}": not a valid quotes file.`;
        return;
      }
      const { quotes: imported, skipped } = result;

      // An imported entry's `.inputs` is the editor's form snapshot with no engine bag, so the bag is rebuilt
      // via toOrderInputs(cfg, formState) rather than mutating the live editor state. Each import is its own POST;
      // one failing entry (e.g. a malformed snapshot) is counted with the parser's `skipped`, not fatal to
      // the batch.
      let created = 0;
      let failed = 0;
      const landed: ServerQuote[] = [];
      for (const q of imported) {
        try {
          const migrated = parseFormState(q.inputs, cfg);
          if (!migrated) throw new Error("unusable saved data");
          const formState = toFormState(migrated);
          const rec = await createQuote({
            label: q.label,
            clientId: q.clientId,
            clientName: q.clientName ?? "",
            project: q.project ?? "",
            reference: q.reference ?? "",
            status: q.status,
            statusLog: q.statusLog,
            invoicedAs: undefined,
            inputs: toOrderInputs(cfg, migrated),
            options: engineOptions(cfg),
            formState,
          });
          landed.push(rec);
          created++;
        } catch {
          failed++;
        }
      }
      quotes = [...landed, ...quotes];
      total += landed.length; // each landed quote now exists server-side too

      const totalSkipped = skipped + failed;
      actionFailed = failed > 0;
      actionStatus = `Imported ${created} quote${created === 1 ? "" : "s"}${totalSkipped > 0 ? ` (${totalSkipped} skipped)` : ""}.`;
    } catch {
      actionFailed = true;
      actionStatus = `Couldn't import "${file.name}": not a valid quotes file.`;
    } finally {
      importing = false;
      input.value = "";
    }
  }
const COLUMNS: DataColumn[] = [
  { key: "label", label: "Label" },
  { key: "client", label: "Client" },
  { key: "price", label: "Price", num: true },
  { key: "status", label: "Status" },
  { key: "saved", label: "Saved" },
  { key: "actions", label: "Actions", hideLabel: true },
];
</script>

<!-- onscrollcapture: a scroll on the DataTable scroll region (a nested container) must be caught; scroll events don't
     bubble, only the capture phase reaches an ancestor listener. -->
<svelte:window onpointerdown={handleWindowPointerDown} onkeydown={handleWindowKeydown} onscrollcapture={handleWindowScroll} />

<div class="page">
  <div class="head-row">
    <h1 tabindex="-1">Quotes</h1>
    <div class="head-actions">
      {#if canEdit}
        <button type="button" class="primary" onclick={() => { if (startNewQuote(cfg)) location.hash = "#/quote"; }}>New quote</button>
      {/if}
      <button type="button" class="quiet" onclick={exportQuotes} aria-disabled={quotes.length === 0 || backfill.backfilling}>
        Export
      </button>
      <button type="button" class="quiet" onclick={() => { if (!importing) fileInput.click(); }} aria-disabled={importing}>{importing ? "Importing…" : "Import"}</button>
      <input
        bind:this={fileInput}
        type="file"
        accept=".json,application/json"
        class="visually-hidden"
        tabindex="-1"
        aria-label="Import quotes"
        onchange={handleImportFile}
      />
    </div>
  </div>

  <p class="lede">
    Track each saved quote from draft to sent to accepted, convert quotes to invoices,
    or delete the ones you no longer need.
  </p>

  {#if quotesUnavailable}
    <p class="banner-warn" role="alert">Couldn't load saved quotes. The server couldn't be reached.</p>
  {/if}

  <!-- action-status carries no styles: it is the stable hook e2e/history.spec.ts selects for this
       surface's actions (convert, duplicate, delete, import, status transitions), separate from
       HistoryPanel's live region (only one is mounted at a time). -->
  <p
    class="status-line action-status"
    class:collapsed={actionStatus === ""}
    role={actionFailed ? "alert" : "status"}
    aria-live={actionFailed ? "assertive" : "polite"}
  >
    {actionStatus}
  </p>

  <BackfillStatus backfilling={backfill.backfilling} backfillFailed={backfill.backfillFailed} verb="filter" />

  {#if cfg.smtp.host === ""}
    <p class="banner-muted">
      Email isn't configured, so quotes can't be emailed from here.
      {#if canConfigure}<a href="#/settings">Set it up in Settings.</a>{:else}Ask an admin to set up email.{/if}
    </p>
  {/if}

  <DataTable
    label="Saved quotes"
    columns={COLUMNS}
    loadMore={!loading && !filterActive ? { shown: quotes.length, total, loadingMore, onLoadMore: loadMore } : null}
  >
    {#snippet filters()}
      {#if showFilter}
        <FormGrid>
          <Field id="quotes-filter" label="Filter by label or client" size="l">
            {#snippet children(f)}
              <input id={f.id} type="search" placeholder="Label or client" bind:value={filterText} />
            {/snippet}
          </Field>
          <Field id="quotes-sort" label="Sort saved quotes" size="m">
            {#snippet children(f)}
              <select id={f.id} bind:value={sortOrder}>
                <option value="newest">Newest first</option>
                <option value="oldest">Oldest first</option>
                <option value="price-desc">Price high to low</option>
                <option value="price-asc">Price low to high</option>
                <option value="label">Label A to Z</option>
                <option value="status">Status</option>
              </select>
            {/snippet}
          </Field>
        </FormGrid>
      {/if}
      {#if quotes.length > 0}
        <div class="chips" role="group" aria-label="Filter by status">
          {#each QUOTE_STATUS_CHIPS as chip (chip.key)}
            <button
              type="button"
              class:active={statusFilter === chip.key}
              aria-pressed={statusFilter === chip.key}
              onclick={() => (statusFilter = chip.key)}
            >{chip.label}</button>
          {/each}
        </div>
      {/if}
    {/snippet}
          {#if loading}
            <tr><td colspan="6" class="empty-state"><span role="status">Loading…</span></td></tr>
          {:else}
            {#each sortedQuotes as q (q.id)}
              {@const stats = quoteRowStats(q)}
              {@const qty = extractQuantity(q.inputs)}
              {@const status = displayStatus(q, cfg.quote.validDays, Date.now())}
              {@const emailReason = emailDisabledReason(q, cfg.smtp.host, clientsLoaded, clients, canManageClients)}
              {@const primaryT = primaryTransition(q)}
              <tr class="quote-row">
                <td class="c-label">
                  {#if canEdit}
                    <button type="button" class="quiet linkish" data-row-id={q.id} onclick={() => openInEditor(q)}>{q.label}</button>
                  {:else}
                    <span data-row-id={q.id}>{q.label}</span>
                  {/if}
                  {#if isMultiPart(q)}<span class="parts-badge">{q.partCount} parts</span>{/if}
                  <AttributionLine createdBy={q.createdBy} updatedBy={q.updatedBy} />
                </td>
                <td class="c-client">{q.clientName ?? "-"}</td>
                <td class="num c-price">
                  {#if stats.priceCents !== null}
                    <div class="price-batch">{money(stats.priceCents * qty, cfg)}</div>
                    {#if qty > 1}
                      <div class="price-unit">{money(stats.priceCents, cfg)}&nbsp;each</div>
                    {/if}
                  {:else}
                    <span class="cost-only">cost only</span>
                  {/if}
                </td>
                <td class="c-status">
                  <StatusBadge status={status} />
                  {#if q.invoicedAs}
                    <!-- In the status cell, not the action cluster: the long "Invoiced INV-…" label made the
                         cluster wider than its column and clipped the ⋯ menu. -->
                    <button type="button" class="pill invoiced-badge" onclick={() => openInvoiced(q)}>
                      {invoicedLabel(q.invoicedAs)}
                    </button>
                  {/if}
                </td>
                <td class="nowrap c-saved" data-label="Saved">{formatSavedAt(q.savedAt)}</td>
                <td class="c-actions">
                  <div class="row-actions">
                    {#if primaryT}
                      <button
                        type="button"
                        class="quiet"
                        data-focus-key={`transition-${q.id}`}
                        onclick={() => askAction(q, primaryT, `transition-${q.id}`)}
                      >{TRANSITION_LABELS[primaryT]}</button>
                    {/if}
                    <button
                      type="button"
                      class="quiet"
                      data-focus-key={`convert-${q.id}`}
                      onclick={() => openConvert(q)}
                      aria-label={q.invoicedAs ? `Invoice again: "${q.label}"` : `Convert to invoice: "${q.label}"`}
                      aria-disabled={convertFor === q.id}
                    >
                      {q.invoicedAs ? "Invoice again" : "Convert to invoice"}
                    </button>

                    <!-- Overflow menu: the non-primary status transition, Email quote, Duplicate and
                         Delete, kept in the DOM (not lazily mounted) so aria-expanded/haspopup stay wired
                         to one stable toggle. data-row-menu is the row's outside-click boundary for
                         handleWindowPointerDown. -->
                    <div class="row-menu" data-row-menu={q.id} onfocusout={handleMenuFocusOut}>
                      <button
                        type="button"
                        class="quiet menu-toggle"
                        aria-controls={`row-menu-${q.id}`}
                        aria-expanded={menuOpenId === q.id}
                        data-focus-key={`menu-toggle-${q.id}`}
                        aria-label={`More actions for "${q.label}"`}
                        onclick={(e) => toggleMenu(q.id, e.currentTarget)}
                      >⋯</button>
                      {#if menuOpenId === q.id && menuAnchor}
                        <div
                          class="menu-panel"
                          id={`row-menu-${q.id}`}
                          style={`${menuAnchor.top !== undefined ? `top:${menuAnchor.top}px;` : `bottom:${menuAnchor.bottom}px;`}right:${menuAnchor.right}px;`}
                        >
                          <!-- A labeled group of buttons, not an ARIA menu: role="menu"/"menuitem" would
                               claim arrow-key roving this panel doesn't implement. The email-reason note
                               below is a sibling, not a child: it describes, not acts. -->
                          <div class="menu-group" role="group" aria-label={`More actions for "${q.label}"`}>
                            {#if canEdit}
                              <button type="button" class="quiet menu-item" onclick={() => { closeMenu(false); openInEditor(q); }} aria-label={`Open "${q.label}" in editor`}>Open in editor</button>
                            {/if}
                            {#each overflowTransitions(q) as t (t)}
                              <button
                                type="button"
                                class="quiet menu-item"
                                onclick={() => askAction(q, t, `menu-toggle-${q.id}`)}
                              >{TRANSITION_LABELS[t]}</button>
                            {/each}
                            {#if needsEmailEntry(emailReason)}
                              <button
                                type="button"
                                class="quiet menu-item"
                                onclick={() => openEmailEntry(q)}
                                aria-label={`Email quote… to client "${q.label}" (add email)`}
                              >Email quote…</button>
                            {:else if emailReason}
                              <button
                                type="button"
                                class="quiet menu-item"
                                disabled
                                aria-label={`Email quote "${q.label}" to client (unavailable)`}
                                aria-describedby={emailReason === "unconfigured" ? undefined : `email-reason-${q.id}`}
                              >Email quote</button>
                            {:else}
                              <button
                                type="button"
                                class="quiet menu-item"
                                onclick={() => { if (emailingId === null) askAction(q, "email", `menu-toggle-${q.id}`); }}
                                aria-label={`Email quote "${q.label}" to client`}
                                aria-disabled={emailingId === q.id}
                              >
                                {emailingId === q.id ? "Emailing…" : "Email quote"}
                              </button>
                            {/if}
                            <button type="button" class="quiet menu-item" onclick={() => duplicate(q)} aria-label={`Duplicate "${q.label}"`}>
                              Duplicate
                            </button>
                            {#if confirmingId === q.id}
                              <button type="button" class="quiet danger menu-item confirm-delete" onclick={() => remove(q)} aria-label={`Yes, delete "${q.label}"`} use:confirmFocus={{ restore: keySelector(`delete-quote-${q.id}`), cancel: cancelDelete }}>Yes, delete quote</button>
                              <button type="button" class="quiet menu-item cancel-delete" onclick={cancelDelete} aria-label={`Cancel deleting "${q.label}"`}>Cancel</button>
                            {:else}
                              <button type="button" class="quiet menu-item" data-focus-key={`delete-quote-${q.id}`} onclick={() => askDelete(q.id)} aria-label={`Delete "${q.label}"`}>Delete</button>
                            {/if}
                          </div>
                          {#if emailReason && emailReason !== "unconfigured" && !needsEmailEntry(emailReason)}
                            <span id={`email-reason-${q.id}`} class="convert-note-plain">
                              {#if emailReason === "loading"}
                                Loading client info…
                              {:else if emailReason === "no-price"}
                                No price set on this quote.
                              {:else if emailReason === "clients-unavailable"}
                                Client list unavailable; try again.
                              {:else}
                                Needs Clients access to add an email.
                              {/if}
                            </span>
                          {/if}
                        </div>
                      {/if}
                    </div>
                  </div>
                </td>
              </tr>
              {#if actionConfirm?.id === q.id}
                <tr class="convert-tr">
                  <td colspan="6">
                    <div class="convert-row" role="group" aria-label="Confirm action">
                      <button type="button" class="primary" onclick={() => runAction(q)} use:confirmFocus={{ restore: keySelector(actionConfirm.restore), cancel: cancelAction }}>{actionConfirmText(q, actionConfirm)}</button>
                      <button type="button" onclick={cancelAction}>Cancel</button>
                      {#if actionConfirm.kind !== "email" && TRANSITION_HINTS[actionConfirm.kind]}
                        <span class="convert-note-plain">{TRANSITION_HINTS[actionConfirm.kind]}</span>
                      {/if}
                    </div>
                  </td>
                </tr>
              {/if}
              {#if emailEntryFor === q.id}
                <tr class="convert-tr">
                  <td colspan="6">
                    <form class="convert-row" onsubmit={(e) => { e.preventDefault(); submitEmailEntry(q); }}>
                      <label class="visually-hidden" for={`email-entry-${q.id}`}>Client email for this quote</label>
                      <input
                        id={`email-entry-${q.id}`}
                        type="email"
                        placeholder="Client email"
                        use:focusOnMount
                        bind:value={emailDraft}
                        aria-describedby={emailEntryError ? `email-entry-error-${q.id}` : undefined}
                      />
                      <span class="convert-note-plain">
                        {q.clientId ? "Saved to the linked client." : "Creates a client from the quote's client name."}
                      </span>
                      {#if emailEntryError}<span id={`email-entry-error-${q.id}`} class="advisory-text" role="alert">{emailEntryError}</span>{/if}
                      <button class="primary" type="submit" aria-disabled={emailingId === q.id}>{emailingId === q.id ? "Sending…" : "Send"}</button>
                      <button type="button" onclick={cancelEmailEntry} aria-disabled={emailingId === q.id}>Cancel</button>
                    </form>
                  </td>
                </tr>
              {/if}
              {#if convertFor === q.id}
                <tr class="convert-tr">
                  <td colspan="6">
                    {#if !quoteHasPrice(q)}
                      <div class="convert-row">
                        <span id={`no-price-note-${q.id}`} class="convert-note-plain">
                          No selling price on this quote. Open it in the editor and set a price first.
                        </span>
                        <button class="primary" disabled aria-describedby={`no-price-note-${q.id}`}>Create invoice</button>
                        <button onclick={cancelConvert} use:focusOnMount>Cancel</button>
                      </div>
                    {:else}
                      {@const preview = convertPreview(q, cfg.invoicing.salesTaxPct, convertOrder)}
                      <div class="convert-row">
                        <label class="visually-hidden" for={`convert-client-${q.id}`}>Client for this invoice</label>
                        <select id={`convert-client-${q.id}`} use:focusOnMount bind:value={clientChoice}>
                          <option value="">Walk-in (no client)</option>
                          {#each clients ?? [] as c (c.id)}<option value={c.id}>{c.name}</option>{/each}
                        </select>
                        {#if clientsLoaded && clients === null}
                          <span class="advisory-text">Client list unavailable; converting as walk-in.</span>
                        {/if}
                        {#if convertOrderError}
                          <span class="advisory-text" role="alert">{convertOrderError}</span>
                        {/if}
                        {#if convertTaxBp(q) === 0}
                          <span class="convert-note-plain">Tax was included in the quoted price; the invoice adds none.</span>
                        {/if}
                        <button class="primary" onclick={() => convertToInvoice(q)} aria-disabled={converting} disabled={preview.lines.length === 0 || (isMultiPart(q) && convertOrder === null)}>
                          {converting ? "Creating…" : `Create invoice · ${money(preview.totalCents, cfg)}`}
                        </button>
                        <button onclick={cancelConvert} aria-disabled={converting}>Cancel</button>
                      </div>
                    {/if}
                  </td>
                </tr>
              {/if}
            {:else}
              <tr>
                <td colspan="6" class="empty-state">
                  {quotes.length === 0
                    ? "No saved quotes yet. Save one from the New quote page."
                    : "No quotes match the filter. Clear it, or choose All, to see every saved quote."}
                </td>
              </tr>
            {/each}
          {/if}
  </DataTable>
</div>

<style>
  .parts-badge { margin-left: var(--space-2); color: var(--muted); font-size: var(--text-sm); }
  .head-row h1 { margin: 0; }
  /* Wraps at 320 CSS px (WCAG 1.4.10): New quote, Export, and Import no longer fit beside the heading. */
  .head-row { flex-wrap: wrap; }
  .head-actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }
  .lede { color: var(--muted); font-size: var(--text-sm); margin: 0; }

  /* .status-line's app-wide min-height reserves room against layout shift, but before any action sets
   * actionStatus that plus .page's flex gap left a dead band above the table. Collapsed only while
   * actionStatus is empty (the live region stays mounted, so its role/aria-live never toggle); once a
   * message is set it reverts to the reserved layout. */
  .action-status.collapsed { min-height: 0; margin-top: calc(var(--space-6) * -1); margin-bottom: calc(var(--space-6) * -1); }

  .linkish {
    border: none; background: none; color: var(--accent-text); padding: 0; text-align: left;
    font-size: var(--text-base); font-weight: 600;
  }
  .linkish:hover { color: var(--accent-text); text-decoration: underline; }

  .cost-only { color: var(--muted); }
  /* Batch total is the primary figure; the per-unit line is secondary (muted, smaller), the same size step
   * BreakdownTable/HistoryPanel use for a subordinate money line. */
  .price-batch { font-variant-numeric: tabular-nums; }
  .price-unit { color: var(--muted); font-size: var(--text-sm); font-variant-numeric: tabular-nums; }
  .nowrap { white-space: nowrap; }
  td.empty-state { text-align: center; }

  /* nowrap with each control keeping its words together: wrapping made this cell's min-content one button
   * wide, so auto table layout gave it barely more and the overflow toggle dropped to a second line on
   * nearly every row. Holding the cluster on one line makes the cell ask for its real width; the wrapping
   * label/client columns give up the difference. .table-scroll is the escape hatch. */
  .row-actions { display: flex; flex-wrap: nowrap; align-items: center; gap: var(--space-2); }
  .row-actions > button, .row-actions .menu-toggle { white-space: nowrap; }
  .confirm-delete { white-space: nowrap; }
  .cancel-delete { white-space: nowrap; }

  /* Overflow menu: .row-menu is only the outside-click boundary. .menu-panel is position: fixed, placed
   * from the toggle's viewport rect at open time (menuAnchor; bottom when flipped upward), not absolute:
   * .table-scroll's computed overflow-y: auto would clip it (e.g. a one-row table). */
  .menu-toggle {
    min-width: var(--tap-sm);
    font-weight: 700;
    letter-spacing: 1px;
  }
  .menu-panel {
    position: fixed;
    z-index: 5;
    min-width: var(--field-s);
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
    padding: var(--space-1);
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: calc(var(--radius) / 2);
    box-shadow: var(--shadow);
  }
  /* The labeled button group only; the email-reason note (a sibling) stacks below via .menu-panel's gap. */
  .menu-group { display: flex; flex-direction: column; gap: var(--space-1); }
  .menu-item { display: block; width: 100%; text-align: left; white-space: nowrap; }

  /* .pill (theme) covers border, radius, font-size and padding; kept locally (like HistoryPanel's former
   * invoiced-badge): the tap-sm min-height (it's a button) and the borderless, muted-to-text hover that
   * marks it clickable. */
  .invoiced-badge {
    min-height: var(--tap-sm);
    background: none;
    color: var(--muted);
    white-space: nowrap;
  }
  .invoiced-badge:hover { color: var(--text); }
  .c-status .invoiced-badge { display: block; margin-top: var(--space-1); }

  /* Phones: each quote becomes a stacked card (label and price on top, client and status, saved date, then the
   * actions) instead of a table that clips its status, price and actions behind a horizontal scroll. DataTable
   * hides the header row and makes the table a block list; the row layout is this screen's. */
  @media (max-width: 600px) {
    tr.quote-row {
      display: grid;
      grid-template-columns: minmax(0, 1fr) auto;
      grid-template-areas: "label price" "client status" "saved saved" "actions actions";
      gap: var(--space-1) var(--space-3);
      padding: var(--space-3) 0;
      border-top: 1px solid var(--border);
    }
    .c-label { grid-area: label; overflow-wrap: anywhere; }
    .c-price { grid-area: price; text-align: right; font-weight: 600; }
    .c-client { grid-area: client; overflow-wrap: anywhere; color: var(--muted); }
    .c-status { grid-area: status; text-align: right; }
    .c-saved { grid-area: saved; color: var(--muted); font-size: var(--text-sm); }
    .c-saved::before { content: attr(data-label) " "; }
    .c-actions { grid-area: actions; margin-top: var(--space-1); }
    .row-actions { flex-wrap: wrap; }
    .convert-tr td { display: block; }
  }

  .convert-row {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: var(--space-2);
    padding: var(--space-1) 0;
  }
  .convert-row select { flex: 1; min-width: var(--field-s); }
  .convert-note-plain { font-size: var(--text-sm); color: var(--muted); }
</style>
