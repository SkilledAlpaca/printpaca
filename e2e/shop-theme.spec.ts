import { test, expect } from "@playwright/test";
import type { APIRequestContext, Page } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { getConfig, putConfig } from "./helpers";
import type { Json } from "./helpers";

// The customer shop's light/dark toggle. The choice is the pp_theme cookie, which the server turns into
// data-theme on <html> in the HTML itself, so a reload never flashes. The button names the theme it switches to.

const TAGS = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"];
const ACCENT = "#1f6f8b";
let original: Json;

async function setShop(request: APIRequestContext, patch: Json): Promise<void> {
  const config = await getConfig(request);
  config.shop = { ...config.shop, ...patch };
  await putConfig(request, config);
}
const toggle = (page: Page) => page.getByRole("button", { name: /^(Dark|Light) mode$/ });
const html = (page: Page) => page.locator("html");
const accentOf = (page: Page) => page.evaluate(() => document.documentElement.style.getPropertyValue("--accent"));

test.beforeAll(async ({ request }) => {
  original = await getConfig(request);
  await setShop(request, { accentColor: ACCENT, acceptingOrders: true, pausedMessage: "" });
});
test.afterAll(async ({ request }) => {
  const config = await getConfig(request);
  config.shop = original.shop;
  await putConfig(request, config);
});

test("with no cookie the device setting decides, and the button names the other theme", async ({ page }) => {
  await page.emulateMedia({ colorScheme: "dark" });
  await page.goto("/");
  await expect(toggle(page)).toHaveAccessibleName("Light mode");
  await expect(toggle(page)).not.toHaveAttribute("aria-pressed", /.*/);
  await expect(html(page)).not.toHaveAttribute("data-theme", /.*/);
  await page.emulateMedia({ colorScheme: "light" });
  await expect(toggle(page)).toHaveAccessibleName("Dark mode");
});

test("toggling switches theme, label, icon and accent at once and saves the cookie; a reload keeps it with no flash", async ({ page, context, request }) => {
  await page.emulateMedia({ colorScheme: "light" });
  await page.goto("/");
  const lightAccent = await expect.poll(() => accentOf(page)).toBe(ACCENT).then(() => accentOf(page));
  const iconBefore = await toggle(page).locator("svg path").getAttribute("d");

  await toggle(page).click();
  await expect(html(page)).toHaveAttribute("data-theme", "dark");
  await expect(toggle(page)).toHaveAccessibleName("Light mode");
  expect(await toggle(page).locator("svg path").getAttribute("d")).not.toBe(iconBefore);
  await expect(page.getByRole("status").filter({ hasText: "Dark mode on" })).toHaveCount(1);
  // The accent switched to its derived dark variant.
  const pub = (await (await request.get("/api/public/config")).json()).shop.accent as Json;
  expect(pub.dark.accent).not.toBe(pub.light.accent);
  await expect.poll(() => accentOf(page)).toBe(pub.dark.accent);
  expect(lightAccent).toBe(pub.light.accent);

  const cookie = (await context.cookies()).find((c) => c.name === "pp_theme")!;
  expect(cookie.value).toBe("dark");
  expect(cookie.sameSite).toBe("Lax");
  expect(cookie.path).toBe("/");
  expect(cookie.httpOnly).toBe(false);
  expect(cookie.expires - Date.now() / 1000).toBeGreaterThan(364 * 86400);

  // The raw HTML for a request carrying the cookie already has the attribute: that is why nothing flashes.
  const raw = await (await request.get("/", { headers: { Cookie: "pp_theme=dark" } })).text();
  expect(raw).toMatch(/<html[^>]* data-theme="dark"/);
  const rawNone = await (await request.get("/")).text();
  expect(rawNone).not.toContain("data-theme");

  // The attribute is on <html> before any script runs, even with JavaScript off.
  const noJs = await page.context().browser()!.newContext({ javaScriptEnabled: false, baseURL: "http://127.0.0.1:8099" });
  await noJs.addCookies([{ name: "pp_theme", value: "dark", url: "http://127.0.0.1:8099" }]);
  const p2 = await noJs.newPage();
  await p2.goto("/");
  await expect(p2.locator("html")).toHaveAttribute("data-theme", "dark");
  await noJs.close();

  await page.reload();
  await expect(html(page)).toHaveAttribute("data-theme", "dark");
  await expect(toggle(page)).toHaveAccessibleName("Light mode");
  await expect.poll(() => accentOf(page)).toBe(pub.dark.accent);

  // The saved choice beats the device setting; toggling back saves light.
  await toggle(page).click();
  await expect(html(page)).toHaveAttribute("data-theme", "light");
  await expect(page.getByRole("status").filter({ hasText: "Light mode on" })).toHaveCount(1);
  await expect.poll(() => accentOf(page)).toBe(pub.light.accent);
  expect((await context.cookies()).find((c) => c.name === "pp_theme")!.value).toBe("light");
});

test("a garbage cookie counts as absent", async ({ page, context }) => {
  await context.addCookies([{ name: "pp_theme", value: "solarized", url: "http://127.0.0.1:8099" }]);
  await page.emulateMedia({ colorScheme: "dark" });
  await page.goto("/");
  await expect(html(page)).not.toHaveAttribute("data-theme", /.*/);
  await expect(toggle(page)).toHaveAccessibleName("Light mode");
});

test("the keyboard operates the toggle", async ({ page }) => {
  await page.emulateMedia({ colorScheme: "light" });
  await page.goto("/");
  await toggle(page).focus();
  await page.keyboard.press("Enter");
  await expect(html(page)).toHaveAttribute("data-theme", "dark");
  await expect(toggle(page)).toBeFocused();
  await page.keyboard.press("Space");
  await expect(html(page)).toHaveAttribute("data-theme", "light");
  await expect(toggle(page)).toBeFocused();
});

test("the toggle sits at the right end of the header row, on the logo's row, 44 px tall, with no line wrap on phones", async ({ page }) => {
  for (const width of [390, 1440]) {
    await page.setViewportSize({ width, height: 844 });
    await page.goto("/");
    const link = page.getByRole("link", { name: /shop home/ });
    const lb = (await link.boundingBox())!;
    const tb = (await toggle(page).boundingBox())!;
    expect(tb.height, `${width}`).toBeGreaterThanOrEqual(43.5);
    expect(tb.width, `${width}`).toBeGreaterThanOrEqual(43.5);
    expect(Math.abs(tb.y + tb.height / 2 - (lb.y + lb.height / 2)), `${width} same row`).toBeLessThan(lb.height / 2);
    expect(tb.x, `${width} right of the link`).toBeGreaterThan(lb.x + lb.width - 1);
    // The shop column is capped at --content-max, so "right end" means the main column's right edge.
    const mb = (await page.locator("main").boundingBox())!;
    expect(tb.x + tb.width, `${width} right end`).toBeGreaterThan(mb.x + mb.width - 40);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
  }
});

test("on a phone the label shows on keyboard focus only, and the name is always there", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/");
  const label = toggle(page).locator(".label");
  expect((await label.boundingBox())!.width).toBeLessThanOrEqual(1.5);
  await page.keyboard.press("Tab");
  await toggle(page).focus();
  await page.keyboard.press("Shift+Tab");
  await page.keyboard.press("Tab");
  await expect(toggle(page)).toBeFocused();
  expect((await label.boundingBox())!.width).toBeGreaterThan(20);
  await expect(toggle(page)).toHaveAccessibleName(/mode$/);
});

test("the Closed page and the footer follow the theme, and the toggle is there too", async ({ page, request }) => {
  await setShop(request, { acceptingOrders: false, pausedMessage: "Back Monday" });
  try {
    await page.emulateMedia({ colorScheme: "light" });
    await page.goto("/");
    await expect(page.getByRole("heading", { level: 1, name: "We're closed right now" })).toBeVisible();
    const bg = () => page.evaluate(() => getComputedStyle(document.body).backgroundColor);
    const cardBg = () => page.evaluate(() => getComputedStyle(document.querySelector(".closed-body")!.parentElement!).backgroundColor);
    const footerColor = () => page.evaluate(() => getComputedStyle(document.querySelector("footer")!).color);
    const before = [await bg(), await cardBg(), await footerColor()];
    await toggle(page).click();
    await expect(html(page)).toHaveAttribute("data-theme", "dark");
    const after = [await bg(), await cardBg(), await footerColor()];
    expect(after[0]).not.toBe(before[0]);
    expect(after[1]).not.toBe(before[1]);
    expect(after[2]).not.toBe(before[2]);
    await page.reload();
    expect(await bg()).toBe(after[0]);
  } finally {
    await setShop(request, { acceptingOrders: true, pausedMessage: "" });
  }
});

for (const width of [390, 1440]) {
  for (const scheme of ["light", "dark"] as const) {
    test(`axe passes in ${scheme} at ${width} px`, async ({ page }) => {
      await page.setViewportSize({ width, height: 900 });
      await page.emulateMedia({ colorScheme: scheme });
      await page.goto("/");
      await expect(toggle(page)).toBeVisible();
      await page.waitForLoadState("networkidle");
      await page.evaluate(() => document.fonts.ready);
      const r = await new AxeBuilder({ page }).withTags(TAGS).analyze();
      expect(r.violations.map((v) => `${v.id} (${v.impact}) ${v.nodes[0]?.target.join(" ")}`)).toEqual([]);
      // Toggled (explicit cookie) too.
      await toggle(page).click();
      await page.waitForTimeout(150);
      const r2 = await new AxeBuilder({ page }).withTags(TAGS).analyze();
      expect(r2.violations.map((v) => `${v.id} (${v.impact}) ${v.nodes[0]?.target.join(" ")}`)).toEqual([]);
    });
  }
}

// The footer's content shares the header's width and left edge; the tinted band stays full width.
test("the footer content lines up with the header content at 1440 and 390 px", async ({ page }) => {
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 900 });
    await page.goto("/");
    const link = page.getByRole("link", { name: /shop home/ });
    await expect(link).toBeVisible();
    const head = (await page.locator("header.brand").boundingBox())!;
    const inner = (await page.locator("footer.shop-footer .inner").boundingBox())!;
    const band = (await page.locator("footer.shop-footer").boundingBox())!;
    const pad = await page.locator("footer.shop-footer .inner").evaluate((el) => parseFloat(getComputedStyle(el).paddingLeft));
    const padR = await page.locator("footer.shop-footer .inner").evaluate((el) => parseFloat(getComputedStyle(el).paddingRight));
    const hl = head.x, hr = head.x + head.width;
    expect(Math.abs(inner.x + pad - hl), `${width} left edge`).toBeLessThanOrEqual(1);
    expect(Math.abs(inner.x + inner.width - padR - hr), `${width} right edge`).toBeLessThanOrEqual(1);
    expect(band.width, `${width} band is full width`).toBeGreaterThanOrEqual(width - 1);
  }
});
