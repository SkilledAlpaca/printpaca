<script lang="ts">
  /** Shared "Load more" control for a paginated list (server/pagination.go's opt-in ?limit=&offset=): a
   * "Showing X of Y" count plus a next-page button, used by ClientsTab, RequestsTab, QuotesTab and
   * InvoiceList. Renders nothing once everything is loaded. */
  let {
    shown,
    total,
    loadingMore,
    onLoadMore,
  }: {
    shown: number;
    total: number;
    loadingMore: boolean;
    onLoadMore: () => void;
  } = $props();
</script>

{#if shown < total}
  <div class="load-more-row">
    <span class="advisory-text">Showing {shown} of {total}</span>
    <button type="button" class="quiet" onclick={() => { if (!loadingMore) onLoadMore(); }} aria-disabled={loadingMore} aria-busy={loadingMore}>
      {loadingMore ? "Loading…" : "Load more"}
    </button>
  </div>
{/if}

<style>
  .load-more-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-2);
    padding-top: var(--space-2);
  }
</style>
