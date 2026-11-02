<script lang="ts">
  import FrontDoor from "./FrontDoor.svelte";
  import PublicQuotePage from "./PublicQuotePage.svelte";
  import { publicViewFor } from "./publicRoute";

  // The server serves this entry at "/" and "/order" with the same HTML; the path picks the view. Each view
  // is a full page load (links between them are plain anchors), so there is no client-side router.
  const view = publicViewFor(location.pathname);
</script>

{#if view === "order"}
  <PublicQuotePage />
{:else}
  <FrontDoor />
{/if}
