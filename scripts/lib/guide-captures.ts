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

/** The ui/Field wrapper (label, control, hint) around a control, so the ring takes in the label. */
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
  await electricityTopic(s, env);
  void page;
}

async function quoteTopics(s: Shooter, env: Env): Promise<void> {
  const { page } = s;
  await blankQuote(page, env);
  const group = (name: RegExp) => page.getByRole("group", { name });
  const sec1 = group(/Model and print time/);
  const sec2 = group(/Material and printer/);
  await s.shot({ key: "quote-overview", targets: [{ loc: sec1.locator("legend"), n: 1 }], region: [page.getByRole("heading", { name: "New quote", level: 1 }), sec1] });
  await page.getByLabel("Model weight (g)").fill("48");
  await page.getByLabel("Print time (hours)").fill("2");
  await page.getByLabel("Print time (minutes)").fill("30");
  await waitForCost(page);
  await s.shot({ key: "quote-weight", targets: [{ loc: fieldOf(page.getByLabel("Model weight (g)")), n: 2 }], region: sec1 });
  await s.shot({ key: "quote-time", targets: [{ loc: page.getByRole("group", { name: "Print time", exact: true }), n: 3 }], region: sec1 });
  await waitForCost(page);
  await s.shot({ key: "quote-material", targets: [{ loc: fieldOf(page.getByLabel("Material", { exact: true })), n: 4 }], region: sec2 });
  const summary = page.locator('[role="group"][aria-label="Quote summary"]');
  await s.shot({ key: "quote-cost", targets: [{ loc: page.locator(".summary-stats .stat.cost"), n: 5 }], region: summary });
  await page.getByRole("button", { name: "Set a client price" }).click();
  await summary.getByText("Client price", { exact: true }).waitFor();
  await page.waitForTimeout(600);
  await s.shot({ key: "quote-client-price", targets: [{ loc: page.locator(".summary-stats"), n: 6 }], region: summary });
}

async function setupTopic(s: Shooter, env: Env): Promise<void> {
  const { page } = s;
  await go(page, env, "settings");
  const card = page.locator("#section-quote");
  await s.shot({ key: "setup-business-card", targets: [{ loc: card.getByRole("heading", { name: "Business identity" }), n: 1 }], region: [card.getByRole("heading", { name: "Business identity" }), card.getByLabel("Business name")] });
  await card.getByLabel("Business name").fill("Lantern Print Works");
  await card.getByLabel("Email", { exact: true }).fill("hello@lantern.example");
  await s.shot({ key: "setup-business-fields", targets: [{ loc: fieldOf(card.getByLabel("Business name")), n: 2 }, { loc: fieldOf(card.getByLabel("Email", { exact: true })), n: 2 }], region: [card.getByRole("heading", { name: "Business identity" }), fieldOf(card.getByLabel("Order reference prefix"))] });
  await s.shot({ key: "setup-save-bar", targets: [{ loc: page.locator(".save-actions"), n: 3 }] });
  await page.getByRole("button", { name: "Save changes" }).click();
  await page.getByText(/saved/i).first().waitFor({ timeout: 10_000 }).catch(() => {});
  await go(page, env, "filament");
  await page.getByRole("button", { name: "Edit PLA" }).click();
  await page.getByLabel("Material 1 price per kg").fill("21.5");
  await s.shot({ key: "setup-material-price", targets: [{ loc: page.getByLabel("Material 1 price per kg"), n: 4 }], region: page.getByRole("table").first() });
  await page.getByRole("button", { name: "Done editing PLA" }).click();
  await page.getByRole("button", { name: "Save changes" }).click();
  await go(page, env, "printers");
  await page.getByLabel("Printer 1 name").fill("Workshop printer");
  await page.getByLabel("Printer 1 watts").fill("120");
  const row = page.getByLabel("Printer 1 name").locator("xpath=ancestor::*[self::fieldset or self::section or self::div][.//input[@aria-label='Printer 1 watts']][1]");
  await s.shot({ key: "setup-printer", targets: [{ loc: fieldOf(page.getByLabel("Printer 1 name")), n: 5 }, { loc: fieldOf(page.getByLabel("Printer 1 watts")), n: 5 }], region: row });
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
  await s.shot({ key: "multi-add-part", targets: [{ loc: addPart, n: 2 }], region: [page.getByRole("heading", { name: "New quote", level: 1 }), addPart, page.getByText(/^Quoting several printed parts/)] });
  await addPart.click();
  const tabs = page.getByRole("tablist");
  await tabs.waitFor();
  await page.getByLabel("Part name").fill("Lid");
  await fillPart(page, "22", "1", "15");
  await waitForCost(page);
  await s.shot({ key: "multi-part-fields", targets: [{ loc: fieldOf(page.getByLabel("Part name")), n: 3 }, { loc: fieldOf(page.getByLabel("Model weight (g)")), n: 3 }], region: [tabs, sec1] });
  await page.getByRole("tab", { name: /Part 1/ }).click();
  await s.shot({ key: "multi-tabs", targets: [{ loc: tabs, n: 4 }], region: [tabs, sec1] });
  const summary = page.locator('[role="group"][aria-label="Quote summary"]');
  await s.shot({ key: "multi-summary", targets: [{ loc: summary.locator(".summary-stats"), n: 5 }], region: summary });
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
  await s.shot({ key: "save-client", targets: [{ loc: fieldOf(client), n: 1 }], region: saveBox });
  await label.fill("Cable clip, 48 g");
  await s.shot({ key: "save-label", targets: [{ loc: fieldOf(label), n: 2 }], region: saveBox });
  await s.shot({ key: "save-button", targets: [{ loc: page.getByRole("button", { name: "Save quote" }), n: 3 }], region: saveBox });
  await page.getByRole("button", { name: "Save quote" }).click();
  await page.getByRole("button", { name: "Update quote" }).waitFor();
  await page.getByText('"Cable clip, 48 g" saved.').waitFor();
  await s.shot({ key: "save-update", targets: [{ loc: page.getByRole("button", { name: "Update quote" }), n: 4 }, { loc: page.getByRole("button", { name: "Save as new" }), n: 4 }], region: [summary.getByRole("heading", { name: "Save this quote" }), page.getByText('"Cable clip, 48 g" saved.')] });
  const recent = page.getByRole("heading", { name: "Saved quotes" });
  await s.shot({ key: "save-recent", targets: [{ loc: page.getByRole("button", { name: /^Open/ }).first(), n: 5 }], region: [recent, page.getByRole("button", { name: /^Open/ }).nth(3)] });
  await go(page, env, "quotes");
  const row = page.locator("tbody tr", { hasText: "Cable clip, 48 g" }).first();
  await row.waitFor();
  await s.shot({ key: "save-quotes-row", targets: [{ loc: row, n: 6 }], region: [page.locator("thead").first(), row] });
  await blankQuote(page, env);
  await fillPart(page, "48", "2", "30");
  await waitForCost(page);
  await page.getByRole("button", { name: "Set a client price" }).click();
  await page.locator('[role="group"][aria-label="Quote summary"]').getByText("Client price", { exact: true }).waitFor();
  await s.shot({ key: "save-print", targets: [{ loc: page.getByRole("button", { name: "Print client quote" }), n: 7 }, { loc: page.getByRole("button", { name: "Print cost audit" }), n: 7 }], region: page.getByText("Print the quote", { exact: true }).locator("xpath=ancestor::*[self::fieldset or self::section or self::div][1]") });
}

async function requestTopic(s: Shooter, env: Env): Promise<void> {
  const { page } = s;
  await go(page, env, "requests");
  const first = page.locator("tr.req-row").first();
  await first.waitFor();
  await s.shot({ key: "req-list", targets: [{ loc: first, n: 1 }], region: [page.getByLabel("Filter requests by name, item, or file"), page.locator("tr.req-row").nth(3)] });
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
  await s.shot({ key: "req-detail", targets: [{ loc: facts, n: 2 }], region: [detail, facts] });
  const actions = [page.getByText(/^Finalize marks this request/), page.getByRole("button", { name: "Delete" })];
  await s.shot({ key: "req-reply", targets: [{ loc: page.getByText("Reply by email", { exact: true }), n: 3 }], region: actions });
  await s.shot({ key: "req-finalize", targets: [{ loc: page.getByRole("button", { name: "Finalize" }), n: 4 }], region: actions });
  await s.shot({ key: "req-convert", targets: [{ loc: page.getByRole("button", { name: "Convert to invoice" }), n: 5 }], region: actions });
}

async function invoiceTopic(s: Shooter, env: Env): Promise<void> {
  const { page } = s;
  await go(page, env, "quotes");
  const row = page.locator("tbody tr", { hasText: "Cable clip, 48 g" }).first();
  await row.waitFor();
  const convert = row.getByRole("button", { name: "Convert to invoice" });
  await s.shot({ key: "inv-convert", targets: [{ loc: convert, n: 1 }], region: [page.locator("thead").first(), row] });
  await convert.click();
  const create = page.getByRole("button", { name: /Create invoice/ });
  await create.waitFor();
  const clientSel = page.getByLabel("Client for this invoice");
  await s.shot({ key: "inv-client", targets: [{ loc: clientSel, n: 2 }, { loc: create, n: 2 }], region: [clientSel.locator("xpath=.."), create] });
  await create.click();
  const save = page.getByRole("button", { name: "Save", exact: true });
  await save.waitFor();
  // Dates default to the day you run the script; show fixed ones.
  await page.getByLabel("Issue date").fill("2026-11-04");
  await page.getByLabel("Due date").fill("2026-12-04");
  await s.shot({ key: "inv-draft", targets: [{ loc: fieldOf(page.getByLabel("Issue date")), n: 3 }, { loc: fieldOf(page.getByLabel("Due date")), n: 3 }, { loc: save, n: 3 }], region: [page.getByRole("heading", { name: /^Invoice / }), save] });
  await save.click();
  await page.getByText(/saved/i).first().waitFor({ timeout: 10_000 }).catch(() => {});
  const sendBtns = [page.getByRole("button", { name: "Mark sent" }), page.getByRole("button", { name: "Email to client" }), page.getByRole("button", { name: "Print", exact: true })];
  await s.shot({ key: "inv-send", targets: sendBtns.map((loc) => ({ loc, n: 4 })), region: sendBtns });
  await page.getByLabel("Date", { exact: true }).fill("2026-11-04");
  await page.getByLabel("Amount ($)").fill("2.00");
  await page.getByLabel("Method").selectOption({ label: "Bank transfer" });
  await page.getByLabel("Note", { exact: true }).fill("Deposit");
  const record = page.getByRole("button", { name: /^Record/ });
  await s.shot({ key: "inv-payment", targets: [{ loc: fieldOf(page.getByLabel("Amount ($)")), n: 5 }, { loc: fieldOf(page.getByLabel("Method")), n: 5 }, { loc: fieldOf(page.getByLabel("Note", { exact: true })), n: 5 }, { loc: record, n: 5 }], region: [page.getByRole("heading", { name: "Payments" }), record] });
  await record.click();
  const summaryLine = page.getByText(/^Paid \$2\.00 of/);
  await summaryLine.waitFor();
  await s.shot({ key: "inv-paid", targets: [{ loc: summaryLine, n: 6 }], region: [page.getByRole("heading", { name: "Payments" }), summaryLine] });
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
  await s.shot({ key: "item-category", targets: [{ loc: fieldOf(catName), n: 1 }, { loc: addCat, n: 1 }], region: [heading(page, "Categories"), addCat] });
  await addCat.click();
  await page.getByText("Added Desk and organizers.").waitFor();
  await go(page, env, "items");
  const add = page.getByRole("button", { name: "Add shop item" });
  await s.shot({ key: "item-add", targets: [{ loc: add, n: 2 }], region: [heading(page, "Shop items", 1), page.getByLabel("Filter shop items")] });
  await add.click();
  const name = page.getByLabel("Name", { exact: true });
  await name.fill("Hex Desk Tray");
  await page.getByLabel("Price ($)").fill("18.50");
  await page.getByLabel("Category", { exact: true }).selectOption({ label: "Desk and organizers" });
  await page.getByLabel("Description").fill("A stackable hexagonal tray for pens, clips, and small parts.");
  await s.shot({ key: "item-details", targets: [{ loc: fieldOf(name), n: 3 }, { loc: fieldOf(page.getByLabel("Price ($)")), n: 3 }, { loc: fieldOf(page.getByLabel("Category", { exact: true })), n: 3 }], region: [heading(page, "Details"), fieldOf(page.getByLabel("Description"))] });
  const stock = page.getByLabel("Stock", { exact: true });
  await stock.selectOption({ label: "In stock (fixed quantity)" });
  await page.getByLabel(/^Stock quantity|^Quantity in stock|^Units in stock/).fill("12").catch(async () => {
    await page.getByRole("spinbutton").last().fill("12");
  });
  await s.shot({ key: "item-stock", targets: [{ loc: fieldOf(stock), n: 4 }], region: [txt(page, "Availability"), page.getByLabel(/^Quantity in stock/), page.getByText("Accept orders beyond stock")] });
  const pub = page.getByRole("checkbox", { name: /^Public/ });
  await pub.check();
  await page.getByRole("checkbox", { name: /^Featured/ }).check();
  await s.shot({ key: "item-visibility", targets: [{ loc: labelOf(pub), n: 5 }, { loc: labelOf(page.getByRole("checkbox", { name: /^Featured/ })), n: 5 }], region: [txt(page, "In the shop"), txt(page, "Options")] });
  await page.getByRole("button", { name: "Add group" }).click();
  await page.getByLabel("Group name").fill("Color");
  await page.getByLabel("Group 1 choice 1", { exact: true }).fill("Black");
  await page.getByRole("button", { name: "Add choice" }).click();
  await page.getByLabel("Group 1 choice 2", { exact: true }).fill("Gold");
  await page.getByLabel(/^Price adjustment/).nth(1).fill("2.50");
  const group = page.locator(".row-editor").first();
  await s.shot({ key: "item-options", targets: [{ loc: group, n: 6 }], region: [txt(page, "Options"), page.getByRole("button", { name: "Add group" })] });
  const save = page.getByRole("button", { name: "Save", exact: true });
  await s.shot({ key: "item-save", targets: [{ loc: save, n: 7 }], region: [txt(page, "Photo"), save] });
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
  const gallery = labelOf(card.getByRole("checkbox", { name: /Item gallery/ }));
  const offers = card.getByText("What the shop offers", { exact: true });
  await s.shot({ key: "shop-offers", targets: [{ loc: gallery, n: 1 }, { loc: labelOf(card.getByRole("checkbox", { name: /Custom estimates/ })), n: 1 }], region: [offers, card.getByText(/^Turn one off/)] });
  const pause = card.getByRole("checkbox", { name: /Pause the shop/ });
  await pause.check();
  const msg = card.getByLabel("Message on the Closed page");
  await msg.fill("Back on Monday, December 1.");
  await s.shot({ key: "shop-pause", targets: [{ loc: labelOf(pause), n: 2 }, { loc: fieldOf(msg), n: 2 }], region: [card.getByText("Pause the shop", { exact: true }).first(), fieldOf(msg)] });
  await pause.uncheck();
  const tabTitle = card.getByLabel("Browser tab title");
  const shopHeading = card.getByLabel("Shop heading");
  await tabTitle.fill("Lantern Print Works | 3D prints");
  await shopHeading.fill("Lantern Print Works shop");
  await s.shot({ key: "shop-wording", targets: [{ loc: fieldOf(tabTitle), n: 3 }, { loc: fieldOf(shopHeading), n: 3 }], region: [card.getByText("Wording", { exact: true }).first(), fieldOf(shopHeading)] });
  const announce = card.getByLabel("Announcement");
  await announce.fill("**Holiday cutoff:** order by *December 15*. [Shipping details](https://lanternprint.example/shipping)");
  await s.shot({ key: "shop-announcement", targets: [{ loc: fieldOf(announce), n: 4 }], region: [fieldOf(announce)] });
  // Upload the hero: saved immediately, with the required description.
  const alt = card.getByLabel("Image description (alt text)");
  await alt.fill("Colorful banner reading Made to order");
  const png = await heroPng(env);
  await card.locator('input[type="file"]').last().setInputFiles({ name: "banner.png", mimeType: "image/png", buffer: png });
  await card.getByRole("button", { name: "Remove hero image" }).waitFor({ timeout: 15_000 });
  await s.shot({ key: "shop-hero", targets: [{ loc: fieldOf(alt), n: 5 }], region: [card.getByText("Hero image", { exact: true }).first(), card.getByRole("button", { name: "Remove hero image" })] });
  const accent = card.getByLabel("Accent color", { exact: true });
  await accent.fill("#0f746c");
  await s.shot({ key: "shop-accent", targets: [{ loc: fieldOf(accent), n: 6 }], region: [card.getByText("Accent color", { exact: true }).first(), card.getByLabel("Accent color preview")] });
  await page.getByRole("button", { name: "Save changes" }).click();
  await page.getByText(/saved/i).first().waitFor({ timeout: 10_000 }).catch(() => {});
  const pub = await env.browser.newContext({ viewport: { width: 1440, height: 1500 }, reducedMotion: "reduce", colorScheme: "light" });
  const shop = await pub.newPage();
  await shop.goto(env.publicUrl + "/");
  const shopHeading2 = shop.getByText("Lantern Print Works shop", { exact: true }).first();
  await shopHeading2.waitFor();
  await s.shot({ page: shop, key: "shop-result", targets: [{ loc: shopHeading2, n: 7 }], region: [shop.getByText(/^Holiday cutoff/), shopHeading2, shop.getByRole("button", { name: "Custom estimate" })] });
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
  await s.shot({ key: "spool-materials", targets: [{ loc: edit, n: 1 }], region: [page.getByRole("heading", { name: "Materials" }), page.getByRole("table").first()] });
  const card = await settingsCard(page, env, "section-integrationsUrls");
  const url = card.getByLabel("Spoolman URL");
  await url.fill("http://192.168.1.50:7912");
  await s.shot({ key: "spool-url", targets: [{ loc: fieldOf(url), n: 2 }], region: [card.getByRole("heading", { name: "Spoolman" }).first(), fieldOf(url)] });
  await url.fill(env.spoolmanUrl);
  const test = card.getByRole("button", { name: "Test" }).first();
  await test.click();
  await card.locator(".status-line").first().filter({ hasText: /\w/ }).waitFor({ timeout: 15_000 });
  await s.shot({ key: "spool-test", targets: [{ loc: test, n: 3 }, { loc: card.locator(".status-line").first(), n: 3 }], region: [fieldOf(url), card.locator(".status-line").first()] });
  await page.getByRole("button", { name: "Save changes" }).click();
  await page.getByText(/saved/i).first().waitFor({ timeout: 10_000 }).catch(() => {});
  // The Filament page reads the integration flag when the app loads, so open it in a fresh document.
  await page.goto("about:blank");
  await page.goto(hash(env, "filament"));
  const inv = page.getByRole("heading", { name: "Spoolman inventory" });
  await inv.waitFor();
  await page.getByText("Loading spools…").waitFor({ state: "hidden", timeout: 20_000 });
  await s.shot({ key: "spool-inventory", targets: [{ loc: inv.locator("xpath=ancestor::*[self::section or self::article or contains(concat(' ',normalize-space(@class),' '),' card ')][1]"), n: 5 }], region: inv.locator("xpath=ancestor::*[self::section or self::article or contains(concat(' ',normalize-space(@class),' '),' card ')][1]") });
  await blankQuote(page, env);
  const picker = page.getByLabel("Spool (from Spoolman)");
  await picker.waitFor({ timeout: 15_000 });
  await s.shot({ key: "spool-picker", targets: [{ loc: fieldOf(picker), n: 6 }], region: page.getByRole("group", { name: /Material and printer/ }) });
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
  await s.shot({ key: "printer-add", targets: ["name", "watts", "purchase cost", "lifespan hours"].map((f) => ({ loc: fieldOf(page.getByLabel(`Printer ${n} ${f}`)), n: 1 })), region: row });
  await page.getByRole("button", { name: "Save changes" }).click();
  await page.getByText(/saved/i).first().waitFor({ timeout: 10_000 }).catch(() => {});
  const card = await settingsCard(page, env, "section-integrationsUrls");
  const url = card.getByLabel("Moonraker URL");
  await url.fill(env.moonrakerUrl);
  const test = card.getByRole("button", { name: "Test" }).nth(1);
  await test.click();
  await card.locator(".status-line").nth(1).filter({ hasText: /\w/ }).waitFor({ timeout: 15_000 });
  await s.shot({ key: "printer-moonraker", targets: [{ loc: fieldOf(url), n: 3 }, { loc: test, n: 3 }], region: [card.getByRole("heading", { name: "Moonraker" }).first(), card.getByRole("group", { name: "Add printer from Moonraker" })] });
  await page.getByRole("button", { name: "Save changes" }).click();
  await page.getByText(/saved/i).first().waitFor({ timeout: 10_000 }).catch(() => {});
  await blankQuote(page, env);
  const recent = page.getByLabel("Recent print (from Moonraker)");
  await recent.waitFor({ timeout: 15_000 });
  await s.shot({ key: "printer-recent", targets: [{ loc: fieldOf(recent), n: 5 }], region: page.getByRole("group", { name: /Model and print time/ }) });
}

async function usersTopic(s: Shooter, env: Env): Promise<void> {
  const { page } = s;
  const card = await settingsCard(page, env, "section-users");
  const multi = card.getByRole("checkbox", { name: /Multi-user mode/ });
  await s.shot({ key: "users-toggle", targets: [{ loc: labelOf(multi), n: 1 }], region: [card.getByRole("heading", { name: "Users" }), labelOf(multi)] });
  const username = card.getByLabel("Username", { exact: true });
  const password = card.getByLabel("Password", { exact: true });
  await username.fill("jules");
  await password.fill("sample-pass-1");
  await s.shot({ key: "users-add", targets: [{ loc: fieldOf(username), n: 2 }, { loc: fieldOf(password), n: 2 }], region: [card.getByText("Add employee", { exact: true }).first(), fieldOf(password)] });
  const tabs = card.getByText("Tabs this employee can open");
  const newTabs = card.getByRole("group", { name: /Tabs this employee can open/ });
  await newTabs.getByLabel("New quote").check();
  await newTabs.getByLabel("Quotes").check();
  await s.shot({ key: "users-tabs", targets: [{ loc: newTabs, n: 3 }], region: [tabs, newTabs] });
  void tabs;
  await card.getByRole("button", { name: "Add employee" }).last().click();
  await card.getByText("jules").first().waitFor({ timeout: 10_000 });
  const table = card.getByRole("table").first();
  const jules = table.locator("tr", { hasText: "jules" });
  await s.shot({ key: "users-table", targets: [{ loc: jules, n: 4 }], region: [table.locator("thead"), jules] });
}

async function dashboardTopic(s: Shooter, env: Env): Promise<void> {
  const { page } = s;
  await go(page, env, "dashboard");
  const tile = page.getByText("New requests", { exact: true }).first();
  await tile.waitFor();
  const row = tile.locator("xpath=ancestor::*[.//*[normalize-space()='Quotes awaiting reply']][1]");
  await s.shot({ key: "dash-tiles", targets: [{ loc: row, n: 1 }], region: row });
  const todo = page.getByRole("heading", { name: "Needs attention" }).locator("xpath=ancestor::*[self::section or self::article or contains(concat(' ',normalize-space(@class),' '),' card ')][1]");
  await s.shot({ key: "dash-todo", targets: [{ loc: todo, n: 2 }], region: [page.getByRole("heading", { name: "Needs attention" }), page.getByText(/^Quote awaiting reply/).first()] });
  const month = page.getByRole("heading", { name: "This month" });
  const monthTiles = page.getByText("Average margin", { exact: true }).first().locator("xpath=ancestor::*[.//*[normalize-space()='Outstanding']][1]");
  await s.shot({ key: "dash-month", targets: [{ loc: monthTiles, n: 3 }], region: [month, monthTiles] });
  const chart = page.getByRole("heading", { name: "Revenue by month" }).locator("xpath=ancestor::*[self::section or self::article or contains(concat(' ',normalize-space(@class),' '),' card ')][1]");
  await s.shot({ key: "dash-chart", targets: [{ loc: chart, n: 4 }], region: chart });
  const top = page.getByRole("heading", { name: "Top items" }).locator("xpath=ancestor::*[self::section or self::article or contains(concat(' ',normalize-space(@class),' '),' card ')][1]");
  await s.shot({ key: "dash-top", targets: [{ loc: top, n: 5 }], region: top });
}

async function electricityTopic(s: Shooter, env: Env): Promise<void> {
  const { page } = s;
  await go(page, env, "electricity");
  const calc = page.getByRole("heading", { name: "Electricity calculator" }).locator("xpath=ancestor::*[self::section or self::article or contains(concat(' ',normalize-space(@class),' '),' card ')][1]");
  await page.getByLabel("Printer power (W)").fill("120");
  await page.getByLabel("Print time (h)").fill("2.5");
  await s.shot({ key: "power-calc", targets: [{ loc: fieldOf(page.getByLabel("Printer power (W)")), n: 1 }, { loc: fieldOf(page.getByLabel("Print time (h)")), n: 1 }], region: calc });
  const mode = page.getByLabel("Mode", { exact: true });
  await mode.selectOption({ label: "Time-of-use schedule" });
  await s.shot({ key: "power-mode", targets: [{ loc: fieldOf(mode), n: 2 }], region: [page.getByRole("heading", { name: "Saved energy settings" }), fieldOf(mode)] });
  const example = page.getByRole("button", { name: /Load SCE TOU-D-4-9PM example/ });
  await s.shot({ key: "power-example", targets: [{ loc: example, n: 3 }], region: [example, page.getByText(/^The example shows the structure/)] });
  await example.click();
  const start = page.getByLabel(/^Default print start/);
  await start.fill("22:00");
  await s.shot({ key: "power-start", targets: [{ loc: fieldOf(start), n: 4 }], region: [fieldOf(start), page.getByText(/^Up to 100 dates/)] });
  await page.getByRole("button", { name: "Save changes" }).click();
  await page.getByText(/saved/i).first().waitFor({ timeout: 10_000 }).catch(() => {});
  await blankQuote(page, env);
  await fillPart(page, "48", "2", "30");
  const ps = page.getByLabel(/^Print start/).first();
  await ps.waitFor({ timeout: 15_000 });
  await s.shot({ key: "power-quote", targets: [{ loc: fieldOf(ps), n: 5 }], region: page.getByRole("group", { name: /Material and printer/ }) });
}
