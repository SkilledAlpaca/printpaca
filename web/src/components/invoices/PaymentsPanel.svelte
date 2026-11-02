<script lang="ts">
  import type { EffectiveConfig } from "../../lib/engine/index";
  import { type Invoice } from "../../lib/invoicesApi";
  import type { PaymentForm } from "../../lib/invoiceForm";
  import { money, formatDate } from "../../lib/format";
  import { appConfig } from "../../lib/state.svelte";
  import { demoLocal } from "../../lib/demoLocal.svelte";
  import { extractRealMultiUser } from "../../lib/attribution";
  import { confirmFocus, keySelector } from "../a11yFocus";

  // Same visibility rule as AttributionLine.svelte (single-user mode and the demo's user-mode toggle hide
  // attribution), reimplemented inline: a payment has no separate updated_by (createdBy and updatedBy are
  // always equal), so "Created by X · Updated by Y" would repeat the name; this shows one compact "By"
  // column.
  const showAttribution = $derived(
    extractRealMultiUser(appConfig.current) && (!(appConfig.current?.demoMode === true) || demoLocal.userMode === "multi"),
  );

  // Rendered only for a loaded invoice with status "sent" or "paid" (InvoiceDetail's #if guards it), so
  // `detail` is a plain Invoice here.
  let {
    detail,
    cfg,
    paymentForm,
    paymentErrors,
    paymentSaving,
    confirmingPaymentDelete,
    onAddPayment,
    onAskDeletePayment,
    onCancelDeletePaymentConfirm,
    onConfirmDeletePayment,
  }: {
    detail: Invoice;
    cfg: EffectiveConfig;
    paymentForm: PaymentForm;
    paymentErrors: string[];
    paymentSaving: boolean;
    confirmingPaymentDelete: string | null;
    onAddPayment: () => void;
    onAskDeletePayment: (id: string) => void;
    onCancelDeletePaymentConfirm: () => void;
    onConfirmDeletePayment: (id: string) => void;
  } = $props();
</script>

<div class="subcard payments-card">
  <h3>Payments</h3>
  {#if paymentErrors.length > 0}
    <div role="alert">
      <ul class="errors">
        {#each paymentErrors as e (e)}<li>{e}</li>{/each}
      </ul>
    </div>
  {/if}
  {#if detail.payments.length > 0}
    <div class="table-scroll">
      <table>
        <thead>
          <tr>
            <th>Date</th><th class="num">Amount</th><th>Method</th><th>Note</th><th>Reference</th>
            {#if showAttribution}<th>By</th>{/if}
            <th><span class="visually-hidden">Actions</span></th>
          </tr>
        </thead>
        <tbody>
          {#each detail.payments as p (p.id)}
            <tr>
              <td>{formatDate(p.paidDate)}</td>
              <td class="num">{money(p.amountCents, cfg)}</td>
              <td>{p.method || "-"}</td>
              <td>{p.note || "-"}</td>
              <td>{p.reference || "-"}</td>
              {#if showAttribution}<td class="hint">{p.createdBy || "-"}</td>{/if}
              <td>
                {#if confirmingPaymentDelete === p.id}
                  <button type="button" class="danger quiet" aria-label={`Delete? payment of ${money(p.amountCents, cfg)}`} onclick={() => onConfirmDeletePayment(p.id)} use:confirmFocus={{ restore: keySelector(`delete-payment-${p.id}`), cancel: onCancelDeletePaymentConfirm }}>Delete?</button>
                  <button type="button" class="quiet" onclick={onCancelDeletePaymentConfirm}>Cancel</button>
                {:else}
                  <button
                    type="button"
                    class="quiet"
                    data-focus-key={`delete-payment-${p.id}`}
                    aria-label={`Delete payment of ${money(p.amountCents, cfg)} on ${formatDate(p.paidDate)}`}
                    onclick={() => onAskDeletePayment(p.id)}
                  >Delete</button>
                {/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {:else}
    <p class="empty-state">No payments recorded yet.</p>
  {/if}

  <p class="balance-line">
    Paid {money(detail.paidCents, cfg)} of {money(detail.totalCents, cfg)}, balance {money(detail.balanceCents, cfg)}
  </p>

  {#if detail.status === "sent"}
    <div class="field-rows">
      <label class="field">
        <span>Date</span>
        <input type="date" aria-label="Payment date" bind:value={paymentForm.paidDate} />
      </label>
      <label class="field">
        <span>Amount ({cfg.currency.symbol})</span>
        <input type="number" min="0.01" step="0.01" aria-label="Payment amount" bind:value={paymentForm.amount} />
      </label>
      <label class="field">
        <span>Method</span>
        <select aria-label="Payment method" bind:value={paymentForm.method}>
          {#each cfg.invoicing.paymentMethods as m (m)}<option value={m}>{m}</option>{/each}
        </select>
      </label>
      <label class="field">
        <span>Note</span>
        <input aria-label="Payment note" bind:value={paymentForm.note} />
      </label>
      <label class="field">
        <span>Reference (optional)</span>
        <input
          aria-label="Payment reference"
          placeholder="Square #4821"
          bind:value={paymentForm.reference}
        />
      </label>
    </div>
    <div class="form-actions">
      <button type="button" class="primary" onclick={onAddPayment} aria-disabled={paymentSaving} aria-busy={paymentSaving}>
        {paymentSaving ? "Recording…" : "Record payment"}
      </button>
    </div>
  {/if}
</div>

<style>
  /* .subcard (theme.css) supplies the separator; the extra space above the Payments block and the h3's top
   * margin stay local. */
  .payments-card { margin-top: var(--space-5); }
  .payments-card h3 { margin-top: 0; }
  .balance-line { font-weight: 600; font-variant-numeric: tabular-nums; margin: 0.6rem 0; }
</style>
