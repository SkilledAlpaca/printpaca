import { test, expect } from "@playwright/test";
import { navButton } from "./helpers";

// The public listener runs on its own port, so its URLs are absolute (see public.spec.ts).
const PUBLIC_URL = "http://127.0.0.1:8098/";
const ITEM_NAME = "Top Items Test Gizmo";
const BUYER_NAME = "Top Items Buyer";
const QUANTITY = 3;
const UNIT_CENTS = 1200;
const EXPECTED_TOTAL = "$36.00"; // 3 x $12.00

let itemId: string | null = null;

test.describe.configure({ mode: "serial" });

test.afterAll(async ({ request }) => {
  // Deleting the item is safe: item_sales keeps the sale, which then lists as removed.
  if (itemId) await request.delete(`/admin/api/items/${itemId}`);
});

test("a finalized gallery order is listed in Top items and survives deleting the request", async ({ page, request }) => {
  const createRes = await request.post("/admin/api/items", {
    data: { name: ITEM_NAME, priceCents: UNIT_CENTS, public: true, stockMode: "ready_to_print", sortOrder: 0 },
  });
  expect(createRes.ok()).toBeTruthy();
  itemId = (await createRes.json()).id;

  const orderRes = await request.post(`${PUBLIC_URL}api/public/item-order`, {
    data: { itemId, optionName: "", quantity: QUANTITY, name: BUYER_NAME, email: "top-items@example.com", note: "" },
  });
  expect(orderRes.status()).toBe(201);

  // Finalize it in the Requests tab: opening the row marks it viewed, then Finalize records the sale.
  await page.goto("/admin/");
  await navButton(page, "Requests").click();
  await page.locator(".plain-list li", { hasText: BUYER_NAME }).getByRole("button").first().click();
  await page.getByRole("button", { name: "Finalize", exact: true }).click();
  await expect(page.getByRole("button", { name: "Finalize", exact: true })).toHaveCount(0);

  const topItemsRow = () => page.locator(".top-items tbody tr", { hasText: ITEM_NAME });
  const openDashboard = async () => {
    await navButton(page, "Dashboard").click();
    await expect(page.getByRole("heading", { name: "Top items" })).toBeVisible();
  };

  await openDashboard();
  await expect(topItemsRow()).toBeVisible();
  // Columns: Rank, Item, Units, Orders, Booked.
  await expect(topItemsRow().locator("td").nth(2)).toHaveText(String(QUANTITY));
  await expect(topItemsRow().locator("td").nth(3)).toHaveText("1");
  await expect(topItemsRow().locator("td").nth(4)).toHaveText(EXPECTED_TOTAL);

  // Delete the request. The sale stays.
  await navButton(page, "Requests").click();
  await page.locator(".plain-list li", { hasText: BUYER_NAME }).getByRole("button").first().click();
  await page.getByRole("button", { name: "Delete this request" }).click();
  await page.getByRole("button", { name: "Delete? Also deletes the stored file." }).click();
  await expect(page.locator(".plain-list li", { hasText: BUYER_NAME })).toHaveCount(0);

  await openDashboard();
  await expect(topItemsRow()).toBeVisible();
  await expect(topItemsRow().locator("td").nth(2)).toHaveText(String(QUANTITY));
});

const COUNTER_ITEM = "Counter Sale Gizmo";

test("a counter sale on a sent invoice is counted and takes stock; canceling restores both", async ({ page, request }) => {
  const createRes = await request.post("/admin/api/items", {
    data: {
      name: COUNTER_ITEM,
      priceCents: 2000,
      optionLabel: "Color",
      options: [{ name: "Red", deltaCents: 0 }, { name: "Gold", deltaCents: 500 }],
      public: false,
      stockMode: "in_stock",
      stockQty: 5,
      sortOrder: 0,
    },
  });
  expect(createRes.ok()).toBeTruthy();
  const counterItemId: string = (await createRes.json()).id;
  const stockQty = async (): Promise<number> => {
    const res = await request.get("/admin/api/items");
    const items = (await res.json()).items as { id: string; stockQty: number }[];
    return items.find((i) => i.id === counterItemId)!.stockQty;
  };

  try {
    await page.goto("/admin/");
    await navButton(page, "Invoices").click();
    await page.getByRole("button", { name: "New invoice" }).click();
    await page.getByLabel("Client name").fill("Counter Sale Customer");
    await page.getByRole("button", { name: "Add line" }).click();
    await page.getByLabel("Line 1 gallery item").selectOption({ label: COUNTER_ITEM });
    await page.getByLabel("Line 1 Color").selectOption("Gold");
    await expect(page.getByLabel("Line 1 description")).toHaveValue(`${COUNTER_ITEM} (Gold)`);
    await expect(page.getByLabel("Line 1 unit price")).toHaveValue("25");
    await page.getByLabel("Line 1 quantity").fill("2");
    await page.getByRole("button", { name: "Save" }).click();
    await expect(page.getByRole("heading", { name: /^Invoice INV-/ })).toBeVisible();

    // A draft reserves nothing.
    expect(await stockQty()).toBe(5);

    await page.getByRole("button", { name: "Mark sent" }).click();
    await expect(page.getByText("sent", { exact: true })).toBeVisible();
    expect(await stockQty()).toBe(3);

    const row = () => page.locator(".top-items tbody tr", { hasText: COUNTER_ITEM });
    const openDashboard = async () => {
      await navButton(page, "Dashboard").click();
      await expect(page.getByRole("heading", { name: "Top items" })).toBeVisible();
    };
    await openDashboard();
    await expect(row()).toBeVisible();
    await expect(row().locator("td").nth(2)).toHaveText("2");
    await expect(row().locator("td").nth(4)).toHaveText("$50.00");
    await expect(page.locator(".top-items-summary", { hasText: "in person" })).toContainText("2 in person");

    await navButton(page, "Invoices").click();
    await page.locator(".clickable-row", { hasText: "Counter Sale Customer" }).first().click();
    await page.getByRole("button", { name: "Cancel invoice", exact: true }).click();
    await page.getByRole("button", { name: "Cancel invoice?", exact: true }).click();
    await expect(page.getByText("canceled", { exact: true })).toBeVisible();
    expect(await stockQty()).toBe(5);

    await openDashboard();
    await expect(row()).toHaveCount(0);
  } finally {
    await request.delete(`/admin/api/items/${counterItemId}`);
  }
});
