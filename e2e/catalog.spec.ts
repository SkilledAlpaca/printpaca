import { test, expect } from "@playwright/test";
import type { APIRequestContext, Locator, Page } from "@playwright/test";

// v1.2 catalog lane: the Shop items list and editor, the categories manager, and RowEditor's ⋯ menu (keyboard
// open, arrow keys, Escape and focus return, moving rows, and deleting the row that held focus).
// Everything it creates is deleted again: storefront-core.spec.ts expects a shop with no categories.

const TAG = "Catlane";
const createdItems: string[] = [];
const createdCategories: string[] = [];

type Json = Record<string, unknown>;

async function apiCategories(request: APIRequestContext): Promise<{ id: string; name: string; slug: string; sortOrder: number; hidden: boolean; itemCount: number }[]> {
  return ((await (await request.get("/admin/api/categories")).json()) as { categories: never[] }).categories;
}

async function makeCategory(request: APIRequestContext, name: string, extra: Json = {}): Promise<string> {
  const res = await request.post("/admin/api/categories", { data: { name, slug: "", sortOrder: 0, hidden: false, ...extra } });
  expect(res.ok()).toBeTruthy();
  const id = ((await res.json()) as { id: string }).id;
  createdCategories.push(id);
  return id;
}

async function makeItem(request: APIRequestContext, name: string, extra: Json = {}): Promise<string> {
  const res = await request.post("/admin/api/items", {
    data: { name, description: "", priceCents: 500, optionGroups: [], public: false, stockMode: "ready_to_print", stockQty: 0, sortOrder: 900, ...extra },
  });
  expect(res.ok()).toBeTruthy();
  const id = ((await res.json()) as { id: string }).id;
  createdItems.push(id);
  return id;
}

test.afterAll(async ({ request }) => {
  for (const id of createdItems) await request.delete(`/admin/api/items/${id}`);
  for (const id of createdCategories) await request.delete(`/admin/api/categories/${id}`);
});

const trigger = (page: Page, row: string | RegExp): Locator =>
  page.getByRole("button", { name: typeof row === "string" ? `More actions for ${row}` : row });

test.describe("categories manager", () => {
  test("add with a generated slug, edit the slug, rename, hide, and reorder from the keyboard", async ({ page, request }) => {
    await page.goto("/admin/#/items");
    const card = page.locator(".card", { has: page.getByRole("heading", { name: "Categories" }) });
    await expect(card).toBeVisible();

    // The slug follows the name until you type in it.
    const form = card.getByRole("form", { name: "Add a category" });
    await form.getByLabel("Name", { exact: true }).fill(`${TAG} Gifts & Cards`);
    await expect(form.getByLabel("Address name (slug)")).toHaveValue("catlane-gifts-cards");
    await form.getByLabel("Address name (slug)").fill("catlane-gift-ideas");
    await form.getByLabel("Name", { exact: true }).fill(`${TAG} Gifts`);
    await expect(form.getByLabel("Address name (slug)")).toHaveValue("catlane-gift-ideas");
    await form.getByRole("button", { name: "Add category" }).click();
    await expect(card.getByText(`Added ${TAG} Gifts.`)).toBeVisible();

    await form.getByLabel("Name", { exact: true }).fill(`${TAG} Toys`);
    await form.getByRole("button", { name: "Add category" }).click();
    await expect(card.getByText(`Added ${TAG} Toys.`)).toBeVisible();

    let cats = (await apiCategories(request)).filter((c) => c.name.startsWith(TAG));
    createdCategories.push(...cats.map((c) => c.id));
    expect(cats.map((c) => [c.name, c.slug])).toEqual([
      [`${TAG} Gifts`, "catlane-gift-ideas"],
      [`${TAG} Toys`, "catlane-toys"],
    ]);

    // Rename on blur; the slug stays put. The inputs are found by id: a row's accessible name follows the name as you type.
    const [gifts, toys] = cats;
    const nameOf = (id: string) => card.locator(`#cat-name-${id}`);
    await nameOf(gifts!.id).fill(`${TAG} Presents`);
    await nameOf(gifts!.id).press("Tab");
    await expect(card.getByText(`Saved ${TAG} Presents.`)).toBeVisible();
    cats = (await apiCategories(request)).filter((c) => c.name.startsWith(TAG));
    expect(cats.map((c) => [c.name, c.slug])[0]).toEqual([`${TAG} Presents`, "catlane-gift-ideas"]);

    // A duplicate name is refused next to the field, and the stored name is untouched.
    const presentsRow = card.getByRole("group", { name: new RegExp(`^Category \\d+: ${TAG} Presents$`) });
    await nameOf(gifts!.id).fill(`${TAG} Toys`);
    await nameOf(gifts!.id).press("Tab");
    await expect(card.getByText(/another category already has this name/i)).toBeVisible();
    await expect(nameOf(gifts!.id)).toHaveAttribute("aria-invalid", "true");
    expect((await apiCategories(request)).find((c) => c.id === gifts!.id)?.name).toBe(`${TAG} Presents`);
    await nameOf(gifts!.id).fill(`${TAG} Presents`);
    await nameOf(gifts!.id).press("Tab");
    await expect(card.getByText(/another category already has this name/i)).toHaveCount(0);
    await expect(presentsRow).toBeVisible();

    // Hide.
    const toysRow = card.getByRole("group", { name: new RegExp(`^Category \\d+: ${TAG} Toys$`) });
    await toysRow.getByLabel("Hidden from the shop").check();
    await expect(card.getByText(`Saved ${TAG} Toys.`)).toBeVisible();
    expect((await apiCategories(request)).find((c) => c.id === toys!.id)?.hidden).toBe(true);

    // Reorder with the keyboard: Toys up. Focus returns to Toys' ⋯ button, which moved with the row.
    const toysTrigger = trigger(page, new RegExp(`^More actions for Category \\d+: ${TAG} Toys$`));
    await toysTrigger.focus();
    await page.keyboard.press("Enter");
    await expect(page.getByRole("button", { name: "Move up" })).toBeFocused();
    await page.keyboard.press("Enter");
    await expect(toysTrigger).toBeFocused();
    await expect.poll(async () => (await apiCategories(request)).filter((c) => c.name.startsWith(TAG)).map((c) => c.name)).toEqual([`${TAG} Toys`, `${TAG} Presents`]);
    await expect(page.getByRole("group", { name: `Category ${((await apiCategories(request)).findIndex((c) => c.name === `${TAG} Toys`)) + 1}: ${TAG} Toys` })).toBeVisible();
  });

  test("deleting a category that items use asks first, and the items keep no category", async ({ page, request }) => {
    const catId = await makeCategory(request, `${TAG} Doomed`);
    const itemId = await makeItem(request, `${TAG} Item in doomed`, { categoryId: catId });
    await makeItem(request, `${TAG} Item in doomed 2`, { categoryId: catId });
    await page.goto("/admin/#/items");
    const card = page.locator(".card", { has: page.getByRole("heading", { name: "Categories" }) });
    const row = card.getByRole("group", { name: new RegExp(`^Category \\d+: ${TAG} Doomed$`) });
    await expect(row.getByText("2 items")).toBeVisible();

    const rowTrigger = trigger(page, new RegExp(`^More actions for Category \\d+: ${TAG} Doomed$`));
    await rowTrigger.focus();
    await page.keyboard.press("Enter");
    await page.getByRole("button", { name: "Delete" }).click();
    // The confirmation names the verb, the object and the count, and says what happens to the items.
    const confirm = row.getByRole("button", { name: `Yes, delete ${TAG} Doomed` });
    await expect(confirm).toBeFocused();
    await expect(row).toContainText("2 items use");
    await expect(row).toContainText("will have no category");
    // Escape cancels and returns focus to the ⋯ button; nothing was deleted.
    await page.keyboard.press("Escape");
    await expect(confirm).toHaveCount(0);
    await expect(rowTrigger).toBeFocused();
    expect((await apiCategories(request)).some((c) => c.id === catId)).toBe(true);

    await page.keyboard.press("Enter");
    await page.getByRole("button", { name: "Delete" }).click();
    await row.getByRole("button", { name: `Yes, delete ${TAG} Doomed` }).click();
    await expect(row).toHaveCount(0);
    expect((await apiCategories(request)).some((c) => c.id === catId)).toBe(false);
    const items = ((await (await request.get("/admin/api/items")).json()) as { items: { id: string; categoryId: string }[] }).items;
    expect(items.find((i) => i.id === itemId)?.categoryId).toBe("");
    // Focus went somewhere that still exists, not <body>.
    await expect(page.locator("body")).not.toBeFocused();
    expect(await page.evaluate(() => document.activeElement?.tagName)).not.toBe("BODY");
  });
});

test.describe("item editor", () => {
  test("Category and Featured save and list", async ({ page, request }) => {
    const catId = await makeCategory(request, `${TAG} Shelf`);
    await page.goto("/admin/#/items");
    await page.getByRole("button", { name: "Add shop item" }).click();
    await page.getByLabel("Name", { exact: true }).fill(`${TAG} Shelf bracket`);
    await expect(page.getByLabel("Category", { exact: true })).toHaveValue("");
    await expect(page.getByLabel("Category", { exact: true }).locator("option").first()).toHaveText("None");
    await page.getByLabel("Category", { exact: true }).selectOption({ label: `${TAG} Shelf` });
    await page.getByLabel("Featured (shown first in its category and in All)").check();
    await page.getByRole("button", { name: "Save", exact: true }).click();
    await expect(page.getByTestId("item-saved")).toContainText("Saved.");
    const saved = ((await (await request.get("/admin/api/items")).json()) as { items: { id: string; name: string; categoryId: string; featured: boolean }[] }).items.find((i) => i.name === `${TAG} Shelf bracket`);
    expect(saved).toBeTruthy();
    createdItems.push(saved!.id);
    expect(saved!.categoryId).toBe(catId);
    expect(saved!.featured).toBe(true);

    // The list shows the category and a Featured marker; saving the list's Public toggle keeps both.
    await page.getByRole("button", { name: "Back to list" }).click();
    const row = page.getByRole("region", { name: "Shop items" }).locator("tr", { hasText: `${TAG} Shelf bracket` });
    await expect(row).toContainText(`${TAG} Shelf`);
    await expect(row).toContainText("Featured");
    await row.getByRole("checkbox", { name: /^Public/ }).check();
    await expect.poll(async () => ((await (await request.get("/admin/api/items")).json()) as { items: { id: string; public: boolean; categoryId: string; featured: boolean }[] }).items.find((i) => i.id === saved!.id)).toMatchObject({ public: true, categoryId: catId, featured: true });

    // Clearing the category with None saves too.
    await row.locator("button.row-open").click();
    await page.getByLabel("Category", { exact: true }).selectOption("");
    await page.getByLabel("Description").press("Control+s");
    await expect(page.getByTestId("item-saved")).toContainText("Saved.");
    const after = ((await (await request.get("/admin/api/items")).json()) as { items: { id: string; categoryId: string }[] }).items.find((i) => i.id === saved!.id);
    expect(after?.categoryId).toBe("");
  });

  test("the ⋯ menu: keyboard open, arrow keys, Escape, moving rows, and deleting the focused row", async ({ page, request }) => {
    const id = await makeItem(request, `${TAG} Options`, {
      optionGroups: [
        { label: "Color", required: true, options: [{ name: "Red", deltaCents: 0 }, { name: "Blue", deltaCents: 0 }] },
        { label: "Size", required: false, options: [{ name: "Small", deltaCents: 0 }] },
        { label: "Finish", required: false, options: [{ name: "Matte", deltaCents: 0 }] },
      ],
    });
    await page.goto("/admin/#/items");
    await page.getByRole("button", { name: new RegExp(`${TAG} Options`) }).first().click();
    const names = () => page.getByLabel("Group name").evaluateAll((els) => els.map((e) => (e as HTMLInputElement).value));
    expect(await names()).toEqual(["Color", "Size", "Finish"]);

    // Opens from the keyboard and focuses the first item; the panel is a named group.
    const sizeTrigger = trigger(page, "Option group 2: Size");
    await expect(sizeTrigger).toHaveAttribute("aria-expanded", "false");
    await sizeTrigger.focus();
    await page.keyboard.press("Enter");
    await expect(sizeTrigger).toHaveAttribute("aria-expanded", "true");
    const panel = page.getByRole("group", { name: "More actions for Option group 2: Size" });
    await expect(panel.getByRole("button")).toHaveText(["Move up", "Move down", "Remove group"]);
    await expect(panel.getByRole("button", { name: "Move up" })).toBeFocused();
    await page.keyboard.press("ArrowDown");
    await expect(panel.getByRole("button", { name: "Move down" })).toBeFocused();
    await page.keyboard.press("End");
    await expect(panel.getByRole("button", { name: "Remove group" })).toBeFocused();
    await page.keyboard.press("ArrowDown");
    await expect(panel.getByRole("button", { name: "Move up" })).toBeFocused();

    // Escape closes only the menu and puts focus back on the ⋯ button.
    await page.keyboard.press("Escape");
    await expect(panel).toHaveCount(0);
    await expect(sizeTrigger).toBeFocused();
    await expect(sizeTrigger).toHaveAttribute("aria-expanded", "false");
    // The record is still open: Escape didn't leave it.
    await expect(page.getByRole("heading", { level: 1, name: `${TAG} Options` })).toBeVisible();

    // Tab closes it too, and focus continues from the ⋯ button.
    await page.keyboard.press("Enter");
    await expect(panel).toBeVisible();
    await page.keyboard.press("Tab");
    await expect(panel).toHaveCount(0);

    // Move up from the keyboard: Size becomes group 1, and focus is on Size's ⋯ button under its new name.
    await sizeTrigger.focus();
    await page.keyboard.press("Enter");
    await page.keyboard.press("Enter");
    expect(await names()).toEqual(["Size", "Color", "Finish"]);
    await expect(trigger(page, "Option group 1: Size")).toBeFocused();
    // The first row cannot move up: the item is present but inert.
    await page.keyboard.press("Enter");
    await expect(page.getByRole("button", { name: "Move up" })).toHaveAttribute("aria-disabled", "true");
    await expect(page.getByRole("button", { name: "Move down" })).toBeFocused();
    await page.keyboard.press("Escape");

    // Delete the group that holds focus: focus lands on the ⋯ button of the row that took its place.
    await page.keyboard.press("Enter");
    await page.keyboard.press("End");
    await page.keyboard.press("Enter");
    expect(await names()).toEqual(["Color", "Finish"]);
    await expect(trigger(page, "Option group 1: Color")).toBeFocused();
    // Delete the last row: focus goes to the previous row's ⋯ button; with none left, to Add group.
    await trigger(page, "Option group 2: Finish").focus();
    await page.keyboard.press("Enter");
    await page.keyboard.press("End");
    await page.keyboard.press("Enter");
    await expect(trigger(page, "Option group 1: Color")).toBeFocused();
    await page.keyboard.press("Enter");
    await page.keyboard.press("End");
    await page.keyboard.press("Enter");
    await expect(page.getByRole("button", { name: "Add group" })).toBeFocused();

    // Nothing was saved by any of that.
    const stored = ((await (await request.get("/admin/api/items")).json()) as { items: { id: string; optionGroups: { label: string }[] }[] }).items.find((i) => i.id === id);
    expect(stored?.optionGroups.map((g) => g.label)).toEqual(["Color", "Size", "Finish"]);
  });

  test("choices move and delete the same way, and an outside click closes the menu", async ({ page, request }) => {
    await makeItem(request, `${TAG} Choices`, {
      optionGroups: [{ label: "Color", required: true, options: [{ name: "Red", deltaCents: 0 }, { name: "Blue", deltaCents: 0 }, { name: "Green", deltaCents: 0 }] }],
    });
    await page.goto("/admin/#/items");
    await page.getByRole("button", { name: new RegExp(`${TAG} Choices`) }).first().click();
    const choices = () => page.getByLabel(/^Group 1 choice \d+$/).evaluateAll((els) => els.map((e) => (e as HTMLInputElement).value));
    expect(await choices()).toEqual(["Red", "Blue", "Green"]);

    await trigger(page, "Group 1 choice 3: Green").focus();
    await page.keyboard.press("Enter");
    await page.getByRole("button", { name: "Move up" }).click();
    expect(await choices()).toEqual(["Red", "Green", "Blue"]);
    await expect(trigger(page, "Group 1 choice 2: Green")).toBeFocused();

    // Remove the middle choice with the keyboard.
    await page.keyboard.press("Enter");
    await page.keyboard.press("End");
    await page.keyboard.press("Enter");
    expect(await choices()).toEqual(["Red", "Blue"]);
    await expect(trigger(page, "Group 1 choice 2: Blue")).toBeFocused();

    // A click elsewhere closes an open menu.
    await trigger(page, "Group 1 choice 1: Red").click();
    await expect(page.getByRole("group", { name: "More actions for Group 1 choice 1: Red" })).toBeVisible();
    await page.getByRole("heading", { level: 1 }).click();
    await expect(page.getByRole("group", { name: "More actions for Group 1 choice 1: Red" })).toHaveCount(0);
  });
});

test.describe("phone layout", () => {
  test.use({ viewport: { width: 390, height: 844 } });

  test("Shop items list and editor stack with 44 px controls and no sideways scroll", async ({ page, request }) => {
    await makeItem(request, `${TAG} Phone`, { optionGroups: [{ label: "Color", required: true, options: [{ name: "Red", deltaCents: 0 }] }] });
    await page.goto("/admin/#/items");
    const region = page.getByRole("region", { name: "Shop items" });
    await expect(region).toBeVisible();
    await expect(region).toHaveAttribute("tabindex", "0");
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    // The categories card sits under the list and its controls reach 44 px.
    const add = page.getByRole("button", { name: "Add category" });
    await add.scrollIntoViewIfNeeded();
    expect((await add.boundingBox())!.height).toBeGreaterThanOrEqual(43.5);

    await page.getByRole("button", { name: new RegExp(`${TAG} Phone`) }).first().click();
    const tr = trigger(page, "Option group 1: Color");
    await tr.scrollIntoViewIfNeeded();
    const t = (await tr.boundingBox())!;
    expect(t.height).toBeGreaterThanOrEqual(43.5);
    expect(t.x + t.width).toBeLessThanOrEqual(390);
    // Stacked: the group's fields are as wide as the row, with the ⋯ button below them rather than beside.
    const field = (await page.getByLabel("Group name").boundingBox())!;
    expect(field.y + field.height).toBeLessThanOrEqual(t.y + 1);
    expect(field.width).toBeGreaterThan(390 * 0.6);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);

    // The open menu stays inside the viewport.
    await tr.click();
    const panel = (await page.getByRole("group", { name: "More actions for Option group 1: Color" }).boundingBox())!;
    expect(panel.x).toBeGreaterThanOrEqual(0);
    expect(panel.x + panel.width).toBeLessThanOrEqual(390);
    for (const b of await page.getByRole("group", { name: "More actions for Option group 1: Color" }).getByRole("button").all()) {
      expect((await b.boundingBox())!.height).toBeGreaterThanOrEqual(43.5);
    }
  });
});

test.describe("direct Remove", () => {
  test("a row whose only action is Remove shows a Remove button named after the row, and focus follows the removal", async ({ page }) => {
    await page.goto("/admin/#/printers");
    const add = page.getByRole("button", { name: "Add printer" });
    const rows = page.getByRole("group", { name: /^Printer \d+: / });
    await expect(rows.first()).toBeVisible();
    const n = await rows.count();

    // No ⋯ menu: the printer rows have nothing but Remove.
    await expect(page.getByRole("button", { name: /^More actions for Printer/ })).toHaveCount(0);

    await add.click();
    await add.click();
    await expect(rows).toHaveCount(n + 2);
    const first = page.getByRole("button", { name: `Remove Printer ${n + 1}: New printer` });
    await expect(first).toBeVisible();
    await expect(first).toHaveText("Remove");

    // Removing a middle row moves focus to the Remove button of the row that took its place.
    await first.click();
    await expect(rows).toHaveCount(n + 1);
    await expect(page.locator(`[data-focus-key="row-menu-printer-${n}"]`)).toBeFocused();

    // Removing the last row moves focus to the previous row's button.
    await page.locator(`[data-focus-key="row-menu-printer-${n}"]`).click();
    await expect(rows).toHaveCount(n);
    await expect(page.locator(`[data-focus-key="row-menu-printer-${n - 1}"]`)).toBeFocused();
  });
});
