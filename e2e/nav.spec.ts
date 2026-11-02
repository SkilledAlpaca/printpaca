import { test, expect } from "@playwright/test";
import { navButton } from "./helpers";

test("clicking a nav destination updates the hash, survives reload, and back navigates away", async ({ page }) => {
  await page.goto("/admin/");
  await expect(page).toHaveURL(/#\/quote$/);
  await expect(navButton(page, "New quote")).toHaveAttribute("aria-current", "page");

  await navButton(page, "Electricity").click();
  await expect(page).toHaveURL(/#\/electricity$/);
  await expect(navButton(page, "Electricity")).toHaveAttribute("aria-current", "page");
  await expect(page.getByText("$0.375")).toBeVisible(); // ElectricityTab actually rendered, not just the hash

  await page.reload();
  await expect(page).toHaveURL(/#\/electricity$/);
  await expect(navButton(page, "Electricity")).toHaveAttribute("aria-current", "page");

  await page.goBack();
  await expect(page).toHaveURL(/#\/quote$/);
  await expect(navButton(page, "New quote")).toHaveAttribute("aria-current", "page");
});

test("an invalid hash is treated as absent and falls back to Quote", async ({ page }) => {
  await page.goto("/admin/#/bogus");
  await expect(navButton(page, "New quote")).toHaveAttribute("aria-current", "page");
  await expect(page).toHaveURL(/#\/quote$/);
});

test("Guide has left the nav; it's reached from the sidebar footer link instead", async ({ page }) => {
  await page.goto("/admin/");
  await expect(navButton(page, "Guide")).toHaveCount(0);

  // The footer link sits outside the nav landmark, so find it unscoped.
  await page.getByRole("button", { name: "Guide", exact: true }).click();
  await expect(page).toHaveURL(/#\/guide$/);
  await expect(page.getByRole("heading", { name: "Guide", level: 1 })).toBeVisible();
  await expect(navButton(page, "New quote")).not.toHaveAttribute("aria-current", "page");
});

test("skip link is the first tab stop and focuses main without changing the route", async ({ page }) => {
  await page.goto("/admin/");
  await expect(page).toHaveURL(/#\/quote$/);

  await page.keyboard.press("Tab");
  const skipLink = page.getByRole("link", { name: "Skip to content" });
  await expect(skipLink).toBeFocused();

  await page.keyboard.press("Enter");
  await expect(page.locator("#main")).toBeFocused();
  // The skip link's handler preventDefaults, so it must not set location.hash to "#main" and clobber
  // the tab router.
  await expect(page).toHaveURL(/#\/quote$/);
});
