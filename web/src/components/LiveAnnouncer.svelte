<script lang="ts">
  import { announcer } from "../lib/a11y.svelte";
</script>

<!-- Rendered once per app root; announce() in lib/a11y.svelte.ts writes the text. -->
<div class="visually-hidden" role="status" aria-live="polite" aria-atomic="true">{announcer.polite}</div>
<div class="visually-hidden" role="alert" aria-live="assertive" aria-atomic="true">{announcer.assertive}</div>
