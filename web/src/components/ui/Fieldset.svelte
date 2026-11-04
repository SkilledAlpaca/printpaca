<script lang="ts">
  import type { Snippet } from "svelte";

  // A native <fieldset> and <legend> for controls that answer one question together (hours and minutes of a
  // print time). Borderless: the legend is the group's label, and the fields inside are Fields.
  // `step` is opt-in, for a numbered section of a long form: the legend then reads as a heading with its number in
  // a small circle in front. The number is part of the legend's name ("Step 1: ...") for assistive technology.
  // Without `step` nothing changes.
  let { legend, hint, hintId, step, children }: { legend: string; hint?: string; hintId?: string; step?: number; children: Snippet } = $props();
</script>

<fieldset aria-describedby={hint ? hintId : undefined}>
  <legend class:step={step !== undefined}>
    {#if step !== undefined}<span class="step-num" aria-hidden="true">{step}</span><span class="visually-hidden">Step {step}: </span>{/if}{legend}
  </legend>
  {#if hint}<p class="fieldset-hint" id={hintId}>{hint}</p>{/if}
  {@render children()}
</fieldset>

<style>
  fieldset { border: 0; margin: 0; padding: 0; min-width: 0; display: flex; flex-direction: column; gap: var(--space-2); }
  legend { padding: 0; font-size: var(--type-secondary); font-weight: 600; margin-bottom: var(--space-1); }
  legend.step { display: flex; align-items: center; gap: var(--space-3); font-size: var(--type-heading); font-weight: 700; margin-bottom: var(--space-2); }
  .step-num {
    flex: none;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    inline-size: var(--space-6);
    block-size: var(--space-6);
    border-radius: 999px;
    background: color-mix(in srgb, var(--accent) 22%, var(--card-bg));
    color: var(--accent-text);
    font-size: var(--type-secondary);
    font-weight: 700;
  }
  .fieldset-hint { margin: 0; color: var(--muted); font-size: var(--type-secondary); }
</style>
