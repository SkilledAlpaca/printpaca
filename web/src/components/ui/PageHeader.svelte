<script lang="ts">
  import type { Snippet } from "svelte";
  import { mdiArrowLeft } from "@mdi/js";
  import Icon from "../Icon.svelte";
  import { focusOnMount } from "../../lib/a11yFocus";
  import { recordTitle } from "../../lib/recordTitle";

  // The head of a view: an optional Back action, the view's single h1, an optional status next to it, and an
  // optional subtitle. Back is a button that calls `onback`, so the caller keeps its own guard (an unsaved-changes
  // check) and its own focus restoration; it sits before the h1 in the DOM so Shift+Tab from the title reaches it.
  // `record` marks a record view: the h1 takes focus when it appears (WCAG 2.4.3) and mirrors its text into
  // document.title (WCAG 2.4.2), as DetailTitle does. Without a title only the Back row renders, which covers a
  // record that is still loading.
  let {
    title,
    onback,
    backLabel = "Back to list",
    record = false,
    status,
    subtitle,
  }: {
    title?: string;
    onback?: () => void;
    backLabel?: string;
    record?: boolean;
    status?: Snippet;
    subtitle?: Snippet;
  } = $props();
</script>

<div class="page-header">
  {#if onback}
    <button type="button" class="quiet back" onclick={onback}><Icon path={mdiArrowLeft} /> {backLabel}</button>
  {/if}
  {#if title}
    <div class="title-row">
      {#if record}
        <h1 tabindex="-1" use:focusOnMount use:recordTitle>{title}</h1>
      {:else}
        <h1 tabindex="-1">{title}</h1>
      {/if}
      {#if status}{@render status()}{/if}
    </div>
    {#if subtitle}{@render subtitle()}{/if}
  {/if}
</div>

<style>
  .page-header { display: flex; flex-direction: column; align-items: flex-start; gap: var(--space-2); }
  .back { display: inline-flex; align-items: center; gap: var(--space-1); }
  .title-row { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2) var(--space-4); }
  h1 { margin: 0; font-size: var(--type-title); }
</style>
