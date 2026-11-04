<script lang="ts">
  import LiveAnnouncer from "../components/LiveAnnouncer.svelte";
  import PublicQuotePage from "./PublicQuotePage.svelte";

  // The server serves this entry at both "/" and "/order" with the same HTML, and both show the shop, so there
  // is no router: the storefront is the landing page, and /order stays only so old links keep working.
</script>

<LiveAnnouncer />

<PublicQuotePage />
