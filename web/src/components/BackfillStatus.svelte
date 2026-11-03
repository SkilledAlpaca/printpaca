<script lang="ts">
  /** The "backfill in progress / failed" status pair shared by QuotesTab, ClientsTab and InvoiceList, each
   * from its own createBackfillController. `verb` is the one differing word: "to filter" (quotes,
   * invoices) or "to search" (clients). */
  let {
    backfilling,
    backfillFailed,
    verb = "filter",
  }: {
    backfilling: boolean;
    backfillFailed: boolean;
    verb?: "filter" | "search";
  } = $props();
</script>

{#if backfilling}
  <p class="status-line" role="status">Loading the rest of the list to {verb}…</p>
{:else if backfillFailed}
  <p class="banner-warn" role="alert">
    {verb === "search"
      ? "Couldn't load the rest of the list; search results may be incomplete."
      : "Couldn't load the rest of the list; the filter may be missing some results."}
  </p>
{/if}
