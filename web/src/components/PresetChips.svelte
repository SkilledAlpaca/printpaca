<script lang="ts">
  import type { EffectiveConfig } from "../lib/engine/index";
  import { presetDisplayName } from "../lib/format";
  import { applyPreset, type PartForm } from "../lib/state.svelte";

  let { cfg, part }: { cfg: EffectiveConfig; part: PartForm } = $props();

</script>

<div class="chips" role="group" aria-label="Cost presets">
  {#each Object.keys(cfg.presets) as name (name)}
    {@const selected = part.presetName === name && !part.presetModified}
    <button class:active={selected} aria-pressed={selected} onclick={() => applyPreset(cfg, part, name)}>
      {presetDisplayName(name)}{part.presetName === name && part.presetModified ? " (modified)" : ""}
    </button>
  {/each}
</div>

<!-- .chips styles come from theme.css. -->

