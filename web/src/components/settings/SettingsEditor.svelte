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
  import { announce } from "../../lib/a11y.svelte";
  import { stickyInset } from "../../lib/stickyInset";
  import ErrorSummary from "../ErrorSummary.svelte";
  import Card from "../ui/Card.svelte";
  import { findFieldByLabel, errorLabel, type ErrorTarget } from "../../lib/fieldErrors";
  import { fieldLabel, hasFieldLabel } from "../../lib/format";
  import { refocusIfLost } from "../../lib/a11yFocus";
  import { registerPageSave } from "../../lib/pageShortcuts.svelte";
  import { scheduleProblems, splitOvernightWindows } from "../../lib/scheduleUi";

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
          // For state the server changed outside the Save button (the shop's hero image has its own
          // endpoints): applies the same edit to the draft and to the saved baseline, so it never reads as an
          // unsaved change and a later discard keeps it.
          sync: (mutate: (config: ServerConfig) => void) => void;
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

  // Ctrl/Cmd+S saves the draft (App.svelte's handler); a no-op while there is nothing to save.
  $effect(() => registerPageSave(() => { if (dirty) void save(); }));

  // The time-of-use schedule is validated here as well as on the server, so Save stays blocked (with the
  // reason) instead of posting a request that comes back 422. Overnight windows pass: they are split below.
  const scheduleBlockers = $derived(form ? scheduleProblems(form.energy.schedule, form.energy.mode === "schedule") : []);
  const blocked = $derived(dirty && scheduleBlockers.length > 0);

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
    if (!form || saving) return;
    if (blocked) {
      announce("Fix the highlighted energy schedule problems before saving.");
      return;
    }
    // Windows that run past midnight are stored as two windows (the server requires start before end).
    const { schedule: normalized, split } = splitOvernightWindows(form.energy.schedule);
    if (split > 0) form.energy.schedule = normalized;
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
      savedNotice = split > 0 ? `Settings saved. ${split === 1 ? "A window that ran past midnight was" : `${split} windows that ran past midnight were`} split at midnight.` : "Settings saved.";
      // The save bar unmounts now that the form is clean; keep focus on the saved notice (WCAG 2.4.3).
      // preventScroll: the notice sits at the end of the page, and a plain focus() jumped there from the card the
      // user had just edited (A5). Focus still lands on the notice, a role=status region, so it is announced.
      void refocusIfLost("[data-settings-status]", { preventScroll: true });
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

  function sync(mutate: (config: ServerConfig) => void) {
    if (!form || !original) return;
    mutate(form);
    mutate(original);
  }

  function discard() {
    if (!original) return;
    // Same proxy caveat as save(): snapshot first; it is already detached from `original`.
    form = $state.snapshot(original) as ServerConfig;
    errors = [];
    savedNotice = "";
    announce("Changes discarded.");
    void refocusIfLost("[data-settings-status]", { preventScroll: true });
  }

  // A save error names a config path ("pricing.marginPct: ..."). Link it to its field when the path has a field
  // label, else to the first control of the card that holds it (errorSection), so the summary always leads somewhere.
  function resolveSettingsError(message: string): ErrorTarget | null {
    const path = errorLabel(message);
    // A path ("pricing.marginPct") maps to its label; a message that already leads with a label ("Accent color") is looked up as is.
    const named = path ? findFieldByLabel(hasFieldLabel(path) ? fieldLabel(path) : path) : null;
    if (named) return { el: named, field: true };
    const card = document.getElementById(`section-${errorSection(message)}`);
    const first = card?.querySelector<HTMLElement>("input:not([type=hidden]), select, textarea, button");
    return first ? { el: first, field: false } : null;
  }
</script>

<div class="settings-editor">
  {#if loading}
    <p role="status">Loading…</p>
  {:else if loadFailed}
    <Card class="banner-error">
      <p>Couldn't load settings from the server. Check that printpaca's backend is running.</p>
      <button type="button" onclick={refresh}>Retry</button>
    </Card>
  {:else if form && original && builtin}
    {#if loadErrorMsg}
      <Card class="banner-warn">
        <p>config.yaml on disk is invalid, so this page ignores it: {loadErrorMsg}. Saving overwrites it.</p>
      </Card>
    {/if}

    {@render children({ form, builtin, original, hasError, sync })}

    <p class="hint" role="status" tabindex="-1" data-settings-status>{dirty ? "" : savedNotice}</p>

    {#if dirty || errors.length > 0}
      <div class="save-bar" use:stickyInset={{ edge: "bottom" }}>
        <ErrorSummary {errors} resolve={resolveSettingsError} />
        {#if blocked}
          <p class="warn-text" id="save-blocked">Fix the highlighted problems in the energy schedule to save ({scheduleBlockers.length} to fix).</p>
        {/if}
        <div class="save-actions">
          <button type="button" onclick={() => { if (!saving) discard(); }} disabled={!dirty} aria-disabled={saving} aria-busy={saving}>Discard changes</button>
          <button type="button" class="primary" onclick={save} aria-disabled={saving || blocked} aria-describedby={blocked ? "save-blocked" : undefined} aria-busy={saving}>{saving ? "Saving…" : "Save changes"}</button>
        </div>
      </div>
    {/if}
  {/if}
</div>

<style>
  .settings-editor { display: flex; flex-direction: column; gap: var(--gap-section); }

  /* Sticky to the bottom of the scroll container (<main> at >=900px, the window below that), flush with its
   * edge because <main> has no padding. It spans the same content column as the tab's cards. This component
   * doesn't own the page layout (each embedding tab does), so it is self-contained: a card tint instead of an
   * outline (D6), and its own padding. */
  .save-bar {
    position: sticky;
    bottom: 0;
    z-index: 2;
    background: var(--card-bg);
    border-radius: var(--radius) var(--radius) 0 0;
    padding: var(--space-3) var(--space-4);
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }
  .save-actions { display: flex; flex-wrap: wrap; justify-content: flex-end; gap: var(--space-2); }
  /* The save bar's flex gap already spaces the error list; theme's .errors bottom margin would double it. */
  .save-bar :global(.errors) { margin-bottom: 0; }
</style>
