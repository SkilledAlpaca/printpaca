import { test, expect } from "@playwright/test";
import { fillCanonicalQuote, readMoney, openMorePricing } from "./helpers";

// UI-rendering invariants that no unit or compat test covers: a blanking rule, which number maps to
// which on-screen field, and a precondition's on-screen consequence.

test("an invalid weight blanks only the Material row and both footer totals, not unrelated rows", async ({ page }) => {
  await page.goto("/admin/");
  await fillCanonicalQuote(page);
  // Quantity > 1 so both footer totals render; at quantity 1 the Batch total row is absent.
  await page.getByLabel(/Quantity/).fill("2");

  const table = page.getByRole("table");
  await expect(table.getByText("Material", { exact: true })).toBeVisible(); // sanity: valid before clearing
  await expect(page.locator("tfoot")).not.toContainText("-");

  await page.getByLabel(/Model weight/).fill("");

  // An invalid value renders as "-", never a wrong number; only the line whose own field is in error blanks.
  const materialRow = table.locator("tr", { hasText: "Material" });
  await expect(materialRow.locator("td.num")).toHaveText("-");

  const electricityRow = table.locator("tr", { hasText: "Electricity" });
  await expect(electricityRow.locator("td.num")).not.toHaveText("-");
  await expect(electricityRow.locator("td.num")).toContainText("$");

  const totals = page.locator("tfoot th.num");
  await expect(totals).toHaveCount(2);
  await expect(totals.nth(0)).toHaveText("-");
  await expect(totals.nth(1)).toHaveText("-");
});

test("shipping charge and shipping cost are not transposed in the profit math", async ({ page }) => {
  await page.goto("/admin/");
  await fillCanonicalQuote(page);
  await page.getByRole("checkbox", { name: /Set a selling price/ }).check();

  const netProfit = page.locator(".pricing-out .net dd");
  await expect(netProfit).toBeVisible();
  const baseline = await readMoney(netProfit);

  // Charge $5, cost $9: net profit must drop by exactly $4, which catches swapped formula inputs.
  await openMorePricing(page);
  await page.getByLabel(/Shipping charged/).fill("5");
  await page.getByLabel(/Shipping cost/).fill("9");

  // The pricing panel recomputes through a debounced server round trip; poll until it lands.
  await expect
    .poll(async () => baseline - (await readMoney(netProfit)), { timeout: 10_000 })
    .toBeCloseTo(4, 2);
});

test("a valid all-zero-cost quote cannot be priced, and shows a reason instead of $0.00", async ({ page }) => {
  await page.goto("/admin/");

  // Printer power through Monthly fixed costs sit in the collapsed "Edit rates" details; open it first.
  await page.locator("details.rates > summary").click();

  // Explicit valid zeros, so the breakdown is valid and totals $0.00: this exercises the
  // "perUnitTotal > 0" pricing precondition, not the blank-input case above.
  await page.getByLabel(/Model weight/).fill("0");
  await page.getByLabel(/^Print time \(h/).fill("0");
  await page.getByLabel(/Print time \(minutes\)/).fill("0");
  await page.getByLabel(/Filament price/).fill("0");
  await page.getByLabel(/Printer power/).fill("0");
  await page.getByLabel(/Failure rate/).fill("0");
  await page.getByLabel(/Prep time/).fill("0");
  await page.getByLabel(/Labor rate/).fill("0");
  await page.getByLabel(/Printer cost/).fill("0");
  await page.getByLabel(/Monthly fixed costs/).fill("0");

  await expect(page.locator("tfoot")).toContainText("$0.00");

  await page.getByRole("checkbox", { name: /Set a selling price/ }).check();

  const pricingCard = page.locator("#pricing-card");
  const recommended = pricingCard
    .locator(".pricing-out > div", { hasText: "Recommended price (per unit)" })
    .locator("dd");
  await expect(recommended).toHaveText("-");
  await expect(recommended).not.toContainText("$0.00");

  // The field-less engine warning is the only explanation for why a $0.00-cost quote can't be priced.
  await expect(pricingCard).toContainText("cost basis must be positive to price");
});

// Tax is price-inclusive (docs/formulas.md): pinned at the invoice and Go layers, and here for the Quote
// tab's rendering of "Tax (included in price)", where inclusive vs exclusive math would show a wrong number.
test("the tax row renders the price-inclusive figure, not price x tax%, under its documented label", async ({ page }) => {
  await page.goto("/admin/");
  await fillCanonicalQuote(page);
  await page.getByRole("checkbox", { name: /Set a selling price/ }).check();

  // Pin the margin so the expected tax figure doesn't depend on what an earlier spec left in config.
  await page.getByLabel("Margin (%)").fill("30");
  // LabeledNumber renders `${label} (${suffix})`, so the accessible name is "Tax (included in price) (%)".
  await openMorePricing(page);
  await page.getByLabel(/^Tax \(included in price\)/).fill("20");

  // Cost $2.24 at 30% margin: the pre-tax price is 224c / 0.7 = 320c, grossed up by 20% tax to 384c.
  // Inclusive tax = 384 x 20 / 120 = 64c (the pre-tax price x 20%), not the exclusive 384c x 0.20 = 76.8c -> 77c.
  const taxRow = page.locator(".pricing-out .sub", { hasText: "Tax (included in price)" });
  await expect(taxRow.locator("dd")).toHaveText("-$0.64");
  await expect(taxRow.locator("dd")).not.toHaveText("-$0.77");
});
