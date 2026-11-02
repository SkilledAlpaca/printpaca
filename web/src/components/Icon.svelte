<script lang="ts">
  // Thin wrapper around one MDI path (@mdi/js, Apache-2.0): the 24x24 viewBox, fill and accessibility wiring
  // live here. Decorative by default (aria-hidden); pass `label` when the icon is the control's only
  // accessible content.
  let { path, size = 18, label }: { path: string; size?: number; label?: string } = $props();
</script>

<svg
  class="mdi-icon"
  width={size}
  height={size}
  viewBox="0 0 24 24"
  fill="currentColor"
  role={label ? "img" : undefined}
  aria-label={label}
  aria-hidden={label ? undefined : "true"}
>
  <path d={path} />
</svg>

<style>
  .mdi-icon { flex-shrink: 0; display: block; }
</style>
