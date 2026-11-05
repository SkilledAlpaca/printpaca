<script lang="ts">
  import type { ServerConfig } from "../../lib/settings";
  import { settingsWarnings } from "../../lib/settingsRules";
  import { warnFor } from "../../lib/format";
  import { testSmtp } from "../../lib/sendApi";
  import Card from "../ui/Card.svelte";
  import Field from "../ui/Field.svelte";
  import FormGrid from "../ui/FormGrid.svelte";
  import LabeledNumber from "../LabeledNumber.svelte";
  import LabeledText from "../LabeledText.svelte";

  let { form, hasError }: { form: ServerConfig; hasError: (section: string) => boolean } = $props();

  const warnings = $derived(settingsWarnings(form));

  // Send test email: fires against the server's currently saved config (POST /api/smtp/test reads the running
  // ConfigStore, not unsaved edits), one-shot fire-and-report like the invoice and quote send actions.
  // ok/error shows verbatim in the status line for either outcome (unconfigured HTTP failure or inner send
  // failure).
  const noHost = $derived(form.smtp.host.trim() === "");
  let testing = $state(false);
  let testStatus = $state("");
  let testFailed = $state(false);

  async function sendTestEmail() {
    if (testing) return;
    testing = true;
    testStatus = "";
    testFailed = false;
    try {
      const result = await testSmtp();
      if (!result.ok) {
        testFailed = true;
        testStatus = result.error;
        return;
      }
      if (result.data.ok) {
        testFailed = false;
        testStatus = "Test email sent.";
      } else {
        testFailed = true;
        testStatus = result.data.error;
      }
    } catch (err) {
      testFailed = true;
      testStatus = err instanceof Error ? err.message : "Couldn't reach the server.";
    } finally {
      testing = false;
    }
  }
</script>

<Card id="section-smtp" tone={hasError("smtp") ? "danger" : "default"} heading="Email">
  <p class="hint">Notifies you of each new estimate request and sends quotes and invoices to clients from the
    Quotes and Invoices pages. Leave host empty to disable both. The password is read from the SMTP_PASSWORD
    environment variable and never stored in config.yaml.</p>
  {#if form.smtp.host === ""}
    <p class="hint">The other fields are checked once you enter a host.</p>
  {/if}
  <FormGrid align="controls">
    <Field id="smtp-host" label="Host" size="m">
      {#snippet children(a)}
        <input id={a.id} aria-describedby={a.describedby} bind:value={form.smtp.host} placeholder="smtp.example.com" />
      {/snippet}
    </Field>
    <!-- min/max mirror server/config.go's validate(): smtp.port out of range [1,65535]. -->
    <LabeledNumber
      label="Port"
      min={1}
      max={65535}
      step={1}
      bind:value={form.smtp.port}
      warn={warnFor(warnings, "smtp.port")}
    />
    <Field id="smtp-username" label="Username" size="m">
      {#snippet children(a)}
        <input id={a.id} aria-describedby={a.describedby} bind:value={form.smtp.username} />
      {/snippet}
    </Field>
    <LabeledText
      label="From address"
      type="email"
      bind:value={form.smtp.from}
      warn={warnFor(warnings, "smtp.from")}
    />
    <LabeledText
      label="Notification address"
      help="Where new request notifications go."
      type="email"
      bind:value={form.smtp.to}
      warn={warnFor(warnings, "smtp.to")}
    />
  </FormGrid>
  <div class="test-row">
    <!-- aria-disabled, not disabled: the button stays focusable so its reason is reachable, and the two states
         can't disagree. -->
    <button
      type="button"
      onclick={() => { if (!noHost) void sendTestEmail(); }}
      aria-disabled={noHost || testing}
      aria-busy={testing}
      aria-describedby="smtp-test-hint"
    >
      {testing ? "Sending…" : "Send test email"}
    </button>
    <span class="hint" id="smtp-test-hint">{noHost ? "Set an SMTP host first." : "Tests the saved settings, not unsaved changes above."}</span>
  </div>
  <p class="status-line" role={testFailed ? "alert" : "status"} aria-live={testFailed ? "assertive" : "polite"}>
    {testStatus}
  </p>
</Card>

<style>
  .test-row { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-3); margin-top: var(--space-2); }
  .test-row .hint { margin: 0; }
</style>
