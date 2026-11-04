<script lang="ts">
  import { formatDate } from "../lib/format";
  import { onMount } from "svelte";
  import type { EffectiveConfig, OrderBreakdown } from "../lib/engine/index";
  import type { SavedQuote } from "../lib/api";
  import { sortQuotes } from "../lib/quotesFile";
  import { listQuotes, getQuote, createQuote, updateQuote, toSavedQuote, adoptServerQuote, QuoteConflictError, type QuoteInputMeta } from "../lib/quotesApi";
  import {
    consumePendingOpen,
    quoteActions,
    quoteClient,
    quoteMeta,
    quoteSession,
    toOrderInputs,
    quoteEngineOptions,
    toFormState,
    restoreFormState,
    reconcileWithConfig,
    pinStoredRates,
    captureQuoteBaseline,
    quoteIsDirty,
    syncQuoteUrl,
  } from "../lib/state.svelte";
  import { prefill } from "../lib/prefill.svelte";
  import { unsavedForm } from "../lib/unsavedForm.svelte";
  import { costValid, orderCostHint } from "../lib/orderView";
  import { noteSaveAttempt } from "../lib/saveAttempt.svelte";
  import { quote, partLabel } from "../lib/state.svelte";
  import { formatSavedAt } from "../lib/quoteRow";
  import { listClients, createClient, type Client } from "../lib/clientsApi";
  import CardHelp from "./CardHelp.svelte";
  import { TILE_HELP } from "../lib/guideContent";
  import { stickyInset } from "../lib/stickyInset";

  let {
    cfg,
    order,
    pricingPending,
    settledOrder,
  }: {
    cfg: EffectiveConfig;
    order: OrderBreakdown;
    // True while QuoteTab's preview has a recompute in flight or queued behind its debounce.
    // saveRowDisabled() reads it because `order` holds only the last resolved answer (the pre-edit
    // placeholder within the debounce window), so gating on cost validity alone would let a Ctrl+S/Save
    // silently no-op in that window.
    pricingPending: boolean;
    // Awaits the in-flight debounce/round trip and resolves with the breakdown it settles on
    // (quotePreview.ts's settled()), or null if none. save()/update() call it first so the write uses a price
    // the server stands behind, not the stale placeholder.
    settledOrder: () => Promise<OrderBreakdown | null>;
  } = $props();

  // The saved-quote list, mapped via quotesApi's toSavedQuote (`.inputs` is the editor's form snapshot, not
  // the engine bag). Opening a quote reads it fresh from the server (getQuote), so this list only feeds the
  // "Recent saves" strip; save()/update() always build a fresh bag, options and formState from the live form.
  let quotes = $state<SavedQuote[]>([]);
  // Set once from a null listQuotes() (load failure); a muted note, not a blocking error, like ClientsTab.
  let quotesUnavailable = $state(false);
  let loading = $state(true);
  let saving = $state(false);
  // The saved quote the form is bound to, its label, version and save feedback live in quoteSession
  // (state.svelte.ts) so the heading, the sidebar and the top save buttons can read them. Bound means "Update"
  // writes to it; "Save as new" forks a copy.
  const loadedQuote = $derived(quoteSession.loadedId ? (quotes.find((x) => x.id === quoteSession.loadedId) ?? null) : null);
  // The ledger's delete can drop the loaded row from a separate mount with no call path back here; catch it
  // generically instead of threading a notification through every ledger delete. Only once the list has
  // loaded, and only for a quote this list has seen (a quote opened by URL may postdate the list).
  let seenIds = new Set<string>();
  $effect(() => {
    for (const q of quotes) seenIds.add(q.id);
    const id = quoteSession.loadedId;
    if (id !== null && !loading && seenIds.has(id) && !quotes.some((q) => q.id === id)) {
      quoteSession.loadedId = null;
      syncQuoteUrl();
    }
  });
  // Client chosen in the saverow picker, applied to save()/update(): the shared quoteClient box
  // (state.svelte.ts), so QuoteTab's print toolbar reads the same selection. Restoring seeds it from the
  // quote's clientId/clientName so "Update" without touching the picker preserves the link.

  // Client list for the saverow picker: loaded once, lazily, on first focus. A failed load degrades to
  // walk-in-only rather than blocking saving.
  let clients = $state<Client[] | null>(null);
  let clientsLoaded = $state(false);
  // The in-flight load, shared so near-simultaneous callers await one request. Keyed on the promise, not the
  // clientsLoaded boolean, which can't flip until the request resolves without reintroducing the race.
  let clientsPromise: Promise<Client[] | null> | null = null;
  async function ensureClientsLoaded(): Promise<void> {
    clientsPromise ??= listClients();
    clients = await clientsPromise;
    clientsLoaded = true;
  }

  // Compact "Recent saves" strip: the 5 most recent quotes. The full list (filter, sort, export/import,
  // convert, duplicate, delete) is on the Quotes ledger (QuotesTab.svelte, #/quotes); this is display only.
  const recentQuotes = $derived(sortQuotes(quotes, "newest").slice(0, 5));

  // Feedback for the latest save/restore: a single always-present live region (text swapped, not the element)
  // because announcements for freshly inserted regions are unreliable. The ledger has its own .action-status.
  // The text lives in quoteSession.status so the top save buttons can echo it.
  function setStatus(text: string, failed = false): void {
    quoteSession.status = { text, failed };
  }

  onMount(async () => {
    const list = await listQuotes();
    if (list === null) {
      quotesUnavailable = true;
    } else {
      quotes = list.map(toSavedQuote);
    }
    loading = false;
    // The ledger's "Open in editor" (and label link), or a pasted #/quote/<id>, sets this before the editor
    // mounts: the same one-shot handoff as InvoicesTab's pending-open (pendingOpen in state.svelte.ts).
    // Cleared on read. The quote already open in memory is kept as is, so coming Back to its URL doesn't
    // overwrite unsaved edits with the saved copy.
    const pendingId = consumePendingOpen("quote");
    if (pendingId && pendingId !== quoteSession.loadedId) {
      await openById(pendingId);
    } else if (!pendingId) {
      // Returning to the editor: make the address match what is open.
      syncQuoteUrl();
    }
    // Marks the form clean on the very first mount. Later mounts keep the baseline, so edits made before
    // leaving the tab still count as unsaved.
    if (quoteSession.baseline === null) captureQuoteBaseline();
  });

  // Mirror the list size for QuoteTab (first-run panel) once known.
  $effect(() => {
    if (!loading && !quotesUnavailable) quoteSession.savedCount = quotes.length;
  });

  /** Exact, case-insensitive, trimmed name match against the loaded client list (null if unloaded/failed). */
  function findClientMatch(name: string): Client | undefined {
    const trimmed = name.trim().toLowerCase();
    if (!trimmed) return undefined;
    return clients?.find((c) => c.name.trim().toLowerCase() === trimmed);
  }

  /** Keeps quoteClient's id and name in sync with each keystroke: name is what's typed; id is set only
   * while the text exactly matches an existing client, so an unmatched name leaves id empty (the
   * auto-create case in resolveClient()). restore() seeds both directly. */
  function handleClientInput(e: Event & { currentTarget: HTMLInputElement }): void {
    quoteClient.name = e.currentTarget.value;
    quoteClient.id = findClientMatch(quoteClient.name)?.id ?? "";
  }

  // A typed name matching no loaded client: the "this will create a new client" case. A match or empty field
  // needs no note.
  const clientIsNew = $derived(quoteClient.name.trim() !== "" && quoteClient.id === "");

  /** Shared client resolution for save() and update(): returns the {id?, name} for the quote's meta,
   * auto-creating a client when the typed name matches nobody. quoteClient.id already carries the "linked
   * and untouched" case (restore() seeds it; only handleClientInput's re-match changes it), so it
   * round-trips the link even if the client list never loaded. Only a typed name with no id needs the
   * list. */
  async function resolveClient(): Promise<{ id?: string; name: string } | undefined> {
    // Id first, before the empty-name guard: a linked client with a blank stored name must still round-trip
    // its link. Clearing the field can't reach here with an id set (handleClientInput zeroes it when the text
    // stops matching).
    if (quoteClient.id) return { id: quoteClient.id, name: quoteClient.name };
    const name = quoteClient.name.trim();
    if (!name) return undefined;

    await ensureClientsLoaded();
    const match = findClientMatch(name);
    if (match) {
      quoteClient.id = match.id;
      return { id: match.id, name: match.name };
    }
    if (clients === null) {
      // List unavailable: creating risks a duplicate once it returns, so save as a name-only walk-in. The
      // saverow's advisory note already says new clients can't be set right now.
      return { name };
    }
    // A failed create propagates to save()/update()'s catch (status) and aborts the save: the quote is never
    // written without the client it named.
    const created = await createClient({ name, email: "", phone: "", billingAddress: "", shippingAddress: "", notes: "" });
    clients = [...clients, created];
    quoteClient.id = created.id;
    return { id: created.id, name: created.name };
  }

  /** The label a new save gets: what was typed, else a dated default. Saving a copy of a loaded quote under
   * its own label would list two identical rows, so the copy says so. */
  function labelForNew(copying: boolean): string {
    const typed = quoteSession.label.trim();
    if (typed === "") return `Quote ${formatDate(new Date().toISOString())}`;
    if (copying && typed === loadedQuote?.label) return `${typed} (copy)`;
    return typed;
  }

  /** Creates a new saved quote from the form and binds the form to it, so the next save updates it instead of
   * creating another. With a quote loaded this is "Save as new": a fork that the user then works on. */
  async function save() {
    if (saving) return;
    // Set before the settledBreakdown() await so the save row stays disabled for the whole wait, and a second
    // Ctrl+S/click can't fire a second save.
    saving = true;
    const copying = quoteSession.loadedId !== null;
    try {
      const bd = await settledOrder();
      if (bd === null) {
        setStatus("Couldn't reach the server to price this quote, so nothing was saved. Change any field to retry.", true);
        return;
      }
      if (!costValid(bd)) {
        setStatus("Complete the quote before saving.", true);
        return;
      }
      const client = await resolveClient();
      const meta: QuoteInputMeta = {
        label: labelForNew(copying),
        clientId: client?.id,
        clientName: client?.name ?? "",
        project: quoteMeta.project,
        reference: quoteMeta.reference,
        status: "draft",
        statusLog: [],
      };
      const created = await createQuote({
        ...meta,
        inputs: toOrderInputs(cfg),
        options: quoteEngineOptions(cfg),
        formState: toFormState(),
      });
      const mapped = toSavedQuote(created);
      quotes = [mapped, ...quotes];
      bindTo(created.id, created.savedAt, mapped.label);
      pinStoredRates(created);
      setStatus(`"${mapped.label}" saved.`);
      captureQuoteBaseline();
    } catch (err) {
      setStatus(err instanceof Error ? err.message : "Couldn't save the quote.", true);
    } finally {
      saving = false;
    }
  }

  /** Points the form at a saved quote: the heading, sidebar highlight and URL follow. */
  function bindTo(id: string, savedAt: string, label: string): void {
    quoteSession.loadedId = id;
    quoteSession.version = savedAt;
    quoteSession.label = label;
    quoteSession.conflict = false;
    syncQuoteUrl();
  }

  /** Replaces the loaded quote in place: same id, fresh savedAt/totals (server-recomputed)/inputs, label
   * from the input when non-empty. resolveClient() is awaited first, before reading the loaded quote and the
   * form: it is the only await before the mutating work, so the rest runs synchronously (reading earlier let
   * Load mutate `quotes` or `form` mid-wait). The save names the version it loaded; if someone saved since,
   * the server refuses it (409) and the user chooses Reload or Overwrite. An explicit selection change,
   * including "No client", wins over the old link; an untouched link survives even if /api/clients never
   * loaded. */
  async function update() {
    const id = quoteSession.loadedId;
    if (!id || saving) return;
    // See save(): set before the settledBreakdown() await so the row stays disabled for the whole wait.
    saving = true;
    try {
      const bd = await settledOrder();
      if (bd === null) {
        setStatus("Couldn't reach the server to price this quote, so nothing was saved. Change any field to retry.", true);
        return;
      }
      if (!costValid(bd)) {
        setStatus("Complete the quote before saving.", true);
        return;
      }
      const client = await resolveClient();
      if (quoteSession.loadedId !== id) {
        setStatus("Couldn't update: this quote has been deleted.", true);
        return;
      }
      const idx = quotes.findIndex((x) => x.id === id);
      const old = idx === -1 ? undefined : quotes[idx];
      const meta: QuoteInputMeta = {
        label: quoteSession.label.trim() || old?.label || labelForNew(false),
        clientId: client?.id,
        clientName: client?.name ?? "",
        project: quoteMeta.project,
        reference: quoteMeta.reference,
        status: old?.status ?? "draft",
        statusLog: old?.statusLog ?? [],
        invoicedAs: old?.invoicedAs,
      };
      const updated = await updateQuote(
        id,
        {
          ...meta,
          inputs: toOrderInputs(cfg),
          options: quoteEngineOptions(cfg),
          formState: toFormState(),
        },
        quoteSession.version || undefined,
      );
      const mapped = toSavedQuote(updated);
      quotes = idx === -1 ? [mapped, ...quotes] : quotes.map((q) => (q.id === id ? mapped : q));
      bindTo(updated.id, updated.savedAt, mapped.label);
      pinStoredRates(updated);
      setStatus(`"${mapped.label}" updated.`);
      captureQuoteBaseline();
    } catch (err) {
      if (err instanceof QuoteConflictError) {
        quoteSession.conflict = true;
        setStatus("This was changed in another tab or by another user. Reload or overwrite it.", true);
      } else {
        setStatus(err instanceof Error ? err.message : "Couldn't update the saved quote.", true);
      }
    } finally {
      saving = false;
    }
  }

  /** Discards the form and shows the saved copy of the loaded quote. */
  async function reload() {
    const id = quoteSession.loadedId;
    if (!id || saving) return;
    await openById(id, { askFirst: false });
  }

  /** Saves the form over the quote as it is now, after the user chose Overwrite on a conflict. */
  async function overwrite() {
    const id = quoteSession.loadedId;
    if (!id || saving) return;
    const fresh = await getQuote(id);
    if (!fresh) {
      setStatus("Couldn't update: this quote has been deleted.", true);
      return;
    }
    // update() carries status, log, link and client over from the cached row; refresh that row first so
    // Overwrite replaces the content but keeps a link or status change made elsewhere.
    quotes = adoptServerQuote(quotes, fresh);
    quoteSession.version = fresh.savedAt;
    quoteSession.conflict = false;
    await update();
  }

  /** Enter in the label input runs the primary save-row action (Update when a quote is loaded, else Save
   * current) unless disabled. `breakdown` is the last resolved price, possibly the pre-edit placeholder
   * while pricingPending, so this stays optimistic during a recompute; save()/update() await
   * settledBreakdown() and re-check validity before writing, so an invalid price is never saved. */
  function saveRowDisabled(): boolean {
    return saving || loading || (!pricingPending && !costValid(order));
  }

  /** One-line reason the save row is disabled, or "" when it isn't or the reason is already visible
   * (`saving` flips the button label; `loading` is only the initial fetch). Ignores pricingPending like
   * saveRowDisabled(): a stale-invalid hint beats flashing it each keystroke. */
  const saveHint = $derived(costValid(order) ? "" : orderCostHint(order, quote.parts.map((p, i) => partLabel(p, i))));

  // Quote-dirty tracking, like the Settings editor's original/configsEqual (settingsDirty.svelte.ts): the
  // baseline snapshot lives in quoteSession (captured whenever the form becomes clean: first mount, after a
  // successful save/update, after opening a quote or resetting) and is compared per render.
  const dirty = $derived(quoteIsDirty());

  // The save row floats (with its "Unsaved changes" affordance) only when a save/update could act; a dirty
  // form the row can't submit (invalid breakdown, saving, loading) looks clean.
  const floating = $derived(dirty && !saveRowDisabled());

  // Leaving the editor with unsaved edits asks first (App.svelte's tab-switch guard reads this box).
  $effect(() => {
    unsavedForm.current = dirty;
    return () => {
      unsavedForm.current = false;
    };
  });

  function handleLabelKeydown(e: KeyboardEvent) {
    if (e.key !== "Enter" || saveRowDisabled()) return;
    e.preventDefault();
    primaryAction();
  }

  /** Runs `act` unless the row is disabled. A save attempt on an incomplete quote reveals the Quote tab's
   * quiet required-field errors; the row's buttons are natively disabled then, so Ctrl+S is the way to
   * "try". */
  function guarded(act: () => void): void {
    if (!saving && !loading && !pricingPending && !costValid(order)) noteSaveAttempt();
    if (saveRowDisabled()) return;
    act();
  }

  /** Routes to the primary action the button represents (Update or Save current); a no-op when disabled.
   * Used by handleLabelKeydown and registered on quoteActions so App's global Ctrl+S triggers it from
   * anywhere on the quote tab. */
  function primaryAction(): void {
    guarded(() => (quoteSession.loadedId ? void update() : void save()));
  }

  // Registered on mount, cleared on unmount; check identity before nulling so a quick remount's registration
  // isn't cleared.
  $effect(() => {
    const mine = {
      save: primaryAction,
      update: () => guarded(() => void update()),
      saveAsNew: () => guarded(() => void save()),
      reload: () => void reload(),
      overwrite: () => void overwrite(),
    };
    Object.assign(quoteActions, mine);
    return () => {
      if (quoteActions.save === mine.save) {
        quoteActions.save = quoteActions.update = quoteActions.saveAsNew = quoteActions.reload = quoteActions.overwrite = null;
      }
    };
  });
  $effect(() => {
    quoteActions.busy = saving;
    quoteActions.blocked = !saving && saveRowDisabled();
  });

  let openSeq = 0;

  /** Opens a saved quote in the editor from its server copy, so the form, the version and the stored rates
   * are what is stored now. Asks first when the open form has unsaved edits. A missing or unreadable quote
   * leaves the form as it was. */
  async function openById(id: string, opts: { askFirst?: boolean } = {}): Promise<void> {
    if (opts.askFirst !== false && quoteIsDirty() && !confirm("You have unsaved changes to this quote. Open another quote anyway?")) {
      syncQuoteUrl();
      return;
    }
    const seq = ++openSeq;
    const fresh = await getQuote(id);
    if (seq !== openSeq) return; // a newer open superseded this one
    if (!fresh) {
      setStatus("Couldn't open that quote. It may have been deleted.", true);
      syncQuoteUrl();
      return;
    }
    const mapped = toSavedQuote(fresh);
    if (!fresh.formState || typeof fresh.formState !== "object" || !restoreFormState(fresh.formState, cfg)) {
      setStatus(`Couldn't open "${mapped.label}": saved data is missing or corrupt.`, true);
      syncQuoteUrl();
      return;
    }
    const notes = reconcileWithConfig(cfg);

    prefill.byPart = {};
    quoteClient.id = mapped.clientId ?? "";
    quoteClient.name = mapped.clientName ?? "";
    quoteMeta.project = mapped.project ?? "";
    quoteMeta.reference = mapped.reference ?? "";
    bindTo(fresh.id, fresh.savedAt, mapped.label);
    // The stored rates, so the price here is the price in the list. Pinned after reconcile, which can touch
    // the printer but never the energy fields the pin watches.
    pinStoredRates(fresh);
    quotes = quotes.some((q) => q.id === mapped.id) ? quotes.map((q) => (q.id === mapped.id ? mapped : q)) : [mapped, ...quotes];

    setStatus(notes.length > 0 ? `Opened "${mapped.label}". ${notes.join(" ")}` : `Opened "${mapped.label}".`);
    captureQuoteBaseline();
  }

  // Typing or following a #/quote/<id> link while the editor is open switches quotes; a bare #/quote while a
  // saved quote is open is put back (starting a new quote goes through the New quote button, never the URL).
  function onHashChange(): void {
    const m = /^#\/quote\/([^/#]+)/.exec(location.hash);
    if (m) {
      const id = decodeURIComponent(m[1]!);
      if (id !== quoteSession.loadedId) void openById(id);
    } else if (quoteSession.loadedId && location.hash === "#/quote") {
      syncQuoteUrl();
    }
  }
</script>

<svelte:window onhashchange={onHashChange} />

<div class="card">
  <div class="head-row with-help">
    <h2>Saved quotes</h2>
    <CardHelp id="history" title="Saved quotes" text={TILE_HELP.history} />
  </div>

  {#if quotesUnavailable}
    <p class="banner-warn space-above" role="alert">Couldn't load saved quotes. The server couldn't be reached.</p>
  {/if}

  <div class="saverow" class:dirty={floating} use:stickyInset={{ edge: "bottom", active: floating }}>
    <div class="save-field">
      <label for="history-label">Label for this quote</label>
      <input
        id="history-label"
        placeholder={`for example "Benchy for Sam"`}
        bind:value={quoteSession.label}
        onkeydown={handleLabelKeydown}
      />
    </div>
    <div class="save-field">
      <label for="history-client">Client for this quote</label>
      <input
        id="history-client"
        list="history-client-options"
        placeholder="Type to search or add"
        autocomplete="off"
        value={quoteClient.name}
        oninput={handleClientInput}
        onfocus={ensureClientsLoaded}
      />
    </div>
    <datalist id="history-client-options">
      {#each clients ?? [] as c (c.id)}<option value={c.name}></option>{/each}
    </datalist>
    {#if clientsLoaded && clients === null}
      <span class="advisory-text">Couldn't load the client list. This quote keeps the client it already has, but you can't pick a new one right now.</span>
    {/if}
    {#if clientIsNew}
      <span class="advisory-text">New client. Saving adds them to your clients.</span>
    {/if}
    {#if floating}<span class="unsaved-badge">Unsaved changes</span>{/if}
    {#if quoteSession.loadedId}
      <button class="primary" onclick={update} disabled={!saving && saveRowDisabled()} aria-disabled={saving} aria-busy={saving}>
        {saving ? "Saving…" : `Update "${loadedQuote?.label ?? ""}"`}
      </button>
      <button type="button" onclick={save} disabled={!saving && saveRowDisabled()} aria-disabled={saving} aria-busy={saving}>Save as new</button>
    {:else}
      <button class="primary" onclick={save} disabled={!saving && saveRowDisabled()} aria-disabled={saving} aria-busy={saving}>
        {saving ? "Saving…" : "Save quote"}
      </button>
    {/if}
  </div>
  {#if saveHint}<p class="hint">{saveHint}</p>{/if}

  <!-- action-status carries no styles: it is the stable hook e2e/history.spec.ts selects, since several
       .status-line elements can share a page. -->
  <p class="status-line action-status" role={quoteSession.status.failed ? "alert" : "status"} aria-live={quoteSession.status.failed ? "assertive" : "polite"}>
    {quoteSession.status.text}
  </p>

  <ul class="plain-list space-above">
    {#if loading}
      <li class="empty-state"><span role="status">Loading…</span></li>
    {:else}
      {#each recentQuotes as q (q.id)}
        <li class:active-row={quoteSession.loadedId === q.id}>
          <button class="quiet linkish" tabindex="-1" onclick={() => openById(q.id)}>
            <span class="q-label">{q.label}</span>
            <span class="q-time">
              {formatSavedAt(q.savedAt)}
              {#if q.clientName}<span class="q-client">· {q.clientName}</span>{/if}
            </span>
          </button>
          <button type="button" class="quiet" onclick={() => openById(q.id)} aria-label={`Open "${q.label}"`}>
            Open
          </button>
        </li>
      {:else}
        <li class="empty-state">No saved quotes yet. Save your first quote with the button above.</li>
      {/each}
    {/if}
  </ul>
  {#if !loading && quotes.length > 0}
    <a class="all-quotes-link" href="#/quotes">All quotes</a>
  {/if}
</div>

<style>
  /* flex-wrap so the row (label input, client select, save buttons) reflows at very narrow widths instead
   * of forcing horizontal scroll; the input's intrinsic min-width can't shrink enough around 380px, and
   * min-width: 0 lets it shrink before a wrap is needed. */
  .saverow { display: flex; flex-wrap: wrap; gap: 0.5rem; margin-top: 0.6rem; }
  .saverow input { width: 100%; min-width: 0; }
  .save-field { flex: 1 1 12rem; display: flex; flex-direction: column; gap: 0.2rem; min-width: 0; }
  .save-field label { font-size: var(--text-sm); }
  .saverow > button { align-self: flex-end; }
  /* Below 480px the label and client inputs each take a full line so their placeholders stay readable, and
   * the save buttons share the next line. */
  @media (max-width: 480px) {
    .save-field { flex: 1 1 100%; }
    .saverow button { flex: 1 1 auto; }
  }

  /* Floats the row to the viewport bottom only while there is an unsaved edit a save/update would act on,
   * so it stays reachable while scrolling; same sticky + surface background + top border as
   * SettingsEditor's .save-bar. The clean state keeps the static row with no layout shift. */
  .saverow.dirty {
    position: sticky;
    bottom: 0;
    z-index: 1;
    background: var(--surface);
    border-top: 1px solid var(--border);
    padding: 0.5rem 0.4rem;
  }
  .unsaved-badge { font-size: var(--text-xs); color: var(--warn); }
  li {
    display: flex; align-items: center; flex-wrap: nowrap; gap: 0.5rem;
    padding: 0.3rem 0 0.3rem 0.4rem;
    border-top: 1px solid var(--border);
    /* Transparent by default so the active-row highlight changes only the border color, not its width
     * (otherwise loading a quote shifts every row 3px). The accent is re-declared locally because this
     * scoped rule outranks theme.css's .active-row, which still supplies the background. */
    border-left: 3px solid;
    border-left-color: transparent;
  }
  li.active-row { border-left-color: var(--accent); }
  .linkish {
    border: none; background: none; color: var(--accent-text); padding: 0; text-align: left;
    display: flex; flex-direction: column; align-items: flex-start; line-height: 1.25;
    font-size: var(--text-base);
  }
  /* Keeps the accent-link look on hover: theme's button.quiet:hover sets var(--text) at higher specificity
   * and would repaint the primary link as body text. */
  li > .linkish { flex: 1; min-width: 0; }
  li > button:not(.linkish) { flex: none; white-space: nowrap; }
  .linkish:hover { color: var(--accent-text); }
  .q-time { font-size: var(--text-xs); color: var(--muted); }
  .q-client { color: var(--muted); }
  .all-quotes-link {
    display: inline-block;
    margin-top: 0.5rem;
    font-size: var(--text-sm);
  }
</style>
