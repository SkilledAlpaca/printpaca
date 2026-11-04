<script lang="ts">
  import type { Snippet } from "svelte";

  // One card groups one task (plan: "a simple page doesn't get a decorative wrapper"). Soft tint, no outline
  // (D6): the look is the global .card rule in theme.css, which unmigrated screens share. `tone="inset"` is a
  // block nested inside a card, tinted with the page color. `heading` renders an h2 for the card; use `as`
  // for a form or a labelled section, and pass role/aria-* straight through.
  type Tag = "div" | "section" | "form" | "article";
  let {
    as = "div",
    tone = "default",
    heading,
    el = $bindable(),
    children,
    ...rest
  }: {
    as?: Tag;
    tone?: "default" | "inset";
    heading?: string;
    el?: HTMLElement;
    children: Snippet;
    [attr: string]: unknown;
  } = $props();
</script>

<svelte:element this={as} class="card" class:inset={tone === "inset"} bind:this={el} {...rest}>
  {#if heading}<h2 class="card-heading">{heading}</h2>{/if}
  {@render children()}
</svelte:element>

<style>
  .inset { background: var(--inset-bg); }
  .card-heading { margin-top: 0; }
</style>
