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
    uploadOptionImage,
    deleteOptionImage,
    ConflictError,
    type Item,
    type ItemInput,
  } from "../lib/itemsApi";
  import { registerPageSave } from "../lib/pageShortcuts.svelte";
  import { filterItems } from "../lib/itemFilter";
  import { moveItem } from "../lib/itemOptions";
  import { MAX_OPTION_GROUPS, MAX_OPTIONS_PER_GROUP } from "../lib/itemFormValidation";
  import { dollarsToCents, nextSortOrders } from "../lib/itemMath";
  import { validateItemForm } from "../lib/itemFormValidation";
  import { downscaleImage } from "../lib/downscaleImage";
  import { money } from "../lib/format";
  import { mdiArrowDown, mdiArrowUp } from "@mdi/js";
  import Icon from "./Icon.svelte";
  import LabeledNumber from "./LabeledNumber.svelte";
  import AttributionLine from "./AttributionLine.svelte";
  import PageHeader from "./ui/PageHeader.svelte";
  import Card from "./ui/Card.svelte";
  import Field from "./ui/Field.svelte";
  import FormGrid from "./ui/FormGrid.svelte";
  import Fieldset from "./ui/Fieldset.svelte";
  import DataTable, { type DataColumn } from "./ui/DataTable.svelte";
  import ConfirmInline from "./ui/ConfirmInline.svelte";
  import RowEditor from "./ui/RowEditor.svelte";
  import { focusAfterRowRemoved, rowMenuKey } from "./ui/rowEditor";
  import ItemCategories from "./ItemCategories.svelte";
  import { listCategories, type Category } from "../lib/categoriesApi";
  import { announce, focusAfterUpdate } from "../lib/a11y.svelte";
  import { focusRowOrHeading, keySelector, neighborId, refocusIfLost } from "../lib/a11yFocus";

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

  // Categories (D17): the item editor's Category select and the manager card share this list. `categoriesLoaded`
  // is false until the first answer; a failed load leaves the select with None only and the manager disabled.
  let categories = $state<Category[]>([]);
  let categoriesUnavailable = $state(false);
  async function refreshCategories() {
    const result = await listCategories();
    if (result === null) {
      categoriesUnavailable = true;
    } else {
      categoriesUnavailable = false;
      categories = result;
    }
  }
  // After a category write: the category list and the items (deleting a category unlinks its items).
  async function reloadCatalog() {
    await Promise.all([refreshCategories(), refresh()]);
  }

  onMount(() => {
    void refreshCategories();
    void refresh();
  });

  // Full-record write from the list from the row's own fields, not through the dollars-based edit form: the
  // public toggle and reorder buttons change one field, and Item carries the same fields as ItemInput (minus
  // id/imageFile/timestamps).
  function itemToInput(it: Item, overrides: Partial<ItemInput> = {}): ItemInput {
    return {
      name: it.name,
      description: it.description,
      priceCents: it.priceCents,
      optionGroups: it.optionGroups.map((g) => ({
        id: g.id,
        label: g.label,
        required: g.required,
        options: g.options.map((o) => ({ id: o.id, name: o.name, deltaCents: o.deltaCents })),
      })),
      public: it.public,
      stockMode: it.stockMode,
      stockQty: it.stockQty,
      allowBackorder: it.allowBackorder,
      sortOrder: it.sortOrder,
      version: it.version,
      ...overrides,
    };
  }

  // The checkbox is one-way (checked={it.public}, not bind:checked): the DOM flips on click ahead of the
  // write. On failure `items` is untouched, so reset the checkbox to `it.public` explicitly or it would show
  // the flipped state while the server has the old one.
  async function togglePublic(it: Item, e: Event) {
    const checkbox = e.currentTarget as HTMLInputElement;
    listActionError = null;
    listNotice = null;
    try {
      const saved = await updateItem(it.id, itemToInput(it, { public: !it.public }));
      await refresh();
      const text = it.public ? `${it.name} is now hidden from the shop.` : `${it.name} is now shown in the shop.`;
      listNotice = { text, undoId: saved.id, undoPublic: it.public };
      announce(text);
    } catch (err) {
      checkbox.checked = it.public;
      listActionError = err instanceof ConflictError ? `${err.message} Reload the list and try again.` : err instanceof Error ? err.message : "Couldn't update item.";
      if (err instanceof ConflictError) await refresh();
    }
  }

  // The list's Public checkbox flips visibility in one click, so the result is stated with an Undo.
  let listNotice = $state<{ text: string; undoId: string; undoPublic: boolean } | null>(null);
  async function undoPublic() {
    const n = listNotice;
    if (!n) return;
    const it = items.find((x) => x.id === n.undoId);
    listNotice = null;
    if (!it) return;
    listActionError = null;
    try {
      await updateItem(it.id, itemToInput(it, { public: n.undoPublic }));
      await refresh();
      announce(n.undoPublic ? `${it.name} is shown in the shop again.` : `${it.name} is hidden from the shop again.`);
    } catch (err) {
      listActionError = err instanceof Error ? err.message : "Couldn't undo.";
    }
  }

  // List filter (Ctrl/Cmd+K focuses it; App.svelte's FILTER_IDS).
  let filter = $state("");
  const visibleItems = $derived(filterItems(items, filter));

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
  // key is a stable identity for {#each} and focus targets: the server id for a saved row, "new-N" for one
  // added in this form. id is "" until the first save.
  interface OptionForm {
    key: string;
    id: string;
    name: string;
    delta: number | null;
  }
  interface GroupForm {
    key: string;
    id: string;
    label: string;
    required: boolean;
    options: OptionForm[];
  }
  interface ItemForm {
    name: string;
    description: string;
    price: number | null;
    groups: GroupForm[];
    public: boolean;
    stockMode: "ready_to_print" | "in_stock";
    stockQty: number | null;
    allowBackorder: boolean;
    sortOrder: number;
    /** "" for none. */
    categoryId: string;
    featured: boolean;
  }

  function emptyForm(): ItemForm {
    // New items sort after everything already listed; the up/down buttons reorder from there.
    const nextSortOrder = items.reduce((max, it) => Math.max(max, it.sortOrder), -1) + 1;
    return {
      name: "",
      description: "",
      price: 0,
      groups: [],
      public: false,
      stockMode: "ready_to_print",
      stockQty: 0,
      allowBackorder: false,
      sortOrder: nextSortOrder,
      categoryId: "",
      featured: false,
    };
  }

  function formFromItem(it: Item): ItemForm {
    return {
      name: it.name,
      description: it.description,
      price: it.priceCents / 100,
      groups: it.optionGroups.map((g) => ({
        key: g.id,
        id: g.id,
        label: g.label,
        required: g.required,
        options: g.options.map((o) => ({ key: o.id, id: o.id, name: o.name, delta: o.deltaCents / 100 })),
      })),
      public: it.public,
      stockMode: it.stockMode,
      stockQty: it.stockQty,
      allowBackorder: it.allowBackorder,
      sortOrder: it.sortOrder,
      categoryId: it.categoryId,
      featured: it.featured,
    };
  }

  // Called only after validateForm confirmed price/stockQty/option deltas are non-null; the `?? 0` fallbacks
  // are unreachable, not a looser tolerance.
  function toInput(f: ItemForm): ItemInput {
    return {
      name: f.name,
      description: f.description,
      priceCents: dollarsToCents(f.price ?? 0),
      // Rows without an id are new; the server assigns ids and keeps the ids and photos of the rest.
      optionGroups: f.groups.map((g) => ({
        id: g.id || undefined,
        label: g.label,
        required: g.required,
        options: g.options.map((o) => ({ id: o.id || undefined, name: o.name, deltaCents: dollarsToCents(o.delta ?? 0) })),
      })),
      public: f.public,
      stockMode: f.stockMode,
      stockQty: f.stockQty ?? 0,
      allowBackorder: f.allowBackorder,
      sortOrder: f.sortOrder,
      categoryId: f.categoryId,
      featured: f.featured,
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
  // "Saved." status under the Save button; cleared by the next edit. `hidden` adds the Show in shop action.
  let savedStatus = $state<{ text: string; hidden: boolean } | null>(null);
  // True after a 409: the form shows the Reload / Overwrite choice instead of a plain error.
  let conflict = $state(false);
  $effect(() => {
    if (dirty && savedStatus) savedStatus = null;
  });
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
    optionImageError = null;
    savedStatus = null;
    conflict = false;
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
    savedStatus = null;
    conflict = false;
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

  // `overwrite` resends the form on top of the newest stored version after a 409.
  async function save(overwrite = false) {
    if (saving || (!dirty && !overwrite)) return;
    const nullFieldErrors = validateForm(form);
    if (nullFieldErrors.length > 0) {
      formErrors = nullFieldErrors;
      return;
    }
    saving = true;
    formErrors = [];
    conflict = false;
    try {
      const input = toInput(form);
      let result: Item;
      if (detailId === null) {
        result = await createItem(input);
      } else {
        if (overwrite) {
          const latest = (await listItems())?.find((x) => x.id === detailId);
          if (latest) detail = latest;
        }
        result = await updateItem(detailId, { ...input, version: detail?.version });
      }
      const wasNew = detailId === null;
      detail = result;
      detailId = result.id;
      form = formFromItem(result);
      formBaseline = formFromItem(result); // saved state is the new clean baseline
      await refresh();
      savedStatus = {
        text: result.public ? "Saved." : "Saved. Hidden from the shop.",
        hidden: !result.public,
      };
      announce(result.public ? `${result.name} saved.` : `${result.name} saved. Hidden from the shop.`);
      if (wasNew) openerId = result.id;
      // The form was rebuilt from the saved item, which can drop the focused field's element: put focus back on
      // Save when it fell to <body> (A11).
      void refocusIfLost(keySelector("save-item"));
    } catch (err) {
      if (err instanceof ConflictError) {
        conflict = true;
        formErrors = [];
        void focusAfterUpdate("#item-conflict");
      } else {
        formErrors = [err instanceof Error ? err.message : "Couldn't save item."];
      }
    } finally {
      saving = false;
    }
  }

  // Throws away the form and loads the stored item again.
  async function reloadFromServer() {
    if (detailId === null) return;
    const latest = (await listItems())?.find((x) => x.id === detailId);
    conflict = false;
    if (!latest) {
      formErrors = ["That item no longer exists."];
      return;
    }
    detail = latest;
    form = formFromItem(latest);
    formBaseline = formFromItem(latest);
    savedStatus = null;
    announce("Reloaded the latest version.");
  }

  // "Show in shop" on the Saved notice: turns Public on and saves.
  async function showInShop() {
    form.public = true;
    await save();
  }

  // Ctrl/Cmd+S on the detail view (App.svelte's handler).
  $effect(() => {
    if (view !== "detail") return;
    return registerPageSave(() => void save());
  });

  function askDelete() {
    confirmingDelete = true;
  }
  function cancelDelete() {
    confirmingDelete = false;
  }
  let deleting = $state(false);
  async function confirmDeleteItem() {
    if (!detail || deleting) return;
    deleting = true;
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
    } finally {
      deleting = false;
    }
  }

  let newKeySeq = 0;
  function newKey(): string {
    return `new-${++newKeySeq}`;
  }

  function addGroup() {
    if (form.groups.length >= MAX_OPTION_GROUPS) return;
    const group: GroupForm = { key: newKey(), id: "", label: "", required: true, options: [{ key: newKey(), id: "", name: "", delta: 0 }] };
    form.groups = [...form.groups, group];
    void focusAfterUpdate(keySelector(`group-label-${group.key}`));
  }
  // Removing a saved group or choice asks first (A12): unsaved edits can be abandoned, but a stored choice is
  // gone for good once the item is saved. A row added in this session has no id yet and goes at once.
  let confirmingRemove = $state<string | null>(null);
  function askRemoveGroup(gi: number) {
    const g = form.groups[gi];
    if (!g) return;
    if (g.id === "") removeGroup(gi);
    else confirmingRemove = groupRowKey(g);
  }
  function askRemoveOption(gi: number, i: number) {
    const g = form.groups[gi];
    const o = g?.options[i];
    if (!g || !o) return;
    if (o.id === "") removeOption(gi, i);
    else confirmingRemove = optionRowKey(g, o);
  }
  function removeGroup(gi: number) {
    confirmingRemove = null;
    const label = form.groups[gi]?.label.trim() || `group ${gi + 1}`;
    form.groups = form.groups.filter((_, idx) => idx !== gi);
    announce(`Removed ${label}.`);
    // The removed group held focus; move to the next group's ⋯ button, else Add group.
    void focusAfterRowRemoved(form.groups.map((g) => groupRowKey(g)), gi, "add-group");
  }
  function moveGroup(gi: number, delta: -1 | 1) {
    const moved = moveItem(form.groups, gi, delta);
    if (moved === form.groups) return;
    form.groups = moved;
  }
  function addOption(gi: number) {
    const g = form.groups[gi];
    if (!g || g.options.length >= MAX_OPTIONS_PER_GROUP) return;
    const opt: OptionForm = { key: newKey(), id: "", name: "", delta: 0 };
    g.options = [...g.options, opt];
    void focusAfterUpdate(keySelector(`option-name-${g.key}-${opt.key}`));
  }
  function removeOption(gi: number, i: number) {
    const g = form.groups[gi];
    if (!g) return;
    confirmingRemove = null;
    g.options = g.options.filter((_, idx) => idx !== i);
    announce("Choice removed.");
    // The removed row held focus; move to the next choice's ⋯ button, else this group's Add choice.
    void focusAfterRowRemoved(g.options.map((o) => optionRowKey(g, o)), i, `add-option-${g.key}`);
  }
  function moveOption(gi: number, i: number, delta: -1 | 1) {
    const g = form.groups[gi];
    if (!g) return;
    const moved = moveItem(g.options, i, delta);
    if (moved === g.options) return;
    g.options = moved;
  }
  // Row keys: stable per row, so the ⋯ button follows its row when it moves.
  const groupRowKey = (g: GroupForm) => `group-${g.key}`;
  const optionRowKey = (g: GroupForm, o: OptionForm) => `option-${g.key}-${o.key}`;
  // What a row is called to assistive technology: its place in the list, then its name.
  const groupLabel = (g: GroupForm, gi: number) => `Option group ${gi + 1}: ${g.label.trim() || "unnamed"}`;
  // Never equal to the choice field's own name ("Group 1 choice 1"), so the row and its field stay distinct.
  const optionLabel = (g: GroupForm, gi: number, o: OptionForm, i: number) => `Group ${gi + 1} choice ${i + 1}: ${o.name.trim() || "unnamed"}`;

  // Option photos live on the saved item: `detail` carries each option's imageFile, so the controls below
  // read it from there (by option id) rather than from the form, which would make a photo change look like
  // an unsaved edit.
  function optionImageFile(optionId: string): string {
    if (!detail || !optionId) return "";
    for (const g of detail.optionGroups) {
      const o = g.options.find((x) => x.id === optionId);
      if (o) return o.imageFile;
    }
    return "";
  }
  let optionImageError = $state<string | null>(null);
  let optionImageBusy = $state<string | null>(null);

  async function handleOptionFile(e: Event, o: OptionForm) {
    const input = e.currentTarget as HTMLInputElement;
    const file = input.files?.[0];
    input.value = "";
    if (!file || detailId === null || !o.id || optionImageBusy) return;
    optionImageError = null;
    optionImageBusy = o.id;
    try {
      detail = await uploadOptionImage(detailId, o.id, await downscaleImage(file));
      await refresh();
      announce(`Photo uploaded for ${o.name.trim() || "the choice"}.`);
    } catch (err) {
      optionImageError = err instanceof Error ? err.message : "Couldn't upload image.";
    } finally {
      optionImageBusy = null;
    }
  }
  async function removeOptionPhoto(o: OptionForm) {
    if (detailId === null || !o.id || optionImageBusy) return;
    optionImageError = null;
    optionImageBusy = o.id;
    try {
      detail = await deleteOptionImage(detailId, o.id);
      await refresh();
      announce(`Photo removed from ${o.name.trim() || "the choice"}.`);
      await focusAfterUpdate(keySelector(`option-photo-${o.key}`));
    } catch (err) {
      optionImageError = err instanceof Error ? err.message : "Couldn't remove the photo.";
    } finally {
      optionImageBusy = null;
    }
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

  const COLUMNS: DataColumn[] = [
    { key: "item", label: "Item" },
    { key: "price", label: "Price", num: true },
    { key: "stock", label: "Stock" },
    { key: "category", label: "Category" },
    { key: "public", label: "In the shop" },
    { key: "order", label: "Order" },
  ];

  function categoryName(id: string): string {
    return id === "" ? "" : (categories.find((c) => c.id === id)?.name ?? "");
  }
</script>

<div class="page">
  {#if view === "list"}
    <PageHeader title="Shop items" actionsFill>
      {#snippet actions()}
        <button type="button" class="primary" onclick={openNew} disabled={unavailable}>Add shop item</button>
      {/snippet}
    </PageHeader>

    {#if unavailable}
      <p class="banner-warn" role="alert">Couldn't load items. The server couldn't be reached.</p>
    {/if}
    {#if listActionError}
      <p class="banner-warn" role="alert">{listActionError}</p>
    {/if}
    {#if listNotice}
      <p class="banner-muted notice">{listNotice.text} <button type="button" class="quiet" onclick={undoPublic}>Undo</button></p>
    {/if}

    <DataTable label="Shop items" columns={COLUMNS}>
      {#snippet filters()}
        {#if items.length > 0}
          <Field id="items-filter" label="Filter shop items" size="l">
            {#snippet children(f)}
              <input id={f.id} type="search" bind:value={filter} autocomplete="off" placeholder="Name or description" />
            {/snippet}
          </Field>
          <p class="hint" role="status">{filter.trim() === "" ? "" : `${visibleItems.length} of ${items.length} shop items match.`}</p>
        {/if}
      {/snippet}
      {#if loading}
        <tr><td colspan="6" class="empty-state"><span role="status">Loading…</span></td></tr>
      {:else}
        {#each visibleItems as it (it.id)}
          {@const i = items.indexOf(it)}
          <tr class="item-row">
            <td class="c-item">
              <button type="button" class="row-open" data-row-id={it.id} onclick={() => openDetail(it.id)}>
                {#if it.imageFile}
                  <img class="thumb" src={"item-images/" + it.imageFile} alt="" />
                {:else}
                  <span class="thumb thumb-empty" aria-hidden="true"></span>
                {/if}
                <span class="i-name">{it.name}</span>
                {#if it.featured}<span class="pill">Featured</span>{/if}
              </button>
            </td>
            <td class="num c-price">{money(it.priceCents, cfg)}</td>
            <td class="c-stock">{it.stockMode === "in_stock" ? `In stock: ${it.stockQty}` : "Ready to print"}</td>
            <td class="c-category">{categoryName(it.categoryId)}</td>
            <td class="c-public">
              <label class="toggle i-public">
                <input type="checkbox" checked={it.public} onchange={(e) => togglePublic(it, e)} />
                <span>Public<span class="visually-hidden"> ({it.name}, shown in the shop)</span></span>
              </label>
            </td>
            <td class="c-order">
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
            </td>
          </tr>
        {:else}
          <tr><td colspan="6" class="empty-state">{items.length === 0 ? "No shop items yet. Add one with the Add item button above." : "No shop items match that filter."}</td></tr>
        {/each}
      {/if}
    </DataTable>

    <ItemCategories {categories} onreload={reloadCatalog} available={!categoriesUnavailable} />
  {:else}
    <PageHeader record title={detail ? detail.name : "Add shop item"} onback={requestBack}>
      {#snippet subtitle()}{#if detail}<AttributionLine createdBy={detail.createdBy} updatedBy={detail.updatedBy} />{/if}{/snippet}
    </PageHeader>

    <ErrorSummary errors={formErrors} />
    {#if conflict}
      <div class="banner-warn" role="alert" id="item-conflict" tabindex="-1">
        <p>This was changed in another tab or by another user.</p>
        <div class="form-actions">
          <button type="button" onclick={reloadFromServer}>Reload</button>
          <button type="button" onclick={() => save(true)}>Overwrite</button>
        </div>
      </div>
    {/if}

    <Card>
<div class="stack">
<h2>Details</h2>
      <FormGrid>
        <Field id="item-name" label="Name" size="m">
          {#snippet children(f)}
            <!-- Mirrors server/items.go's validateItemInput: name is at most 200 characters. -->
            <input id={f.id} bind:value={form.name} maxlength={200} />
          {/snippet}
        </Field>
        <LabeledNumber label="Price" suffix={cfg.currency.symbol} min={0} max={1000000} step={0.01} bind:value={form.price} warn={priceWarn} />
        <Field id="item-category" label="Category" size="m" hint={categoriesUnavailable ? "Categories couldn't be loaded." : undefined}>
          {#snippet children(f)}
            <select id={f.id} bind:value={form.categoryId} aria-describedby={f.describedby}>
              <option value="">None</option>
              {#each categories as c (c.id)}
                <option value={c.id}>{c.name}{c.hidden ? " (hidden)" : ""}</option>
              {/each}
              {#if form.categoryId !== "" && !categories.some((c) => c.id === form.categoryId)}
                <option value={form.categoryId}>Unknown category</option>
              {/if}
            </select>
          {/snippet}
        </Field>
      </FormGrid>
      <Field id="item-description" label="Description">
        {#snippet children(f)}
          <!-- Mirrors validateItemInput: description is at most 5000 characters. -->
          <textarea id={f.id} rows="3" maxlength={5000} bind:value={form.description}></textarea>
        {/snippet}
      </Field>

      <Fieldset legend="Availability">
        <FormGrid>
          <Field id="item-stock-mode" label="Stock" size="m">
            {#snippet children(f)}
              <select id={f.id} bind:value={form.stockMode}>
                <option value="ready_to_print">Ready to print (made to order)</option>
                <option value="in_stock">In stock (fixed quantity)</option>
              </select>
            {/snippet}
          </Field>
          {#if form.stockMode === "in_stock"}
            <LabeledNumber label="Quantity in stock" min={0} max={1000000} step={1} bind:value={form.stockQty} warn={stockQtyWarn} />
          {/if}
        </FormGrid>
        {#if form.stockMode === "in_stock"}
          <label class="toggle">
            <input type="checkbox" bind:checked={form.allowBackorder} />
            <span>Accept orders beyond stock</span>
          </label>
        {/if}
      </Fieldset>

      <Fieldset legend="In the shop">
        <label class="toggle">
          <input type="checkbox" bind:checked={form.public} />
          <span>Public (visible in the shop)</span>
        </label>
        <label class="toggle">
          <input type="checkbox" bind:checked={form.featured} />
          <span>Featured (shown first in its category and in All)</span>
        </label>
      </Fieldset>
    </div>
</Card>

    <Card>
<div class="stack">
<h2>Options</h2>
      <p class="hint">
        Each group is one choice the customer makes, such as Color or Size. When "Customer must choose one" is on, the customer must pick a choice before ordering;
        when it is off, the group starts on None. Groups and choices show in the order listed here.
      </p>
      <div class="rows">
        {#each form.groups as g, gi (g.key)}
          <RowEditor
            tone="inset"
            label={groupLabel(g, gi)}
            name={g.label.trim() || `Group ${gi + 1}`}
            rowKey={groupRowKey(g)}
            position={{ index: gi, count: form.groups.length }}
            onmoveup={() => moveGroup(gi, -1)}
            onmovedown={() => moveGroup(gi, 1)}
            onremove={() => askRemoveGroup(gi)}
            removeLabel="Remove group"
          >
            {#snippet notice()}
              {#if confirmingRemove === groupRowKey(g)}
                <ConfirmInline
                  confirmLabel={`Yes, remove ${g.label.trim() || `group ${gi + 1}`}`}
                  danger
                  restore={keySelector(rowMenuKey(groupRowKey(g)))}
                  onconfirm={() => removeGroup(gi)}
                  oncancel={() => (confirmingRemove = null)}
                >It and its {g.options.length === 1 ? "choice" : `${g.options.length} choices`} are deleted when you save the item.</ConfirmInline>
              {/if}
            {/snippet}
            {#snippet fields()}
              <FormGrid>
                <Field id={`group-name-${g.key}`} label="Group name" size="m">
                  {#snippet children(f)}
                    <!-- Mirrors validateOptionGroups: a group label is at most 100 characters. -->
                    <input id={f.id} bind:value={g.label} maxlength={100} placeholder="For example, Color" data-focus-key={`group-label-${g.key}`} />
                  {/snippet}
                </Field>
                <label class="toggle cell-end">
                  <input type="checkbox" bind:checked={g.required} />
                  <span>Customer must choose one</span>
                </label>
              </FormGrid>
            {/snippet}
            {#snippet below()}
              {#each g.options as opt, i (opt.key)}
                {@const deltaWarn = opt.delta === null ? "Enter a valid price adjustment." : ""}
                {@const photo = optionImageFile(opt.id)}
                <RowEditor
                  label={optionLabel(g, gi, opt, i)}
                  name={opt.name.trim() || `Choice ${i + 1}`}
                  rowKey={optionRowKey(g, opt)}
                  position={{ index: i, count: g.options.length }}
                  onmoveup={() => moveOption(gi, i, -1)}
                  onmovedown={() => moveOption(gi, i, 1)}
                  onremove={() => askRemoveOption(gi, i)}
                >
                  {#snippet notice()}
                    {#if confirmingRemove === optionRowKey(g, opt)}
                      <ConfirmInline
                        confirmLabel={`Yes, remove ${opt.name.trim() || `choice ${i + 1}`}`}
                        danger
                        restore={keySelector(rowMenuKey(optionRowKey(g, opt)))}
                        onconfirm={() => removeOption(gi, i)}
                        oncancel={() => (confirmingRemove = null)}
                      >It is deleted when you save the item.</ConfirmInline>
                    {/if}
                    {#if opt.id && detailId !== null}
                      <div class="option-photo">
                        {#if photo}
                          <img class="option-thumb" src={"item-images/" + photo} alt={`Photo of ${opt.name.trim() || "choice"}`} />
                        {/if}
                        <label class="file-label">
                          <span class="visually-hidden">{photo ? "Replace" : "Add"} photo for group {gi + 1} choice {i + 1}{opt.name.trim() ? ` (${opt.name.trim()})` : ""}</span>
                          <input
                            type="file"
                            accept="image/jpeg,image/png,image/webp"
                            data-focus-key={`option-photo-${opt.key}`}
                            onchange={(e) => handleOptionFile(e, opt)}
                            disabled={optionImageBusy !== null}
                            aria-busy={optionImageBusy === opt.id}
                          />
                        </label>
                        {#if photo}
                          <button type="button" aria-label={`Remove photo from group ${gi + 1} choice ${i + 1}`} onclick={() => removeOptionPhoto(opt)}>Remove photo</button>
                        {/if}
                      </div>
                    {/if}
                  {/snippet}
                  {#snippet fields()}
                    <FormGrid>
                      <Field id={`option-name-${g.key}-${opt.key}`} label="Choice" size="m">
                        {#snippet children(f)}
                          <!-- Mirrors validateOptionGroups: each option name is at most 100 characters. -->
                          <input
                            id={f.id}
                            aria-label={`Group ${gi + 1} choice ${i + 1}`}
                            bind:value={opt.name}
                            maxlength={100}
                            data-focus-key={`option-name-${g.key}-${opt.key}`}
                          />
                        {/snippet}
                      </Field>
                      <LabeledNumber
                        label="Price adjustment"
                        suffix={cfg.currency.symbol}
                        min={-1000000}
                        max={1000000}
                        step={0.01}
                        bind:value={opt.delta}
                        warn={deltaWarn}
                      />
                    </FormGrid>
                  {/snippet}
                </RowEditor>
              {/each}
              {#if detailId === null || g.options.some((o) => o.id === "")}
                <small class="field-hint">Save the item first to add a photo to a new choice.</small>
              {/if}
              <div class="add-line">
                <button type="button" data-focus-key={`add-option-${g.key}`} aria-disabled={g.options.length >= MAX_OPTIONS_PER_GROUP} onclick={() => addOption(gi)}>Add choice</button>
                {#if g.options.length >= MAX_OPTIONS_PER_GROUP}<small class="field-hint">A group has at most {MAX_OPTIONS_PER_GROUP} choices.</small>{/if}
              </div>
            {/snippet}
          </RowEditor>
        {/each}
      </div>
      {#if optionImageError}<p class="banner-warn" role="alert">{optionImageError}</p>{/if}
      <div class="add-line">
        <button type="button" data-focus-key="add-group" aria-disabled={form.groups.length >= MAX_OPTION_GROUPS} onclick={addGroup}>Add group</button>
        {#if form.groups.length >= MAX_OPTION_GROUPS}<small class="field-hint">An item has at most {MAX_OPTION_GROUPS} groups.</small>{/if}
      </div>
    </div>
</Card>

    <Card>
<div class="stack">
<h2>Photo</h2>
      {#if detail?.imageFile}
        <img class="preview" src={"item-images/" + detail.imageFile} alt={detail.name} />
      {/if}
      {#if detailId === null}
        <p class="hint">Save the item first to add a photo.</p>
      {:else}
        <input type="file" aria-label="Upload photo" accept="image/jpeg,image/png,image/webp" onchange={handleFileChange} disabled={uploadingImage} aria-busy={uploadingImage} />
        <p class="status-line" role="status">{uploadingImage ? "Uploading…" : ""}</p>
        {#if imageError}<p class="banner-warn" role="alert">{imageError}</p>{/if}
      {/if}
    </div>
</Card>

    <div class="save-area">
      <div class="form-actions start">
        <button type="button" class="primary" data-focus-key="save-item" onclick={() => save()} aria-disabled={saving || !dirty}>{saving ? "Saving…" : "Save"}</button>
      </div>
      <p class="status-line saved-status" role="status" data-testid="item-saved">
        {#if savedStatus}
          {savedStatus.text}
          {#if savedStatus.hidden}<button type="button" class="quiet" onclick={showInShop}>Show in shop</button>{/if}
        {/if}
      </p>
    </div>

    {#if detail}
      <Card tone="inset">
        {#if confirmingDelete}
          <div class="form-actions start">
            <ConfirmInline
              confirmLabel={`Yes, delete ${detail.name}`}
              danger
              busy={deleting}
              restore={keySelector("delete-item")}
              onconfirm={confirmDeleteItem}
              oncancel={cancelDelete}
            >Deleting a shop item is permanent. Past orders keep their own copy.</ConfirmInline>
          </div>
        {:else}
          <div class="form-actions start">
            <button type="button" data-focus-key="delete-item" onclick={askDelete}>Delete shop item</button>
          </div>
        {/if}
      </Card>
    {/if}
  {/if}
</div>

<style>
  /* .page, .banner-warn, .form-actions and .toggle come from theme.css. DataTable owns the scroll region and the
   * sticky header; the row layout is this screen's. */
  td.empty-state { text-align: center; }
  .notice { display: flex; align-items: center; gap: var(--space-2); flex-wrap: wrap; }

  /* The click-to-open affordance: the item's name and photo are one button (controls can't nest in a button,
   * so the public toggle and the reorder buttons sit in their own cells). */
  .row-open {
    display: flex;
    align-items: center;
    gap: var(--space-3);
    max-width: 100%;
    background: none;
    border: none;
    padding: var(--space-1) 0;
    text-align: left;
    cursor: pointer;
  }
  .row-open:hover .i-name { text-decoration: underline; }
  .thumb { inline-size: var(--space-12); block-size: var(--space-12); border-radius: var(--radius); object-fit: cover; flex: none; background: var(--surface-2); }
  .thumb-empty { border: 1px dashed var(--border); }
  .i-name { min-width: 0; overflow-wrap: anywhere; font-weight: 600; color: var(--accent-text); text-align: left; }
  .c-stock, .c-category { color: var(--muted); font-size: var(--type-secondary); }
  .i-public { min-height: 0; }
  .reorder { display: flex; gap: var(--space-1); }
  .pill { white-space: nowrap; }

  /* Phones: each item becomes a card: photo and name on top, price, stock and category, then the toggle and the
   * order buttons. */
  @media (max-width: 600px) {
    tr.item-row {
      display: grid;
      grid-template-columns: minmax(0, 1fr) auto;
      grid-template-areas: "item item" "stock price" "category category" "public order";
      gap: var(--space-1) var(--space-3);
      align-items: center;
      padding: var(--space-3) 0;
      border-bottom: 1px solid var(--border);
    }
    .c-item { grid-area: item; }
    .c-price { grid-area: price; text-align: right; font-weight: 600; }
    .c-stock { grid-area: stock; }
    .c-category { grid-area: category; }
    .c-category:empty { display: none; }
    .c-public { grid-area: public; }
    .c-order { grid-area: order; }
  }

  .rows { display: flex; flex-direction: column; gap: var(--gap); }
  .cell-end { align-self: end; }
  .add-line { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-3); }
  .field-hint { grid-column: auto; }
  .option-photo { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); min-width: 0; }
  .option-photo .file-label { display: block; min-width: 0; max-width: 100%; }
  input[type="file"] { max-width: 100%; }
  .option-thumb { inline-size: var(--space-8); block-size: var(--space-8); object-fit: cover; border-radius: var(--radius); background: var(--surface-2); }

  .preview { display: block; max-inline-size: var(--field-m); max-block-size: var(--field-m); border-radius: var(--radius); object-fit: cover; }
  .start { justify-content: flex-start; margin-top: 0; }
  .save-area { display: flex; flex-direction: column; gap: var(--space-2); }
  .saved-status { margin: 0; }
  textarea { resize: vertical; }
</style>
