<script lang="ts">
  import { appConfig } from "../lib/state.svelte";
  import { demoLocal } from "../lib/demoLocal.svelte";
  import { extractRealMultiUser, attributionText } from "../lib/attribution";

  // Shared "Created by X · Updated by Y" line for every detail view (requests show updated-by only, no
  // createdBy), so the visibility rule can't drift between call sites. Two presentation-only gates: real
  // single-user mode (config.users.multiUser === false) hides it, since stamps are still written as "admin"
  // and showing them is noise; in the demo (seeded multiUser: true) the banner's "User mode" toggle hides it
  // when set to "single".
  let { createdBy = "", updatedBy = "" }: { createdBy?: string; updatedBy?: string } = $props();

  const demoMode = $derived(appConfig.current?.demoMode === true);
  const realMultiUser = $derived(extractRealMultiUser(appConfig.current));
  const presentable = $derived(realMultiUser && (!demoMode || demoLocal.userMode === "multi"));
  const text = $derived(presentable ? attributionText(createdBy, updatedBy) : null);
</script>

{#if text}
  <p class="hint attribution-line">{text}</p>
{/if}

<style>
  /* .hint (theme.css) supplies the muted look; only the vertical rhythm under a heading is local, tighter
   * than .hint's default bottom margin. */
  .attribution-line { margin: 0.1rem 0 0.6rem; }
</style>
