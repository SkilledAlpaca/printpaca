import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { loginAsAdmin, resetServerState } from "./helpers";

/** D54: the theme button names the theme it switches to. */
const THEME_BTN = /^(Dark|Light) mode$/;

// Release candidate shell changes: D39 sidebar footer text, D40 theme toggle, D41 Sign out confirmation,
// and D57 (no integration badges).
// The 8099 server has no integrations or sign-in; 9099 has a password (Sign out); 9199 has Spoolman and Moonraker.

const TAGS = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"];
const INTEGRATIONS_URL = "http://127.0.0.1:9199/admin/";
const sidebar = (page: Page) => page.getByRole("complementary");

async function axeClean(page: Page, label: string): Promise<void> {
  await page.waitForLoadState("networkidle");
  await page.evaluate(() => document.fonts.ready);
  const results = await new AxeBuilder({ page }).withTags(TAGS).analyze();
  expect(
    results.violations.map((v) => `${label}: ${v.id} (${v.impact}) ${v.nodes[0]?.target.join(" ")}`),
  ).toEqual([]);
}

async function pickTheme(page: Page, theme: "light" | "dark"): Promise<void> {
  const btn = sidebar(page).getByRole("button", { name: /^(Dark|Light) mode$/ });
  const isDark = (await btn.getAttribute("aria-label")) === "Light mode";
  if (isDark !== (theme === "dark")) await btn.click();
  await expect(btn).toHaveAccessibleName(theme === "dark" ? "Light mode" : "Dark mode");
}

test.describe("D39 sidebar footer text", () => {
  test("the line and the version sit in the sidebar; there is no page footer", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto("/admin/");
    await expect(page.locator("main footer, #app > footer")).toHaveCount(0);
    await expect(page.getByText("printpaca, an open-source 3D print cost and quote calculator.")).toHaveCount(0);
    const about = sidebar(page).locator(".about");
    // D57: one line, "printpaca <version> · open source".
    await expect(about).toHaveText(/^printpaca \S+ · open source$/);
    const oneLine = await about.evaluate((el) => el.getBoundingClientRect().height < parseFloat(getComputedStyle(el).fontSize) * 2);
    expect(oneLine).toBe(true);
    // Short enough for the 220 px sidebar: no clipping, no horizontal overflow of the footer.
    const fits = await sidebar(page).locator(".sidebar-footer").evaluate((el) => el.scrollWidth <= el.clientWidth);
    expect(fits).toBe(true);
  });

  test("the collapsed rail hides the line and keeps the version as a tooltip", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto("/admin/");
    await sidebar(page).getByRole("button", { name: "Collapse sidebar" }).click();
    await expect(sidebar(page).locator(".about")).toHaveCount(0);
    await expect(sidebar(page).getByText("open source")).toHaveCount(0);
    const rail = sidebar(page).locator(".version-rail");
    await expect(rail).toHaveAttribute("title", /^printpaca /);
    await expect(rail).toHaveAttribute("aria-label", /^printpaca /);
    await sidebar(page).getByRole("button", { name: "Expand sidebar" }).click();
  });

  test("the shortcut hint stays as a line at the end of the content", async ({ page }) => {
    await page.goto("/admin/");
    await expect(page.locator("main .shortcuts-line")).toContainText("Shortcuts:");
  });
});

test.describe("D40 theme toggle", () => {
  test("starts from the device setting, then remembers the choice per browser", async ({ page }) => {
    await page.emulateMedia({ colorScheme: "dark" });
    await page.goto("/admin/");
    const btn = sidebar(page).getByRole("button", { name: /^(Dark|Light) mode$/ });
    await expect(btn).toHaveAccessibleName("Light mode");
    await expect(btn).not.toHaveAttribute("aria-pressed", /.*/);
    await expect(page.locator("html")).not.toHaveAttribute("data-theme", /.*/);
    expect(await page.evaluate(() => localStorage.getItem("printpaca-theme"))).toBeNull();

    await btn.click();
    await expect(btn).toHaveAccessibleName("Dark mode");
    await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
    expect(await page.evaluate(() => localStorage.getItem("printpaca-theme"))).toBe("light");

    // The choice beats the device setting after a reload.
    await page.reload();
    await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
    await expect(sidebar(page).getByRole("button", { name: /^(Dark|Light) mode$/ })).toHaveAccessibleName("Dark mode");
    await page.evaluate(() => localStorage.removeItem("printpaca-theme"));
  });

  test("a stored choice is applied before the app mounts, so there is no flash", async ({ page }) => {
    await page.addInitScript(() => {
      try {
        localStorage.setItem("printpaca-theme", "dark");
      } catch { /* ignore */ }
      const orig = Element.prototype.setAttribute;
      Element.prototype.setAttribute = function (this: Element, name: string, value: string) {
        const w = window as unknown as { __themeAtMount?: string };
        if (name === "data-theme" && this === document.documentElement && w.__themeAtMount === undefined) {
          w.__themeAtMount = `${value}:${document.getElementById("app")?.childElementCount ?? -1}`;
        }
        return orig.call(this, name, value);
      };
    });
    await page.emulateMedia({ colorScheme: "light" });
    await page.goto("/admin/");
    expect(await page.evaluate(() => (window as unknown as { __themeAtMount?: string }).__themeAtMount)).toBe("dark:0");
    await page.evaluate(() => localStorage.removeItem("printpaca-theme"));
  });

  test("the label switches Dark mode / Light mode with the icon, never wraps, and announces politely", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.emulateMedia({ colorScheme: "light" });
    await page.goto("/admin/");
    const btn = sidebar(page).getByRole("button", { name: THEME_BTN });
    await expect(btn).toHaveText("Dark mode");
    const icon = () => btn.locator("svg path").getAttribute("d");
    const moon = await icon();
    const oneLine = () => btn.locator("span").evaluate((el) => el.getBoundingClientRect().height < parseFloat(getComputedStyle(el).fontSize) * 2);
    expect(await oneLine()).toBe(true);
    await btn.click();
    await expect(btn).toHaveText("Light mode");
    expect(await icon()).not.toBe(moon);
    expect(await oneLine()).toBe(true);
    await expect(page.getByRole("status").filter({ hasText: "Dark mode on" })).toHaveCount(1);
    await btn.click();
    await expect(page.getByRole("status").filter({ hasText: "Light mode on" })).toHaveCount(1);
    await page.evaluate(() => localStorage.removeItem("printpaca-theme"));
  });

  test("a blocked localStorage does not break the toggle", async ({ page }) => {
    await page.addInitScript(() => {
      Object.defineProperty(window, "localStorage", { get() { throw new Error("blocked"); } });
    });
    await page.goto("/admin/");
    const btn = sidebar(page).getByRole("button", { name: /^(Dark|Light) mode$/ });
    await btn.click();
    await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  });

  test("the footer toggles pass axe in both themes, expanded, collapsed, and in the phone drawer", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto("/admin/");
    for (const theme of ["light", "dark"] as const) {
      await pickTheme(page, theme);
      await axeClean(page, `expanded ${theme}`);
    }
    await sidebar(page).getByRole("button", { name: "Collapse sidebar" }).click();
    for (const theme of ["light", "dark"] as const) {
      await pickTheme(page, theme);
      await axeClean(page, `collapsed ${theme}`);
    }
    await sidebar(page).getByRole("button", { name: "Expand sidebar" }).click();
    await page.setViewportSize({ width: 390, height: 844 });
    await page.getByRole("button", { name: /^Open menu/ }).click();
    for (const theme of ["light", "dark"] as const) {
      await pickTheme(page, theme);
      await axeClean(page, `drawer ${theme}`);
    }
    await page.evaluate(() => localStorage.removeItem("printpaca-theme"));
  });

  test("every footer control is at least 44 px tall in the phone drawer, in both densities", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto("/admin/");
    await page.getByRole("button", { name: /^Open menu/ }).click();
    for (const compact of [false, true]) {
      if (compact) await sidebar(page).getByRole("button", { name: "Compact density" }).click();
      for (const name of ["Guide", "Compact density", THEME_BTN]) {
        const b = (await sidebar(page).getByRole("button", { name }).boundingBox())!;
        expect(b.height, `${name} (compact ${compact})`).toBeGreaterThanOrEqual(43.5);
      }
    }
    await sidebar(page).getByRole("button", { name: "Compact density" }).click();
  });
});

test.describe("D41 Sign out confirmation", () => {
  test.beforeEach(async () => { await resetServerState(); });
  test.afterAll(async () => { await resetServerState(); });

  test("the first click turns Sign out into cancel and confirm, focus on confirm", async ({ page }) => {
    await loginAsAdmin(page);
    const signOut = sidebar(page).getByRole("button", { name: "Sign out", exact: true });
    await signOut.click();
    const group = sidebar(page).getByRole("group", { name: "Confirm sign out" });
    await expect(group).toBeVisible();
    await expect(signOut).toHaveCount(0);
    const cancel = group.getByRole("button", { name: "Cancel sign out" });
    const confirm = group.getByRole("button", { name: "Confirm sign out" });
    await expect(confirm).toBeFocused();
    // Each half has an icon and its own color (red cancel, green confirm), so color is never the only cue.
    await expect(cancel.locator("svg")).toHaveCount(1);
    await expect(confirm.locator("svg")).toHaveCount(1);
    const [c1, c2] = await Promise.all([cancel, confirm].map((b) => b.evaluate((el) => getComputedStyle(el).color)));
    expect(c1).not.toBe(c2);
  });

  test("Escape cancels, returns focus to Sign out, and announces politely", async ({ page }) => {
    await loginAsAdmin(page);
    await sidebar(page).getByRole("button", { name: "Sign out", exact: true }).click();
    await page.keyboard.press("Escape");
    const signOut = sidebar(page).getByRole("button", { name: "Sign out", exact: true });
    await expect(signOut).toBeFocused();
    await expect(page.locator('[role="status"][aria-live="polite"]').filter({ hasText: "Sign out cancelled" })).toHaveCount(1);
  });

  test("Cancel returns focus to Sign out; a click elsewhere reverts without stealing focus", async ({ page }) => {
    await loginAsAdmin(page);
    await sidebar(page).getByRole("button", { name: "Sign out", exact: true }).click();
    await sidebar(page).getByRole("button", { name: "Cancel sign out" }).click();
    await expect(sidebar(page).getByRole("button", { name: "Sign out", exact: true })).toBeFocused();

    await sidebar(page).getByRole("button", { name: "Sign out", exact: true }).click();
    await page.getByRole("navigation", { name: "Sections" }).getByRole("button", { name: "Quotes" }).click();
    await expect(sidebar(page).getByRole("button", { name: "Sign out", exact: true })).toBeVisible();
    await expect(sidebar(page).getByRole("group", { name: "Confirm sign out" })).toHaveCount(0);
  });

  test("it reverts after 5 s, and focus moving inside the control restarts the timer", async ({ page }) => {
    await page.clock.install();
    await loginAsAdmin(page);
    await sidebar(page).getByRole("button", { name: "Sign out", exact: true }).click();
    const group = sidebar(page).getByRole("group", { name: "Confirm sign out" });
    await page.clock.runFor(4000);
    await expect(group).toBeVisible();
    await page.keyboard.press("Shift+Tab"); // focus moves to Cancel: restarts the 5 s
    await page.clock.runFor(4000);
    await expect(group).toBeVisible();
    await page.clock.runFor(1500);
    await expect(group).toHaveCount(0);
    await expect(sidebar(page).getByRole("button", { name: "Sign out", exact: true })).toBeFocused();
    await expect(page.locator('[role="status"][aria-live="polite"]').filter({ hasText: "Sign out cancelled" })).toHaveCount(1);
  });

  test("Confirm signs out", async ({ page }) => {
    await loginAsAdmin(page);
    await sidebar(page).getByRole("button", { name: "Sign out", exact: true }).click();
    await sidebar(page).getByRole("button", { name: "Confirm sign out" }).click();
    await expect(page.getByLabel("Password")).toBeVisible();
  });

  test("the split control passes axe in both themes, in the rail and in the drawer, and keeps 44 px targets", async ({ page }) => {
    await loginAsAdmin(page);
    await page.setViewportSize({ width: 1440, height: 900 });
    await sidebar(page).getByRole("button", { name: "Sign out", exact: true }).click();
    for (const theme of ["light", "dark"] as const) {
      await pickTheme(page, theme); // the click on the theme toggle is "elsewhere" inside the sidebar: re-open
      if ((await sidebar(page).getByRole("group", { name: "Confirm sign out" }).count()) === 0) {
        await sidebar(page).getByRole("button", { name: "Sign out", exact: true }).click();
      }
      await axeClean(page, `split expanded ${theme}`);
    }
    await page.keyboard.press("Escape");
    await sidebar(page).getByRole("button", { name: "Collapse sidebar" }).click();
    for (const theme of ["light", "dark"] as const) {
      await pickTheme(page, theme);
      await sidebar(page).getByRole("button", { name: "Sign out", exact: true }).click();
      await axeClean(page, `split rail ${theme}`);
      await page.keyboard.press("Escape");
    }
    await sidebar(page).getByRole("button", { name: "Expand sidebar" }).click();
    await page.setViewportSize({ width: 390, height: 844 });
    await page.getByRole("button", { name: /^Open menu/ }).click();
    for (const theme of ["light", "dark"] as const) {
      await pickTheme(page, theme);
      await sidebar(page).getByRole("button", { name: "Sign out", exact: true }).click();
      for (const name of ["Cancel sign out", "Confirm sign out"]) {
        const b = (await sidebar(page).getByRole("button", { name }).boundingBox())!;
        expect(b.height, name).toBeGreaterThanOrEqual(43.5);
      }
      await axeClean(page, `split drawer ${theme}`);
      await page.keyboard.press("Escape");
      // Escape on the control must not close the drawer behind it.
      await expect(sidebar(page).getByRole("button", { name: "Sign out", exact: true })).toBeVisible();
    }
    await page.evaluate(() => localStorage.removeItem("printpaca-theme"));
  });
});

test.describe("D57 no integration badges in the sidebar", () => {
  test("neither Spoolman nor Moonraker shows a status pill, even when both are configured", async ({ page }) => {
    for (const url of ["/admin/", INTEGRATIONS_URL]) {
      await page.goto(url);
      await expect(sidebar(page).getByText(/: configured/)).toHaveCount(0);
      await expect(sidebar(page).locator(".int-badge, .badges, .badge-detail")).toHaveCount(0);
      await expect(sidebar(page).getByRole("button", { name: /Spoolman|Moonraker/ })).toHaveCount(0);
    }
  });

  test("integration status lives in Settings > Integrations", async ({ page }) => {
    await page.goto(INTEGRATIONS_URL + "#/settings");
    await expect(page.getByRole("heading", { name: "Integrations", level: 2 })).toBeVisible();
  });

  test("at 1366x768 every nav item is reachable and the whole footer stays inside the sidebar", async ({ page }) => {
    await page.setViewportSize({ width: 1366, height: 768 });
    await page.goto(INTEGRATIONS_URL + "#/dashboard");
    const aside = (await sidebar(page).boundingBox())!;
    for (const name of ["Guide", "Compact density", THEME_BTN, "Collapse sidebar"]) {
      const b = (await sidebar(page).getByRole("button", { name }).boundingBox())!;
      expect(b.y, `${name} top`).toBeGreaterThanOrEqual(aside.y);
      expect(b.y + b.height, `${name} bottom`).toBeLessThanOrEqual(aside.y + aside.height + 0.5);
    }
    const settings = page.getByRole("navigation", { name: "Sections" }).getByRole("button", { name: "Settings", exact: true });
    await settings.scrollIntoViewIfNeeded();
    const hit = await settings.evaluate((el) => {
      const r = el.getBoundingClientRect();
      const top = document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2);
      return el === top || el.contains(top);
    });
    expect(hit).toBe(true);
  });
});
