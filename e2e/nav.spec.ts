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

// <main> takes script focus (tabindex="-1") when a destination opens, so the skip target and the scroll container
// are focused. theme.css's :focus-visible ring must not frame the whole content area for that; real controls keep it.
test("opening a destination leaves no focus ring on the main area, on any admin route", async ({ page }) => {
  await page.goto("/admin/");
  const nav = page.getByRole("navigation", { name: "Sections" });
  await expect(nav.getByRole("button").first()).toBeVisible();
  const names = (await nav.getByRole("button").allInnerTexts()).map((t) => t.trim().split("\n")[0]!.trim()).filter(Boolean);
  expect(names.length).toBeGreaterThan(8);
  const main = page.locator("#main");
  for (const how of ["click", "keyboard"] as const) {
    for (const name of names) {
      const button = navButton(page, name);
      if (how === "click") await button.click();
      else {
        await button.focus();
        await page.keyboard.press("Enter");
      }
      await expect(button).toHaveAttribute("aria-current", "page");
      await expect(main).toHaveAttribute("tabindex", "-1");
      await expect.poll(() => main.evaluate((el) => getComputedStyle(el).outlineStyle), `${how} ${name}`).toBe("none");
    }
  }
});

test("keyboard focus on real controls keeps the focus ring", async ({ page }) => {
  await page.goto("/admin/");
  await navButton(page, "Settings").click();
  await page.keyboard.press("Tab");
  const focused = page.locator(":focus");
  await expect(focused).toBeVisible();
  expect(await focused.evaluate((el) => getComputedStyle(el).outlineStyle)).toBe("solid");
});
