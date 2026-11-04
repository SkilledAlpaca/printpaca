<script module lang="ts">
  /** Handed to `onordered` on success: a self-contained summary independent of live component state, which
   * PublicQuotePage persists to sessionStorage and renders after a reload with no PublicItem lookup (the
   * item may since be deleted). */
  export interface ItemOrderConfirmation {
    id: string;
    /** The ordered item's id, so the confirmation can lead back to its card; absent in a confirmation saved by an older build. */
    itemId?: string;
    /** The short reference the customer quotes ("HL-1001"); absent in a confirmation saved by an older build. */
    reference?: string;
    priceCents: number;
    itemName: string;
    /** "Color: Red · Size: Large" (answered groups only); "" when the item has no options chosen. */
    optionName: string;
    quantity: number;
    email: string;
  }
</script>

<script lang="ts">
  import { onMount, tick } from "svelte";
  import { announce } from "../lib/a11y.svelte";
  import { visitor } from "./visitor.svelte";
  import { formatMoney, type CurrencyConfig } from "../lib/engine/index";
  import { itemTotalCents } from "../lib/itemMath";
  import {
    missingRequired,
    optionSummary,
    pickDisplayImage,
    selectedDeltaCents as sumSelectedDelta,
    selectionsPayload,
    type Selected,
  } from "../lib/itemOptions";
  import { NAME_MAX_BYTES, NOTE_MAX_BYTES, ITEM_ORDER_EMAIL_MAX_BYTES, utf8ByteLength, isPlausibleEmail, EMAIL_FORMAT_ERROR } from "../lib/publicFormLimits";
  import { DEFAULT_MAX_ORDER_QTY, fromUnitCents, isValidQuantity, normalizedQuantityText, overCapMessage, unitBreakdown } from "./publicPricing";
  import type { OrderDraft } from "./orderDraft";
  import VisitorReset from "./VisitorReset.svelte";
  import FormErrorSummary from "./FormErrorSummary.svelte";
  import Card from "../components/ui/Card.svelte";
  import Field from "../components/ui/Field.svelte";
  import RateLimitNotice from "./RateLimitNotice.svelte";
  import { submitItemOrder, type PublicConfig, type PublicItem } from "./api";

  let {
    item,
    currency,
    branding,
    maxOrderQty = DEFAULT_MAX_ORDER_QTY,
    onback,
    onordered,
    onpaused,
    draft,
    shippingNote = "",
    turnaroundNote = "",
  }: {
    item: PublicItem;
    currency: CurrencyConfig;
    /** Contact details for the rate-limit message. */
    branding: PublicConfig["branding"];
    /** shop.maxOrderQty: the per-order cap the server enforces. */
    maxOrderQty?: number;
    onback: () => void;
    /** Fired instead of showing a confirmation locally: the parent owns display and persistence so it
     * survives a reload, even though this component is torn down right after. */
    onordered: (confirmation: ItemOrderConfirmation) => void;
    /** Fired when the server refuses the order because the shop closed after this page loaded (503 with
     * paused: true); the parent then shows the Closed page. */
    onpaused?: (message: string, draft: OrderDraft) => void;
    /** What the visitor had typed when an earlier submit was refused because the shop closed; restores the form. */
    draft?: OrderDraft;
    /** shop.shippingNote; the fixed wording shows when blank. */
    shippingNote?: string;
    /** shop.turnaroundNote: shown with the total when the owner wrote one. */
    turnaroundNote?: string;
  } = $props();

  // groupId -> optionId. Starts empty: a required group shows its "Choose ..." placeholder and an optional one
  // shows "None" (an empty choice adds no price). item is a remount-on-change prop (PublicQuotePage.svelte
  // wraps this component in {#key orderItem.id}), so nothing here needs to reset when it changes.
  // svelte-ignore state_referenced_locally -- the draft only seeds the form once, on mount
  let selected = $state<Selected>({ ...(draft?.selected ?? {}) });
  function choose(groupId: string, optionId: string) {
    selected[groupId] = optionId;
  }
  // The photo of the first group (in display order) whose choice has one, else the item photo.
  const displayImage = $derived(pickDisplayImage(item.optionGroups, selected));
  const heroImage = $derived(displayImage ?? (item.imageUrl ? { url: item.imageUrl, alt: item.name } : null));
  let lightbox: HTMLDialogElement | undefined = $state();
  // number | null like PublicQuotePage's quantity: a cleared number input binds null, not 0 or NaN.
  // svelte-ignore state_referenced_locally -- see above
  let quantity = $state<number | null>(draft ? draft.quantity : 1);
  // Name and email are shared with the custom-estimate form (visitor.svelte.ts), so a visitor who already
  // typed them isn't asked again (WCAG 3.3.7).
  const name = $derived(visitor.name);
  const email = $derived(visitor.email);
  // svelte-ignore state_referenced_locally -- see above
  let note = $state(draft?.note ?? "");
  // Focus target on open (WCAG 2.4.3): the card that opened this panel unmounts, so focus would otherwise drop
  // to <body>.
  let headingEl: HTMLElement | undefined = $state();
  onMount(() => headingEl?.focus());
  let submitting = $state(false);
  // Errors from the server. They describe the form as it was sent, so they disappear as soon as the visitor
  // changes anything (serverErrorsFor must match the current form).
  let serverErrors = $state<string[]>([]);
  let serverErrorsFor = $state("");
  let rateLimited = $state(false);
  const formSnapshot = $derived(
    JSON.stringify([selected, quantity, visitor.name, visitor.email, note]),
  );
  // True once the visitor has chosen an option, changed the quantity or written a note.
  const dirty = $derived(Object.values(selected).some((v) => v !== "") || quantity !== 1 || note !== "");
  const visibleServerErrors = $derived(serverErrorsFor === formSnapshot ? serverErrors : []);
  const visibleRateLimited = $derived(rateLimited && serverErrorsFor === formSnapshot);

  // in_stock is capped at what's left and at the shop's per-order cap; allowBackorder lifts the cap to the
  // per-order cap regardless of remaining stock, matching handlePublicItemOrder (server/public.go).
  const maxQuantity = $derived(
    item.stock.mode === "in_stock" && !item.stock.allowBackorder ? Math.min(maxOrderQty, item.stock.qty ?? 0) : maxOrderQty,
  );

  // The cap comes from the stock on hand when that is below the per-order cap.
  const stockLimited = $derived(item.stock.mode === "in_stock" && !item.stock.allowBackorder && (item.stock.qty ?? 0) < maxOrderQty);
  // A quantity above the cap is left as typed: the message replaces the total, and Place order is blocked.
  const quantityNotice = $derived(overCapMessage(quantity, maxQuantity, stockLimited ? "stock" : "policy"));

  // A backorder in_stock item's "Made to order" label appears on the gallery card only; show it here too for
  // visitors arriving by direct link, so exceeding remaining stock isn't read as an error.
  const showMadeToOrderNotice = $derived(
    item.stock.mode === "in_stock" && item.stock.allowBackorder && quantity !== null && quantity > (item.stock.qty ?? 0),
  );

  const selectedDeltaCents = $derived(sumSelectedDelta(item.optionGroups, selected));
  const missingGroups = $derived(missingRequired(item.optionGroups, selected));
  const unitPriceCents = $derived(item.priceCents + selectedDeltaCents);
  // null unless the quantity is a whole number in range: a cleared, negative, fractional or too-large entry has
  // no honest total, so the template shows a prompt, not a price.
  const totalCents = $derived(
    isValidQuantity(quantity, maxQuantity) ? itemTotalCents(item.priceCents, selectedDeltaCents, quantity) : null,
  );
  // "$22.00 + Large $4.00" under the total, and the lowest price still open while a required choice that changes
  // the price is unanswered, so the panel never shows a plain base price that the next choice would raise.
  const breakdown = $derived(unitBreakdown(item.priceCents, item.optionGroups, selected, (c) => formatMoney(c, currency)));
  const fromCents = $derived.by(() => {
    const unit = fromUnitCents(item.priceCents, item.optionGroups, selected);
    return unit === null || !isValidQuantity(quantity, maxQuantity) ? null : unit * quantity;
  });
  const stockLine = $derived.by(() => {
    if (item.stock.mode !== "in_stock") return "Made to order.";
    const qty = item.stock.qty ?? 0;
    if (qty > 0) return item.stock.allowBackorder ? `${qty} in stock; more made to order.` : `${qty} in stock.`;
    return item.stock.allowBackorder ? "Made to order." : "Out of stock.";
  });

  // Name, email, and note errors are shown inline on their fields (aria-invalid + aria-describedby, WCAG
  // 3.3.1); the rest go to the summary list. Shown only after the first submit attempt.
  const fieldErrors = $derived.by(() => {
    const out: { name?: string; email?: string; note?: string; other: string[] } = { other: [] };
    if (name.trim() === "") out.name = "Name is required.";
    else if (utf8ByteLength(name) > NAME_MAX_BYTES) out.name = `Name must be ${NAME_MAX_BYTES} characters or fewer.`;
    if (email.trim() === "") out.email = "Email is required.";
    else if (utf8ByteLength(email) > ITEM_ORDER_EMAIL_MAX_BYTES)
      out.email = `Email must be ${ITEM_ORDER_EMAIL_MAX_BYTES} characters or fewer.`;
    else if (!isPlausibleEmail(email.trim())) out.email = EMAIL_FORMAT_ERROR;
    if (utf8ByteLength(note) > NOTE_MAX_BYTES) out.note = `Note must be ${NOTE_MAX_BYTES} characters or fewer.`;
    for (const g of missingGroups) out.other.push(`Choose ${g.label}.`);
    if (!isValidQuantity(quantity, maxQuantity)) {
      out.other.push(`Quantity must be a whole number from 1 to ${maxQuantity.toLocaleString("en-US")}.`);
    }
    return out;
  });
  const formErrorCount = $derived(
    (fieldErrors.name ? 1 : 0) + (fieldErrors.email ? 1 : 0) + (fieldErrors.note ? 1 : 0) + fieldErrors.other.length,
  );
  let showErrors = $state(false);
  let formEl: HTMLElement | undefined = $state();
  const nameError = $derived(showErrors ? fieldErrors.name : undefined);
  const emailError = $derived(showErrors ? fieldErrors.email : undefined);
  const noteError = $derived(showErrors ? fieldErrors.note : undefined);
  // Each error shows once, under its field. The line above the button counts them and links to the first.
  const quantityError = $derived(quantityNotice !== "" ? quantityNotice : showErrors && !isValidQuantity(quantity, maxQuantity) ? (fieldErrors.other.at(-1) ?? "") : "");
  const summaryProblems = $derived.by(() => {
    if (!showErrors) return [];
    const out: { id: string; label: string }[] = [];
    for (const g of item.optionGroups) if (g.required && missingGroups.some((m) => m.id === g.id)) out.push({ id: `item-group-${g.id}`, label: g.label });
    if (!isValidQuantity(quantity, maxQuantity)) out.push({ id: "item-qty", label: "Quantity" });
    if (fieldErrors.name) out.push({ id: "item-name", label: "Name" });
    if (fieldErrors.email) out.push({ id: "item-email", label: "Email" });
    if (fieldErrors.note) out.push({ id: "item-note", label: "Note" });
    return out;
  });
  const quantityInvalid = $derived(!isValidQuantity(quantity, maxQuantity) && (showErrors || quantityNotice !== ""));
  const missingIds = $derived(new Set(missingGroups.map((g) => g.id)));

  async function submit() {
    // aria-disabled (not disabled) while in flight, so the focused button keeps focus (WCAG 2.4.3).
    if (submitting) return;
    serverErrors = [];
    rateLimited = false;
    if (formErrorCount > 0 || quantity === null) {
      showErrors = true;
      await tick();
      formEl?.querySelector<HTMLElement>("[aria-invalid='true']")?.focus();
      return;
    }

    submitting = true;
    announce("Sending your order.");
    const result = await submitItemOrder(
      {
        itemId: item.id,
        selections: selectionsPayload(item.optionGroups, selected),
        quantity,
        name: name.trim(),
        email: email.trim(),
        note: note.trim(),
      },
    );
    submitting = false;
    // Whatever happened, "Sending your order." no longer applies.
    announce("");

    if (result.ok && result.id !== undefined && result.priceCents !== undefined) {
      // Name and email stay in the in-memory store so "Order another item" doesn't ask again; "Not you? Clear"
      // empties them, and a reload does too.
      const sentEmail = email.trim();
      onordered({
        id: result.id,
        itemId: item.id,
        reference: result.reference,
        priceCents: result.priceCents,
        itemName: item.name,
        optionName: optionSummary(item.optionGroups, selected),
        quantity,
        email: sentEmail,
      });
      return;
    }
    serverErrorsFor = formSnapshot;
    if (result.status === 429) {
      rateLimited = true;
      return;
    }
    if (result.paused) {
      onpaused?.(result.paused, {
        kind: "order",
        itemId: item.id,
        selected: { ...selected },
        quantity,
        note,
        name: visitor.name,
        email: visitor.email,
      });
      return;
    }
    if (result.status === 503) {
      // Friendlier than the raw backpressure message; this is the visitor's own retry path.
      serverErrors = ["The shop is busy right now. Try again in a moment."];
      return;
    }
    // Any other failure (network, 422, ...) leaves the visitor's typed fields untouched.
    serverErrors = result.errors;
  }

  // Escape goes back to the gallery, like the Back button. Ignored in text fields and selects (which use Escape
  // themselves) and while the photo is enlarged (the dialog closes itself).
  function onKeydown(e: KeyboardEvent) {
    if (e.key !== "Escape" || e.defaultPrevented || lightbox?.open) return;
    // A form with choices, a quantity or a note in it is never discarded by a stray Escape (WCAG 3.2.2); Back
    // still leaves it. Name and email survive in the visitor store, so they don't count.
    if (dirty) return;
    const t = e.target as HTMLElement | null;
    if (t && /^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName)) return;
    onback();
  }
</script>

<svelte:window onkeydown={onKeydown} />

<div class="panel">
  <Card>
    <div class="item-summary">
      {#if heroImage}
        <!-- Tap to enlarge. The alt names the option, so the swap is announced; the box is fixed, so swapping
             never shifts the layout. -->
        <button type="button" class="photo-btn" aria-label="Enlarge photo" onclick={() => lightbox?.showModal()}>
          {#if displayImage}
            <img class="hero" src={displayImage.url} alt={displayImage.alt} data-option-photo />
          {:else}
            <img class="hero" src={heroImage.url} alt="" />
          {/if}
        </button>
      {:else}
        <div class="hero thumb-empty" aria-hidden="true"></div>
      {/if}
      <div class="item-summary-text">
        <h2 tabindex="-1" bind:this={headingEl}>{item.name}</h2>
        {#if item.description}<p class="i-desc">{item.description}</p>{/if}
        <p class="stock-line">{stockLine}</p>
        <!-- After the heading in the DOM, so the first Tab from the heading reaches it. -->
        <button type="button" class="quiet back" onclick={onback}>&larr; Back to gallery</button>
        {#if !dirty}<small class="esc-hint">Press Esc to go back.</small>{/if}
      </div>
    </div>
  </Card>

  <dialog
    class="lightbox"
    bind:this={lightbox}
    aria-label="Photo of {item.name}"
    onclick={(e) => {
      if (e.target === lightbox) lightbox?.close();
    }}
  >
    {#if heroImage}<img src={heroImage.url} alt={displayImage?.alt ?? item.name} />{/if}
    <button type="button" onclick={() => lightbox?.close()}>Close</button>
  </dialog>

  <!-- novalidate: the panel validates itself and shows the messages inline (WCAG 3.3.1). -->
  <Card
    as="form"
    bind:el={formEl}
    novalidate
    onsubmit={(e: SubmitEvent) => {
      e.preventDefault();
      void submit();
    }}
  >
    <div class="fields">
      {#each item.optionGroups as g (g.id)}
        {@const invalid = showErrors && g.required && missingIds.has(g.id)}
        <Field
          id="item-group-{g.id}"
          label="{g.label}{g.required ? ' (required)' : ' (optional)'}"
          error={invalid ? `Choose ${g.label}.` : undefined}
          size="m"
        >
          {#snippet children(f)}
            <select
              id={f.id}
              required={g.required}
              value={selected[g.id] ?? ""}
              onchange={(e) => choose(g.id, e.currentTarget.value)}
              aria-invalid={f.invalid}
              aria-describedby={f.describedby}
              data-group-id={g.id}
            >
              <option value="">{g.required ? `Choose ${g.label}` : "None"}</option>
              {#each g.options as o (o.id)}
                <option value={o.id}
                  >{o.name}{o.deltaCents !== 0
                    ? ` (${o.deltaCents > 0 ? "+" : ""}${formatMoney(o.deltaCents, currency)})`
                    : ""}</option
                >
              {/each}
            </select>
          {/snippet}
        </Field>
      {/each}

      <Field id="item-qty" label="Quantity" size="s" describedby="item-qty-notice">
        {#snippet children(f)}
          <input
            id={f.id}
            type="number"
            min="1"
            max={maxQuantity}
            step="1"
            bind:value={quantity}
            onchange={(e) => {
              // "1e3" stays on screen as typed; show the number it prices.
              const plain = normalizedQuantityText(e.currentTarget.value);
              if (plain !== null) e.currentTarget.value = plain;
            }}
            required
            aria-invalid={quantityInvalid ? "true" : undefined}
            aria-describedby={f.describedby}
          />
        {/snippet}
      </Field>
      <!-- Outside the field so the notices aren't part of its name, and always mounted so the live region
           announces text changes (WCAG 1.3.1, 4.1.3). The quantity message shows here and nowhere else. -->
      <div id="item-qty-notice" role="status">
        {#if quantityError}<p class="warn-text qty-error">{quantityError}</p>{/if}
        {#if showMadeToOrderNotice}
          <p class="advisory-text">Made to order. Larger quantities may take longer.</p>
        {/if}
      </div>

      <VisitorReset nameId="item-name" />
      <Field id="item-name" label="Name" error={nameError}>
        {#snippet children(f)}
          <input
            id={f.id}
            type="text"
            autocomplete="name"
            bind:value={visitor.name}
            maxlength={NAME_MAX_BYTES}
            required
            aria-invalid={f.invalid}
            aria-describedby={f.describedby}
          />
        {/snippet}
      </Field>
      <Field id="item-email" label="Email" error={emailError}>
        {#snippet children(f)}
          <input
            id={f.id}
            type="email"
            autocomplete="email"
            inputmode="email"
            bind:value={visitor.email}
            maxlength={ITEM_ORDER_EMAIL_MAX_BYTES}
            required
            aria-invalid={f.invalid}
            aria-describedby={f.describedby}
          />
        {/snippet}
      </Field>
      <Field id="item-note" label="Note (optional)" error={noteError}>
        {#snippet children(f)}
          <textarea
            id={f.id}
            bind:value={note}
            maxlength={NOTE_MAX_BYTES}
            rows="3"
            aria-invalid={f.invalid}
            aria-describedby={f.describedby}
          ></textarea>
        {/snippet}
      </Field>
    </div>

    <!-- The customer's submission block: the amount, the shipping note, any errors, the follow-up note and the
         action stay together, so what the customer is agreeing to sits next to the button that sends it. -->
    <Card tone="inset" role="group" aria-labelledby="item-total-label">
      <div class="submit-block">
        <h2 class="section-label" id="item-total-label">Total</h2>
        <!-- Persistent live region: the settled total is spoken when option or quantity changes (WCAG 4.1.3). -->
        <div aria-live="polite" aria-atomic="true">
          {#if totalCents !== null}
            <p class="price">
              {#if fromCents !== null}<span class="from">from&nbsp;</span>{formatMoney(fromCents, currency)}{:else}{formatMoney(totalCents, currency)}{/if}
              {#if quantity !== null && quantity > 1}
                <span class="per-unit"> &middot; {formatMoney(unitPriceCents, currency)} each</span>
              {/if}
            </p>
            {#if fromCents !== null}
              <p class="muted breakdown">Choose {missingGroups.map((g) => g.label).join(" and ")} to see the exact total.</p>
            {:else if breakdown}
              <p class="muted breakdown">{breakdown}{quantity !== null && quantity > 1 ? ` each, \u00d7 ${quantity}` : ""}</p>
            {/if}
          {:else}
            <p class="muted">Enter a whole number from 1 to {maxQuantity.toLocaleString("en-US")} to see a total.</p>
          {/if}
        </div>
        <p class="muted shipping-note">{shippingNote || "Shipping, if any, is confirmed by email."}</p>
        {#if turnaroundNote}<p class="muted turnaround-note">{turnaroundNote}</p>{/if}

        <!-- Persistent alert container with a plain list inside (WCAG 1.3.1, 4.1.3). -->
        <div role="alert">
          {#if visibleRateLimited}
            <RateLimitNotice {branding} />
          {/if}
          <FormErrorSummary problems={summaryProblems} />
          {#if visibleServerErrors.length > 0}
            <ul class="error-list">
              {#each new Set(visibleServerErrors) as e (e)}<li class="warn-text">{e}</li>{/each}
            </ul>
          {/if}
        </div>

        <p id="order-submit-note" class="submit-note">You won't be charged now. We'll review your order and follow up by email.</p>
        <button type="submit" class="primary" class:busy={submitting} aria-disabled={submitting ? "true" : undefined} aria-describedby="order-submit-note">
          {submitting ? "Submitting…" : "Place order"}
        </button>
      </div>
    </Card>
  </Card>
</div>

<style>
  .panel {
    display: flex;
    flex-direction: column;
    gap: var(--gap);
  }

  .item-summary {
    display: flex;
    align-items: flex-start;
    gap: var(--gap);
  }
  .item-summary-text { min-width: 0; flex: 1; display: flex; flex-direction: column; gap: var(--space-1); align-items: flex-start; }
  .item-summary-text h2 { margin: 0; }
  .i-desc { margin: 0; color: var(--muted); font-size: var(--text-sm); }
  .stock-line { margin: 0; font-size: var(--text-sm); color: var(--muted); }
  .back { margin-top: var(--space-1); }
  /* Only where there is a keyboard to press it on: a mouse or trackpad, not a touch screen. */
  .esc-hint { display: none; color: var(--muted); font-size: var(--text-sm); }
  @media (hover: hover) and (pointer: fine) {
    .esc-hint { display: inline; }
  }

  /* A real photo size, not a thumbnail: 12rem beside the text on a wide screen, full width on a phone. */
  .photo-btn {
    padding: 0;
    border: 0;
    background: var(--surface-2);
    border-radius: calc(var(--radius) / 2);
    line-height: 0;
    flex-shrink: 0;
    cursor: zoom-in;
    min-height: 0;
  }
  .hero {
    display: block;
    width: 12rem;
    height: 12rem;
    object-fit: cover;
    border-radius: calc(var(--radius) / 2);
    background: var(--surface-2);
  }
  .thumb-empty { border: 1px dashed var(--border); flex-shrink: 0; }

  .lightbox {
    max-width: min(92vw, 48rem);
    padding: var(--space-3);
    border: 0;
    border-radius: var(--radius);
    background: var(--surface);
    color: var(--text);
  }
  .lightbox::backdrop { background: rgb(0 0 0 / 0.6); }
  .lightbox img { display: block; max-width: 100%; max-height: 70vh; margin: 0 auto var(--space-3); }
  .lightbox button { display: block; margin: 0 auto; }

  @media (max-width: 600px) {
    .item-summary { flex-direction: column; align-items: stretch; }
    .photo-btn, .thumb-empty { width: 100%; }
    .hero { width: 100%; height: auto; aspect-ratio: 4 / 3; }
    /* 44 px tap targets on phones; the app-wide quiet button is 32 px. */
    .back { min-height: 44px; }
  }

  /* The form card stacks its fields, then the submission block, on the density gap. */
  .fields { display: flex; flex-direction: column; gap: var(--gap); margin-bottom: var(--gap); }
  /* Focused fields scroll clear of the viewport edge, so the ring isn't clipped at the bottom. */
  .fields :global(:is(input, select, textarea, button)), .submit-block button { scroll-margin: var(--space-4); }

  .submit-block { display: flex; flex-direction: column; gap: var(--space-2); text-align: center; }
  .submit-block h2 { margin: 0; }
  .muted { color: var(--muted); font-size: var(--text-sm); margin: 0; }
  .shipping-note { margin: 0; }
  .price {
    font-size: var(--text-xl);
    font-weight: 700;
    font-variant-numeric: tabular-nums;
    margin: var(--space-1) 0;
  }
  /* The per-unit price sits inside the same .price line as the total, lighter and muted so the total stays
   * dominant. */
  .per-unit { font-weight: 400; font-size: var(--text-base); color: var(--muted); }

  .from { font-size: var(--text-base); font-weight: 400; color: var(--muted); }
  .qty-error { margin: var(--space-1) 0 0; }
  .error-list { list-style: none; margin: 0; padding: 0; text-align: left; }
  .error-list li { margin: var(--space-1) 0; }
  /* In flight: aria-disabled keeps focus on the button, so the busy look is visual only. */
  .submit-note { color: var(--muted); font-size: var(--text-sm); margin: 0; }
  button.busy { opacity: 0.6; cursor: progress; }
</style>
