<script lang="ts">
  import { parseLightMarkdown, type Inline } from "../lib/lightMarkdown";

  // The owner's light-markdown text as Svelte markup. The parser returns a token tree and this component
  // builds elements from it: user text only ever reaches the DOM as a text node, never through {@html}, and a
  // link exists only for an https: or mailto: address (the parser drops every other URL to literal text).
  let { text }: { text: string } = $props();

  const blocks = $derived(parseLightMarkdown(text));
</script>

{#snippet inline(nodes: Inline[])}
  {#each nodes as n, i (i)}
    {#if n.t === "text"}{n.v}{:else if n.t === "strong"}<strong>{@render inline(n.c)}</strong>{:else if n.t === "em"}<em>{@render inline(n.c)}</em>{:else if n.href.slice(0, 7).toLowerCase() === "mailto:"}<a href={n.href}>{@render inline(n.c)}</a>{:else}<a href={n.href} target="_blank" rel="noopener noreferrer">{@render inline(n.c)}</a>{/if}
  {/each}
{/snippet}

{#each blocks as b, bi (bi)}
  {#if b.t === "p"}
    <p class="rt-p">
      {#each b.lines as line, li (li)}{#if li > 0}<br />{/if}{@render inline(line)}{/each}
    </p>
  {:else}
    <ul class="rt-ul">
      {#each b.items as item, ii (ii)}<li>{@render inline(item)}</li>{/each}
    </ul>
  {/if}
{/each}

<style>
  .rt-p { margin: 0; overflow-wrap: anywhere; }
  .rt-p + .rt-p, .rt-p + .rt-ul, .rt-ul + .rt-p, .rt-ul + .rt-ul { margin-block-start: var(--space-2); }
  .rt-ul { margin: 0; padding-inline-start: var(--space-5); overflow-wrap: anywhere; }
  a { color: var(--accent-text); text-decoration: underline; text-underline-offset: 0.15em; }
</style>
