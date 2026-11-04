import { test, expect } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { navButton } from "./helpers";

// v1.2 D5 and D20: the admin app has Comfortable (default) and Compact density, chosen with a toggle in the
// sidebar footer and kept in this browser's localStorage. The public pages never apply it.

const toggle = (page: import("@playwright/test").Page) => page.getByRole("button", { name: "Compact density" });

test("density defaults to comfortable, toggles to compact, and survives a reload", async ({ page }) => {
  await page.goto("/admin/");
  await expect(navButton(page, "New quote")).toBeVisible();
  const html = page.locator("html");
  await expect(html).not.toHaveAttribute("data-density", /.*/);
  await expect(toggle(page)).toHaveAttribute("aria-pressed", "false");

  const comfortable = (await page.getByLabel("Model weight (g)").boundingBox())!.height;
  await toggle(page).click();
  await expect(html).toHaveAttribute("data-density", "compact");
  await expect(toggle(page)).toHaveAttribute("aria-pressed", "true");
  const compact = (await page.getByLabel("Model weight (g)").boundingBox())!.height;
  // A ratio, not pixels: fonts differ between machines, but 32 px controls are always shorter than 40 px ones.
  expect(compact).toBeLessThan(comfortable);

  await page.reload();
  await expect(html).toHaveAttribute("data-density", "compact");
  await expect(toggle(page)).toHaveAttribute("aria-pressed", "true");

  await toggle(page).click();
  await expect(html).not.toHaveAttribute("data-density", /.*/);
  expect(await page.evaluate(() => localStorage.getItem("printpaca-density"))).toBe("comfortable");
});

test("the shop stays comfortable even when the admin chose compact", async ({ page }) => {
  await page.goto("/admin/");
  await toggle(page).click();
  await expect(page.locator("html")).toHaveAttribute("data-density", "compact");
  // Same origin, so the stored choice is visible to the shop; it must ignore it.
  await page.goto("/");
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
  await expect(page.locator("html")).not.toHaveAttribute("data-density", /.*/);
  await page.goto("/admin/");
  await toggle(page).click(); // restore the default for the specs that follow
});

test("an unreadable stored value falls back to comfortable", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("printpaca-density", "tiny"));
  await page.goto("/admin/");
  await expect(navButton(page, "New quote")).toBeVisible();
  await expect(page.locator("html")).not.toHaveAttribute("data-density", /.*/);
  await page.evaluate(() => localStorage.removeItem("printpaca-density"));
});

test("the toggle is in the mobile drawer and compact density passes axe in both themes", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/admin/");
  await page.getByRole("button", { name: /^Open menu/ }).click();
  await expect(toggle(page)).toBeVisible();
  await toggle(page).click();
  await expect(page.locator("html")).toHaveAttribute("data-density", "compact");
  await page.keyboard.press("Escape");
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    await page.waitForLoadState("networkidle");
    const results = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"]).analyze();
    expect(results.violations.map((v) => `${v.id}: ${v.nodes[0]?.target.join(" ")}`), scheme).toEqual([]);
  }
  await page.evaluate(() => localStorage.removeItem("printpaca-density"));
});
