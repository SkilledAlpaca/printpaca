<script lang="ts">
  import type { PublicCategory } from "./api";
  import { routeHash } from "./route";

  // All plus one chip per visible category that has public items, linking to #/category/<slug>. Links, not
  // buttons, because the shop routes by location.hash and a chip is a place you can share or reload.
  let { categories, active = null }: { categories: PublicCategory[]; active?: string | null } = $props();
</script>

{#if categories.length > 0}
  <nav class="chips-nav" aria-label="Categories">
    <ul>
      <li><a class="chip" href="#/" aria-current={active === null ? "page" : undefined}>All</a></li>
      {#each categories as c (c.slug)}
        <li><a class="chip" href={routeHash({ name: "category", slug: c.slug })} aria-current={active === c.slug ? "page" : undefined}>{c.name}</a></li>
      {/each}
    </ul>
  </nav>
{/if}

<style>
  ul { list-style: none; margin: 0; padding: 0; display: flex; flex-wrap: wrap; gap: var(--space-2); }
  .chip {
    display: inline-flex;
    align-items: center;
    min-height: 44px;
    padding: 0 var(--space-4);
    border-radius: 999px;
    background: var(--card-bg);
    color: var(--text);
    text-decoration: none;
    font-size: var(--type-secondary);
    overflow-wrap: anywhere;
  }
  .chip:hover { background: var(--surface-2); }
  .chip[aria-current="page"] { background: var(--accent); color: var(--accent-contrast); font-weight: 600; }
  .chip:focus-visible { outline-offset: 2px; }
  .chip[aria-current="page"]:focus-visible { outline-color: var(--accent-contrast); outline-offset: -4px; }
</style>
