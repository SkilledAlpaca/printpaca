import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";
import { navButton } from "./helpers";

// Shares the suite's one server (workers 1); no other spec touches clients, so the table starts empty.

async function gotoClients(page: Page): Promise<void> {
  await page.goto("/admin/");
  await navButton(page, "Clients").click();
  await expect(page).toHaveURL(/#\/clients$/);
}

function clientRow(page: Page, name: string) {
  return page.locator("li", { hasText: name });
}

test("create, edit, and delete a client through the detail view", async ({ page }) => {
  await gotoClients(page);
  await expect(page.getByText("No clients yet.")).toBeVisible();

  await page.getByRole("button", { name: "Add client" }).click();
  await page.getByLabel("Name").fill("Jordan Alpaca");
  await page.getByLabel("Email").fill("jordan@example.com");
  await page.getByLabel("Phone").fill("555-0100");
  await page.getByRole("button", { name: "Save" }).click();

  // Saving a new client opens its detail view (#/clients#<id>) rather than returning to the list.
  await expect(page.getByRole("heading", { name: "Jordan Alpaca" })).toBeVisible();
  await expect(page).toHaveURL(/#\/clients#.+/);

  await page.getByRole("button", { name: "Back to list" }).click();
  await expect(page).toHaveURL(/#\/clients$/);
  const row = clientRow(page, "Jordan Alpaca");
  await expect(row).toBeVisible();
  await expect(row).toContainText("555-0100");

  await row.getByRole("button").click();
  await expect(page.getByRole("heading", { name: "Jordan Alpaca" })).toBeVisible();
  await page.getByLabel("Phone").fill("555-0199");
  await page.getByRole("button", { name: "Save" }).click();
  await expect(page.getByLabel("Phone")).toHaveValue("555-0199");

  // The detail view names next actions in its empty Quotes/Invoices sections.
  await expect(page.getByText("No saved quotes are linked to this client yet.")).toBeVisible();
  await expect(page.getByText("No invoices for this client yet.")).toBeVisible();

  await page.getByRole("button", { name: "Delete client" }).click();
  await expect(page.getByText("Invoices keep their own copy of this client's details.")).toBeVisible();
  await page.getByRole("button", { name: "Delete client?" }).click();

  await expect(page).toHaveURL(/#\/clients$/);
  await expect(page.getByText("No clients yet.")).toBeVisible();
});
