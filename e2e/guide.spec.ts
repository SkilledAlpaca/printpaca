import { test, expect } from "@playwright/test";

test("guide is reachable as its own page via the sidebar footer link", async ({ page }) => {
  await page.goto("/admin/");
  await expect(page.getByRole("heading", { name: "Guide", level: 1 })).toHaveCount(0);

  // Guide isn't a nav destination; it's a sidebar-footer button that calls activate("guide").
  await page.getByRole("button", { name: "Guide", exact: true }).click();
  await expect(page).toHaveURL(/#\/guide$/);
  await expect(page.getByRole("heading", { name: "Guide", level: 1 })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Getting started" })).toBeVisible();
});

test("a cold-load deep link to a guide anchor scrolls to that topic", async ({ page }) => {
  // On a fresh navigation, App's replaceState normalization must not strip the fragment before
  // GettingStarted's onMount reads location.hash.
  await page.goto("/admin/#/guide#margin-vs-markup");

  await expect(page.getByRole("heading", { name: "Guide", level: 1 })).toBeVisible();
  await expect
    .poll(() => page.locator("#main").evaluate((el) => el.scrollTop))
    .toBeGreaterThan(0);
});

test("a cold-load deep link reaches a topic the removed card help used to explain", async ({ page }) => {
  await page.goto("/admin/#/guide#model-files");
  await expect(page.getByRole("heading", { name: "Model files and print time" })).toBeVisible();
  await expect
    .poll(() => page.locator("#main").evaluate((el) => el.scrollTop))
    .toBeGreaterThan(0);
});
