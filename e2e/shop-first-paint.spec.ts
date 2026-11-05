import { test, expect } from "@playwright/test";
import type { APIRequestContext } from "@playwright/test";
import { getConfig, putConfig } from "./helpers";
import type { Json } from "./helpers";

// A hard refresh of the shop paints the owner's settings, never built-in defaults. The server renders the
// shop's head (title, description) and embeds the public config; the page reads it synchronously and refreshes
// it in the background. All specs share one server, so every test restores what it changed.

const NAME = "Zephyr Print Works";
const INTRO = "Zephyr intro: hand-finished prints from our garage.";
const PAGE_TITLE = "Zephyr Prints & Co <3D>";
const ITEM_NAME = "Zephyr Gizmo";
const ACCENT = "#9c4212";
// Built-in text the page used to show before its config arrived.
const DEFAULTS = ["Get an estimate", "Order a ready-made 3D print", "Loading…"];

let original: Json;
let itemId = "";

async function applyShop(request: APIRequestContext, shop: Json = {}): Promise<void> {
  const config = await getConfig(request);
  config.quote = { ...config.quote, businessName: NAME };
  config.publicQuotes = { ...config.publicQuotes, introText: INTRO };
  config.shop = { ...config.shop, accentColor: ACCENT, pageTitle: PAGE_TITLE, ...shop };
  await putConfig(request, config);
}

test.describe.configure({ mode: "serial" });

test.beforeAll(async ({ request }) => {
  original = await getConfig(request);
  const withMaterial = structuredClone(original);
  withMaterial.materials = withMaterial.materials.map((m: Json) => (m.name === "PLA" ? { ...m, public: true } : m));
  await putConfig(request, withMaterial);
  const item = await request.post("/admin/api/items", {
    data: { name: ITEM_NAME, description: "A gizmo.", priceCents: 1500, public: true, sortOrder: 60 },
  });
  expect(item.ok()).toBeTruthy();
  itemId = (await item.json()).id;
  // The public items list is cached for 5 s with no write invalidation.
  await expect
    .poll(async () => ((await (await request.get("/api/public/items")).json()).items as Json[]).some((i) => i.id === itemId), {
      timeout: 15_000,
      intervals: [500, 500, 1000],
    })
    .toBe(true);
});

test.afterAll(async ({ request }) => {
  await request.delete(`/admin/api/items/${itemId}`);
  await putConfig(request, original);
});

test.describe("served HTML, JavaScript disabled", () => {
  test.use({ javaScriptEnabled: false });

  test("the first response already holds the owner's title, description, and config, and no defaults", async ({ page, request }) => {
    await applyShop(request);
    try {
      for (const path of ["/", "/order"]) {
        const res = await page.goto(path);
        expect(res?.status()).toBe(200);
        expect(res?.headers()["cache-control"]).toBe("no-cache");
        expect(await page.title()).toBe(PAGE_TITLE);
        const html = await page.content();
        expect(html).toContain('id="shop-config"');
        expect(html).toContain(NAME);
        expect(html).toContain(INTRO);
        expect(html).toContain('name="description"');
        for (const text of DEFAULTS) expect(html).not.toContain(text);

        const raw = await (await request.get(path)).text();
        const block = /<script type="application\/json" id="shop-config">(.*?)<\/script>/s.exec(raw);
        expect(block).not.toBeNull();
        // No raw markup characters can close the block early: the title's "<3D>" arrives escaped.
        expect(block![1] ?? "").not.toMatch(/[<>&]/);
        const embedded = JSON.parse(block![1] ?? "");
        const api = await (await request.get("/api/public/config")).json();
        expect(embedded).toEqual(api);
        expect(embedded.branding.businessName).toBe(NAME);
        expect(embedded.shop.pageTitle).toBe(PAGE_TITLE);
        expect(embedded.shop.accent).not.toBeNull();
        expect(raw).toContain("<title>Zephyr Prints &amp; Co &lt;3D&gt;</title>");
      }
    } finally {
      await putConfig(request, { ...(await getConfig(request)), shop: original.shop, quote: original.quote, publicQuotes: original.publicQuotes });
    }
  });

  test("the title falls back to the business name, then to Shop", async ({ page, request }) => {
    await applyShop(request, { pageTitle: "" });
    try {
      await page.goto("/");
      expect(await page.title()).toBe(NAME);
      const config = await getConfig(request);
      config.quote = { ...config.quote, businessName: "" };
      await putConfig(request, config);
      await page.goto("/");
      expect(await page.title()).toBe("Shop");
    } finally {
      await putConfig(request, { ...(await getConfig(request)), shop: original.shop, quote: original.quote, publicQuotes: original.publicQuotes });
    }
  });

  test("a paused shop still answers 200 and embeds the paused state", async ({ page, request }) => {
    await applyShop(request, { acceptingOrders: false, pausedMessage: "Back Monday" });
    try {
      const res = await page.goto("/");
      expect(res?.status()).toBe(200);
      const html = await page.content();
      expect(html).toContain("Back Monday");
      expect(html).toContain('"acceptingOrders":false');
    } finally {
      await putConfig(request, { ...(await getConfig(request)), shop: original.shop, quote: original.quote, publicQuotes: original.publicQuotes });
    }
  });
});

test.describe("first paint with JavaScript", () => {
  test("renders the owner's name before a slow /api/public/config returns", async ({ page, request }) => {
    await applyShop(request);
    try {
      let configDone = false;
      await page.route("**/api/public/config", async (route) => {
        await new Promise((r) => setTimeout(r, 2000));
        configDone = true;
        await route.continue();
      });
      await page.goto("/");
      // Well inside the 2 s delay, so a fetch-first page would still be showing its loading line.
      await expect(page.getByRole("heading", { level: 1 }).first()).toHaveText(NAME, { timeout: 1500 });
      expect(configDone).toBe(false);
      expect(await page.title()).toBe(PAGE_TITLE);
      await expect(page.getByText(INTRO)).toBeVisible();
      expect(await page.evaluate(() => document.documentElement.style.getPropertyValue("--accent"))).not.toBe("");
      for (const text of DEFAULTS.slice(1)) await expect(page.getByText(text)).toHaveCount(0);
    } finally {
      await page.unroute("**/api/public/config");
      await putConfig(request, { ...(await getConfig(request)), shop: original.shop, quote: original.quote, publicQuotes: original.publicQuotes });
    }
  });

  test("a page whose embedded config is missing shows a neutral loading state, then the shop", async ({ page, request }) => {
    await applyShop(request);
    try {
      // Strip the block from the HTML, as an old cache or proxy might.
      await page.route(/\/$/, async (route) => {
        const res = await route.fetch();
        const body = (await res.text()).replace(/<script type="application\/json" id="shop-config">.*?<\/script>/s, "");
        await route.fulfill({ response: res, body });
      });
      await page.route("**/api/public/config", async (route) => {
        await new Promise((r) => setTimeout(r, 1500));
        await route.continue();
      });
      await page.goto("/");
      await expect(page.getByText("Loading…")).toBeVisible();
      await expect(page.getByRole("heading", { level: 1 })).toHaveCount(0);
      await expect(page.getByText(INTRO)).toHaveCount(0);
      await expect(page.getByRole("heading", { level: 1 })).toHaveText(NAME);
    } finally {
      await page.unroute(/\/$/);
      await page.unroute("**/api/public/config");
      await putConfig(request, { ...(await getConfig(request)), shop: original.shop, quote: original.quote, publicQuotes: original.publicQuotes });
    }
  });

  test("tab titles: shop title on the gallery, '<page> · <shop title>' on an item and the Closed page", async ({ page, request }) => {
    await applyShop(request, { pageTitle: "Zephyr Prints" });
    try {
      await page.goto("/");
      await expect(page).toHaveTitle("Zephyr Prints");
      await page.getByRole("button", { name: new RegExp(ITEM_NAME) }).click();
      await expect(page).toHaveTitle(`${ITEM_NAME} · Zephyr Prints`);
      await page.goBack();
      await expect(page).toHaveTitle("Zephyr Prints");

      await applyShop(request, { pageTitle: "Zephyr Prints", acceptingOrders: false, pausedMessage: "Back Monday" });
      await page.goto("/");
      await expect(page.getByRole("heading", { level: 1, name: "We're closed right now" })).toBeVisible();
      await expect(page).toHaveTitle("Closed · Zephyr Prints");
    } finally {
      await putConfig(request, { ...(await getConfig(request)), shop: original.shop, quote: original.quote, publicQuotes: original.publicQuotes });
    }
  });
});
