<script module lang="ts">
  /** Handed to `onordered` on success: a self-contained summary independent of live component state, which
   * PublicQuotePage persists to sessionStorage and renders after a reload with no PublicItem lookup (the
   * item may since be deleted). */
  export interface ItemOrderConfirmation {
    id: string;
    priceCents: number;
    itemName: string;
    optionLabel: string;
    optionName: string;
    quantity: number;
    email: string;
  }
</script>

<script lang="ts">
  import { formatMoney, type CurrencyConfig } from "../lib/engine/index";
  import { itemTotalCents } from "../lib/itemMath";
  import { NAME_MAX_BYTES, NOTE_MAX_BYTES, ITEM_ORDER_EMAIL_MAX_BYTES, utf8ByteLength } from "../lib/publicFormLimits";
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

  // svelte-ignore state_referenced_locally -- initial seed only: item is a
  // remount-on-change prop (PublicQuotePage.svelte wraps this component in
  // {#key orderItem.id}), so reading it once here to pick the default
  // option is intentional, not a missed reactive dependency.
  let optionName = $state(item.options[0]?.name ?? "");
  // number | null like PublicQuotePage's quantity: a cleared number input binds null, not 0 or NaN.
  let quantity = $state<number | null>(1);
  let name = $state("");
  let email = $state("");
  let note = $state("");
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

  const selectedDeltaCents = $derived(item.options.find((o) => o.name === optionName)?.deltaCents ?? 0);
  const unitPriceCents = $derived(item.priceCents + selectedDeltaCents);
  // null while the quantity input is cleared: there is nothing honest to compute (the template shows a dash,
  // not $0.00).
  const totalCents = $derived(quantity === null ? null : itemTotalCents(item.priceCents, selectedDeltaCents, quantity));

  const formErrors = $derived.by(() => {
    const errors: string[] = [];
    if (name.trim() === "") errors.push("Name is required.");
    else if (utf8ByteLength(name) > NAME_MAX_BYTES) errors.push(`Name must be ${NAME_MAX_BYTES} characters or fewer.`);
    if (email.trim() === "") errors.push("Email is required.");
    else if (utf8ByteLength(email) > ITEM_ORDER_EMAIL_MAX_BYTES)
      errors.push(`Email must be ${ITEM_ORDER_EMAIL_MAX_BYTES} characters or fewer.`);
    else if (!email.includes("@")) errors.push("Enter a valid email address.");
    if (utf8ByteLength(note) > NOTE_MAX_BYTES) errors.push(`Note must be ${NOTE_MAX_BYTES} characters or fewer.`);
    if (item.options.length > 0 && optionName === "") errors.push(`Choose ${item.optionLabel || "an option"}.`);
    if (quantity === null || !Number.isInteger(quantity) || quantity < 1 || quantity > maxQuantity) {
      errors.push(`Quantity must be a whole number from 1 to ${maxQuantity}.`);
    }
    return errors;
  });

  async function submit() {
    submitErrors = [];
    rateLimited = false;
    if (formErrors.length > 0 || quantity === null) {
      submitErrors = formErrors;
      return;
    }

    submitting = true;
    const result = await submitItemOrder(
      {
        itemId: item.id,
        optionName,
        quantity,
        name: name.trim(),
        email: email.trim(),
        note: note.trim(),
      },
      item.optionLabel,
    );
    submitting = false;

    if (result.ok && result.id !== undefined && result.priceCents !== undefined) {
      onordered({
        id: result.id,
        priceCents: result.priceCents,
        itemName: item.name,
        optionLabel: item.optionLabel,
        optionName,
        quantity,
        email: email.trim(),
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
    {#if item.imageUrl}
      <img class="thumb" src={item.imageUrl} alt="" />
    {:else}
      <div class="thumb thumb-empty" aria-hidden="true"></div>
    {/if}
    <div class="item-summary-text">
      <h2>{item.name}</h2>
      {#if item.description}<p class="i-desc">{item.description}</p>{/if}
    </div>
  </div>

  <form
    class="card"
    onsubmit={(e) => {
      e.preventDefault();
      void submit();
    }}
  >
    {#if item.options.length > 0}
      <label class="field">
        <span>{item.optionLabel || "Option"}</span>
        <select bind:value={optionName} required>
          {#each item.options as o (o.name)}
            <option value={o.name}
              >{o.name}{o.deltaCents !== 0
                ? ` (${o.deltaCents > 0 ? "+" : ""}${formatMoney(o.deltaCents, currency)})`
                : ""}</option
            >
          {/each}
        </select>
      </label>
    {/if}

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
      />
      {#if quantityClampNotice}<p class="clamp-notice" role="status">{quantityClampNotice}</p>{/if}
      {#if showMadeToOrderNotice}
        <p class="advisory-text" role="status">Made to order. Larger quantities may take longer.</p>
      {/if}
    </label>

    <label class="field">
      <span>Name</span>
      <input type="text" bind:value={name} maxlength={NAME_MAX_BYTES} required />
    </label>
    <label class="field">
      <span>Email</span>
      <input type="email" bind:value={email} maxlength={ITEM_ORDER_EMAIL_MAX_BYTES} required />
    </label>
    <label class="field">
      <span>Note (optional)</span>
      <textarea bind:value={note} maxlength={NOTE_MAX_BYTES} rows="3"></textarea>
    </label>

    <div class="card estimate">
      <p class="section-label">Total</p>
      {#if totalCents !== null}
        <p class="price">
          {formatMoney(totalCents, currency)}
          {#if quantity !== null && quantity > 1}
            <span class="per-unit"> &middot; {formatMoney(unitPriceCents, currency)} each</span>
          {/if}
        </p>
      {:else}
        <p class="muted">Pick an option and quantity to see a price.</p>
      {/if}
    </div>

    {#if rateLimited}
      <p class="warn-text" role="alert">Too many requests from this network. Try again in an hour.</p>
    {/if}
    {#if submitErrors.length > 0}
      <div role="alert">
        {#each submitErrors as e (e)}<p class="warn-text">{e}</p>{/each}
      </div>
    {/if}

    <button type="submit" class="primary" disabled={submitting}>
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
</style>
