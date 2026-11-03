import { test, expect } from "@playwright/test";
import type { Locator, Page } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { navButton } from "./helpers";

// Spoolman inventory card on the Filament tab (v1.1 item 11), against the stateful stub in
// scripts/e2e-spoolman-stub.mjs. 9199 sets SPOOLMAN_URL, so the proxy under test is the real one in
// server/spoolman_guard.go. The stub keeps state between tests, so the tests run in order.

test.use({ baseURL: "http://127.0.0.1:9199" });
test.describe.configure({ mode: "serial" });

async function openInventory(page: Page): Promise<Locator> {
  await page.goto("/admin/");
  await navButton(page, "Filament").click();
  const card = page.locator("#spoolman-inventory");
  await expect(card.getByRole("heading", { name: "Spoolman inventory" })).toBeVisible();
  return card;
}

function row(card: Locator, id: number): Locator {
  return card.locator(`tr[data-spool-id="${id}"]`);
}

test("lists active spools, hides archived ones until asked, and filters", async ({ page }) => {
  const card = await openInventory(page);
  await expect(row(card, 1)).toContainText("Prusament Galaxy Black");
  await expect(row(card, 1)).toContainText("PLA");
  await expect(row(card, 1)).toContainText("850");
  await expect(row(card, 1)).toContainText("$24.99");
  await expect(row(card, 1)).toContainText("Shelf A");
  await expect(row(card, 1)).toContainText("#1A1A1A");
  await expect(row(card, 2)).toBeVisible();
  await expect(row(card, 3)).toHaveCount(0);

  await card.getByLabel("Show archived").check();
  await expect(row(card, 3)).toContainText("Archived");

  await card.getByLabel("Filter spools").fill("petg");
  await expect(row(card, 2)).toBeVisible();
  await expect(row(card, 1)).toHaveCount(0);
  await expect(row(card, 3)).toHaveCount(0);
});

test("set remaining weight updates the row and announces it", async ({ page }) => {
  const card = await openInventory(page);
  await row(card, 1).getByRole("button", { name: "Set remaining weight for spool 1" }).click();
  const input = card.getByLabel("Remaining weight (g)");
  await expect(input).toBeFocused();
  await input.fill("700");
  await card.getByRole("button", { name: "Save weight" }).click();
  await expect(row(card, 1)).toContainText("700");
  await expect(page.getByText("Spool #1 now has 700 g remaining.")).toBeVisible();
  // Focus returns to the control that opened the form.
  await expect(row(card, 1).getByRole("button", { name: "Set remaining weight for spool 1" })).toBeFocused();
});

test("edit location and price", async ({ page }) => {
  const card = await openInventory(page);
  await row(card, 2).getByRole("button", { name: "Edit location and price for spool 2" }).click();
  await card.getByLabel("Location", { exact: true }).fill("Rack 4");
  await card.getByLabel("Price ($)", { exact: true }).fill("18.25");
  await card.getByRole("button", { name: "Save details" }).click();
  await expect(row(card, 2)).toContainText("Rack 4");
  await expect(row(card, 2)).toContainText("$18.25");
});

test("clearing the price removes it from the spool", async ({ page }) => {
  const card = await openInventory(page);
  await row(card, 2).getByRole("button", { name: "Edit location and price for spool 2" }).click();
  await card.getByLabel("Price ($)", { exact: true }).fill("");
  await card.getByRole("button", { name: "Save details" }).click();
  await expect(page.getByText("Updated spool #2.")).toBeVisible();
  await expect(row(card, 2)).not.toContainText("$18.25");
  await openInventory(page).then((c) => expect(row(c, 2)).not.toContainText("$18.25"));
});

test("add spool for an existing filament", async ({ page }) => {
  const card = await openInventory(page);
  await card.getByRole("button", { name: "Add spool" }).click();
  const select = card.getByLabel("Filament", { exact: true });
  await expect(select).toBeFocused();
  await select.selectOption({ label: "Polymaker Lava Red (PLA)" });
  // The filament's own weight and price seed the form.
  await expect(card.getByLabel("Initial weight (g)", { exact: true })).toHaveValue("750");
  await expect(card.getByLabel("Price ($)", { exact: true })).toHaveValue("19.99");
  await card.getByLabel("Initial weight (g)", { exact: true }).fill("800");
  await card.getByLabel("Location", { exact: true }).fill("Rack 9");
  await card.getByRole("form", { name: "Add spool" }).getByRole("button", { name: "Add spool" }).click();
  const added = card.locator("tr", { hasText: "Rack 9" });
  await expect(added).toContainText("Polymaker Lava Red");
  await expect(added).toContainText("800");
});

test("add spool requires a filament", async ({ page }) => {
  const card = await openInventory(page);
  await card.getByRole("button", { name: "Add spool" }).click();
  await card.getByRole("form", { name: "Add spool" }).getByRole("button", { name: "Add spool" }).click();
  await expect(card.getByRole("alert").filter({ hasText: "Pick a filament first." })).toBeVisible();
  await card.getByRole("button", { name: "Cancel" }).click();
  await expect(card.getByRole("button", { name: "Add spool" })).toBeFocused();
});

test("archive asks first, hides the spool, and unarchive brings it back", async ({ page }) => {
  const card = await openInventory(page);
  await row(card, 2).getByRole("button", { name: "Archive spool 2" }).click();
  const confirm = card.getByRole("button", { name: "Archive spool 2?" });
  await expect(confirm).toBeFocused();

  // Cancel leaves the spool alone.
  await card.getByRole("button", { name: "Cancel" }).click();
  await expect(row(card, 2)).toContainText("Active");

  await row(card, 2).getByRole("button", { name: "Archive spool 2" }).click();
  await confirm.click();
  await expect(row(card, 2)).toHaveCount(0);
  await expect(page.getByText(/Archived spool #2/)).toBeVisible();

  await card.getByLabel("Show archived").check();
  await expect(row(card, 2)).toContainText("Archived");
  await row(card, 2).getByRole("button", { name: "Unarchive spool 2" }).click();
  await expect(row(card, 2)).toContainText("Active");
});

test("the browser cannot reach other Spoolman paths or methods through the proxy", async ({ request }) => {
  const base = "http://127.0.0.1:9199/admin/api/spoolman/api/v1";
  expect((await request.get(`${base}/health`)).status()).toBe(403);
  expect((await request.get(`${base}/spool/1/use`)).status()).toBe(403);
  expect((await request.delete(`${base}/spool/1`)).status()).toBe(405);
  expect((await request.post(`${base}/vendor`, { data: { name: "x" } })).status()).toBe(405);
  // A field outside the allowlist never reaches Spoolman.
  const bad = await request.patch(`${base}/spool/1`, { data: { used_weight: 5 } });
  expect(bad.status()).toBe(400);
});

test("axe: no WCAG 2.2 AA violations on the inventory, light and dark, desktop and 390 px", async ({ page }) => {
  const card = await openInventory(page);
  await card.getByLabel("Show archived").check();
  await row(card, 1).getByRole("button", { name: "Set remaining weight for spool 1" }).click();
  const tags = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"];
  for (const size of [{ width: 1280, height: 900 }, { width: 390, height: 844 }]) {
    await page.setViewportSize(size);
    for (const scheme of ["light", "dark"] as const) {
      await page.emulateMedia({ colorScheme: scheme });
      await page.waitForLoadState("networkidle");
      await page.evaluate(() => document.fonts.ready);
      const results = await new AxeBuilder({ page }).withTags(tags).analyze();
      expect(
        results.violations.map((v) => `${v.id}: ${v.nodes[0]?.target.join(" ")}`),
        `${scheme} ${size.width}px`,
      ).toEqual([]);
    }
  }
});
