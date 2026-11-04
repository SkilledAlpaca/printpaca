<script lang="ts">
  // One line above the submit button: how many fields need attention and a link to the first. The messages
  // themselves live under their fields, so each error appears once. The link moves focus; it does not
  // navigate (the page routes by location.hash).
  let { problems }: { problems: { id: string; label: string }[] } = $props();

  function focusField(e: MouseEvent, id: string) {
    e.preventDefault();
    const el = document.getElementById(id);
    el?.scrollIntoView({ block: "center" });
    el?.focus();
  }
</script>

{#if problems.length > 0}
  {@const first = problems[0]}
  <p class="warn-text error-summary">
    {problems.length === 1 ? "1 field needs attention:" : `${problems.length} fields need attention, starting with`}
    {#if first}<a href="#{first.id}" onclick={(e) => focusField(e, first.id)}>{first.label}</a>{/if}.
  </p>
{/if}
