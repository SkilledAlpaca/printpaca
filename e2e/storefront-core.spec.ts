import { test, expect } from "@playwright/test";
import type { APIRequestContext, Page } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { deflateSync, crc32 } from "node:zlib";
import { navButton, requestIdByReference } from "./helpers";

// v1.2 storefront core: the shop.* server rules (custom estimates, gallery, accepting orders), categories, the
// featured flag, the hero image and the accent-color check, with the minimal temporary shop markup. The styled
// storefront and the Settings > Storefront card arrive in phase 2; these specs pin the behavior underneath.
//
// All specs share one server and database, so every test restores the shop block and removes what it created.

const TAGS = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"];
const ITEM_NAME = "Core Widget";
const PAUSED = "Closed until January 5";

type Json = Record<string, any>;

/** A valid 1x1 PNG of one color: distinct colors give distinct bytes, so distinct content-addressed files. */
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

let originalConfig: Json;
let itemId = "";
const createdRequests: string[] = [];
const createdItems: string[] = [];
const createdCategories: string[] = [];
let xffCounter = 0;
const xff = () => ({ "X-Forwarded-For": `198.51.100.${10 + (xffCounter++ % 200)}` });

async function getConfig(request: APIRequestContext): Promise<Json> {
  const res = await request.get("/admin/api/settings");
  expect(res.ok()).toBeTruthy();
  return (await res.json()).config as Json;
}

/** PUT the current config with `patch` merged into shop (and `top` into the top level). */
async function setShop(request: APIRequestContext, patch: Json, top: Json = {}): Promise<void> {
  const config = await getConfig(request);
  Object.assign(config.shop, patch);
  Object.assign(config, top);
  const res = await request.put("/admin/api/settings", { data: { config } });
  expect(res.ok(), await res.text()).toBeTruthy();
}

async function resetShop(request: APIRequestContext): Promise<void> {
  const config = await getConfig(request);
  config.shop = structuredClone(originalConfig.shop);
  config.quote = structuredClone(originalConfig.quote);
  // The settings save keeps the stored hero file; the delete endpoint is what removes it.
  await request.delete("/admin/api/shop/hero-image");
  config.shop.heroImage = { file: "", alt: "" };
  const res = await request.put("/admin/api/settings", { data: { config } });
  expect(res.ok(), await res.text()).toBeTruthy();
}

async function publicShop(request: APIRequestContext): Promise<Json> {
  return (await (await request.get("/api/public/config")).json()).shop as Json;
}

async function publicItems(request: APIRequestContext): Promise<{ items: Json[]; categories: Json[] }> {
  return await (await request.get("/api/public/items")).json();
}

/** The public items list is cached for 5 s with no write invalidation: wait for `ok` to hold. */
async function pollItems(request: APIRequestContext, ok: (r: { items: Json[]; categories: Json[] }) => boolean): Promise<void> {
  await expect.poll(async () => ok(await publicItems(request)), { timeout: 15_000, intervals: [500, 500, 1000] }).toBe(true);
}

function orderBody(id: string): Json {
  return { itemId: id, selections: [], quantity: 1, name: "Core Visitor", email: "core@example.com", note: "" };
}

test.describe.configure({ mode: "serial" });

test.beforeAll(async ({ request }) => {
  originalConfig = await getConfig(request);
  // A public material, so the custom estimate flow exists.
  const withMaterial = structuredClone(originalConfig);
  withMaterial.materials = withMaterial.materials.map((m: Json) => (m.name === "PLA" ? { ...m, public: true } : m));
  expect((await request.put("/admin/api/settings", { data: { config: withMaterial } })).ok()).toBeTruthy();

  const item = await request.post("/admin/api/items", {
    data: { name: ITEM_NAME, description: "A widget.", priceCents: 1000, public: true, sortOrder: 50 },
  });
  expect(item.ok()).toBeTruthy();
  itemId = (await item.json()).id;
  createdItems.push(itemId);
  await pollItems(request, (r) => r.items.some((i) => i.id === itemId));
});

test.afterAll(async ({ request }) => {
  for (const id of createdRequests) await request.delete(`/admin/api/requests/${id}`);
  for (const id of createdItems) await request.delete(`/admin/api/items/${id}`);
  for (const id of createdCategories) await request.delete(`/admin/api/categories/${id}`);
  await request.delete("/admin/api/shop/hero-image");
  const restore = structuredClone(originalConfig);
  restore.shop.heroImage = { file: "", alt: "" };
  await request.put("/admin/api/settings", { data: { config: restore } });
});

test("defaults keep the v1.1 shop: both flows, the toggle, the custom card, ordering open", async ({ page, request }) => {
  const shop = await publicShop(request);
  expect(shop).toMatchObject({
    customEstimates: true,
    gallery: true,
    acceptingOrders: true,
    defaultView: "gallery",
    heroImage: null,
    accent: null,
    showStock: true,
    showFromPrice: true,
    itemSort: "manual",
    footerLinks: [],
    showContact: true,
  });
  await page.goto("/");
  await expect(page.getByRole("group", { name: "View" })).toBeVisible();
  await expect(page.getByText("Need something custom?")).toBeVisible();
  await expect(page.getByRole("button", { name: new RegExp(ITEM_NAME) })).toBeVisible();
});

test("custom estimates off: no estimate UI, D13 redirect with the note and contact details, API 404, items still orderable", async ({ page, request }) => {
  await setShop(request, { customEstimates: false }, { quote: { ...originalConfig.quote, email: "shop@example.com", phone: "555-0100" } });
  try {
    // Server: preview and submit are refused with a bare 404 that names no setting.
    const preview = await request.post("/api/public/quote/preview", { data: { material: "PLA", quantity: 1, weightG: 10, timeSeconds: 600 } });
    expect(preview.status()).toBe(404);
    const submit = await request.post("/api/public/quote-request", {
      headers: xff(),
      multipart: { name: "V", email: "v@example.com", material: "PLA", quantity: "1", weightG: "10", timeSeconds: "600" },
    });
    expect(submit.status()).toBe(404);
    for (const res of [preview, submit]) {
      const text = (await res.text()).toLowerCase();
      expect(text).not.toContain("estimate");
      expect(text).not.toContain("customestimates");
    }
    // Item orders still work.
    const order = await request.post("/api/public/item-order", { headers: xff(), data: orderBody(itemId) });
    expect(order.status()).toBe(201);
    createdRequests.push((await order.json()).id);

    // UI: no toggle, no custom card, and an old #/estimate link lands on the gallery with the note.
    await page.goto("/");
    await expect(page.getByRole("button", { name: new RegExp(ITEM_NAME) })).toBeVisible();
    await expect(page.getByRole("group", { name: "View" })).toHaveCount(0);
    await expect(page.getByText("Need something custom?")).toHaveCount(0);
    await page.goto("/#/estimate");
    await expect(page.getByRole("status").filter({ hasText: "Custom estimates aren't offered right now." })).toContainText("shop@example.com");
    await expect(page.getByRole("status").filter({ hasText: "Custom estimates aren't offered right now." })).toContainText("555-0100");
    await expect(page.getByRole("button", { name: new RegExp(ITEM_NAME) })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Custom estimate" })).toHaveCount(0);
    await expect(page).toHaveURL(/#\/$/);
  } finally {
    await resetShop(request);
  }
  // Turned back on, the flow returns and the material's Public flag was never touched.
  const preview = await request.post("/api/public/quote/preview", { data: { material: "PLA", quantity: 1, weightG: 10, timeSeconds: 600 } });
  expect(preview.status()).toBe(200);
});

test("gallery off: the shop is the estimate form only; item orders are refused; the items list is empty", async ({ page, request }) => {
  await setShop(request, { gallery: false });
  try {
    const order = await request.post("/api/public/item-order", { headers: xff(), data: orderBody(itemId) });
    expect(order.status()).toBe(404);
    expect(await order.text()).not.toContain("gallery");
    expect(await publicItems(request)).toEqual({ items: [], categories: [] });
    const preview = await request.post("/api/public/quote/preview", { data: { material: "PLA", quantity: 1, weightG: 10, timeSeconds: 600 } });
    expect(preview.status()).toBe(200);

    await page.goto("/");
    await expect(page.getByRole("heading", { name: "Custom estimate" })).toBeVisible();
    await expect(page.getByRole("button", { name: new RegExp(ITEM_NAME) })).toHaveCount(0);
    await expect(page.getByRole("group", { name: "View" })).toHaveCount(0);
    // An old item link shows the form, not a broken page.
    await page.goto(`/#/item/${itemId}`);
    await expect(page.getByRole("heading", { name: "Custom estimate" })).toBeVisible();
  } finally {
    await resetShop(request);
  }
  await pollItems(request, (r) => r.items.some((i) => i.id === itemId));
});

test("both gallery and custom estimates off is rejected with a clear message", async ({ request }) => {
  const config = await getConfig(request);
  config.shop.gallery = false;
  config.shop.customEstimates = false;
  const res = await request.put("/admin/api/settings", { data: { config } });
  expect(res.status()).toBe(422);
  const text = JSON.stringify(await res.json());
  expect(text).toContain("can't both be off");
  expect(text).toContain("accepting orders");
  expect((await publicShop(request)).gallery).toBe(true);
});

test("accepting orders off: the message replaces every submit, the server refuses both kinds, existing requests still work", async ({ page, request }) => {
  const existing = await request.post("/api/public/item-order", { headers: xff(), data: orderBody(itemId) });
  expect(existing.status()).toBe(201);
  const existingId = (await existing.json()).id as string;
  createdRequests.push(existingId);

  await setShop(request, { acceptingOrders: false, pausedMessage: PAUSED });
  try {
    const order = await request.post("/api/public/item-order", { headers: xff(), data: orderBody(itemId) });
    expect(order.status()).toBe(503);
    expect(await order.json()).toMatchObject({ paused: true, pausedMessage: PAUSED, error: PAUSED, errors: [PAUSED] });
    const preview = await request.post("/api/public/quote/preview", { data: { material: "PLA", quantity: 1, weightG: 10, timeSeconds: 600 } });
    expect(preview.status()).toBe(503);
    expect((await preview.json()).pausedMessage).toBe(PAUSED);
    const submit = await request.post("/api/public/quote-request", {
      headers: xff(),
      multipart: { name: "V", email: "v@example.com", material: "PLA", quantity: "1", weightG: "10", timeSeconds: "600" },
    });
    expect(submit.status()).toBe(503);
    expect((await submit.json()).paused).toBe(true);

    // The owner still sees and can work the request that came in before the pause.
    expect((await request.get(`/admin/api/requests/${existingId}`)).status()).toBe(200);

    // UI (D27): every public route shows the Closed page with the owner's message, and no order or estimate UI.
    for (const route of ["/", `/#/item/${itemId}`, "/#/estimate", "/#/category/anything", "/#/order/received"]) {
      await page.goto(route);
      await page.reload();
      await expect(page.getByRole("heading", { level: 1, name: "We're closed right now" })).toBeVisible();
      await expect(page.getByText(PAUSED)).toBeVisible();
      await expect(page.getByRole("button", { name: new RegExp(ITEM_NAME) })).toHaveCount(0);
      await expect(page.getByRole("button", { name: /place order|request an estimate/i })).toHaveCount(0);
      await expect(page.getByRole("group", { name: "View" })).toHaveCount(0);
      await expect(page.getByRole("navigation", { name: "Footer" })).toBeVisible();
    }
  } finally {
    await resetShop(request);
  }
  const reopened = await request.post("/api/public/item-order", { headers: xff(), data: orderBody(itemId) });
  expect(reopened.status()).toBe(201);
  createdRequests.push((await reopened.json()).id);
});

test("styled storefront: accent vars, stock and from-price switches, turnaround note, Closed page contact, mid-session pause", async ({ page, request }) => {
  const config = await getConfig(request);
  const item = await request.post("/admin/api/items", {
    data: { name: "Core Optioned", description: "d", priceCents: 700, public: true, stockMode: "in_stock", stockQty: 4, sortOrder: 60, optionGroups: [{ label: "Color", required: false, options: [{ name: "Red", deltaCents: 300 }] }] },
  });
  expect(item.ok()).toBeTruthy();
  const optioned = (await item.json()).id as string;
  createdItems.push(optioned);
  await pollItems(request, (r) => r.items.some((i) => i.id === optioned));
  try {
    await setShop(request, { accentColor: "#9c4212", showStock: false, showFromPrice: false, turnaroundNote: "Ships in 3 days." }, { quote: { ...config.quote, email: "shop@example.com", phone: "555-0100" } });
    await page.setViewportSize({ width: 390, height: 800 });
    await page.emulateMedia({ colorScheme: "light" });
    await page.goto("/");
    const card = page.getByRole("button", { name: /Core Optioned/ });
    await expect(card).toBeVisible();
    await expect(card).not.toContainText("In stock");
    await expect(card).not.toContainText("from");
    expect(await page.evaluate(() => document.documentElement.style.getPropertyValue("--accent"))).toBe("#9c4212");
    const dark = (await publicShop(request)).accent.dark.accent as string;
    await page.emulateMedia({ colorScheme: "dark" });
    await expect.poll(() => page.evaluate(() => document.documentElement.style.getPropertyValue("--accent"))).toBe(dark);
    // Customer tap targets stay 44 px on a phone.
    const box = await page.getByRole("link", { name: "Staff sign-in" }).boundingBox().catch(() => null);
    if (box) expect(box.height).toBeGreaterThanOrEqual(44);
    await card.click();
    await expect(page.getByText("Ships in 3 days.")).toBeVisible();
    await setShop(request, { showStock: true, showFromPrice: true });
    await page.goto("/");
    await page.reload();
    await expect(page.getByRole("button", { name: /Core Optioned/ })).toContainText("from");
    await expect(page.getByRole("button", { name: /Core Optioned/ })).toContainText("In stock: 4");

    // The shop closes while an order form is open: the refused submit flips the page to the Closed page.
    await page.goto(`/#/item/${optioned}`);
    await page.reload();
    await page.getByLabel("Name", { exact: true }).fill("Mid Session");
    await page.getByLabel("Email").fill("mid@example.com");
    await setShop(request, { acceptingOrders: false, pausedMessage: PAUSED });
    await page.getByRole("button", { name: "Place order" }).click();
    await expect(page.getByRole("heading", { level: 1, name: "We're closed right now" })).toBeVisible();
    await expect(page.getByText(PAUSED)).toBeVisible();
    await expect(page.getByRole("link", { name: "shop@example.com" })).toBeVisible();
    await expect(page.getByRole("link", { name: "555-0100" })).toBeVisible();
  } finally {
    await resetShop(request);
  }
});

test("paused: a saved confirmation stays visible with a note, a missing one shows the Closed page", async ({ page, request }) => {
  try {
    // Place a gallery order and an estimate through the UI while the shop is open.
    await page.goto("/");
    await page.getByRole("button", { name: new RegExp(ITEM_NAME) }).click();
    await page.getByLabel("Name", { exact: true }).fill("Saved Visitor");
    await page.getByLabel("Email").fill("saved@example.com");
    await page.getByRole("button", { name: "Place order" }).click();
    await expect(page.getByRole("heading", { name: "Order received" })).toBeVisible();
    const orderRef = ((await page.locator(".confirmation .ref strong").textContent()) ?? "").trim();
    createdRequests.push(await requestIdByReference(request, orderRef));

    await setShop(request, { acceptingOrders: false, pausedMessage: PAUSED });
    await page.reload();
    await expect(page.getByRole("heading", { name: "Order received" })).toBeVisible();
    await expect(page.locator(".confirmation .ref strong")).toHaveText(orderRef);
    const note = page.getByRole("status").filter({ hasText: "We're closed right now. We still have your request." });
    await expect(note).toContainText(PAUSED);
    await expect(page.getByRole("heading", { name: "We're closed right now" })).toHaveCount(0);
    expect(await axeProblems(page, "paused order confirmation")).toEqual([]);

    // Starting another order leads to the Closed page.
    await page.getByRole("button", { name: "Order another item" }).click();
    await expect(page.getByRole("heading", { level: 1, name: "We're closed right now" })).toBeVisible();

    // With nothing saved for the route, the Closed page shows.
    await page.evaluate(() => sessionStorage.clear());
    await page.goto("/#/order/received");
    await page.reload();
    await expect(page.getByRole("heading", { level: 1, name: "We're closed right now" })).toBeVisible();
    await page.goto("/#/estimate/received");
    await page.reload();
    await expect(page.getByRole("heading", { level: 1, name: "We're closed right now" })).toBeVisible();
  } finally {
    await resetShop(request);
  }
});

test("the Settings > Storefront card reaches the server rules", async ({ page, request }) => {
  await page.goto("/admin/");
  await navButton(page, "Settings").click();
  await expect(page).toHaveURL(/#\/settings$/);
  await page.getByRole("checkbox", { name: "Pause the shop (vacation mode)" }).check();
  await page.getByRole("textbox", { name: "Message on the Closed page" }).fill(PAUSED);
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByText("Settings saved.")).toBeVisible();
  try {
    await expect.poll(async () => (await publicShop(request)).acceptingOrders).toBe(false);
    expect((await publicShop(request)).pausedMessage).toBe(PAUSED);

    // The both-off rule shows before saving, with the next step.
    await page.getByRole("checkbox", { name: "Item gallery" }).uncheck();
    await page.getByRole("checkbox", { name: "Custom estimates" }).uncheck();
    await expect(page.getByRole("alert").filter({ hasText: /can't both be off/ })).toBeVisible();
  } finally {
    await resetShop(request);
  }
});

test("a low-contrast accent is rejected with the measured ratios and a suggestion; a passing one derives a dark variant", async ({ request }) => {
  const config = await getConfig(request);
  config.shop.accentColor = "#ffd700";
  const bad = await request.put("/admin/api/settings", { data: { config } });
  expect(bad.status()).toBe(422);
  const message = JSON.stringify(await bad.json());
  expect(message).toMatch(/links measure \d+\.\d\d:1/);
  expect(message).toMatch(/button text \d+\.\d\d:1/);
  expect(message).toMatch(/try #[0-9a-f]{6}/);

  try {
    await setShop(request, { accentColor: "#9C4212" });
    const accent = (await publicShop(request)).accent as Json;
    expect(accent.light).toEqual({ accent: "#9c4212", buttonText: "#ffffff" });
    expect(accent.dark.accent).toMatch(/^#[0-9a-f]{6}$/);
    expect(accent.dark.buttonText).toMatch(/^#[0-9a-f]{6}$/);
  } finally {
    await resetShop(request);
  }
  expect((await publicShop(request)).accent).toBeNull();
});

test("hero image: alt is required, the image is served, a settings save can't change the file, delete clears it", async ({ page, request }) => {
  const png = tinyPng(12, 34, 56);
  const noAlt = await request.post("/admin/api/shop/hero-image", { data: png, headers: { "Content-Type": "image/png" } });
  expect(noAlt.status()).toBe(422);
  expect(JSON.stringify(await noAlt.json())).toContain("alt");
  const notImage = await request.post("/admin/api/shop/hero-image?alt=Banner", { data: Buffer.from("plain text"), headers: { "Content-Type": "image/png" } });
  expect(notImage.status()).toBe(422);

  try {
    const up = await request.post("/admin/api/shop/hero-image?alt=Shelves%20of%20prints", { data: png, headers: { "Content-Type": "image/png" } });
    expect(up.status()).toBe(200);
    const hero = (await up.json()) as { file: string; url: string; alt: string };
    expect(hero.url).toBe(`/item-images/${hero.file}`);
    expect((await request.get(hero.url)).status()).toBe(200);

    expect((await publicShop(request)).heroImage).toEqual({ url: hero.url, alt: "Shelves of prints" });
    await page.goto("/");
    await expect(page.getByRole("img", { name: "Shelves of prints" })).toBeVisible();

    // A settings save keeps the file and takes only the alt; clearing the alt is refused.
    const config = await getConfig(request);
    expect(config.shop.heroImage.file).toBe(hero.file);
    config.shop.heroImage = { file: "ffffffffffffffff.png", alt: "Updated alt" };
    expect((await request.put("/admin/api/settings", { data: { config } })).ok()).toBeTruthy();
    expect((await getConfig(request)).shop.heroImage).toEqual({ file: hero.file, alt: "Updated alt" });
    config.shop.heroImage = { file: hero.file, alt: "" };
    const cleared = await request.put("/admin/api/settings", { data: { config } });
    expect(cleared.status()).toBe(422);
    expect(JSON.stringify(await cleared.json())).toContain("shop.heroImage.alt");

    expect((await request.delete("/admin/api/shop/hero-image")).status()).toBe(200);
    expect((await publicShop(request)).heroImage).toBeNull();
    expect((await request.get(hero.url)).status()).toBe(404);
  } finally {
    await resetShop(request);
  }
});

test("categories and featured: chips list visible categories with public items, featured first, filter route, delete unlinks", async ({ page, request }) => {
  const mkCategory = async (data: Json): Promise<Json> => {
    const res = await request.post("/admin/api/categories", { data });
    expect(res.status(), await res.text()).toBe(201);
    const c = (await res.json()) as Json;
    createdCategories.push(c.id);
    return c;
  };
  const mkItem = async (data: Json): Promise<Json> => {
    const res = await request.post("/admin/api/items", { data: { public: true, ...data } });
    expect(res.status(), await res.text()).toBe(201);
    const i = (await res.json()) as Json;
    createdItems.push(i.id);
    return i;
  };
  const toys = await mkCategory({ name: "Core Toys", sortOrder: 1 });
  const secret = await mkCategory({ name: "Core Secret", sortOrder: 2, hidden: true });
  expect(toys.slug).toBe("core-toys");
  const dup = await request.post("/admin/api/categories", { data: { name: "Other", slug: "core-toys" } });
  expect(dup.status()).toBe(409);

  await mkItem({ name: "Core Dino", priceCents: 500, sortOrder: 1, categoryId: toys.id });
  await mkItem({ name: "Core Star", priceCents: 700, sortOrder: 90, categoryId: toys.id, featured: true });
  await mkItem({ name: "Core Ghost", priceCents: 300, sortOrder: 2, categoryId: secret.id });
  await pollItems(request, (r) => r.items.some((i) => i.name === "Core Ghost"));

  const list = await publicItems(request);
  expect(list.categories).toEqual([{ slug: "core-toys", name: "Core Toys" }]);
  const names = list.items.map((i) => i.name);
  // Featured items lead, then the manual order.
  expect(names.indexOf("Core Star")).toBe(0);
  expect(names.indexOf("Core Dino")).toBeLessThan(names.indexOf(ITEM_NAME));
  const ghost = list.items.find((i) => i.name === "Core Ghost")!;
  expect(ghost.category).toBeNull();

  await page.goto("/");
  const nav = page.getByRole("navigation", { name: "Categories" });
  await expect(nav.getByRole("link", { name: "Core Toys" })).toBeVisible();
  await expect(nav.getByRole("link", { name: "Core Secret" })).toHaveCount(0);
  await nav.getByRole("link", { name: "Core Toys" }).click();
  await expect(page).toHaveURL(/#\/category\/core-toys$/);
  await expect(page.getByRole("heading", { name: "Core Toys" })).toBeVisible();
  await expect(page.getByRole("button", { name: /Core Star/ })).toBeVisible();
  await expect(page.getByRole("button", { name: /Core Dino/ })).toBeVisible();
  await expect(page.getByRole("button", { name: new RegExp(ITEM_NAME) })).toHaveCount(0);
  await expect(page.getByRole("button", { name: /Core Ghost/ })).toHaveCount(0);

  // An unknown slug returns to the gallery with a note.
  await page.goto("/#/category/nope");
  await expect(page.getByRole("status").filter({ hasText: "That category is no longer available." })).toBeVisible();

  // Deleting the category unlinks its items; the items stay public.
  expect((await request.delete(`/admin/api/categories/${toys.id}`)).status()).toBe(204);
  createdCategories.splice(createdCategories.indexOf(toys.id), 1);
  await pollItems(request, (r) => r.categories.length === 0 && r.items.some((i) => i.name === "Core Dino" && i.category === null));
  const adminItems = ((await (await request.get("/admin/api/items")).json()).items as Json[]).filter((i) => i.name.startsWith("Core "));
  expect(adminItems.every((i) => i.categoryId === "" || i.categoryId === secret.id)).toBe(true);
});

test("the item API keeps version concurrency with the new fields", async ({ request }) => {
  const res = await request.post("/admin/api/items", { data: { name: "Core Versioned", priceCents: 100, public: false } });
  const item = (await res.json()) as Json;
  createdItems.push(item.id);
  const first = await request.put(`/admin/api/items/${item.id}`, { data: { name: "Core Versioned", priceCents: 100, featured: true, version: item.version } });
  expect(first.status()).toBe(200);
  const stale = await request.put(`/admin/api/items/${item.id}`, { data: { name: "Core Versioned", priceCents: 100, featured: false, version: item.version } });
  expect(stale.status()).toBe(409);
  const unknown = await request.put(`/admin/api/items/${item.id}`, { data: { name: "Core Versioned", priceCents: 100, categoryId: "nope" } });
  expect(unknown.status()).toBe(422);
});

test("the export carries categories and the new item fields", async ({ request }) => {
  const c = await request.post("/admin/api/categories", { data: { name: "Core Export" } });
  const cat = (await c.json()) as Json;
  createdCategories.push(cat.id);
  const body = (await (await request.get("/admin/api/export")).json()) as Json;
  expect(body.categories.some((x: Json) => x.id === cat.id)).toBe(true);
  expect(body.items.every((i: Json) => "categoryId" in i && "featured" in i)).toBe(true);
});

async function axeProblems(page: Page, label: string): Promise<string[]> {
  const out: string[] = [];
  for (const scheme of ["light", "dark"] as const) {
    for (const vp of [{ width: 1280, height: 900 }, { width: 390, height: 844 }]) {
      await page.setViewportSize(vp);
      await page.emulateMedia({ colorScheme: scheme });
      await page.waitForLoadState("networkidle");
      await page.evaluate(() => document.fonts.ready);
      const results = await new AxeBuilder({ page }).withTags(TAGS).analyze();
      out.push(...results.violations.map((v) => `${label} [${scheme}, ${vp.width}]: ${v.id} (${v.impact}) ${v.nodes[0]?.target.join(" ")}`));
    }
  }
  await page.emulateMedia({ colorScheme: "light" });
  await page.setViewportSize({ width: 1280, height: 900 });
  return out;
}

test("axe: the shop passes with the storefront settings on and with each flow switched off", async ({ page, request }) => {
  test.setTimeout(180_000);
  const problems: string[] = [];
  try {
    const up = await request.post("/admin/api/shop/hero-image?alt=Shelves%20of%20prints", { data: tinyPng(90, 80, 70), headers: { "Content-Type": "image/png" } });
    expect(up.ok()).toBeTruthy();
    await setShop(request, {
      heading: "Core Print Shop",
      announcement: "Holiday shipping cutoff is December 15.",
      accentColor: "#9c4212",
      footerLinks: [{ label: "Etsy", url: "https://example.com/shop" }],
      pausedMessage: PAUSED,
    });
    await page.goto("/");
    await expect(page.getByText("Holiday shipping cutoff")).toBeVisible();
    problems.push(...(await axeProblems(page, "shop with settings")));

    await setShop(request, { acceptingOrders: false });
    await page.goto("/");
    await expect(page.getByRole("heading", { level: 1, name: "We're closed right now" })).toBeVisible();
    await expect(page.getByText(PAUSED)).toBeVisible();
    problems.push(...(await axeProblems(page, "closed page")));
    await page.goto("/#/estimate");
    await page.reload();
    await expect(page.getByText(PAUSED)).toBeVisible();
    problems.push(...(await axeProblems(page, "closed estimate route")));

    await setShop(request, { acceptingOrders: true, customEstimates: false });
    await page.goto("/#/estimate");
    await page.reload();
    await expect(page.getByRole("status").filter({ hasText: "Custom estimates aren't offered right now." })).toBeVisible();
    problems.push(...(await axeProblems(page, "estimates off")));

    await setShop(request, { customEstimates: true, gallery: false });
    await page.goto("/");
    await expect(page.getByRole("heading", { name: "Custom estimate" })).toBeVisible();
    problems.push(...(await axeProblems(page, "gallery off")));
  } finally {
    await resetShop(request);
  }
  expect(problems).toEqual([]);
});
