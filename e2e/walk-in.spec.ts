import { test, expect } from "@playwright/test";
import { fillCanonicalQuote, historyRow, quotesRow } from "./helpers";

// Walk-in customer: one name field on the Quote tab feeds the print customer
// line, the saved quote, and the converted invoice.

test("walk-in name persists through save, restore, and convert", async ({ page }) => {
  const stamp = Date.now();
  const label = `e2e-walkin-${stamp}`;
  const customer = `Walk-in Sam ${stamp}`;

  await page.goto("/admin/");
  await fillCanonicalQuote(page);
  await page.getByLabel("Set a selling price").check();
  await page.getByPlaceholder("Client name").fill(customer);
  await expect(page.getByLabel("Client for this quote")).toHaveValue(customer);

  await page.getByLabel("Label for this quote").fill(label);
  await page.getByRole("button", { name: "Save quote" }).click();
  await expect(historyRow(page, label)).toBeVisible();
  await expect(page.locator(".action-status")).toContainText(`"${label}" saved.`);

  await page.reload();
  // Opening over unsaved edits asks first; accept it.
  page.once("dialog", (d) => void d.accept());
  await historyRow(page, label).getByRole("button", { name: /^Open "/ }).click();
  await expect(page.getByLabel("Client for this quote")).toHaveValue(customer);
  await expect(page.locator(".linked-customer")).toHaveText(customer);

  await page.evaluate(() => { window.print = () => {}; });
  await page.getByRole("button", { name: "Print client quote" }).click();
  await expect(page.getByText(`For: ${customer}`)).toHaveCount(1);

  await page.goto("/admin/#/quotes");
  const row = quotesRow(page, label);
  await expect(row).toBeVisible();
  await row.getByRole("button", { name: `Convert "${label}" to invoice` }).click();
  await expect(page.getByLabel("Client for this invoice")).toHaveText(new RegExp(customer));
  await page.getByRole("button", { name: /^Create invoice/ }).click();
  await expect(page).toHaveURL(/#\/invoices/);
  const picked = page.getByRole("combobox").filter({ has: page.locator("option:checked", { hasText: customer }) });
  await expect(picked.first()).toBeVisible();
  await expect(picked.first().locator("option:checked")).toHaveText(customer);
});
