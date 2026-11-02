<script lang="ts">
  import type { EffectiveConfig } from "../lib/engine/index";
  import { moneyAmount, presetDisplayName } from "../lib/format";
  import { applyPreset, type PartForm } from "../lib/state.svelte";

  let { cfg, part }: { cfg: EffectiveConfig; part: PartForm } = $props();

  /** One-line summary of a preset's rates for the chip's title tooltip. Plain numbers, not money()/cents:
   * cfg.presets values are already dollars (as in QuoteTab's rates-summary line). */
  function presetTitle(name: string): string {
    const p = cfg.presets[name];
    if (!p) return "";
    return `${p.failureRatePct}% failure, ${p.prepMinutes} min prep, ${moneyAmount(p.laborRate, cfg)} per hour labor, ${moneyAmount(p.monthlyFixed, cfg)} per month over ${p.monthlyHours} h`;
  }
</script>

<div class="chips" role="group" aria-label="Cost presets">
  {#each Object.keys(cfg.presets) as name (name)}
    {@const selected = part.presetName === name && !part.presetModified}
    <button class:active={selected} aria-pressed={selected} title={presetTitle(name)} onclick={() => applyPreset(cfg, part, name)}>
      {presetDisplayName(name)}{part.presetName === name && part.presetModified ? " (modified)" : ""}
    </button>
  {/each}
</div>

<!-- .chips styles come from theme.css. -->

