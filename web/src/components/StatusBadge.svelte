<script lang="ts">
  import { invoiceStatusVariant, todayInTimezone } from "../lib/invoiceStatus";
  import { appConfig } from "../lib/state.svelte";

  // Shared by InvoicesTab and DashboardTab's recent-invoices list, including the derived "overdue" variant.
  let { status, dueDate = "" }: { status: string; dueDate?: string } = $props();

  const variant = $derived(invoiceStatusVariant({ status, dueDate }, todayInTimezone(appConfig.current?.timezone)));
</script>

<span class={`badge badge-${variant}`}>{variant}</span>

<style>
  /* One muted background and readable text pair per status, derived from its status token via color-mix
   * (reads --surface's computed value, so light and dark both work). Each mix percentage is tuned so the
   * pair clears 4.5:1 in both schemes (the tighter max wins); contrast.test.ts enforces it, so a regression
   * fails CI. */
  .badge {
    display: inline-block;
    font-size: var(--text-xs);
    text-transform: uppercase;
    letter-spacing: 0.06em;
    font-weight: 700;
    padding: 0.2rem 0.6rem;
    border-radius: 999px;
    white-space: nowrap;
    /* One width for every status, wide enough for the longest word ("CANCELED"/"FINALIZED"): statuses lead
     * rows on three surfaces, and varying widths make what follows start at different x. */
    min-width: 6rem;
    text-align: center;
    /* Each variant sets its own border-color; this transparent border only reserves the box space so
     * variants don't shift layout. */
    border: 1px solid transparent;
  }
  /* Each variant also gets a 1px border in its own token: the lighter fills are subtle, so the border keeps
   * a distinction that doesn't rely on color alone. Non-text UI needs only 3:1 (see contrast.test.ts). */
  .badge-draft, .badge-canceled {
    color: var(--muted);
    background: color-mix(in srgb, var(--muted) 10%, var(--surface));
    border-color: var(--muted);
  }
  /* --accent clears only ~3.5:1 against --surface (fine for UI, not text); --accent-text is the darkened
   * text-safe variant, as in HistoryPanel's .linkish and App.svelte's Guide link. */
  .badge-sent {
    color: var(--accent-text);
    background: color-mix(in srgb, var(--accent-text) 18%, var(--surface));
    border-color: var(--accent-text);
  }
  .badge-paid {
    color: var(--ok);
    background: color-mix(in srgb, var(--ok) 18%, var(--surface));
    border-color: var(--ok);
  }
  .badge-overdue {
    color: var(--danger);
    background: color-mix(in srgb, var(--danger) 8%, var(--surface));
    border-color: var(--danger);
  }

  /* RequestsTab variants: request status (new/viewed/finalized) and verification outcomes
   * (verified/mismatch), copied color-for-color from RequestsTab.svelte. */
  .badge-new {
    color: var(--accent-text);
    background: color-mix(in srgb, var(--accent-text) 18%, var(--surface));
    border-color: var(--accent-text);
  }
  .badge-viewed {
    color: var(--muted);
    background: color-mix(in srgb, var(--muted) 10%, var(--surface));
    border-color: var(--muted);
  }
  .badge-finalized, .badge-verified, .badge-invoiced {
    color: var(--ok);
    background: color-mix(in srgb, var(--ok) 18%, var(--surface));
    border-color: var(--ok);
  }
  .badge-mismatch {
    color: var(--warn);
    background: color-mix(in srgb, var(--warn) 10%, var(--surface));
    border-color: var(--warn);
  }

  /* Quote lifecycle variants (draft/sent are reused). Each reuses a tuned token+percent pair: accepted
   * mirrors paid/finalized/verified, declined mirrors overdue, expired mirrors draft/canceled. */
  .badge-accepted {
    color: var(--ok);
    background: color-mix(in srgb, var(--ok) 18%, var(--surface));
    border-color: var(--ok);
  }
  .badge-declined {
    color: var(--danger);
    background: color-mix(in srgb, var(--danger) 8%, var(--surface));
    border-color: var(--danger);
  }
  .badge-expired {
    color: var(--muted);
    background: color-mix(in srgb, var(--muted) 10%, var(--surface));
    border-color: var(--muted);
  }
</style>
