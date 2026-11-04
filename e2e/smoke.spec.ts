import { test, expect } from "@playwright/test";
import { fillCanonicalQuote, navButton, openBreakdown } from "./helpers";

test("app boots and computes the canonical quote", async ({ page }) => {
  await page.goto("/admin/");
  await expect(page.getByRole("heading", { name: "New quote", level: 1 })).toBeVisible();

  await openBreakdown(page);
  // Canonical case: 50 g, 1 h 20 m, defaults -> $2.24 total. The hours lookup is anchored because an
  // unanchored /Print time/ also matches the wrapping role=group.
  await page.getByLabel(/Model weight/).fill("50");
  await page.getByLabel(/^Print time \(h/).fill("1");
  await page.getByLabel(/Print time \(minutes\)/).fill("20");

  // Scoped to the breakdown table: saved quotes from other specs can leave the same amounts in history.
  const table = page.getByRole("table");
  await expect(table.getByText("$2.24")).toBeVisible();
  await expect(table.getByText("$1.00").first()).toBeVisible(); // material line

  await table.getByText("Material", { exact: true }).click();
  await expect(page.getByText("50 g × 20/kg ÷ 1000")).toBeVisible();
});

test("clearing a field shows a scoped warning and blanks the computed total", async ({ page }) => {
  await page.goto("/admin/");

  // A fresh, untouched field never shows its warning even though Model weight starts empty. Wait for
  // the input to be visible first, so the assertion doesn't pass trivially before hydration.
  await expect(page.getByLabel(/Model weight/)).toBeVisible();
  const weightWarning = page.locator(".ui-field", { has: page.getByLabel(/Model weight/) }).locator(".error-text");
  await expect(weightWarning).not.toBeVisible();

  await page.getByLabel(/Model weight/).fill("50");
  await page.getByLabel(/^Print time \(h/).fill("1");
  await page.getByLabel(/Print time \(minutes\)/).fill("20");
  await expect(page.locator("tfoot")).toContainText("$2.24"); // sanity: valid before clearing

  await page.getByLabel(/Model weight/).fill("");

  // Scoped to the field's own <label>: an unscoped match would also hit the footer's blanked-total dash.
  await expect(weightWarning).toContainText("Enter a valid model weight.");

  // The computed total must blank out (render "-"), not keep a stale or zero number.
  await expect(page.locator("tfoot")).toContainText("-");
  await expect(page.locator("tfoot")).not.toContainText("$2.24");
});

test("electricity tab matches reference math", async ({ page }) => {
  await page.goto("/admin/");
  await navButton(page, "Electricity").click();
  await expect(page.getByText("$0.375")).toBeVisible(); // 250 W · 10 h · $0.15
});

test("Edit rates open state persists across reload", async ({ page }) => {
  await page.goto("/admin/");

  // persistCostsOpen must read e.currentTarget.open (the post-toggle DOM value), not the costsOpen
  // binding, which bind:open hasn't synced yet and would persist the previous state.
  const rates = page.locator("details.rates");
  await expect(rates).not.toHaveAttribute("open", "");

  await rates.locator("summary").click();
  await expect(rates).toHaveAttribute("open", "");

  // <details>'s toggle event fires as a queued task; wait for the write to land before reloading.
  await expect
    .poll(() => page.evaluate(() => localStorage.getItem("printpaca-costs-open")))
    .toBe("1");

  await page.reload();
  await expect(page.locator("details.rates")).toHaveAttribute("open", "");
});

test("an invalid print-time half warns only that half", async ({ page }) => {
  await page.goto("/admin/");
  await fillCanonicalQuote(page); // valid baseline: 1 h, 20 min

  const hours = page.getByLabel(/^Print time \(h/);
  const minutes = page.getByLabel(/Print time \(minutes\)/);

  await hours.fill("-5"); // below the engine's min:0 bound for timeHours

  // One shared warning line under the pair, not one per input.
  const timeField = page.locator(".ui-field", { has: page.locator(".time-pair") });
  await expect(timeField.locator(".error-text")).toHaveCount(1);

  // Each half flags aria-invalid from its own warning: hours is out of bounds, minutes is valid.
  await expect(hours).toHaveAttribute("aria-invalid", "true");
  await expect(minutes).not.toHaveAttribute("aria-invalid", "true");
});
