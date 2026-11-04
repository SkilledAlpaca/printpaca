import { test, expect } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { expandSummary, fillCanonicalQuote } from "./helpers";

// The v1.2 New quote layout (D23): three numbered sections, parts as tabs, a sticky summary beside the form from
// 1100px, and a compact summary pinned to the bottom below it.

const TAGS = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"];

test("the form has three numbered sections and the summary holds the save fields", async ({ page }) => {
  await page.setViewportSize({ width: 1366, height: 768 });
  await page.goto("/admin/");
  for (const legend of ["1. Model and print time", "2. Material and printer", "3. Price"]) {
    await expect(page.getByRole("group", { name: legend })).toBeVisible();
  }
  const summary = page.getByRole("group", { name: "Quote summary" });
  await expect(summary.getByLabel("Client for this quote")).toBeVisible();
  await expect(summary.getByLabel("Label for this quote")).toBeVisible();
  await expect(summary.getByRole("button", { name: "Save quote" })).toBeVisible();
  // No compact bar at this width.
  await expect(page.getByRole("button", { name: /^Expand/ })).toHaveCount(0);
});

test("two columns from 1100px: the summary sits right of the form and stays reachable while the page scrolls", async ({ page }) => {
  await page.setViewportSize({ width: 1366, height: 768 });
  await page.goto("/admin/");
  await fillCanonicalQuote(page);
  const form = await page.getByRole("group", { name: "1. Model and print time" }).boundingBox();
  const summary = page.getByRole("group", { name: "Quote summary" });
  const box = await summary.boundingBox();
  expect(box!.x).toBeGreaterThan(form!.x + form!.width - 1);

  await page.getByLabel("Set a selling price").check();
  await page.locator("#main").evaluate((el) => (el.scrollTop = el.scrollHeight));
  const save = summary.getByRole("button", { name: "Save quote" });
  await expect(save).toBeInViewport();
  const after = await summary.boundingBox();
  expect(after!.y + after!.height).toBeLessThanOrEqual(768);
});

test("below 1100px one column, with a compact summary pinned to the bottom that expands to the full one", async ({ page }) => {
  await page.setViewportSize({ width: 844, height: 390 });
  await page.goto("/admin/");
  await fillCanonicalQuote(page);
  await page.getByLabel("Set a selling price").check();

  const summary = page.getByRole("group", { name: "Quote summary" });
  const expand = summary.getByRole("button", { name: /^Expand/ });
  await expect(expand).toBeVisible();
  await expect(expand).toHaveAttribute("aria-expanded", "false");
  // The bar shows the price, profit and Save, and sits on the bottom edge of the viewport.
  const barStats = summary.locator(".bar");
  await expect(barStats.getByText("Price", { exact: true })).toBeVisible();
  await expect(barStats.getByText("Profit", { exact: true })).toBeVisible();
  const save = summary.getByRole("button", { name: "Save", exact: true });
  await expect(save).toBeInViewport();
  const bar = await summary.boundingBox();
  expect(Math.round(bar!.y + bar!.height)).toBe(390);
  await expect(summary.getByLabel("Label for this quote")).toBeHidden();

  await expand.click();
  await expect(expand).toHaveAttribute("aria-expanded", "true");
  await expect(summary.getByLabel("Label for this quote")).toBeVisible();
  await expect(summary.getByRole("button", { name: "Save quote" })).toBeVisible();
  await expect(save).toBeInViewport();
  await expand.click();
  await expect(summary.getByLabel("Label for this quote")).toBeHidden();
});

test("the pinned bar never covers a focused field", async ({ page }) => {
  await page.setViewportSize({ width: 844, height: 390 });
  await page.goto("/admin/");
  const summary = page.getByRole("group", { name: "Quote summary" });
  // Tab through the form from the top: every field that takes focus ends up above the bar.
  await page.getByLabel(/Model weight/).focus();
  for (let i = 0; i < 12; i++) {
    await page.keyboard.press("Tab");
    const info = await page.evaluate(() => {
      const el = document.activeElement as HTMLElement;
      const bar = document.querySelector<HTMLElement>("[aria-label='Quote summary']")!;
      if (bar.contains(el)) return null;
      return { bottom: el.getBoundingClientRect().bottom, barTop: bar.getBoundingClientRect().top };
    });
    if (info) expect(info.bottom, "focused control bottom edge").toBeLessThanOrEqual(info.barTop + 1);
  }
  await expect(summary).toBeVisible();
});

test("saving from the compact bar works", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/admin/");
  await fillCanonicalQuote(page);
  const label = `e2e-compact-${Date.now()}`;
  await expandSummary(page);
  await page.getByLabel("Label for this quote").fill(label);
  await page.getByRole("button", { name: "Collapse" }).click();
  await page.getByRole("button", { name: "Save", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: `Edit quote: ${label}` })).toBeVisible();
  const list = await (await request.get("/admin/api/quotes")).json();
  const saved = list.quotes.find((q: { label: string }) => q.label === label);
  expect(saved).toBeTruthy();
  await request.delete(`/admin/api/quotes/${saved.id}`);
});

test("parts are tabs: arrow keys, Home and End move between them, and removing a part keeps focus on a tab", async ({ page }) => {
  await page.setViewportSize({ width: 1366, height: 768 });
  await page.goto("/admin/");
  await page.getByRole("button", { name: "Add part" }).click();
  await page.getByRole("button", { name: "+ Add part" }).click();
  const tablist = page.getByRole("tablist", { name: "Parts" });
  const tabs = tablist.getByRole("tab");
  await expect(tabs).toHaveCount(3);
  // "+ Add part" follows the tabs and is not one of them.
  await expect(tablist.getByRole("button")).toHaveCount(0);
  await expect(tabs.nth(2)).toHaveAttribute("aria-selected", "true");

  await tabs.nth(2).focus();
  await page.keyboard.press("ArrowLeft");
  await expect(tabs.nth(1)).toBeFocused();
  await expect(tabs.nth(1)).toHaveAttribute("aria-selected", "true");
  await page.keyboard.press("Home");
  await expect(tabs.nth(0)).toBeFocused();
  await page.keyboard.press("End");
  await expect(tabs.nth(2)).toBeFocused();
  await page.keyboard.press("ArrowRight");
  await expect(tabs.nth(0)).toBeFocused();
  // Only the selected tab is in the Tab order.
  await expect(tabs.nth(0)).toHaveAttribute("tabindex", "0");
  await expect(tabs.nth(1)).toHaveAttribute("tabindex", "-1");

  // The panel is named by its tab.
  await expect(page.getByRole("tabpanel", { name: /^Part 1/ })).toBeVisible();

  // Removing asks first, and focus lands on a tab that still exists.
  await tabs.nth(1).click();
  await page.getByRole("button", { name: "Remove Part 2", exact: true }).click();
  await page.getByRole("button", { name: "Remove Part 2?" }).click();
  await expect(tabs).toHaveCount(2);
  await expect(tablist.getByRole("tab", { selected: true })).toBeFocused();

  const results = await new AxeBuilder({ page }).withTags(TAGS).analyze();
  expect(results.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(" ")).join(", ")}`)).toEqual([]);
});

test("a missing-field link inside More pricing options opens it and focuses the field", async ({ page }) => {
  await page.setViewportSize({ width: 1366, height: 768 });
  await page.goto("/admin/");
  await fillCanonicalQuote(page);
  await page.getByLabel("Set a selling price").check();
  const more = page.locator("details.more-pricing");
  await more.locator("summary").click();
  await page.getByLabel(/^Tax \(included in price\)/).fill("150");
  await more.locator("summary").click();
  await expect(more).not.toHaveAttribute("open", "");
  const link = page.locator("#pricing-card .blockers").getByRole("link", { name: /Tax/ });
  await expect(link).toBeVisible();
  await link.click();
  await expect(more).toHaveAttribute("open", "");
  await expect(page.getByLabel(/^Tax \(included in price\)/)).toBeFocused();
});
