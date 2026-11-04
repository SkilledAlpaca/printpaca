import { test, expect } from "@playwright/test";
import type { APIRequestContext, Page } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { deflateSync, crc32 } from "node:zlib";
import { gotoSettings } from "./helpers";

// v1.2 Settings > Storefront: the card that replaced the temporary storefront inputs under Requests. These specs
// drive the card in the browser and check the result through the public API. All specs share one server and
// database, so every test puts the shop back the way it found it.

const TAGS = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"];

type Json = Record<string, any>;

/** A valid 1x1 PNG of one color. */
function tinyPng(r: number, g: number, b: number): Buffer {
  const chunk = (type: string, data: Buffer): Buffer => {
    const len = Buffer.alloc(4);
    len.writeUInt32BE(data.length);
    const body = Buffer.concat([Buffer.from(type, "ascii"), data]);
    const crc = Buffer.alloc(4);
    crc.writeUInt32BE(crc32(body));
    return Buffer.concat([len, body, crc]);
  };
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(1, 0);
  ihdr.writeUInt32BE(1, 4);
  ihdr[8] = 8;
  ihdr[9] = 2;
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk("IHDR", ihdr),
    chunk("IDAT", deflateSync(Buffer.from([0, r, g, b]))),
    chunk("IEND", Buffer.alloc(0)),
  ]);
}

let original: Json;

async function getConfig(request: APIRequestContext): Promise<Json> {
  const res = await request.get("/admin/api/settings");
  expect(res.ok()).toBeTruthy();
  return (await res.json()).config as Json;
}

async function putConfig(request: APIRequestContext, config: Json): Promise<void> {
  const res = await request.put("/admin/api/settings", { data: { config } });
  expect(res.ok(), await res.text()).toBeTruthy();
}

async function publicConfig(request: APIRequestContext): Promise<Json> {
  return (await (await request.get("/api/public/config")).json()) as Json;
}

test.describe.configure({ mode: "serial" });

test.beforeAll(async ({ request }) => {
  original = await getConfig(request);
});

// Put the shop, intro text, site block, and hero image back after every test.
test.afterEach(async ({ request }) => {
  await request.delete("/admin/api/shop/hero-image");
  const config = await getConfig(request);
  config.shop = structuredClone(original.shop);
  config.shop.heroImage = { file: "", alt: "" };
  config.publicQuotes = structuredClone(original.publicQuotes);
  config.site = structuredClone(original.site);
  await putConfig(request, config);
});

const storefront = (page: Page) => page.locator("#section-storefront");

test("the Storefront card lives in Settings; Requests keeps only the printer and preset", async ({ page }) => {
  await gotoSettings(page);
  const card = storefront(page);
  await expect(card.getByRole("heading", { name: "Storefront", level: 2 })).toBeVisible();
  for (const label of ["Item gallery", "Custom estimates", "Pause the shop (vacation mode)", "Shop heading", "Intro text", "Announcement", "Shipping note", "Turnaround note", "Accent color", "Largest order quantity", "Show stock counts", "Show \"from\" prices", "Image description (alt text)"]) {
    await expect(card.getByLabel(label, { exact: false }).first()).toBeVisible();
  }
  // The section rail lists it.
  await expect(page.getByRole("navigation", { name: "Settings sections" }).getByRole("button", { name: "Storefront" })).toBeVisible();

  await page.goto("/admin/#/requests");
  const requestsCard = page.locator("#section-public-quotes");
  await expect(requestsCard.getByRole("heading", { name: "Public estimates" })).toBeVisible();
  await expect(requestsCard.getByLabel("Printer")).toBeVisible();
  await expect(requestsCard.getByLabel("Preset")).toBeVisible();
  await expect(page.getByLabel("Shop heading")).toHaveCount(0);
  await expect(page.getByLabel("Accent color")).toHaveCount(0);
  await expect(page.getByLabel("Intro text")).toHaveCount(0);
});

test("wording and switches save with Ctrl+S and reach the public config", async ({ page, request }) => {
  await gotoSettings(page);
  const card = storefront(page);
  await card.getByLabel("Shop heading").fill("Alpaca Prints Shop");
  await card.getByLabel("Intro text").fill("Hand-finished prints.");
  await card.getByLabel("Announcement").fill("Shipping cutoff Dec 15");
  await card.getByLabel("Turnaround note").fill("About a week.");
  await card.getByLabel("Item order").selectOption("name");
  await card.getByLabel("Show stock counts").uncheck();
  await expect(page.locator(".save-bar")).toBeVisible();
  await page.keyboard.press("Control+s");
  await expect(page.getByText("Settings saved.")).toBeVisible();
  await expect.poll(async () => (await publicConfig(request)).shop.heading).toBe("Alpaca Prints Shop");
  const pub = await publicConfig(request);
  expect(pub.shop).toMatchObject({ announcement: "Shipping cutoff Dec 15", turnaroundNote: "About a week.", itemSort: "name", showStock: false });
  expect(pub.introText).toBe("Hand-finished prints.");
});

test("turning off both the gallery and custom estimates is blocked with the reason", async ({ page, request }) => {
  await gotoSettings(page);
  const card = storefront(page);
  await card.getByLabel("Item gallery").uncheck();
  await card.getByLabel("Custom estimates").uncheck();
  await expect(card.getByRole("alert").filter({ hasText: "can't both be off" })).toBeVisible();
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByRole("alert").filter({ hasText: "shop.gallery" }).first()).toBeVisible();
  expect((await publicConfig(request)).shop.gallery).toBe(true);
});

test("pausing the shop shows a live Closed page preview and closes the public shop", async ({ page, request }) => {
  await gotoSettings(page);
  const card = storefront(page);
  const preview = card.getByRole("region", { name: "Closed page preview" });
  await expect(preview).toContainText("The shop is open. This is what customers will see while it is paused.");
  await expect(preview.getByRole("heading", { name: "We're closed right now", level: 2 })).toHaveCount(1);
  // The preview is a sample of the shop's page, not the admin page's title: Settings keeps exactly one h1.
  await expect(page.locator("h1:visible")).toHaveCount(1);
  await expect(page.getByRole("heading", { level: 1 })).toHaveCount(1);

  await card.getByLabel("Pause the shop (vacation mode)").check();
  await card.getByLabel("Message on the Closed page").fill("Closed until January 5");
  await expect(preview).toContainText("Customers see this now.");
  await expect(preview).toContainText("Closed until January 5");
  // The preview follows the unsaved business name and contact details, as customers would see them.
  await page.getByLabel("Business name").fill("Preview Prints");
  await page.getByLabel("Email", { exact: true }).fill("closed@example.com");
  await expect(preview).toContainText("Preview Prints");
  await expect(preview.getByText("closed@example.com")).toBeVisible();
  await card.getByLabel("Show my email, phone, and address in the shop").uncheck();
  await expect(preview.getByText("closed@example.com")).toHaveCount(0);
  await card.getByLabel("Show my email, phone, and address in the shop").check();

  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByText("Settings saved.")).toBeVisible();
  await expect.poll(async () => (await publicConfig(request)).shop.acceptingOrders).toBe(false);

  // What the preview shows is what the shop shows.
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "We're closed right now" })).toBeVisible();
  await expect(page.getByText("Closed until January 5")).toBeVisible();
  const refused = await request.post("/api/public/quote-request", { data: {} });
  expect(refused.status()).toBe(503);
  expect((await refused.json()).paused).toBe(true);

  // Restore the business identity this test changed (afterEach restores the shop block).
  const config = await getConfig(request);
  config.quote = structuredClone(original.quote);
  await putConfig(request, config);
});

test("a low-contrast accent is rejected with the measured ratio and a passing suggestion; a good one shows both variants", async ({ page, request }) => {
  await gotoSettings(page);
  const card = storefront(page);
  const accent = card.getByLabel("Accent color", { exact: true });

  await accent.fill("#ffee00");
  await expect(card.getByText(/doesn't have enough contrast/)).toBeVisible();
  await expect(card.getByText(/links measure \d+\.\d\d:1/)).toBeVisible();
  const suggestion = card.getByRole("button", { name: /^Use #[0-9a-f]{6} instead$/ });
  await expect(suggestion).toBeVisible();
  await expect(card.getByRole("group", { name: "Accent color preview" })).toHaveCount(0);
  // The server rejects it too.
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByRole("alert").filter({ hasText: "shop.accentColor" }).first()).toBeVisible();
  expect((await publicConfig(request)).shop.accent).toBeNull();

  await suggestion.click();
  const swatches = card.getByRole("group", { name: "Accent color preview" });
  await expect(swatches).toBeVisible();
  await expect(swatches.getByText("Light mode", { exact: true })).toBeVisible();
  await expect(swatches.getByText("Dark mode", { exact: true })).toBeVisible();
  await expect(swatches.getByText("Sample link")).toHaveCount(2);
  await expect(swatches.getByText("Sample button")).toHaveCount(2);
  await expect(swatches.getByText(/Link text \d+\.\d\d:1 · Button text \d+\.\d\d:1/)).toHaveCount(2);

  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByText("Settings saved.")).toBeVisible();
  await expect.poll(async () => (await publicConfig(request)).shop.accent).not.toBeNull();
  const pub = (await publicConfig(request)).shop.accent as Json;
  expect(pub.light.accent).toMatch(/^#[0-9a-f]{6}$/);
  expect(pub.dark.accent).toMatch(/^#[0-9a-f]{6}$/);

  await card.getByRole("button", { name: "Use the theme accent" }).click();
  await expect(accent).toHaveValue("");
});

test("a dark-adjusted accent says so", async ({ page }) => {
  await gotoSettings(page);
  const card = storefront(page);
  // A dark brown passes on the light backgrounds and needs a lighter variant on the dark ones.
  await card.getByLabel("Accent color", { exact: true }).fill("#5a1d00");
  const swatches = card.getByRole("group", { name: "Accent color preview" });
  await expect(swatches).toBeVisible();
  await expect(swatches.getByText("(adjusted for dark mode)")).toBeVisible();
});

test("the hero image needs alt text, uploads at once without dirtying the form, and removes after a confirmation", async ({ page, request }) => {
  await gotoSettings(page);
  const card = storefront(page);
  const file = { name: "banner.png", mimeType: "image/png", buffer: tinyPng(12, 34, 56) };

  // No description yet: nothing is sent, the field explains, and focus goes to it.
  let uploads = 0;
  page.on("request", (r) => {
    if (r.method() === "POST" && r.url().includes("/api/shop/hero-image")) uploads++;
  });
  await card.getByLabel("Upload image").setInputFiles(file);
  await expect(card.getByText(/Describe the image first/)).toBeVisible();
  await expect(card.getByLabel("Image description (alt text)")).toBeFocused();
  expect(uploads).toBe(0);

  await card.getByLabel("Image description (alt text)").fill("Shelves of prints");
  await card.getByLabel("Upload image").setInputFiles(file);
  await expect(card.getByText("Hero image uploaded and saved.")).toBeVisible();
  expect(uploads).toBe(1);
  // Stored at once, and not an unsaved change.
  await expect(page.locator(".save-bar")).toHaveCount(0);
  expect((await publicConfig(request)).shop.heroImage).toMatchObject({ alt: "Shelves of prints" });
  await expect(card.getByRole("img", { name: "Shelves of prints" })).toBeVisible();

  // Another edit's Discard keeps the uploaded image.
  await card.getByLabel("Shop heading").fill("Temp heading");
  await page.getByRole("button", { name: "Discard changes" }).click();
  await expect(card.getByRole("img", { name: "Shelves of prints" })).toBeVisible();

  // Removing asks first; Cancel changes nothing, and focus returns to the trigger.
  await card.getByRole("button", { name: "Remove hero image" }).click();
  await card.getByRole("button", { name: "Cancel" }).click();
  await expect(card.getByRole("button", { name: "Remove hero image" })).toBeFocused();
  await expect(card.getByRole("img", { name: "Shelves of prints" })).toBeVisible();
  await card.getByRole("button", { name: "Remove hero image" }).click();
  await card.getByRole("button", { name: "Yes, remove the hero image" }).click();
  await expect(card.getByText("Hero image removed.")).toBeVisible();
  await expect(card.getByRole("img", { name: "Shelves of prints" })).toHaveCount(0);
  expect((await publicConfig(request)).shop.heroImage).toBeNull();
  await expect(page.locator(".save-bar")).toHaveCount(0);
});

test("footer links: add up to six, errors name the field, removing moves focus to a neighbour", async ({ page, request }) => {
  await gotoSettings(page);
  const card = storefront(page);
  const add = card.getByRole("button", { name: "Add link" });
  for (let i = 0; i < 6; i++) {
    await add.click();
    await expect(card.getByLabel(/^Link \d address$/)).toHaveCount(i + 1);
  }
  await expect(card.getByLabel(/^Link \d address$/)).toHaveCount(6);
  await expect(add).toHaveAttribute("aria-disabled", "true");
  await add.click({ force: true }); // aria-disabled: the button is inert, so a click adds nothing
  await expect(card.getByLabel(/^Link \d address$/)).toHaveCount(6);

  await card.getByLabel("Link 1 label").fill("Etsy");
  await card.getByLabel("Link 1 address").fill("http://insecure.example.com");
  await card.getByLabel("Link 1 address").blur();
  await expect(card.getByText("must be an https:// address")).toBeVisible();
  await card.getByLabel("Link 1 address").fill("https://etsy.example.com/shop");
  // Rows 2 to 6 are blank and would be rejected; remove them, middle first.
  await card.getByRole("button", { name: "Remove link 3" }).click();
  await expect(card.getByLabel(/^Link \d address$/)).toHaveCount(5);
  await expect(card.getByRole("button", { name: "Remove link 3" })).toBeFocused();
  for (let i = 0; i < 4; i++) await card.getByRole("button", { name: "Remove link 2" }).click();
  await expect(card.getByRole("button", { name: "Remove link 2" })).toHaveCount(0);
  await expect(card.getByRole("button", { name: "Remove link 1" })).toBeFocused();

  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByText("Settings saved.")).toBeVisible();
  await expect.poll(async () => (await publicConfig(request)).shop.footerLinks).toEqual([{ label: "Etsy", url: "https://etsy.example.com/shop" }]);
});

test("leaving with unsaved Storefront edits asks first", async ({ page }) => {
  await gotoSettings(page);
  await storefront(page).getByLabel("Shop heading").fill("Unsaved heading");
  const dialogs: string[] = [];
  page.on("dialog", async (d) => {
    dialogs.push(d.message());
    await d.dismiss();
  });
  await page.getByRole("button", { name: "Quotes", exact: true }).click();
  await expect.poll(() => dialogs.length).toBeGreaterThan(0);
  await expect(page).toHaveURL(/#\/settings$/);
  await expect(storefront(page).getByLabel("Shop heading")).toHaveValue("Unsaved heading");
});

for (const scheme of ["light", "dark"] as const) {
  test(`the Storefront card has no axe violations (${scheme}, paused with an accent)`, async ({ page }) => {
    await page.emulateMedia({ colorScheme: scheme });
    await gotoSettings(page);
    const card = storefront(page);
    await card.getByLabel("Pause the shop (vacation mode)").check();
    await card.getByLabel("Message on the Closed page").fill("Back soon");
    await card.getByLabel("Accent color", { exact: true }).fill("#5a1d00");
    await card.getByRole("button", { name: "Add link" }).click();
    await expect(card.getByRole("group", { name: "Accent color preview" })).toBeVisible();
    const results = await new AxeBuilder({ page }).include("#section-storefront").withTags(TAGS).analyze();
    expect(results.violations, JSON.stringify(results.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target) })), null, 2)).toEqual([]);
  });
}

// D30: the Browser tab title reaches /api/public/config and the server-rendered <title> of the shop's HTML.
test("the Browser tab title saves and reaches the public config and the served HTML", async ({ page, request }) => {
  const title = "Zephyr Tab Title";
  const businessName = "Zephyr Prints";
  const seeded = await getConfig(request);
  seeded.quote.businessName = businessName;
  await putConfig(request, seeded);
  try {
  await gotoSettings(page);
  const card = storefront(page);
  await card.getByLabel("Browser tab title").fill(title);
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByText("Settings saved.")).toBeVisible();
  await expect.poll(async () => (await getConfig(request)).shop.pageTitle).toBe(title);
  expect((await publicConfig(request)).shop.pageTitle).toBe(title);
  const html = await (await request.get("/")).text();
  expect(html).toContain(`<title>${title}</title>`);
  await page.goto("/");
  await expect(page).toHaveTitle(new RegExp(`${title}$`)); // an inner page puts its own name first

  // Emptying the field falls back to the business name, in the public config and in the HTML.
  await gotoSettings(page);
  await storefront(page).getByLabel("Browser tab title").fill("");
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByText("Settings saved.")).toBeVisible();
  await expect.poll(async () => (await getConfig(request)).shop.pageTitle).toBe("");
  expect((await publicConfig(request)).shop.pageTitle).toBe(businessName);
  const fallbackHtml = await (await request.get("/")).text();
  expect(fallbackHtml).toContain(`<title>${businessName}</title>`);
  } finally {
    const config = await getConfig(request);
    config.quote.businessName = original.quote.businessName;
    await putConfig(request, config);
  }
});
