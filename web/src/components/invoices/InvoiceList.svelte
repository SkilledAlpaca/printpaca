<script lang="ts">
  import type { EffectiveConfig } from "../../lib/engine/index";
  import { type InvoiceSummary } from "../../lib/invoicesApi";
  import { isInvoiceOverdue, todayInTimezone } from "../../lib/invoiceStatus";
  import { money, formatDate } from "../../lib/format";
  import { canReadSettings } from "../../lib/permissions";
  import StatusBadge from "../StatusBadge.svelte";
  import SettingsEditor from "../settings/SettingsEditor.svelte";
  import InvoicingSettings from "../settings/InvoicingSettings.svelte";
  import LoadMoreRow from "../LoadMoreRow.svelte";

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
  // "invoices-filter" so App.svelte's "/" shortcut can focus it.
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
  <h1>Invoices</h1>
  <button type="button" class="primary" onclick={onNew}>New invoice</button>
</div>

{#if unavailable}
  <p class="banner-warn" role="alert">Couldn't load invoices. The server couldn't be reached.</p>
{/if}

{#if invoices.length > 0}
  <input
    id="invoices-filter"
    class="search-input"
    placeholder="Filter by invoice number or client name"
    aria-label="Filter invoices"
    bind:value={filterText}
  />
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

{#if backfilling}
  <p class="status-line">Loading the rest of the list to filter…</p>
{:else if backfillFailed}
  <p class="banner-warn" role="alert">
    Couldn't load the rest of the list; the filter may be missing some results.
  </p>
{/if}

<div class="card">
  <div class="table-scroll">
    <table>
      <thead>
        <tr>
          <th>Number</th>
          <th>Client</th>
          <th>Issued</th>
          <th>Due</th>
          <th>Status</th>
          <th class="num">Amount</th>
        </tr>
      </thead>
      <tbody>
        {#if loading}
          <tr><td colspan="6" class="empty-state">Loading invoices…</td></tr>
        {:else}
          {#each filteredInvoices as inv (inv.id)}
            <tr class="clickable-row" onclick={() => onOpenDetail(inv.id)}>
              <td class="nowrap">
                <button type="button" class="open-btn" onclick={(e) => { e.stopPropagation(); onOpenDetail(inv.id); }}>{inv.number}</button>
              </td>
              <td>{inv.clientName}</td>
              <td class="nowrap">{inv.issueDate ? formatDate(inv.issueDate) : "-"}</td>
              <td class="nowrap">{inv.dueDate ? formatDate(inv.dueDate) : "-"}</td>
              <td>
                <StatusBadge status={inv.status} dueDate={inv.dueDate} />
                {#if inv.status === "sent" && inv.paidCents > 0 && inv.paidCents < inv.totalCents}
                  <span class="pill">Partially paid</span>
                {/if}
              </td>
              <td class="num">{money(inv.totalCents, cfg)}</td>
            </tr>
          {:else}
            <tr><td colspan="6" class="empty-state">{invoices.length === 0 ? "No invoices yet. Convert a saved quote from the Quotes page, or press New invoice." : "No invoices match the filter."}</td></tr>
          {/each}
        {/if}
      </tbody>
    </table>
  </div>
  {#if !loading && !filterActive}
    <LoadMoreRow shown={invoices.length} {total} {loadingMore} {onLoadMore} />
  {/if}
</div>

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

  /* Same rule as ClientsTab's .search-input (scoped styles don't share): a full-width text filter above the
   * chips. */
  .search-input { width: 100%; margin-bottom: 0.75rem; }

  /* List table baseline comes from theme.css's shared table styles; this adds the click-to-open affordance
   * and centers the empty/loading row, which theme's .empty-state doesn't assume. */
  .clickable-row { cursor: pointer; }
  td.empty-state { text-align: center; }
  /* Number/Issued/Due scroll horizontally via .table-scroll instead of wrapping mid-token. */
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
   * chips (untouched; the rule is scoped) but uneven across All/Draft/Sent/Paid/Overdue/Canceled.
   * min-width in rem so it scales with root font size and doesn't clip "Canceled". */
  .chips button { min-width: 6.5rem; }
</style>
