<script lang="ts">
  import { characterCountText } from "../../lib/countText";
  import type { ServerConfig } from "../../lib/settings";
  import { settingsWarnings, PUBLIC_INTRO_TEXT_MAX_BYTES } from "../../lib/settingsRules";
  import { presetDisplayName, warnFor } from "../../lib/format";
  import LabeledText from "../LabeledText.svelte";

  let { form, hasError }: { form: ServerConfig; hasError: (section: string) => boolean } = $props();

  const warnings = $derived(settingsWarnings(form));
</script>

<div id="section-public-quotes" class="card roomy" class:danger-border={hasError("publicQuotes") || hasError("site") || hasError("shop")}>
  <p class="section-label">Settings</p>
  <h2>Public estimates</h2>
  <p class="hint">The printer and preset that price every visitor estimate. The intro text appears at the top of your
    public estimate page.</p>
  <div class="field-rows">
    <label class="field">
      <span>Printer</span>
      <select bind:value={form.publicQuotes.printer}>
        {#each form.printers as p (p.id)}<option value={p.id}>{p.name}</option>{/each}
      </select>
    </label>
    <label class="field">
      <span>Preset</span>
      <select bind:value={form.publicQuotes.preset}>
        {#each Object.keys(form.presets) as key (key)}<option value={key}>{presetDisplayName(key)}</option>{/each}
      </select>
    </label>
  </div>
  <div class="field field-textarea">
    <label for="public-intro-text">Intro text</label>
    <!-- No maxlength attribute: the limit is in BYTES (server/config.go's publicIntroTextMaxBytes) and
         HTML maxlength counts UTF-16 units, undercounting multi-byte scripts. The byte counter and
         warning below measure as the server does. -->
    <textarea
      id="public-intro-text"
      rows="5"
      bind:value={form.publicQuotes.introText}
      class:warn={warnFor(warnings, "publicQuotes.introText") !== ""}
      aria-invalid={warnFor(warnings, "publicQuotes.introText") !== ""}
      aria-describedby={warnFor(warnings, "publicQuotes.introText") !== "" ? "public-intro-count public-intro-warn" : "public-intro-count"}
    ></textarea>
    <small id="public-intro-count" class="field-hint">{characterCountText(form.publicQuotes.introText, PUBLIC_INTRO_TEXT_MAX_BYTES)}</small>
    {#if warnFor(warnings, "publicQuotes.introText") !== ""}
      <small id="public-intro-warn" class="warn-text">{warnFor(warnings, "publicQuotes.introText")}</small>
    {/if}
  </div>

  <h3 class="section-label">Storefront</h3>
  <p class="hint">What your server's main address shows to customers. Changes apply without a restart.</p>
  <label class="toggle">
    <input type="checkbox" bind:checked={form.site.shopOnMainPort} />
    <span>Serve the shop on the main address</span>
  </label>
  <small class="field-hint">Turn off to send / to the admin sign-in and hide the shop and estimate page on the main address. A separate public port is not affected.</small>
  <label class="toggle">
    <input type="checkbox" bind:checked={form.site.staffLink} />
    <span>Show a Staff sign-in link in the shop footer</span>
  </label>
  <small class="field-hint">Adds a Staff sign-in link to the footer of the shop that opens the admin app at /admin/.</small>
  <label class="field">
    <span>Largest order quantity</span>
    <input type="number" min="1" max="10000" step="1" bind:value={form.shop.maxOrderQty} aria-describedby="shop-max-qty-hint" />
    <small class="field-hint" id="shop-max-qty-hint">The most units one estimate request or gallery order may carry, from 1 to 10,000. Customers see a message instead of a total above it.</small>
  </label>
  <!-- TEMPORARY plain inputs (v1.2 storefront-core lane): they expose the shop.* server rules. The Settings >
       Storefront card with its preview, the hero image upload, the footer links editor and the accent color
       picker replace this block in phase 2. -->
  <h3 class="section-label">What the shop offers</h3>
  <label class="toggle">
    <input type="checkbox" bind:checked={form.shop.customEstimates} />
    <span>Offer custom estimates</span>
  </label>
  <label class="toggle">
    <input type="checkbox" bind:checked={form.shop.gallery} />
    <span>Offer the item gallery</span>
  </label>
  {#if warnFor(warnings, "shop.gallery") !== ""}<small class="warn-text" role="alert">{warnFor(warnings, "shop.gallery")}</small>{/if}
  <label class="toggle">
    <input type="checkbox" bind:checked={form.shop.acceptingOrders} />
    <span>Accepting orders</span>
  </label>
  <small class="field-hint">Turn off for vacation mode: the shop stays visible, your message replaces the submit buttons, and new orders and estimates are refused.</small>
  <LabeledText
    label="Message while not accepting orders"
    bind:value={form.shop.pausedMessage}
    placeholder="Closed until January 5"
    warn={warnFor(warnings, "shop.pausedMessage")}
  />
  <label class="field">
    <span>Opening view</span>
    <select bind:value={form.shop.defaultView}>
      <option value="gallery">Item gallery</option>
      <option value="estimate">Custom estimate</option>
    </select>
  </label>
  <label class="field">
    <span>Item order</span>
    <select bind:value={form.shop.itemSort}>
      <option value="manual">Your manual order</option>
      <option value="name">By name</option>
      <option value="price">By price</option>
    </select>
  </label>
  <LabeledText label="Shop heading" bind:value={form.shop.heading} warn={warnFor(warnings, "shop.heading")} />
  <LabeledText label="Announcement" bind:value={form.shop.announcement} warn={warnFor(warnings, "shop.announcement")} />
  <LabeledText
    label="Accent color"
    bind:value={form.shop.accentColor}
    placeholder="#9c4212"
    warn={warnFor(warnings, "shop.accentColor")}
    hint="Optional. A six-digit color such as #9c4212 for shop buttons and links. It must be readable on the shop background."
  />
  <LabeledText label="Shipping note" bind:value={form.shop.shippingNote} warn={warnFor(warnings, "shop.shippingNote")} />
  <LabeledText label="Turnaround note" bind:value={form.shop.turnaroundNote} warn={warnFor(warnings, "shop.turnaroundNote")} />
  <label class="toggle">
    <input type="checkbox" bind:checked={form.shop.showStock} />
    <span>Show stock counts</span>
  </label>
  <label class="toggle">
    <input type="checkbox" bind:checked={form.shop.showFromPrice} />
    <span>Show "from" prices</span>
  </label>
  <label class="toggle">
    <input type="checkbox" bind:checked={form.shop.showContact} />
    <span>Show my email, phone, and address in the shop</span>
  </label>
  <LabeledText
    label="Staff sign-in link on the public port"
    bind:value={form.site.adminUrl}
    placeholder="https://admin.example.com/admin/"
    hint="Only for a separate public port. The full web address of your admin app, starting with http:// or https://. Leave empty to hide the button there."
  />
</div>

<style>
  /* Same eyebrow-to-heading spacing as InvoicingSettings.svelte's and SettingsTab.svelte's .section-label
   * overrides. */
  .section-label { margin: 0.6rem 0 0.3rem; }
</style>
