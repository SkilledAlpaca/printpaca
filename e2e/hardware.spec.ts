import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";
import { fillCanonicalQuote, gotoSettings, historyRow, navButton, openBreakdown, readMoney } from "./helpers";

// Shares the suite's one server. The catalog entry added here is inert for other specs, and the final
// step removes it so later specs see the hardware-free config.

async function saveSettingsChanges(page: Page): Promise<void> {
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByText("Settings saved.")).toBeVisible();
}

// The last-visited tab is restored on a hash-less load, so use the nav button, not page.goto("/admin/").
async function gotoQuoteEditor(page: Page): Promise<void> {
  await navButton(page, "New quote").click();
  await expect(page).not.toHaveURL(/#\/settings$/);
}

test("configure a hardware entry, attach it on a quote, and shift the breakdown/total", async ({ page }) => {
  // 1. Configure a $0.50/unit entry that is not lost on failure, so the total shifts by exactly
  // unitCost * qtyPerUnit.
  await gotoSettings(page);
  await page.getByRole("button", { name: "Add hardware" }).click();
  await page.getByLabel("Hardware 1 name").fill("e2e magnet");
  await page.getByLabel("Hardware 1 unit cost").fill("0.50");
  await saveSettingsChanges(page);

  // 2. Fill the canonical quote ($2.24).
  await gotoQuoteEditor(page);
  await openBreakdown(page);
  await fillCanonicalQuote(page);
  await expect(page.locator("tfoot")).toContainText("$2.24");

  // 3. Attach it at qty 2; the Hardware area only appears once the catalog is non-empty.
  await page.getByRole("button", { name: "Add hardware" }).click();
  await expect(page.getByLabel("Hardware 1 item")).toHaveValue("e2e magnet");
  await page.getByLabel("Hardware 1 quantity per unit").fill("2");

  // 4. The breakdown gains a named line and the total shifts by exactly $1.00.
  const breakdown = page.locator(".breakdown");
  await expect(breakdown).toContainText("e2e magnet");
  await expect(page.locator("tfoot")).toContainText("$3.24");
  await expect(page.locator("tfoot")).not.toContainText("$2.24");

  // 5. The attached row must survive the JSON save/restore round trip.
  const label = `e2e-hardware-${Date.now()}`;
  await page.getByLabel("Label for this quote").fill(label);
  await page.getByRole("button", { name: "Save quote" }).click();
  const row = historyRow(page, label);
  await expect(row).toBeVisible();

  await page.getByRole("button", { name: "Remove hardware row 1" }).click();
  await expect(page.locator("tfoot")).toContainText("$2.24");

  // Opening over unsaved edits asks first; accept it.

  page.once("dialog", (d) => void d.accept());

  await row.getByRole("button", { name: /^Open "/ }).click();
  await expect(page.getByLabel("Hardware 1 item")).toHaveValue("e2e magnet");
  await expect(page.getByLabel("Hardware 1 quantity per unit")).toHaveValue("2");
  await expect(page.locator("tfoot")).toContainText("$3.24");

  // 6. Cleanup: remove the catalog entry.
  await gotoSettings(page);
  await page.getByRole("button", { name: "Remove hardware e2e magnet" }).click();
  await saveSettingsChanges(page);
});

// The test above leaves lostOnFailure unchecked. With it checked, the default failure base
// (docs/formulas.md, "consumables") also includes the hardware, so both the hardware row and the
// Failure buffer row (default 5%) must move, not just the total.
test("a checked lostOnFailure hardware entry also grows the Failure buffer row", async ({ page }) => {
  await gotoSettings(page);
  await page.getByRole("button", { name: "Add hardware" }).click();
  await page.getByLabel("Hardware 1 name").fill("e2e lost magnet");
  await page.getByLabel("Hardware 1 unit cost").fill("1.00");
  await page.getByLabel("Hardware 1 lost on failed prints").check();
  await saveSettingsChanges(page);

  await gotoQuoteEditor(page);
  await openBreakdown(page);
  await fillCanonicalQuote(page);
  await expect(page.locator("tfoot")).toContainText("$2.24");

  const failureAmount = page.getByRole("table").locator("tr", { hasText: "Failure buffer" }).locator("td.num");
  const baselineFailure = await readMoney(failureAmount);

  // Qty 1: $1.00 hardware plus a 5% buffer on it ($0.05) takes $2.24 to $3.29.
  await page.getByRole("button", { name: "Add hardware" }).click();
  await expect(page.getByLabel("Hardware 1 item")).toHaveValue("e2e lost magnet");

  const breakdown = page.locator(".breakdown");
  await expect(breakdown).toContainText("e2e lost magnet");
  await expect(page.locator("tfoot")).toContainText("$3.29");
  await expect(page.locator("tfoot")).not.toContainText("$3.24"); // would be the unchecked-box shift

  // The Failure buffer line itself carries the increase: 1.00 x 1 x 5% = 0.05.
  await expect.poll(async () => readMoney(failureAmount)).toBeCloseTo(baselineFailure + 0.05, 2);

  // Cleanup: remove the catalog entry.
  await gotoSettings(page);
  await page.getByRole("button", { name: "Remove hardware e2e lost magnet" }).click();
  await saveSettingsChanges(page);
});
