<script lang="ts">
  import { toCents, type EffectiveConfig } from "../../lib/engine/index";
  import { type Invoice } from "../../lib/invoicesApi";
  import { overpaidCents, overpaymentQuestion, recordPaymentLabel, type PaymentForm } from "../../lib/invoiceForm";
  import { money, formatDate } from "../../lib/format";
  import { appConfig } from "../../lib/state.svelte";
  import { demoLocal } from "../../lib/demoLocal.svelte";
  import { extractRealMultiUser } from "../../lib/attribution";
  import { confirmFocus, keySelector } from "../../lib/a11yFocus";
  import Card from "../ui/Card.svelte";
  import Field from "../ui/Field.svelte";
  import FormGrid from "../ui/FormGrid.svelte";

  // Same visibility rule as AttributionLine.svelte (single-user mode and the demo's user-mode toggle hide
  // attribution), reimplemented inline: a payment has no separate updated_by (createdBy and updatedBy are
  // always equal), so "Created by X · Updated by Y" would repeat the name; this shows one compact "By"
  // column.
  const showAttribution = $derived(
    extractRealMultiUser(appConfig.current) && (!(appConfig.current?.demoMode === true) || demoLocal.userMode === "multi"),
  );

  // Rendered only for a loaded invoice that is a draft, sent or paid (InvoiceDetail's #if guards it), so
  // `detail` is a plain Invoice here. A draft shows the form too: recording a payment sends it.
  let {
    detail,
    cfg,
    paymentForm,
    paymentErrors,
    paymentSaving,
    confirmingPaymentDelete,
    today,
    dirty,
    confirmingOverpay,
    onConfirmOverpay,
    onCancelOverpay,
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
    /** Today in the shop's time zone: the Record payment button says "today" for it. */
    today: string;
    /** The invoice editor holds unsaved changes (a draft can't record a payment until they are saved). */
    dirty: boolean;
    confirmingOverpay: boolean;
    onConfirmOverpay: () => void;
    onCancelOverpay: () => void;
    onAddPayment: () => void;
    onAskDeletePayment: (id: string) => void;
    onCancelDeletePaymentConfirm: () => void;
    onConfirmDeletePayment: (id: string) => void;
  } = $props();

  const amountCents = $derived(toCents(paymentForm.amount ?? 0));
  const excessCents = $derived(Math.max(0, amountCents - detail.balanceCents));
  const overpaid = $derived(overpaidCents(detail));
  const recordLabel = $derived(recordPaymentLabel(money(amountCents, cfg), paymentForm.method, paymentForm.paidDate, today));
</script>

<div class="payments-card">
<Card tone="inset">
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
                  <button type="button" class="danger quiet" aria-label={`Yes, delete payment of ${money(p.amountCents, cfg)}`} onclick={() => onConfirmDeletePayment(p.id)} use:confirmFocus={{ restore: keySelector(`delete-payment-${p.id}`), cancel: onCancelDeletePaymentConfirm }}>Yes, delete</button>
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
    {#if overpaid > 0}<span class="pill overpaid">Overpaid {money(overpaid, cfg)}</span>{/if}
  </p>

  {#if detail.status === "draft" || detail.status === "sent"}
    <form
      onsubmit={(e) => {
        e.preventDefault();
        onAddPayment();
      }}
    >
    {#if detail.status === "draft"}
      <p class="hint">
        {#if dirty}
          Save your changes to the invoice before recording a payment.
        {:else}
          This invoice is still a draft. Recording a payment marks it sent and fills in today's issue date and the due date.
        {/if}
      </p>
    {/if}
    <FormGrid>
      <Field id="payment-date" label="Date" size="s">
        {#snippet children(f)}
          <input id={f.id} type="date" bind:value={paymentForm.paidDate} />
        {/snippet}
      </Field>
      <Field id="payment-amount" label={`Amount (${cfg.currency.symbol})`} size="xs">
        {#snippet children(f)}
          <input id={f.id} type="number" min="0.01" step="0.01" bind:value={paymentForm.amount} />
        {/snippet}
      </Field>
      <Field id="payment-method" label="Method" size="s">
        {#snippet children(f)}
          <select id={f.id} bind:value={paymentForm.method}>
            {#each cfg.invoicing.paymentMethods as m (m)}<option value={m}>{m}</option>{/each}
          </select>
        {/snippet}
      </Field>
      <Field id="payment-note" label="Note" size="m">
        {#snippet children(f)}
          <input id={f.id} bind:value={paymentForm.note} />
        {/snippet}
      </Field>
      <Field id="payment-reference" label="Reference (optional)" size="m">
        {#snippet children(f)}
          <input id={f.id} placeholder="Square #4821" bind:value={paymentForm.reference} />
        {/snippet}
      </Field>
    </FormGrid>
    <div class="form-actions">
      {#if confirmingOverpay}
        <span class="overpay-question" role="status">{overpaymentQuestion(money(excessCents, cfg))}</span>
        <button type="button" class="primary" onclick={onConfirmOverpay} use:confirmFocus={{ restore: keySelector("record-payment"), cancel: onCancelOverpay }}>Yes, record {money(amountCents, cfg)}</button>
        <button type="button" onclick={onCancelOverpay}>Cancel</button>
      {:else}
        <button type="submit" class="primary" data-focus-key="record-payment" aria-disabled={paymentSaving} aria-busy={paymentSaving}>
          {paymentSaving ? "Recording…" : recordLabel}
        </button>
      {/if}
    </div>
    </form>
  {/if}
</Card>
</div>

<style>
  /* An inset card inside the invoice card (D6: tint, no separator line); the space above it and the h3's top
   * margin stay local. */
  .payments-card { margin-top: var(--space-5); }
  .payments-card h3 { margin-top: 0; }
  .balance-line { font-weight: 600; font-variant-numeric: tabular-nums; margin: var(--space-3) 0; }
  .overpaid { margin-left: var(--space-2); font-weight: 600; }
  .overpay-question { font-size: var(--text-sm); }
</style>
