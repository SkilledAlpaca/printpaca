import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";
import { navButton } from "./helpers";

// Shares the suite's one server. Only creates a freeform (no clientId) draft, so it is independent of
// clients.spec.ts.

async function gotoInvoices(page: Page): Promise<void> {
  await page.goto("/admin/");
  await navButton(page, "Invoices").click();
  await expect(page).toHaveURL(/#\/invoices$/);
}

test("create a draft invoice, mark it sent, then cancel it", async ({ page }) => {
  await gotoInvoices(page);

  await page.getByRole("button", { name: "New invoice" }).click();
  await page.getByLabel("Client name").fill("Walk-in Test Customer");

  await page.getByRole("button", { name: "Add line" }).click();
  await page.getByLabel("Line 1 description").fill("Custom bracket");
  await page.getByLabel("Line 1 quantity").fill("2");
  await page.getByLabel("Line 1 unit price").fill("12.50");

  // Live preview total (no tax configured by default): 2 x $12.50 = $25.00.
  await expect(page.getByText("$25.00").first()).toBeVisible();

  await page.getByRole("button", { name: "Save" }).click();
  await expect(page.getByRole("heading", { name: /^Invoice INV-/ })).toBeVisible();
  await expect(page.getByText("draft", { exact: true })).toBeVisible();

  await page.getByRole("button", { name: "Mark sent" }).click();
  await page.getByRole("button", { name: "Yes, mark as sent" }).click();
  await expect(page.getByText("sent", { exact: true })).toBeVisible();

  await page.getByRole("button", { name: "Cancel invoice", exact: true }).click();
  await page.getByRole("button", { name: "Yes, cancel invoice", exact: true }).click();
  await expect(page.getByText("canceled", { exact: true })).toBeVisible();
});

test("canceling an invoice that has payments shows the server's message until the payments are removed", async ({ page }) => {
  await gotoInvoices(page);

  await page.getByRole("button", { name: "New invoice" }).click();
  await page.getByLabel("Client name").fill("Deposit Customer");
  await page.getByRole("button", { name: "Add line" }).click();
  await page.getByLabel("Line 1 description").fill("Custom bracket");
  await page.getByLabel("Line 1 quantity").fill("1");
  await page.getByLabel("Line 1 unit price").fill("50.00");
  await page.getByRole("button", { name: "Save" }).click();
  await expect(page.getByRole("heading", { name: /^Invoice INV-/ })).toBeVisible();
  await page.getByRole("button", { name: "Mark sent" }).click();
  await page.getByRole("button", { name: "Yes, mark as sent" }).click();
  await expect(page.getByText("sent", { exact: true })).toBeVisible();

  // A $10.00 deposit leaves the invoice sent with a balance.
  await page.locator(".payments-card").getByLabel(/^Amount/).fill("10.00");
  await page.getByRole("button", { name: /^Record \$/ }).click();
  await expect(page.getByText(/Paid \$10\.00 of \$50\.00/)).toBeVisible();

  await page.getByRole("button", { name: "Cancel invoice" }).click();
  await page.getByRole("button", { name: "Yes, cancel invoice" }).click();
  await expect(page.getByRole("alert").filter({ hasText: /remove the payments/i })).toBeVisible();
  await expect(page.getByText("sent", { exact: true })).toBeVisible();

  // Removing the payment unblocks the cancel.
  await page.getByRole("button", { name: /^Delete payment of \$10\.00/ }).click();
  await page.getByRole("button", { name: /^Yes, delete payment of \$10\.00/ }).click();
  await expect(page.getByText(/No payments recorded yet/)).toBeVisible();
  await page.getByRole("button", { name: "Cancel invoice" }).click();
  await page.getByRole("button", { name: "Yes, cancel invoice" }).click();
  await expect(page.getByText("canceled", { exact: true })).toBeVisible();
});
