import { test, expect } from "@playwright/test";
import { geometryOnly3mf } from "./geometry3mf";

// v1.2 release-candidate form changes: card help and "Explain this page" are gone (D33), so is the slicer box (D34),
// "Set a client price" turns selling on and lands on the price target (D35) (D36 is in money-requests-print-time.spec.ts). The field-help tooltips (D32) are in field-help.spec.ts.

test("New quote has no Explain this page, card help buttons, or slicer box", async ({ page }) => {
  await page.goto("/admin/");
  await expect(page.getByRole("heading", { name: "New quote" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Explain this page" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: /^About / })).toHaveCount(0);
  await expect(page.locator(".help-note")).toHaveCount(0);

  // A model with no slicer time: the notice stays, the "What is a slicer?" box does not.
  await page.locator('input[type="file"]').first().setInputFiles({ name: "cube.3mf", mimeType: "model/3mf", buffer: geometryOnly3mf() });
  await expect(page.getByText("This file has no slicer time. Enter the print time from your slicer.")).toBeVisible();
  await expect(page.getByText("What is a slicer?")).toHaveCount(0);

});

test("the Guide covers what the removed card help explained", async ({ page }) => {
  // Each concept is its own Guide topic now, opened by its deep link.
  for (const [id, title] of [
    ["model-files", "Model files and print time"],
    ["material-and-printer", "Material, printer, and print start"],
    ["cost-breakdown", "Cost breakdown"],
    ["saving-quotes", "Saving quotes"],
    ["margin-vs-markup", "Margin versus markup"],
    ["marketplace-fees", "Marketplace fees"],
  ] as const) {
    await page.goto(`/admin/#/guide#${id}`);
    await expect(page.getByRole("heading", { name: title, level: 2 })).toBeVisible();
  }
  await expect(page.getByText(/press the \? next to any card heading/)).toHaveCount(0);
  await page.goto("/admin/#/guide#model-files");
  await expect(page.getByText(/A slicer is the program that turns a 3D model/)).toBeVisible();
});

test("Set a client price is a secondary button that turns selling on and focuses the price target", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/admin/");
  const summary = page.getByRole("group", { name: "Quote summary" });
  const button = summary.getByRole("button", { name: "Set a client price" });
  await expect(button).toBeVisible();
  await expect(summary.getByRole("button", { name: "Set a price", exact: true })).toHaveCount(0);
  await expect(button).not.toHaveClass(/primary/);
  await expect(page.getByLabel("Set a selling price")).not.toBeChecked();

  await button.click();
  await expect(page.getByLabel("Set a selling price")).toBeChecked();
  await expect(page.getByLabel(/^Margin/)).toBeFocused();
  await expect(summary.getByRole("button", { name: "Set a client price" })).toHaveCount(0);
});

test("Set a client price focuses Markup when the strategy is markup", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/admin/");
  await page.getByLabel("Set a selling price").check();
  await page.getByLabel("Pricing strategy").selectOption("markup");
  await page.getByLabel("Set a selling price").uncheck();
  await page.getByRole("group", { name: "Quote summary" }).getByRole("button", { name: "Set a client price" }).click();
  await expect(page.getByLabel(/^Markup/)).toBeFocused();
});
