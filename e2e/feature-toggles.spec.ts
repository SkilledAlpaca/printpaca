import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";
import { gotoSettings } from "./helpers";

// Settings saves hot-apply to every later spec via /api/config, so everything runs in one test and
// Filament ends up re-enabled before the next spec.

function navButtons(page: Page, name: string) {
  return page.locator('nav[aria-label="Sections"] button', { hasText: name });
}

test("disabling a tab in Settings hides it from the nav and its hash falls back to Quote; re-enabling restores it", async ({
  page,
}) => {
  await gotoSettings(page);
  const filamentCheckbox = page.getByRole("checkbox", { name: "Filament", exact: true });
  await expect(filamentCheckbox).toBeChecked();
  await expect(navButtons(page, "Filament")).toHaveCount(1);

  await filamentCheckbox.uncheck();
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByText("Settings saved.")).toBeVisible();
  await expect(navButtons(page, "Filament")).toHaveCount(0);

  // Same-document navigation: a hash naming the disabled Filament tab is rewritten to the tab on screen.
  await page.goto("/admin/#/filament");
  await expect(page).toHaveURL(/#\/settings$/);
  await expect(page.getByRole("heading", { name: "Settings", exact: true })).toBeVisible();

  // Full load: the hash alone drives the initial route, and a disabled tab falls back to Quote.
  // Set the hash and reload in one evaluate so the reload aborts before the app's hashchange
  // correction runs; the call may reject when the context is torn down, which is expected.
  await page
    .evaluate(() => {
      location.hash = "#/filament";
      location.reload();
    })
    .catch(() => {});
  await expect(page).toHaveURL(/#\/quote$/);

  await gotoSettings(page);
  await expect(filamentCheckbox).not.toBeChecked();
  await filamentCheckbox.check();
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByText("Settings saved.")).toBeVisible();
  await expect(navButtons(page, "Filament")).toHaveCount(1);

  await page.goto("/admin/#/filament");
  await expect(page).toHaveURL(/#\/filament$/);
});
