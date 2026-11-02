<script lang="ts">
  import type { EffectiveConfig } from "../../lib/engine/index";
  import { money, formatDate } from "../../lib/format";
  import { bpToPct, type Invoice, type InvoiceStatus } from "../../lib/invoicesApi";
  import type { Client } from "../../lib/clientsApi";
  import type { Item } from "../../lib/itemsApi";
  import {
    matchingClientByName,
    previewTotalsFor,
    hasQuoteProvenance,
    linkLineToItem,
    unlinkLine,
    emailDisabledReason,
    formatSentAt,
    type InvoiceForm,
    type LineForm,
    type PaymentForm,
  } from "../../lib/invoiceForm";
  import { mdiArrowLeft } from "@mdi/js";
  import Icon from "../Icon.svelte";
  import StatusBadge from "../StatusBadge.svelte";
  import PaymentsPanel from "./PaymentsPanel.svelte";
  import AttributionLine from "../AttributionLine.svelte";
  import DetailTitle from "../DetailTitle.svelte";
  import ScrollRegion from "../ScrollRegion.svelte";
  import { confirmFocus, keySelector } from "../a11yFocus";

  let {
    cfg,
    clients,
    items,
    detail,
    detailLoading,
    detailError,
    form,
    formErrors,
    confirmingLineRemove,
    saving,
    statusActionMessage,
    statusActionFailed,
    confirmingMarkSent,
    confirmingCancel,
    confirmingDelete,
    emailing,
    paymentForm,
    paymentErrors,
    paymentSaving,
    confirmingPaymentDelete,
    onBackToList,
    onLinkMatchingClient,
    onAddLine,
    onAskRemoveLine,
    onCancelRemoveLine,
    onRemoveLine,
    onSave,
    onDuplicate,
    onMarkSentClick,
    onCancelMarkSent,
    onChangeStatus,
    onAskCancel,
    onCancelCancel,
    onAskDelete,
    onCancelDeleteConfirm,
    onConfirmDeleteInvoice,
    onSendEmail,
    onPrint,
    onAddPayment,
    onAskDeletePayment,
    onCancelDeletePaymentConfirm,
    onConfirmDeletePayment,
  }: {
    cfg: EffectiveConfig;
    clients: Client[];
    items: Item[];
    detail: Invoice | null;
    detailLoading: boolean;
    detailError: string | null;
    form: InvoiceForm;
    formErrors: string[];
    confirmingLineRemove: number | null;
    saving: boolean;
    statusActionMessage: string;
    statusActionFailed: boolean;
    confirmingMarkSent: boolean;
    confirmingCancel: boolean;
    confirmingDelete: boolean;
    emailing: boolean;
    paymentForm: PaymentForm;
    paymentErrors: string[];
    paymentSaving: boolean;
    confirmingPaymentDelete: string | null;
    onBackToList: () => void;
    onLinkMatchingClient: (id: string) => void;
    onAddLine: () => void;
    onAskRemoveLine: (i: number) => void;
    onCancelRemoveLine: () => void;
    onRemoveLine: (i: number) => void;
    onSave: () => void;
    onDuplicate: (inv: Invoice) => void;
    onMarkSentClick: () => void;
    onCancelMarkSent: () => void;
    onChangeStatus: (status: InvoiceStatus) => void;
    onAskCancel: () => void;
    onCancelCancel: () => void;
    onAskDelete: () => void;
    onCancelDeleteConfirm: () => void;
    onConfirmDeleteInvoice: () => void;
    onSendEmail: () => void;
    onPrint: () => void;
    onAddPayment: () => void;
    onAskDeletePayment: (id: string) => void;
    onCancelDeletePaymentConfirm: () => void;
    onConfirmDeletePayment: (id: string) => void;
  } = $props();

  const matchingClient = $derived(matchingClientByName(clients, form));
  const previewTotals = $derived(previewTotalsFor(form));
  const provenance = $derived(hasQuoteProvenance(form));

  // Picking an item (or switching its option) rewrites the line's description, price and link; "" unlinks.
  function pickItem(i: number, line: LineForm, itemId: string) {
    const item = items.find((x) => x.id === itemId);
    form.lines[i] = item ? linkLineToItem(line, item, item.options[0]?.name ?? "") : unlinkLine(line);
  }
  function pickOption(i: number, line: LineForm, item: Item, optionName: string) {
    form.lines[i] = linkLineToItem(line, item, optionName);
  }
</script>

<div class="head-row">
  <button type="button" class="back-link" onclick={onBackToList}><Icon path={mdiArrowLeft} /> Back to list</button>
  {#if detail}<StatusBadge status={detail.status} dueDate={detail.dueDate} />{/if}
</div>

{#if detailLoading}
  <p>Loading…</p>
{:else if detailError}
  <p class="banner-warn" role="alert">{detailError}</p>
{:else}
  <div class="card">
    <DetailTitle>{detail ? `Invoice ${detail.number}` : "New invoice"}</DetailTitle>
    {#if detail}<AttributionLine createdBy={detail.createdBy} updatedBy={detail.updatedBy} />{/if}
    {#if provenance}<p class="hint">Created from a saved quote.</p>{/if}

    {#if formErrors.length > 0}
      <div role="alert">
        <ul class="errors">
          {#each formErrors as e (e)}<li>{e}</li>{/each}
        </ul>
      </div>
    {/if}

    {#if detail === null || detail.status === "draft"}
      <div class="field-rows">
        <label class="field">
          <span>Client</span>
          <select bind:value={form.clientId}>
            <option value="">Walk-in (no client)</option>
            {#each clients as c (c.id)}<option value={c.id}>{c.name}</option>{/each}
          </select>
        </label>
        {#if form.clientId === ""}
          <label class="field">
            <span>Client name</span>
            <input bind:value={form.clientName} placeholder="Walk-in client name" />
          </label>
          {#if matchingClient}
            <p class="hint">
              A client named {matchingClient.name} exists.
              <button type="button" class="quiet" onclick={() => onLinkMatchingClient(matchingClient!.id)}>Link client</button>
            </p>
          {/if}
        {/if}
        <label class="field">
          <span>Issue date</span>
          <input type="date" bind:value={form.issueDate} />
        </label>
        <label class="field">
          <span>Due date</span>
          <input type="date" bind:value={form.dueDate} />
        </label>
        <label class="field">
          <span>Sales tax (%)</span>
          <input type="number" min="0" max="30" step="0.01" bind:value={form.taxPct} />
        </label>
        <label class="field">
          <span>Invoice discount ({cfg.currency.symbol})</span>
          <input type="number" min="0" step="0.01" bind:value={form.discount} />
        </label>
      </div>
      <label class="field">
        <span>Notes</span>
        <textarea rows="3" bind:value={form.notes}></textarea>
      </label>

      <h3>Lines</h3>
      <p class="hint">Link a line to a gallery item so a counter sale counts in Top items once the invoice is sent or paid, and sending takes in-stock items off the shelf.</p>
      <div class="table-scroll">
        <table>
          <thead>
            <tr><th>Description</th><th>Qty</th><th>Unit price</th><th>Discount</th><th class="num">Amount</th><th><span class="visually-hidden">Actions</span></th></tr>
          </thead>
          <tbody>
            {#each form.lines as line, i (i)}
              {@const linked = items.find((x) => x.id === line.itemId)}
              <tr>
                <td>
                  <input aria-label={`Line ${i + 1} description`} bind:value={line.description} />
                  <div class="item-link">
                    <select
                      aria-label={`Line ${i + 1} gallery item`}
                      value={line.itemId}
                      onchange={(e) => pickItem(i, line, e.currentTarget.value)}
                    >
                      <option value="">No gallery item</option>
                      {#if line.itemId && !linked}<option value={line.itemId}>Gallery item (removed)</option>{/if}
                      {#each items as it (it.id)}<option value={it.id}>{it.name}</option>{/each}
                    </select>
                    {#if linked && linked.options.length > 0}
                      <select
                        aria-label={`Line ${i + 1} ${linked.optionLabel || "option"}`}
                        value={line.optionName}
                        onchange={(e) => pickOption(i, line, linked, e.currentTarget.value)}
                      >
                        {#each linked.options as o (o.name)}<option value={o.name}>{o.name}</option>{/each}
                      </select>
                    {/if}
                    {#if line.itemId}
                      <button type="button" class="quiet" aria-label={`Unlink line ${i + 1} from its gallery item`} onclick={() => pickItem(i, line, "")}>Unlink</button>
                    {/if}
                    <label class="toggle">
                      <input
                        type="checkbox"
                        aria-label={`Shipping (not taxed), line ${i + 1}`}
                        checked={line.kind === "shipping"}
                        onchange={(e) => (line.kind = e.currentTarget.checked ? "shipping" : "item")}
                      />
                      <span>Shipping (not taxed)</span>
                    </label>
                  </div>
                </td>
                <td><input type="number" min={line.itemId ? 1 : 0.0001} step={line.itemId ? 1 : "any"} aria-label={`Line ${i + 1} quantity`} bind:value={line.quantity} /></td>
                <td><input type="number" min="0" step="0.01" aria-label={`Line ${i + 1} unit price`} bind:value={line.unitPrice} /></td>
                <td><input type="number" min="0" step="0.01" aria-label={`Line ${i + 1} discount`} bind:value={line.discount} /></td>
                <td class="num">{money(previewTotals.lineAmounts[i] ?? 0, cfg)}</td>
                <td>
                  {#if confirmingLineRemove === i}
                    <button type="button" class="danger quiet" aria-label={`Remove?, line ${i + 1}`} onclick={() => onRemoveLine(i)} use:confirmFocus={{ restore: keySelector(`remove-line-${i}`), cancel: onCancelRemoveLine }}>Remove?</button>
                    <button type="button" class="quiet" aria-label={`Cancel, keep line ${i + 1}`} onclick={onCancelRemoveLine}>Cancel</button>
                  {:else}
                    <button type="button" class="quiet" aria-label={`Remove line ${i + 1}`} data-focus-key={`remove-line-${i}`} onclick={() => onAskRemoveLine(i)}>Remove</button>
                  {/if}
                </td>
              </tr>
            {:else}
              <tr><td colspan="6" class="empty-state">No lines yet. Add one with the Add line button below.</td></tr>
            {/each}
          </tbody>
        </table>
      </div>
      <button type="button" data-focus-key="add-line" onclick={onAddLine}>Add line</button>

      <div class="totals">
        <div><span>Subtotal</span><span>{money(previewTotals.subtotalCents, cfg)}</span></div>
        <div><span>Tax</span><span>{money(previewTotals.taxCents, cfg)}</span></div>
        <div class="total-line"><span>Total</span><span>{money(previewTotals.totalCents, cfg)}</span></div>
      </div>

      <div class="form-actions">
        <button type="button" class="primary" onclick={onSave} aria-disabled={saving}>{saving ? "Saving…" : "Save"}</button>
      </div>
    {:else if detail}
      <div class="readonly-grid">
        <div><span class="lbl">Client</span><span>{detail.clientName}</span></div>
        <div><span class="lbl">Issue date</span><span>{detail.issueDate ? formatDate(detail.issueDate) : "-"}</span></div>
        <div><span class="lbl">Due date</span><span>{detail.dueDate ? formatDate(detail.dueDate) : "-"}</span></div>
        <div><span class="lbl">Sales tax</span><span>{bpToPct(detail.taxBp).toFixed(2)}%</span></div>
      </div>
      {#if detail.clientBillingAddress}<p class="address"><span class="lbl">Billing</span> {detail.clientBillingAddress}</p>{/if}
      {#if detail.clientShippingAddress}<p class="address"><span class="lbl">Shipping</span> {detail.clientShippingAddress}</p>{/if}
      {#if detail.notes}<p class="notes"><span class="lbl">Notes</span> {detail.notes}</p>{/if}

      <ScrollRegion label="Invoice lines">
        <table>
          <thead>
            <tr><th>Description</th><th class="num">Qty</th><th class="num">Unit price</th><th class="num">Discount</th><th class="num">Amount</th></tr>
          </thead>
          <tbody>
            {#each detail.lines as line (line.id)}
              <tr>
                <td>{line.description}{#if line.quoteId}<span class="from-quote">From quote</span>{/if}{#if line.itemId}<span class="from-quote">Gallery item</span>{/if}</td>
                <td class="num">{line.quantity}</td>
                <td class="num">{money(line.unitPriceCents, cfg)}</td>
                <td class="num">{line.discountCents > 0 ? `-${money(line.discountCents, cfg)}` : "-"}</td>
                <td class="num">{money(line.amountCents, cfg)}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </ScrollRegion>
      <div class="totals">
        <div><span>Subtotal</span><span>{money(detail.subtotalCents, cfg)}</span></div>
        <div><span>Tax</span><span>{money(detail.taxCents, cfg)}</span></div>
        <div class="total-line"><span>Total</span><span>{money(detail.totalCents, cfg)}</span></div>
      </div>

      {#if detail.sends.length > 0}
        <div class="subcard delivery-card">
          <h3>Delivery</h3>
          <ScrollRegion label="Delivery history">
            <table>
              <thead>
                <tr><th>Date</th><th>Channel</th><th>Recipient</th></tr>
              </thead>
              <tbody>
                {#each detail.sends as s (s.id)}
                  <tr>
                    <td class="nowrap">{formatSentAt(s.sentAt)}</td>
                    <td>{s.channel === "email" ? "Email" : "Manual"}</td>
                    <td>{s.recipient || "-"}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </ScrollRegion>
        </div>
      {/if}

      {#if detail.status === "sent" || detail.status === "paid"}
        <PaymentsPanel
          {detail}
          {cfg}
          {paymentForm}
          {paymentErrors}
          {paymentSaving}
          {confirmingPaymentDelete}
          onAddPayment={onAddPayment}
          onAskDeletePayment={onAskDeletePayment}
          onCancelDeletePaymentConfirm={onCancelDeletePaymentConfirm}
          onConfirmDeletePayment={onConfirmDeletePayment}
        />
      {/if}
    {/if}

    {#if detail}
      {@const emailReason = emailDisabledReason(cfg.smtp.host, detail)}
      <p class="status-line" role={statusActionFailed ? "alert" : "status"} aria-live={statusActionFailed ? "assertive" : "polite"}>
        {statusActionMessage}
      </p>
      <div class="status-actions">
        {#if detail.status === "draft"}
          {#if confirmingMarkSent}
            <button type="button" class="danger" onclick={() => onChangeStatus("sent")} use:confirmFocus={{ restore: keySelector("mark-sent"), cancel: onCancelMarkSent }}>Mark an empty invoice as sent?</button>
            <button type="button" onclick={onCancelMarkSent}>Cancel</button>
          {:else}
            <button type="button" data-focus-key="mark-sent" onclick={onMarkSentClick}>Mark sent</button>
          {/if}
          {#if confirmingDelete}
            <button type="button" class="danger" onclick={onConfirmDeleteInvoice} use:confirmFocus={{ restore: keySelector("delete-invoice"), cancel: onCancelDeleteConfirm }}>Delete?</button>
            <button type="button" onclick={onCancelDeleteConfirm}>Cancel</button>
          {:else}
            <button type="button" data-focus-key="delete-invoice" onclick={onAskDelete}>Delete</button>
          {/if}
        {:else if detail.status === "sent"}
          <button type="button" onclick={() => onChangeStatus("paid")}>Mark paid</button>
          {#if confirmingCancel}
            <button type="button" class="danger" onclick={() => onChangeStatus("canceled")} use:confirmFocus={{ restore: keySelector("cancel-invoice"), cancel: onCancelCancel }}>Cancel invoice?</button>
            <button type="button" onclick={onCancelCancel}>Keep invoice</button>
          {:else}
            <button type="button" data-focus-key="cancel-invoice" onclick={onAskCancel}>Cancel invoice</button>
          {/if}
          <button type="button" onclick={() => onDuplicate(detail)}>Duplicate</button>
        {:else if detail.status === "paid"}
          <button type="button" onclick={() => onChangeStatus("sent")}>Mark unpaid</button>
          <button type="button" onclick={() => onDuplicate(detail)}>Duplicate</button>
        {:else if detail.status === "canceled"}
          <!-- Canceled is terminal server-side (no transition to offer); Duplicate, the replacement for
               reopening, is its only action. -->
          <button type="button" onclick={() => onDuplicate(detail)}>Duplicate</button>
        {/if}
        <button
          type="button"
          onclick={onSendEmail}
          disabled={emailReason !== null}
          aria-disabled={emailing}
          aria-label={emailReason ? "Email to client (unavailable)" : undefined}
          aria-describedby={emailReason ? "email-disabled-reason" : undefined}
        >
          {emailing ? "Sending…" : "Email to client"}
        </button>
        <button type="button" class="primary" onclick={onPrint}>Print</button>
      </div>
      {#if emailReason}
        <p id="email-disabled-reason" class="hint">
          {#if emailReason === "unconfigured"}
            Email isn't configured. <a href="#/settings">Set it up in Settings.</a>
          {:else}
            This client has no email on file. Add one on the Clients page.
          {/if}
        </p>
      {/if}
      {#if cfg.quote.businessName === ""}
        <p class="hint">Printed invoices have no business name yet. <a href="#/settings">Add yours in Settings &gt; Business identity.</a></p>
      {/if}
    {/if}
  </div>
{/if}

<style>
  .back-link { display: inline-flex; align-items: center; gap: 0.35rem; }

  .card .field { margin-bottom: 0.6rem; }
  .address, .notes { font-size: var(--text-sm); margin: 0.3rem 0; }
  .from-quote { color: var(--muted); margin-left: 0.35rem; }
  .item-link { display: flex; flex-wrap: wrap; gap: var(--space-2); align-items: center; margin-top: 0.25rem; }
  /* Delivery table's Date column: same treatment as InvoiceList's .nowrap (scoped styles don't share); a
   * timestamp scrolls horizontally instead of wrapping mid-token. */
  .nowrap { white-space: nowrap; }

  /* h3 font-size comes from theme.css; only the component's vertical rhythm stays local. */
  h3 { margin: var(--space-3) 0 var(--space-2); }

  .totals { margin-top: 0.75rem; display: flex; flex-direction: column; gap: 0.2rem; align-items: flex-end; }
  .totals div { display: flex; gap: 1rem; justify-content: space-between; width: 14rem; }
  .totals span:last-child { font-variant-numeric: tabular-nums; }
  .total-line { font-weight: 700; border-top: 1px solid var(--border); padding-top: 0.3rem; }

  /* Like PaymentsPanel's .payments-card, just ahead of it: Delivery is history and Payments is the
   * actionable card, so Delivery reads first when both show. */
  .delivery-card { margin-top: var(--space-5); }
  .delivery-card h3 { margin-top: 0; }
</style>
