<script lang="ts">
  import { onMount } from "svelte";
  import type { EffectiveConfig } from "../lib/engine/index";
  import { DEFAULT_FETCH_TIMEOUT_MS } from "../lib/fetchTimeouts";
  import { applyMaterial, type PartForm } from "../lib/state.svelte";
  import {
    SPOOLMAN_SPOOL_PATH,
    isLowStock,
    isValidSpool,
    spoolLabel,
    spoolOverrideFrom,
    type SpoolDto,
  } from "../lib/spoolman";

  let { cfg, part }: { cfg: EffectiveConfig; part: PartForm } = $props();

  type Status = "loading" | "ok" | "empty" | "error";

  let spools = $state<SpoolDto[]>([]);
  let status = $state<Status>("loading");
  let errorDetail = $state("");

  async function load() {
    status = "loading";
    errorDetail = "";
    try {
      const res = await fetch(SPOOLMAN_SPOOL_PATH, { signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS) });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data: unknown = await res.json();
      spools = Array.isArray(data) ? data.filter(isValidSpool).filter((s) => s.archived !== true) : [];
      status = spools.length > 0 ? "ok" : "empty";
    } catch (e) {
      spools = [];
      errorDetail = e instanceof Error ? e.message : "unknown error";
      status = "error";
    }
  }

  onMount(load);

  // The "Use material preset" option and the Clear button both go through clear(), which restores
  // price/density via applyMaterial; just nulling the override would leave a stale spool price in
  // part.pricePerKg.
  function clear() {
    materialNote = "";
    applyMaterial(cfg, part, part.materialId);
  }

  /** The catalog material a Spoolman material string names ("PETG" matches id or name, ignoring case). */
  function catalogMaterial(name: string) {
    const want = name.trim().toLowerCase();
    return want === "" ? undefined : cfg.materials.find((m) => m.id.toLowerCase() === want || m.name.toLowerCase() === want);
  }

  // Set when picking a spool changed the Material select, so the change is visible and not a surprise.
  let materialNote = $state("");

  function pick(rawValue: string) {
    if (rawValue === "") {
      clear();
      return;
    }
    const id = Number(rawValue);
    const s = spools.find((x) => x.id === id);
    if (!s) {
      clear();
      return;
    }
    const override = spoolOverrideFrom(s);
    part.spoolOverride = override;
    // A spool with no usable price must not zero-fill part.pricePerKg; keep what the user or material had.
    if (override.pricePerKg !== null) part.pricePerKg = override.pricePerKg;
    // The spool decides the material: pricing PETG filament as PLA would pick the wrong waste figure and
    // label. Only the identity and waste follow; price and density stay the spool's own.
    materialNote = "";
    const match = catalogMaterial(s.filament.material ?? "");
    if (match && match.id !== part.materialId) {
      part.materialId = match.id;
      part.wastePct = match.wastePct;
      materialNote = `Material changed to ${match.name} to match this spool.`;
    }
  }

  // No local selection state: the value derives from part.spoolOverride.spoolId, so a material switch (nulls
  // the override) or a restore (sets it directly) keeps the dropdown honest without a reconciling $effect.
  const selectedId = $derived(part.spoolOverride?.spoolId ?? "");

  const selectedSpool = $derived(spools.find((x) => x.id === selectedId));
  // A spool whose material the catalog doesn't know (or that the user switched away from) keeps the spool's
  // price and density but is flagged, since the Material select then names something else.
  const spoolMaterial = $derived(selectedSpool?.filament.material?.trim() ?? "");
  const materialMismatch = $derived(
    part.spoolOverride !== null &&
      spoolMaterial !== "" &&
      catalogMaterial(spoolMaterial)?.id !== part.materialId,
  );
  const currentMaterialName = $derived(cfg.materials.find((m) => m.id === part.materialId)?.name ?? part.materialId);

  const needed = $derived((part.weightG ?? 0) * (part.quantity ?? 1));
  const low = $derived(part.spoolOverride !== null && isLowStock(part.spoolOverride.remainingG, needed));
  const priceMissing = $derived(part.spoolOverride !== null && part.spoolOverride.pricePerKg === null);

  // Single always-present live region: screen-reader announcements for freshly inserted live regions are
  // unreliable, so swap text, not elements. Empty inventory and a fetch failure get distinct wording.
  const statusText = $derived(
    status === "loading"
      ? "Loading spools…"
      : status === "error"
        ? `Spoolman unreachable at ${SPOOLMAN_SPOOL_PATH} (${errorDetail}).`
        : status === "empty"
          ? "No spools found in Spoolman."
          : "",
  );
  const assertive = $derived(status === "error");
</script>

<div class="spool-picker">
  <p
    class="status-line {status}"
    role={assertive ? "alert" : "status"}
    aria-live={assertive ? "assertive" : "polite"}
  >{statusText}</p>

  {#if status === "error"}
    <button class="quiet" onclick={load}>Retry</button>
  {/if}

  {#if status === "ok"}
    <label class="field field-inline spool">
      <span>Spool (from Spoolman)</span>
      <select value={selectedId} onchange={(e) => pick(e.currentTarget.value)}>
        <option value="">Use material preset</option>
        {#each spools as s (s.id)}
          <option value={s.id}>
            {spoolLabel(s)} · {s.remaining_weight == null ? "remaining unknown" : `${Math.round(s.remaining_weight)} g left`}
          </option>
        {/each}
      </select>
    </label>

    {#if materialNote}
      <p class="advisory-text hint-text" role="status">{materialNote}</p>
    {/if}

    {#if materialMismatch}
      <p class="warn-text hint-text" role="status">
        This spool is {spoolMaterial}, but Material is {currentMaterialName}. The price and density come from the spool.
      </p>
    {/if}

    {#if part.spoolOverride && priceMissing}
      <p class="advisory-text hint-text" role="status">
        Spool has no price in Spoolman. Using current filament price.
      </p>
    {/if}

    {#if part.spoolOverride && low}
      <p class="warn-text" role="alert">
        Only {Math.round(part.spoolOverride.remainingG ?? 0)} g left on this spool. This job needs ≈{Math.round(needed)} g.
      </p>
    {/if}

    {#if part.spoolOverride}
      <button class="quiet" onclick={clear}>Clear spool</button>
    {/if}
  {/if}
</div>

<style>
  .spool-picker { margin-top: 0.6rem; }
  .spool { margin-top: 0.4rem; }
  .status-line { margin: 0 0 0.4rem; }
  .status-line.error { color: var(--danger); }
  .hint-text { margin: 0.3rem 0; }
</style>
