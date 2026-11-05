<script lang="ts">
  import LiveAnnouncer from "../components/LiveAnnouncer.svelte";
  import PublicQuotePage from "./PublicQuotePage.svelte";
  import { setHintMode } from "../components/ui/hintMode";

  // The customer shop keeps every field hint visible; only the admin app turns hints into tooltips.
  setHintMode("visible");

  // The server serves this entry at both "/" and "/order" with the same HTML, and both show the shop, so there
  // is no router: the storefront is the landing page, and /order stays only so old links keep working.
</script>

<LiveAnnouncer />

<PublicQuotePage />
