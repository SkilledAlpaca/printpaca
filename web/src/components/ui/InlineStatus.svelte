<script lang="ts">
  import type { Snippet } from "svelte";

  // The four states of an action, kept distinct (plan): "saving" (in progress), "invalid" (local validation
  // failed), "error" (the server or network failed), "success". Each is text plus a colored marker, never color
  // alone. Only "error" is announced assertively (role="alert"); the others are polite status messages.
  // Render it only while there is something to say; callers keep their own persistent live regions when a
  // message must be announced on first appearance.
  let { kind, children }: { kind: "saving" | "invalid" | "error" | "success"; children: Snippet } = $props();
</script>

<p class="inline-status {kind}" role={kind === "error" ? "alert" : "status"}>{@render children()}</p>

<style>
  .inline-status {
    margin: 0;
    padding: var(--space-2) var(--space-3);
    border-inline-start: 4px solid var(--status, var(--muted));
    border-radius: calc(var(--radius) / 2);
    background: color-mix(in srgb, var(--status, var(--muted)) 12%, var(--card-bg));
    color: var(--text);
    font-size: var(--type-secondary);
  }
  .saving { --status: var(--muted); }
  .invalid, .error { --status: var(--danger); }
  .success { --status: var(--ok); }
</style>
