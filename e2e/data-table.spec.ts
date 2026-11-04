import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

// v1.2 money lane: the DataTable scroll region (D24) on a long list. Clients is the long list: it needs no
// other fixtures. Every record is deleted again, because clients.spec.ts and others expect an empty list. The
// full-width KeyValueTable (D28) is asserted in money-requests.spec.ts. Layout is asserted with ratios and
// positions, never raw pixel heights.

const PREFIX = "DT Client";
const COUNT = 40;
const ids: string[] = [];

test.beforeAll(async ({ request }) => {
  for (let i = 0; i < COUNT; i++) {
    const name = `${PREFIX} ${String(i).padStart(2, "0")}`;
    const res = await request.post("/admin/api/clients", {
      data: { name, email: `dt${i}@example.com`, phone: "", billingAddress: "", shippingAddress: "", notes: "" },
    });
    expect(res.ok(), await res.text()).toBe(true);
    ids.push(((await res.json()) as { id: string }).id);
  }
});

test.afterAll(async ({ request }) => {
  for (const id of ids) await request.delete(`/admin/api/clients/${id}`);
});

async function openClients(page: Page): Promise<void> {
  await page.goto("/admin/#/clients");
  await expect(page.getByRole("heading", { level: 1, name: "Clients" })).toBeVisible();
  await expect(page.getByRole("region", { name: "Clients" })).toBeVisible();
  await expect(page.locator("tr.client-row").first()).toBeVisible();
}

test.describe("DataTable region", () => {
  test("a long list scrolls inside a labelled, focusable region and the page does not grow with it", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await openClients(page);
    const region = page.getByRole("region", { name: "Clients" });
    await expect(region).toHaveAttribute("tabindex", "0");
    const m = await region.evaluate((el) => ({ client: el.clientHeight, scroll: el.scrollHeight }));
    expect(m.scroll, "the list is taller than its region").toBeGreaterThan(m.client * 1.5);
    const box = (await region.boundingBox())!;
    expect(box.height, "region is shorter than the viewport").toBeLessThan(800 * 0.8);
    // The page itself holds the region plus the controls around it, not one row per client.
    const pageH = await page.evaluate(() => document.documentElement.scrollHeight);
    expect(pageH, "page height does not scale with the 40 rows").toBeLessThan(m.scroll);
  });

  test("the column header stays at the top of the region while the rows scroll", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await openClients(page);
    const region = page.getByRole("region", { name: "Clients" });
    await region.evaluate((el) => { el.scrollTop = el.scrollHeight; });
    const head = page.getByRole("columnheader", { name: "Name" });
    await expect(head).toBeVisible();
    const regionBox = (await region.boundingBox())!;
    const headBox = (await head.boundingBox())!;
    expect(Math.abs(headBox.y - regionBox.y), "header sits at the region's top edge").toBeLessThanOrEqual(2);
  });

  test("keyboard focus moving into a far row scrolls the region and never hides it under the header", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await openClients(page);
    const region = page.getByRole("region", { name: "Clients" });
    // Tab from the search box into the region stop, then into the first row, then walk down with Tab.
    await page.getByLabel("Search clients").focus();
    await page.keyboard.press("Tab");
    await expect(region).toBeFocused();
    for (let i = 0; i < 25; i++) await page.keyboard.press("Tab");
    const focused = page.locator(":focus");
    await expect(focused).toHaveAttribute("data-row-id", /.+/);
    const headBottom = (await page.getByRole("columnheader", { name: "Name" }).boundingBox())!;
    const fBox = (await focused.boundingBox())!;
    expect(fBox.y, "focused row control is below the sticky header").toBeGreaterThanOrEqual(headBottom.y + headBottom.height - 1);
    const regionBox = (await region.boundingBox())!;
    expect(fBox.y + fBox.height, "and inside the region").toBeLessThanOrEqual(regionBox.y + regionBox.height + 1);
  });

  test("the region scrolls from the keyboard", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await openClients(page);
    const region = page.getByRole("region", { name: "Clients" });
    await region.focus();
    await page.keyboard.press("PageDown");
    await expect.poll(() => region.evaluate((el) => el.scrollTop)).toBeGreaterThan(0);
  });

  test("at 390 px the rows are cards inside the same region and the header is hidden", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await openClients(page);
    const region = page.getByRole("region", { name: "Clients" });
    const m = await region.evaluate((el) => ({ client: el.clientHeight, scroll: el.scrollHeight }));
    expect(m.scroll).toBeGreaterThan(m.client * 1.5);
    const display = await page.locator("tr.client-row").first().evaluate((el) => getComputedStyle(el).display);
    expect(display).not.toBe("table-row");
    // Still named for assistive technology, but not drawn.
    const headBox = await page.getByRole("columnheader", { name: "Name" }).boundingBox();
    expect(headBox === null || headBox.width <= 2 || headBox.height <= 2 || (await page.getByRole("columnheader", { name: "Name" }).evaluate((el) => getComputedStyle(el.closest("thead")!).clipPath !== "none"))).toBe(true);
    const overflowX = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflowX, "no horizontal page scroll").toBeLessThanOrEqual(0);
  });

  for (const scheme of ["light", "dark"] as const) {
    test(`axe passes on the long list in the ${scheme} scheme`, async ({ page }) => {
      await page.emulateMedia({ colorScheme: scheme });
      await page.setViewportSize({ width: 1280, height: 800 });
      await openClients(page);
      const results = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"]).analyze();
      expect(results.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(" ")).join(" | ")}`)).toEqual([]);
    });
  }
});
