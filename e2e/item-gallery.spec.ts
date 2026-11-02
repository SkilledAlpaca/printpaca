import { test, expect } from "@playwright/test";
import { navButton } from "./helpers";

// The public listener runs on its own port, so URLs here are absolute (see public.spec.ts).
const PUBLIC_URL = "http://127.0.0.1:8098/";
const CUSTOMER_NAME = "Gallery Visitor";
const CUSTOMER_EMAIL = "gallery@example.com";
const ITEM_NAME = "Articulated Dragon";

// priceCents 1000 plus the "Red" option's deltaCents 500 is $15.00 per unit; times quantity 2 is $30.00.
const UNIT_CENTS = 1000;
const OPTION_DELTA_CENTS = 500;
const ORDER_QUANTITY = 2;
const EXPECTED_TOTAL = "$30.00";

// PUT /api/settings needs the entire config document, so read it and PUT back a full copy with only
// materials[pla].public flipped to true.
let originalConfig: Record<string, unknown> & { materials: Array<{ name: string; public?: boolean }> };
let materialIndex = -1;
let createdItemId: string | null = null;
let createdRequestId: string | null = null;
let createdBackorderItemId: string | null = null;
let createdBackorderRequestId: string | null = null;

test.describe.configure({ mode: "serial" });

test.beforeAll(async ({ request }) => {
  // The Gallery/Custom estimate toggle needs both items and public materials, and the zero-item test
  // needs the quote form rather than the "No materials are currently offered" empty state.
  const getRes = await request.get("/admin/api/settings");
  expect(getRes.ok()).toBeTruthy();
  const body = await getRes.json();
  originalConfig = body.config;

  materialIndex = originalConfig.materials.findIndex((m: { name: string }) => m.name === "PLA");
  expect(materialIndex).toBeGreaterThanOrEqual(0);

  const updated = structuredClone(originalConfig);
  updated.materials = updated.materials.map((m, i) => (i === materialIndex ? { ...m, public: true } : m));

  const putRes = await request.put("/admin/api/settings", { data: { config: updated } });
  expect(putRes.ok()).toBeTruthy();
});

test.afterAll(async ({ request }) => {
  if (createdRequestId) {
    await request.delete(`/admin/api/requests/${createdRequestId}`);
  }
  if (createdItemId) {
    await request.delete(`/admin/api/items/${createdItemId}`);
  }
  if (createdBackorderRequestId) {
    await request.delete(`/admin/api/requests/${createdBackorderRequestId}`);
  }
  if (createdBackorderItemId) {
    await request.delete(`/admin/api/items/${createdBackorderItemId}`);
  }
  if (originalConfig) {
    const revertRes = await request.put("/admin/api/settings", { data: { config: originalConfig } });
    expect(revertRes.ok()).toBeTruthy();
  }
});

test("with no public items, the toggle is absent and the quote form renders directly", async ({ page }) => {
  // No item exists yet, with the public material in place: the state a fresh shop starts in.
  await page.goto(`${PUBLIC_URL}order`);
  await expect(page.getByRole("heading", { name: "printpaca" })).toBeVisible();

  await expect(page.getByRole("group", { name: "View" })).toHaveCount(0);

  await expect(page.getByLabel("Material")).toBeVisible();
  await expect(page.getByRole("button", { name: "Request an estimate" })).toBeVisible();
});

test("admin seeds a public item and the public gallery shows it", async ({ request, page }) => {
  const createRes = await request.post("/admin/api/items", {
    data: {
      name: ITEM_NAME,
      description: "A fully articulated 3D-printed dragon.",
      priceCents: UNIT_CENTS,
      optionLabel: "Color",
      options: [
        { name: "Black", deltaCents: 0 },
        { name: "Red", deltaCents: OPTION_DELTA_CENTS },
      ],
      public: true,
      stockMode: "in_stock",
      stockQty: 5,
      sortOrder: 0,
    },
  });
  expect(createRes.ok()).toBeTruthy();
  const created = await createRes.json();
  createdItemId = created.id;

  // handlePublicItems caches its response for publicItemsCacheTTL (5s) with no write invalidation, so
  // poll the public endpoint until the new item shows up before navigating.
  await expect
    .poll(
      async () => {
        const r = await request.get(`${PUBLIC_URL}api/public/items`);
        return ((await r.json()).items as unknown[]).length;
      },
      { timeout: 15_000, intervals: [500, 500, 1000] },
    )
    .toBe(1);

  await page.goto(`${PUBLIC_URL}order`);
  await expect(page.getByRole("heading", { name: "printpaca" })).toBeVisible();

  // With items present the page defaults to the gallery view; no toggle click needed.
  const toggle = page.getByRole("group", { name: "View" });
  await expect(toggle).toBeVisible();
  await expect(page.getByRole("button", { name: "Gallery" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Custom estimate" })).toBeVisible();

  const card = page.getByRole("button", { name: new RegExp(ITEM_NAME) });
  await expect(card).toBeVisible();
  // A positive deltaCents makes the card prefix the base price with "from ", so match a substring.
  await expect(card).toContainText("$10.00");
  await expect(card).toContainText("In stock: 5");
});

test("ordering flow: pick an option, set quantity, submit, confirm the total", async ({ page }) => {
  await page.goto(`${PUBLIC_URL}order`);
  const card = page.getByRole("button", { name: new RegExp(ITEM_NAME) });
  await card.click();

  await expect(page.getByRole("heading", { name: ITEM_NAME })).toBeVisible();

  // Pick the non-default option (options[0] is "Black") and a quantity of 2.
  await page.getByLabel("Color").selectOption("Red");
  await page.getByLabel("Quantity").fill(String(ORDER_QUANTITY));

  // The live total also carries a trailing " · $X.XX each" suffix, so use toContainText to pin only
  // the total.
  const totalLocator = page.locator(".estimate .price");
  await expect(totalLocator).toContainText(EXPECTED_TOTAL);

  await page.getByLabel("Name").fill(CUSTOMER_NAME);
  await page.getByLabel("Email").fill(CUSTOMER_EMAIL);

  await page.getByRole("button", { name: "Place order" }).click();

  await expect(page.getByText("Order received")).toBeVisible();
  await expect(page.locator(".confirmation .price")).toHaveText(EXPECTED_TOTAL);
  const refText = (await page.locator(".confirmation .ref").textContent())?.trim() ?? "";
  const refMatch = refText.match(/^Reference: (.+)$/);
  expect(refMatch).not.toBeNull();
  createdRequestId = refMatch![1]!;
});

test("the order appears in the admin Requests list as an item order", async ({ page, request }) => {
  await page.goto("/admin/");
  await navButton(page, "Requests").click();
  await expect(page).toHaveURL(/#\/requests$/);

  const row = page.locator(".plain-list li", { hasText: CUSTOMER_NAME });
  await expect(row).toBeVisible();
  await expect(row.locator(".r-price")).toHaveText(EXPECTED_TOTAL);
  await expect(row.getByText("item order")).toBeVisible();

  // The pill is the UI check; Kind/ItemID/ItemName/OptionName live in the API response.
  const apiRes = await request.get(`/admin/api/requests/${createdRequestId}`);
  expect(apiRes.ok()).toBeTruthy();
  const stored = await apiRes.json();
  expect(stored.kind).toBe("item");
  expect(stored.itemId).toBe(createdItemId);
  expect(stored.itemName).toBe(ITEM_NAME);
  expect(stored.optionName).toBe("Red");
  expect(stored.priceCents).toBe((UNIT_CENTS + OPTION_DELTA_CENTS) * ORDER_QUANTITY);
});

// "Accept orders beyond stock" (allowBackorder)

const BACKORDER_ITEM_NAME = "Backordered Widget";
const BACKORDER_UNIT_CENTS = 500;
const BACKORDER_QUANTITY = 3; // exceeds the item's own stockQty of 0

test("an in_stock item with allowBackorder and zero qty shows 'Available to order' and still accepts an order", async ({
  page,
  request,
}) => {
  const createRes = await request.post("/admin/api/items", {
    data: {
      name: BACKORDER_ITEM_NAME,
      description: "Printed after ordering.",
      priceCents: BACKORDER_UNIT_CENTS,
      public: true,
      stockMode: "in_stock",
      stockQty: 0,
      allowBackorder: true,
      sortOrder: 1,
    },
  });
  expect(createRes.ok()).toBeTruthy();
  const created = await createRes.json();
  createdBackorderItemId = created.id;

  // Same cache-busting poll as above.
  await expect
    .poll(
      async () => {
        const r = await request.get(`${PUBLIC_URL}api/public/items`);
        const items = (await r.json()).items as Array<{ name: string }>;
        return items.some((it) => it.name === BACKORDER_ITEM_NAME);
      },
      { timeout: 15_000, intervals: [500, 500, 1000] },
    )
    .toBe(true);

  await page.goto(`${PUBLIC_URL}order`);
  await expect(page.getByRole("button", { name: "Gallery" })).toBeVisible();

  const card = page.getByRole("button", { name: new RegExp(BACKORDER_ITEM_NAME) });
  await expect(card).toBeVisible();
  await expect(card).toContainText("Available to order");
  await expect(card).toBeEnabled(); // zero qty must not disable the card when allowBackorder is set

  await card.click();
  await expect(page.getByRole("heading", { name: BACKORDER_ITEM_NAME })).toBeVisible();

  // Quantity's max reflects the lifted cap (100), not the item's stockQty of 0.
  await expect(page.getByLabel("Quantity")).toHaveAttribute("max", "100");

  await page.getByLabel("Quantity").fill(String(BACKORDER_QUANTITY));
  await page.getByLabel("Name").fill(CUSTOMER_NAME);
  await page.getByLabel("Email").fill(CUSTOMER_EMAIL);
  await page.getByRole("button", { name: "Place order" }).click();

  await expect(page.getByText("Order received")).toBeVisible();
  const refText = (await page.locator(".confirmation .ref").textContent())?.trim() ?? "";
  const refMatch = refText.match(/^Reference: (.+)$/);
  expect(refMatch).not.toBeNull();
  createdBackorderRequestId = refMatch![1]!;

  const apiRes = await request.get(`/admin/api/requests/${createdBackorderRequestId}`);
  expect(apiRes.ok()).toBeTruthy();
  const stored = await apiRes.json();
  expect(stored.priceCents).toBe(BACKORDER_UNIT_CENTS * BACKORDER_QUANTITY);
});

// Custom-estimate CTA card

test("the gallery's custom-estimate CTA card switches to the quote view", async ({ page }) => {
  // Items and a public material exist by now, so the toggle and the CTA card both render.
  await page.goto(`${PUBLIC_URL}order`);
  await expect(page.getByRole("button", { name: "Gallery" })).toBeVisible();

  const cta = page.getByRole("button", { name: /Need something custom\?/ });
  await expect(cta).toBeVisible();
  await expect(cta).toContainText("Upload your model for an instant estimate.");

  await cta.click();

  // The plain manual-quote form is the quote view's content.
  await expect(page.getByLabel("Material")).toBeVisible();
  await expect(page.getByRole("button", { name: "Request an estimate" })).toBeVisible();
});
