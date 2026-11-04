import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";
import { navButton } from "./helpers";

// Shares the suite's one server and creates its own freeform invoice. It adds a payment to the shared
// database, so the Dashboard assertion only checks that the revenue tile is nonzero.

async function gotoInvoices(page: Page): Promise<void> {
  await page.goto("/admin/");
  await navButton(page, "Invoices").click();
  await expect(page).toHaveURL(/#\/invoices$/);
}

test("record a full payment on a sent invoice and see it reflected on the Dashboard", async ({ page }) => {
  await gotoInvoices(page);

  await page.getByRole("button", { name: "New invoice" }).click();
  await page.getByLabel("Client name").fill("Payments Test Customer");

  await page.getByRole("button", { name: "Add line" }).click();
  await page.getByLabel("Line 1 description").fill("Custom bracket");
  await page.getByLabel("Line 1 quantity").fill("1");
  await page.getByLabel("Line 1 unit price").fill("40.00");

  await page.getByRole("button", { name: "Save" }).click();
  await expect(page.getByRole("heading", { name: /^Invoice INV-/ })).toBeVisible();

  await page.getByRole("button", { name: "Mark sent" }).click();
  await page.getByRole("button", { name: "Yes, mark as sent" }).click();
  await expect(page.getByText("sent", { exact: true })).toBeVisible();

  // The form defaults to today and the open balance ($40.00), so recording as-is fully pays the invoice.
  await page.getByLabel("Payment reference").fill("Square #4821");
  await page.getByRole("button", { name: /^Record \$/ }).click();

  await expect(page.getByText("paid", { exact: true })).toBeVisible();
  await expect(page.getByText(/Paid \$40\.00 of \$40\.00, balance \$0\.00/)).toBeVisible();
  await expect(page.getByRole("cell", { name: "Square #4821" })).toBeVisible();

  await navButton(page, "Dashboard").click();
  await expect(page).toHaveURL(/#\/dashboard$/);

  const monthTile = page.locator(".tile", { has: page.getByText("Revenue this month") });
  await expect(monthTile.locator(".tile-value")).toBeVisible();
  await expect(monthTile.locator(".tile-value")).not.toHaveText("$0.00");
});
