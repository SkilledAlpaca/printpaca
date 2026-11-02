import { test, expect } from "@playwright/test";
import { fillCanonicalQuote, historyRow, quotesRow } from "./helpers";

// All specs share one server-backed database and quotes persist per record, so each test cleans up
// after itself where it matters.

test("save, restore round-trip, and persists across reload", async ({ page }) => {
  const label = `e2e-roundtrip-${Date.now()}`;

  await page.goto("/admin/");
  await fillCanonicalQuote(page);
  await expect(page.locator("tfoot")).toContainText("$2.24");

  await page.getByLabel("Label for this quote").fill(label);
  await page.getByRole("button", { name: "Save quote" }).click();
  // The just-saved quote is newest, so it is always in the 5-item recent-saves strip.
  const row = historyRow(page, label);
  await expect(row).toBeVisible();

  // Perturb an obvious field (weight) and a non-obvious one (quantity); restore must revert both.
  await page.getByLabel(/Model weight/).fill("999");
  await page.getByLabel(/^Quantity/).fill("5");
  await expect(page.locator("tfoot")).not.toContainText("$2.24");

  // Opening over unsaved edits asks first; accept it.

  page.once("dialog", (d) => void d.accept());

  await row.getByRole("button", { name: /^Open "/ }).click();

  await expect(page.locator("tfoot")).toContainText("$2.24");
  await expect(page.getByLabel(/^Quantity/)).toHaveValue("1");

  // The save's POST landed before the row appeared, so reloading and re-fetching proves persistence.
  await page.reload();
  await expect(historyRow(page, label)).toBeVisible();
});

test("delete requires two-step confirmation and stays removed after reload", async ({ page }) => {
  const label = `e2e-delete-${Date.now()}`;

  await page.goto("/admin/");
  await fillCanonicalQuote(page);
  await page.getByLabel("Label for this quote").fill(label);
  await page.getByRole("button", { name: "Save quote" }).click();
  await expect(historyRow(page, label)).toBeVisible();
  // The row appears before the POST settles; wait for the settled status so delete can't race the write.
  await expect(page.locator(".action-status")).toContainText(`"${label}" saved.`);

  // Delete lives on the Quotes ledger page.
  await page.goto("/admin/#/quotes");
  const row = quotesRow(page, label);
  await expect(row).toBeVisible();

  // Delete is in the row's overflow menu. Its items are plain buttons in a labeled group, not an ARIA
  // menu, because arrow-key roving isn't implemented.
  await row.getByRole("button", { name: `More actions for "${label}"` }).click();
  await row.getByRole("button", { name: `Delete "${label}"` }).click();
  const confirmBtn = row.getByRole("button", { name: `Confirm delete "${label}"` });
  await expect(confirmBtn).toBeVisible();
  await confirmBtn.click();
  await expect(quotesRow(page, label)).toHaveCount(0);
  // The row vanishes optimistically; wait on the ledger's .action-status so a reload can't race the DELETE.
  await expect(page.locator(".action-status")).toContainText(`Deleted "${label}"`);

  await page.reload();
  await expect(quotesRow(page, label)).toHaveCount(0);
});

test("a failed save is reported in the action-status alert", async ({ page }) => {
  // Only the write path fails; the initial GET /api/quotes succeeds.
  await page.route("**/api/quotes", (route) => {
    if (route.request().method() === "POST") {
      return route.fulfill({ status: 500, contentType: "text/plain", body: "boom" });
    }
    return route.continue();
  });

  await page.goto("/admin/");
  await fillCanonicalQuote(page);
  await page.getByLabel("Label for this quote").fill(`e2e-save-fail-${Date.now()}`);
  await page.getByRole("button", { name: "Save quote" }).click();

  // A non-JSON 500 body falls through to errorsFrom's fallback message, the same wording the Clients
  // and Invoices tabs show for a failed mutation.
  const status = page.locator(".action-status");
  await expect(status).toHaveAttribute("role", "alert");
  await expect(status).toContainText("The server couldn't complete that request. Try again.");
});

test("a failed initial load shows a couldn't-load note", async ({ page }) => {
  await page.route("**/api/quotes", (route) => route.fulfill({ status: 500, contentType: "text/plain", body: "boom" }));

  await page.goto("/admin/");
  const banner = page.getByRole("alert").filter({ hasText: "Couldn't load saved quotes" });
  await expect(banner).toBeVisible();
});
