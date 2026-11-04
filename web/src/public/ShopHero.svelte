<script lang="ts">
  import type { PublicShop } from "./api";

  // The owner's banner image (shop.heroImage), shown full width of the reading column with its required alt text.
  // A fixed ratio and a height cap keep it from pushing the items off the first screen; in a short landscape
  // viewport it is hidden outright (the plan's "hidden if it would push the items below the first screen").
  let { hero }: { hero: NonNullable<PublicShop["heroImage"]> } = $props();
</script>

<img class="shop-hero" src={hero.url} alt={hero.alt} />

<style>
  .shop-hero {
    display: block;
    width: 100%;
    aspect-ratio: 3 / 1;
    max-height: 14rem;
    object-fit: cover;
    border-radius: var(--radius);
    background: var(--surface-2);
  }
  @media (max-height: 500px) {
    .shop-hero { display: none; }
  }
</style>
