import { test, expect } from "@playwright/test";
import type { APIRequestContext, Page } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

// D43: the Dashboard's Recent invoices card matches the Revenue by month card's height and scrolls inside.

const TAGS = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"];

async function axeClean(page: Page, label: string): Promise<void> {
  await page.waitForLoadState("networkidle");
  await page.evaluate(() => document.fonts.ready);
  const results = await new AxeBuilder({ page }).withTags(TAGS).analyze();
  expect(
    results.violations.map((v) => `${label}: ${v.id} (${v.impact}) ${v.nodes[0]?.target.join(" ")}`),
  ).toEqual([]);
}

test.describe("D43 Dashboard recent invoices", () => {
  const created: string[] = [];
  test.beforeAll(async ({ request }: { request: APIRequestContext }) => {
    for (let i = 0; i < 12; i++) {
      const res = await request.post("/admin/api/invoices", {
        data: { clientName: `D43 Client ${i}`, lines: [{ description: "Widget", quantity: 1, unitPriceCents: 1000 + i }] },
      });
      expect(res.status(), await res.text()).toBe(201);
      created.push(((await res.json()) as { id: string }).id);
    }
  });
  test.afterAll(async ({ request }: { request: APIRequestContext }) => {
    for (const id of created) await request.delete(`/admin/api/invoices/${id}`);
  });

  test("at 1440 px the two cards match in height, the list scrolls inside, and the page does not grow", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto("/admin/#/dashboard");
    const chart = page.locator(".cards > .card").first();
    const recent = page.locator(".cards > .card").nth(1);
    await expect(recent.getByRole("heading", { name: "Recent invoices" })).toBeVisible();
    const region = recent.getByRole("region", { name: "Recent invoices" });
    await expect(region).toBeVisible();
    // Each row shows its invoice date.
    const invRows = region.locator("a.inv-row");
    expect(await invRows.locator("time[datetime]").count()).toBe(await invRows.count());
    const [cb, rb] = [(await chart.boundingBox())!, (await recent.boundingBox())!];
    expect(Math.abs(cb.height - rb.height)).toBeLessThanOrEqual(1);
    const scroll = await region.evaluate((el) => ({ sh: el.scrollHeight, ch: el.clientHeight, ov: getComputedStyle(el).overflowY }));
    expect(scroll.sh).toBeGreaterThan(scroll.ch);
    expect(scroll.ov).toBe("auto");
    // Focusable and scrollable by keyboard.
    await region.focus();
    await page.keyboard.press("PageDown");
    await expect.poll(() => region.evaluate((el) => el.scrollTop)).toBeGreaterThan(0);
    // The card is not taller than the chart card, so the 12 rows (10 shown) did not stretch the row.
    const after = (await recent.boundingBox())!;
    expect(after.height).toBeLessThanOrEqual(cb.height + 1);
    const rows = await recent.locator("li").count();
    expect(rows).toBeGreaterThan(6);
  });

  test("on one column the list is capped at about six rows and scrolls", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto("/admin/#/dashboard");
    const region = page.getByRole("region", { name: "Recent invoices" });
    await expect(region).toBeVisible();
    const m = await region.evaluate((el) => {
      const li = el.querySelector("li")!.getBoundingClientRect().height;
      return { sh: el.scrollHeight, ch: el.clientHeight, li };
    });
    expect(m.sh).toBeGreaterThan(m.ch);
    expect(m.ch / m.li).toBeLessThanOrEqual(7);
    expect(m.ch / m.li).toBeGreaterThanOrEqual(4);
  });

  test("the Dashboard passes axe in both themes at 1440 and 390 px", async ({ page }) => {
    for (const vp of [{ width: 1440, height: 900 }, { width: 390, height: 844 }]) {
      await page.setViewportSize(vp);
      await page.goto("/admin/#/dashboard");
      await expect(page.getByRole("region", { name: "Recent invoices" })).toBeVisible();
      for (const scheme of ["light", "dark"] as const) {
        await page.emulateMedia({ colorScheme: scheme });
        await axeClean(page, `dashboard ${scheme} ${vp.width}`);
      }
    }
    await page.emulateMedia({ colorScheme: "light" });
  });
});
