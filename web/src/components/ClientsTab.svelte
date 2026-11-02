<script lang="ts">
  import { canLeave, escapeBelongsToField } from "../lib/unsavedGuard";
  import { unsavedForm } from "../lib/unsavedForm.svelte";
  import { onMount } from "svelte";
  import type { EffectiveConfig } from "../lib/engine/index";
  import { listClients, createClient, updateClient, deleteClient, type Client, type ClientInput } from "../lib/clientsApi";
  import { listInvoices, type InvoiceSummary } from "../lib/invoicesApi";
  import type { SavedQuote } from "../lib/api";
  import { sortQuotes } from "../lib/quotesFile";
  import { listQuotes, toSavedQuote } from "../lib/quotesApi";
  import { displayStatus } from "../lib/quoteStatus";
  import { displayPriceCents } from "../lib/quoteRow";
  import { money, formatDate } from "../lib/format";
  import { consumePendingOpen, pendingOpen } from "../lib/state.svelte";
  import { syncDetail } from "../lib/hashRoute";
  import { resolveDetailMiss, refetchLimit, INITIAL_PAGE_LIMIT } from "../lib/pagination";
  import { createBackfillController } from "../lib/backfillController.svelte";
  import { mdiArrowLeft } from "@mdi/js";
  import Icon from "./Icon.svelte";
  import StatusBadge from "./StatusBadge.svelte";
  import AttributionLine from "./AttributionLine.svelte";
  import LoadMoreRow from "./LoadMoreRow.svelte";
  import BackfillStatus from "./BackfillStatus.svelte";
  import DetailTitle from "./DetailTitle.svelte";
  import { announce } from "../lib/a11y.svelte";
  import { confirmFocus, focusRowOrHeading, keySelector, neighborId } from "./a11yFocus";

  let { cfg }: { cfg: EffectiveConfig } = $props();

  let clients = $state<Client[]>([]);
  let loading = $state(true);
  // Set when listClients() returns null (network/server failure). There is no localStorage fallback for
  // clients; it shows a muted note and an empty list.
  let unavailable = $state(false);

  // Server pagination (server/pagination.go's opt-in ?limit=&offset=): `clients` holds only the loaded window
  // and `total` is the pre-slice count (X-Total-Count). fullyLoaded says whether the window covers every row.
  let total = $state(0);
  const fullyLoaded = $derived(clients.length >= total);
  let loadingMore = $state(false);

  let searchText = $state("");
  const filteredClients = $derived.by(() => {
    const q = searchText.trim().toLowerCase();
    if (q === "") return clients;
    return clients.filter(
      (c) => c.name.toLowerCase().includes(q) || c.email.toLowerCase().includes(q) || c.phone.toLowerCase().includes(q),
    );
  });

  // Search must never run over a partially-loaded list: with text in the box and the list not fully loaded,
  // backfill every remaining row first (shared latch, see backfillController.svelte.ts). Re-runs per
  // keystroke via the $effect; ensureReady is a no-op once fullyLoaded, backfilling, failed or blank.
  const backfill = createBackfillController<Client>({
    getItems: () => clients,
    getTotal: () => total,
    fetchPage: (page) => listClients(page),
    setItems: (items) => {
      clients = items;
      total = items.length;
    },
  });
  $effect(() => {
    backfill.ensureReady(searchText);
  });

  // The first load fetches 100; a refetch after a mutation asks for at least as many rows as are loaded, so
  // the loaded window and any in-progress backfill don't shrink.
  async function refresh() {
    loading = true;
    const limit = refetchLimit(clients.length);
    const result = await listClients({ limit, offset: 0 });
    if (result === null) {
      unavailable = true;
      clients = [];
      total = 0;
    } else {
      unavailable = false;
      clients = result.items;
      total = result.total;
    }
    loading = false;
  }

  async function loadMore() {
    loadingMore = true;
    const result = await listClients({ limit: INITIAL_PAGE_LIMIT, offset: clients.length });
    if (result !== null) {
      clients = [...clients, ...result.items];
      total = result.total;
    }
    loadingMore = false;
  }

  // Every invoice/saved quote, loaded once on mount and filtered to the open client by the $derived below
  // rather than refetched on each open; neither list is huge (DashboardTab/QuotesTab load the same way).
  let allInvoices = $state<InvoiceSummary[]>([]);
  let allQuotes = $state<SavedQuote[]>([]);
  // Set only when the quotes list fails to load (listInvoices folds its own failure into an empty list),
  // shown next to the Quotes section so a failed load doesn't read as "no quotes".
  let quotesUnavailable = $state(false);

  async function loadLedgers() {
    const [invoiceResult, quotesResult] = await Promise.all([listInvoices(), listQuotes()]);
    allInvoices = invoiceResult ?? [];
    if (quotesResult === null) {
      quotesUnavailable = true;
      allQuotes = [];
    } else {
      allQuotes = quotesResult.map(toSavedQuote);
    }
  }

  // List <-> detail navigation, addressable via "#/clients#<id>" (App.svelte's tabFromHash strips it for tab
  // routing; this component reacts locally), like InvoicesTab/RequestsTab. `clients` holds only the loaded
  // window, so a miss backfills the rest via ensureFullyLoaded and re-looks-up; that await needs the same
  // openDetail staleness-token guard as the other tabs.

  const CLIENTS_HASH = "#/clients";

  function setDetailHash(id: string) {
    const target = `${CLIENTS_HASH}#${id}`;
    if (location.hash !== target) location.hash = target; // pushes a history entry
  }

  function emptyForm(): ClientInput {
    return { name: "", email: "", phone: "", billingAddress: "", shippingAddress: "", notes: "" };
  }

  function formFromClient(c: Client): ClientInput {
    return {
      name: c.name,
      email: c.email,
      phone: c.phone,
      billingAddress: c.billingAddress,
      shippingAddress: c.shippingAddress,
      notes: c.notes,
    };
  }

  // Field-by-field compare (every ClientInput field is a plain string) so Save enables only when the form
  // differs from what was loaded, like Settings' configsEqual.
  function clientInputsEqual(a: ClientInput, b: ClientInput): boolean {
    return (
      a.name === b.name &&
      a.email === b.email &&
      a.phone === b.phone &&
      a.billingAddress === b.billingAddress &&
      a.shippingAddress === b.shippingAddress &&
      a.notes === b.notes
    );
  }

  type View = "list" | "detail";
  let view = $state<View>("list");
  // null while composing a not-yet-created client; the loaded client's id once one exists (created, opened
  // from the list, or a deep link).
  let detailId = $state<string | null>(null);
  let detail = $state<Client | null>(null);
  let detailError = $state<string | null>(null);
  // True only while a miss on the loaded window is being resolved by backfill (see openDetail); distinct from
  // `detail === null`, which also describes the "new client" composer.
  let detailLoading = $state(false);
  let form = $state<ClientInput>(emptyForm());
  // The form as last loaded or saved: Save enables only while `form` differs (clientInputsEqual). Reseeded
  // wherever `form` is.
  let formBaseline = $state<ClientInput>(emptyForm());
  const dirty = $derived(!clientInputsEqual(form, formBaseline));
  let formErrors = $state<string[]>([]);
  let saving = $state(false);
  let confirmingDelete = $state(false);

  // Resets the view state of "closing" without touching the URL: used by backToList() (which owns the URL)
  // and syncDetailFromHash (where location.hash already changed, so rewriting it would churn history).
  function closeDetailView() {
    view = "list";
    detailId = null;
    detail = null;
    detailError = null;
    detailLoading = false;
    formErrors = [];
    confirmingDelete = false;
  }

  function openNew() {
    openerId = null;
    view = "detail";
    detailId = null;
    detail = null;
    detailError = null;
    form = emptyForm();
    formBaseline = emptyForm();
    formErrors = [];
    confirmingDelete = false;
  }

  // Staleness token: openDetail can re-enter while a previous backfill is in flight (quick double-open, fast
  // back/forward). A call superseded by a later one bails before touching state or the address bar.
  let openSeq = 0;

  function applyDetail(c: Client) {
    view = "detail";
    detailId = c.id;
    detail = c;
    detailError = null;
    detailLoading = false;
    form = formFromClient(c);
    formBaseline = formFromClient(c);
    formErrors = [];
    confirmingDelete = false;
    setDetailHash(c.id);
  }

  function handleDetailMiss(id: string, opts?: { silent?: boolean }) {
    if (opts?.silent) {
      closeDetailView();
      if (location.hash !== CLIENTS_HASH) history.replaceState(null, "", CLIENTS_HASH);
    } else {
      view = "detail";
      detailId = id;
      detail = null;
      detailLoading = false;
      detailError = "Client not found.";
    }
  }

  // Looks the id up in the loaded `clients` first; on a miss, backfills via ensureFullyLoaded and
  // re-looks-up, so a client past the first page still resolves. `silent`: a genuinely nonexistent id falls
  // back to the list quietly instead of an error banner over an empty detail.
  // The row that opened the detail view, so "Back to list" can return focus to it (WCAG 2.4.3).
  let openerId: string | null = null;

  async function openDetail(id: string, opts?: { silent?: boolean }) {
    const seq = ++openSeq;
    openerId = id;
    const found = clients.find((c) => c.id === id);
    if (found) {
      applyDetail(found);
      return;
    }
    if (fullyLoaded) {
      handleDetailMiss(id, opts);
      return;
    }
    // Not in the loaded window and more rows exist: show the loading state during the backfill instead of
    // flashing the "Add client" composer (what an empty `detail` renders as).
    view = "detail";
    detailId = id;
    detail = null;
    detailError = null;
    detailLoading = true;
    const resolved = await resolveDetailMiss(id, clients, total, (c) => c.id, (page) => listClients(page));
    if (seq !== openSeq) return;
    if (resolved.kind === "error") {
      // The backfill itself failed (network or server): a real load failure, not a 404, so it always surfaces
      // even when silent.
      detailLoading = false;
      detailError = "Couldn't load client.";
      return;
    }
    // The backfill ran to completion either way, so the loaded window and total are authoritative whether or
    // not the id turned up.
    clients = resolved.items;
    total = resolved.items.length;
    if (resolved.kind === "miss") {
      handleDetailMiss(id, opts);
      return;
    }
    applyDetail(resolved.item);
  }

  // Back to list, Escape: asks first when the form has unsaved edits. backToList() stays unguarded for
  // flows that just saved or deleted.
  function requestBack() {
    if (canLeave(view === "detail" && dirty)) backToList();
  }

  $effect(() => {
    unsavedForm.current = view === "detail" && dirty;
    return () => {
      unsavedForm.current = false;
    };
  });

  function backToList(focusId: string | null = openerId) {
    closeDetailView();
    if (location.hash !== CLIENTS_HASH) location.hash = CLIENTS_HASH; // pushes a history entry
    void focusRowOrHeading(focusId);
  }

  // Reacts to hash changes while mounted: back/forward between detail and list (or two ids), or a manual edit
  // of the id. A hashchange from this component's own setDetailHash()/backToList() is a no-op (the id matches
  // or the hash is bare).
  function syncDetailFromHash() {
    syncDetail(location.hash, CLIENTS_HASH, detailId, view === "detail", (id) => openDetail(id, { silent: true }), () => {
      closeDetailView();
      void focusRowOrHeading(openerId);
    });
  }

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
    requestBack();
  }

  onMount(async () => {
    await Promise.all([refresh(), loadLedgers()]);
    // A "#/clients#<id>" deep link (App.svelte's routePendingOpen) sets this before the tab mounts; open that
    // client instead of the list. silent: true so a stale bookmarked id falls back to the list quietly.
    const id = consumePendingOpen("clients");
    if (id) {
      // Set the hash before opening: a hashchange queued by this tab's mount (App's activate()) fires
      // asynchronously and would otherwise see a bare #/clients that doesn't match detailId and close the
      // just-opened detail. replaceState, because this completes the navigation that brought us here rather
      // than starting a new one.
      history.replaceState(null, "", `${CLIENTS_HASH}#${id}`);
      openDetail(id, { silent: true });
    }
  });

  async function save() {
    if (saving || !dirty) return;
    saving = true;
    formErrors = [];
    try {
      const result = detailId === null ? await createClient(form) : await updateClient(detailId, form);
      detail = result;
      detailId = result.id;
      form = formFromClient(result);
      formBaseline = formFromClient(result); // saved state is the new clean baseline
      setDetailHash(result.id); // a freshly-created client becomes addressable the moment it exists
      await refresh();
      announce(`${result.name || "Client"} saved.`);
    } catch (err) {
      formErrors = [err instanceof Error ? err.message : "Couldn't save client."];
      announce("Couldn't save the client. See the errors above the form.", "assertive");
    } finally {
      saving = false;
    }
  }

  function askDelete() {
    confirmingDelete = true;
  }
  function cancelDelete() {
    confirmingDelete = false;
  }
  let deleting = $state(false);
  async function confirmDeleteClient() {
    if (!detail || deleting) return;
    deleting = true;
    const { id, name } = detail;
    const next = neighborId(clients.map((x) => x.id), id);
    try {
      await deleteClient(id);
      await refresh();
      backToList(next);
      announce(`${name || "Client"} deleted.`);
    } catch (err) {
      formErrors = [err instanceof Error ? err.message : "Couldn't delete client."];
      confirmingDelete = false;
    } finally {
      deleting = false;
    }
  }

  // Quotes/Invoices sections and the money line, derived from the whole-app lists loaded in loadLedgers(),
  // filtered to the open client.

  // Captured into a local `c` in each block: TS can't narrow a reactive `T | null` across a closure boundary.
  const clientQuotes = $derived.by(() => {
    const c = detail;
    if (!c) return [];
    return sortQuotes(allQuotes.filter((q) => q.clientId === c.id), "newest");
  });
  // Newest first by createdAt, not issueDate: a draft's issueDate is often blank and would sort arbitrarily.
  const clientInvoices = $derived.by(() => {
    const c = detail;
    if (!c) return [];
    return [...allInvoices.filter((i) => i.clientId === c.id)].sort((a, b) => b.createdAt.localeCompare(a.createdAt));
  });

  // "Invoiced/paid/outstanding" count only invoices that left draft (still being composed) and aren't
  // canceled (voided). Outstanding narrows to status "sent", mirroring server/dashboard.go's query so it
  // matches the Dashboard tab.
  const clientMoney = $derived.by(() => {
    const counted = clientInvoices.filter((i) => i.status !== "draft" && i.status !== "canceled");
    const totalInvoicedCents = counted.reduce((sum, i) => sum + i.totalCents, 0);
    const totalPaidCents = counted.reduce((sum, i) => sum + i.paidCents, 0);
    const outstandingCents = clientInvoices
      .filter((i) => i.status === "sent")
      .reduce((sum, i) => sum + i.balanceCents, 0);
    return { totalInvoicedCents, totalPaidCents, outstandingCents };
  });

  function openInvoice(id: string) {
    // Same cross-tab open mechanism as InvoicesTab's detail view and DashboardTab's recent invoices
    // (pendingOpen in state.svelte.ts); InvoicesTab reads and clears it on mount.
    pendingOpen.current = { tab: "invoices", id };
    location.hash = "#/invoices";
  }

  // The Quotes ledger has no addressable per-record view: its only "open" action (QuotesTab's openInEditor)
  // jumps into the Quote tab's edit form, a mismatched action from a read-oriented client summary. Linking to
  // the ledger (#/quotes) keeps this row a "go to where the record lives" action like Invoices/Requests.
  function openQuotesLedger() {
    location.hash = "#/quotes";
  }
</script>

<svelte:window onhashchange={syncDetailFromHash} onkeydown={handleDetailKeydown} />

<div class="page">
  {#if view === "list"}
    <div class="head-row">
      <h1 tabindex="-1">Clients</h1>
      <button type="button" class="primary" onclick={openNew}>Add client</button>
    </div>

    {#if unavailable}
      <p class="banner-warn" role="alert">Couldn't load clients. The server couldn't be reached.</p>
    {/if}

    {#if clients.length > 0}
      <input id="clients-filter" class="search-input" placeholder="Search by name, email, or phone" aria-label="Search clients" bind:value={searchText} />
    {/if}

    <BackfillStatus backfilling={backfill.backfilling} backfillFailed={backfill.backfillFailed} verb="search" />

    <div class="card">
      <ul class="plain-list">
        {#if loading}
          <li class="empty-state">Loading clients…</li>
        {:else}
          {#each filteredClients as c (c.id)}
            <li class="client-row">
              <button type="button" class="row-open" data-row-id={c.id} onclick={() => openDetail(c.id)}>
                <span class="c-name">{c.name}</span>
                <span class="c-contact">{[c.email, c.phone].filter(Boolean).join(" · ")}</span>
                {#if c.notes}<span class="c-notes">{c.notes}</span>{/if}
              </button>
            </li>
          {:else}
            <li class="empty-state">{clients.length === 0 ? "No clients yet. Add one with the Add client button above." : "No clients match the search."}</li>
          {/each}
        {/if}
      </ul>
      {#if !loading && searchText.trim() === ""}
        <LoadMoreRow shown={clients.length} {total} {loadingMore} onLoadMore={loadMore} />
      {/if}
    </div>
  {:else}
    <div class="head-row">
      <button type="button" class="back-link" onclick={requestBack}><Icon path={mdiArrowLeft} /> Back to list</button>
    </div>

    <div class="card">
      {#if detailLoading}
        <p>Loading…</p>
      {:else}
      <DetailTitle>{detail ? detail.name : detailError ? "Client" : "Add client"}</DetailTitle>
      {#if detail}<AttributionLine createdBy={detail.createdBy} updatedBy={detail.updatedBy} />{/if}

      {#if detailError}
        <p class="banner-warn" role="alert">{detailError}</p>
      {:else}
        {#if formErrors.length > 0}
          <div role="alert">
            <ul class="errors">
              {#each formErrors as e (e)}<li>{e}</li>{/each}
            </ul>
          </div>
        {/if}
        <div class="field-rows">
          <label class="field">
            <span>Name</span>
            <input bind:value={form.name} autocomplete="name" />
          </label>
          <label class="field">
            <span>Email</span>
            <input type="email" bind:value={form.email} autocomplete="email" />
          </label>
          <label class="field">
            <span>Phone</span>
            <input type="tel" bind:value={form.phone} autocomplete="tel" />
          </label>
        </div>
        <label class="field">
          <span>Billing address</span>
          <textarea rows="3" bind:value={form.billingAddress} autocomplete="street-address"></textarea>
        </label>
        <label class="field">
          <span>Shipping address</span>
          <textarea rows="3" bind:value={form.shippingAddress} autocomplete="street-address"></textarea>
        </label>
        <label class="field">
          <span>Notes</span>
          <textarea rows="3" bind:value={form.notes}></textarea>
        </label>
        <div class="form-actions">
          <button type="button" class="primary" onclick={save} aria-disabled={saving || !dirty}>{saving ? "Saving…" : "Save"}</button>
        </div>

        {#if detail}
          <div class="subcard">
            <h3>Quotes</h3>
            {#if clientQuotes.length === 0}
              <p class="empty-state">No saved quotes are linked to this client yet. Pick this client in the Saved quotes panel on the New quote page.</p>
            {:else}
              <ul class="plain-list">
                {#each clientQuotes as q (q.id)}
                  {@const priceCents = displayPriceCents(q)}
                  {@const status = displayStatus(q, cfg.quote.validDays, Date.now())}
                  <li>
                    <button type="button" class="row-link" onclick={openQuotesLedger}>
                      <span class="q-label">{q.label}</span>
                      <span class="q-price">{priceCents !== null ? money(priceCents, cfg) : "cost only"}</span>
                      <StatusBadge status={status} />
                      <span class="q-date">{formatDate(q.savedAt)}</span>
                    </button>
                  </li>
                {/each}
              </ul>
            {/if}
            {#if quotesUnavailable}
              <p class="advisory-text">Saved quotes unavailable; this list may be incomplete.</p>
            {/if}
          </div>

          <div class="subcard">
            <h3>Invoices</h3>
            {#if clientInvoices.length === 0}
              <p class="empty-state">No invoices for this client yet. Convert a saved quote, or create one from the Invoices page.</p>
            {:else}
              <ul class="plain-list">
                {#each clientInvoices as inv (inv.id)}
                  <li>
                    <button type="button" class="row-link" onclick={() => openInvoice(inv.id)}>
                      <span class="inv-number">{inv.number}</span>
                      <StatusBadge status={inv.status} dueDate={inv.dueDate} />
                      <span class="inv-total">{money(inv.totalCents, cfg)}</span>
                      <span class="inv-date">{inv.issueDate || "-"}</span>
                    </button>
                  </li>
                {/each}
              </ul>
              <div class="money-line">
                <span><span class="lbl">Total invoiced</span> {money(clientMoney.totalInvoicedCents, cfg)}</span>
                <span><span class="lbl">Total paid</span> {money(clientMoney.totalPaidCents, cfg)}</span>
                <span><span class="lbl">Outstanding</span> {money(clientMoney.outstandingCents, cfg)}</span>
              </div>
            {/if}
          </div>

          <div class="subcard">
            {#if confirmingDelete}
              <p class="delete-consequences">
                Invoices keep their own copy of this client's details. Saved quotes keep the name but lose the link.
              </p>
              <div class="form-actions">
                <button type="button" class="danger" aria-disabled={deleting} aria-busy={deleting} onclick={confirmDeleteClient} use:confirmFocus={{ restore: keySelector("delete-client"), cancel: cancelDelete }}>Delete client?</button>
                <button type="button" onclick={cancelDelete}>Cancel</button>
              </div>
            {:else}
              <button type="button" data-focus-key="delete-client" onclick={askDelete}>Delete client</button>
            {/if}
          </div>
        {/if}
      {/if}
      {/if}
    </div>
  {/if}
</div>

<style>
  /* .page, .banner-warn, .errors, .form-actions and .head-row's layout come from theme.css; the h1 is
   * nested one level down, so theme's `.page > h1` doesn't reach it and this override stays. */
  .head-row h1 { margin: 0; }
  .back-link { display: inline-flex; align-items: center; gap: 0.35rem; }

  .card .field { margin-bottom: 0.6rem; }
  .card textarea { resize: vertical; }

  .search-input { width: 100%; margin-bottom: 0.75rem; }

  li { border-top: 1px solid var(--border); }
  li:first-child { border-top: none; }
  /* A plain <li onclick> trips svelte-check's a11y_no_noninteractive_element_interactions, so the row has
   * no handlers; .row-open is a real <button> and the only thing that opens it. */
  .row-open {
    width: 100%;
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 0.1rem;
    background: none;
    border: none;
    padding: 0.5rem 0;
    text-align: left;
    cursor: pointer;
  }
  .row-open:hover { background: var(--surface-2); }
  .c-name { font-weight: 600; }
  .c-contact { font-size: var(--text-sm); color: var(--muted); }
  .c-notes { font-size: var(--text-sm); color: var(--muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 30rem; }

  h3 { margin: 0 0 var(--space-2); }

  /* Quote/invoice rows: same shape as RequestsTab's .row-link (a full-width button of label/status/date
   * columns), shared by both sections. */
  .row-link {
    width: 100%;
    display: flex;
    align-items: center;
    gap: 0.75rem;
    flex-wrap: wrap;
    background: none;
    border: none;
    padding: 0.5rem 0;
    text-align: left;
    min-height: 32px;
  }
  .row-link:hover { background: var(--surface-2); }
  .q-label, .inv-number { flex: 1 1 8rem; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 600; }
  .q-price, .inv-total { font-variant-numeric: tabular-nums; min-width: 5rem; }
  .q-date, .inv-date { color: var(--muted); font-size: var(--text-sm); min-width: 6rem; text-align: right; }

  .money-line {
    display: flex;
    flex-wrap: wrap;
    gap: 1.25rem;
    margin-top: 0.75rem;
    padding-top: 0.6rem;
    border-top: 1px solid var(--border);
    font-variant-numeric: tabular-nums;
  }
  .money-line .lbl { display: block; }

  .delete-consequences { color: var(--muted); font-size: var(--text-sm); margin: 0 0 0.6rem; }
</style>
