<script lang="ts">
  import type { ServerConfig } from "../../lib/settings";
  import { loadSettings, saveSettings, newPrinter } from "../../lib/settings";
  import { loadConfig } from "../../lib/api";
  import { testIntegration } from "../../lib/sendApi";
  import { appConfig } from "../../lib/state.svelte";
  import { announce, focusAfterUpdate } from "../../lib/a11y.svelte";
  import Card from "../ui/Card.svelte";
  import Field from "../ui/Field.svelte";
  import FormGrid from "../ui/FormGrid.svelte";

  // Saved URLs apply at once: the server resolves them on every request (server/proxy.go's registerProxies).
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

  // Per-row test state keyed by kind, so ROWS render through one #each instead of three duplicated Test
  // buttons. `url` is the value that was tested: a result describes that URL only, so it is hidden once the
  // field changes.
  let testing = $state<Partial<Record<Kind, boolean>>>({});
  let testResult = $state<Partial<Record<Kind, { ok: boolean; detail: string; hostname?: string; url: string }>>>({});

  // What Test sends. A URL set by an environment variable is never shown in the (disabled) field, so the
  // request carries "" and the server probes the effective URL (server/proxy.go's handleIntegrationsTest).
  function testUrl(row: (typeof ROWS)[number]): string {
    return sourceFor(row.kind) === "env" ? "" : form.integrationsUrls[row.field];
  }
  function canTest(row: (typeof ROWS)[number]): boolean {
    return !demoMode && (sourceFor(row.kind) === "env" || form.integrationsUrls[row.field].trim() !== "");
  }

  function visibleResult(kind: "spoolman" | "moonraker", url: string) {
    const r = testResult[kind];
    return r && r.url === url ? r : undefined;
  }

  async function runTest(kind: "spoolman" | "moonraker", url: string) {
    if (testing[kind]) return;
    testing[kind] = true;
    testResult[kind] = undefined;
    try {
      const result = await testIntegration(kind, url);
      testResult[kind] = result.ok
        ? { ok: result.data.ok, detail: result.data.detail, hostname: result.data.hostname, url }
        : { ok: false, detail: result.error, url };
    } catch (err) {
      testResult[kind] = { ok: false, detail: err instanceof Error ? err.message : "Couldn't reach the server.", url };
    } finally {
      testing[kind] = false;
    }
  }

  // Auto-create a printer from Moonraker (v1.1 item 10). The prompt shows after a passing Moonraker test that
  // reported a hostname no printer is already named. "Not now" hides it for that URL; testing a different URL
  // (or the same one after an edit) shows it again.
  let dismissedUrl = $state<string | null>(null);
  let addingPrinter = $state(false);
  let addError = $state("");
  let addedName = $state("");

  const moonrakerResult = $derived(visibleResult("moonraker", form.integrationsUrls.moonrakerUrl));
  const hostnameToAdd = $derived.by(() => {
    const host = moonrakerResult?.ok ? (moonrakerResult.hostname ?? "").trim() : "";
    if (host === "" || dismissedUrl === moonrakerResult?.url) return "";
    const lower = host.toLowerCase();
    const known = [...form.printers, ...original.printers].some((p) => p.name.trim().toLowerCase() === lower);
    return known ? "" : host;
  });

  async function addPrinterFromMoonraker() {
    if (addingPrinter || hostnameToAdd === "") return;
    const name = hostnameToAdd;
    addingPrinter = true;
    addError = "";
    // Saved on its own, from the server's current config, so the click doesn't also save unrelated unsaved
    // edits in the form below.
    const payload = await loadSettings();
    if (!payload) {
      addError = "Couldn't load the saved settings, so the printer wasn't added. Try again.";
      addingPrinter = false;
      return;
    }
    const config = payload.config;
    const printer = newPrinter(config.printers.map((p) => p.id), name);
    config.printers = [...config.printers, printer];
    const saved = await saveSettings(config);
    addingPrinter = false;
    if (!saved.ok) {
      addError = `Couldn't add the printer: ${saved.errors.join(" ")}`;
      return;
    }
    // Keep the editor's draft and its saved baseline in step with the server, or the next Save changes
    // would drop the new printer.
    form.printers.push({ ...printer });
    original.printers.push({ ...printer });
    addedName = name;
    announce(`Added printer ${name}. Check its wattage, cost, and lifespan on the Printers tab.`);
    const fresh = await loadConfig();
    if (fresh) appConfig.current = fresh;
    void focusAfterUpdate("[data-printer-added]");
  }

  function dismissPrinterPrompt() {
    dismissedUrl = moonrakerResult?.url ?? null;
    announce("Printer prompt dismissed.");
    void focusAfterUpdate("[data-moonraker-test]");
  }
</script>

<Card id="section-integrationsUrls" tone={hasError("integrationsUrls") ? "danger" : "default"} heading="Integrations">
  <p class="hint">
    Spoolman, Moonraker, and Fluidd URLs. Saved changes apply right away, with no restart. An environment variable
    on the container always overrides the value here.
  </p>
  {#if demoMode}
    <p class="banner-warn" role="status">
      This is a read-only demo: Spoolman and Moonraker serve sample data regardless of the fields below, which are
      disabled and can't be tested.
    </p>
  {/if}

  <div class="stack">
    {#each ROWS as row (row.kind)}
      <Card tone="inset">
        <div class="subhead">
          <h3>{row.label}</h3>
          <span class="pill" class:pill-ok={badgeOk(row.kind)}>{badgeFor(row.kind)}</span>
        </div>
        <FormGrid>
          <Field
            id={`integration-${row.kind}-url`}
            label={`${row.label} URL`}
            size="l"
            hint={sourceFor(row.kind) === "env" ? `${row.envVar} is set on the container and overrides this field. Test checks that URL. Remove the variable to manage the URL here.` : undefined}
          >
            {#snippet children(a)}
              <input
                id={a.id}
                bind:value={form.integrationsUrls[row.field]}
                placeholder={row.placeholder}
                disabled={locked(row.kind)}
                aria-describedby={a.describedby}
              />
            {/snippet}
          </Field>
        </FormGrid>
        <p class="used-in">
          Used for:
          {#if row.kind === "spoolman"}
            <a href="#/quote">Spool picker on New quote</a> and <a href="#/filament">Spool inventory on the Filament tab</a>.
          {:else if row.kind === "moonraker"}
            <a href="#/quote">Recent prints on New quote</a>.
          {:else}
            A link to Fluidd in the sidebar.
          {/if}
        </p>
        {#if row.testable}
          {@const kind = row.kind as "spoolman" | "moonraker"}
          {@const result = visibleResult(kind, testUrl(row))}
          {@const testable = canTest(row)}
          <div class="test-row">
            <button
              type="button"
              data-moonraker-test={kind === "moonraker" ? "" : undefined}
              onclick={() => { if (testable) void runTest(kind, testUrl(row)); }}
              disabled={demoMode}
              aria-disabled={demoMode || testing[kind] === true || !testable}
              aria-describedby={!testable && !demoMode ? `test-help-${kind}` : undefined}
              aria-busy={testing[kind] === true}
            >
              {testing[kind] ? "Testing…" : "Test"}
            </button>
            {#if !testable && !demoMode}<small id={`test-help-${kind}`} class="field-hint">Enter a URL to test it.</small>{/if}
            <span
              class="status-line"
              class:test-ok={result?.ok}
              role={result && !result.ok ? "alert" : "status"}
              aria-live={result && !result.ok ? "assertive" : "polite"}
            >
              {result?.detail ?? ""}
            </span>
          </div>
          {#if kind === "moonraker"}
            {#if hostnameToAdd !== ""}
              <div class="printer-prompt" role="group" aria-label="Add printer from Moonraker">
                <p>Add printer '{hostnameToAdd}' to your printers?</p>
                <div class="prompt-actions">
                  <button type="button" class="primary" onclick={addPrinterFromMoonraker} aria-disabled={addingPrinter} aria-busy={addingPrinter}>
                    {addingPrinter ? "Adding…" : "Add printer"}
                  </button>
                  <button type="button" onclick={dismissPrinterPrompt}>Not now</button>
                </div>
                {#if addError}<p class="warn-text" role="alert">{addError}</p>{/if}
              </div>
            {/if}
            {#if addedName !== ""}
              <p class="printer-added" role="status" tabindex="-1" data-printer-added>
                Added printer '{addedName}'. It starts with default values, so check its wattage, cost, and lifespan on
                the <a href="#/printers">Printers tab</a>.
              </p>
            {/if}
          {/if}
        {/if}
      </Card>
    {/each}
  </div>
</Card>

<style>
  .stack { display: flex; flex-direction: column; gap: var(--gap); margin-block-start: var(--space-3); }
  .test-row { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-3); margin-top: var(--space-2); }
  .used-in { margin: var(--space-2) 0 0; color: var(--muted); font-size: var(--text-sm); }
  .test-ok { color: var(--ok); }
  .printer-prompt { margin-top: var(--space-2); padding: var(--space-3); background: var(--card-bg); border-radius: var(--radius); }
  .printer-prompt p { margin: 0 0 var(--space-2); }
  .prompt-actions { display: flex; gap: var(--space-2); flex-wrap: wrap; }
  .printer-added { margin: var(--space-2) 0 0; }

  /* .pill (theme.css) is neutral by default; this card tints "something answers here" (env, config or the
   * demo's fake data) against "not configured", mirroring StatusBadge's --ok color-mix formula rather than
   * a new literal color. */
  .pill-ok {
    color: var(--ok);
    background: color-mix(in srgb, var(--ok) 18%, var(--surface));
    border-color: var(--ok);
  }
</style>
