<script lang="ts">
  import { timezoneOptions as timezoneOptionList } from "../lib/timezones";
  import { nextKey, type ServerConfig } from "../lib/settings";
  import { settingsWarnings } from "../lib/settingsRules";
  import { presetDisplayName, feeScheduleDisplayName, warnFor } from "../lib/format";
  import { extractConfigUser } from "../lib/permissions";
  import { appConfig } from "../lib/state.svelte";
  import SettingsEditor from "./settings/SettingsEditor.svelte";
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
  import { stickyInset } from "../lib/stickyInset";
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

  // Jump nav: id, label and error-key per card, in page order. Buttons use scrollIntoView, not href="#..."
  // anchors, because location.hash is the tab router in App.svelte. "More settings" and "Data" have no form
  // section, so their key is "" (hasError never matches it).
  const JUMP_SECTIONS: { id: string; label: string; key: string }[] = [
    { id: "section-account", label: "Account", key: "" },
    { id: "section-currency", label: "Currency", key: "currency" },
    { id: "section-pricing", label: "Pricing", key: "pricing" },
    { id: "section-timezone", label: "Time zone", key: "timezone" },
    { id: "section-fee-schedules", label: "Fee schedules", key: "feeSchedules" },
    { id: "section-hardware", label: "Hardware", key: "hardware" },
    { id: "section-defaults", label: "Defaults", key: "defaults" },
    { id: "section-quote", label: "Business identity", key: "quote" },
    { id: "section-custom-css", label: "Custom CSS", key: "customCss" },
    { id: "section-smtp", label: "Email", key: "smtp" },
    { id: "section-integrationsUrls", label: "Integrations", key: "integrationsUrls" },
    { id: "section-users", label: "Users", key: "users" },
    { id: "section-features", label: "Features", key: "ui" },
    { id: "section-more", label: "More settings", key: "" },
    { id: "section-data", label: "Data", key: "" },
    { id: "section-diagnostics", label: "Diagnostics", key: "" },
  ];
  // Suggestions for the time zone field; a typed name outside the list is still accepted if Intl knows it.
  const timezoneOptions = timezoneOptionList();
  // The jump nav is sticky only at >=600px (see the CSS below); below that it reserves no scroll-padding.
  const stickyNavQuery = new MediaQuery("min-width: 600px", true);
  const stickyNav = $derived(stickyNavQuery.current);
  function jumpTo(id: string) {
    const section = document.getElementById(id);
    if (!section) return;
    section.scrollIntoView({ behavior: matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth", block: "start" });
    const heading = section.querySelector<HTMLElement>("h2");
    if (heading) {
      heading.tabIndex = -1;
      heading.focus({ preventScroll: true });
    }
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

<div class="page">
  <h1>Settings</h1>

  {#if !isAdminView}
    <!-- Employee view: GET /api/settings is hard admin-only, so SettingsEditor and its cards would only
         show a load-failure banner for this identity. The Account card needs neither (see
         AccountCard.svelte). -->
    <AccountCard user={currentUser} />
  {:else}
  <SettingsEditor>
    {#snippet children({ form, builtin, original, hasError })}
      {@const builtinFeeScheduleKeys = new Set(Object.keys(builtin.feeSchedules))}
      {@const warnings = settingsWarnings(form)}

      <nav class="jump-nav" aria-label="Settings sections" use:stickyInset={{ edge: "top", active: stickyNav }}>
        {#each JUMP_SECTIONS as s (s.id)}
          <button
            type="button"
            class="quiet jump-link"
            class:has-error={hasError(s.key)}
            onclick={() => jumpTo(s.id)}
          >{`${s.label}${hasError(s.key) ? " (error)" : ""}`}</button>
        {/each}
      </nav>

      <AccountCard user={currentUser} />

      <div id="section-currency" class="card roomy" class:danger-border={hasError("currency")}>
        <h2>Currency</h2>
        <p class="hint">Symbol and decimal precision used everywhere money is displayed.</p>
        <div class="field-rows">
          <label class="field field-narrow">
            <span>Symbol</span>
            <input bind:value={form.currency.symbol} />
          </label>
          <LabeledText
            label="Currency code"
            maxlength={3}
            bind:value={form.currency.code}
            warn={warnFor(warnings, "currency.code")}
            hint="Optional three-letter code for your currency, such as USD or EUR. Only used to put the amount in the PayPal.me link."
          />
          <LabeledNumber
            label="Decimals"
            min={0}
            max={4}
            step={1}
            bind:value={form.currency.decimals}
            warn={warnFor(warnings, "currency.decimals")}
          />
        </div>
      </div>

      <div id="section-pricing" class="card roomy" class:danger-border={hasError("pricing")}>
        <h2>Pricing</h2>
        <p class="hint">Default sale-price strategy, tax, shipping, and the fee schedule new quotes start with.</p>
        <div class="field-rows">
          <label class="field">
            <span>Strategy</span>
            <select bind:value={form.pricing.strategy}>
              <option value="margin">Margin on price</option>
              <option value="markup">Markup on cost</option>
            </select>
          </label>
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
          <label class="field">
            <span>Default fee schedule</span>
            <select bind:value={form.pricing.defaultFeeSchedule}>
              <option value="none">None (direct sale)</option>
              {#each Object.keys(form.feeSchedules) as key (key)}<option value={key}>{feeScheduleDisplayName(key)}</option>{/each}
            </select>
          </label>
        </div>
        <label class="toggle">
          <input type="checkbox" bind:checked={form.pricing.laborPerUnit} />
          <span>Charge labor per unit instead of once per batch</span>
        </label>
      </div>

      <div id="section-timezone" class="card roomy" class:danger-border={hasError("timezone")}>
        <h2>Time zone</h2>
        <p class="hint">Decides which month a payment, a quote, and "today" belong to on the Dashboard, when an invoice is overdue, and the date a marked-paid payment gets. Stored timestamps stay UTC.</p>
        <div class="field-rows">
          <div class="field">
            <label for="timezone-input">Time zone</label>
            <input
              id="timezone-input"
              list="timezone-options"
              bind:value={form.timezone}
              placeholder="UTC"
              autocomplete="off"
              aria-invalid={warnFor(warnings, "timezone") !== ""}
              aria-describedby={warnFor(warnings, "timezone") ? "timezone-hint timezone-warn" : "timezone-hint"}
            />
            <datalist id="timezone-options">
              {#each timezoneOptions as tz (tz)}<option value={tz}></option>{/each}
            </datalist>
            <small id="timezone-hint" class="field-hint">Type the name of a city or region, such as America/New_York or Europe/London. UTC means no offset. Times in printpaca are shown in this time zone.</small>
            {#if warnFor(warnings, "timezone")}<small id="timezone-warn" class="warn-text">{warnFor(warnings, "timezone")}</small>{/if}
          </div>
        </div>
      </div>

      <div id="section-fee-schedules" class="card roomy" class:danger-border={hasError("feeSchedules")}>
        <h2>Fee schedules</h2>
        <p class="hint">Marketplace fee breakdowns: fixed charges plus percent-of-revenue charges. Built-in schedules can't be removed.</p>
        {#each Object.entries(form.feeSchedules) as [key, fs] (key)}
          <div class="subcard">
            <div class="subhead">
              <h3>{feeScheduleDisplayName(key)}</h3>
              {#if builtinFeeScheduleKeys.has(key)}
                <span class="pill">built-in</span>
              {:else}
                <button type="button" aria-label={`Remove fee schedule ${key}`} onclick={() => removeFeeSchedule(form, key)}>Remove</button>
              {/if}
            </div>
            <label class="field">
              <span>Source</span>
              <input aria-label={`${key} source`} bind:value={fs.source} placeholder="For example, the Etsy fee page and the date you checked it" />
            </label>

            <h4 class="section-label">Fixed charges</h4>
            {#each fs.fixed as charge, i (i)}
              <div class="charge-row">
                <label class="field field-inline">
                  <span>Amount ({form.currency.symbol})</span>
                  <input type="number" step="0.01" aria-label={`${key} fixed charge ${i + 1} amount`} bind:value={charge.amount} />
                </label>
                <label class="field field-inline">
                  <span>Label</span>
                  <input aria-label={`${key} fixed charge ${i + 1} label`} bind:value={charge.label} />
                </label>
                <button type="button" aria-label={`Remove ${key} fixed charge ${i + 1}`} data-focus-key={`remove-fixed-${key}-${i}`} onclick={() => removeFixedCharge(form, key, i)}>Remove</button>
              </div>
            {/each}
            <button type="button" data-focus-key={`add-fixed-${key}`} onclick={() => addFixedCharge(form, key)}>Add fixed charge</button>

            <h4 class="section-label">Percent charges</h4>
            {#each fs.percent as charge, i (i)}
              <div class="charge-row">
                <label class="field field-inline">
                  <span>Percent</span>
                  <span class="percent-input">
                    <input type="number" step="any" aria-label={`${key} percent charge ${i + 1} percent of revenue`} bind:value={charge.pct} />
                    <span class="suffix">% of revenue</span>
                  </span>
                </label>
                <label class="field field-inline">
                  <span>Label</span>
                  <input aria-label={`${key} percent charge ${i + 1} label`} bind:value={charge.label} />
                </label>
                <button type="button" aria-label={`Remove ${key} percent charge ${i + 1}`} data-focus-key={`remove-percent-${key}-${i}`} onclick={() => removePercentCharge(form, key, i)}>Remove</button>
              </div>
            {/each}
            <button type="button" data-focus-key={`add-percent-${key}`} onclick={() => addPercentCharge(form, key)}>Add percent charge</button>
          </div>
        {/each}
        <button type="button" data-focus-key="add-fee-schedule" onclick={() => addFeeSchedule(form)}>Add fee schedule</button>
      </div>

      <HardwareSettings {form} {hasError} />

      <div id="section-defaults" class="card roomy" class:danger-border={hasError("defaults")}>
        <h2>Defaults</h2>
        <p class="hint">What a fresh quote starts with.</p>
        <div class="field-rows">
          <label class="field">
            <span>Preset</span>
            <select bind:value={form.defaults.preset}>
              {#each Object.keys(form.presets) as key (key)}<option value={key}>{presetDisplayName(key)}</option>{/each}
            </select>
          </label>
          <label class="field">
            <span>Printer</span>
            <select bind:value={form.defaults.printer}>
              {#each form.printers as p (p.id)}<option value={p.id}>{p.name}</option>{/each}
            </select>
          </label>
          <label class="field">
            <span>Material</span>
            <select bind:value={form.defaults.material}>
              {#each form.materials as m (m.id)}<option value={m.id}>{m.name}</option>{/each}
            </select>
          </label>
          <label class="field">
            <span>Failure rate (%)</span>
            <input type="number" step="any" min="0" max="90" bind:value={form.defaults.failureRatePct} />
          </label>
          <LabeledNumber
            label="Quantity"
            min={1}
            step={1}
            bind:value={form.defaults.quantity}
            warn={warnFor(warnings, "defaults.quantity")}
          />
        </div>
      </div>

      <QuoteSettings {form} {hasError} />

      <CustomCssSettings {form} {hasError} />

      <SmtpSettings {form} {hasError} />

      <IntegrationsSettings {form} {original} {hasError} />

      <UsersSettings {form} {original} {hasError} />

      <div id="section-features" class="card roomy" class:danger-border={hasError("ui")}>
        <h2>Features</h2>
        <p class="hint">Hide tabs you don't use from navigation. Data is kept and comes back when you re-enable a tab.</p>
        <fieldset class="feature-grid">
          <legend>Show these tabs</legend>
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
        </fieldset>
        {#if !isTabEnabled(form, "requests") && form.materials.some((m) => m.public)}
          <p class="banner-warn" role="alert">
            If you use the public estimate page, submissions keep arriving unseen until you turn Requests back on.
          </p>
        {/if}
      </div>

      <div id="section-more" class="card roomy">
        <h2>More settings</h2>
        <p class="hint">These settings live on the pages they affect.</p>
        <ul class="plain-list">
          <li><a href="#/invoices">Invoicing</a> · on Invoices</li>
          <li><a href="#/requests">Public estimates</a> · on Requests</li>
          <li><a href="#/filament">Materials</a> · on Filament</li>
          <li><a href="#/printers">Printers and presets</a> · on Printers</li>
          <li><a href="#/electricity">Energy</a> · on Electricity</li>
        </ul>
      </div>

      <div id="section-data" class="card roomy">
        <h2>Data</h2>
        <p class="hint">
          Everything in one JSON file: clients, invoices, payments, quotes, requests (without uploaded G-code), and
          your settings. The export is one-way: printpaca can't import it. For a restorable backup, copy the
          <code>config</code> directory on the server instead. See "Where your data lives" in the Getting started
          guide (docs/getting-started.md).
        </p>
        <a class="button-link" href="api/export" download>Download data export</a>
      </div>

      <div id="section-diagnostics" class="card roomy">
        <h2>Diagnostics</h2>
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
      </div>
    {/snippet}
  </SettingsEditor>
  {/if}
</div>

<style>
  /* Sticky jump nav: buttons, not href="#..." anchors, because location.hash is the tab router. At >=600px it
   * sticks to the top of the scroll container (<main> at >=900px, the window below that) and the pills wrap
   * onto as many lines as the content column needs, so it never scrolls sideways. <main> has no padding, so
   * top: 0 is flush with the scrollport edge; below 900px --demo-banner-h clears the sticky demo banner.
   * Below 600px the wrapped nav would cover a large share of the screen, so it scrolls with the page and
   * stickyInset reserves no height. */
  .jump-nav {
    z-index: 2;
    display: flex;
    flex-wrap: wrap;
    gap: 0.3rem;
    background: var(--surface);
    border-bottom: 1px solid var(--border);
    padding: 0.4rem 0;
  }
  @media (min-width: 600px) {
    .jump-nav {
      position: sticky;
      top: var(--demo-banner-h, 0px);
    }
  }
  /* button.quiet (theme.css) supplies tap sizing and hover fill; here only the jump-link look (transparent
   * pill, muted, no wrap) and the error state. */
  .jump-link {
    flex: 0 0 auto;
    color: var(--muted);
    background: none;
    border: 1px solid transparent;
    border-radius: var(--radius);
    white-space: nowrap;
  }
  .jump-link.has-error { color: var(--danger); }

  /* Anchor jumps: the scroll container's scroll-padding-top (App.svelte) already reserves the sticky nav's
   * measured height, so a card's heading lands fully visible below the nav; scroll-margin-top only adds a
   * little air. :global() by id because several cards (account, quote, custom-css, smtp, hardware, users)
   * are separate components; targets are exactly the ids in JUMP_SECTIONS. */
  :global(:is(
    #section-account,
    #section-currency,
    #section-pricing,
    #section-fee-schedules,
    #section-timezone,
    #section-hardware,
    #section-defaults,
    #section-quote,
    #section-custom-css,
    #section-smtp,
    #section-integrationsUrls,
    #section-users,
    #section-features,
    #section-more,
    #section-data,
    #section-diagnostics
  )) {
    scroll-margin-top: 0.25rem;
  }

  .section-label { margin: 0.6rem 0 0.3rem; }

  /* Checkbox grid for Features: field-rows assumes label+input pairs and has no row-gap, which left these
   * flush. A narrower column floor than field-rows' 24rem, since nine short labels fit at half that. */
  .feature-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 12rem), 1fr));
    gap: 0.3rem 0.8rem;
    border: 0;
    padding: 0;
    margin: 0;
  }
  .feature-grid legend { grid-column: 1 / -1; padding: 0; font-weight: 600; margin-bottom: 0.2rem; }

  /* theme .toggle has no margin of its own; this one follows a .field-rows block and needs the same gap as
   * other stacked blocks in a card. */
  .toggle { margin-top: 0.6rem; }

  .charge-row { display: flex; align-items: end; gap: 0.5rem; margin-bottom: 0.4rem; flex-wrap: wrap; }
  .charge-row .field { flex: 1 1 100px; min-width: 90px; }
  /* The percent field's nowrap "% of revenue" suffix needs more than the generic 90px floor: below it the
   * cell can shrink under the suffix's min-content width while the row never wraps, painting the suffix
   * over the next field. */
  .charge-row .field:has(.percent-input) { flex-basis: 160px; min-width: 150px; }
  .percent-input { display: flex; align-items: center; gap: 0.3rem; width: 100%; }
  .percent-input input { width: auto; flex: 1 1 auto; min-width: 4rem; }

  /* theme.css's box and tap-target styling covers input/select/button/textarea, not <a>; mirrors the
   * default (non-.primary) button look (e.g. SettingsEditor's "Discard changes"). */
  .button-link {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: fit-content;
    min-height: 44px;
    padding: 0.4rem 0.6rem;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: calc(var(--radius) / 2);
    color: var(--text);
    text-decoration: none;
  }
  .button-link:hover { background: var(--surface-2); }
</style>
