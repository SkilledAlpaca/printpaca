<script lang="ts">
  import { formatDate } from "../lib/format";
  import { onMount } from "svelte";
  import type { EffectiveConfig, OrderBreakdown } from "../lib/engine/index";
  import type { SavedQuote } from "../lib/api";
  import { sortQuotes } from "../lib/quotesFile";
  import { listQuotes, createQuote, updateQuote, toSavedQuote, type QuoteInputMeta } from "../lib/quotesApi";
  import { consumePendingOpen, quoteActions, quoteClient, quoteMeta, quote, toOrderInputs, toFormState, restoreFormState, reconcileWithConfig, engineOptions } from "../lib/state.svelte";
  import type { FormState } from "../lib/state.svelte";
  import { costValid, orderCostHint } from "../lib/orderView";
  import { partLabel } from "../lib/state.svelte";
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
  // the engine bag). Unlike QuotesTab this never needs the raw server shape: save()/update() always build a
  // fresh bag, options and formState from the live form.
  let quotes = $state<SavedQuote[]>([]);
  // Set once from a null listQuotes() (load failure); a muted note, not a blocking error, like ClientsTab.
  let quotesUnavailable = $state(false);
  let loading = $state(true);
  let saving = $state(false);
  let label = $state("");
  // The saved quote currently loaded into the form (via restore()/"Load"): drives the active-row highlight
  // and switches the primary action from "Save current" to "Update". Cleared when that row is deleted. A
  // delete on the Quotes ledger can evict it (see the dangling-loadedId effect); loading is still driven from
  // here.
  let loadedId = $state<string | null>(null);
  const loadedQuote = $derived(loadedId ? (quotes.find((x) => x.id === loadedId) ?? null) : null);
  // Dangling-loadedId guard: the ledger's delete can drop the loaded row from a separate mount with no call
  // path back here; catch it generically instead of threading a notification through every ledger delete.
  $effect(() => {
    if (loadedId !== null && !quotes.some((q) => q.id === loadedId)) {
      loadedId = null;
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
  let actionStatus = $state("");
  let actionFailed = $state(false);

  onMount(async () => {
    const list = await listQuotes();
    if (list === null) {
      quotesUnavailable = true;
    } else {
      quotes = list.map(toSavedQuote);
    }
    loading = false;
    // The ledger's "Open in editor" (and label link) sets this before navigating: the same one-shot handoff
    // as InvoicesTab's pending-open (pendingOpen in state.svelte.ts). Cleared on read; a missing id (deleted
    // since) is a silent no-op.
    const pendingId = consumePendingOpen("quote");
    if (pendingId) {
      const q = quotes.find((x) => x.id === pendingId);
      if (q) restore(q);
    }
    // Marks the form clean at mount. restore() captures its own baseline, so this is redundant then, but it
    // is the only capture point when there was no pending quote.
    captureBaseline();
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
    // A failed create propagates to save()/update()'s catch (actionFailed/actionStatus) and aborts the save:
    // the quote is never written without the client it named.
    const created = await createClient({ name, email: "", phone: "", billingAddress: "", shippingAddress: "", notes: "" });
    clients = [...clients, created];
    quoteClient.id = created.id;
    return { id: created.id, name: created.name };
  }

  async function save() {
    if (saving) return;
    // Set before the settledBreakdown() await so the save row stays disabled for the whole wait, and a second
    // Ctrl+S/click can't fire a second save.
    saving = true;
    // "Save as new" while a quote is loaded is a fork: the user then works on the new entry.
    const forking = loadedId !== null;
    try {
      const bd = await settledOrder();
      if (bd === null) {
        actionFailed = true;
        actionStatus = "Couldn't reach the server to price this quote, so nothing was saved. Change any field to retry.";
        return;
      }
      if (!costValid(bd)) {
        actionFailed = true;
        actionStatus = "Complete the quote before saving.";
        return;
      }
      const client = await resolveClient();
      const meta: QuoteInputMeta = {
        label: label || `Quote ${formatDate(new Date().toISOString())}`,
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
        options: engineOptions(cfg),
        formState: toFormState(),
      });
      const mapped = toSavedQuote(created);
      quotes = [mapped, ...quotes];
      actionFailed = false;
      actionStatus = `"${mapped.label}" saved.`;
      if (forking) loadedId = mapped.id;
      label = "";
      captureBaseline();
    } catch (err) {
      actionFailed = true;
      actionStatus = err instanceof Error ? err.message : "Couldn't save the quote.";
    } finally {
      saving = false;
    }
  }

  /** Replaces the loaded quote in place: same id, fresh savedAt/totals (server-recomputed)/inputs, label
   * from the input when non-empty. resolveClient() is awaited first, before reading loadedId/quotes/form:
   * it is the only await before the mutating work, so the rest runs synchronously (reading earlier let
   * Load mutate `quotes` or `form` mid-wait). loadedId is re-checked after the await (the ledger can
   * delete the row). An explicit selection change, including "No client", wins over the old link; an
   * untouched link survives even if /api/clients never loaded. */
  async function update() {
    if (!loadedId || saving) return;
    // See save(): set before the settledBreakdown() await so the row stays disabled for the whole wait.
    saving = true;
    try {
      const bd = await settledOrder();
      if (bd === null) {
        actionFailed = true;
        actionStatus = "Couldn't reach the server to price this quote, so nothing was saved. Change any field to retry.";
        return;
      }
      if (!costValid(bd)) {
        actionFailed = true;
        actionStatus = "Complete the quote before saving.";
        return;
      }
      const client = await resolveClient();
      const idx = loadedId ? quotes.findIndex((x) => x.id === loadedId) : -1;
      const old = idx === -1 ? undefined : quotes[idx];
      if (!loadedId || !old) {
        actionFailed = true;
        actionStatus = "Couldn't update: this quote has been deleted.";
        return;
      }
      const meta: QuoteInputMeta = {
        label: label || old.label,
        clientId: client?.id,
        clientName: client?.name ?? "",
        project: quoteMeta.project,
        reference: quoteMeta.reference,
        status: old.status,
        statusLog: old.statusLog,
        invoicedAs: old.invoicedAs,
      };
      const updated = await updateQuote(loadedId, {
        ...meta,
        inputs: toOrderInputs(cfg),
        options: engineOptions(cfg),
        formState: toFormState(),
      });
      const mapped = toSavedQuote(updated);
      const next = [...quotes];
      next[idx] = mapped;
      quotes = next;
      actionFailed = false;
      actionStatus = `"${mapped.label}" updated.`;
      label = "";
      captureBaseline();
    } catch (err) {
      actionFailed = true;
      actionStatus = err instanceof Error ? err.message : "Couldn't update the saved quote.";
    } finally {
      saving = false;
    }
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

  // Quote-dirty tracking, like the Settings editor's original/configsEqual (settingsDirty.svelte.ts): a
  // baseline snapshot captured whenever the form becomes clean (after mount's pendingOpen restore, at the end
  // of restore(), after a successful save()/update()), compared per render via JSON.stringify of plain form
  // data.
  let baseline = $state<string | null>(null);

  function snapshotDirtyState(): {
    form: FormState;
    clientId: string;
    clientName: string;
    project: string;
    reference: string;
    label: string;
  } {
    return {
      form: toFormState(),
      clientId: quoteClient.id,
      clientName: quoteClient.name,
      project: quoteMeta.project,
      reference: quoteMeta.reference,
      label,
    };
  }

  function captureBaseline(): void {
    baseline = JSON.stringify(snapshotDirtyState());
  }

  // Guards the first render before any captureBaseline() call: no baseline reads as clean.
  const dirty = $derived(baseline !== null && JSON.stringify(snapshotDirtyState()) !== baseline);

  // The save row floats (with its "Unsaved changes" affordance) only when a save/update could act; a dirty
  // form the row can't submit (invalid breakdown, saving, loading) looks clean.
  const floating = $derived(dirty && !saveRowDisabled());

  function handleLabelKeydown(e: KeyboardEvent) {
    if (e.key !== "Enter" || saveRowDisabled()) return;
    e.preventDefault();
    primaryAction();
  }

  /** Routes to the primary action the button represents (Update or Save current); a no-op when disabled.
   * Used by handleLabelKeydown and registered on quoteActions so App's global Ctrl+S triggers it from
   * anywhere on the quote tab. */
  function primaryAction(): void {
    if (saveRowDisabled()) return;
    if (loadedId) void update();
    else void save();
  }

  // Registered on mount, cleared on unmount; check identity before nulling so a quick remount's registration
  // isn't cleared.
  $effect(() => {
    quoteActions.save = primaryAction;
    return () => {
      if (quoteActions.save === primaryAction) quoteActions.save = null;
    };
  });

  function restore(q: SavedQuote) {
    if (dirty && !confirm("You have unsaved changes to this quote. Open another quote anyway?")) return;
    const record = q.inputs;
    if (!record || typeof record !== "object") {
      actionFailed = true;
      actionStatus = `Couldn't open "${q.label}": saved data is missing or corrupt.`;
      return;
    }
    if (!restoreFormState(record, cfg)) {
      actionFailed = true;
      actionStatus = `Couldn't open "${q.label}": saved data is missing or corrupt.`;
      return;
    }
    const notes = reconcileWithConfig(cfg);

    loadedId = q.id;
    quoteClient.id = q.clientId ?? "";
    quoteClient.name = q.clientName ?? "";
    quoteMeta.project = q.project ?? "";
    quoteMeta.reference = q.reference ?? "";
    label = q.label;

    actionFailed = false;
    actionStatus = notes.length > 0 ? `Opened "${q.label}". ${notes.join(" ")}` : `Opened "${q.label}".`;
    captureBaseline();
  }
</script>

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
        bind:value={label}
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
    {#if loadedId}
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
  <p class="status-line action-status" role={actionFailed ? "alert" : "status"} aria-live={actionFailed ? "assertive" : "polite"}>
    {actionStatus}
  </p>

  <ul class="plain-list space-above">
    {#if loading}
      <li class="empty-state"><span role="status">Loading…</span></li>
    {:else}
      {#each recentQuotes as q (q.id)}
        <li class:active-row={loadedId === q.id}>
          <button class="quiet linkish" tabindex="-1" onclick={() => restore(q)}>
            <span class="q-label">{q.label}</span>
            <span class="q-time">
              {formatSavedAt(q.savedAt)}
              {#if q.clientName}<span class="q-client">· {q.clientName}</span>{/if}
            </span>
          </button>
          <button type="button" class="quiet" onclick={() => restore(q)} aria-label={`Open "${q.label}"`}>
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
