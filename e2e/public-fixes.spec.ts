import { test, expect } from "@playwright/test";
import type { APIRequestContext } from "@playwright/test";
import { requestIdByReference } from "./helpers";

// v1.2 public-shop usability fixes (P1-P10 of the simulated review). Each test fails without its fix.
// Orders use their own X-Forwarded-For address (ADMIN_TRUST_PROXY is on for this server), outside the per-IP
// submission budget the other specs use.

type Json = Record<string, any>;
const PREFIX = "PubFix";
const WIDGET = `${PREFIX} Widget`;
const TRAY = `${PREFIX} Tray`;
let originalConfig: Json;
const createdItems: string[] = [];
const createdRequests: string[] = [];
let xffCounter = 0;
const xff = () => ({ "X-Forwarded-For": `192.0.2.${100 + (xffCounter++ % 100)}` });

async function getConfig(request: APIRequestContext): Promise<Json> {
  const res = await request.get("/admin/api/settings");
  expect(res.ok()).toBeTruthy();
  return (await res.json()).config as Json;
}
async function putConfig(request: APIRequestContext, config: Json): Promise<void> {
  const res = await request.put("/admin/api/settings", { data: { config } });
  expect(res.ok(), await res.text()).toBeTruthy();
}
async function setShop(request: APIRequestContext, patch: Json, top: Json = {}): Promise<void> {
  const config = await getConfig(request);
  Object.assign(config.shop, patch);
  Object.assign(config, top);
  await putConfig(request, config);
}
async function resetShop(request: APIRequestContext): Promise<void> {
  const config = await getConfig(request);
  config.shop = structuredClone(originalConfig.shop);
  config.quote = structuredClone(originalConfig.quote);
  await putConfig(request, config);
}
async function mkItem(request: APIRequestContext, body: Json): Promise<string> {
  const res = await request.post("/admin/api/items", { data: { public: true, ...body } });
  expect(res.ok(), await res.text()).toBeTruthy();
  const id = (await res.json()).id as string;
  createdItems.push(id);
  return id;
}
async function pollItems(request: APIRequestContext, names: string[]): Promise<void> {
  await expect
    .poll(
      async () => {
        const got = ((await (await request.get("/api/public/items")).json()).items as { name: string }[]).map((i) => i.name);
        return names.every((n) => got.includes(n));
      },
      { timeout: 15_000, intervals: [500, 500, 1000] },
    )
    .toBe(true);
}

test.describe.configure({ mode: "serial" });

test.beforeAll(async ({ request }) => {
  originalConfig = await getConfig(request);
  const withMaterial = structuredClone(originalConfig);
  withMaterial.materials = withMaterial.materials.map((m: Json) => (m.name === "PLA" ? { ...m, public: true } : m));
  await putConfig(request, withMaterial);
  await mkItem(request, { name: WIDGET, description: "A widget.", priceCents: 900, sortOrder: 80 });
  await mkItem(request, {
    name: TRAY,
    description: "A tray.",
    priceCents: 2200,
    sortOrder: 81,
    featured: true,
    optionGroups: [
      {
        label: "Size",
        required: true,
        options: [
          { name: "Small", deltaCents: 0 },
          { name: "Large", deltaCents: 400 },
        ],
      },
    ],
  });
  await pollItems(request, [WIDGET, TRAY]);
});

test.afterAll(async ({ request }) => {
  for (const id of createdRequests) await request.delete(`/admin/api/requests/${id}`);
  for (const id of createdItems) await request.delete(`/admin/api/items/${id}`);
  await putConfig(request, originalConfig);
});

const openItem = async (page: import("@playwright/test").Page, name: string) => {
  await page.goto("/");
  await page.getByRole("button", { name: new RegExp(name) }).click();
};

test.describe("P1: Not you? never appears while typing", () => {
  test("item form: Name, Email, Tab, then typing reaches the Note and keeps both fields", async ({ page }) => {
    await page.setExtraHTTPHeaders(xff());
    await openItem(page, WIDGET);
    await page.getByLabel("Name", { exact: true }).fill("Pat Typist");
    await page.getByLabel("Email").fill("pat@example.com");
    await page.getByLabel("Email").press("Tab");
    await expect(page.getByLabel("Note (optional)")).toBeFocused();
    await page.keyboard.type(" hello");
    await expect(page.getByLabel("Name", { exact: true })).toHaveValue("Pat Typist");
    await expect(page.getByLabel("Email")).toHaveValue("pat@example.com");
    await expect(page.getByLabel("Note (optional)")).toHaveValue(" hello");
    await expect(page.getByRole("button", { name: /Not you/ })).toHaveCount(0);
  });

  test("estimate form: Name, Email, Tab, then typing reaches the Note and keeps both fields", async ({ page }) => {
    await page.setExtraHTTPHeaders(xff());
    await page.goto("/#/estimate");
    await page.getByLabel("Name", { exact: true }).fill("Pat Typist");
    await page.getByLabel("Email").fill("pat@example.com");
    await page.getByLabel("Email").press("Tab");
    await expect(page.getByLabel("Note (optional)")).toBeFocused();
    await page.keyboard.type(" hello");
    await expect(page.getByLabel("Name", { exact: true })).toHaveValue("Pat Typist");
    await expect(page.getByLabel("Email")).toHaveValue("pat@example.com");
    await expect(page.getByLabel("Note (optional)")).toHaveValue(" hello");
    await expect(page.getByRole("button", { name: /Not you/ })).toHaveCount(0);
  });

  test("prefilled details show Not you above Name, at least 24 px tall; clearing focuses Name and Undo restores", async ({ page }) => {
    await page.setExtraHTTPHeaders(xff());
    await page.goto("/#/estimate");
    await page.getByLabel("Name", { exact: true }).fill("Pat Typist");
    await page.getByLabel("Email").fill("pat@example.com");
    // Carry the details to an item form: they arrive prefilled, so the control shows.
    await page.getByRole("button", { name: "Gallery", exact: true }).click();
    await page.getByRole("button", { name: new RegExp(WIDGET) }).click();
    const notYou = page.getByRole("button", { name: "Not you? Clear name and email" });
    await expect(notYou).toBeVisible();
    expect((await notYou.boundingBox())!.height).toBeGreaterThanOrEqual(24);
    // Before Name in the page order.
    const [by, ny] = [(await notYou.boundingBox())!.y, (await page.getByLabel("Name", { exact: true }).boundingBox())!.y];
    expect(by).toBeLessThan(ny);

    await notYou.click();
    await expect(page.getByLabel("Name", { exact: true })).toHaveValue("");
    await expect(page.getByLabel("Email")).toHaveValue("");
    await expect(page.getByLabel("Name", { exact: true })).toBeFocused();
    await page.getByRole("button", { name: "Undo" }).click();
    await expect(page.getByLabel("Name", { exact: true })).toHaveValue("Pat Typist");
    await expect(page.getByLabel("Email")).toHaveValue("pat@example.com");
  });

  test("the estimate form also shows Not you for prefilled details", async ({ page }) => {
    await page.setExtraHTTPHeaders(xff());
    await openItem(page, WIDGET);
    await page.getByLabel("Name", { exact: true }).fill("Pat Typist");
    await page.getByLabel("Email").fill("pat@example.com");
    await expect(page.getByRole("button", { name: /Not you/ })).toHaveCount(0);
    await page.getByRole("button", { name: /Back to gallery/ }).click();
    await page.getByRole("button", { name: "Custom estimate" }).click();
    await expect(page.getByRole("button", { name: "Not you? Clear name and email" })).toBeVisible();
    await expect(page.getByLabel("Name", { exact: true })).toHaveValue("Pat Typist");
  });
});
