import { test, expect } from "@playwright/test";
import { navButton, gotoSettings } from "./helpers";
import { geometryOnly3mf } from "./geometry3mf";

test("a wheel over the right gutter scrolls main at 1920 px", async ({ page }) => {
  await page.setViewportSize({ width: 1920, height: 1080 });
  await page.goto("/admin/");
  await gotoSettings(page, { load: false });
  const main = page.locator("main#main");
  await expect(page.getByRole("heading", { name: "Settings", level: 1 })).toBeVisible();

  // main spans its grid cell to the window edge; the 1440 px column sits inside it.
  const box = await main.boundingBox();
  expect(box).not.toBeNull();
  expect(Math.round(box!.x + box!.width)).toBe(1920);
  const inner = await page.locator(".main-inner").boundingBox();
  expect(inner!.width).toBeLessThanOrEqual(1440);

  expect(await main.evaluate((el) => el.scrollTop)).toBe(0);
  await page.mouse.move(1900, 500); // inside the right gutter, outside the 1440 px column
  await page.mouse.wheel(0, 600);
  await expect.poll(() => main.evaluate((el) => el.scrollTop)).toBeGreaterThan(0);
});

test("the Settings section rail stays in view at 1366x768, scrolls on its own, and reaches every card", async ({ page }) => {
  await page.setViewportSize({ width: 1366, height: 768 });
  await page.goto("/admin/");
  await gotoSettings(page, { load: false });
  const nav = page.getByRole("navigation", { name: "Settings sections" });
  await expect(nav).toBeVisible();
  // A rail beside the cards, not a bar across the top: a narrow share of the width, so the cards keep the rest.
  const first = (await nav.boundingBox())!;
  expect(first.width / 1366).toBeLessThan(0.2);
  await page.locator("main#main").evaluate((el) => (el.scrollTop = 1200));
  // Sticky: still on screen after scrolling, and never taller than the screen.
  await expect.poll(async () => (await nav.boundingBox())!.y).toBeLessThan(40);
  const box = (await nav.boundingBox())!;
  expect(box.y).toBeGreaterThanOrEqual(0);
  expect(box.y + box.height).toBeLessThanOrEqual(768);
  expect(await nav.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);

  // Every section is reachable from the rail, the last one included, and its heading lands in view.
  for (const name of ["Storefront", "Hardware", "Diagnostics"]) {
    const link = nav.getByRole("button", { name });
    await link.scrollIntoViewIfNeeded();
    await link.click();
    const id = { Storefront: "#section-storefront", Hardware: "#section-hardware", Diagnostics: "#section-diagnostics" }[name]!;
    await expect
      .poll(async () => {
        const heading = (await page.locator(`${id} h2`).first().boundingBox())!;
        return heading.y >= 0 && heading.y + heading.height <= 768;
      })
      .toBe(true);
  }
});

test("on a phone the Settings sections are a collapsed list that closes after a jump", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/admin/#/settings");
  const nav = page.getByRole("navigation", { name: "Settings sections" });
  await expect(nav).toBeVisible();
  expect(await nav.evaluate((el) => getComputedStyle(el).position)).toBe("static");
  const toggle = nav.getByRole("button", { name: "Jump to a section" });
  await expect(toggle).toHaveAttribute("aria-expanded", "false");
  await expect(nav.getByRole("button", { name: "Hardware" })).toHaveCount(0);
  // The collapsed control is a full-size tap target.
  expect((await toggle.boundingBox())!.height).toBeGreaterThanOrEqual(44);

  await toggle.click();
  await expect(toggle).toHaveAttribute("aria-expanded", "true");
  await nav.getByRole("button", { name: "Hardware" }).click();
  await expect(toggle).toHaveAttribute("aria-expanded", "false");
  await expect(page.locator("#section-hardware h2")).toBeFocused();
  // No sideways scrolling anywhere.
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
});

test("stepping Solidity never moves the field", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/admin/");
  await navButton(page, "New quote").click();
  await page.locator('input[type="file"]').first().setInputFiles({
    name: "cube.3mf",
    mimeType: "model/3mf",
    buffer: geometryOnly3mf(),
  });
  const solidity = page.getByLabel("Solidity");
  await expect(solidity).toBeVisible();
  // Slow the server so "Updating price…" is on screen for part of the run.
  await page.route("**/api/quote/compute", async (route) => {
    await new Promise((resolve) => setTimeout(resolve, 500));
    await route.continue();
  });
  await solidity.focus();
  const y0 = (await solidity.boundingBox())!.y;
  for (let i = 0; i < 5; i++) {
    await page.keyboard.press("ArrowUp");
    await page.waitForTimeout(200);
    expect((await solidity.boundingBox())!.y).toBe(y0);
  }
  await expect(page.getByText("Updating price…")).toBeHidden();
  expect((await solidity.boundingBox())!.y).toBe(y0);
});

test("a geometry-only 3MF explains the missing print time and the pricing panel links to it", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/admin/");
  await navButton(page, "New quote").click();
  await page.locator('input[type="file"]').first().setInputFiles({ name: "cube.3mf", mimeType: "model/3mf", buffer: geometryOnly3mf() });
  await expect(page.getByLabel("Solidity")).toBeVisible();

  const hours = page.getByLabel("Print time (hours)");
  const minutes = page.getByLabel("Print time (minutes)");
  const note = page.getByText("This file has no slicer time. Enter the print time from your slicer.");
  await expect(note).toBeVisible();
  await expect(hours).toHaveAttribute("aria-describedby", /print-time-note/);
  await expect(minutes).toHaveAttribute("aria-describedby", /print-time-note/);
  // Quiet until the user leaves the fields.
  await expect(hours).not.toHaveAttribute("aria-invalid", "true");

  await hours.focus();
  await minutes.focus(); // moving between the halves is not leaving
  await expect(minutes).not.toHaveAttribute("aria-invalid", "true");
  await page.getByLabel("Quantity").focus();
  await expect(hours).toHaveAttribute("aria-invalid", "true");
  await expect(page.getByText("Enter the print time.")).toBeVisible();

  // The pricing panel names the blocker, and the link focuses the field.
  await page.getByLabel("Set a selling price").check();
  const link = page.getByRole("link", { name: "Print time", exact: true });
  await expect(page.getByText(/To price this quote, add:/)).toBeVisible();
  await expect(page.getByText("Pricing is incomplete")).toHaveCount(0);
  await link.click();
  await expect(hours).toBeFocused();

  // Entering a time clears the note and the blocker.
  await hours.fill("2");
  await expect(note).toHaveCount(0);
  await expect(page.getByText(/To price this quote, add:/)).toHaveCount(0);
});

test("the admin brand shows the business name and logo, links home, and updates after a save", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/admin/");
  const sidebar = page.getByRole("complementary");
  await expect(sidebar.getByRole("link", { name: "printpaca home" })).toBeVisible();
  await expect(page).toHaveTitle(/ · printpaca$/);

  await gotoSettings(page, { load: false });
  await page.getByLabel("Business name").fill("Alpaca Prints");
  await page.getByLabel("Logo URL").fill("/no-such-logo.png");
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByText("Settings saved.")).toBeVisible();

  // No reload: the brand, the title, and the logo fallback (the URL 404s) all follow the saved settings.
  const brand = sidebar.getByRole("link", { name: "Alpaca Prints home" });
  await expect(brand).toBeVisible();
  await expect(brand).toHaveAttribute("href", "#/dashboard");
  await expect(page).toHaveTitle("Settings · Alpaca Prints");
  await expect.poll(() => brand.locator("img.logo").getAttribute("src")).not.toContain("no-such-logo");

  // Activating it opens the Dashboard (D22) and focuses that view's h1.
  await brand.click();
  await expect(page).toHaveURL(/#\/dashboard$/);
  await expect(page.getByRole("heading", { level: 1, name: "Dashboard" })).toBeFocused();
  await expect(page).toHaveTitle("Dashboard · Alpaca Prints");

  // Mobile: the header copy of the link closes nothing when the drawer is shut, and the drawer copy closes it.
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/admin/#/settings");
  await page.getByRole("button", { name: /open menu/i }).click();
  await page.getByRole("complementary").getByRole("link", { name: "Alpaca Prints home" }).click();
  await expect(page.getByRole("button", { name: /open menu/i })).toBeVisible();
  await expect(page.getByRole("heading", { level: 1, name: "Dashboard" })).toBeFocused();

  // Restore the defaults for the specs that follow.
  await page.setViewportSize({ width: 1440, height: 900 });
  await gotoSettings(page, { load: false });
  await page.getByLabel("Business name").fill("");
  await page.getByLabel("Logo URL").fill("");
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByText("Settings saved.")).toBeVisible();
  await expect(sidebar.getByRole("link", { name: "printpaca home" })).toBeVisible();
});

test("the Sell group lists New quote, Dashboard, Quotes, Requests, Invoices, Shop items, Clients in that order", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/admin/");
  const names = await page.getByRole("navigation", { name: "Sections" }).getByRole("group", { name: "Sell" }).getByRole("button").evaluateAll(
    (els) => els.map((el) => (el.querySelector(".label")?.textContent ?? "").trim()),
  );
  expect(names).toEqual(["New quote", "Dashboard", "Quotes", "Requests", "Invoices", "Shop items", "Clients"]);
});

test("at 1366x768 every nav item can be reached, and the footer controls stay inside the sidebar", async ({ page }) => {
  await page.setViewportSize({ width: 1366, height: 768 });
  await page.goto("/admin/#/dashboard");
  const nav = page.getByRole("navigation", { name: "Sections" });
  const labels = ["New quote", "Dashboard", "Quotes", "Requests", "Invoices", "Shop items", "Clients", "Filament", "Electricity", "Printers", "Settings"];
  for (const name of labels) {
    const item = nav.getByRole("button", { name, exact: true });
    await item.scrollIntoViewIfNeeded();
    const hit = await item.evaluate((el) => {
      const r = el.getBoundingClientRect();
      const top = document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2);
      return el === top || el.contains(top);
    });
    expect(hit, `${name} is clickable`).toBe(true);
  }
  const aside = (await page.getByRole("complementary").boundingBox())!;
  for (const name of ["Guide", "Compact density", "Dark theme", "Collapse sidebar"]) {
    const b = (await page.getByRole("complementary").getByRole("button", { name }).boundingBox())!;
    expect(b.y + b.height, `${name} bottom edge`).toBeLessThanOrEqual(aside.y + aside.height + 0.5);
  }
});
