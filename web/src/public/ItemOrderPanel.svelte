<script module lang="ts">
  /** Handed to `onordered` on success: a self-contained summary independent of live component state, which
   * PublicQuotePage persists to sessionStorage and renders after a reload with no PublicItem lookup (the
   * item may since be deleted). */
  export interface ItemOrderConfirmation {
    id: string;
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
  import { clearVisitor, visitor } from "./visitor.svelte";
  import { formatMoney, type CurrencyConfig } from "../lib/engine/index";
  import { itemTotalCents } from "../lib/itemMath";
  import {
    missingRequired,
    optionSummary,
    pickDisplayImage,
    selectedDeltaCents as sumSelectedDelta,
    selectionsPayload,
    touchRecent,
    type Selected,
  } from "../lib/itemOptions";
  import { NAME_MAX_BYTES, NOTE_MAX_BYTES, ITEM_ORDER_EMAIL_MAX_BYTES, utf8ByteLength, isPlausibleEmail, EMAIL_FORMAT_ERROR } from "../lib/publicFormLimits";
  import { clampQuantity, perUnitCents } from "./publicPricing";
  import { submitItemOrder, type PublicItem } from "./api";

  let {
    item,
    currency,
    onback,
    onordered,
  }: {
    item: PublicItem;
    currency: CurrencyConfig;
    onback: () => void;
    /** Fired instead of showing a confirmation locally: the parent owns display and persistence so it
     * survives a reload, even though this component is torn down right after. */
    onordered: (confirmation: ItemOrderConfirmation) => void;
  } = $props();

  // groupId -> optionId. Starts empty: a required group shows its "Choose ..." placeholder and an optional one
  // shows "None" (an empty choice adds no price). item is a remount-on-change prop (PublicQuotePage.svelte
  // wraps this component in {#key orderItem.id}), so nothing here needs to reset when it changes.
  let selected = $state<Selected>({});
  // Group ids, most recently changed first: the photo shown is the newest selection that has one.
  let recent = $state<string[]>([]);
  function choose(groupId: string, optionId: string) {
    selected[groupId] = optionId;
    recent = touchRecent(recent, groupId);
  }
  const displayImage = $derived(pickDisplayImage(item.optionGroups, selected, recent));
  // number | null like PublicQuotePage's quantity: a cleared number input binds null, not 0 or NaN.
  let quantity = $state<number | null>(1);
  // Name and email are shared with the custom-estimate form (visitor.svelte.ts), so a visitor who already
  // typed them isn't asked again (WCAG 3.3.7).
  const name = $derived(visitor.name);
  const email = $derived(visitor.email);
  let note = $state("");
  // Focus target on open (WCAG 2.4.3): the card that opened this panel unmounts, so focus would otherwise drop
  // to <body>.
  let headingEl: HTMLElement | undefined = $state();
  onMount(() => headingEl?.focus());
  // Set only when a typed quantity exceeded maxQuantity and was clamped; cleared via oninput (see
  // PublicQuotePage's quantityClampNotice).
  let quantityClampNotice = $state("");

  let submitting = $state(false);
  let submitErrors = $state<string[]>([]);
  let rateLimited = $state(false);

  // in_stock is capped at what's left and at 100; allowBackorder lifts the cap to the global 100 regardless
  // of remaining stock, matching handlePublicItemOrder (server/public.go).
  const maxQuantity = $derived(
    item.stock.mode === "in_stock" && !item.stock.allowBackorder ? Math.min(100, item.stock.qty ?? 0) : 100,
  );

  $effect(() => {
    if (quantity === null) return;
    const clamped = clampQuantity(quantity, maxQuantity);
    if (clamped.message) {
      quantity = clamped.value;
      quantityClampNotice = clamped.message;
    }
  });

  // A backorder in_stock item's "Made to order" label appears on the gallery card only; show it here too for
  // visitors arriving by direct link, so exceeding remaining stock isn't read as an error.
  const showMadeToOrderNotice = $derived(
    item.stock.mode === "in_stock" && item.stock.allowBackorder && quantity !== null && quantity > (item.stock.qty ?? 0),
  );

  const selectedDeltaCents = $derived(sumSelectedDelta(item.optionGroups, selected));
  const missingGroups = $derived(missingRequired(item.optionGroups, selected));
  const unitPriceCents = $derived(item.priceCents + selectedDeltaCents);
  // null while the quantity input is cleared: there is nothing honest to compute (the template shows a dash,
  // not $0.00).
  const totalCents = $derived(quantity === null ? null : itemTotalCents(item.priceCents, selectedDeltaCents, quantity));

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
    if (quantity === null || !Number.isInteger(quantity) || quantity < 1 || quantity > maxQuantity) {
      out.other.push(`Quantity must be a whole number from 1 to ${maxQuantity}.`);
    }
    return out;
  });
  const formErrorCount = $derived(
    (fieldErrors.name ? 1 : 0) + (fieldErrors.email ? 1 : 0) + (fieldErrors.note ? 1 : 0) + fieldErrors.other.length,
  );
  let showErrors = $state(false);
  let formEl: HTMLFormElement | undefined = $state();
  const nameError = $derived(showErrors ? fieldErrors.name : undefined);
  const emailError = $derived(showErrors ? fieldErrors.email : undefined);
  const noteError = $derived(showErrors ? fieldErrors.note : undefined);

  async function submit() {
    // aria-disabled (not disabled) while in flight, so the focused button keeps focus (WCAG 2.4.3).
    if (submitting) return;
    submitErrors = [];
    rateLimited = false;
    if (formErrorCount > 0 || quantity === null) {
      showErrors = true;
      submitErrors = fieldErrors.other;
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

    if (result.ok && result.id !== undefined && result.priceCents !== undefined) {
      // The order is placed: a shared device shouldn't keep the visitor's name and email around.
      const sentEmail = email.trim();
      clearVisitor();
      onordered({
        id: result.id,
        priceCents: result.priceCents,
        itemName: item.name,
        optionName: optionSummary(item.optionGroups, selected),
        quantity,
        email: sentEmail,
      });
      return;
    }
    if (result.status === 429) {
      rateLimited = true;
      return;
    }
    if (result.status === 503) {
      // Friendlier than the raw backpressure message; this is the visitor's own retry path.
      submitErrors = ["The shop is busy right now. Try again in a moment."];
      return;
    }
    // Any other failure (network, 422, ...) leaves the visitor's typed fields untouched.
    submitErrors = result.errors;
  }
</script>

<div class="panel">
  <button type="button" class="quiet back" onclick={onback}>&larr; Back to gallery</button>

  <div class="card item-summary">
    {#if displayImage}
      <!-- Same fixed box as the item photo, so swapping never shifts the layout; the alt names the option. -->
      <img class="thumb" src={displayImage.url} alt={displayImage.alt} data-option-photo />
    {:else if item.imageUrl}
      <img class="thumb" src={item.imageUrl} alt="" />
    {:else}
      <div class="thumb thumb-empty" aria-hidden="true"></div>
    {/if}
    <div class="item-summary-text">
      <h2 tabindex="-1" bind:this={headingEl}>{item.name}</h2>
      {#if item.description}<p class="i-desc">{item.description}</p>{/if}
    </div>
  </div>

  <!-- novalidate: the panel validates itself and shows the messages inline (WCAG 3.3.1). -->
  <form
    bind:this={formEl}
    class="card"
    novalidate
    onsubmit={(e) => {
      e.preventDefault();
      void submit();
    }}
  >
    {#each item.optionGroups as g (g.id)}
      {@const invalid = showErrors && g.required && !selected[g.id]}
      <label class="field">
        <span>{g.label}{g.required ? "" : " (optional)"}</span>
        <select
          required={g.required}
          value={selected[g.id] ?? ""}
          onchange={(e) => choose(g.id, e.currentTarget.value)}
          aria-invalid={invalid ? "true" : undefined}
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
      </label>
    {/each}

    <label class="field">
      <span>Quantity</span>
      <input
        type="number"
        min="1"
        max={maxQuantity}
        step="1"
        bind:value={quantity}
        oninput={() => (quantityClampNotice = "")}
        required
        aria-describedby="item-qty-notice"
      />
    </label>
    <!-- Outside the <label> so the notices aren't part of the field's name, and always mounted so the live
         region announces text changes (WCAG 1.3.1, 4.1.3). -->
    <div id="item-qty-notice" role="status">
      {#if quantityClampNotice}<p class="clamp-notice">{quantityClampNotice}</p>{/if}
      {#if showMadeToOrderNotice}
        <p class="advisory-text">Made to order. Larger quantities may take longer.</p>
      {/if}
    </div>

    <div class="field">
      <label for="item-name">Name</label>
      <input
        id="item-name"
        type="text"
        autocomplete="name"
        bind:value={visitor.name}
        maxlength={NAME_MAX_BYTES}
        required
        aria-invalid={nameError ? "true" : undefined}
        aria-describedby={nameError ? "item-name-err" : undefined}
      />
      {#if nameError}<small class="warn-text" id="item-name-err">{nameError}</small>{/if}
    </div>
    <div class="field">
      <label for="item-email">Email</label>
      <input
        id="item-email"
        type="email"
        autocomplete="email"
        inputmode="email"
        bind:value={visitor.email}
        maxlength={ITEM_ORDER_EMAIL_MAX_BYTES}
        required
        aria-invalid={emailError ? "true" : undefined}
        aria-describedby={emailError ? "item-email-err" : undefined}
      />
      {#if emailError}<small class="warn-text" id="item-email-err">{emailError}</small>{/if}
    </div>
    <div class="field">
      <label for="item-note">Note (optional)</label>
      <textarea
        id="item-note"
        bind:value={note}
        maxlength={NOTE_MAX_BYTES}
        rows="3"
        aria-invalid={noteError ? "true" : undefined}
        aria-describedby={noteError ? "item-note-err" : undefined}
      ></textarea>
      {#if noteError}<small class="warn-text" id="item-note-err">{noteError}</small>{/if}
    </div>

    <div class="card estimate" role="group" aria-labelledby="item-total-label">
      <h2 class="section-label" id="item-total-label">Total</h2>
      <!-- Persistent live region: the settled total is spoken when option or quantity changes (WCAG 4.1.3). -->
      <div aria-live="polite" aria-atomic="true">
        {#if totalCents !== null}
          <p class="price">
            {formatMoney(totalCents, currency)}
            {#if quantity !== null && quantity > 1}
              <span class="per-unit"> &middot; {formatMoney(unitPriceCents, currency)} each</span>
            {/if}
          </p>
        {:else}
          <p class="muted">Enter a quantity to see a price.</p>
        {/if}
      </div>
    </div>

    <!-- Persistent alert container with a plain list inside (WCAG 1.3.1, 4.1.3). -->
    <div role="alert">
      {#if rateLimited}
        <p class="warn-text">Too many requests from this network. Try again in an hour.</p>
      {/if}
      {#if submitErrors.length > 0}
        <ul class="error-list">
          {#each submitErrors as e (e)}<li class="warn-text">{e}</li>{/each}
        </ul>
      {/if}
    </div>

    <p id="order-submit-note" class="submit-note">You won't be charged now. We'll review your order and follow up by email.</p>
    <button type="submit" class="primary" class:busy={submitting} aria-disabled={submitting ? "true" : undefined} aria-describedby="order-submit-note">
      {submitting ? "Submitting…" : "Place order"}
    </button>
  </form>
</div>

<style>
  .panel {
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
  }
  .back { align-self: flex-start; }

  .item-summary {
    display: flex;
    align-items: center;
    gap: var(--space-3);
  }
  .item-summary-text { min-width: 0; }
  .item-summary-text h2 { margin: 0; }
  .item-summary .i-desc { margin: var(--space-1) 0 0; color: var(--muted); font-size: var(--text-sm); }
  .item-summary .thumb {
    width: 4.5rem;
    height: 4.5rem;
    flex-shrink: 0;
    object-fit: cover;
    border-radius: calc(var(--radius) / 2);
    background: var(--surface-2);
  }
  .item-summary .thumb-empty { border: 1px dashed var(--border); }

  form.card {
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
  }

  .estimate { text-align: center; }
  .estimate .muted { color: var(--muted); font-size: var(--text-sm); }
  .estimate .price {
    font-size: var(--text-xl);
    font-weight: 700;
    font-variant-numeric: tabular-nums;
    margin: var(--space-1) 0;
  }
  /* The per-unit price sits inside the same .price line as the total, lighter and muted so the total stays
   * dominant. */
  .per-unit { font-weight: 400; font-size: var(--text-base); color: var(--muted); }

  .clamp-notice { color: var(--warn); font-size: var(--text-sm); margin: 0.3rem 0 0; }
  .error-list { list-style: none; margin: 0; padding: 0; }
  .error-list li { margin: 0.2rem 0; }
  /* In flight: aria-disabled keeps focus on the button, so the busy look is visual only. */
  .submit-note { color: var(--muted); font-size: var(--text-sm); margin: 0; }
  button.busy { opacity: 0.6; cursor: progress; }
</style>
