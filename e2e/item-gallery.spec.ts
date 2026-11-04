import { test, expect } from "@playwright/test";
import { deflateSync, crc32 } from "node:zlib";
import { navButton, requestIdByReference } from "./helpers";

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
  ihdr[8] = 8; // bit depth
  ihdr[9] = 2; // RGB
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk("IHDR", ihdr),
    chunk("IDAT", deflateSync(Buffer.from([0, r, g, b]))),
    chunk("IEND", Buffer.alloc(0)),
  ]);
}

// The public listener runs on its own port, so URLs here are absolute (see public.spec.ts).
const PUBLIC_URL = "http://127.0.0.1:8098/";
const CUSTOMER_NAME = "Gallery Visitor";
const CUSTOMER_EMAIL = "gallery@example.com";
const ITEM_NAME = "Articulated Dragon";

// priceCents 1000 plus the "Red" option's deltaCents 500 is $15.00 per unit; times quantity 2 is $30.00.
const UNIT_CENTS = 1000;
const OPTION_DELTA_CENTS = 500;
const ENGRAVING_DELTA_CENTS = 200;
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
      optionGroups: [
        {
          label: "Color",
          required: true,
          options: [
            { name: "Black", deltaCents: 0 },
            { name: "Red", deltaCents: OPTION_DELTA_CENTS },
          ],
        },
        { label: "Engraving", required: false, options: [{ name: "Name plate", deltaCents: ENGRAVING_DELTA_CENTS }] },
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

  // The Black and Red options get photos (the item has none); Engraving's option has no photo.
  const colorOptions = created.optionGroups[0].options as { id: string; name: string }[];
  for (const [name, png] of [
    ["Black", tinyPng(10, 10, 10)],
    ["Red", tinyPng(200, 20, 20)],
  ] as const) {
    const up = await request.post(`/admin/api/items/${created.id}/options/${colorOptions.find((o) => o.name === name)!.id}/image`, {
      headers: { "Content-Type": "image/png" },
      data: png,
    });
    expect(up.ok()).toBeTruthy();
  }

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

test("ordering flow: required group blocks, optional starts on None, prices sum, photo follows the choice", async ({ page, request }) => {
  await page.goto(`${PUBLIC_URL}order`);
  const card = page.getByRole("button", { name: new RegExp(ITEM_NAME) });
  await card.click();

  await expect(page.getByRole("heading", { name: ITEM_NAME })).toBeVisible();

  // A required group starts on its placeholder; an optional one starts on None.
  const color = page.getByLabel("Color");
  const engraving = page.getByLabel("Engraving (optional)");
  await expect(color).toHaveValue("");
  await expect(color.locator("option:checked")).toHaveText("Choose Color");
  await expect(engraving.locator("option:checked")).toHaveText("None");
  await expect(page.locator("img[data-option-photo]")).toHaveCount(0);

  // Ordering is blocked until the required group is chosen, with the usual error pattern.
  await page.getByLabel("Name", { exact: true }).fill(CUSTOMER_NAME);
  await page.getByLabel("Email").fill(CUSTOMER_EMAIL);
  await page.getByRole("button", { name: "Place order" }).click();
  const colorError = page.getByText("Choose Color.", { exact: true });
  await expect(colorError).toBeVisible();
  await expect(page.getByRole("alert").locator(".error-summary")).toContainText("1 field needs attention");
  await expect(color).toHaveAttribute("aria-invalid", "true");
  await expect(color).toBeFocused();
  // The error sits under the select and is linked to it; the group is marked required.
  await expect(color).toHaveAccessibleDescription("Choose Color.");
  await expect(page.getByText("Color (required)")).toBeVisible();

  // Choosing Red swaps in its photo; the alt names the option.
  await color.selectOption({ label: "Red (+$5.00)" });
  // The errors clear as soon as the problem is fixed, not on the next submit.
  await expect(colorError).toHaveCount(0);
  await expect(page.locator(".error-summary")).toHaveCount(0);
  await expect(color).not.toHaveAttribute("aria-invalid", "true");
  const photo = page.locator("img[data-option-photo]");
  await expect(photo).toHaveAttribute("alt", "Color: Red");
  const redSrc = await photo.getAttribute("src");
  expect(redSrc).toMatch(/^\/item-images\/[0-9a-f]{16}\.png$/);
  // Tap to enlarge: the photo opens in a dialog, and Escape closes it without leaving the item.
  await page.getByRole("button", { name: "Enlarge photo" }).click();
  await expect(page.getByRole("dialog", { name: /Photo of/ })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.getByRole("heading", { name: ITEM_NAME })).toBeVisible();
  await page.getByLabel("Quantity").fill(String(ORDER_QUANTITY));

  // The live total also carries a trailing " · $X.XX each" suffix, so use toContainText to pin only
  // the total.
  const totalLocator = page.getByRole("group", { name: "Total" });
  await expect(totalLocator).toContainText(EXPECTED_TOTAL);

  // The optional group adds to the sum, and its option has no photo, so Red's photo stays.
  await engraving.selectOption({ label: "Name plate (+$2.00)" });
  await expect(totalLocator).toContainText("$34.00");
  await expect(photo).toHaveAttribute("src", redSrc!);
  // Switching Color to Black (which has a photo) shows Black's, as the newest change.
  await color.selectOption({ label: "Black" });
  await expect(photo).toHaveAttribute("alt", "Color: Black");
  expect(await photo.getAttribute("src")).not.toBe(redSrc);
  await expect(totalLocator).toContainText("$24.00");

  // Back to Red, and None for the optional group: the order stays $30.00.
  await color.selectOption({ label: "Red (+$5.00)" });
  await engraving.selectOption({ label: "None" });
  await expect(totalLocator).toContainText(EXPECTED_TOTAL);
  await expect(photo).toHaveAttribute("src", redSrc!);

  await page.getByRole("button", { name: "Place order" }).click();

  await expect(page.getByText("Order received")).toBeVisible();
  await expect(page.locator(".confirmation .price")).toHaveText(EXPECTED_TOTAL);
  await expect(page.locator(".confirmation .order-summary")).toContainText("Color: Red");
  const refText = (await page.locator(".confirmation .ref").textContent())?.trim() ?? "";
  const refMatch = refText.match(/^Reference: (.+)$/);
  expect(refMatch).not.toBeNull();
  createdRequestId = await requestIdByReference(request, refMatch![1]!);
});

test("the order appears in the admin Requests list as an item order", async ({ page, request }) => {
  await page.goto("/admin/");
  await navButton(page, "Requests").click();
  await expect(page).toHaveURL(/#\/requests$/);

  const row = page.locator("tr.req-row", { hasText: CUSTOMER_NAME });
  await expect(row).toBeVisible();
  await expect(row.locator(".c-price")).toHaveText(EXPECTED_TOTAL);
  await expect(row.getByText("item order")).toBeVisible();

  // The pill is the UI check; Kind/ItemID/ItemName/OptionName live in the API response.
  const apiRes = await request.get(`/admin/api/requests/${createdRequestId}`);
  expect(apiRes.ok()).toBeTruthy();
  const stored = await apiRes.json();
  expect(stored.kind).toBe("item");
  expect(stored.itemId).toBe(createdItemId);
  expect(stored.itemName).toBe(ITEM_NAME);
  expect(stored.optionName).toBe("Color: Red");
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

  // Quantity's max reflects the lifted cap (shop.maxOrderQty, 100 by default), not the item's stockQty of 0.
  await expect(page.getByLabel("Quantity")).toHaveAttribute("max", "100");

  await page.getByLabel("Quantity").fill(String(BACKORDER_QUANTITY));
  await page.getByLabel("Name").fill(CUSTOMER_NAME);
  await page.getByLabel("Email").fill(CUSTOMER_EMAIL);
  await page.getByRole("button", { name: "Place order" }).click();

  await expect(page.getByText("Order received")).toBeVisible();
  const refText = (await page.locator(".confirmation .ref").textContent())?.trim() ?? "";
  const refMatch = refText.match(/^Reference: (.+)$/);
  expect(refMatch).not.toBeNull();
  createdBackorderRequestId = await requestIdByReference(request, refMatch![1]!);

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
  await expect(cta).toContainText("Upload your own 3D model and get a price estimate.");

  await cta.click();

  // The plain manual-quote form is the quote view's content.
  await expect(page.getByLabel("Material")).toBeVisible();
  await expect(page.getByRole("button", { name: "Request an estimate" })).toBeVisible();
});

// Admin: reorder groups and options with Move up / Move down; focus follows the moved row.

test("admin reorders option groups and options, and the order persists and reaches the public panel", async ({ page, request }) => {
  await page.goto("/admin/#/items");
  await page.locator("main").getByRole("button", { name: new RegExp(ITEM_NAME) }).first().click();
  await expect(page.getByLabel("Group 1 choice 1", { exact: true })).toHaveValue("Black");

  // Group 2 (Engraving) up from its ⋯ menu: it becomes group 1, and focus follows its ⋯ button.
  await page.getByRole("button", { name: "More actions for Option group 2: Engraving" }).click();
  await page.getByRole("button", { name: "Move up" }).click();
  await expect(page.getByRole("button", { name: "More actions for Option group 1: Engraving" })).toBeFocused();
  await expect(page.getByLabel("Group 1 choice 1", { exact: true })).toHaveValue("Name plate");

  // Color's second option (Red) up, now in group 2.
  await page.getByRole("button", { name: "More actions for Group 2 choice 2: Red" }).click();
  await page.getByRole("button", { name: "Move up" }).click();
  await expect(page.getByRole("button", { name: "More actions for Group 2 choice 1: Red" })).toBeFocused();
  await expect(page.getByLabel("Group 2 choice 1", { exact: true })).toHaveValue("Red");

  // Unsaved photos note: an unsaved new option explains photos upload after saving.
  await page.getByRole("button", { name: "Add choice" }).nth(1).click();
  await expect(page.getByText("Save the item first to add a photo.").first()).toBeVisible();
  await page.getByRole("button", { name: /^More actions for Group 2 choice 3: / }).click();
  await page.getByRole("button", { name: "Remove", exact: true }).click();

  await page.getByRole("button", { name: "Save" }).click();
  await expect(page.getByText(`${ITEM_NAME} saved.`)).toBeAttached();

  const res = await request.get("/admin/api/items");
  const item = ((await res.json()).items as { id: string; optionGroups: { label: string; required: boolean; options: { name: string; imageFile: string }[] }[] }[]).find(
    (i) => i.id === createdItemId,
  )!;
  expect(item.optionGroups.map((g) => g.label)).toEqual(["Engraving", "Color"]);
  expect(item.optionGroups[1]!.options.map((o) => o.name)).toEqual(["Red", "Black"]);
  // Photos and ids ride along with their options through a reorder.
  expect(item.optionGroups[1]!.options.every((o) => /\.png$/.test(o.imageFile))).toBe(true);

  await expect
    .poll(
      async () => {
        const r = await request.get(`${PUBLIC_URL}api/public/items`);
        const it = ((await r.json()).items as { name: string; optionGroups: { label: string }[] }[]).find((i) => i.name === ITEM_NAME);
        return it?.optionGroups.map((g) => g.label).join(",");
      },
      { timeout: 15_000, intervals: [500, 500, 1000] },
    )
    .toBe("Engraving,Color");
  await page.goto(`${PUBLIC_URL}order`);
  await page.getByRole("button", { name: new RegExp(ITEM_NAME) }).click();
  await expect(page.getByRole("combobox").nth(0)).toHaveAccessibleName("Engraving (optional)");
  await expect(page.getByRole("combobox").nth(1)).toHaveAccessibleName("Color (required)");
});
