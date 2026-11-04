<script lang="ts">
  import { tick } from "svelte";
  import Card from "./ui/Card.svelte";
  import Field from "./ui/Field.svelte";
  import FormGrid from "./ui/FormGrid.svelte";
  import InlineStatus from "./ui/InlineStatus.svelte";
  import ConfirmInline from "./ui/ConfirmInline.svelte";
  import RowEditor from "./ui/RowEditor.svelte";
  import { focusAfterRowRemoved, rowMenuKey } from "./ui/rowEditor";
  import { createCategory, deleteCategory, updateCategory, type Category } from "../lib/categoriesApi";
  import {
    CATEGORY_NAME_MAX,
    CATEGORY_SLUG_MAX,
    MAX_CATEGORIES,
    deleteConsequence,
    nextCategoryOrder,
    reorderUpdates,
    slugProblem,
    slugify,
  } from "../lib/categories";
  import { announce } from "../lib/a11y.svelte";
  import { keySelector } from "../lib/a11yFocus";

  // The categories manager (D17): add, rename, reorder, hide and delete the categories items can belong to. The
  // list is the parent's (`categories`), so the item editor's Category select and this card never disagree;
  // every successful write asks the parent to reload (`onreload`), which also reloads the items, because deleting
  // a category unlinks the items that used it.
  //
  // Each row saves when you leave a field or flip Hidden, and reordering and deleting write at once, so there is
  // no unsaved state to guard. The slug is the shop address (#/category/<slug>). It is generated from the name
  // when you add a category and stays editable; renaming a category later leaves its slug alone, so shared links
  // keep working.
  let {
    categories,
    onreload,
    available = true,
  }: {
    categories: Category[];
    onreload: () => Promise<void>;
    /** False while the category list could not be loaded: editing then could overwrite what we cannot see. */
    available?: boolean;
  } = $props();

  interface Row {
    id: string;
    name: string;
    slug: string;
    hidden: boolean;
    itemCount: number;
    sortOrder: number;
    /** The last problem saving this row, and the field it belongs under. */
    error: { field: "name" | "slug"; text: string } | null;
  }

  let rows = $state<Row[]>([]);
  $effect(() => {
    rows = categories.map((c) => ({ id: c.id, name: c.name, slug: c.slug, hidden: c.hidden, itemCount: c.itemCount, sortOrder: c.sortOrder, error: null }));
  });

  let actionError = $state("");
  let notice = $state("");
  let busy = $state(false);

  // Add form
  let newName = $state("");
  let newSlug = $state("");
  // True once the slug has been typed into; until then it follows the name.
  let slugTouched = $state(false);
  let addError = $state<{ field: "name" | "slug"; text: string } | null>(null);
  const derivedSlug = $derived(slugify(newName));
  const shownSlug = $derived(slugTouched ? newSlug : derivedSlug);
  const addSlugProblem = $derived(slugProblem(shownSlug));
  const atLimit = $derived(categories.length >= MAX_CATEGORIES);

  function describeError(err: unknown, fallback: string): string {
    return err instanceof Error ? err.message : fallback;
  }
  // The server's messages lead with the field ("Address name: another category already uses this slug.").
  function fieldOf(text: string): "name" | "slug" {
    return /^address name\b|\bslug\b/i.test(text) ? "slug" : "name";
  }

  async function add(e: SubmitEvent) {
    e.preventDefault();
    if (busy) return;
    addError = null;
    const name = newName.trim();
    if (name === "") {
      addError = { field: "name", text: "Enter a name." };
      return;
    }
    if (addSlugProblem) {
      addError = { field: "slug", text: addSlugProblem };
      return;
    }
    busy = true;
    try {
      const created = await createCategory({ name, slug: shownSlug.trim(), sortOrder: nextCategoryOrder(categories), hidden: false });
      newName = "";
      newSlug = "";
      slugTouched = false;
      await onreload();
      notice = `Added ${created.name}.`;
      announce(notice);
      await tick();
      document.querySelector<HTMLElement>(keySelector("add-category"))?.focus();
    } catch (err) {
      const text = describeError(err, "Couldn't add the category.");
      addError = { field: fieldOf(text), text };
    } finally {
      busy = false;
    }
  }

  // Saves one row's name, slug and hidden flag when any differs from the stored category.
  async function saveRow(row: Row) {
    const stored = categories.find((c) => c.id === row.id);
    if (!stored || busy) return;
    const name = row.name.trim();
    const slug = row.slug.trim();
    if (name === stored.name && slug === stored.slug && row.hidden === stored.hidden) {
      row.error = null;
      return;
    }
    if (name === "") {
      row.error = { field: "name", text: "Enter a name." };
      return;
    }
    const problem = slugProblem(slug);
    if (problem) {
      row.error = { field: "slug", text: problem };
      return;
    }
    busy = true;
    row.error = null;
    actionError = "";
    try {
      await updateCategory(row.id, { name, slug, sortOrder: stored.sortOrder, hidden: row.hidden });
      notice = `Saved ${name}.`;
      await onreload();
      announce(notice);
    } catch (err) {
      const text = describeError(err, "Couldn't save the category.");
      row.error = { field: fieldOf(text), text };
    } finally {
      busy = false;
    }
  }

  async function move(index: number, delta: -1 | 1) {
    const updates = reorderUpdates(categories, index, delta);
    if (updates.length === 0 || busy) return;
    busy = true;
    actionError = "";
    try {
      for (const u of updates) {
        const c = categories.find((x) => x.id === u.id);
        if (c) await updateCategory(c.id, { name: c.name, slug: c.slug, sortOrder: u.sortOrder, hidden: c.hidden });
      }
      await onreload();
    } catch (err) {
      actionError = describeError(err, "Couldn't reorder the categories.");
      // An earlier write may have landed; show what persisted.
      await onreload();
    } finally {
      busy = false;
    }
  }

  let confirmingId = $state<string | null>(null);

  function askDelete(row: Row) {
    actionError = "";
    if (row.itemCount === 0) void doDelete(row);
    else confirmingId = row.id;
  }

  async function doDelete(row: Row) {
    if (busy) return;
    busy = true;
    actionError = "";
    const index = rows.findIndex((r) => r.id === row.id);
    const remaining = rows.filter((r) => r.id !== row.id).map((r) => r.id);
    try {
      await deleteCategory(row.id);
      confirmingId = null;
      await onreload();
      notice = `Deleted ${row.name}.`;
      announce(notice);
      await focusAfterRowRemoved(remaining, index, "add-category");
    } catch (err) {
      actionError = describeError(err, "Couldn't delete the category.");
      confirmingId = null;
      await tick();
      document.querySelector<HTMLElement>(keySelector(rowMenuKey(row.id)))?.focus();
    } finally {
      busy = false;
    }
  }
</script>

<Card>
<div class="stack">
<h2>Categories</h2>
  <p class="hint">
    A category groups shop items under one chip on your shop page. An item has no category or one. Hidden categories stay out of the shop and keep their items.
  </p>

  {#if !available}
    <p class="banner-warn" role="alert">Couldn't load categories. Reload the page to manage them.</p>
  {:else}
    {#if actionError}<InlineStatus kind="error">{actionError}</InlineStatus>{/if}

    {#if rows.length === 0}
      <p class="empty-state">No categories yet. Add one below.</p>
    {/if}

    <div class="cat-list">
      {#each rows as row, i (row.id)}
        <RowEditor
          tone="inset"
          label={`Category ${i + 1}: ${row.name.trim() || "unnamed"}`}
          rowKey={row.id}
          position={{ index: i, count: rows.length }}
          onmoveup={() => move(i, -1)}
          onmovedown={() => move(i, 1)}
          onremove={() => askDelete(row)}
          removeLabel="Delete"
        >
          {#snippet fields()}
            <FormGrid>
              <Field id={`cat-name-${row.id}`} label="Name" size="m" error={row.error?.field === "name" ? row.error.text : undefined}>
                {#snippet children(f)}
                  <input
                    id={f.id}
                    bind:value={row.name}
                    maxlength={CATEGORY_NAME_MAX}
                    aria-describedby={f.describedby}
                    aria-invalid={f.invalid}
                    onchange={() => saveRow(row)}
                  />
                {/snippet}
              </Field>
              <Field id={`cat-slug-${row.id}`} label="Address name (slug)" size="m" hint={`Shop address: #/category/${row.slug || "…"}`} error={row.error?.field === "slug" ? row.error.text : undefined}>
                {#snippet children(f)}
                  <input
                    id={f.id}
                    bind:value={row.slug}
                    maxlength={CATEGORY_SLUG_MAX}
                    autocapitalize="off"
                    spellcheck="false"
                    aria-describedby={f.describedby}
                    aria-invalid={f.invalid}
                    onchange={() => saveRow(row)}
                  />
                {/snippet}
              </Field>
              <label class="toggle cell-end">
                <input type="checkbox" bind:checked={row.hidden} onchange={() => saveRow(row)} />
                <span>Hidden from the shop</span>
              </label>
              <p class="count cell-end">{row.itemCount === 1 ? "1 item" : `${row.itemCount} items`}</p>
            </FormGrid>
          {/snippet}
          {#snippet below()}
            {#if confirmingId === row.id}
              <div class="confirm">
                <ConfirmInline
                  confirmLabel={`Yes, delete ${row.name.trim() || "this category"}`}
                  danger
                  busy={busy}
                  restore={keySelector(rowMenuKey(row.id))}
                  onconfirm={() => doDelete(row)}
                  oncancel={() => (confirmingId = null)}
                >{deleteConsequence({ name: row.name.trim() || "This category", itemCount: row.itemCount })}</ConfirmInline>
              </div>
            {/if}
          {/snippet}
        </RowEditor>
      {/each}
    </div>

    <form class="add" onsubmit={add} aria-label="Add a category">
      <h3>Add a category</h3>
      <FormGrid align="controls">
        <Field id="cat-new-name" label="Name" size="m" error={addError?.field === "name" ? addError.text : undefined}>
          {#snippet children(f)}
            <input id={f.id} bind:value={newName} maxlength={CATEGORY_NAME_MAX} data-focus-key="add-category" aria-describedby={f.describedby} aria-invalid={f.invalid} disabled={atLimit} />
          {/snippet}
        </Field>
        <Field
          id="cat-new-slug"
          label="Address name (slug)"
          size="m"
          hint={slugTouched ? undefined : "Made from the name. You can change it."}
          error={addError?.field === "slug" ? addError.text : undefined}
        >
          {#snippet children(f)}
            <input
              id={f.id}
              value={shownSlug}
              maxlength={CATEGORY_SLUG_MAX}
              autocapitalize="off"
              spellcheck="false"
              aria-describedby={f.describedby}
              aria-invalid={f.invalid}
              disabled={atLimit}
              oninput={(e) => {
                slugTouched = true;
                newSlug = e.currentTarget.value;
              }}
            />
          {/snippet}
        </Field>
      </FormGrid>
      <div class="form-actions start">
        <button type="submit" class="primary" aria-disabled={busy || atLimit}>Add category</button>
      </div>
      {#if atLimit}<p class="hint">A shop has at most {MAX_CATEGORIES} categories.</p>{/if}
    </form>
    <p class="hint" role="status">{notice}</p>
  {/if}
</div>
</Card>

<style>
  .stack { display: flex; flex-direction: column; gap: var(--gap); min-width: 0; }
  .stack > :global(h2), .stack > :global(h3) { margin: 0; }
  .cat-list { display: flex; flex-direction: column; gap: var(--gap); }
  .add { display: flex; flex-direction: column; gap: var(--gap); }
  .add h3 { margin: 0; }
  .start { justify-content: flex-start; margin-top: 0; }
  .cell-end { align-self: end; }
  .count { margin: 0; color: var(--muted); font-size: var(--type-secondary); min-height: var(--control-h); display: flex; align-items: center; }
  .confirm { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); }
</style>
