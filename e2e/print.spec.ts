import { test, expect } from "@playwright/test";
import { countPdfPages, fillCanonicalQuote, stubWindowPrint } from "./helpers";

async function enableSelling(page: import("@playwright/test").Page) {
  await page.getByLabel("Set a selling price").check();
}

test("client print preview hides internal cost-line labels and app chrome", async ({ page }) => {
  await stubWindowPrint(page);
  await page.goto("/admin/");
  await fillCanonicalQuote(page);
  await enableSelling(page);

  const printBtn = page.getByRole("button", { name: "Print client quote" });
  await expect(printBtn).toBeEnabled();
  await printBtn.click();

  await page.emulateMedia({ media: "print" });

  const printView = page.locator("#print-view");
  await expect(printView).toBeVisible();
  await expect(page.locator("#app > header")).toBeHidden();
  await expect(page.locator("#app > main")).toBeHidden();

  const text = await printView.innerText();
  for (const label of ["Material", "Electricity", "Depreciation", "Net profit", "Fee:"]) {
    expect(text).not.toContain(label);
  }

  // The client quote still shows its own total: $2.24 cost at 30% margin is 2.24 / 0.70 = $3.20.
  await expect(printView).toContainText("Total");
  await expect(printView).toContainText("$3.20");
});

test("internal print preview shows net profit and full cost lines, hides app chrome", async ({ page }) => {
  await stubWindowPrint(page);
  await page.goto("/admin/");
  await fillCanonicalQuote(page);
  await enableSelling(page);

  const printBtn = page.getByRole("button", { name: "Print cost audit" });
  await expect(printBtn).toBeEnabled();
  await printBtn.click();

  await page.emulateMedia({ media: "print" });

  const printView = page.locator("#print-view");
  await expect(printView).toBeVisible();
  await expect(page.locator("#app > header")).toBeHidden();
  await expect(page.locator("#app > main")).toBeHidden();
  await expect(printView).toContainText("Net profit");
});

test("printed PDF has no blank trailing pages", async ({ page }) => {
  await stubWindowPrint(page);
  await page.goto("/admin/");
  await fillCanonicalQuote(page);
  await enableSelling(page);
  await page.getByRole("button", { name: "Print cost audit" }).click();
  await expect(page.locator("#print-view")).toBeVisible();

  const pageCount = countPdfPages(await page.pdf());
  expect(pageCount).toBeGreaterThan(0);
  // Guards the blank-trailing-pages regression: a short quote must not spill past 2 pages because a
  // hidden app-chrome element reserved layout height.
  expect(pageCount).toBeLessThanOrEqual(2);
});

test("invalid margin gates both print buttons", async ({ page }) => {
  await page.goto("/admin/");
  await fillCanonicalQuote(page);
  await enableSelling(page);

  const clientBtn = page.getByRole("button", { name: "Print client quote" });
  const internalBtn = page.getByRole("button", { name: "Print cost audit" });
  // Prove the gate toggles the buttons, not that they're permanently disabled.
  await expect(clientBtn).toBeEnabled();
  await expect(internalBtn).toBeEnabled();

  await page.getByLabel(/^Margin/).fill("150"); // engine caps marginPct at 99.9

  await expect(clientBtn).toBeDisabled();
  await expect(internalBtn).toBeDisabled();
});
