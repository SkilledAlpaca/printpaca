<script lang="ts">
  import { tick } from "svelte";
  import { announce } from "../lib/a11y.svelte";
  import { clearVisitor, visitor } from "./visitor.svelte";

  /** "Not you?" for a form whose Name and Email came prefilled from the visitor store (an earlier estimate or
   * order in this tab). It is decided once, when the form opens: it never appears because the visitor is typing,
   * so it can't take a tab stop between Email and Note. It sits above Name, ahead of the fields it clears. */
  let { nameId }: { nameId: string } = $props();

  const prefilled = visitor.name !== "" || visitor.email !== "";
  let undo = $state<{ name: string; email: string } | null>(null);

  async function clear() {
    undo = { name: visitor.name, email: visitor.email };
    clearVisitor();
    announce("Name and email cleared.");
    await tick();
    document.getElementById(nameId)?.focus();
  }

  async function restore() {
    if (!undo) return;
    visitor.name = undo.name;
    visitor.email = undo.email;
    undo = null;
    announce("Name and email restored.");
    await tick();
    document.getElementById(nameId)?.focus();
  }
</script>

{#if prefilled}
  <div class="visitor-reset">
    {#if undo}
      <span class="cleared">Name and email cleared.</span>
      <button type="button" class="quiet inline-link not-you" onclick={restore}>Undo</button>
    {:else}
      <button type="button" class="quiet inline-link not-you" onclick={clear}>Not you? Clear name and email</button>
    {/if}
  </div>
{/if}

<style>
  .visitor-reset { display: flex; align-items: center; gap: var(--space-2); flex-wrap: wrap; }
  .cleared { color: var(--muted); font-size: var(--text-sm); }
  /* A target of at least 24 px at every width; phones get 44 px. */
  .not-you { min-height: 24px; }
  @media (max-width: 600px) {
    .not-you { min-height: 44px; }
  }
</style>
