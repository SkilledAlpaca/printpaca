<script lang="ts">
  import type { EffectiveConfig } from "../lib/engine/index";
  import { money, formatDate } from "../lib/format";
  import { bpToPct, type Invoice } from "../lib/invoicesApi";
  import { printLogo } from "../lib/printLogo.svelte";
  import { totalsRows } from "../lib/invoiceForm";
  import { paypalPayLink } from "../lib/paypal";
  import QrCode from "./QrCode.svelte";

  let { cfg, invoice }: { cfg: EffectiveConfig; invoice: Invoice } = $props();

  const taxPct = $derived(bpToPct(invoice.taxBp));
  // "Ship to" renders only when set and different from the billing address; it also gates the parties band's
  // one- vs two-column layout.
  const hasShipTo = $derived(
    !!invoice.clientShippingAddress && invoice.clientShippingAddress !== invoice.clientBillingAddress,
  );
  const hasTax = $derived(invoice.taxCents !== 0 || invoice.taxBp !== 0);
  // The same rows as the editor and the sent view (invoiceForm.ts's totalsRows): Subtotal, Shipping and
  // Discount only when present, then Sales tax and Total, so the printed rows add up.
  const rows = $derived(
    totalsRows({
      lineKinds: invoice.lines.map((l) => l.kind),
      lineAmountsCents: invoice.lines.map((l) => l.amountCents),
      subtotalCents: invoice.subtotalCents,
      discountCents: invoice.discountCents,
      taxCents: invoice.taxCents,
      totalCents: invoice.totalCents,
    }).filter((r) => r.key !== "total" && (r.key !== "tax" || hasTax)),
  );
  const hasPayments = $derived(invoice.payments.length > 0);
  // A fully paid invoice prints "PAID" instead of "$0.00", which read as unpaid. <= 0, not === 0: an
  // overpayment still reads as satisfied, never a negative balance due.
  const isPaid = $derived(invoice.balanceCents <= 0);
  // "" hides the whole pay block: no handle, canceled, or nothing owed.
  const payLink = $derived(paypalPayLink(cfg.invoicing.paypalMe, cfg.currency, invoice));
  // Without a currency code the link carries no amount, so the box tells the payer what to send.
  const payLinkHasAmount = $derived(!!cfg.currency.code);
</script>

<div id="print-view" class="invoice">
  <header>
    <div class="seller-col">
      <!-- Rendered from printLogo.readyUrl (preloaded by App.svelte), not cfg.quote.logoUrl, so
           window.print() never waits on the network (same as PrintView). -->
      {#if printLogo.readyUrl}<img class="logo" src={printLogo.readyUrl} alt="" />{/if}
      {#if cfg.quote.businessName}<p class="business-name">{cfg.quote.businessName}</p>{/if}
      <div class="seller">
        {#if cfg.quote.address}<p class="address">{cfg.quote.address}</p>{/if}
        {#if cfg.quote.email}<p>{cfg.quote.email}</p>{/if}
        {#if cfg.quote.phone}<p>{cfg.quote.phone}</p>{/if}
        {#if cfg.quote.taxId}<p>Tax ID: {cfg.quote.taxId}</p>{/if}
      </div>
    </div>
    <div class="invoice-col">
      <h1 class="wordmark">Invoice <span class="visually-hidden">{invoice.number}</span></h1>
      <p class="invoice-number">{invoice.number}</p>
      <table class="meta-table">
        <tbody>
          <tr><th>Issued</th><td>{invoice.issueDate ? formatDate(invoice.issueDate) : "-"}</td></tr>
          <tr><th>Due</th><td>{invoice.dueDate ? formatDate(invoice.dueDate) : "-"}</td></tr>
        </tbody>
      </table>
    </div>
  </header>

  <section class="parties" class:single={!hasShipTo}>
    <div class="bill-to">
      <h2>Bill to</h2>
      <p class="client-name">{invoice.clientName}</p>
      {#if invoice.clientBillingAddress}<p class="address">{invoice.clientBillingAddress}</p>{/if}
    </div>
    {#if hasShipTo}
      <div class="ship-to">
        <h2>Ship to</h2>
        <p class="address">{invoice.clientShippingAddress}</p>
      </div>
    {/if}
  </section>

  <table class="lines">
    <thead>
      <tr>
        <th>Description</th>
        <th class="num">Qty</th>
        <th class="num">Unit price</th>
        <th class="num">Discount</th>
        <th class="num">Amount</th>
      </tr>
    </thead>
    <tbody>
      {#each invoice.lines as line (line.id)}
        <tr>
          <td>{line.description}</td>
          <td class="num">{line.quantity}</td>
          <td class="num">{money(line.unitPriceCents, cfg)}</td>
          <td class="num">{line.discountCents > 0 ? `-${money(line.discountCents, cfg)}` : "-"}</td>
          <td class="num">{money(line.amountCents, cfg)}</td>
        </tr>
      {/each}
    </tbody>
  </table>

  <div class="totals">
    {#each rows as row (row.key)}
      <div class="totals-row">
        <span>{row.key === "tax" ? `Sales tax (${taxPct.toFixed(2)}%)` : row.key === "discount" ? "Invoice discount" : row.label}</span>
        <span class="num">{money(row.cents, cfg)}</span>
      </div>
    {/each}
    <div class="totals-row">
      <span>Total</span>
      <span class="num">{money(invoice.totalCents, cfg)}</span>
    </div>
    {#if hasPayments}
      <div class="payments">
        <p class="payments-label">Payments</p>
        {#each invoice.payments as p (p.id)}
          <div class="payments-row">
            <span class="payment-date">{formatDate(p.paidDate)}</span>
            <span class="payment-method">{p.method}{p.reference ? ` · ${p.reference}` : ""}</span>
            <span class="num">-{money(p.amountCents, cfg)}</span>
          </div>
        {/each}
      </div>
    {/if}
    <div class="totals-row balance-due">
      <span>Balance due</span>
      {#if isPaid}
        <span class="paid-stamp">Paid</span>
      {:else}
        <span class="num">{money(invoice.balanceCents, cfg)}</span>
      {/if}
    </div>
  </div>

  <footer>
    {#if payLink}
      <div class="payment-box paypal-box">
        <div class="paypal-text">
          <p class="payment-label">Pay with PayPal</p>
          {#if !payLinkHasAmount}<p class="paypal-amount">Send {money(invoice.balanceCents, cfg)}</p>{/if}
          <p class="paypal-link">{payLink}</p>
        </div>
        <QrCode value={payLink} label="QR code for the PayPal payment link" />
      </div>
    {/if}
    {#if cfg.quote.paymentInstructions}
      <div class="payment-box">
        <p class="payment-label">Payment</p>
        <p class="payment-text">{cfg.quote.paymentInstructions}</p>
      </div>
    {/if}
    {#if invoice.notes}<p class="notes">{invoice.notes}</p>{/if}
    {#if cfg.quote.termsText}<p class="terms">{cfg.quote.termsText}</p>{/if}
  </footer>
</div>

<style>
  /* Print colors stay literal (black on white), same spacing and type scale as the screen. The brand amber
   * appears literally exactly twice on purpose (header rule and .total-due's top rule), never
   * var(--accent), which differs in dark theme. */
  #print-view { background: white; color: black; padding: 2rem; }
  /* theme.css stripes and highlights table rows with tokens; a dark scheme would put black text on those. */
  #print-view tr, #print-view th, #print-view td { background: transparent; }

  /* Header band: two-column grid; the 2px amber rule is the band's border-bottom, not a separate <hr>. */
  header {
    display: grid;
    grid-template-columns: 1fr auto;
    gap: var(--space-4);
    align-items: start;
    padding-bottom: var(--space-3);
    border-bottom: 2px solid #f1b750;
    margin-bottom: var(--space-4);
  }
  .logo { display: block; max-height: 56px; max-width: 240px; object-fit: contain; margin-bottom: var(--space-2); }
  .business-name { font-weight: 700; margin: 0; }
  .seller { font-size: var(--text-sm); color: #444; margin: var(--space-1) 0 0; }
  .seller p { margin: 0.1rem 0; white-space: pre-line; }
  .address { margin: 0.1rem 0; white-space: pre-line; }

  .invoice-col { text-align: right; }
  .wordmark {
    font-size: 28px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    margin: 0;
  }
  .invoice-number { margin: 0.2rem 0 0.6rem; font-weight: 600; }
  .meta-table { margin-left: auto; border-collapse: collapse; font-size: var(--text-sm); }
  .meta-table th {
    font-weight: 600;
    color: #666;
    text-align: right;
    padding: 0 0.5rem 0 0;
  }
  .meta-table td { text-align: right; font-variant-numeric: tabular-nums; }

  /* Parties band: two columns; one column when there is no ship-to address. */
  .parties { display: grid; grid-template-columns: 1fr 1fr; gap: var(--space-4); margin: 0 0 var(--space-4); }
  .parties.single { grid-template-columns: 1fr; }
  /* Uppercase micro-label heading mirroring theme.css's .section-label with a literal gray (see the
   * print-colors note above). */
  .bill-to h2, .ship-to h2 {
    font-size: var(--text-xs);
    text-transform: uppercase;
    letter-spacing: 0.06em;
    font-weight: 600;
    color: #666;
    margin: 0 0 var(--space-1);
  }
  .client-name { font-weight: 600; margin: 0; }

  /* Line table: description stretches; every other column is width:1% + nowrap, which makes auto table
   * layout size it to content instead of splitting spare width evenly. */
  table.lines { width: 100%; border-collapse: collapse; }
  table.lines th, table.lines td { padding: 0.5rem var(--space-2); border-bottom: 1px solid #ddd; text-align: left; }
  table.lines th.num, table.lines td.num { width: 1%; white-space: nowrap; text-align: right; font-variant-numeric: tabular-nums; }
  table.lines thead th {
    text-transform: uppercase;
    letter-spacing: 0.04em;
    font-size: var(--text-xs);
    color: #666;
    font-weight: 600;
  }

  /* Totals block: right-aligned, capped width; replaces tfoot so "Total due" can carry its own heavy rule
   * apart from the line table's hairlines. */
  .totals { margin-left: auto; margin-top: var(--space-3); max-width: 45%; }
  .totals-row {
    display: flex;
    justify-content: space-between;
    gap: var(--space-3);
    padding: 0.25rem 0;
    font-variant-numeric: tabular-nums;
  }
  /* The payments list and Balance due row live in .totals between "Total" and the footer. Balance due
   * carries the amber heavy rule, keeping the accent count at two. */
  .payments {
    margin-top: var(--space-2);
    padding-top: var(--space-2);
    border-top: 1px solid #ddd;
  }
  .payments-label {
    font-size: var(--text-xs);
    text-transform: uppercase;
    letter-spacing: 0.06em;
    font-weight: 600;
    color: #666;
    margin: 0 0 var(--space-1);
  }
  .payments-row {
    display: flex;
    justify-content: space-between;
    gap: var(--space-2);
    font-size: var(--text-sm);
    color: #444;
    padding: 0.15rem 0;
    font-variant-numeric: tabular-nums;
  }
  .payment-date { flex-shrink: 0; }
  .payment-method {
    flex: 1;
    text-align: left;
    margin: 0 var(--space-2);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .balance-due {
    margin-top: 0.4rem;
    padding-top: 0.5rem;
    border-top: 2px solid #f1b750;
    font-weight: 700;
    font-size: var(--text-lg);
  }
  /* Text badge, no images: a bordered box reads as a stamp even in monochrome print. */
  .paid-stamp {
    display: inline-block;
    border: 2px solid #2e7d32;
    color: #2e7d32;
    font-weight: 700;
    font-size: var(--text-sm);
    letter-spacing: 0.08em;
    text-transform: uppercase;
    padding: 0.15rem 0.7rem;
    border-radius: 3px;
  }

  /* Footer band: payment instructions in a light box so the actionable info stands out; notes and terms
   * below, small and muted. */
  .payment-box {
    border: 1px solid #ddd;
    padding: var(--space-3);
    margin: var(--space-4) 0 var(--space-2);
  }
  .payment-label {
    font-size: var(--text-xs);
    text-transform: uppercase;
    letter-spacing: 0.06em;
    font-weight: 600;
    color: #666;
    margin: 0 0 0.3rem;
  }
  .payment-text { margin: 0; white-space: pre-line; }
  .paypal-box { display: flex; align-items: center; justify-content: space-between; gap: var(--space-3); break-inside: avoid; }
  .paypal-text { min-width: 0; }
  .paypal-amount { margin: 0 0 0.2rem; font-weight: 600; }
  .paypal-link { margin: 0; overflow-wrap: anywhere; }
  .notes { font-size: var(--text-sm); white-space: pre-line; }
  .terms { font-size: var(--text-sm); color: #444; }
</style>
