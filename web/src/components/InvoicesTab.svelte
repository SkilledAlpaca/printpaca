<script lang="ts">
  import { canLeave, escapeBelongsToField } from "../lib/unsavedGuard";
  import { unsavedForm } from "../lib/unsavedForm.svelte";
  import { onMount, tick, untrack } from "svelte";
  import { toCents, type EffectiveConfig } from "../lib/engine/index";
  import { listClients, type Client } from "../lib/clientsApi";
  import { listItems, type Item } from "../lib/itemsApi";
  import {
    listInvoices,
    getInvoice,
    createInvoice,
    updateInvoice,
    setInvoiceStatus,
    deleteInvoice,
    addPayment,
    deletePayment,
    type Invoice,
    type InvoiceStatus,
    type InvoiceSummary,
  } from "../lib/invoicesApi";
  import { sendInvoiceEmail } from "../lib/sendApi";
  import { printJob, invoicePrintJob, consumePendingOpen } from "../lib/state.svelte";
  import { syncDetail } from "../lib/hashRoute";
  import { refetchLimit, INITIAL_PAGE_LIMIT } from "../lib/pagination";
  import { createBackfillController } from "../lib/backfillController.svelte";
  import {
    emptyForm,
    formFromInvoice,
    formFromInvoiceDuplicate,
    newInvoiceDates,
    buildInput,
    defaultPaymentForm,
    type InvoiceForm,
    type PaymentForm,
  } from "../lib/invoiceForm";
  import { todayInTimezone } from "../lib/invoiceStatus";
  import { money } from "../lib/format";
  import InvoiceList from "./invoices/InvoiceList.svelte";
  import InvoiceDetail from "./invoices/InvoiceDetail.svelte";
  import { announce, focusAfterUpdate } from "../lib/a11y.svelte";
  import { focusAfterRowRemoval, focusRowOrHeading, keySelector, neighborId, refocusIfLost, rowSelector } from "../lib/a11yFocus";

  let { cfg }: { cfg: EffectiveConfig } = $props();

  let invoices = $state<InvoiceSummary[]>([]);
  let loading = $state(true);
  // Set when listInvoices() returns null (network/server failure), same tolerance as ClientsTab: a muted note
  // and an empty list.
  let unavailable = $state(false);

  // Server pagination (server/pagination.go's opt-in ?limit=&offset=): `invoices` holds the loaded window and
  // `total` is the pre-slice count (X-Total-Count). No local `fullyLoaded` here; the backfill controller
  // computes it from getItems()/getTotal().
  let total = $state(0);
  let loadingMore = $state(false);

  // InvoiceList.svelte owns the status-chip and filter-text state and calls ensureReady when either becomes
  // active (shared latch, see backfillController.svelte.ts), so a filter never runs over a partial list.
  // `filterKey` (statusFilter+filterText) lets ensureReady tell "filter changed" (clear the failure latch,
  // allow a retry) from "still active", which InvoiceList's filterActive boolean can't.
  const backfill = createBackfillController<InvoiceSummary>({
    getItems: () => invoices,
    getTotal: () => total,
    fetchPage: (page) => listInvoices(page),
    setItems: (items) => {
      invoices = items;
      total = items.length;
    },
  });

  // The first load fetches 100; a refetch after a mutation asks for at least as many rows as are loaded, so
  // the window doesn't shrink.
  async function refresh() {
    loading = true;
    const limit = refetchLimit(invoices.length);
    const result = await listInvoices({ limit, offset: 0 });
    if (result === null) {
      unavailable = true;
      invoices = [];
      total = 0;
    } else {
      unavailable = false;
      invoices = result.items;
      total = result.total;
    }
    loading = false;
  }

  async function loadMore() {
    loadingMore = true;
    const result = await listInvoices({ limit: INITIAL_PAGE_LIMIT, offset: invoices.length });
    if (result !== null) {
      const firstNew = result.items[0];
      invoices = [...invoices, ...result.items];
      total = result.total;
      // The button that was focused goes away when this page exhausts the list: land on the first new row.
      if (firstNew) void refocusIfLost(rowSelector(firstNew.id));
    }
    loadingMore = false;
  }

  let clients = $state<Client[]>([]);
  let items = $state<Item[]>([]);

  // Wired to InvoiceDetail's "Link client" hint (matchingClientByName, from `clients`/`form` props). Linking
  // is a plain field write, so it stays with the owner of `form`.
  function linkMatchingClient(id: string) {
    form.clientId = id;
  }

  // Escape closes an open detail view like "Back to list", but first cancels any armed two-click confirm
  // (delete or cancel invoice, remove line, delete payment). Ignored while a native <select> is focused so
  // the browser can close its own dropdown.
  function handleDetailKeydown(e: KeyboardEvent) {
    if (e.key !== "Escape") return;
    if (view !== "detail") return;
    if (escapeBelongsToField(e.target)) return;
    e.preventDefault();
    if (confirmingDelete) {
      cancelDeleteConfirm();
    } else if (confirmingCancel) {
      cancelCancel();
    } else if (confirmingLineRemove !== null) {
      cancelRemoveLine();
    } else if (confirmingPaymentDelete !== null) {
      cancelDeletePaymentConfirm();
    } else if (confirmingMarkSent) {
      confirmingMarkSent = false;
    } else if (confirmingEmail) {
      confirmingEmail = false;
    } else {
      requestBack();
    }
  }

  async function loadClients() {
    const loaded = await listClients();
    if (loaded) clients = loaded;
  }

  async function loadItems() {
    const loaded = await listItems();
    if (loaded) items = loaded;
  }

  onMount(() => {
    refresh();
    void loadClients(); // not awaited: must not delay the pending-open flow below
    void loadItems(); // likewise: the line editor's item picker
    // Quote conversion (HistoryPanel's "Convert to invoice") and a "#/invoices#<id>" deep link
    // (routePendingOpen) set this before the tab mounts; open that invoice instead of the list. silent: true
    // matters only for deep links (a stale bookmarked id falls back to the list quietly); conversion hands
    // off a just-created id, so one code path serves both.
    const id = consumePendingOpen("invoices");
    if (id) {
      // Set the hash before opening: a hashchange queued by this tab's mount (App's activate()) fires
      // asynchronously and would otherwise see a bare #/invoices that doesn't match detailId and close the
      // just-opened detail. replaceState, because this completes the navigation that brought us here.
      history.replaceState(null, "", `${INVOICES_HASH}#${id}`);
      openDetail(id, { silent: true });
    }
  });

  // List <-> detail navigation, addressable via "#/invoices#<id>" (App.svelte's tabFromHash strips it for tab
  // routing; handled locally here). Opening pushes a history entry via a guarded location.hash assignment;
  // back/forward and manual edits are picked up by syncDetailFromHash.

  const INVOICES_HASH = "#/invoices";

  function setDetailHash(id: string) {
    const target = `${INVOICES_HASH}#${id}`;
    if (location.hash !== target) location.hash = target; // pushes a history entry
  }

  // Resets the view state of "closing" without touching the URL: used by backToList() (which owns the URL)
  // and syncDetailFromHash (where location.hash already changed).
  function closeDetailView() {
    view = "list";
    detailId = null;
    detail = null;
    formErrors = [];
  }

  // Reacts to hash changes while mounted: back/forward between detail and list (or two ids), or a manual edit
  // of the id. A hashchange from this component's own setDetailHash()/backToList() is a no-op, so it never
  // double-fetches.
  function syncDetailFromHash() {
    syncDetail(location.hash, INVOICES_HASH, detailId, view === "detail", (id) => openDetail(id, { silent: true }), () => {
      closeDetailView();
      void focusRowOrHeading(openerId);
    });
  }

  type View = "list" | "detail";
  let view = $state<View>("list");
  // null while composing a not-yet-created invoice; the created or loaded invoice's id once one exists.
  let detailId = $state<string | null>(null);
  let detail = $state<Invoice | null>(null);
  let detailLoading = $state(false);
  let detailError = $state<string | null>(null);

  // untrack: a one-time snapshot at declaration, not a live binding to cfg; a later cfg change must not
  // rewrite an in-progress form.
  let form = $state<InvoiceForm>(untrack(() => emptyForm(cfg.invoicing.salesTaxPct)));
  let formErrors = $state<string[]>([]);
  let saving = $state(false);

  let confirmingCancel = $state(false);
  let confirmingDelete = $state(false);
  // Two-click confirms (WCAG 3.3.4) before the two actions a user can't take back: marking a draft sent (a
  // sent invoice never returns to draft; it takes stock and records a delivery) and emailing it to the client.
  let confirmingMarkSent = $state(false);
  let confirmingEmail = $state(false);
  // Shared result line for every status-action button (errors, and the "Email to client" result): the same
  // status-line/aria-live pattern as QuotesTab's action-status, carrying success messages too.
  let statusActionMessage = $state("");
  let statusActionFailed = $state(false);
  let emailing = $state(false);
  // Two-click remove: index of the line asking "Remove?", keyed by index since draft lines have no id.
  let confirmingLineRemove = $state<number | null>(null);

  // The payment-recording form; money stays in currency units, converted at the API edge in
  // addPaymentSubmit(). untrack: same one-time-snapshot rationale as `form`.
  let paymentForm = $state<PaymentForm>(
    untrack(() => defaultPaymentForm(detail ? detail.balanceCents : 0, cfg.invoicing.paymentMethods[0] ?? "", todayInTimezone(cfg.timezone))),
  );
  let paymentErrors = $state<string[]>([]);
  let paymentSaving = $state(false);
  // Two-click delete: id of the payment asking "Delete?", keyed by id since several rows can ask at once.
  let confirmingPaymentDelete = $state<string | null>(null);

  // Shared reset for landing on a new unsaved draft (openNew() and openDuplicate()), so they can't drift on
  // which confirm/error state is cleared.
  function resetToNewComposer() {
    openerId = null;
    view = "detail";
    detailId = null;
    detail = null;
    detailError = null;
    formErrors = [];
    confirmingCancel = false;
    confirmingDelete = false;
    confirmingLineRemove = null;
    confirmingMarkSent = false;
    confirmingEmail = false;
    statusActionMessage = "";
    statusActionFailed = false;
  }

  function openNew() {
    resetToNewComposer();
    form = emptyForm(cfg.invoicing.salesTaxPct, newInvoiceDates(cfg.invoicing.dueDays, todayInTimezone(cfg.timezone)));
    composerBaseline = JSON.stringify(form);
  }

  // "Duplicate": opens the new-draft composer pre-seeded via formFromInvoiceDuplicate; the way to continue a
  // canceled invoice (a canceled invoice can't be reopened), also offered from sent/paid. Dates get the fresh-draft prefill, not the
  // source's.
  function openDuplicate(inv: Invoice) {
    resetToNewComposer();
    form = { ...formFromInvoiceDuplicate(inv), ...newInvoiceDates(cfg.invoicing.dueDays, todayInTimezone(cfg.timezone)) };
    composerBaseline = JSON.stringify(form);
  }

  // Staleness token: openDetail can re-enter while a fetch is in flight (quick double-open, fast
  // back/forward). A superseded call bails before touching state or the address bar, so it can't reopen an
  // abandoned record or rewrite the hash.
  let openSeq = 0;

  // The row that opened the detail view, so "Back to list" can return focus to it (WCAG 2.4.3).
  let openerId: string | null = null;

  async function openDetail(id: string, opts?: { silent?: boolean }) {
    const seq = ++openSeq;
    openerId = id;
    view = "detail";
    detailId = id;
    detail = null;
    detailLoading = true;
    detailError = null;
    formErrors = [];
    confirmingCancel = false;
    confirmingDelete = false;
    confirmingLineRemove = null;
    confirmingMarkSent = false;
    confirmingEmail = false;
    statusActionMessage = "";
    statusActionFailed = false;
    paymentErrors = [];
    confirmingPaymentDelete = null;
    try {
      const inv = await getInvoice(id);
      if (seq !== openSeq) return;
      detail = inv;
      form = formFromInvoice(inv);
      paymentForm = defaultPaymentForm(inv.balanceCents, cfg.invoicing.paymentMethods[0] ?? "", todayInTimezone(cfg.timezone));
      setDetailHash(id);
    } catch (err) {
      if (seq !== openSeq) return;
      if (opts?.silent) {
        // Stale or bookmarked id: fall back to the list quietly instead of an error banner over an empty
        // detail. replaceState, because this corrects a URL that was already wrong.
        closeDetailView();
        if (location.hash !== INVOICES_HASH) history.replaceState(null, "", INVOICES_HASH);
      } else {
        detailError = err instanceof Error ? err.message : "Couldn't load invoice.";
      }
    } finally {
      if (seq === openSeq) detailLoading = false;
    }
  }

  // Baseline for a not-yet-created invoice (openNew/openDuplicate set it). A saved invoice's baseline is
  // formFromInvoice(detail).
  let composerBaseline = $state("");
  const dirty = $derived(
    view === "detail" &&
      !detailLoading &&
      (detail ? JSON.stringify(form) !== JSON.stringify(formFromInvoice(detail)) : JSON.stringify(form) !== composerBaseline),
  );

  function requestBack() {
    if (canLeave(dirty)) backToList();
  }

  $effect(() => {
    unsavedForm.current = dirty;
    return () => {
      unsavedForm.current = false;
    };
  });

  function backToList(focusId: string | null = openerId) {
    closeDetailView();
    if (location.hash !== INVOICES_HASH) location.hash = INVOICES_HASH; // pushes a history entry
    void focusRowOrHeading(focusId);
  }

  function addLine() {
    form.lines = [...form.lines, { description: "", quantity: 1, unitPrice: 0, discount: 0, quoteId: "", kind: "item", itemId: "", optionName: "" }];
  }
  function askRemoveLine(i: number) {
    confirmingLineRemove = i;
  }
  function cancelRemoveLine() {
    confirmingLineRemove = null;
  }
  function removeLine(i: number) {
    form.lines = form.lines.filter((_, idx) => idx !== i);
    confirmingLineRemove = null;
    // The confirmed row is gone: focus the line that took its place, else Add line.
    void focusAfterRowRemoval("remove-line", i, form.lines.length, "add-line");
  }

  async function save() {
    if (saving) return;
    saving = true;
    formErrors = [];
    try {
      const input = buildInput(form);
      const result = detailId === null ? await createInvoice(input) : await updateInvoice(detailId, input);
      detail = result;
      detailId = result.id;
      form = formFromInvoice(result);
      setDetailHash(result.id); // a freshly-created draft becomes addressable the moment it exists
      await refresh();
      announce(`Invoice ${result.number} saved.`);
    } catch (err) {
      formErrors = [err instanceof Error ? err.message : "Couldn't save invoice."];
    } finally {
      saving = false;
    }
  }

  let statusSaving = false;
  // Past-tense phrase for the status line after a successful change.
  const STATUS_DONE: Record<InvoiceStatus, string> = { draft: "set to draft", sent: "marked sent", paid: "marked paid", canceled: "canceled" };
  async function changeStatus(status: InvoiceStatus) {
    if (!detail || statusSaving) return;
    statusSaving = true;
    statusActionMessage = "";
    statusActionFailed = false;
    try {
      const updated = await setInvoiceStatus(detail.id, status);
      detail = updated;
      form = formFromInvoice(updated);
      paymentForm = defaultPaymentForm(updated.balanceCents, cfg.invoicing.paymentMethods[0] ?? "", todayInTimezone(cfg.timezone));
      confirmingCancel = false;
      confirmingMarkSent = false;
      statusActionMessage = `Invoice ${updated.number} ${STATUS_DONE[status]}.`;
      // The pressed button is replaced by the next status's actions; keep focus in the action row.
      void refocusIfLost(".status-actions button");
      await refresh();
    } catch (err) {
      statusActionFailed = true;
      confirmingCancel = false;
      statusActionMessage = err instanceof Error ? err.message : "Couldn't update status.";
      void refocusIfLost(".status-actions button");
    } finally {
      statusSaving = false;
    }
  }

  // "Mark sent" always asks first: a sent invoice can't go back to draft.
  function markSentClick() {
    if (!detail) return;
    confirmingMarkSent = true;
  }
  function askSendEmail() {
    if (emailing) return;
    confirmingEmail = true;
  }
  function cancelSendEmail() {
    confirmingEmail = false;
  }
  function cancelMarkSent() {
    confirmingMarkSent = false;
  }

  function askCancel() {
    confirmingCancel = true;
  }
  function cancelCancel() {
    confirmingCancel = false;
  }

  function askDelete() {
    confirmingDelete = true;
  }
  function cancelDeleteConfirm() {
    confirmingDelete = false;
  }
  let deletingInvoice = false;
  async function confirmDeleteInvoice() {
    if (!detail || deletingInvoice) return;
    deletingInvoice = true;
    const { id, number } = detail;
    const next = neighborId(invoices.map((x) => x.id), id);
    try {
      await deleteInvoice(id);
      await refresh();
      backToList(next);
      announce(`Invoice ${number} deleted.`);
    } catch (err) {
      statusActionFailed = true;
      statusActionMessage = err instanceof Error ? err.message : "Couldn't delete invoice.";
      confirmingDelete = false;
    } finally {
      deletingInvoice = false;
    }
  }

  async function sendInvoiceEmailAction() {
    if (!detail || emailing) return;
    emailing = true;
    confirmingEmail = false;
    statusActionMessage = "";
    statusActionFailed = false;
    try {
      const result = await sendInvoiceEmail(detail.id);
      if (!result.ok) {
        statusActionFailed = true;
        statusActionMessage = result.error;
        return;
      }
      detail = result.data;
      form = formFromInvoice(result.data);
      paymentForm = defaultPaymentForm(result.data.balanceCents, cfg.invoicing.paymentMethods[0] ?? "", todayInTimezone(cfg.timezone));
      const sent = result.data.sends.at(-1);
      statusActionFailed = false;
      statusActionMessage = sent ? `Emailed to ${sent.recipient}.` : "Emailed to client.";
      await refresh();
    } catch (err) {
      statusActionFailed = true;
      // A thrown error (including sendApi.ts's 45s abort) leaves the outcome unknown: the server may have
      // sent the email and committed a send record. Re-fetch the delivery list and word the message as
      // indeterminate, unless it is plainly a non-abort failure.
      statusActionMessage =
        err instanceof Error && err.name !== "TimeoutError"
          ? err.message
          : "The request timed out or the connection dropped. Check the delivery history below before re-sending.";
      await refresh();
      // Re-fetch the open detail directly, not via openDetail(), which would reset statusActionMessage and
      // clobber the message just set.
      if (detailId) {
        try {
          const inv = await getInvoice(detailId);
          detail = inv;
          form = formFromInvoice(inv);
        } catch {
          // Keep the indeterminate message; the detail just doesn't get a refreshed delivery list this time.
        }
      }
    } finally {
      emailing = false;
    }
  }

  async function printInvoice() {
    if (!detail) return;
    // Mutual exclusion with the quote print job: only one #print-view mounts at a time (see invoicePrintJob
    // in state.svelte.ts).
    printJob.current = null;
    invoicePrintJob.current = detail;
    await tick();
    window.print();
  }

  async function addPaymentSubmit() {
    if (!detail || paymentSaving) return;
    paymentSaving = true;
    paymentErrors = [];
    const amountCents = toCents(paymentForm.amount);
    try {
      const updated = await addPayment(detail.id, {
        paidDate: paymentForm.paidDate,
        amountCents,
        method: paymentForm.method,
        note: paymentForm.note,
        reference: paymentForm.reference,
      });
      detail = updated;
      form = formFromInvoice(updated);
      paymentForm = defaultPaymentForm(updated.balanceCents, cfg.invoicing.paymentMethods[0] ?? "", todayInTimezone(cfg.timezone));
      announce(`Payment of ${money(amountCents, cfg)} recorded. Balance ${money(updated.balanceCents, cfg)}.`);
      // The list view's paidCents/balanceCents/status are now stale; the next list load refetches (same
      // tolerance as save()).
      await refresh();
    } catch (err) {
      paymentErrors = [err instanceof Error ? err.message : "Couldn't record payment."];
    } finally {
      paymentSaving = false;
    }
  }

  function askDeletePayment(id: string) {
    confirmingPaymentDelete = id;
  }
  function cancelDeletePaymentConfirm() {
    confirmingPaymentDelete = null;
  }
  let deletingPayment = false;
  async function confirmDeletePayment(paymentId: string) {
    if (!detail || deletingPayment) return;
    deletingPayment = true;
    paymentErrors = [];
    const next = neighborId(detail.payments.map((p) => p.id), paymentId);
    try {
      const updated = await deletePayment(detail.id, paymentId);
      detail = updated;
      form = formFromInvoice(updated);
      paymentForm = defaultPaymentForm(updated.balanceCents, cfg.invoicing.paymentMethods[0] ?? "", todayInTimezone(cfg.timezone));
      confirmingPaymentDelete = null;
      announce(`Payment deleted. Balance ${money(updated.balanceCents, cfg)}.`);
      // The deleted row is gone: focus the next payment's Delete button, else the previous, else the date field.
      void focusAfterUpdate(
        () => (next ? document.querySelector<HTMLElement>(keySelector(`delete-payment-${next}`)) : null) ?? document.querySelector<HTMLElement>(".payments-card input"),
      );
      await refresh();
    } catch (err) {
      paymentErrors = [err instanceof Error ? err.message : "Couldn't delete payment."];
      confirmingPaymentDelete = null;
    } finally {
      deletingPayment = false;
    }
  }
</script>

<svelte:window onhashchange={syncDetailFromHash} onkeydown={handleDetailKeydown} />

<div class="page">
  {#if view === "list"}
    <InvoiceList
      {invoices}
      {loading}
      {unavailable}
      {cfg}
      {total}
      {loadingMore}
      backfilling={backfill.backfilling}
      backfillFailed={backfill.backfillFailed}
      onNew={openNew}
      onOpenDetail={(id) => openDetail(id)}
      onLoadMore={loadMore}
      onFilterActive={backfill.ensureReady}
    />
  {:else}
    <InvoiceDetail
      {cfg}
      {clients}
      {items}
      {detail}
      {detailLoading}
      {detailError}
      {form}
      {formErrors}
      {confirmingLineRemove}
      {saving}
      {statusActionMessage}
      {statusActionFailed}
      {confirmingMarkSent}
      {confirmingCancel}
      {confirmingDelete}
      {confirmingEmail}
      {emailing}
      {paymentForm}
      {paymentErrors}
      {paymentSaving}
      {confirmingPaymentDelete}
      onBackToList={requestBack}
      onLinkMatchingClient={linkMatchingClient}
      onAddLine={addLine}
      onAskRemoveLine={askRemoveLine}
      onCancelRemoveLine={cancelRemoveLine}
      onRemoveLine={removeLine}
      onSave={save}
      onDuplicate={openDuplicate}
      onMarkSentClick={markSentClick}
      onCancelMarkSent={cancelMarkSent}
      onChangeStatus={changeStatus}
      onAskCancel={askCancel}
      onCancelCancel={cancelCancel}
      onAskDelete={askDelete}
      onCancelDeleteConfirm={cancelDeleteConfirm}
      onConfirmDeleteInvoice={confirmDeleteInvoice}
      onAskSendEmail={askSendEmail}
      onCancelSendEmail={cancelSendEmail}
      onSendEmail={sendInvoiceEmailAction}
      onPrint={printInvoice}
      onAddPayment={addPaymentSubmit}
      onAskDeletePayment={askDeletePayment}
      onCancelDeletePaymentConfirm={cancelDeletePaymentConfirm}
      onConfirmDeletePayment={confirmDeletePayment}
    />
  {/if}
</div>
