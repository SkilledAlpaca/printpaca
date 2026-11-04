# Accessibility

printpaca targets [WCAG 2.2](https://www.w3.org/TR/WCAG22/) Level AA for the admin app, the sign-in screen, and the customer pages (the shop, served at `/` and `/order`). Version 1.0.4 is the first release built to that target. This page states what is covered, how it's tested, how to use printpaca without a mouse or with a screen reader, what isn't fixed, and how to report a problem. Contributors: see [Contributor guidance](#contributor-guidance).

This is a statement of intent and of testing done, not a certification. No third party has audited printpaca.

## How it's tested

Three layers back the conformance target:

- **An automated gate.** [`e2e/a11y.spec.ts`](../e2e/a11y.spec.ts) runs [axe-core](https://github.com/dequelabs/axe-core) with the tags `wcag2a`, `wcag2aa`, `wcag21a`, `wcag21aa`, and `wcag22aa`. CI runs it as part of `npm run e2e`, and any violation fails the build.
- **Manual review.** Before 1.0.4, the code was reviewed by hand against every WCAG 2.2 A and AA success criterion, and a separate scan checked reflow, resize, text spacing, and keyboard Tab order. The fixes in 1.0.4 came from that review.
- **No real screen-reader testing.** Nobody has tested printpaca with NVDA, JAWS, VoiceOver, or TalkBack. The screen-reader behavior described below comes from the markup and from the automated checks, so treat it as intended behavior, not verified behavior. If it doesn't match what you hear, [report it](#report-an-accessibility-problem).

### What the gate covers

| Dimension | Coverage |
|---|---|
| Pages | The sign-in screen (empty, and after a failed submit), and the shop: gallery, an open item panel, the custom estimate form (empty, with a live estimate, and with validation errors), and the order and estimate confirmations. In the admin app: all 12 tabs (New quote, Quotes, Requests, Invoices, Items, Clients, Dashboard, Filament, Electricity, Printers, Settings, and Guide) and one open detail view each for an invoice, a client, a request, and an item (with option groups). |
| Themes | Light and dark, by emulating the system color scheme. |
| Viewports | Desktop (1280 by 900 px) and phone (390 by 844 px). |
| Reflow | At 320 by 640 px (light theme), the page has no horizontal scroll (WCAG 1.4.10). |
| Heading and title | Each detail view has exactly one `h1`, and its text is the record segment of the browser tab title (WCAG 1.3.1, 2.4.2). |
| Focus | The mobile drawer: focus moves into it on open, back to the menu button on Escape, and to the main content after you pick a page. A delete confirm: focus lands on the confirm button. The shop: opening an item focuses its heading and the next Tab reaches Back to gallery; Escape also goes back, and the page says so. Going back focuses the card that opened the item, and a failed submit focuses the first invalid field and links it to its message (a required option group's error sits under its select). Error messages go away as soon as the problem is fixed. Submitted orders and estimate requests focus the confirmation heading. The sign-in error focuses the password field. |
| Sticky bars | On Settings, a focused field stays clear of the sticky save bar and the jump navigation (WCAG 2.4.11). |
| Shortcuts | A bare `/` isn't a shortcut (WCAG 2.1.4). |

### What the gate doesn't cover

- Anything axe can't judge: whether alternative text, labels, and live announcements make sense, and contrast on gradients or overlapping content.
- Hover, focus, and open states beyond those listed above, such as an open row menu or every Settings section's error states.
- The 3D model preview, print views, and the HTML emails printpaca sends.
- Reflow in the dark theme, and browser zoom. The manual text-resize check narrowed the viewport instead of zooming.

Run the gate yourself with the steps in [Run the accessibility spec](#run-the-accessibility-spec).

## Keyboard use

Every control works from the keyboard, and the focus indicator is visible on every control.

| Key | Action |
|---|---|
| Tab and Shift+Tab | Move through controls. A **Skip to content** link is the first stop in the admin app; it moves focus to the main content. |
| Ctrl+K (⌘K on macOS) | On every page that has a list filter (Quotes, Clients, Invoices, Requests, Shop items, Filament spools, and Recent prints on New quote), focus the filter. It's ignored while focus is in a text field, select, or editable area. |
| Ctrl+S (⌘S on macOS) | In every editor that has a Save button: New quote, the shop item editor, Settings, the Filament materials, Electricity, and Printers, invoice drafts, and client details. It works from inside a text field. |
| Arrow Up, Arrow Down, Home, End | In an open row menu (a Quotes row, or More on Requests), move between its items. Arrow keys wrap at the ends. Tab closes the menu and puts focus on its toggle, so the next Tab continues from there. |
| Escape | Close the mobile menu, a row menu, or an open record's detail view (Clients, Invoices, and Requests). When a two-step confirm is armed, Escape cancels the confirm first and leaves the record open. Inside a text field or select, Escape keeps its normal meaning and doesn't close the record. |

Ctrl+K and Ctrl+S need a modifier key, so no single-key shortcut can fire by accident (WCAG 2.1.4). The sidebar footer lists only the shortcuts that work on the page you're on, and the Guide lists them all. Before 1.0.4 the filter shortcut was a bare `/`; it no longer does anything.

### Focus behavior

printpaca moves focus so you never land on a blank page:

- Opening a record focuses its heading. Going back to the list focuses the row that opened it.
- A two-step confirm (for example **Delete client** then **Delete client?**) moves focus to the confirm button. Cancel or Escape returns focus to the original button. After a confirmed delete, focus goes to the next row, then the previous row, then the list heading. A held Enter or Space key doesn't trigger the confirm.
- Opening the mobile menu focuses its first item. Closing it returns focus to the menu button. Picking a page focuses the main content.
- Submitting an order or an estimate request focuses the confirmation heading.
- Loading more rows focuses the first new row.
- Actions that take time, such as saving, keep focus on their button. The button gets `aria-disabled="true"` and a busy style, and repeat presses are ignored, so focus is never lost to a disabled button.
- The sticky save bars and jump navigation reserve scroll padding, so a focused field is never hidden behind them (WCAG 2.4.11).
- The Settings jump navigation wraps onto several lines and sticks to the top at 600 px and wider. Below 600 px it scrolls with the page, so it never covers a large part of a small screen. A jump lands with the section heading fully visible below the navigation.
- The business name and logo at the top of the sidebar (and of the top bar on a phone) are a link named "<business name> home". Activating it opens the first page in the menu, closes the mobile menu, and moves focus to the page's heading.
- After you drop an STL or 3MF file that has no slicer time, a note under the print time fields says so. The fields are described by that note and are marked invalid only after you leave them or try to save with Ctrl+S or Cmd+S. When the price can't be calculated, the cost bar and the Price and profit card list each missing field as a link that moves focus to it. A "What is a slicer?" disclosure explains the word on demand.
- A **Skip to model weight and print time** link at the top of New quote moves focus past the model drop zone and the Recent prints list.
- A saved quote open in the editor is headed "Edit quote: <label>", and the sidebar marks Quotes (not New quote) as the current page. A browser reload, or leaving New quote with unsaved edits, asks for confirmation.

### 3D model preview

When a visitor drops an STL or 3MF file on the shop's estimate form, the page shows a 3D preview. Focus the preview and press the arrow keys to rotate it, or Home to reset the view. The **Rotate left**, **Rotate right**, and **Reset view** buttons below it do the same without dragging. A vertical swipe on a phone scrolls the page instead of rotating the model.

## Screen readers

These behaviors are built in, but see the note about [testing](#how-its-tested): none have been confirmed with a real screen reader.

- **Live announcements.** A hidden polite region and a hidden assertive region, mounted once in the admin app and once in the customer pages, announce results that have no visible message of their own. Examples are a saved quote, a failed save, a loading state, and the live price on the shop page.
- **Form errors.** An invalid field has `aria-invalid="true"` and an `aria-describedby` that points at its message. Forms that list several errors show an error summary: an alert region with one button per error that moves focus to the field.
- **Headings and landmarks.** Every view has exactly one `h1`. The sign-in screen and the customer pages sit in a `main` landmark. The sidebar groups (Sell and Shop) are labeled groups.
- **Shop addresses.** Each shop view has its own address (`#/item/<id>`, `#/estimate`, and the two confirmations), so the browser's Back button, reload, and shared links work. When a confirmation is no longer available, the page returns to the gallery and a status message says why.
- **Page titles.** The browser tab title names the current view, in the form `Quotes · printpaca`. In a detail view it adds the record, as in `Invoice INV-2026-0016 · Invoices · printpaca`. The sign-in screen is `Sign in · printpaca`. When you set a **Business name** in Settings, it replaces `printpaca` in every title (`Quotes · Alpaca Prints`), and the sign-in screen uses it too when the server offers the public branding. The shop's title changes with the step the visitor is on.
- **Dashboard chart.** The revenue chart is an image with a text summary of the range and total. A **Revenue by month as a table** disclosure under it lists the same numbers.
- **Cost breakdown.** The legend shows each segment's percentage in text, so color isn't the only cue.
- **Names.** Visible text is part of every control's accessible name, so speech-input users can say what they see (WCAG 2.5.3). Help text, hints, warnings, and counters are linked to their fields as descriptions and are not part of the name.
- **Unread requests.** The sidebar item and the mobile menu button both include the unread count in their names.
- **Confirmations before consequential actions.** printpaca asks before it marks an invoice sent, emails an invoice or a quote, changes a quote's status, finalizes requests, records a payment above the balance, or deletes all new requests. The confirm button names the action ("Yes, mark as sent", "Yes, delete 8 requests") and only a destructive confirm is red. The delete-all confirm states the count and that the stored files go too, and focus starts on Cancel.

## Theme and contrast

printpaca has a light and a dark theme. Both follow your system preference (`prefers-color-scheme`), and the admin app has no theme switch of its own. The demo build adds a per-browser theme choice.

The color tokens in `web/src/theme.css` are chosen to meet the following minimum contrast ratios against the page, surface, and raised-surface backgrounds. Body text clears 11:1 in both themes.

| Token | Use | Light | Dark | Requirement |
|---|---|---|---|---|
| `--muted` | Secondary text | 5.09:1 or more | 5.77:1 or more | 4.5:1 (WCAG 1.4.3) |
| `--control-border` | Borders of inputs, selects, text areas, checkboxes, radio buttons, and the file drop zone | 3.44:1 or more | 3.39:1 or more | 3:1 (WCAG 1.4.11) |
| `--focus-ring` | The keyboard focus outline | 5.39:1 or more | 8.06:1 or more | 3:1 (WCAG 1.4.11) |
| `--accent-contrast` | Text and focus outline on accent-filled buttons | 4.76:1 or more | 8.06:1 or more | 4.5:1 and 3:1 |

`--border` stays lighter than `--control-border` because it only draws decorative card edges.

Your own [custom CSS and logo](configuration.md#custom-css) aren't checked. If you override theme tokens, keep these ratios.

## Reduced motion

When your system requests reduced motion (`prefers-reduced-motion: reduce`), printpaca shortens every transition and animation to an instant change, and scroll jumps (for example, from a Settings jump link) happen without smooth scrolling.

## Known limitations

These are the gaps left in 1.0.4:

- **Gallery descriptions are clipped.** On the shop gallery, an item's description is cut to one line. With increased text spacing (WCAG 1.4.12) it can be cut further. The full text is in the item panel that opens when you select the card.
- **Collapsed sidebar tooltips.** When you collapse the admin sidebar to icons, the tab names and the Spoolman, Moonraker, and Fluidd status lines appear as `title` tooltips. Those tooltips show on mouse hover only, not on keyboard focus. Screen readers still hear the names, because the text stays in the page. Expand the sidebar to see the labels.
- **No screen-reader or assistive-technology testing.** See [How it's tested](#how-its-tested).
- **Print views and emails.** The printed quote, cost audit, and invoice views were scanned in emulated print media during review but aren't in the automated gate. The HTML and plain-text emails (quotes, invoices, and request notifications) weren't audited.
- **Gate coverage is partial.** See [What the gate doesn't cover](#what-the-gate-doesnt-cover).
- **Ctrl+S needs an editor.** Pages without a Save button, such as the Dashboard, don't use it, so the browser's own Save command runs there.
- **User content.** Logos, item photos, custom CSS, and text you enter aren't checked. Item photos and logos are treated as decorative, because the name is always next to them.
- **Browsers.** The gate runs in Chromium. Firefox and Safari aren't tested automatically.

## Report an accessibility problem

[Open an accessibility issue on GitHub](https://github.com/SkilledAlpaca/printpaca/issues/new?template=accessibility.yml). Include the page, your browser, and any assistive technology you use, and say what you expected and what happened instead. A screenshot or a short recording helps.

Don't use the private security channel for accessibility problems. [SECURITY.md](../SECURITY.md) is only for vulnerabilities.

## Contributor guidance

Accessibility is a release gate: `npm run e2e` fails if `e2e/a11y.spec.ts` finds a violation. Follow these conventions so new UI passes on the first run.

### Helpers

- **`announce(message, politeness)`** in `web/src/lib/a11y.svelte.ts` posts a status message to the live regions. Use it for every success, failure, and progress result that doesn't already appear in a visible, persistent live region. The default is `"polite"`. Use `"assertive"` only for errors. Don't add a one-off `aria-live` region for a transient result. `LiveAnnouncer` is mounted in both app roots (`App.svelte` and `PublicApp.svelte`).
- **`focusAfterUpdate(target)`** in the same file moves focus after Svelte applies the pending DOM updates. Use it whenever the focused element is removed, replaced, or disabled by the state change. The target can be an element, a selector, or a getter.
- **`confirmFocus`** in `web/src/lib/a11yFocus.ts` is a Svelte action for the confirm button of a two-step confirm. It takes focus on mount, handles Escape, ignores key auto-repeat, and returns focus to the trigger (pass `restore` with a `data-focus-key` selector).
- **`refocusIfLost(selector)`** in the same file refocuses a control only if focus fell to the page body. Use it after an action that replaces the control you pressed. The same file has `focusRowOrHeading` for lists, `focusAfterRowRemoval` for editable rows, and `focusOnMount`.
- **`ErrorSummary`** (`web/src/components/ErrorSummary.svelte`) renders a form's error list as an alert with a button per error, and sets `aria-invalid` and `aria-describedby` on the matching control. Use it instead of a hand-written error list.
- **`DetailTitle`** (`web/src/components/DetailTitle.svelte`) is the one `h1` of a record's detail view. It takes focus on mount and sets the record segment of the page title. Every view has exactly one `h1`.
- **`ScrollRegion`** (`web/src/components/ScrollRegion.svelte`) wraps a horizontally scrolling table that has no focusable content, so keyboard users can scroll it.

### In-flight actions

Don't set `disabled` on a button while its request runs: a focused control that becomes disabled drops focus to the page body. Set `aria-disabled` and `aria-busy` instead, and return early at the top of the handler:

```svelte
<button type="button" class="primary" onclick={save} aria-disabled={saving || !dirty} aria-busy={saving}>
  {saving ? "Saving…" : "Save"}
</button>
```

```ts
async function save() {
  if (saving || !dirty) return; // aria-disabled doesn't block clicks
  // ...
}
```

Keep `disabled` for actions that are truly unavailable, such as a validation failure or a missing permission. `theme.css` styles `[aria-disabled="true"]` and `[aria-busy="true"]`.

### Names, labels, and targets

- If a control has an `aria-label`, it must start with the control's visible text, or the visible text must be part of the name (WCAG 2.5.3).
- Keep help, hint, warning, and counter text out of a `<label>`. Link it with `aria-describedby`.
- Make targets at least 24 by 24 CSS px, or space them 24 px apart (WCAG 2.5.8).
- Don't put information only in a `title` attribute.
- Give every input an `autocomplete` token when the field has a standard one.

### Color tokens

Use the tokens in `web/src/theme.css`, not new hex values. Use `--control-border` for the border of any input, select, text area, checkbox, or drop zone. Use `--focus-ring` for focus outlines (the global `:focus-visible` rule already does), and `--accent-contrast` for the outline on accent-filled controls. Use `--muted` for secondary text, and don't mute text further with `opacity`. If you must add a color, compute its contrast against `--bg`, `--surface`, and `--surface-2` in both themes, and update the table in [Theme and contrast](#theme-and-contrast).

### Run the accessibility spec

Run the one spec against the e2e server:

```bash
npx playwright install --with-deps chromium   # one time
npx playwright test -c e2e a11y.spec.ts
```

`npm run e2e` also runs it. A failure lists every violation on every page together, with the axe rule, the count, and the first selector. Add a new page or detail view to `TABS` or to the detail-view tests in `e2e/a11y.spec.ts` when you add one.
