<script lang="ts">
  import { onMount } from "svelte";
  import { MediaQuery } from "svelte/reactivity";
  import { DEFAULT_FETCH_TIMEOUT_MS } from "../../lib/fetchTimeouts";
  import { characterCountText } from "../../lib/countText";
  import type { ServerConfig } from "../../lib/settings";
  import { settingsWarnings, PUBLIC_INTRO_TEXT_MAX_BYTES } from "../../lib/settingsRules";
  import { warnFor } from "../../lib/format";
  import { checkAccentColor, isAccentHex, ACCENT_DARK_BACKGROUNDS, ACCENT_LIGHT_BACKGROUNDS, type AccentScheme } from "../../lib/accent";
  import { closedPreviewBranding } from "../../lib/storefrontPreview";
  import { deleteHeroImage, uploadHeroImage } from "../../lib/shopHeroApi";
  import { downscaleImage } from "../../lib/downscaleImage";
  import { announce, focusAfterUpdate } from "../../lib/a11y.svelte";
  import { focusAfterRowRemoval, keySelector } from "../../lib/a11yFocus";
  import ClosedPage from "../../public/ClosedPage.svelte";
  import RichText from "../../public/RichText.svelte";
  import { accentVars, prefersDark } from "../../public/shopAccent";
  import Card from "../ui/Card.svelte";
  import ConfirmInline from "../ui/ConfirmInline.svelte";
  import Field from "../ui/Field.svelte";
  import Fieldset from "../ui/Fieldset.svelte";
  import FieldRow from "./FieldRow.svelte";
  import FormGrid from "../ui/FormGrid.svelte";
  import InlineStatus from "../ui/InlineStatus.svelte";
  import LabeledNumber from "../LabeledNumber.svelte";
  import LabeledText from "../LabeledText.svelte";

  // Settings > Storefront (v1.2): everything the owner controls about the customer shop in one card. It binds into
  // the shared settings form, so Save, Ctrl+S, the unsaved-changes guard and the server's error list work as on
  // every other card. The hero image is the one exception: it has its own endpoints and is stored at once, so
  // `sync` records the result in the saved baseline too and the upload never reads as an unsaved change.
  let {
    form,
    hasError,
    sync,
  }: {
    form: ServerConfig;
    hasError: (section: string) => boolean;
    sync: (mutate: (config: ServerConfig) => void) => void;
  } = $props();

  const warnings = $derived(settingsWarnings(form));

  // ---- View shop. The live shop's address as the server serves it now (saved settings, not the draft): GET
  // api/demo's shopUrl is "/" while site.shopOnMainPort is on, and its publicUrl is the demo's customer site. A
  // separate PUBLIC_PORT address isn't known to the admin app, so then there is no link, only a note.
  let shopUrl = $state("");
  let shopUrlKnown = $state(false);
  onMount(async () => {
    try {
      const res = await fetch("api/demo", { signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS) });
      if (!res.ok) return;
      const body = (await res.json()) as { shopUrl?: string; publicUrl?: string };
      shopUrl = body.shopUrl || body.publicUrl || "";
    } catch {
      // Offline or slow: the note below stands in for the link.
    } finally {
      shopUrlKnown = true;
    }
  });
  const MAX_FOOTER_LINKS = 6;

  // ---- Pause the shop. The stored flag is "accepting orders"; the control says what the owner is doing.
  const paused = $derived(!form.shop.acceptingOrders);

  // ---- Closed page preview: the real ClosedPage with the unsaved values, inert so it can't be tabbed into.
  const darkQuery = new MediaQuery("prefers-color-scheme: dark", false);
  const previewBranding = $derived(closedPreviewBranding(form));
  const accentCheck = $derived(isAccentHex(form.shop.accentColor.trim()) ? checkAccentColor(form.shop.accentColor.trim()) : null);
  const previewVars = $derived.by(() => {
    if (!accentCheck?.ok) return null;
    // darkQuery.current makes the scheme follow the OS; an explicit data-theme on <html> wins, as on the shop.
    return accentVars(prefersDark(document.documentElement, { matches: darkQuery.current }) ? accentCheck.dark : accentCheck.light);
  });

  // ---- Accent color.
  const accentWarn = $derived(warnFor(warnings, "shop.accentColor"));
  function useSuggestion(color: string) {
    form.shop.accentColor = color;
    announce(`Accent color set to ${color}.`);
  }
  function clearAccent() {
    form.shop.accentColor = "";
    announce("Using the theme accent color.");
    void focusAfterUpdate("#storefront-accent");
  }
  const pickerValue = $derived(isAccentHex(form.shop.accentColor.trim()) ? form.shop.accentColor.trim().toLowerCase() : "#9c4212");
  const floor2 = (v: number) => (Math.floor(v * 100) / 100).toFixed(2);

  // The shop backgrounds a swatch shows, and fixed text colors that read on each (a swatch depicts the shop, so it
  // doesn't follow the admin theme).
  const SWATCH = {
    light: { bg: ACCENT_LIGHT_BACKGROUNDS[0], text: "#1f1a14" },
    dark: { bg: ACCENT_DARK_BACKGROUNDS[0], text: "#f2f2f2" },
  } as const;

  // ---- Hero image.
  let draftAlt = $state("");
  let heroBusy = $state(false);
  let heroError = $state("");
  let heroNotice = $state("");
  let confirmRemoveHero = $state(false);
  const hasHero = $derived(form.shop.heroImage.file !== "");
  const altValue = $derived(hasHero ? form.shop.heroImage.alt : draftAlt);
  // Set when a file was chosen before the description; cleared on the next keystroke in the description.
  let altMissing = $state(false);
  const altWarn = $derived(altMissing ? "Describe the image first. The description is required, for people who can't see it." : warnFor(warnings, "shop.heroImage.alt"));

  function setAlt(v: string) {
    altMissing = false;
    if (hasHero) form.shop.heroImage.alt = v;
    else draftAlt = v;
  }

  async function onHeroFile(e: Event) {
    const input = e.currentTarget as HTMLInputElement;
    const file = input.files?.[0];
    input.value = ""; // allow choosing the same file again after an error
    if (!file || heroBusy) return;
    const alt = altValue.trim();
    heroNotice = "";
    if (alt === "") {
      altMissing = true;
      document.getElementById("storefront-hero-alt")?.focus();
      return;
    }
    heroError = "";
    altMissing = false;
    heroBusy = true;
    try {
      const blob = await downscaleImage(file);
      const saved = await uploadHeroImage(blob, alt);
      sync((c) => {
        c.shop.heroImage = { file: saved.file, alt: saved.alt };
      });
      draftAlt = "";
      heroNotice = "Hero image uploaded and saved.";
      announce(heroNotice);
    } catch (err) {
      heroError = err instanceof Error ? err.message : "Couldn't upload the image.";
    } finally {
      heroBusy = false;
    }
  }

  async function removeHero() {
    if (heroBusy) return;
    heroBusy = true;
    heroError = "";
    confirmRemoveHero = false;
    try {
      await deleteHeroImage();
      sync((c) => {
        c.shop.heroImage = { file: "", alt: "" };
      });
      heroNotice = "Hero image removed.";
      announce(heroNotice);
      void focusAfterUpdate("#storefront-hero-file");
    } catch (err) {
      heroError = err instanceof Error ? err.message : "Couldn't remove the image.";
    } finally {
      heroBusy = false;
    }
  }

  // ---- Footer links.
  // Which rows have been typed in: a new blank row stays quiet until then (the save bar still lists real problems).
  let linkTouched = $state<boolean[]>([]);
  const linkWarn = (i: number, part: "label" | "url") => (linkTouched[i] ? warnFor(warnings, `shop.footerLinks[${i}].${part}`) : "");
  function addLink() {
    if (form.shop.footerLinks.length >= MAX_FOOTER_LINKS) return;
    form.shop.footerLinks.push({ label: "", url: "" });
    linkTouched.push(false);
    void focusAfterUpdate(`#storefront-link-label-${form.shop.footerLinks.length - 1}`);
  }
  function removeLink(index: number) {
    form.shop.footerLinks = form.shop.footerLinks.filter((_, i) => i !== index);
    linkTouched.splice(index, 1);
    void focusAfterRowRemoval("remove-footer-link", index, form.shop.footerLinks.length, "add-footer-link");
  }

  const introWarn = $derived(warnFor(warnings, "publicQuotes.introText"));
  const invalid = $derived(hasError("shop") || hasError("site") || hasError("publicQuotes"));
</script>

{#snippet swatch(name: string, scheme: AccentScheme, tone: "light" | "dark", picked: string)}
  <div class="swatch" style:background={SWATCH[tone].bg} style:color={SWATCH[tone].text}>
    <p class="swatch-name">{name}</p>
    <p class="swatch-sample">
      <span class="sample-link" style:color={scheme.accent}>Sample link</span>
      <span class="sample-button" style:background={scheme.accent} style:color={scheme.buttonText}>Sample button</span>
    </p>
    <p class="swatch-detail">
      {scheme.accent}{scheme.accent !== picked ? " (adjusted for dark mode)" : ""}
    </p>
    <p class="swatch-detail">Link text {floor2(scheme.linkRatio)}:1 · Button text {floor2(scheme.buttonRatio)}:1</p>
  </div>
{/snippet}

<Card id="section-storefront" tone={invalid ? "danger" : "default"}>
  <div class="card-head">
    <h2>Storefront</h2>
    {#if shopUrl}
      <div class="view-shop">
        <a class="view-shop-link" href={shopUrl} target="_blank" rel="noopener noreferrer" aria-describedby="view-shop-hint">View shop<span class="visually-hidden"> (opens in a new tab)</span></a>
        <small class="hint" id="view-shop-hint">Shows saved settings. Save first to see your changes.</small>
      </div>
    {:else if shopUrlKnown}
      <small class="hint view-shop">The shop isn't served on this address, so there is no link here. Open it from its public address. It shows saved settings.</small>
    {/if}
  </div>
  <p class="hint">What customers see on your shop. Changes apply as soon as you save, with no restart. The hero image is the one exception: it is saved the moment you upload or remove it.</p>
  <p class="hint">Categories and featured items are set on <a href="#/items" data-testid="storefront-items-link">Shop items</a>: open Categories to add or reorder them, and edit an item to feature it.</p>

  <div class="sections">
    <Fieldset legend="What the shop offers">
      <label class="toggle">
        <input type="checkbox" bind:checked={form.shop.gallery} />
        <span>Item gallery</span>
      </label>
      <label class="toggle">
        <input type="checkbox" bind:checked={form.shop.customEstimates} />
        <span>Custom estimates</span>
      </label>
      {#if warnFor(warnings, "shop.gallery") !== ""}<small class="warn-text" role="alert">{warnFor(warnings, "shop.gallery")}</small>{/if}
      <small class="hint">Turn one off to show only the other. They can't both be off: to stop taking orders, pause the shop below. Materials keep their Public setting for when custom estimates come back.</small>
      <FormGrid align="controls">
        <Field id="storefront-default-view" label="Opening view" size="m" hint="Which one the shop opens on when both are on.">
          {#snippet children(a)}
            <select id={a.id} aria-describedby={a.describedby} bind:value={form.shop.defaultView}>
              <option value="gallery">Item gallery</option>
              <option value="estimate">Custom estimate</option>
            </select>
          {/snippet}
        </Field>
        <Field id="storefront-item-sort" label="Item order" size="m" hint="Featured items always come first.">
          {#snippet children(a)}
            <select id={a.id} aria-describedby={a.describedby} bind:value={form.shop.itemSort}>
              <option value="manual">Your manual order</option>
              <option value="name">By name</option>
              <option value="price">By price</option>
            </select>
          {/snippet}
        </Field>
        <LabeledNumber
          label="Largest order quantity"
          min={1}
          max={10000}
          step={1}
          bind:value={form.shop.maxOrderQty}
          warn={warnFor(warnings, "shop.maxOrderQty")}
          help="The most units one order or estimate may carry, from 1 to 10,000. Customers see a message instead of a total above it."
        />
      </FormGrid>
    </Fieldset>

    <Fieldset legend="Pause the shop">
      <label class="toggle">
        <input
          type="checkbox"
          checked={paused}
          onchange={(e) => (form.shop.acceptingOrders = !e.currentTarget.checked)}
        />
        <span>Pause the shop (vacation mode)</span>
      </label>
      <small class="hint">While paused, every page of the shop shows the Closed page below, and new orders and estimates are refused. Requests you already have are unaffected, and customers can still open their saved confirmations.</small>
      <FormGrid>
        <LabeledText
          label="Message on the Closed page"
          size="l"
          bind:value={form.shop.pausedMessage}
          placeholder="Closed until January 5"
          warn={warnFor(warnings, "shop.pausedMessage")}
        />
      </FormGrid>
      <div class="preview" role="region" aria-label="Closed page preview">
        <p class="preview-caption">
          {paused ? "Customers see this now." : "The shop is open. This is what customers will see while it is paused."}
        </p>
        <Card tone="inset">
          <!-- inert: a picture of the customer page, not controls to tab through. The accent applies here the way it does on the shop. -->
          <div
            class="preview-frame"
            inert
            style:--accent={previewVars?.["--accent"]}
            style:--accent-text={previewVars?.["--accent-text"]}
            style:--focus-ring={previewVars?.["--focus-ring"]}
            style:--accent-contrast={previewVars?.["--accent-contrast"]}
          >
            <ClosedPage
              businessName={form.quote.businessName}
              logoUrl={form.quote.logoUrl.trim()}
              message={form.shop.pausedMessage.trim()}
              branding={previewBranding}
              headingLevel={2}
            />
          </div>
        </Card>
      </div>
    </Fieldset>

    <Fieldset legend="Wording">
      <FormGrid>
        <!-- shop.pageTitle (D30) may be absent from the settings response until the server sends it; the field then
             reads "" and only writes the key once the owner types. -->
        <LabeledText
          label="Browser tab title"
          size="l"
          bind:value={() => form.shop.pageTitle ?? "", (v) => (form.shop.pageTitle = v)}
          placeholder={form.quote.businessName.trim() || "Shop"}
          warn={warnFor(warnings, "shop.pageTitle")}
          hint="Shown in the browser tab and in search results. Leave empty to use your business name."
        />
        <LabeledText label="Shop heading" size="l" bind:value={form.shop.heading} warn={warnFor(warnings, "shop.heading")} hint="Shown above the intro. Leave empty to use your business name." />
      </FormGrid>
      <Field
        id="storefront-intro"
        label="Intro text"
        hint="Appears at the top of the shop. You can use **bold**, *italic*, [link](https://…), and - list."
        error={introWarn !== "" ? introWarn : undefined}
        describedby="storefront-intro-count"
      >
        {#snippet children(a)}
          <!-- No maxlength attribute: the limit is in BYTES (server/config.go's publicIntroTextMaxBytes) and HTML
               maxlength counts UTF-16 units. The counter measures as the server does. -->
          <textarea
            id={a.id}
            rows="4"
            bind:value={form.publicQuotes.introText}
            class:warn={introWarn !== ""}
            aria-invalid={a.invalid ?? "false"}
            aria-describedby={a.describedby}
          ></textarea>
          <small id="storefront-intro-count" class="count">{characterCountText(form.publicQuotes.introText, PUBLIC_INTRO_TEXT_MAX_BYTES)}</small>
        {/snippet}
      </Field>
      {@render textPreview("storefront-intro-preview", "Intro text preview", form.publicQuotes.introText, "intro-preview")}
      <FormGrid>
        <LabeledText label="Announcement" size="l" bind:value={form.shop.announcement} warn={warnFor(warnings, "shop.announcement")} hint="An optional banner at the top of the shop, for notices such as holiday shipping cutoffs. You can use **bold**, *italic*, and [link](https://…)." />
      </FormGrid>
      {@render textPreview("storefront-announcement-preview", "Announcement preview", form.shop.announcement, "announcement-preview")}
      <FormGrid>
        <LabeledText label="Shipping note" size="l" bind:value={form.shop.shippingNote} warn={warnFor(warnings, "shop.shippingNote")} hint="Replaces the default shipping line next to the total. Leave empty to keep it." />
        <LabeledText label="Turnaround note" size="l" bind:value={form.shop.turnaroundNote} warn={warnFor(warnings, "shop.turnaroundNote")} hint="Replaces the default turnaround wording. Leave empty to keep it." />
      </FormGrid>
    </Fieldset>

    <Fieldset legend="Item display">
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
    </Fieldset>

    <Fieldset legend="Hero image">
      <small class="hint">An optional banner across the top of the shop. It is hidden on short screens so items stay in view. JPEG, PNG, or WebP.</small>
      {#if hasHero}
        <img class="hero-thumb" src={"item-images/" + form.shop.heroImage.file} alt={form.shop.heroImage.alt} />
      {/if}
      <FormGrid>
        <Field
          id="storefront-hero-alt"
          label="Image description (alt text)"
          size="l"
          hint="Required. Describes the image for people who can't see it."
          error={altWarn !== "" ? altWarn : undefined}
        >
          {#snippet children(a)}
            <input
              id={a.id}
              value={altValue}
              oninput={(e) => setAlt(e.currentTarget.value)}
              aria-invalid={a.invalid ?? "false"}
              aria-describedby={a.describedby}
              class:warn={altWarn !== ""}
              autocomplete="off"
            />
          {/snippet}
        </Field>
        <Field id="storefront-hero-file" label={hasHero ? "Replace image" : "Upload image"} size="l">
          {#snippet children(a)}
            <input
              id={a.id}
              type="file"
              accept="image/jpeg,image/png,image/webp"
              aria-describedby={a.describedby}
              aria-busy={heroBusy}
              onchange={onHeroFile}
            />
          {/snippet}
        </Field>
      </FormGrid>
      {#if hasHero}
        <div class="row-actions">
          {#if confirmRemoveHero}
            <ConfirmInline
              confirmLabel="Yes, remove the hero image"
              onconfirm={removeHero}
              oncancel={() => (confirmRemoveHero = false)}
              restore={keySelector("remove-hero")}
              danger
              busy={heroBusy}
            >Removing it deletes the image from the shop at once.</ConfirmInline>
          {:else}
            <button type="button" data-focus-key="remove-hero" aria-disabled={heroBusy} onclick={() => { if (!heroBusy) confirmRemoveHero = true; }}>Remove hero image</button>
          {/if}
        </div>
      {/if}
      {#if heroBusy}<InlineStatus kind="saving">Saving the image…</InlineStatus>{/if}
      {#if heroError}<InlineStatus kind="error">{heroError}</InlineStatus>{/if}
      {#if heroNotice && !heroBusy && !heroError}<InlineStatus kind="success">{heroNotice}</InlineStatus>{/if}
    </Fieldset>

    <Fieldset legend="Accent color">
      <small class="hint">Colors the shop's buttons and links. It must be readable on the shop background in both light and dark mode. Dark mode gets an automatically adjusted variant when needed. Leave empty to use the theme accent.</small>
      <FormGrid>
        <Field id="storefront-accent" label="Accent color" size="s" hint="A six-digit color such as #9c4212." error={accentWarn !== "" ? accentWarn : undefined}>
          {#snippet children(a)}
            <input
              id={a.id}
              bind:value={form.shop.accentColor}
              placeholder="#9c4212"
              autocomplete="off"
              spellcheck="false"
              class:warn={accentWarn !== ""}
              aria-invalid={a.invalid ?? "false"}
              aria-describedby={a.describedby}
            />
          {/snippet}
        </Field>
        <Field id="storefront-accent-picker" label="Pick a color" size="xs">
          {#snippet children(a)}
            <input
              id={a.id}
              type="color"
              value={pickerValue}
              aria-describedby={a.describedby}
              oninput={(e) => (form.shop.accentColor = e.currentTarget.value)}
            />
          {/snippet}
        </Field>
      </FormGrid>
      <div class="row-actions">
        {#if accentCheck && !accentCheck.ok && accentCheck.suggestion}
          <button type="button" onclick={() => useSuggestion(accentCheck.suggestion)}>Use {accentCheck.suggestion} instead</button>
        {/if}
        {#if form.shop.accentColor.trim() !== ""}
          <button type="button" onclick={clearAccent}>Use the theme accent</button>
        {/if}
      </div>
      {#if accentCheck?.ok}
        <div class="swatches" role="group" aria-label="Accent color preview">
          {@render swatch("Light mode", accentCheck.light, "light", accentCheck.accent)}
          {@render swatch("Dark mode", accentCheck.dark, "dark", accentCheck.accent)}
        </div>
        <small class="hint">Both variants pass 4.5:1 for link text and button text.</small>
      {:else if form.shop.accentColor.trim() === ""}
        <small class="hint">No accent set: the shop uses the theme accent.</small>
      {/if}
    </Fieldset>

    <Fieldset legend="Footer links">
      <small class="hint">Up to {MAX_FOOTER_LINKS} links in the shop footer, such as your Etsy shop or policies. Addresses must start with https://.</small>
      {#each form.shop.footerLinks as link, i (i)}
        <FieldRow removeLabel={`Remove link ${i + 1}`} focusKey={`remove-footer-link-${i}`} onremove={() => removeLink(i)}>
          <FormGrid>
            <Field id={`storefront-link-label-${i}`} label={`Link ${i + 1} label`} size="m" error={linkWarn(i, "label") || undefined}>
              {#snippet children(a)}
                <input id={a.id} bind:value={link.label} oninput={() => (linkTouched[i] = true)} onblur={() => { if (link.label !== "") linkTouched[i] = true; }} aria-invalid={a.invalid ?? "false"} aria-describedby={a.describedby} autocomplete="off" />
              {/snippet}
            </Field>
            <Field id={`storefront-link-url-${i}`} label={`Link ${i + 1} address`} size="l" error={linkWarn(i, "url") || undefined}>
              {#snippet children(a)}
                <input id={a.id} type="url" bind:value={link.url} oninput={() => (linkTouched[i] = true)} onblur={() => { if (link.url !== "") linkTouched[i] = true; }} placeholder="https://" aria-invalid={a.invalid ?? "false"} aria-describedby={a.describedby} autocomplete="off" />
              {/snippet}
            </Field>
          </FormGrid>
        </FieldRow>
      {/each}
      {#if warnFor(warnings, "shop.footerLinks") !== ""}<small class="warn-text" role="alert">{warnFor(warnings, "shop.footerLinks")}</small>{/if}
      <div class="row-actions">
        <button type="button" data-focus-key="add-footer-link" aria-disabled={form.shop.footerLinks.length >= MAX_FOOTER_LINKS} onclick={addLink}>Add link</button>
        {#if form.shop.footerLinks.length >= MAX_FOOTER_LINKS}<small class="hint">That's the most links the footer takes.</small>{/if}
      </div>
    </Fieldset>

    <Fieldset legend="Where the shop appears">
      <label class="toggle">
        <input type="checkbox" bind:checked={form.site.shopOnMainPort} />
        <span>Serve the shop on the main address</span>
      </label>
      <small class="hint">Turn off to send / to the admin sign-in and hide the shop on the main address. A separate public port is not affected.</small>
      <label class="toggle">
        <input type="checkbox" bind:checked={form.site.staffLink} />
        <span>Show a Staff sign-in link in the shop footer</span>
      </label>
      <small class="hint">Adds a Staff sign-in link to the footer of the shop that opens the admin app at /admin/.</small>
      <FormGrid>
        <LabeledText
          label="Staff sign-in link on the public port"
          size="l"
          bind:value={form.site.adminUrl}
          placeholder="https://admin.example.com/admin/"
          hint="Only for a separate public port. The full web address of your admin app, starting with http:// or https://. Leave empty to hide the button there."
        />
      </FormGrid>
    </Fieldset>
  </div>
</Card>

{#snippet textPreview(id: string, caption: string, text: string, testid: string)}
  {#if text.trim() !== ""}
    <div class="md-preview" {id} data-testid={testid}>
      <p class="preview-caption">{caption}, as customers see it</p>
      <div class="md-preview-body"><RichText {text} /></div>
    </div>
  {/if}
{/snippet}

<style>
  .card-head { display: flex; flex-wrap: wrap; align-items: flex-start; justify-content: space-between; gap: var(--space-2) var(--space-4); margin-bottom: var(--space-3); }
  .card-head h2 { margin: 0; }
  .view-shop { display: flex; flex-direction: column; align-items: flex-end; max-width: var(--field-l); text-align: right; }
  .view-shop .hint { margin: 0; }
  .view-shop-link { font-weight: 600; }
  @media (max-width: 600px) {
    .view-shop { align-items: flex-start; text-align: left; }
  }
  .sections { display: flex; flex-direction: column; gap: var(--gap-section); }
  .row-actions { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); }
  .count { color: var(--muted); font-size: var(--type-secondary); }
  .md-preview { display: flex; flex-direction: column; gap: var(--space-1); max-width: var(--measure-record); }
  .md-preview-body { padding: var(--space-3) var(--space-4); border-radius: var(--radius); background: var(--surface-2); overflow-wrap: anywhere; }
  .preview { display: flex; flex-direction: column; gap: var(--space-2); }
  .preview-caption { margin: 0; color: var(--muted); font-size: var(--type-secondary); }
  /* The shop's reading column, so the preview wraps as customers' pages do. */
  .preview-frame { max-width: var(--measure-record); }
  .hero-thumb { display: block; width: 100%; max-width: var(--field-l); aspect-ratio: 3 / 1; object-fit: cover; border-radius: var(--radius); background: var(--surface-2); }
  .swatches { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, var(--field-m)), 1fr)); gap: var(--gap); }
  .swatch { display: flex; flex-direction: column; gap: var(--space-2); padding: var(--space-4); border-radius: var(--radius); }
  .swatch p { margin: 0; }
  .swatch-name { font-weight: 700; }
  .swatch-sample { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-3); }
  .sample-link { text-decoration: underline; font-weight: 600; }
  .sample-button { padding: var(--space-2) var(--space-4); border-radius: calc(var(--radius) / 2); font-weight: 600; }
  .swatch-detail { font-size: var(--type-secondary); }
</style>
