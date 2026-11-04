<script lang="ts">
  import type { EffectiveConfig } from "../../lib/engine/index";
  import { type InvoiceSummary } from "../../lib/invoicesApi";
  import { isInvoiceOverdue, todayInTimezone } from "../../lib/invoiceStatus";
  import { money, formatDate } from "../../lib/format";
  import { overpaidCents } from "../../lib/invoiceForm";
  import { canReadSettings } from "../../lib/permissions";
  import StatusBadge from "../StatusBadge.svelte";
  import SettingsEditor from "../settings/SettingsEditor.svelte";
  import InvoicingSettings from "../settings/InvoicingSettings.svelte";
  import DataTable, { type DataColumn } from "../ui/DataTable.svelte";
  import Field from "../ui/Field.svelte";

  let {
    invoices,
    loading,
    unavailable,
    cfg,
    total,
    loadingMore,
    backfilling,
    backfillFailed,
    onNew,
    onOpenDetail,
    onLoadMore,
    onFilterActive,
  }: {
    invoices: InvoiceSummary[];
    loading: boolean;
    unavailable: boolean;
    cfg: EffectiveConfig;
    /** Server pagination (server/pagination.go's opt-in ?limit=&offset=): `invoices` holds only the loaded
     * window; `total` is the pre-slice count, owned by InvoicesTab. */
    total: number;
    loadingMore: boolean;
    /** True while InvoicesTab backfills the rest of the list in response to onFilterActive. */
    backfilling: boolean;
    backfillFailed: boolean;
    onNew: () => void;
    onOpenDetail: (id: string) => void;
    onLoadMore: () => void;
    /** Called (possibly repeatedly) whenever a status chip or the filter text is active, so InvoicesTab can
     * backfill before this component filters; a filter must never run over a partial list. Passed
     * statusFilter+filterText joined, so InvoicesTab can tell "filter changed" (clear its failure latch,
     * allow a retry) from "still active", which filterActive alone can't. */
    onFilterActive: (filterKey: string) => void;
  } = $props();

  const COLUMNS: DataColumn[] = [
    { key: "number", label: "Number" },
    { key: "client", label: "Client" },
    { key: "issued", label: "Issued" },
    { key: "due", label: "Due" },
    { key: "status", label: "Status" },
    { key: "amount", label: "Amount", num: true },
  ];

  type StatusFilter = "all" | "draft" | "sent" | "paid" | "overdue" | "canceled";
  const STATUS_CHIPS: { key: StatusFilter; label: string }[] = [
    { key: "all", label: "All" },
    { key: "draft", label: "Draft" },
    { key: "sent", label: "Sent" },
    { key: "paid", label: "Paid" },
    { key: "overdue", label: "Overdue" },
    { key: "canceled", label: "Canceled" },
  ];
  let statusFilter = $state<StatusFilter>("all");
  // Free-text filter composing (AND) with statusFilter; same look as ClientsTab's search-input, registered as
  // "invoices-filter" so App.svelte's Ctrl+K (⌘K on macOS) shortcut can focus it.
  let filterText = $state("");

  const filterActive = $derived(statusFilter !== "all" || filterText.trim() !== "");
  $effect(() => {
    if (filterActive) onFilterActive(`${statusFilter}|${filterText}`);
  });

  const filteredInvoices = $derived.by(() => {
    let result = invoices;
    if (statusFilter === "overdue") result = result.filter((i) => isInvoiceOverdue(i, todayInTimezone(cfg.timezone)));
    else if (statusFilter !== "all") result = result.filter((i) => i.status === statusFilter);
    const q = filterText.trim().toLowerCase();
    if (q !== "") result = result.filter((i) => i.number.toLowerCase().includes(q) || i.clientName.toLowerCase().includes(q));
    return result;
  });

  // GET /api/settings (fetched by the embedded SettingsEditor on mount) is gated on admin or one of
  // filament/electricity/printers (server/main.go). An employee granted only "invoices" would 403 on mount
  // and see "Check that printpaca's backend is running" for what is a permissions boundary.
  const canEditInvoicingSettings = $derived(canReadSettings(cfg.user));
</script>

<div class="head-row">
  <h1 tabindex="-1">Invoices</h1>
  <button type="button" class="primary" onclick={onNew}>New invoice</button>
</div>

{#if unavailable}
  <p class="banner-warn" role="alert">Couldn't load invoices. The server couldn't be reached.</p>
{/if}

{#if backfilling}
  <p class="status-line" role="status">Loading the rest of the list to filter…</p>
{:else if backfillFailed}
  <p class="banner-warn" role="alert">
    Couldn't load the rest of the list; the filter may be missing some results.
  </p>
{/if}

<DataTable
  label="Invoices"
  columns={COLUMNS}
  loadMore={!loading && !filterActive ? { shown: invoices.length, total, loadingMore, onLoadMore } : null}
>
  {#snippet filters()}
    {#if invoices.length > 0}
      <Field id="invoices-filter" label="Filter invoices" size="l">
        {#snippet children(f)}
          <input id={f.id} type="search" placeholder="Invoice number or client name" bind:value={filterText} />
        {/snippet}
      </Field>
    {/if}
    <div class="chips" role="group" aria-label="Filter by status">
      {#each STATUS_CHIPS as chip (chip.key)}
        <button
          type="button"
          class:active={statusFilter === chip.key}
          aria-pressed={statusFilter === chip.key}
          onclick={() => (statusFilter = chip.key)}
        >{chip.label}</button>
      {/each}
    </div>
  {/snippet}
  {#if loading}
    <tr><td colspan="6" class="empty-state"><span role="status">Loading invoices…</span></td></tr>
  {:else}
    {#each filteredInvoices as inv (inv.id)}
      <tr class="clickable-row" onclick={() => onOpenDetail(inv.id)}>
        <td class="nowrap c-num">
          <button type="button" class="open-btn" data-row-id={inv.id} onclick={(e) => { e.stopPropagation(); onOpenDetail(inv.id); }}>{inv.number}</button>
        </td>
        <td class="c-client">{inv.clientName}</td>
        <td class="nowrap c-issued" data-label="Issued">{inv.issueDate ? formatDate(inv.issueDate) : "-"}</td>
        <td class="nowrap c-due" data-label="Due">{inv.dueDate ? formatDate(inv.dueDate) : "-"}</td>
        <td class="c-status">
          <StatusBadge status={inv.status} dueDate={inv.dueDate} />
          {#if inv.status === "sent" && inv.paidCents > 0 && inv.paidCents < inv.totalCents}
            <span class="pill">Partially paid</span>
          {/if}
          {#if overpaidCents(inv) > 0}
            <span class="pill">Overpaid {money(overpaidCents(inv), cfg)}</span>
          {/if}
        </td>
        <td class="num c-amount">{money(inv.totalCents, cfg)}</td>
      </tr>
    {:else}
      <tr><td colspan="6" class="empty-state">{invoices.length === 0 ? "No invoices yet. Convert a saved quote from the Quotes page, or press New invoice." : "No invoices match the filter."}</td></tr>
    {/each}
  {/if}
</DataTable>

{#if canEditInvoicingSettings}
  <SettingsEditor>
    {#snippet children({ form, hasError })}
      <InvoicingSettings {form} {hasError} />
    {/snippet}
  </SettingsEditor>
{/if}

<style>
  /* .page > h1 (theme.css) zeroes the margin only for a direct child of .page; here h1 shares .head-row
   * with the New invoice button, so the base margin is zeroed locally. */
  .head-row h1 { margin: 0; }

  /* The click-to-open affordance, and the centered empty/loading row that theme's .empty-state does not
   * assume. DataTable owns the scroll region, the sticky header and the phone card conversion. */
  .clickable-row { cursor: pointer; }
  td.empty-state { text-align: center; }
  .nowrap { white-space: nowrap; }

  /* Flat link treatment, not button.quiet: the invoice number is the row's primary link. min-height: 0
   * overrides the 44px tap target so the button doesn't inflate the row. */
  .open-btn {
    background: none;
    border: none;
    padding: 0;
    min-height: 0;
    font-weight: 600;
    color: var(--accent-text);
  }
  .open-btn:hover { text-decoration: underline; }

  /* Uniform-width status pills: theme.css's .chips sizes each to its text, right for QuotesTab's preset
   * chips but uneven across All/Draft/Sent/Paid/Overdue/Canceled. min-width in rem so it scales with root
   * font size and doesn't clip "Canceled". */
  .chips button { min-width: var(--field-xs); }

  /* "Partially paid" and "Overpaid $x" never break mid-phrase; they sit under the status badge, not beside it. */
  .c-status .pill { display: inline-block; white-space: nowrap; margin-top: var(--space-1); }

  /* Phones: each row becomes a stacked card with the number and amount on top, then client and status, then
   * the two dates. DataTable hides the header row and turns the table into a block list; the card layout of a
   * row is this screen's. 600px matches the rest of the app's narrow breakpoint. */
  @media (max-width: 600px) {
    tr.clickable-row {
      display: grid;
      grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
      grid-template-areas: "num amount" "client status" "issued due";
      gap: var(--space-1) var(--space-3);
      padding: var(--space-3) 0;
      border-bottom: 1px solid var(--border);
    }
    .c-num { grid-area: num; }
    .c-amount { grid-area: amount; text-align: right; font-weight: 600; }
    .c-client { grid-area: client; overflow-wrap: anywhere; }
    td.c-status { grid-area: status; display: flex; flex-direction: column; align-items: flex-end; }
    .c-issued { grid-area: issued; }
    .c-due { grid-area: due; text-align: right; }
    .c-issued, .c-due { color: var(--muted); font-size: var(--text-sm); }
    .c-issued::before, .c-due::before { content: attr(data-label) " "; }
  }
</style>
