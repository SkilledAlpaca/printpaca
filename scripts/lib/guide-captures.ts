// The captures for scripts/guide-shots.ts: for each Guide image, drive the admin app to the right state and
// highlight the control the step names. The keys must match web/src/lib/guideContent.ts. Order matters where a
// capture saves data a later one shows, so each topic starts from a known page.
import type { Browser, Locator, Page } from "@playwright/test";
import type { Shooter } from "./guide-shooter.ts";

export interface Env {
  adminUrl: string;
  publicUrl: string;
  spoolmanUrl: string;
  moonrakerUrl: string;
  browser: Browser;
}

// Highlight targets are the controls themselves; the shooter adds each control's label to the ring and rejects a
// row, grid, fieldset, card, or the save bar (guide-shooter.ts measureTarget). fieldOf and labelOf are for crop
// regions only.
/** The ui/Field wrapper (label, control, hint) around a control. */
const fieldOf = (l: Locator): Locator => l.locator("xpath=ancestor::div[contains(concat(' ',normalize-space(@class),' '),' ui-field ')][1]");

const labelOf = (l: Locator): Locator => l.locator("xpath=ancestor::label[1]");

const hash = (e: Env, route: string) => `${e.adminUrl}/admin/#/${route}`;

async function go(page: Page, env: Env, route: string): Promise<void> {
  await page.evaluate((h) => { location.hash = h; }, `#/${route}`);
  await page.waitForTimeout(500);
}

/** Opens the editor page on a blank New quote (a reload clears any unsaved state). */
async function blankQuote(page: Page, env: Env): Promise<void> {
  // A fresh document, so no state from an earlier quote carries over.
  await page.goto("about:blank");
  await page.goto(hash(env, "quote"));
  await page.getByLabel("Model weight (g)").waitFor({ timeout: 8000 }).catch(async (e) => {
    await page.screenshot({ path: "/scratch/fail.png" });
    throw e;
  });
}

/** Waits until the summary shows a computed cost (pricing is server-side and debounced). */
async function waitForCost(page: Page): Promise<void> {
  await page.locator(".summary-stats .stat.cost .stat-value").filter({ hasText: /\d/ }).first().waitFor({ timeout: 15_000 });
}

export async function captureAll(s: Shooter, env: Env): Promise<void> {
  const { page } = s;
  await quoteTopics(s, env);
  await multiPartTopic(s, env);
  await saveTopic(s, env);
  await requestTopic(s, env);
  await invoiceTopic(s, env);
  await setupTopic(s, env);
  await itemTopic(s, env);
  await storefrontTopic(s, env);
  await spoolTopic(s, env);
  await printerTopic(s, env);
  await usersTopic(s, env);
  await dashboardTopic(s, env);
  await dashboardTopicEnd(s);
  await electricityTopic(s, env);
  void page;
}

async function quoteTopics(s: Shooter, env: Env): Promise<void> {
  const { page } = s;
  await blankQuote(page, env);
  const group = (name: RegExp) => page.getByRole("group", { name });
  const sec1 = group(/Model and print time/);
  const sec2 = group(/Material and printer/);
  await s.shot({ key: "quote-overview", targets: [sec1.locator("legend")], region: [page.getByRole("heading", { name: "New quote", level: 1 }), sec1] });
  await page.getByLabel("Model weight (g)").fill("48");
  await page.getByLabel("Print time (hours)").fill("2");
  await page.getByLabel("Print time (minutes)").fill("30");
  await waitForCost(page);
  await s.shot({ key: "quote-weight", targets: [page.getByLabel("Model weight (g)")], region: sec1 });
  await s.shot({ key: "quote-time", targets: [page.getByLabel("Print time (hours)"), page.getByLabel("Print time (minutes)")], region: sec1 });
  await waitForCost(page);
  await s.shot({ key: "quote-material", targets: [page.getByLabel("Material", { exact: true })], region: sec2 });
  const summary = page.locator('[role="group"][aria-label="Quote summary"]');
  await s.shot({ key: "quote-cost", targets: [page.locator(".summary-stats .stat.cost")], region: summary });
  await page.getByRole("button", { name: "Set a client price" }).click();
  await summary.getByText("Client price", { exact: true }).waitFor();
  await page.waitForTimeout(600);
  const stat = (name: string) => (name === "Client price" ? page.locator(".summary-stats .group").first() : page.locator(".summary-stats .stat").filter({ hasText: new RegExp(`^${name}`) }).first());
  await s.shot({ key: "quote-client-price", targets: [stat("Client price"), stat("Profit"), stat("Margin")], region: summary });
}

async function setupTopic(s: Shooter, env: Env): Promise<void> {
  const { page } = s;
  await go(page, env, "settings");
  const card = page.locator("#section-quote");
  await s.shot({ key: "setup-business-card", targets: [card.getByRole("heading", { name: "Business identity" })], region: [card.getByRole("heading", { name: "Business identity" }), card.getByLabel("Business name")] });
  await card.getByLabel("Business name").fill("Lantern Print Works");
  await card.getByLabel("Email", { exact: true }).fill("hello@lantern.example");
  await s.shot({ key: "setup-business-fields", targets: [card.getByLabel("Business name"), card.getByLabel("Email", { exact: true })], region: [card.getByRole("heading", { name: "Business identity" }), fieldOf(card.getByLabel("Order reference prefix"))] });
  await s.shot({ key: "setup-save-bar", targets: [page.getByRole("button", { name: "Save changes" })], region: page.locator(".save-actions") });
  await page.getByRole("button", { name: "Save changes" }).click();
  await page.getByText(/saved/i).first().waitFor({ timeout: 10_000 }).catch(() => {});
  await go(page, env, "filament");
  await page.getByRole("button", { name: "Edit PLA" }).click();
  await page.getByLabel("Material 1 price per kg").fill("21.5");
  await s.shot({ key: "setup-material-price", targets: [page.getByLabel("Material 1 price per kg")], region: page.getByRole("table").first() });
  // The image shows the example edit; put the demo price back so later images keep the demo filament's PLA price.
  await page.getByLabel("Material 1 price per kg").fill("22.99");
  await page.getByRole("button", { name: "Done editing PLA" }).click();
  // Back at the demo price nothing is unsaved, so Save changes may not be offered.
  const savePrice = page.getByRole("button", { name: "Save changes" });
  if (await savePrice.isVisible()) await savePrice.click();
  await go(page, env, "printers");
  await page.getByLabel("Printer 1 name").fill("Workshop printer");
  await page.getByLabel("Printer 1 watts").fill("120");
  const row = page.getByLabel("Printer 1 name").locator("xpath=ancestor::*[self::fieldset or self::section or self::div][.//input[@aria-label='Printer 1 watts']][1]");
  await s.shot({ key: "setup-printer", targets: [page.getByLabel("Printer 1 name"), page.getByLabel("Printer 1 watts")], region: row });
  await page.getByRole("button", { name: "Save changes" }).click();
}

async function fillPart(page: Page, weight: string, hours: string, minutes: string): Promise<void> {
  await page.getByLabel("Model weight (g)").fill(weight);
  await page.getByLabel("Print time (hours)").fill(hours);
  await page.getByLabel("Print time (minutes)").fill(minutes);
}

async function multiPartTopic(s: Shooter, env: Env): Promise<void> {
  const { page } = s;
  await blankQuote(page, env);
  await fillPart(page, "48", "2", "30");
  await waitForCost(page);
  const sec1 = page.getByRole("group", { name: /Model and print time/ });
  const addPart = page.getByRole("button", { name: "Add part" });
  await s.shot({ key: "multi-add-part", targets: [addPart], region: [page.getByRole("heading", { name: "New quote", level: 1 }), addPart, page.getByText(/^Quoting several printed parts/)] });
  await addPart.click();
  const tabs = page.getByRole("tablist");
  await tabs.waitFor();
  await page.getByLabel("Part name").fill("Lid");
  await fillPart(page, "22", "1", "15");
  await waitForCost(page);
  await s.shot({ key: "multi-part-fields", targets: [page.getByLabel("Part name"), page.getByLabel("Model weight (g)"), page.getByLabel("Print time (hours)"), page.getByLabel("Print time (minutes)")], region: [tabs, sec1] });
  await page.getByRole("tab", { name: /Part 1/ }).click();
  await s.shot({ key: "multi-tabs", targets: [page.getByRole("tab", { name: /Part 1/ })], region: [tabs, sec1] });
  const summary = page.locator('[role="group"][aria-label="Quote summary"]');
  await s.shot({ key: "multi-summary", targets: [summary.getByText(/Order of 2 parts/).first()], region: summary });
}

async function saveTopic(s: Shooter, env: Env): Promise<void> {
  const { page } = s;
  await blankQuote(page, env);
  await fillPart(page, "48", "2", "30");
  await waitForCost(page);
  const summary = page.locator('[role="group"][aria-label="Quote summary"]');
  const client = page.getByLabel("Client for this quote");
  const label = page.getByLabel("Label for this quote");
  const saveBox = [summary.getByRole("heading", { name: "Save this quote" }), page.getByRole("button", { name: "Save quote" })];
  await page.getByRole("button", { name: "Set a client price" }).click();
  await summary.getByText("Client price", { exact: true }).waitFor();
  await client.fill("Alex Merriweather");
  await page.getByText("New client. Saving adds them to your clients.").waitFor();
  await s.shot({ key: "save-client", targets: [client], region: saveBox });
  await label.fill("Cable clip, 48 g");
  await s.shot({ key: "save-label", targets: [label], region: saveBox });
  await s.shot({ key: "save-button", targets: [page.getByRole("button", { name: "Save quote" })], region: saveBox });
  await page.getByRole("button", { name: "Save quote" }).click();
  await page.getByRole("button", { name: "Update quote" }).waitFor();
  await page.getByText('"Cable clip, 48 g" saved.').waitFor();
  await s.shot({ key: "save-update", targets: [page.getByRole("button", { name: "Update quote" }), page.getByRole("button", { name: "Save as new" })], region: [summary.getByRole("heading", { name: "Save this quote" }), page.getByText('"Cable clip, 48 g" saved.')] });
  const recent = page.getByRole("heading", { name: "Saved quotes" });
  await s.shot({ key: "save-recent", targets: [page.getByRole("button", { name: /^Open/ }).first()], region: [recent, page.getByRole("button", { name: /^Open/ }).nth(3)] });
  await go(page, env, "quotes");
  const row = page.locator("tbody tr", { hasText: "Cable clip, 48 g" }).first();
  await row.waitFor();
  await s.shot({ key: "save-quotes-row", targets: [row.locator(".badge").first(), row.getByRole("button", { name: "Mark sent" }), row.getByRole("button", { name: /^More actions/ })], region: [page.locator("thead").first(), row] });
  await blankQuote(page, env);
  await fillPart(page, "48", "2", "30");
  await waitForCost(page);
  await page.getByRole("button", { name: "Set a client price" }).click();
  await page.locator('[role="group"][aria-label="Quote summary"]').getByText("Client price", { exact: true }).waitFor();
  await s.shot({ key: "save-print", targets: [page.getByRole("button", { name: "Print client quote" }), page.getByRole("button", { name: "Print cost audit" })], region: page.getByText("Print the quote", { exact: true }).locator("xpath=ancestor::*[self::fieldset or self::section or self::div][1]") });
}

async function requestTopic(s: Shooter, env: Env): Promise<void> {
  const { page } = s;
  await go(page, env, "requests");
  const first = page.locator("tr.req-row").first();
  await first.waitFor();
  await s.shot({ key: "req-list", targets: [first.locator(".badge").first()], region: [page.getByLabel("Filter requests by name, item, or file"), page.locator("tr.req-row").nth(3)] });
  await first.click();
  await page.getByRole("button", { name: "Finalize" }).waitFor();
  const detail = page.getByRole("heading", { name: /Estimate request from/ });
  const facts = page.getByText("Reference", { exact: true }).locator("xpath=ancestor::*[self::dl or self::table][1]");
  // The reference is random per request; show a fixed one.
  await page.evaluate(() => {
    const w = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
    while (w.nextNode()) {
      const n = w.currentNode as Text;
      if (/^[0-9A-F]{8}$/.test((n.nodeValue ?? "").trim())) n.nodeValue = "7292FE0D";
    }
  });
  await s.shot({ key: "req-detail", targets: [page.locator(".badge").filter({ hasText: /viewed/i }).first()], region: [detail, facts] });
  const actions = [page.getByText(/^Finalize marks this request/), page.getByRole("button", { name: "Delete" })];
  await s.shot({ key: "req-reply", targets: [page.getByRole("link", { name: "Reply by email" }).or(page.getByRole("button", { name: "Reply by email" })).first()], region: actions });
  await s.shot({ key: "req-finalize", targets: [page.getByRole("button", { name: "Finalize" })], region: actions });
  await s.shot({ key: "req-convert", targets: [page.getByRole("button", { name: "Convert to invoice" })], region: actions });
}

async function invoiceTopic(s: Shooter, env: Env): Promise<void> {
  const { page } = s;
  await go(page, env, "quotes");
  const row = page.locator("tbody tr", { hasText: "Cable clip, 48 g" }).first();
  await row.waitFor();
  const convert = row.getByRole("button", { name: "Convert to invoice" });
  await s.shot({ key: "inv-convert", targets: [convert], region: [page.locator("thead").first(), row] });
  await convert.click();
  const create = page.getByRole("button", { name: /Create invoice/ });
  await create.waitFor();
  const clientSel = page.getByLabel("Client for this invoice");
  await s.shot({ key: "inv-client", targets: [clientSel, create], region: [clientSel.locator("xpath=.."), create] });
  await create.click();
  const save = page.getByRole("button", { name: "Save", exact: true });
  await save.waitFor();
  await s.shot({ key: "inv-draft", targets: [page.getByLabel("Issue date"), page.getByLabel("Due date"), page.getByLabel(/^Sales tax/), save], region: [page.getByRole("heading", { name: /^Invoice / }), save] });
  const sendBtns = [page.getByRole("button", { name: "Mark sent" }), page.getByRole("button", { name: "Email to client" }), page.getByRole("button", { name: "Print", exact: true })];
  await s.shot({ key: "inv-send", targets: [sendBtns[0]!, sendBtns[1]!, sendBtns[2]!], region: sendBtns });
  await page.getByLabel("Amount ($)").fill("2.00");
  await page.getByLabel("Method").selectOption({ label: "Bank transfer" });
  await page.getByLabel("Note", { exact: true }).fill("Deposit");
  const record = page.getByRole("button", { name: /^Record/ });
  await s.shot({ key: "inv-payment", targets: [page.getByLabel("Amount ($)"), page.getByLabel("Method"), page.getByLabel("Note", { exact: true }), record], region: [page.getByRole("heading", { name: "Payments" }), record] });
  await record.click();
  const summaryLine = page.getByText(/^Paid \$2\.00 of/);
  await summaryLine.waitFor();
  await s.shot({ key: "inv-paid", targets: [summaryLine], region: [page.getByRole("heading", { name: "Payments" }), summaryLine] });
}

const txt = (page: Page, t: string) => page.getByText(t, { exact: true }).first();
const heading = (page: Page, name: string | RegExp, level?: number) => page.getByRole("heading", { name, level });

async function itemTopic(s: Shooter, env: Env): Promise<void> {
  const { page } = s;
  await go(page, env, "items");
  const cats = page.getByRole("heading", { name: "Categories" }).locator("xpath=ancestor::*[self::section or self::article or contains(concat(' ',normalize-space(@class),' '),' card ')][1]");
  const catName = cats.getByLabel("Name", { exact: true }).last();
  await catName.fill("Desk and organizers");
  const addCat = page.getByRole("button", { name: "Add category" });
  await s.shot({ key: "item-category", targets: [catName, addCat], region: [heading(page, "Categories"), addCat] });
  await addCat.click();
  await page.getByText("Added Desk and organizers.").waitFor();
  await go(page, env, "items");
  const add = page.getByRole("button", { name: "Add shop item" });
  await s.shot({ key: "item-add", targets: [add], region: [add, page.getByLabel("Filter shop items")] });
  await add.click();
  const name = page.getByLabel("Name", { exact: true });
  await name.fill("Hex Desk Tray");
  await page.getByLabel("Price ($)").fill("18.50");
  await page.getByLabel("Category", { exact: true }).selectOption({ label: "Desk and organizers" });
  await page.getByLabel("Description").fill("A stackable hexagonal tray for pens, clips, and small parts.");
  await s.shot({ key: "item-details", targets: [name, page.getByLabel("Price ($)"), page.getByLabel("Category", { exact: true }), page.getByLabel("Description")], region: [heading(page, "Details"), fieldOf(page.getByLabel("Description"))] });
  const stock = page.getByLabel("Stock", { exact: true });
  await stock.selectOption({ label: "In stock (fixed quantity)" });
  await page.getByLabel(/^Stock quantity|^Quantity in stock|^Units in stock/).fill("12").catch(async () => {
    await page.getByRole("spinbutton").last().fill("12");
  });
  await s.shot({ key: "item-stock", targets: [stock, page.getByLabel(/^Quantity in stock/)], region: [txt(page, "Availability"), page.getByLabel(/^Quantity in stock/), page.getByText("Accept orders beyond stock")] });
  const pub = page.getByRole("checkbox", { name: /^Public/ });
  await pub.check();
  await page.getByRole("checkbox", { name: /^Featured/ }).check();
  await s.shot({ key: "item-visibility", targets: [pub, page.getByRole("checkbox", { name: /^Featured/ })], region: [txt(page, "In the shop"), txt(page, "Options")] });
  await page.getByRole("button", { name: "Add group" }).click();
  await page.getByLabel("Group name").fill("Color");
  await page.getByLabel("Group 1 choice 1", { exact: true }).fill("Black");
  await page.getByRole("button", { name: "Add choice" }).click();
  await page.getByLabel("Group 1 choice 2", { exact: true }).fill("Gold");
  await page.getByLabel(/^Price adjustment/).nth(1).fill("2.50");
  const group = page.locator(".row-editor").first();
  await s.shot({ key: "item-options", targets: [page.getByLabel("Group name"), page.getByLabel("Group 1 choice 1", { exact: true }), page.getByLabel(/^Price adjustment/).nth(0), page.getByLabel("Group 1 choice 2", { exact: true }), page.getByLabel(/^Price adjustment/).nth(1)], region: [txt(page, "Options"), page.getByRole("button", { name: "Add group" })] });
  const save = page.getByRole("button", { name: "Save", exact: true });
  await s.shot({ key: "item-save", targets: [save], region: [txt(page, "Photo"), save] });
  await save.click();
  await page.getByText(/saved/i).first().waitFor({ timeout: 10_000 }).catch(() => {});
}

/** A fictional banner drawn in the browser, so the hero upload needs no file on disk. */
async function heroPng(env: Env): Promise<Buffer> {
  const ctx = await env.browser.newContext({ viewport: { width: 1600, height: 420 } });
  const p = await ctx.newPage();
  await p.setContent('<body style="margin:0;height:420px;background:linear-gradient(120deg,#0f766e,#f59e0b 60%,#be185d);display:flex;align-items:center;justify-content:center;font:700 84px sans-serif;color:#fff">Made to order</body>');
  const buf = await p.screenshot({ type: "png" });
  await ctx.close();
  return buf;
}

async function storefrontTopic(s: Shooter, env: Env): Promise<void> {
  const { page } = s;
  await go(page, env, "settings");
  const card = page.locator("#section-storefront");
  await card.scrollIntoViewIfNeeded();
  const gallery = card.getByRole("checkbox", { name: /Item gallery/ });
  const offers = card.getByText("What the shop offers", { exact: true });
  await s.shot({ key: "shop-offers", targets: [gallery, card.getByRole("checkbox", { name: /Custom estimates/ })], region: [offers, card.getByText(/^Turn one off/)] });
  const pause = card.getByRole("checkbox", { name: /Pause the shop/ });
  await pause.check();
  const msg = card.getByLabel("Message on the Closed page");
  await msg.fill("Back on Monday, December 1.");
  await s.shot({ key: "shop-pause", targets: [pause, msg], region: [card.getByText("Pause the shop", { exact: true }).first(), fieldOf(msg)] });
  await pause.uncheck();
  const tabTitle = card.getByLabel("Browser tab title");
  const shopHeading = card.getByLabel("Shop heading");
  await tabTitle.fill("Lantern Print Works | 3D prints");
  await shopHeading.fill("Lantern Print Works shop");
  await s.shot({ key: "shop-wording", targets: [tabTitle, shopHeading], region: [card.getByText("Wording", { exact: true }).first(), fieldOf(shopHeading)] });
  const announce = card.getByLabel("Announcement");
  await announce.fill("**Holiday cutoff:** order by *December 15*. [Shipping details](https://lanternprint.example/shipping)");
  await s.shot({ key: "shop-announcement", targets: [announce], region: [fieldOf(announce)] });
  // Upload the hero: saved immediately, with the required description.
  const alt = card.getByLabel("Image description (alt text)");
  await alt.fill("Colorful banner reading Made to order");
  await s.shot({ key: "shop-hero", targets: [alt, card.getByRole("button", { name: /Upload image/ })], region: [card.getByText("Hero image", { exact: true }).first(), fieldOf(alt)] });
  const png = await heroPng(env);
  await card.locator('input[type="file"]').last().setInputFiles({ name: "banner.png", mimeType: "image/png", buffer: png });
  await card.getByRole("button", { name: "Remove hero image" }).waitFor({ timeout: 15_000 });
  const accent = card.getByLabel("Accent color", { exact: true });
  await accent.fill("#0f746c");
  await s.shot({ key: "shop-accent", targets: [accent], region: [card.getByText("Accent color", { exact: true }).first(), card.getByLabel("Accent color preview"), card.getByText(/^Both variants pass/)] });
  await page.getByRole("button", { name: "Save changes" }).click();
  await page.getByText(/saved/i).first().waitFor({ timeout: 10_000 }).catch(() => {});
  const pub = await env.browser.newContext({ viewport: { width: 1440, height: 1500 }, reducedMotion: "reduce", colorScheme: "light" });
  const shop = await pub.newPage();
  await shop.goto(env.publicUrl + "/");
  const shopHeading2 = shop.getByText("Lantern Print Works shop", { exact: true }).first();
  await shopHeading2.waitFor();
  await s.shot({ page: shop, key: "shop-result", targets: [shopHeading2], region: [shop.getByText(/^Holiday cutoff/), shopHeading2, shop.getByRole("button", { name: "Custom estimate" })] });
  await pub.close();
}

async function settingsCard(page: Page, env: Env, id: string): Promise<Locator> {
  await go(page, env, "settings");
  const card = page.locator(`#${id}`);
  await card.waitFor();
  await card.scrollIntoViewIfNeeded();
  return card;
}

async function spoolTopic(s: Shooter, env: Env): Promise<void> {
  const { page } = s;
  await go(page, env, "filament");
  const edit = page.getByRole("button", { name: "Edit PLA" });
  await s.shot({ key: "spool-materials", targets: [edit], region: [page.getByRole("heading", { name: "Materials" }), page.getByRole("table").first()] });
  const card = await settingsCard(page, env, "section-integrationsUrls");
  const url = card.getByLabel("Spoolman URL");
  await url.fill("http://192.168.1.50:7912");
  await s.shot({ key: "spool-url", targets: [url], region: [card.getByRole("heading", { name: "Spoolman" }).first(), fieldOf(url)] });
  await url.fill(env.spoolmanUrl);
  const test = card.getByRole("button", { name: "Test" }).first();
  await test.click();
  await card.locator(".status-line").first().filter({ hasText: /\w/ }).waitFor({ timeout: 15_000 });
  await s.shot({ key: "spool-test", targets: [test], region: [fieldOf(url), card.locator(".status-line").first()] });
  await page.getByRole("button", { name: "Save changes" }).click();
  await page.getByText(/saved/i).first().waitFor({ timeout: 10_000 }).catch(() => {});
  // The Filament page reads the integration flag when the app loads, so open it in a fresh document.
  await page.goto("about:blank");
  await page.goto(hash(env, "filament"));
  const inv = page.getByRole("heading", { name: "Spoolman inventory" });
  await inv.waitFor();
  await page.getByText("Loading spools…").waitFor({ state: "hidden", timeout: 20_000 });
  await s.shot({ key: "spool-inventory", targets: [inv], region: inv.locator("xpath=ancestor::*[self::section or self::article or contains(concat(' ',normalize-space(@class),' '),' card ')][1]") });
  await blankQuote(page, env);
  const picker = page.getByLabel("Spool (from Spoolman)");
  await picker.waitFor({ timeout: 15_000 });
  await s.shot({ key: "spool-picker", targets: [picker], region: page.getByRole("group", { name: /Material and printer/ }) });
}

async function printerTopic(s: Shooter, env: Env): Promise<void> {
  const { page } = s;
  await go(page, env, "printers");
  await page.getByRole("button", { name: "Add printer" }).click();
  const n = await page.locator('input[aria-label$=" name"]').count();
  await page.getByLabel(`Printer ${n} name`).fill("Corner printer");
  await page.getByLabel(`Printer ${n} watts`).fill("200");
  await page.getByLabel(`Printer ${n} purchase cost`).fill("450");
  await page.getByLabel(`Printer ${n} lifespan hours`).fill("5000");
  const row = page.getByLabel(`Printer ${n} name`).locator("xpath=ancestor::*[self::fieldset or self::section or self::div][.//input[@aria-label='Printer " + n + " watts']][1]");
  await s.shot({ key: "printer-add", targets: ["name", "watts", "purchase cost", "lifespan hours"].map((f) => page.getByLabel(`Printer ${n} ${f}`)), region: row });
  await page.getByRole("button", { name: "Save changes" }).click();
  await page.getByText(/saved/i).first().waitFor({ timeout: 10_000 }).catch(() => {});
  const card = await settingsCard(page, env, "section-integrationsUrls");
  const url = card.getByLabel("Moonraker URL");
  await url.fill(env.moonrakerUrl);
  const test = card.getByRole("button", { name: "Test" }).nth(1);
  await test.click();
  await card.locator(".status-line").nth(1).filter({ hasText: /\w/ }).waitFor({ timeout: 15_000 });
  await s.shot({ key: "printer-moonraker", targets: [url, test], region: [card.getByRole("heading", { name: "Moonraker" }).first(), card.getByRole("group", { name: "Add printer from Moonraker" })] });
  await page.getByRole("button", { name: "Save changes" }).click();
  await page.getByText(/saved/i).first().waitFor({ timeout: 10_000 }).catch(() => {});
  await blankQuote(page, env);
  const recent = page.getByLabel("Recent print (from Moonraker)");
  await recent.waitFor({ timeout: 15_000 });
  await s.shot({ key: "printer-recent", targets: [recent], region: page.getByRole("group", { name: /Model and print time/ }) });
}

async function usersTopic(s: Shooter, env: Env): Promise<void> {
  const { page } = s;
  const card = await settingsCard(page, env, "section-users");
  const multi = card.getByRole("checkbox", { name: /Multi-user mode/ });
  await s.shot({ key: "users-toggle", targets: [multi], region: [card.getByRole("heading", { name: "Users" }), labelOf(multi)] });
  const username = card.getByLabel("Username", { exact: true });
  const password = card.getByLabel("Password", { exact: true });
  await username.fill("jules");
  await password.fill("sample-pass-1");
  await s.shot({ key: "users-add", targets: [username, password], region: [card.getByText("Add employee", { exact: true }).first(), fieldOf(password)] });
  const tabs = card.getByText("Tabs this employee can open");
  const newTabs = card.getByRole("group", { name: /Tabs this employee can open/ });
  await newTabs.getByLabel("New quote").check();
  await newTabs.getByLabel("Quotes").check();
  await s.shot({ key: "users-tabs", targets: [newTabs.getByLabel("New quote"), newTabs.getByLabel("Quotes")], region: [tabs, newTabs] });
  void tabs;
  await card.getByRole("button", { name: "Add employee" }).last().click();
  await card.getByText("jules").first().waitFor({ timeout: 10_000 });
  const table = card.getByRole("table").first();
  const jules = table.locator("tr", { hasText: "jules" });
  await s.shot({ key: "users-table", targets: [jules.getByRole("button", { name: /Reset password/ })], region: [table.locator("thead"), jules] });
}

async function dashboardTopic(s: Shooter, env: Env): Promise<void> {
  const { page } = s;
  // A narrower window keeps the five tiles within the 1200 px crop cap.
  await page.setViewportSize({ width: 1200, height: 1500 });
  await go(page, env, "dashboard");
  const tile = page.getByText("New requests", { exact: true }).first();
  await tile.waitFor();
  const row = tile.locator("xpath=ancestor::*[.//*[normalize-space()='Quotes awaiting reply']][1]");
  await s.shot({ key: "dash-tiles", targets: ["New requests", "Overdue invoices", "Draft invoices", "Needs print time", "Quotes awaiting reply"].map((t) => page.getByRole("button", { name: new RegExp(t) })), region: row });
  const todo = page.getByRole("heading", { name: "Needs attention" }).locator("xpath=ancestor::*[self::section or self::article or contains(concat(' ',normalize-space(@class),' '),' card ')][1]");
  await s.shot({ key: "dash-todo", targets: [page.getByRole("heading", { name: "Needs attention" })], region: todo });
  const month = page.getByRole("heading", { name: "This month" });
  const monthTiles = page.getByText("Average margin", { exact: true }).first().locator("xpath=ancestor::*[.//*[normalize-space()='Outstanding']][1]");
  await s.shot({ key: "dash-month", targets: ["Revenue", "Outstanding", "Average margin"].map((t) => page.getByText(t, { exact: true }).first().locator("xpath=..")), region: [month, monthTiles] });
  const chart = page.getByRole("heading", { name: "Revenue by month" }).locator("xpath=ancestor::*[self::section or self::article or contains(concat(' ',normalize-space(@class),' '),' card ')][1]");
  await s.shot({ key: "dash-chart", targets: [page.getByRole("heading", { name: "Revenue by month" })], region: chart });
  const top = page.getByRole("heading", { name: "Top items" }).locator("xpath=ancestor::*[self::section or self::article or contains(concat(' ',normalize-space(@class),' '),' card ')][1]");
  await s.shot({ key: "dash-top", targets: [page.getByRole("heading", { name: "Top items" })], region: top });
}

async function dashboardTopicEnd(s: Shooter): Promise<void> {
  await s.page.setViewportSize({ width: 1440, height: 1500 });
}

async function electricityTopic(s: Shooter, env: Env): Promise<void> {
  const { page } = s;
  await go(page, env, "electricity");
  const calc = page.getByRole("heading", { name: "Electricity calculator" }).locator("xpath=ancestor::*[self::section or self::article or contains(concat(' ',normalize-space(@class),' '),' card ')][1]");
  await page.getByLabel("Printer power (W)").fill("120");
  await page.getByLabel("Print time (h)").fill("2.5");
  await s.shot({ key: "power-calc", targets: [page.getByLabel("Printer power (W)"), page.getByLabel("Print time (h)")], region: calc });
  const mode = page.getByLabel("Mode", { exact: true });
  await mode.selectOption({ label: "Time-of-use schedule" });
  await s.shot({ key: "power-mode", targets: [mode], region: [page.getByRole("heading", { name: "Saved energy settings" }), fieldOf(mode)] });
  const example = page.getByRole("button", { name: /Load SCE TOU-D-4-9PM example/ });
  await s.shot({ key: "power-example", targets: [example], region: [example, page.getByText(/^The example shows the structure/)] });
  await example.click();
  const start = page.getByLabel(/^Default print start/);
  await start.fill("22:00");
  await s.shot({ key: "power-start", targets: [start], region: [fieldOf(start), page.getByText(/^Up to 100 dates/)] });
  await page.getByRole("button", { name: "Save changes" }).click();
  await page.getByText(/saved/i).first().waitFor({ timeout: 10_000 }).catch(() => {});
  await blankQuote(page, env);
  await fillPart(page, "48", "2", "30");
  const ps = page.getByLabel(/^Print start/).first();
  await ps.waitFor({ timeout: 15_000 });
  await s.shot({ key: "power-quote", targets: [ps], region: page.getByRole("group", { name: /Material and printer/ }) });
}
