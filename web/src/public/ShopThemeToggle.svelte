<script lang="ts">
  import { mdiWeatherNight, mdiWhiteBalanceSunny } from "@mdi/js";
  import Icon from "../components/Icon.svelte";
  import { announce } from "../lib/a11y.svelte";
  import { prefersDark } from "./shopAccent";
  import { setShopTheme } from "./shopTheme";

  // The button names the theme it switches to: a moon and "Dark mode" while light shows, a sun and
  // "Light mode" while dark shows (the same as the admin toggle). The label carries the state, so there is
  // no aria-pressed. With no saved choice the device setting decides which one shows first.
  // <html data-theme> is the source of truth (the server set it from the cookie); a MutationObserver keeps the
  // label right when something else changes it.
  const mq = window.matchMedia("(prefers-color-scheme: dark)");
  const root = document.documentElement;
  let dark = $state(prefersDark(root, mq));

  $effect(() => {
    const sync = () => (dark = prefersDark(root, mq));
    sync();
    mq.addEventListener("change", sync);
    const mo = new MutationObserver(sync);
    mo.observe(root, { attributes: true, attributeFilter: ["data-theme"] });
    return () => {
      mq.removeEventListener("change", sync);
      mo.disconnect();
    };
  });

  function toggle() {
    const next = dark ? "light" : "dark";
    setShopTheme(next);
    announce(next === "dark" ? "Dark mode on" : "Light mode on");
  }
</script>

<!-- On phones the label hides to keep the name on one line; it shows while the button has keyboard focus. -->
<button type="button" class="theme-toggle" onclick={toggle}>
  <Icon path={dark ? mdiWhiteBalanceSunny : mdiWeatherNight} size={18} />
  <span class="label">{dark ? "Light mode" : "Dark mode"}</span>
</button>

<style>
  .theme-toggle {
    display: inline-flex;
    align-items: center;
    gap: var(--space-2);
    flex: none;
    margin-left: auto;
    min-height: 44px;
    padding: 0 var(--space-3);
  }
  @media (max-width: 600px) {
    .theme-toggle { min-width: 44px; justify-content: center; }
    /* Collapsed to zero width but still in the accessibility tree, so the name stays "Dark mode" or "Light mode". */
    .theme-toggle { gap: 0; }
    .theme-toggle .label { max-width: 0; overflow: hidden; white-space: nowrap; }
    .theme-toggle:focus-visible { gap: var(--space-2); }
    .theme-toggle:focus-visible .label { max-width: none; }
  }
</style>
