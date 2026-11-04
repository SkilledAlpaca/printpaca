<script lang="ts">
  import { onMount, tick } from "svelte";
  import type { EffectiveConfig } from "../lib/engine/index";
  import { DEFAULT_FETCH_TIMEOUT_MS } from "../lib/fetchTimeouts";
  import { announce, focusAfterUpdate } from "../lib/a11y.svelte";
  import { confirmFocus, keySelector } from "../lib/a11yFocus";
  import {
    SPOOLMAN_FILAMENT_URL,
    SPOOLMAN_INVENTORY_URL,
    colorName,
    createSpool,
    filamentLabel,
    filterSpools,
    isValidFilament,
    isValidSpool,
    normalizeHex,
    patchSpool,
    spoolLabel,
    type FilamentDto,
    type SpoolDto,
    type SpoolPatchBody,
  } from "../lib/spoolman";
  import LabeledNumber from "./LabeledNumber.svelte";
  import Card from "./ui/Card.svelte";
  import Field from "./ui/Field.svelte";
  import FormGrid from "./ui/FormGrid.svelte";
  import DataTable, { type DataColumn } from "./ui/DataTable.svelte";
  import ConfirmInline from "./ui/ConfirmInline.svelte";

  let { cfg }: { cfg: EffectiveConfig } = $props();

  // The server proxy allows writes for the admin role only (the filament grant is read-only) and blocks them
  // in the demo. The UI mirrors that so a non-admin never sees a button that can only fail.
  const canWrite = $derived(cfg.user.isAdmin && !cfg.demoMode);

  type Status = "loading" | "ok" | "error";
  let spools = $state<SpoolDto[]>([]);
  let status = $state<Status>("loading");
  let errorDetail = $state("");
  let filter = $state("");
  let showArchived = $state(false);

  async function load(announceDone = false) {
    status = "loading";
    errorDetail = "";
    try {
      const res = await fetch(SPOOLMAN_INVENTORY_URL, { signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS) });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data: unknown = await res.json();
      spools = Array.isArray(data) ? data.filter(isValidSpool) : [];
      status = "ok";
      if (announceDone) announce(`Spoolman inventory refreshed. ${spools.length} spools.`);
    } catch (e) {
      spools = [];
      errorDetail = e instanceof Error ? e.message : "unknown error";
      status = "error";
    }
  }

  onMount(() => {
    if (cfg.integrations.spoolman) void load();
  });

  const visible = $derived(filterSpools(spools, filter, showArchived));
  const archivedCount = $derived(spools.filter((s) => s.archived === true).length);

  const summary = $derived(
    status === "loading"
      ? "Loading spools…"
      : status === "error"
        ? `Couldn't load the Spoolman inventory (${errorDetail}). Check the Spoolman URL in Settings, then press Refresh.`
        : spools.length === 0
          ? "Spoolman has no spools yet."
          : `Showing ${visible.length} of ${spools.length} spools${showArchived || archivedCount === 0 ? "" : `. ${archivedCount} archived hidden`}.`,
  );

  // ---- editor (one form open at a time) ---------------------------------------------------------------------

  type Editor =
    | { kind: "add" }
    | { kind: "remaining"; id: number }
    | { kind: "details"; id: number };
  let editor = $state<Editor | null>(null);
  let saving = $state(false);
  let formError = $state("");

  let filaments = $state<FilamentDto[]>([]);
  let filamentsStatus = $state<"idle" | "loading" | "ok" | "error">("idle");
  let newFilamentId = $state("");
  let newInitial = $state<number | null>(null);
  let newPrice = $state<number | null>(null);
  let newLocation = $state("");

  let editRemaining = $state<number | null>(null);
  let editPrice = $state<number | null>(null);
  let editLocation = $state("");

  function spoolById(id: number): SpoolDto | undefined {
    return spools.find((s) => s.id === id);
  }

  async function loadFilaments() {
    filamentsStatus = "loading";
    try {
      const res = await fetch(SPOOLMAN_FILAMENT_URL, { signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS) });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data: unknown = await res.json();
      filaments = Array.isArray(data) ? data.filter(isValidFilament) : [];
      filamentsStatus = "ok";
    } catch {
      filaments = [];
      filamentsStatus = "error";
    }
  }

  function openAdd() {
    editor = { kind: "add" };
    formError = "";
    newFilamentId = "";
    newInitial = null;
    newPrice = null;
    newLocation = "";
    if (filamentsStatus !== "ok") void loadFilaments();
    void focusAfterUpdate("#spool-add-filament");
  }

  function pickFilament(value: string) {
    newFilamentId = value;
    const f = filaments.find((x) => String(x.id) === value);
    // Seed from the filament's own defaults; the person can still change both.
    newInitial = f?.weight ?? null;
    newPrice = f?.price ?? null;
  }

  function openRemaining(s: SpoolDto) {
    editor = { kind: "remaining", id: s.id };
    formError = "";
    editRemaining = s.remaining_weight ?? null;
    void focusAfterUpdate("#spool-edit-remaining input");
  }

  function openDetails(s: SpoolDto) {
    editor = { kind: "details", id: s.id };
    formError = "";
    editPrice = s.price ?? null;
    editLocation = s.location ?? "";
    void focusAfterUpdate("#spool-edit-details input");
  }

  async function closeEditor(restoreKey: string) {
    editor = null;
    formError = "";
    await tick();
    const el = document.querySelector<HTMLElement>(keySelector(restoreKey)) ?? document.querySelector<HTMLElement>("#spool-filter");
    el?.focus();
  }

  async function submitAdd() {
    if (saving) return;
    const filamentId = Number(newFilamentId);
    if (!Number.isInteger(filamentId) || filamentId < 1) {
      formError = "Pick a filament first.";
      return;
    }
    if (newInitial === null || !(newInitial > 0)) {
      formError = "Enter the spool's initial weight in grams.";
      return;
    }
    if (newPrice !== null && newPrice < 0) {
      formError = "The price can't be negative.";
      return;
    }
    saving = true;
    formError = "";
    const body: { filament_id: number; initial_weight: number; price?: number; location?: string } = {
      filament_id: filamentId,
      initial_weight: newInitial,
    };
    if (newPrice !== null) body.price = newPrice;
    if (newLocation.trim() !== "") body.location = newLocation.trim();
    const result = await createSpool(body);
    saving = false;
    if (!result.ok) {
      formError = result.error;
      return;
    }
    const label = isValidSpool(result.data) ? spoolLabel(result.data) : `spool #${(result.data as { id?: number }).id ?? ""}`;
    announce(`Added ${label}.`);
    await load();
    await closeEditor("spool-add");
  }

  async function submitPatch(id: number, body: SpoolPatchBody, done: string, restoreKey: string) {
    if (saving) return;
    saving = true;
    formError = "";
    const result = await patchSpool(id, body);
    saving = false;
    if (!result.ok) {
      formError = result.error;
      return;
    }
    announce(done);
    await load();
    await closeEditor(restoreKey);
  }

  function submitRemaining() {
    if (editor?.kind !== "remaining") return;
    if (editRemaining === null || editRemaining < 0) {
      formError = "Enter the remaining weight in grams (0 or more).";
      return;
    }
    const id = editor.id;
    void submitPatch(id, { remaining_weight: editRemaining }, `Spool #${id} now has ${Math.round(editRemaining)} g remaining.`, `spool-${id}-remaining`);
  }

  function submitDetails() {
    if (editor?.kind !== "details") return;
    if (editPrice !== null && editPrice < 0) {
      formError = "The price can't be negative.";
      return;
    }
    const id = editor.id;
    // A cleared price goes as null; omitting the key would leave Spoolman's old value in place.
    const body: SpoolPatchBody = { location: editLocation.trim(), price: editPrice };
    void submitPatch(id, body, `Updated spool #${id}.`, `spool-${id}-details`);
  }

  // ---- archive (asks first) / unarchive ---------------------------------------------------------------------

  let confirmingArchive = $state<number | null>(null);

  async function archive(id: number) {
    confirmingArchive = null;
    if (saving) return;
    saving = true;
    const result = await patchSpool(id, { archived: true });
    saving = false;
    if (!result.ok) {
      announce(`Couldn't archive spool #${id}: ${result.error}`, "assertive");
      formError = result.error;
      return;
    }
    announce(`Archived spool #${id}.${showArchived ? "" : " It is hidden until you turn on Show archived."}`);
    await load();
    // The row left the list unless archived rows are shown; fall back to the filter box.
    await closeEditor(`spool-${id}-archive`);
  }

  async function unarchive(id: number) {
    if (saving) return;
    saving = true;
    const result = await patchSpool(id, { archived: false });
    saving = false;
    if (!result.ok) {
      announce(`Couldn't unarchive spool #${id}: ${result.error}`, "assertive");
      formError = result.error;
      return;
    }
    announce(`Unarchived spool #${id}.`);
    await load();
    await closeEditor(`spool-${id}-archive`);
  }

  function money(n: number | null | undefined): string {
    return n == null ? "-" : `${cfg.currency.symbol}${n.toFixed(2)}`;
  }

  const COLUMNS: DataColumn[] = [
    { key: "id", label: "ID" },
    { key: "filament", label: "Filament" },
    { key: "material", label: "Material" },
    { key: "colour", label: "Colour" },
    { key: "remaining", label: "Remaining (g)", num: true },
    { key: "price", label: "Price (per spool)", num: true },
    { key: "location", label: "Location" },
    { key: "state", label: "State" },
  ];
  const columns = $derived<DataColumn[]>(canWrite ? [...COLUMNS, { key: "actions", label: "Actions", hideLabel: true }] : COLUMNS);

  const emptyText = $derived(
    status !== "ok" ? "No spools to show." : spools.length === 0 ? "Spoolman has no spools yet." : "No spools match the filter.",
  );
</script>

<section class="inventory" id="spoolman-inventory" aria-labelledby="spoolman-inventory-title">
  <h2 id="spoolman-inventory-title">Spoolman inventory</h2>

  {#if !cfg.integrations.spoolman}
    <Card>
<div class="stack">
      <p class="hint">
        Connect Spoolman to see and manage your spools here and to pick a spool on New quote.
        {#if cfg.user.isAdmin}
          Enter your Spoolman URL under Integrations in <a href="#/settings">Settings</a>; it applies as soon as you save.
        {:else}
          Ask an admin to connect it.
        {/if}
      </p>
    </div>
</Card>
  {:else}
    {#if cfg.demoMode}
      <p class="hint">This demo shows sample spools. Changing the inventory is turned off.</p>
    {:else if !canWrite}
      <p class="hint">You can view the inventory. Only an admin can add or change spools.</p>
    {/if}

    {#if editor?.kind === "add"}
      <Card as="form" onsubmit={(e: SubmitEvent) => { e.preventDefault(); void submitAdd(); }} aria-labelledby="spool-add-title">
<div class="stack">
        <h3 id="spool-add-title">Add spool</h3>
        <Field id="spool-add-filament" label="Filament" size="l" describedby="spool-add-filament-hint">
          {#snippet children(f)}
            <select id={f.id} value={newFilamentId} onchange={(e) => pickFilament(e.currentTarget.value)} aria-describedby={f.describedby}>
              <option value="">Pick a filament…</option>
              {#each filaments as fil (fil.id)}<option value={String(fil.id)}>{filamentLabel(fil)}</option>{/each}
            </select>
            <small id="spool-add-filament-hint" class="hint-text" role="status">
              {#if filamentsStatus === "loading"}Loading filaments…
              {:else if filamentsStatus === "error"}Couldn't load Spoolman's filaments. Close this form and try again.
              {:else if filamentsStatus === "ok" && filaments.length === 0}Spoolman has no filaments yet. Add one in Spoolman first, then come back.
              {:else}Spools are added for a filament that already exists in Spoolman.{/if}
            </small>
          {/snippet}
        </Field>
        <FormGrid align="controls">
          <LabeledNumber label="Initial weight" suffix="g" bind:value={newInitial} />
          <LabeledNumber label="Price" suffix={cfg.currency.symbol} bind:value={newPrice} />
          <Field id="spool-add-location" label="Location" size="m">
            {#snippet children(f)}
              <input id={f.id} bind:value={newLocation} maxlength={128} />
            {/snippet}
          </Field>
        </FormGrid>
        {#if formError}<p class="warn-text" role="alert">{formError}</p>{/if}
        <div class="form-actions start">
          <button type="submit" class="primary" aria-disabled={saving} aria-busy={saving}>{saving ? "Adding…" : "Add spool"}</button>
          <button type="button" onclick={() => closeEditor("spool-add")}>Cancel</button>
        </div>
      </div>
</Card>
    {/if}

    {#if editor?.kind === "remaining"}
      {@const s = spoolById(editor.id)}
      <Card as="form" id="spool-edit-remaining" onsubmit={(e: SubmitEvent) => { e.preventDefault(); submitRemaining(); }} aria-labelledby="spool-remaining-title">
<div class="stack">
        <h3 id="spool-remaining-title">Set remaining weight for spool #{editor.id}{s ? ` (${spoolLabel(s)})` : ""}</h3>
        <LabeledNumber label="Remaining weight" suffix="g" bind:value={editRemaining} />
        {#if formError}<p class="warn-text" role="alert">{formError}</p>{/if}
        <div class="form-actions start">
          <button type="submit" class="primary" aria-disabled={saving} aria-busy={saving}>{saving ? "Saving…" : "Save weight"}</button>
          <button type="button" onclick={() => closeEditor(`spool-${editor?.kind === "remaining" ? editor.id : 0}-remaining`)}>Cancel</button>
        </div>
      </div>
</Card>
    {/if}

    {#if editor?.kind === "details"}
      {@const s = spoolById(editor.id)}
      <Card as="form" id="spool-edit-details" onsubmit={(e: SubmitEvent) => { e.preventDefault(); submitDetails(); }} aria-labelledby="spool-details-title">
<div class="stack">
        <h3 id="spool-details-title">Edit location and price for spool #{editor.id}{s ? ` (${spoolLabel(s)})` : ""}</h3>
        <FormGrid align="controls">
          <Field id="spool-edit-location" label="Location" size="m">
            {#snippet children(f)}
              <input id={f.id} bind:value={editLocation} maxlength={128} />
            {/snippet}
          </Field>
          <LabeledNumber label="Price" suffix={cfg.currency.symbol} bind:value={editPrice} />
        </FormGrid>
        {#if formError}<p class="warn-text" role="alert">{formError}</p>{/if}
        <div class="form-actions start">
          <button type="submit" class="primary" aria-disabled={saving} aria-busy={saving}>{saving ? "Saving…" : "Save details"}</button>
          <button type="button" onclick={() => closeEditor(`spool-${editor?.kind === "details" ? editor.id : 0}-details`)}>Cancel</button>
        </div>
      </div>
</Card>
    {/if}

    <DataTable label="Spoolman spools" {columns}>
      {#snippet filters()}
        <div class="toolbar">
          <Field id="spool-filter" label="Filter spools" size="l">
            {#snippet children(f)}
              <input id={f.id} type="search" bind:value={filter} autocomplete="off" placeholder="Vendor, material, colour, location" />
            {/snippet}
          </Field>
          <label class="toggle">
            <input type="checkbox" bind:checked={showArchived} />
            <span>Show archived</span>
          </label>
          <button type="button" onclick={() => load(true)} aria-disabled={status === "loading"} aria-busy={status === "loading"}>Refresh</button>
          {#if canWrite}
            <button type="button" class="primary" data-focus-key="spool-add" onclick={openAdd}>Add spool</button>
          {/if}
        </div>
        <p class="status-line summary" class:error={status === "error"} role={status === "error" ? "alert" : "status"} aria-live={status === "error" ? "assertive" : "polite"}>{summary}</p>
      {/snippet}
      {#each visible as s (s.id)}
        {@const hex = normalizeHex(s.filament.color_hex)}
        <tr class="spool-row" data-spool-id={s.id} class:archived={s.archived === true}>
          <td class="c-id">#{s.id}</td>
          <td class="c-filament">{[s.filament.vendor?.name, s.filament.name].filter(Boolean).join(" ") || "-"}</td>
          <td class="c-material" data-label="Material">{s.filament.material ?? "-"}</td>
          <td class="c-colour">
            {#if hex}
              <span class="swatch" style:background={hex} aria-hidden="true"></span>
              <span>{colorName(hex)} <span class="hex">{hex}</span></span>
            {:else}
              <span class="hex">No colour</span>
            {/if}
          </td>
          <td class="num c-remaining" data-label="Remaining">{s.remaining_weight === undefined ? "-" : Math.round(s.remaining_weight)}</td>
          <td class="num c-price" data-label="Price">{money(s.price)}</td>
          <td class="c-location" data-label="Location">{s.location || "-"}</td>
          <td class="c-state">{s.archived === true ? "Archived" : "Active"}</td>
          {#if canWrite}
            <td class="actions c-actions">
              <button type="button" class="quiet" data-focus-key={`spool-${s.id}-remaining`} aria-label={`Set remaining weight for spool ${s.id}`} onclick={() => openRemaining(s)}>Set remaining</button>
              <button type="button" class="quiet" data-focus-key={`spool-${s.id}-details`} aria-label={`Edit location and price for spool ${s.id}`} onclick={() => openDetails(s)}>Edit</button>
              {#if s.archived === true}
                <button type="button" class="quiet" data-focus-key={`spool-${s.id}-archive`} aria-label={`Unarchive spool ${s.id}`} onclick={() => unarchive(s.id)}>Unarchive</button>
              {:else if confirmingArchive === s.id}
                <ConfirmInline
                  confirmLabel={`Yes, archive spool ${s.id}`}
                  restore={keySelector(`spool-${s.id}-archive`)}
                  onconfirm={() => archive(s.id)}
                  oncancel={() => (confirmingArchive = null)}
                />
              {:else}
                <button type="button" class="quiet" data-focus-key={`spool-${s.id}-archive`} aria-label={`Archive spool ${s.id}`} onclick={() => (confirmingArchive = s.id)}>Archive</button>
              {/if}
            </td>
          {/if}
        </tr>
      {:else}
        <tr><td colspan={columns.length} class="empty-state">{emptyText}</td></tr>
      {/each}
    </DataTable>
  {/if}
</section>

<style>
  .inventory { display: flex; flex-direction: column; gap: var(--gap); }
  .inventory h2 { margin: 0; }
  .toolbar { display: flex; flex-wrap: wrap; align-items: flex-end; gap: var(--gap); }
  .toolbar :global(.ui-field) { flex: 1 1 var(--field-m); }
  .summary { margin: 0; }
  .summary.error { color: var(--danger); }
  .start { justify-content: flex-start; margin-top: 0; }
  .hint-text { color: var(--muted); font-size: var(--type-secondary); }
  td.empty-state { text-align: center; }
  td.actions { display: flex; gap: var(--space-1); flex-wrap: wrap; }
  tr.archived td { color: var(--muted); }
  td.num { text-align: right; font-variant-numeric: tabular-nums; }
  .swatch { display: inline-block; inline-size: var(--space-4); block-size: var(--space-4); border-radius: var(--space-1); border: 1px solid var(--border); vertical-align: middle; margin-inline-end: var(--space-1); }
  .hex { color: var(--muted); font-size: var(--type-secondary); }

  /* Phones: each spool becomes a card: id and state on top, the filament, then material and colour, remaining and
   * price, location, and the actions. */
  @media (max-width: 600px) {
    tr.spool-row {
      display: grid;
      grid-template-columns: minmax(0, 1fr) auto;
      grid-template-areas: "id state" "filament filament" "material colour" "remaining price" "location location" "actions actions";
      gap: var(--space-1) var(--space-3);
      padding: var(--space-3) 0;
      border-bottom: 1px solid var(--border);
    }
    .c-id { grid-area: id; font-weight: 600; }
    .c-state { grid-area: state; text-align: right; }
    .c-filament { grid-area: filament; overflow-wrap: anywhere; }
    .c-material { grid-area: material; }
    .c-colour { grid-area: colour; text-align: right; }
    .c-remaining { grid-area: remaining; }
    .c-price { grid-area: price; }
    .c-location { grid-area: location; }
    .c-actions { grid-area: actions; }
    .c-material::before, .c-remaining::before, .c-price::before, .c-location::before { content: attr(data-label) ": "; color: var(--muted); font-size: var(--type-secondary); }
  }
</style>
