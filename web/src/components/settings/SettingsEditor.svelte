<script lang="ts">
  import { onDestroy, onMount, type Snippet } from "svelte";
  import {
    cloneConfig,
    configsEqual,
    errorSection,
    loadSettings,
    saveSettings,
    type ServerConfig,
  } from "../../lib/settings";
  import { loadConfig } from "../../lib/api";
  import { appConfig } from "../../lib/state.svelte";
  import { settingsDirty } from "../../lib/settingsDirty.svelte";

  // Reusable form-editing shell for SettingsTab and every tab that embeds a settings card (ElectricityTab,
  // FilamentTab, PrintersTab, ...). Only one instance is mounted at a time (tabs unmount on switch), so there
  // is no concurrent-editor state.
  let {
    children,
    form = $bindable(null),
  }: {
    children: Snippet<
      [
        {
          form: ServerConfig;
          builtin: ServerConfig;
          // The last successfully saved config (or the just-loaded one). Its identity changes only on a
          // successful save() or the initial refresh(), never on discard(), so a card can react to "a save
          // just changed section X" (IntegrationsSettings' restart notice) instead of seeing only the live,
          // possibly dirty `form`.
          original: ServerConfig;
          hasError: (section: string) => boolean;
        },
      ]
    >;
    // Escape hatch for a caller that must read or write the draft outside the children snippet (FilamentTab's
    // spool-price card, a sibling of <SettingsEditor>), so dirty-tracking and the save bar react as to a
    // direct edit. Optional; other callers use child-local state.
    form?: ServerConfig | null;
  } = $props();

  let loading = $state(true);
  let loadFailed = $state(false);

  // original/builtin (and form, for callers not binding it) are null until the first successful load;
  // children renders only once all three are non-null, so the snippet never sees a null form/builtin.
  let original = $state<ServerConfig | null>(null);
  let builtin = $state<ServerConfig | null>(null);

  let loadErrorMsg = $state<string | undefined>(undefined);
  let errors = $state<string[]>([]);
  let saving = $state(false);
  let savedNotice = $state("");

  const dirty = $derived(form !== null && original !== null && !configsEqual(form, original));

  // Mirrors `dirty` onto the module box App.svelte's activate() reads before switching tabs
  // (settingsDirty.svelte.ts), and clears it on destroy: an unmount always discards the in-progress edit.
  $effect(() => {
    settingsDirty.current = dirty;
  });
  onDestroy(() => {
    settingsDirty.current = false;
  });

  function hasError(section: string): boolean {
    return errors.some((e) => errorSection(e) === section);
  }

  async function refresh() {
    loading = true;
    loadFailed = false;
    const payload = await loadSettings();
    if (!payload) {
      loadFailed = true;
      loading = false;
      return;
    }
    original = payload.config;
    form = cloneConfig(payload.config);
    builtin = payload.builtin;
    loadErrorMsg = payload.loadError;
    errors = [];
    loading = false;
  }

  onMount(refresh);

  async function save() {
    if (!form) return;
    saving = true;
    savedNotice = "";
    // $state.snapshot, not cloneConfig: form is a $state deep proxy and structuredClone throws DataCloneError
    // on proxies. The snapshot is already a detached deep copy, so it doubles as the new `original`.
    const snapshot = $state.snapshot(form) as ServerConfig;
    const result = await saveSettings(snapshot);
    saving = false;
    if (result.ok) {
      original = snapshot;
      errors = [];
      savedNotice = "Settings saved.";
      // Refresh appConfig so every mounted tab sees what /api/config now serves (the server applies a saved
      // config immediately). Deliberately not applyConfig(fresh): that seeds QuoteTab's form once, on first
      // load (the `seeded` guard in state.svelte.ts), and would clobber what the user has mid-typed. A failed
      // refetch is non-fatal.
      const fresh = await loadConfig();
      if (fresh) {
        appConfig.current = fresh;
      }
    } else {
      errors = result.errors;
    }
  }

  function discard() {
    if (!original) return;
    // Same proxy caveat as save(): snapshot first; it is already detached from `original`.
    form = $state.snapshot(original) as ServerConfig;
    errors = [];
  }
</script>

<div class="settings-editor">
  {#if loading}
    <p>Loading…</p>
  {:else if loadFailed}
    <div class="card banner-error">
      <p>Couldn't load settings from the server. Check that printpaca's backend is running.</p>
      <button type="button" onclick={refresh}>Retry</button>
    </div>
  {:else if form && original && builtin}
    {#if loadErrorMsg}
      <div class="card banner-warn">
        <p>config.yaml on disk is invalid, so this page ignores it: {loadErrorMsg}. Saving overwrites it.</p>
      </div>
    {/if}

    {@render children({ form, builtin, original, hasError })}

    <p class="hint" role="status">{dirty ? "" : savedNotice}</p>

    {#if dirty || errors.length > 0}
      <div class="save-bar">
        {#if errors.length > 0}
          <ul class="errors" role="alert">
            {#each errors as e (e)}<li>{e}</li>{/each}
          </ul>
        {/if}
        <div class="save-actions">
          <button type="button" onclick={discard} disabled={!dirty || saving}>Discard changes</button>
          <button type="button" class="primary" onclick={save} disabled={saving}>{saving ? "Saving…" : "Save changes"}</button>
        </div>
      </div>
    {/if}
  {/if}
</div>

<style>
  .settings-editor { display: flex; flex-direction: column; gap: 1.5rem; }

  /* Sticky to the viewport bottom, not a `.page` wrapper: this component doesn't own the page layout (each
   * embedding tab does), so it is self-contained with its own top border and padding, no negative margins. */
  .save-bar {
    position: sticky;
    bottom: 0;
    background: var(--surface);
    border-top: 1px solid var(--border);
    border-radius: var(--radius) var(--radius) 0 0;
    padding: 0.75rem 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }
  .save-actions { display: flex; justify-content: flex-end; gap: 0.5rem; }
  /* The save bar's flex gap already spaces the error list; theme's .errors bottom margin would double it. */
  .save-bar .errors { margin-bottom: 0; }
</style>
