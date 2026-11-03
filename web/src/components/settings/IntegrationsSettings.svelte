<script lang="ts">
  import type { ServerConfig } from "../../lib/settings";
  import { testIntegration } from "../../lib/sendApi";
  import { appConfig } from "../../lib/state.svelte";

  // Restart-to-apply: proxy targets are resolved once at boot (server/proxy.go's registerProxies), so a saved
  // change to any URL below needs a container restart, unlike every other Settings card. The restart notice
  // exists for that reason.
  let {
    form,
    original,
    hasError,
  }: {
    form: ServerConfig;
    original: ServerConfig;
    hasError: (section: string) => boolean;
  } = $props();

  type Kind = "spoolman" | "moonraker" | "fluidd";

  const ROWS: {
    kind: Kind;
    label: string;
    field: "spoolmanUrl" | "moonrakerUrl" | "fluiddUrl";
    envVar: string;
    placeholder: string;
    testable: boolean;
  }[] = [
    { kind: "spoolman", label: "Spoolman", field: "spoolmanUrl", envVar: "SPOOLMAN_URL", placeholder: "http://spoolman.local:7912", testable: true },
    { kind: "moonraker", label: "Moonraker", field: "moonrakerUrl", envVar: "MOONRAKER_URL", placeholder: "http://printer.local:7125", testable: true },
    { kind: "fluidd", label: "Fluidd", field: "fluiddUrl", envVar: "FLUIDD_URL", placeholder: "http://printer.local", testable: false },
  ];

  const demoMode = $derived(appConfig.current?.demoMode === true);

  // "unset" until the first config response lands.
  function sourceFor(kind: Kind): "env" | "config" | "unset" {
    return appConfig.current?.integrationSources[kind] ?? "unset";
  }

  // Spoolman and Moonraker are the two integrations DEMO_MODE fakes with no real URL (server/main.go's
  // deriveIntegrations/deriveMoonraker); Fluidd is link-only with no demo fallback, so it never gets this
  // badge.
  function isDemoFaked(kind: Kind): boolean {
    return demoMode && kind !== "fluidd" && sourceFor(kind) === "unset";
  }

  function badgeFor(kind: Kind): string {
    if (isDemoFaked(kind)) return "Configured via demo data";
    switch (sourceFor(kind)) {
      case "env":
        return "Configured via environment";
      case "config":
        return "Configured in settings";
      default:
        return "Not configured";
    }
  }

  function badgeOk(kind: Kind): boolean {
    return isDemoFaked(kind) || sourceFor(kind) !== "unset";
  }

  // The environment variable always wins (server/proxy.go's resolveIntegrationURL), so editing the field
  // while it's set would save but never take effect. Demo mode locks every field regardless of source
  // (read-only instance; demoWriteGuard rejects writes).
  function locked(kind: Kind): boolean {
    return demoMode || sourceFor(kind) === "env";
  }

  // Restart notice: fires only when a save just changed one of the three fields, not merely on render or
  // another section's save. `original`'s identity changes only on a successful SettingsEditor save (or
  // initial load), never on discard(). Comparing by JSON lets edit-revert-save no-op. lastKnown starts null
  // so the first effect run sets the baseline without a spurious notice.
  let lastKnown = $state<string | null>(null);
  let showRestartNotice = $state(false);
  $effect(() => {
    const current = JSON.stringify(original.integrationsUrls);
    if (lastKnown === null) {
      lastKnown = current;
      return;
    }
    if (current !== lastKnown) {
      lastKnown = current;
      showRestartNotice = true;
    }
  });

  // Per-row test state keyed by kind, so ROWS render through one #each instead of three duplicated Test
  // buttons.
  let testing = $state<Partial<Record<Kind, boolean>>>({});
  let testResult = $state<Partial<Record<Kind, { ok: boolean; detail: string }>>>({});

  async function runTest(kind: "spoolman" | "moonraker", url: string) {
    if (testing[kind]) return;
    testing[kind] = true;
    testResult[kind] = undefined;
    try {
      const result = await testIntegration(kind, url);
      testResult[kind] = result.ok ? result.data : { ok: false, detail: result.error };
    } catch (err) {
      testResult[kind] = { ok: false, detail: err instanceof Error ? err.message : "Couldn't reach the server." };
    } finally {
      testing[kind] = false;
    }
  }
</script>

<div id="section-integrationsUrls" class="card roomy" class:danger-border={hasError("integrationsUrls")}>
  <h2>Integrations</h2>
  <p class="hint">
    Spoolman, Moonraker, and Fluidd URLs. An environment variable on the container always overrides the value
    here. Saving a change here needs a container restart to take effect. Every other setting on this page applies
    immediately, because printpaca connects to them only at startup.
  </p>
  {#if demoMode}
    <p class="banner-warn" role="status">
      This is a read-only demo: Spoolman and Moonraker serve sample data regardless of the fields below, which are
      disabled and can't be tested.
    </p>
  {/if}
  {#if showRestartNotice}
    <p class="banner-warn" role="status">Saved. Restart the container to apply integration changes.</p>
  {/if}

  {#each ROWS as row (row.kind)}
    <div class="subcard">
      <div class="subhead">
        <h3>{row.label}</h3>
        <span class="pill" class:pill-ok={badgeOk(row.kind)}>{badgeFor(row.kind)}</span>
      </div>
      <label class="field">
        <span>URL</span>
        <input
          aria-label={`${row.label} URL`}
          bind:value={form.integrationsUrls[row.field]}
          placeholder={row.placeholder}
          disabled={locked(row.kind)}
        />
        {#if sourceFor(row.kind) === "env"}
          <small class="field-hint">{row.envVar} is set on the container and overrides this field.</small>
        {/if}
      </label>
      {#if row.testable}
        {@const kind = row.kind as "spoolman" | "moonraker"}
        <div class="test-row">
          <button
            type="button"
            onclick={() => runTest(kind, form.integrationsUrls[row.field])}
            disabled={demoMode || locked(row.kind) || form.integrationsUrls[row.field] === ""}
            aria-disabled={testing[kind] === true} aria-busy={testing[kind] === true}
          >
            {testing[kind] ? "Testing…" : "Test"}
          </button>
          <span
            class="status-line"
            role={testResult[kind] && !testResult[kind]?.ok ? "alert" : "status"}
            aria-live={testResult[kind] && !testResult[kind]?.ok ? "assertive" : "polite"}
          >
            {testResult[kind]?.detail ?? ""}
          </span>
        </div>
      {/if}
    </div>
  {/each}
</div>

<style>
  .test-row { display: flex; align-items: center; gap: 0.6rem; margin-top: var(--space-2); }
  .subcard:not(:last-child) { margin-bottom: 1rem; }

  /* .pill (theme.css) is neutral by default; this card tints "something answers here" (env, config or the
   * demo's fake data) against "not configured", mirroring StatusBadge's --ok color-mix formula rather than
   * a new literal color. */
  .pill-ok {
    color: var(--ok);
    background: color-mix(in srgb, var(--ok) 18%, var(--surface));
    border-color: var(--ok);
  }
</style>
