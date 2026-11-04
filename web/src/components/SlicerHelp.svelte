<script lang="ts">
  // On-demand plain-language meaning of "slicer": a disclosure, so the people who know the word never see
  // the explanation and the people who don't can open it from the place the word appears.
  const uid = $props.id();
</script>

<details class="slicer-help">
  <summary>What is a slicer?</summary>
  <p id="{uid}-text">
    A slicer is the program that turns a 3D model into instructions for the printer, for example Bambu Studio,
    PrusaSlicer, or Cura. It also estimates how long the print takes. Open your model in it, slice it, and copy
    the time it shows. A sliced .gcode or .3mf file already has that time inside.
  </p>
</details>

<style>
  .slicer-help { font-size: var(--text-sm); margin-top: var(--space-1); }
  .slicer-help summary { cursor: pointer; color: var(--accent-text); width: fit-content; }
  .slicer-help p { margin: var(--space-1) 0 0; color: var(--muted); max-width: var(--measure-prose); }
</style>
