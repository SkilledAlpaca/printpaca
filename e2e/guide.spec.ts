import AxeBuilder from "@axe-core/playwright";
import { test, expect, type Locator, type Page } from "@playwright/test";
import { GUIDE_NAV, GUIDE_TASKS } from "../web/src/lib/guideContent";
import { plainInline } from "../web/src/lib/guideInline";

const AXE_TAGS = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"];

test("guide is reachable as its own page via the sidebar footer link", async ({ page }) => {
  await page.goto("/admin/");
  await expect(page.getByRole("heading", { name: "Guide", level: 1 })).toHaveCount(0);

  // Guide isn't a nav destination; it's a sidebar-footer button that calls activate("guide").
  await page.getByRole("button", { name: "Guide", exact: true }).click();
  await expect(page).toHaveURL(/#\/guide$/);
  await expect(page.getByRole("heading", { name: "Guide", level: 1 })).toBeVisible();
  // With no topic in the address, the first task opens.
  await expect(page.getByRole("heading", { name: GUIDE_TASKS[0]!.title, level: 2 })).toBeVisible();
});

test.describe("every task topic opens from its deep link", () => {
  for (const task of GUIDE_TASKS) {
    test(`#/guide#${task.id}`, async ({ page }) => {
      await page.goto(`/admin/#/guide#${task.id}`);
      await expect(page.getByRole("heading", { name: task.title, level: 2 })).toBeVisible();
      await expect(page.locator("ol.guide-steps > li").first()).toContainText(plainInline(task.steps[0]!.text));
      await expect(page.locator("ol.guide-steps > li")).toHaveCount(task.steps.length);
      await expect(page.locator(".topic-link[aria-current='page']")).toHaveText(task.title);
    });
  }
});

test("v1.1 concept anchors still open their topic", async ({ page }) => {
  for (const [id, title] of [
    ["margin-vs-markup", "Margin versus markup"],
    ["model-files", "Model files and print time"],
    ["requests-finalize-vs-convert", "Requests: Finalize or Convert"],
    ["spoolman", "Spoolman"],
    ["users-and-permissions", "Users and permissions"],
    ["keyboard-shortcuts", "Keyboard shortcuts"],
  ] as const) {
    await page.goto(`/admin/#/guide#${id}`);
    await expect(page.getByRole("heading", { name: title, level: 2 })).toBeVisible();
  }
  await expect(page.getByRole("table")).toContainText("Ctrl+S");
});

test("an unknown topic opens the first task and keeps the page working", async ({ page }) => {
  await page.goto("/admin/#/guide#no-such-topic");
  await expect(page.getByRole("heading", { name: GUIDE_TASKS[0]!.title, level: 2 })).toBeVisible();
});

test("desktop: the topic list sits beside the content and switches topics", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.goto("/admin/#/guide");
  const nav = page.getByRole("navigation", { name: "Guide topics" });
  await expect(nav).toBeVisible();
  await expect(nav.getByRole("button")).toHaveCount(0);
  const count = GUIDE_NAV.reduce((n, g) => n + g.items.length, 0);
  await expect(nav.getByRole("link")).toHaveCount(count);
  const [navBox, bodyBox] = await Promise.all([nav.boundingBox(), page.locator(".topic-body").boundingBox()]);
  expect(navBox!.x + navBox!.width).toBeLessThanOrEqual(bodyBox!.x + 1);

  await nav.getByRole("link", { name: "Quote a print" }).click();
  await expect(page).toHaveURL(/#\/guide#quote-a-print$/);
  const heading = page.getByRole("heading", { name: "Quote a print", level: 2 });
  await expect(heading).toBeVisible();
  await expect(heading).toBeFocused();
  await expect(nav.getByRole("link", { name: "Quote a print" })).toHaveAttribute("aria-current", "page");
  // A link inside a topic switches topics the same way.
  await page.locator(".next a").first().click();
  await expect(page).toHaveURL(/#\/guide#save-and-reopen-quote$/);
  await expect(page.getByRole("heading", { name: "Save, send, and reopen a quote", level: 2 })).toBeVisible();
});

test("phone: the topic list is a disclosure that closes after a choice", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/admin/#/guide");
  const toggle = page.getByRole("button", { name: /^Choose a topic/ });
  await expect(toggle).toHaveAttribute("aria-expanded", "false");
  await expect(page.getByRole("link", { name: "Read the Dashboard" })).toHaveCount(0);
  await toggle.click();
  await expect(toggle).toHaveAttribute("aria-expanded", "true");
  await page.getByRole("link", { name: "Read the Dashboard" }).click();
  await expect(page.getByRole("heading", { name: "Read the Dashboard", level: 2 })).toBeVisible();
  await expect(toggle).toHaveAttribute("aria-expanded", "false");
  await expect(toggle).toContainText("Read the Dashboard");
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(overflow).toBeLessThanOrEqual(0);
});

test("images load lazily, carry alt text, and reserve their size", async ({ page }) => {
  await page.goto("/admin/#/guide#set-up-storefront");
  const images = page.locator("img.shot");
  const expected = GUIDE_TASKS.find((t) => t.id === "set-up-storefront")!.steps.filter((s) => s.image).length;
  await expect(images).toHaveCount(expected);
  for (let i = 0; i < expected; i++) {
    const img = images.nth(i);
    await expect(img).toHaveAttribute("loading", "lazy");
    await expect(img).toHaveAttribute("width", /^\d+$/);
    await expect(img).toHaveAttribute("height", /^\d+$/);
    expect(((await img.getAttribute("alt")) ?? "").length).toBeGreaterThan(20);
  }
  // The last image is below the fold: scroll to it, and it loads at the size its attributes reserved.
  const last = images.last();
  await last.scrollIntoViewIfNeeded();
  await expect.poll(() => last.evaluate((el: HTMLImageElement) => el.naturalWidth)).toBeGreaterThan(0);
  const dims = await last.evaluate((el: HTMLImageElement) => ({ nw: el.naturalWidth, nh: el.naturalHeight, w: Number(el.getAttribute("width")), h: Number(el.getAttribute("height")) }));
  expect(dims.nw).toBe(dims.w);
  expect(dims.nh).toBe(dims.h);
});

test("dark mode gives images a border, light mode does not", async ({ page }) => {
  await page.goto("/admin/#/guide#quote-a-print");
  const img = page.locator("img.shot").first();
  await page.emulateMedia({ colorScheme: "light" });
  expect(await img.evaluate((el) => getComputedStyle(el).borderTopWidth)).toBe("0px");
  await page.emulateMedia({ colorScheme: "dark" });
  expect(await img.evaluate((el) => getComputedStyle(el).borderTopWidth)).toBe("1px");
});

async function axe(page: Page, label: string): Promise<string[]> {
  await page.waitForLoadState("networkidle");
  await page.evaluate(() => document.fonts.ready);
  const results = await new AxeBuilder({ page }).withTags(AXE_TAGS).analyze();
  return results.violations.map((v) => `${label}: ${v.id} (${v.impact}), ${v.nodes.length} node(s), e.g. ${v.nodes[0]?.target.join(" ")}`);
}

test("axe: a task, a concept, and the shortcuts pass in both themes at desktop and phone width", async ({ page }) => {
  test.setTimeout(120_000);
  const problems: string[] = [];
  for (const topic of ["quote-a-print", "set-up-storefront", "margin-vs-markup", "keyboard-shortcuts"]) {
    for (const vp of [{ width: 1280, height: 900 }, { width: 390, height: 844 }]) {
      await page.setViewportSize(vp);
      await page.goto(`/admin/#/guide#${topic}`);
      await expect(page.locator("#guide-heading")).toBeVisible();
      for (const scheme of ["light", "dark"] as const) {
        await page.emulateMedia({ colorScheme: scheme });
        problems.push(...(await axe(page, `guide#${topic} [${scheme}, ${vp.width}]`)));
      }
      if (vp.width === 390) {
        await page.getByRole("button", { name: /^Choose a topic/ }).click();
        problems.push(...(await axe(page, `guide#${topic} [topic list open, 390]`)));
      }
    }
  }
  expect(problems).toEqual([]);
});

test("the public shop never loads a Guide image", async ({ page, request }) => {
  // Guide images are admin-only: they must not be reachable from the public entry's assets. Collect every
  // asset the public page requests, and read the public scripts and styles for any .webp reference.
  const requested: string[] = [];
  page.on("request", (r) => requested.push(r.url()));
  await page.goto("http://127.0.0.1:8098/order");
  await expect(page.getByRole("heading", { name: "printpaca" })).toBeVisible();
  expect(requested.filter((u) => /\.webp(\?|$)/.test(u))).toEqual([]);
  for (const url of requested.filter((u) => /\/assets\/.*\.(js|css)$/.test(u))) {
    const res = await request.get(url);
    expect(await res.text(), url).not.toContain(".webp");
  }
});

test.describe("image lightbox", () => {
  const TOPIC = "/admin/#/guide#quote-a-print";
  const enlargeButtons = (page: Page) => page.getByRole("button", { name: /^Enlarge: / });

  test("every image is an Enlarge button named after its alt text", async ({ page }) => {
    await page.goto(TOPIC);
    const imgs = page.locator("img.shot");
    const n = await imgs.count();
    expect(n).toBeGreaterThan(0);
    await expect(enlargeButtons(page)).toHaveCount(n);
    for (let i = 0; i < n; i++) {
      const alt = (await imgs.nth(i).getAttribute("alt"))!;
      await expect(page.getByRole("button", { name: `Enlarge: ${alt}`, exact: true })).toHaveCount(1);
    }
  });

  for (const how of ["click", "Enter"] as const) {
    test(`${how} opens the full-size image, Escape closes it, and focus returns`, async ({ page }) => {
      await page.setViewportSize({ width: 1280, height: 900 });
      await page.goto(TOPIC);
      const btn = enlargeButtons(page).first();
      const inline = await btn.locator("img").evaluate((el) => el.getBoundingClientRect().width);
      if (how === "click") await btn.click();
      else {
        await btn.focus();
        await page.keyboard.press("Enter");
      }
      const dialog = page.getByRole("dialog");
      await expect(dialog).toBeVisible();
      await expect(dialog.getByRole("button", { name: "Close" })).toBeFocused();
      const img = dialog.locator("img");
      await expect.poll(() => img.evaluate((el: HTMLImageElement) => el.naturalWidth)).toBeGreaterThan(0);
      const w = await img.evaluate((el: HTMLImageElement) => ({ nat: el.naturalWidth, shown: el.getBoundingClientRect().width }));
      expect(w.nat).toBeGreaterThan(inline);
      expect(w.shown).toBe(w.nat);
      await page.keyboard.press("Escape");
      await expect(dialog).toHaveCount(0);
      await expect(btn).toBeFocused();
    });
  }

  test("the focus trap holds and a backdrop click closes", async ({ page }) => {
    await page.goto(TOPIC);
    const btn = enlargeButtons(page).first();
    await btn.click();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toBeVisible();
    for (let i = 0; i < 6; i++) {
      await page.keyboard.press(i % 2 ? "Shift+Tab" : "Tab");
      expect(await page.evaluate(() => !!document.activeElement?.closest("dialog"))).toBe(true);
    }
    await page.mouse.click(2, 2);
    await expect(dialog).toHaveCount(0);
    await expect(btn).toBeFocused();
  });

  test("at 390 px the dialog fits the viewport and the image scrolls", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto(TOPIC);
    await enlargeButtons(page).first().click();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toBeVisible();
    const box = (await dialog.boundingBox())!;
    expect(box.x).toBeGreaterThanOrEqual(0);
    expect(box.x + box.width).toBeLessThanOrEqual(390);
    expect(box.y + box.height).toBeLessThanOrEqual(844);
    const region = dialog.getByRole("region");
    await expect.poll(() => region.evaluate((el) => el.scrollWidth > el.clientWidth)).toBe(true);
    await region.evaluate((el) => el.scrollTo(50, 0));
    expect(await region.evaluate((el) => el.scrollLeft)).toBeGreaterThan(0);
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(0);
  });

  test("axe: passes in both themes with the dialog open", async ({ page }) => {
    await page.goto(TOPIC);
    await enlargeButtons(page).first().click();
    await expect(page.getByRole("dialog")).toBeVisible();
    const problems: string[] = [];
    for (const scheme of ["light", "dark"] as const) {
      await page.emulateMedia({ colorScheme: scheme });
      problems.push(...(await axe(page, `lightbox [${scheme}]`)));
    }
    expect(problems).toEqual([]);
  });
});

// D59: the secondary nav follows the reader.
async function fullyVisibleIn(container: Locator, entry: Locator): Promise<boolean> {
  const [c, e] = await Promise.all([container.boundingBox(), entry.boundingBox()]);
  if (!c || !e) return false;
  return e.y >= c.y - 0.5 && e.y + e.height <= c.y + c.height + 0.5;
}

test.describe("secondary nav follows the reader (D59)", () => {
  test.use({ viewport: { width: 1366, height: 768 } });

  test("Guide: a text link to a far topic, a deep link, and Back keep the entry visible", async ({ page }) => {
    await page.goto("/admin/#/guide#invoice-and-payment");
    const nav = page.getByRole("navigation", { name: "Guide topics" });
    await expect(nav.getByRole("link", { name: "Turn a quote into an invoice and record a payment" })).toHaveAttribute("aria-current", "page");
    await page.locator(".next a", { hasText: "Read the Dashboard" }).click();
    const dash = nav.getByRole("link", { name: "Read the Dashboard" });
    await expect(dash).toHaveAttribute("aria-current", "page");
    await expect.poll(() => fullyVisibleIn(nav, dash)).toBe(true);
    expect(await page.evaluate(() => window.scrollY)).toBe(0);

    await page.goto("/admin/#/guide#keyboard-shortcuts");
    await page.reload();
    const keys = nav.getByRole("link", { name: "Keyboard shortcuts" });
    await expect(keys).toHaveAttribute("aria-current", "page");
    await expect.poll(() => fullyVisibleIn(nav, keys)).toBe(true);

    await page.goBack();
    const prev = nav.getByRole("link", { name: "Read the Dashboard" });
    await expect(prev).toHaveAttribute("aria-current", "page");
    await expect.poll(() => fullyVisibleIn(nav, prev)).toBe(true);
  });

  test("Settings: the last section and Back keep the rail entry visible", async ({ page }) => {
    await page.goto("/admin/#/settings");
    const nav = page.getByRole("navigation", { name: "Settings sections" });
    await expect(nav).toBeVisible();
    await page.evaluate(() => (location.hash = "#/settings/diagnostics"));
    const last = nav.getByRole("button", { name: "Diagnostics" });
    await expect(last).toHaveAttribute("aria-current", "true");
    await expect.poll(() => fullyVisibleIn(nav, last)).toBe(true);
    await page.evaluate(() => (location.hash = "#/settings/currency"));
    const cur = nav.getByRole("button", { name: "Currency" });
    await expect(cur).toHaveAttribute("aria-current", "true");
    await page.goBack();
    await expect(last).toHaveAttribute("aria-current", "true");
    await expect.poll(() => fullyVisibleIn(nav, last)).toBe(true);
    // Clicking a jump button marks it too.
    await nav.getByRole("button", { name: "Hardware" }).click();
    await expect(nav.getByRole("button", { name: "Hardware" })).toHaveAttribute("aria-current", "true");
  });

  test("phone: the opened disclosures show the current entry", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto("/admin/#/guide#read-the-dashboard");
    await page.getByRole("button", { name: /^Choose a topic/ }).click();
    await expect(page.getByRole("link", { name: "Read the Dashboard" })).toHaveAttribute("aria-current", "page");
    await page.goto("/admin/#/settings/diagnostics");
    await page.reload();
    await page.getByRole("button", { name: "Jump to a section" }).click();
    await expect(page.getByRole("button", { name: "Diagnostics" })).toHaveAttribute("aria-current", "true");
  });
});
