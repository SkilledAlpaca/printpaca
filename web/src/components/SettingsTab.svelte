<script lang="ts">
  import { settingsSectionFromHash } from "../lib/hashRoute";
  import { timezoneOptions as timezoneOptionList } from "../lib/timezones";
  import { nextKey, type ServerConfig } from "../lib/settings";
  import { settingsWarnings } from "../lib/settingsRules";
  import { presetDisplayName, feeScheduleDisplayName, warnFor } from "../lib/format";
  import { extractConfigUser } from "../lib/permissions";
  import { appConfig } from "../lib/state.svelte";
  import SettingsEditor from "./settings/SettingsEditor.svelte";
  import StorefrontSettings from "./settings/StorefrontSettings.svelte";
  import FieldRow from "./settings/FieldRow.svelte";
  import Card from "./ui/Card.svelte";
  import Field from "./ui/Field.svelte";
  import Fieldset from "./ui/Fieldset.svelte";
  import FormGrid from "./ui/FormGrid.svelte";
  import PageHeader from "./ui/PageHeader.svelte";
  import QuoteSettings from "./settings/QuoteSettings.svelte";
  import CustomCssSettings from "./settings/CustomCssSettings.svelte";
  import SmtpSettings from "./settings/SmtpSettings.svelte";
  import IntegrationsSettings from "./settings/IntegrationsSettings.svelte";
  import HardwareSettings from "./settings/HardwareSettings.svelte";
  import UsersSettings from "./settings/UsersSettings.svelte";
  import AccountCard from "./settings/AccountCard.svelte";
  import LabeledNumber from "./LabeledNumber.svelte";
  import LabeledText from "./LabeledText.svelte";
  import { focusAfterUpdate } from "../lib/a11y.svelte";
  import { MediaQuery } from "svelte/reactivity";
  import { focusAfterRowRemoval, keySelector } from "../lib/a11yFocus";

  // Server-asserted demoMode from appConfig (as in IntegrationsSettings.svelte), used to disable the
  // Diagnostics export button in a read-only demo; the server blocks the request anyway (GET
  // /api/logs/errors, server/logexport.go), so the button doesn't look clickable for a 403.
  const demoMode = $derived(appConfig.current?.demoMode === true);

  // /api/config's "user" extra (server/users.go's meResponse); null until the config loads, which
  // isAdminView treats as admin.
  const currentUser = $derived(extractConfigUser(appConfig.current));
  // GET /api/settings is hard admin-only (server/main.go): an employee session gets 403 regardless of tab
  // grants. So an employee sees only the Account card (GET /api/me, POST /api/me/password), never
  // SettingsEditor, which would show a perpetual "couldn't load settings" banner.
  const isAdminView = $derived(currentUser === null || currentUser.isAdmin);

  // `#/settings/<section>` names a card. An employee has only the Account card, so any other section answers with
  // "No access" rather than quietly showing Account (A16).
  let hashSection = $state(settingsSectionFromHash(location.hash));
  const forbiddenSection = $derived(!isAdminView && hashSection !== null && hashSection !== "account" ? hashSection : null);

  // Section nav: id, label and the error sections (settings.ts's errorSection) that mark it, in page order.
  // Buttons use scrollIntoView, not href="#..." anchors, because location.hash is the tab router in App.svelte.
  // "More settings", "Data" and "Diagnostics" have no form section, so their keys are empty (hasError never
  // matches it).
  const JUMP_SECTIONS: { id: string; label: string; keys: string[] }[] = [
    { id: "section-account", label: "Account", keys: [] },
    { id: "section-currency", label: "Currency", keys: ["currency"] },
    { id: "section-pricing", label: "Pricing", keys: ["pricing"] },
    { id: "section-timezone", label: "Time zone", keys: ["timezone"] },
    { id: "section-fee-schedules", label: "Fee schedules", keys: ["feeSchedules"] },
    { id: "section-hardware", label: "Hardware", keys: ["hardware"] },
    { id: "section-defaults", label: "Defaults", keys: ["defaults"] },
    { id: "section-quote", label: "Business identity", keys: ["quote"] },
    { id: "section-storefront", label: "Storefront", keys: ["shop", "site", "publicQuotes"] },
    { id: "section-custom-css", label: "Custom CSS", keys: ["customCss"] },
    { id: "section-smtp", label: "Email", keys: ["smtp"] },
    { id: "section-integrationsUrls", label: "Integrations", keys: ["integrationsUrls"] },
    { id: "section-users", label: "Users", keys: ["users"] },
    { id: "section-features", label: "Features", keys: ["ui"] },
    { id: "section-more", label: "More settings", keys: [] },
    { id: "section-data", label: "Data", keys: [] },
    { id: "section-diagnostics", label: "Diagnostics", keys: [] },
  ];
  // Suggestions for the time zone field; a typed name outside the list is still accepted if Intl knows it.
  const timezoneOptions = timezoneOptionList();
  // At 900px and wider the sections are a rail beside the cards, sticky and scrolling on its own, so it never
  // costs the cards any height (a sticky bar across the top took a third of a 1366x768 screen). Below that it is a
  // collapsed "Jump to a section" disclosure above the cards and scrolls away with them.
  const railQuery = new MediaQuery("min-width: 900px", true);
  const rail = $derived(railQuery.current);
  let navOpen = $state(false);
  function jumpTo(id: string) {
    const section = document.getElementById(id);
    if (!section) return;
    section.scrollIntoView({ behavior: matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth", block: "start" });
    const heading = section.querySelector<HTMLElement>("h2");
    if (heading) {
      heading.tabIndex = -1;
      heading.focus({ preventScroll: true });
    }
    navOpen = false;
  }

  // Features: which nav tabs App.svelte shows, in nav order (NAV_SECTIONS). "quote" and "settings" are never
  // listed: server/config.go's validate() rejects both ids since App.svelte always renders them.
  const FEATURE_TABS: { id: string; label: string }[] = [
    { id: "quotes", label: "Quotes" },
    { id: "requests", label: "Requests" },
    { id: "invoices", label: "Invoices" },
    { id: "items", label: "Shop items" },
    { id: "clients", label: "Clients" },
    { id: "dashboard", label: "Dashboard" },
    { id: "filament", label: "Filament" },
    { id: "electricity", label: "Electricity" },
    { id: "printers", label: "Printers" },
    { id: "guide", label: "Guide" },
  ];
  function isTabEnabled(form: ServerConfig, id: string): boolean {
    return !form.ui.disabledTabs.includes(id);
  }
  // The checkbox reads "enabled" but the stored field is the inverse (disabled ids).
  function setTabEnabled(form: ServerConfig, id: string, enabled: boolean) {
    const next = new Set(form.ui.disabledTabs);
    if (enabled) next.delete(id);
    else next.add(id);
    form.ui = { disabledTabs: [...next] };
  }

  // Fee schedules: a deep-merge map on the server; built-in keys can't be removed. (Currency, Pricing and
  // Defaults bind straight into `form` and need no helpers.)

  function addFeeSchedule(form: ServerConfig) {
    const key = nextKey(Object.keys(form.feeSchedules), "custom");
    form.feeSchedules = { ...form.feeSchedules, [key]: { source: "", fixed: [], percent: [] } };
  }
  function removeFeeSchedule(form: ServerConfig, key: string) {
    const next = { ...form.feeSchedules };
    delete next[key];
    form.feeSchedules = next;
    void focusAfterUpdate(keySelector("add-fee-schedule"));
  }
  function addFixedCharge(form: ServerConfig, key: string) {
    const fs = form.feeSchedules[key];
    if (!fs) return;
    fs.fixed.push({ amount: 0, label: "" });
  }
  function removeFixedCharge(form: ServerConfig, key: string, index: number) {
    const fs = form.feeSchedules[key];
    if (!fs) return;
    fs.fixed = fs.fixed.filter((_, i) => i !== index);
    void focusAfterRowRemoval(`remove-fixed-${key}`, index, fs.fixed.length, `add-fixed-${key}`);
  }
  function addPercentCharge(form: ServerConfig, key: string) {
    const fs = form.feeSchedules[key];
    if (!fs) return;
    fs.percent.push({ pct: 0, base: "revenue", label: "" });
  }
  function removePercentCharge(form: ServerConfig, key: string, index: number) {
    const fs = form.feeSchedules[key];
    if (!fs) return;
    fs.percent = fs.percent.filter((_, i) => i !== index);
    void focusAfterRowRemoval(`remove-percent-${key}`, index, fs.percent.length, `add-percent-${key}`);
  }
</script>

<svelte:window onhashchange={() => (hashSection = settingsSectionFromHash(location.hash))} />

<div class="page">
  <PageHeader title="Settings" />

  {#if forbiddenSection}
    <div class="card banner-warn" role="alert">
      <h2>No access</h2>
      <p>You don't have access to the {forbiddenSection} settings. Ask an admin to change them.</p>
    </div>
  {/if}

  {#if !isAdminView}
    <!-- Employee view: GET /api/settings is hard admin-only, so SettingsEditor and its cards would only
         show a load-failure banner for this identity. The Account card needs neither (see
         AccountCard.svelte). -->
    <AccountCard user={currentUser} />
  {:else}
  <SettingsEditor>
    {#snippet children({ form, builtin, original, hasError, sync })}
      {@const builtinFeeScheduleKeys = new Set(Object.keys(builtin.feeSchedules))}
      {@const warnings = settingsWarnings(form)}

      <div class="settings-layout">
        <nav class="jump-nav" aria-label="Settings sections">
          {#if !rail}
            <button type="button" class="jump-toggle" aria-expanded={navOpen} aria-controls="jump-list" onclick={() => (navOpen = !navOpen)}>
              Jump to a section
            </button>
          {/if}
          {#if rail || navOpen}
            <ul class="jump-list" id="jump-list">
              {#each JUMP_SECTIONS as s (s.id)}
                {@const bad = s.keys.some((k) => hasError(k))}
                <li>
                  <button type="button" class="quiet jump-link" class:has-error={bad} onclick={() => jumpTo(s.id)}>{`${s.label}${bad ? " (error)" : ""}`}</button>
                </li>
              {/each}
            </ul>
          {/if}
        </nav>

        <div class="settings-cards">
          <AccountCard user={currentUser} />

          <Card id="section-currency" tone={hasError("currency") ? "danger" : "default"} heading="Currency">
            <p class="hint">Symbol and decimal precision used everywhere money is displayed.</p>
            <FormGrid align="controls">
              <Field id="currency-symbol" label="Symbol" size="xs">
                {#snippet children(a)}
                  <input id={a.id} aria-describedby={a.describedby} bind:value={form.currency.symbol} />
                {/snippet}
              </Field>
              <LabeledText
                label="Currency code"
                size="xs"
                maxlength={3}
                bind:value={form.currency.code}
                warn={warnFor(warnings, "currency.code")}
                help="Optional three-letter code, such as USD or EUR. Only used to put the amount in the PayPal.me link."
              />
              <LabeledNumber
                label="Decimals"
                size="xs"
                min={0}
                max={4}
                step={1}
                bind:value={form.currency.decimals}
                warn={warnFor(warnings, "currency.decimals")}
              />
            </FormGrid>
          </Card>

          <Card id="section-pricing" tone={hasError("pricing") ? "danger" : "default"} heading="Pricing">
            <p class="hint">Default sale-price strategy, tax, shipping, and the fee schedule new quotes start with.</p>
            <FormGrid align="controls">
              <Field id="pricing-strategy" label="Strategy" size="m">
                {#snippet children(a)}
                  <select id={a.id} aria-describedby={a.describedby} bind:value={form.pricing.strategy}>
                    <option value="margin">Margin on price</option>
                    <option value="markup">Markup on cost</option>
                  </select>
                {/snippet}
              </Field>
              {#if form.pricing.strategy === "margin"}
                <LabeledNumber
                  label="Margin (%)"
                  help="The share of the pre-tax price that is profit before marketplace fees. Tax is added on top, and fees come out of what remains, so net profit can be lower."
                  step="any"
                  bind:value={form.pricing.marginPct}
                  warn={warnFor(warnings, "pricing.marginPct")}
                />
              {:else}
                <LabeledNumber
                  label="Markup (×)"
                  help="Multiplier applied to your cost. A markup of 2 doubles the cost."
                  step={0.1}
                  bind:value={form.pricing.markupMultiplier}
                  warn={warnFor(warnings, "pricing.markupMultiplier")}
                />
              {/if}
              <LabeledNumber
                label="Tax included in quoted prices (%)"
                help="Use when your quoted prices already include sales tax or VAT."
                step="any"
                bind:value={form.pricing.taxRatePct}
                hint="Converting a quote that included tax creates the invoice with no added sales tax."
                warn={warnFor(warnings, "pricing.taxRatePct")}
              />
              <LabeledNumber
                label={`Default shipping charge (${form.currency.symbol})`}
                help="What you bill the customer for shipping. The public quote page shows it beside the price, and new quotes start with it. Set 0 to hide shipping."
                step="any"
                hint={form.pricing.defaultShippingCost > 0 && !(form.pricing.defaultShippingCharge > 0)
                  ? "A shipping cost is set but the charge is 0, so the public quote page shows no shipping. Set the charge to show it."
                  : undefined}
                warn={warnFor(warnings, "pricing.defaultShippingCharge")}
                bind:value={form.pricing.defaultShippingCharge}
              />
              <LabeledNumber
                label={`Default shipping cost (${form.currency.symbol})`}
                help="What you pay for postage and packaging per order. It lowers your profit and is never shown to customers."
                step="any"
                bind:value={form.pricing.defaultShippingCost}
              />
              <Field id="pricing-fee-schedule" label="Default fee schedule" size="m">
                {#snippet children(a)}
                  <select id={a.id} aria-describedby={a.describedby} bind:value={form.pricing.defaultFeeSchedule}>
                    <option value="none">None (direct sale)</option>
                    {#each Object.keys(form.feeSchedules) as key (key)}<option value={key}>{feeScheduleDisplayName(key)}</option>{/each}
                  </select>
                {/snippet}
              </Field>
            </FormGrid>
            <label class="toggle">
              <input type="checkbox" bind:checked={form.pricing.laborPerUnit} />
              <span>Charge labor per unit instead of once per batch</span>
            </label>
          </Card>

          <Card id="section-timezone" tone={hasError("timezone") ? "danger" : "default"} heading="Time zone">
            <p class="hint">Decides which month a payment, a quote, and "today" belong to on the Dashboard, when an invoice is overdue, and the date a marked-paid payment gets. Stored timestamps stay UTC.</p>
            <FormGrid>
              <Field
                id="timezone-input"
                label="Time zone"
                size="l"
                hint="Type the name of a city or region, such as America/New_York or Europe/London. UTC means no offset. Times in printpaca are shown in this time zone."
                error={warnFor(warnings, "timezone") || undefined}
              >
                {#snippet children(a)}
                  <input
                    id={a.id}
                    list="timezone-options"
                    bind:value={form.timezone}
                    placeholder="UTC"
                    autocomplete="off"
                    aria-invalid={a.invalid ?? "false"}
                    aria-describedby={a.describedby}
                  />
                  <datalist id="timezone-options">
                    {#each timezoneOptions as tz (tz)}<option value={tz}></option>{/each}
                  </datalist>
                {/snippet}
              </Field>
            </FormGrid>
          </Card>

          <Card id="section-fee-schedules" tone={hasError("feeSchedules") ? "danger" : "default"} heading="Fee schedules">
            <p class="hint">Marketplace fee breakdowns: fixed charges plus percent-of-revenue charges. Built-in schedules can't be removed.</p>
            <div class="stack spaced">
              {#each Object.entries(form.feeSchedules) as [key, fs] (key)}
                <Card tone="inset">
                  <Fieldset legend={feeScheduleDisplayName(key)}>
                    <div class="schedule-actions">
                      {#if builtinFeeScheduleKeys.has(key)}
                        <span class="pill">built-in</span>
                      {:else}
                        <button type="button" aria-label={`Remove fee schedule ${key}`} onclick={() => removeFeeSchedule(form, key)}>Remove schedule</button>
                      {/if}
                    </div>
                    <FormGrid>
                      <Field id={`fee-${key}-source`} label="Source" size="l">
                        {#snippet children(a)}
                          <input id={a.id} aria-describedby={a.describedby} bind:value={fs.source} placeholder="For example, the Etsy fee page and the date you checked it" />
                        {/snippet}
                      </Field>
                    </FormGrid>

                    <h3 class="section-label">Fixed charges</h3>
                    {#each fs.fixed as charge, i (i)}
                      <FieldRow removeLabel={`Remove ${key} fixed charge ${i + 1}`} focusKey={`remove-fixed-${key}-${i}`} onremove={() => removeFixedCharge(form, key, i)}>
                        <FormGrid>
                          <Field id={`fee-${key}-fixed-${i}-amount`} label={`Fixed charge ${i + 1} amount (${form.currency.symbol})`} size="s">
                            {#snippet children(a)}
                              <input id={a.id} aria-describedby={a.describedby} type="number" step="0.01" bind:value={charge.amount} />
                            {/snippet}
                          </Field>
                          <Field id={`fee-${key}-fixed-${i}-label`} label={`Fixed charge ${i + 1} label`} size="m">
                            {#snippet children(a)}
                              <input id={a.id} aria-describedby={a.describedby} bind:value={charge.label} />
                            {/snippet}
                          </Field>
                        </FormGrid>
                      </FieldRow>
                    {/each}
                    <div class="row-actions">
                      <button type="button" data-focus-key={`add-fixed-${key}`} onclick={() => addFixedCharge(form, key)}>Add fixed charge</button>
                    </div>

                    <h3 class="section-label">Percent charges</h3>
                    {#each fs.percent as charge, i (i)}
                      <FieldRow removeLabel={`Remove ${key} percent charge ${i + 1}`} focusKey={`remove-percent-${key}-${i}`} onremove={() => removePercentCharge(form, key, i)}>
                        <FormGrid>
                          <Field id={`fee-${key}-percent-${i}-pct`} label={`Percent charge ${i + 1}: percent of revenue (%)`} size="s">
                            {#snippet children(a)}
                              <input id={a.id} aria-describedby={a.describedby} type="number" step="any" bind:value={charge.pct} />
                            {/snippet}
                          </Field>
                          <Field id={`fee-${key}-percent-${i}-label`} label={`Percent charge ${i + 1} label`} size="m">
                            {#snippet children(a)}
                              <input id={a.id} aria-describedby={a.describedby} bind:value={charge.label} />
                            {/snippet}
                          </Field>
                        </FormGrid>
                      </FieldRow>
                    {/each}
                    <div class="row-actions">
                      <button type="button" data-focus-key={`add-percent-${key}`} onclick={() => addPercentCharge(form, key)}>Add percent charge</button>
                    </div>
                  </Fieldset>
                </Card>
              {/each}
            </div>
            <div class="row-actions">
              <button type="button" data-focus-key="add-fee-schedule" onclick={() => addFeeSchedule(form)}>Add fee schedule</button>
            </div>
          </Card>

          <HardwareSettings {form} {hasError} />

          <Card id="section-defaults" tone={hasError("defaults") ? "danger" : "default"} heading="Defaults">
            <p class="hint">What a fresh quote starts with.</p>
            <FormGrid align="controls">
              <Field id="defaults-preset" label="Preset" size="m">
                {#snippet children(a)}
                  <select id={a.id} aria-describedby={a.describedby} bind:value={form.defaults.preset}>
                    {#each Object.keys(form.presets) as key (key)}<option value={key}>{presetDisplayName(key)}</option>{/each}
                  </select>
                {/snippet}
              </Field>
              <Field id="defaults-printer" label="Printer" size="m">
                {#snippet children(a)}
                  <select id={a.id} aria-describedby={a.describedby} bind:value={form.defaults.printer}>
                    {#each form.printers as p (p.id)}<option value={p.id}>{p.name}</option>{/each}
                  </select>
                {/snippet}
              </Field>
              <Field id="defaults-material" label="Material" size="m">
                {#snippet children(a)}
                  <select id={a.id} aria-describedby={a.describedby} bind:value={form.defaults.material}>
                    {#each form.materials as m (m.id)}<option value={m.id}>{m.name}</option>{/each}
                  </select>
                {/snippet}
              </Field>
              <Field id="defaults-failure-rate" label="Failure rate (%)" size="s">
                {#snippet children(a)}
                  <input id={a.id} aria-describedby={a.describedby} type="number" step="any" min="0" max="90" bind:value={form.defaults.failureRatePct} />
                {/snippet}
              </Field>
              <LabeledNumber
                label="Quantity"
                min={1}
                step={1}
                bind:value={form.defaults.quantity}
                warn={warnFor(warnings, "defaults.quantity")}
              />
            </FormGrid>
          </Card>

          <QuoteSettings {form} {hasError} />

          <StorefrontSettings {form} {hasError} {sync} />

          <CustomCssSettings {form} {hasError} />

          <SmtpSettings {form} {hasError} />

          <IntegrationsSettings {form} {original} {hasError} />

          <UsersSettings {form} {original} {hasError} />

          <Card id="section-features" tone={hasError("ui") ? "danger" : "default"} heading="Features">
            <p class="hint">Hide tabs you don't use from navigation. Data is kept and comes back when you re-enable a tab.</p>
            <Fieldset legend="Show these tabs">
              <div class="feature-grid">
                {#each FEATURE_TABS as t (t.id)}
                  <label class="toggle">
                    <input
                      type="checkbox"
                      checked={isTabEnabled(form, t.id)}
                      onchange={(e) => setTabEnabled(form, t.id, e.currentTarget.checked)}
                    />
                    <span>{t.label}</span>
                  </label>
                {/each}
              </div>
            </Fieldset>
            {#if !isTabEnabled(form, "requests") && form.materials.some((m) => m.public)}
              <p class="banner-warn" role="alert">
                If you use the public estimate page, submissions keep arriving unseen until you turn Requests back on.
              </p>
            {/if}
          </Card>

          <Card id="section-more" heading="More settings">
            <p class="hint">These settings live on the pages they affect.</p>
            <ul class="plain-list">
              <li><a href="#/invoices">Invoicing</a> · on Invoices</li>
              <li><a href="#/requests">Public estimate pricing</a> · on Requests</li>
              <li><a href="#/filament">Materials</a> · on Filament</li>
              <li><a href="#/printers">Printers and presets</a> · on Printers</li>
              <li><a href="#/electricity">Energy</a> · on Electricity</li>
            </ul>
          </Card>

          <Card id="section-data" heading="Data">
            <p class="hint">
              Everything in one JSON file: clients, invoices, payments, quotes, requests (without uploaded G-code), and
              your settings. The export is one-way: printpaca can't import it. For a restorable backup, copy the
              <code>config</code> directory on the server instead. See "Where your data lives" in the Getting started
              guide (docs/getting-started.md).
            </p>
            <a class="button-link" href="api/export" download>Download data export</a>
          </Card>

          <Card id="section-diagnostics" heading="Diagnostics">
            {#if demoMode}
              <p class="banner-warn" role="status">
                This is a read-only demo: recent errors aren't available to export.
              </p>
            {/if}
            <p class="hint">
              Downloads recent error lines from this server's memory. IP addresses and emails are pseudonymized; secrets
              are redacted.
            </p>
            <button type="button" disabled={demoMode} onclick={() => (window.location.href = "api/logs/errors")}>
              Export recent errors
            </button>
          </Card>
        </div>
      </div>
    {/snippet}
  </SettingsEditor>
  {/if}
</div>

<style>
  /* The section nav and the cards. At >=900px the nav is a rail in its own column, a field-s plus 2rem wide, which
   * fits "Business identity" on one line; below that the grid is one column and the nav a disclosure. */
  .settings-layout { display: grid; grid-template-columns: minmax(0, 1fr); gap: var(--gap-section); align-items: start; }
  .settings-cards { display: flex; flex-direction: column; gap: var(--gap-section); min-width: 0; }
  @media (min-width: 900px) {
    .settings-layout { grid-template-columns: calc(var(--field-s) + var(--space-8)) minmax(0, 1fr); }
    /* Sticky to the scroll container (<main> at >=900px). It scrolls on its own when the list is taller than the
     * screen (a short laptop), and stops above the save bar, whose measured height stickyInset publishes. */
    .jump-nav {
      position: sticky;
      top: var(--space-2);
      max-height: calc(100dvh - var(--sticky-bottom-h, 0px) - var(--space-4));
      overflow-y: auto;
    }
  }
  .jump-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--space-1); }
  @media (max-width: 899.98px) {
    .jump-list { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, var(--field-s)), 1fr)); margin-block-start: var(--space-2); }
  }
  .jump-toggle { width: 100%; text-align: left; }
  /* button.quiet (theme.css) supplies tap sizing and hover fill; here only the jump-link look and the error state. */
  .jump-link {
    width: 100%;
    text-align: left;
    color: var(--muted);
    background: none;
    border: 1px solid transparent;
    border-radius: var(--radius);
  }
  .jump-link.has-error { color: var(--danger); }

  /* Anchor jumps: the scroll container's scroll-padding-top (App.svelte) reserves any sticky bar above the cards,
   * and scroll-margin-top adds a little air. :global() by id because several cards (account, quote, storefront,
   * custom-css, smtp, hardware, users) are separate components; targets are exactly the ids in JUMP_SECTIONS. */
  :global(:is(
    #section-account,
    #section-currency,
    #section-pricing,
    #section-fee-schedules,
    #section-timezone,
    #section-hardware,
    #section-defaults,
    #section-quote,
    #section-storefront,
    #section-custom-css,
    #section-smtp,
    #section-integrationsUrls,
    #section-users,
    #section-features,
    #section-more,
    #section-data,
    #section-diagnostics
  )) {
    scroll-margin-top: var(--space-1);
  }

  .section-label { margin: var(--space-3) 0 var(--space-1); }
  .schedule-actions { display: flex; align-items: center; gap: var(--space-2); }
  .row-actions { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); }

  /* Checkbox grid for Features: a narrower column floor, since ten short labels fit at half a field width. */
  .feature-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, var(--field-s)), 1fr));
    gap: var(--space-1) var(--gap);
  }

  /* theme.css's box and tap-target styling covers input/select/button/textarea, not <a>; mirrors the
   * default (non-.primary) button look (e.g. SettingsEditor's "Discard changes"). */
  .button-link {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: fit-content;
    min-height: 44px;
    padding: var(--space-2) var(--space-3);
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: calc(var(--radius) / 2);
    color: var(--text);
    text-decoration: none;
  }
  .button-link:hover { background: var(--surface-2); }
</style>
