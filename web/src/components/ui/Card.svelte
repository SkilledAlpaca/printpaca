<script lang="ts">
  import type { Snippet } from "svelte";

  // One card groups one task (plan: "a simple page doesn't get a decorative wrapper"). Soft tint, no outline:
  // the look is the global .card rule in theme.css, which unmigrated screens share. `tone="inset"` is a
  // block nested inside a card, tinted with the page color. `heading` renders an h2 for the card; use `as`
  // for a form or a labelled section, and pass role/aria-* straight through. `tone="danger"` marks a card that holds
  // a rejected field with a left border. A caller's `class` is added to the card's own.
  type Tag = "div" | "section" | "form" | "article";
  let {
    as = "div",
    tone = "default",
    class: extra = "",
    heading,
    el = $bindable(),
    children,
    ...rest
  }: {
    as?: Tag;
    tone?: "default" | "inset" | "danger";
    class?: string;
    heading?: string;
    el?: HTMLElement;
    children: Snippet;
    [attr: string]: unknown;
  } = $props();
</script>

<svelte:element this={as} class={["card", extra]} class:inset={tone === "inset"} class:danger={tone === "danger"} bind:this={el} {...rest}>
  {#if heading}<h2 class="card-heading">{heading}</h2>{/if}
  {@render children()}
</svelte:element>

<style>
  .inset { background: var(--inset-bg); }
  /* A left border is the severity marker, not a full red card: "this section holds a rejected field". */
  .danger { border-left: 3px solid var(--danger); }
  .card-heading { margin-top: 0; }
</style>
