<script lang="ts">
  import ErrorSummary from "./ErrorSummary.svelte";
  import { canLeave } from "../lib/unsavedGuard";
  import { unsavedForm } from "../lib/unsavedForm.svelte";
  import { onMount } from "svelte";
  import type { EffectiveConfig } from "../lib/engine/index";
  import {
    listItems,
    createItem,
    updateItem,
    deleteItem,
    uploadItemImage,
    type Item,
    type ItemInput,
    type ItemOption,
  } from "../lib/itemsApi";
  import { dollarsToCents, nextSortOrders } from "../lib/itemMath";
  import { validateItemForm } from "../lib/itemFormValidation";
  import { downscaleImage } from "../lib/downscaleImage";
  import { money } from "../lib/format";
  import { mdiArrowLeft, mdiArrowDown, mdiArrowUp } from "@mdi/js";
  import Icon from "./Icon.svelte";
  import LabeledNumber from "./LabeledNumber.svelte";
  import AttributionLine from "./AttributionLine.svelte";
  import DetailTitle from "./DetailTitle.svelte";
  import { announce, focusAfterUpdate } from "../lib/a11y.svelte";
  import { confirmFocus, focusAfterRowRemoval, focusRowOrHeading, keySelector, neighborId } from "../lib/a11yFocus";

  let { cfg }: { cfg: EffectiveConfig } = $props();

  // List

  let items = $state<Item[]>([]);
  let loading = $state(true);
  // Same tolerance as ClientsTab/RequestsTab: a load failure shows a muted note and an empty list.
  let unavailable = $state(false);
  // Set by a failed public-toggle or reorder action (both act from the list, with no form to show an error).
  let listActionError = $state<string | null>(null);

  async function refresh() {
    loading = true;
    const result = await listItems();
    if (result === null) {
      unavailable = true;
      items = [];
    } else {
      unavailable = false;
      items = result;
    }
    loading = false;
  }

  onMount(refresh);

  // Full-record write from the list from the row's own fields, not through the dollars-based edit form: the
  // public toggle and reorder buttons change one field, and Item carries the same fields as ItemInput (minus
  // id/imageFile/timestamps).
  function itemToInput(it: Item, overrides: Partial<ItemInput> = {}): ItemInput {
    return {
      name: it.name,
      description: it.description,
      priceCents: it.priceCents,
      optionLabel: it.optionLabel,
      options: it.options,
      public: it.public,
      stockMode: it.stockMode,
      stockQty: it.stockQty,
      allowBackorder: it.allowBackorder,
      sortOrder: it.sortOrder,
      ...overrides,
    };
  }

  // The checkbox is one-way (checked={it.public}, not bind:checked): the DOM flips on click ahead of the
  // write. On failure `items` is untouched, so reset the checkbox to `it.public` explicitly or it would show
  // the flipped state while the server has the old one.
  async function togglePublic(it: Item, e: Event) {
    const checkbox = e.currentTarget as HTMLInputElement;
    listActionError = null;
    try {
      await updateItem(it.id, itemToInput(it, { public: !it.public }));
      await refresh();
      announce(it.public ? `${it.name} is now hidden from the gallery.` : `${it.name} is now public.`);
    } catch (err) {
      checkbox.checked = it.public;
      listActionError = err instanceof Error ? err.message : "Couldn't update item.";
    }
  }

  // `items` is sorted by sort_order then name (server/items.go's handleList), so adjacent positions are
  // adjacent neighbors; the swap is two sequential PUTs (the server serializes writes on one sqlite
  // connection). A value swap is a no-op when the rows share a sortOrder (reachable via emptyForm's
  // unavailable-list fallback), so when tied the mover `a` keeps its value and neighbor `b` is nudged one
  // step toward `a`'s side, ensuring the order changes.
  let reordering = $state(false);
  async function swapSortOrder(i: number, j: number) {
    const a = items[i];
    const b = items[j];
    if (!a || !b) return;
    if (reordering) return;
    reordering = true;
    listActionError = null;
    const [aNext, bNext] = nextSortOrders(a.sortOrder, b.sortOrder, j > i);
    try {
      await updateItem(a.id, itemToInput(a, { sortOrder: aNext }));
      await updateItem(b.id, itemToInput(b, { sortOrder: bNext }));
      await refresh();
      const pos = items.findIndex((x) => x.id === a.id) + 1;
      announce(`${a.name} moved to position ${pos} of ${items.length}.`);
      // Reordering moves the focused button's row in the DOM, which can drop focus; put it back.
      await focusAfterUpdate(keySelector(`${j < i ? "up" : "down"}-${a.id}`));
    } catch (err) {
      listActionError = err instanceof Error ? err.message : "Couldn't reorder items.";
      // The first PUT may have committed before the second failed, leaving sortOrder values changed; reload
      // so the rendered order matches what persisted.
      await refresh();
    } finally {
      reordering = false;
    }
  }

  function moveUp(i: number) {
    if (i <= 0) return;
    void swapSortOrder(i, i - 1);
  }
  function moveDown(i: number) {
    if (i >= items.length - 1) return;
    void swapSortOrder(i, i + 1);
  }

  // List <-> detail: local component state only; items aren't in App.svelte's routePendingOpen set, so there
  // is no deep-link case.

  // Options and price/delta are edited in dollars and converted to cents once, on save (toInput).
  // price/stockQty/delta are `number | null` like LabeledNumber-bound fields elsewhere: bind:value yields
  // null while the box is empty. Plain `number` would coerce it to 0 in dollarsToCents; validateForm rejects
  // null so clearing a box shows an error instead of saving $0.
  interface OptionForm {
    name: string;
    delta: number | null;
  }
  interface ItemForm {
    name: string;
    description: string;
    price: number | null;
    optionLabel: string;
    options: OptionForm[];
    public: boolean;
    stockMode: "ready_to_print" | "in_stock";
    stockQty: number | null;
    allowBackorder: boolean;
    sortOrder: number;
  }

  function emptyForm(): ItemForm {
    // New items sort after everything already listed; the up/down buttons reorder from there.
    const nextSortOrder = items.reduce((max, it) => Math.max(max, it.sortOrder), -1) + 1;
    return {
      name: "",
      description: "",
      price: 0,
      optionLabel: "",
      options: [],
      public: false,
      stockMode: "ready_to_print",
      stockQty: 0,
      allowBackorder: false,
      sortOrder: nextSortOrder,
    };
  }

  function formFromItem(it: Item): ItemForm {
    return {
      name: it.name,
      description: it.description,
      price: it.priceCents / 100,
      optionLabel: it.optionLabel,
      options: it.options.map((o) => ({ name: o.name, delta: o.deltaCents / 100 })),
      public: it.public,
      stockMode: it.stockMode,
      stockQty: it.stockQty,
      allowBackorder: it.allowBackorder,
      sortOrder: it.sortOrder,
    };
  }

  // Called only after validateForm confirmed price/stockQty/option deltas are non-null; the `?? 0` fallbacks
  // are unreachable, not a looser tolerance.
  function toInput(f: ItemForm): ItemInput {
    const options: ItemOption[] = f.options.map((o) => ({ name: o.name, deltaCents: dollarsToCents(o.delta ?? 0) }));
    return {
      name: f.name,
      description: f.description,
      priceCents: dollarsToCents(f.price ?? 0),
      // The server rejects optionLabel and options set independently (validateItemInput), so an emptied
      // options list always clears the label.
      optionLabel: options.length > 0 ? f.optionLabel : "",
      options,
      public: f.public,
      stockMode: f.stockMode,
      stockQty: f.stockQty ?? 0,
      allowBackorder: f.allowBackorder,
      sortOrder: f.sortOrder,
    };
  }

  // Delegates to itemFormValidation.ts: a non-finite or out-of-range value (e.g. 1e308 pasted into Price) is
  // non-null, and would pass toInput's `?? 0` into roundCents (non-finite maps to 0), silently saving $0.00.
  function validateForm(f: ItemForm): string[] {
    return validateItemForm(f);
  }

  type View = "list" | "detail";
  let view = $state<View>("list");
  // null while composing a not-yet-created item; the loaded item's id once one exists.
  let detailId = $state<string | null>(null);
  let detail = $state<Item | null>(null);
  let form = $state<ItemForm>(emptyForm());
  // The form as last loaded or saved; Save enables only while `form` differs. Compared via JSON.stringify
  // because ItemForm's options array is variable-length and nested (ClientsTab's field-by-field compare suits
  // plain strings).
  let formBaseline = $state<ItemForm>(emptyForm());
  const dirty = $derived(JSON.stringify(form) !== JSON.stringify(formBaseline));
  // Inline feedback for the null case validateForm rejects, like ElectricityTab/FilamentTab's warnFor pattern
  // but computed locally (no engine schema for item fields).
  const priceWarn = $derived(form.price === null ? "Enter a valid price." : "");
  const stockQtyWarn = $derived(form.stockMode === "in_stock" && form.stockQty === null ? "Enter a valid quantity." : "");
  let formErrors = $state<string[]>([]);
  let saving = $state(false);
  let confirmingDelete = $state(false);
  let imageError = $state<string | null>(null);
  let uploadingImage = $state(false);

  function openNew() {
    // emptyForm()'s nextSortOrder derives from the loaded `items`; while `unavailable` that list is empty
    // regardless of what exists, so the value would collide with a real row after recovery. Refuse instead of
    // guessing.
    if (unavailable) {
      listActionError = "Can't add a new item: the item list couldn't be loaded. Reload the page and try again.";
      return;
    }
    openerId = null;
    view = "detail";
    detailId = null;
    detail = null;
    form = emptyForm();
    formBaseline = emptyForm();
    formErrors = [];
    confirmingDelete = false;
    imageError = null;
  }

  // The row that opened the detail view, so "Back to list" can return focus to it (WCAG 2.4.3).
  let openerId: string | null = null;

  function openDetail(id: string) {
    const found = items.find((it) => it.id === id);
    if (!found) return;
    openerId = id;
    view = "detail";
    detailId = found.id;
    detail = found;
    form = formFromItem(found);
    formBaseline = formFromItem(found);
    formErrors = [];
    confirmingDelete = false;
    imageError = null;
  }

  function requestBack() {
    if (canLeave(view === "detail" && dirty)) backToList();
  }

  $effect(() => {
    unsavedForm.current = view === "detail" && dirty;
    return () => {
      unsavedForm.current = false;
    };
  });

  function backToList(focusId: string | null = openerId) {
    view = "list";
    detailId = null;
    detail = null;
    formErrors = [];
    confirmingDelete = false;
    imageError = null;
    void focusRowOrHeading(focusId);
  }

  async function save() {
    if (saving || !dirty) return;
    const nullFieldErrors = validateForm(form);
    if (nullFieldErrors.length > 0) {
      formErrors = nullFieldErrors;
      return;
    }
    saving = true;
    formErrors = [];
    try {
      const input = toInput(form);
      const result = detailId === null ? await createItem(input) : await updateItem(detailId, input);
      detail = result;
      detailId = result.id;
      form = formFromItem(result);
      formBaseline = formFromItem(result); // saved state is the new clean baseline
      await refresh();
      announce(`${result.name} saved.`);
    } catch (err) {
      formErrors = [err instanceof Error ? err.message : "Couldn't save item."];
      announce("Couldn't save the item. See the errors above the form.", "assertive");
    } finally {
      saving = false;
    }
  }

  function askDelete() {
    confirmingDelete = true;
  }
  function cancelDelete() {
    confirmingDelete = false;
  }
  async function confirmDeleteItem() {
    if (!detail) return;
    const { id, name } = detail;
    const next = neighborId(items.map((x) => x.id), id);
    try {
      await deleteItem(id);
      await refresh();
      backToList(next);
      announce(`${name} deleted.`);
    } catch (err) {
      formErrors = [err instanceof Error ? err.message : "Couldn't delete item."];
      confirmingDelete = false;
    }
  }

  function addOption() {
    form.options = [...form.options, { name: "", delta: 0 }];
  }
  function removeOption(i: number) {
    form.options = form.options.filter((_, idx) => idx !== i);
    // The removed row held focus; move to the next option's Remove button, else Add option.
    void focusAfterRowRemoval("remove-option", i, form.options.length, "add-option");
  }

  // Image upload needs a real item id, so it is disabled until the item has been saved once.
  async function handleFileChange(e: Event) {
    const input = e.currentTarget as HTMLInputElement;
    const file = input.files?.[0];
    input.value = ""; // allow re-selecting the same file after a failed upload
    if (!file || detailId === null || uploadingImage) return;
    imageError = null;
    uploadingImage = true;
    try {
      const blob = await downscaleImage(file);
      const updated = await uploadItemImage(detailId, blob);
      detail = updated;
      await refresh();
      announce("Photo uploaded.");
    } catch (err) {
      imageError = err instanceof Error ? err.message : "Couldn't upload image.";
    } finally {
      uploadingImage = false;
    }
  }
</script>

<div class="page">
  {#if view === "list"}
    <div class="head-row">
      <h1 tabindex="-1">Items</h1>
      <button type="button" class="primary" onclick={openNew} disabled={unavailable}>Add item</button>
    </div>

    {#if unavailable}
      <p class="banner-warn" role="alert">Couldn't load items. The server couldn't be reached.</p>
    {/if}
    {#if listActionError}
      <p class="banner-warn" role="alert">{listActionError}</p>
    {/if}

    <div class="card">
      <ul class="plain-list">
        {#if loading}
          <li class="empty-state"><span role="status">Loading…</span></li>
        {:else}
          {#each items as it, i (it.id)}
            <li class="item-row">
              <button type="button" class="row-open" data-row-id={it.id} onclick={() => openDetail(it.id)}>
                {#if it.imageFile}
                  <img class="thumb" src={"item-images/" + it.imageFile} alt="" />
                {:else}
                  <span class="thumb thumb-empty" aria-hidden="true"></span>
                {/if}
                <span class="i-name">{it.name}</span>
                <span class="i-price">{money(it.priceCents, cfg)}</span>
                <span class="i-stock">{it.stockMode === "in_stock" ? `In stock: ${it.stockQty}` : "Ready to print"}</span>
              </button>
              <label class="toggle i-public">
                <input type="checkbox" checked={it.public} onchange={(e) => togglePublic(it, e)} />
                <span>Public<span class="visually-hidden"> ({it.name})</span></span>
              </label>
              <div class="reorder">
                <button type="button" aria-label={`Move ${it.name} up`} aria-disabled={reordering || i === 0} data-focus-key={`up-${it.id}`} onclick={() => moveUp(i)}>
                  <Icon path={mdiArrowUp} size={16} />
                </button>
                <button
                  type="button"
                  aria-label={`Move ${it.name} down`}
                  aria-disabled={reordering || i === items.length - 1}
                  data-focus-key={`down-${it.id}`}
                  onclick={() => moveDown(i)}
                >
                  <Icon path={mdiArrowDown} size={16} />
                </button>
              </div>
            </li>
          {:else}
            <li class="empty-state">No items yet. Add one with the Add item button above.</li>
          {/each}
        {/if}
      </ul>
    </div>
  {:else}
    <div class="head-row">
      <button type="button" class="back-link" onclick={requestBack}><Icon path={mdiArrowLeft} /> Back to list</button>
    </div>

    <div class="card">
      <DetailTitle>{detail ? detail.name : "Add item"}</DetailTitle>
      {#if detail}<AttributionLine createdBy={detail.createdBy} updatedBy={detail.updatedBy} />{/if}

      <ErrorSummary errors={formErrors} />

      <div class="field-rows">
        <label class="field">
          <span>Name</span>
          <!-- Mirrors server/items.go's validateItemInput: name is at most 200 characters. -->
          <input bind:value={form.name} maxlength={200} />
        </label>
        <LabeledNumber label="Price" suffix={cfg.currency.symbol} min={0} max={1000000} step={0.01} bind:value={form.price} warn={priceWarn} />
      </div>
      <label class="field">
        <span>Description</span>
        <!-- Mirrors validateItemInput: description is at most 5000 characters. -->
        <textarea rows="3" maxlength={5000} bind:value={form.description}></textarea>
      </label>

      <div class="field-rows">
        <label class="field">
          <span>Stock</span>
          <select bind:value={form.stockMode}>
            <option value="ready_to_print">Ready to print (made to order)</option>
            <option value="in_stock">In stock (fixed quantity)</option>
          </select>
        </label>
        {#if form.stockMode === "in_stock"}
          <LabeledNumber label="Quantity in stock" min={0} max={1000000} step={1} bind:value={form.stockQty} warn={stockQtyWarn} />
        {/if}
      </div>

      {#if form.stockMode === "in_stock"}
        <label class="toggle">
          <input type="checkbox" bind:checked={form.allowBackorder} />
          <span>Accept orders beyond stock</span>
        </label>
      {/if}

      <label class="toggle">
        <input type="checkbox" bind:checked={form.public} />
        <span>Public (visible in the gallery)</span>
      </label>

      <div class="subcard">
        <h3 class="section-label">Options</h3>
        <label class="field">
          <span>Option label</span>
          <!-- Not server-bound (validateItemInput only requires optionLabel and options be set together);
               100 is a UI-only cap matching option name's limit. -->
          <input bind:value={form.optionLabel} maxlength={100} placeholder="For example, Color" disabled={form.options.length === 0} />
          {#if form.options.length === 0}<small class="field-hint">Add an option first.</small>{/if}
        </label>
        {#each form.options as opt, i (i)}
          {@const deltaWarn = opt.delta === null ? "Enter a valid price adjustment." : ""}
          <div class="option-row">
            <label class="field field-inline">
              <span>Name</span>
              <!-- Mirrors validateItemInput: each option name is at most 100 characters. -->
              <input aria-label={`Option ${i + 1} name`} bind:value={opt.name} maxlength={100} />
            </label>
            <LabeledNumber
              label="Price adjustment"
              suffix={cfg.currency.symbol}
              min={-1000000}
              max={1000000}
              step={0.01}
              bind:value={opt.delta}
              warn={deltaWarn}
            />
            <button type="button" aria-label={`Remove option ${i + 1}`} data-focus-key={`remove-option-${i}`} onclick={() => removeOption(i)}>Remove</button>
          </div>
        {/each}
        <button type="button" data-focus-key="add-option" onclick={addOption}>Add option</button>
      </div>

      <div class="subcard">
        <h3 class="section-label">Photo</h3>
        {#if detail?.imageFile}
          <img class="preview" src={"item-images/" + detail.imageFile} alt={detail.name} />
        {/if}
        {#if detailId === null}
          <p class="hint">Save the item first to add a photo.</p>
        {:else}
          <input type="file" aria-label="Upload photo" accept="image/jpeg,image/png,image/webp" onchange={handleFileChange} aria-disabled={uploadingImage} aria-busy={uploadingImage} />
          <p class="status-line" role="status">{uploadingImage ? "Uploading…" : ""}</p>
          {#if imageError}<p class="banner-warn" role="alert">{imageError}</p>{/if}
        {/if}
      </div>

      <div class="form-actions">
        <button type="button" class="primary" onclick={save} aria-disabled={saving || !dirty}>{saving ? "Saving…" : "Save"}</button>
      </div>

      {#if detail}
        <div class="subcard">
          {#if confirmingDelete}
            <div class="form-actions delete-actions">
              <button type="button" class="danger" onclick={confirmDeleteItem} use:confirmFocus={{ restore: keySelector("delete-item"), cancel: cancelDelete }}>Delete item?</button>
              <button type="button" onclick={cancelDelete}>Cancel</button>
            </div>
          {:else}
            <button type="button" data-focus-key="delete-item" onclick={askDelete}>Delete item</button>
          {/if}
        </div>
      {/if}
    </div>
  {/if}
</div>

<style>
  .head-row h1 { margin: 0; }
  .back-link { display: inline-flex; align-items: center; gap: 0.35rem; }

  li { border-top: 1px solid var(--border); }
  li:first-child { border-top: none; }

  .item-row {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    flex-wrap: wrap;
    padding: 0.4rem 0;
  }
  /* A plain <li onclick> trips svelte-check's a11y_no_noninteractive_element_interactions, so the row has
   * no handlers; .row-open is the real <button>, with the toggle and reorder buttons beside it (controls
   * can't nest in a button). */
  .row-open {
    flex: 1 1 16rem;
    min-width: 0;
    display: flex;
    align-items: center;
    gap: 0.75rem;
    background: none;
    border: none;
    padding: 0.3rem 0;
    text-align: left;
    cursor: pointer;
    flex-wrap: wrap;
  }
  .row-open:hover { background: var(--surface-2); }
  .thumb { width: 48px; height: 48px; border-radius: var(--radius); object-fit: cover; flex-shrink: 0; background: var(--surface-2); }
  .thumb-empty { border: 1px dashed var(--border); }
  .i-name { flex: 1 1 8rem; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 600; }
  .i-price { font-variant-numeric: tabular-nums; min-width: 5rem; }
  .i-stock { color: var(--muted); font-size: var(--text-sm); min-width: 0; flex: 0 1 8rem; }
  input[type="file"] { max-width: 100%; }
  .i-public { min-height: 0; flex-shrink: 0; }
  .reorder { display: flex; gap: 0.2rem; flex-shrink: 0; }

  .card textarea { resize: vertical; }

  .option-row { display: flex; align-items: end; gap: 0.5rem; margin-bottom: 0.4rem; flex-wrap: wrap; }
  .option-row .field { flex: 1 1 100px; min-width: 90px; }

  .preview { display: block; max-width: 200px; max-height: 200px; border-radius: var(--radius); margin-bottom: 0.5rem; object-fit: cover; }

  .delete-actions { justify-content: flex-start; }
</style>
