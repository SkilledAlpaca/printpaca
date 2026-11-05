import { test, expect } from "@playwright/test";
import type { Locator, Page } from "@playwright/test";
import { openMorePricing } from "./helpers";

// In the admin app a field's hint is a "?" button beside the label that opens a tooltip; the text stays the
// control's accessible description. The customer shop keeps its hints visible.

const WASTE_HINT = /Extra filament lost to purging, supports, and skirts/;

async function openQuote(page: Page, width = 1440) {
  await page.setViewportSize({ width, height: 900 });
  await page.goto("/admin/");
  await expect(page.getByLabel(/^Waste/)).toBeVisible();
}

const helpFor = (page: Page, label: string): Locator => page.getByRole("button", { name: `Help: ${label}` });

test("a hint is a Help button, not text under the field, and still describes the control", async ({ page }) => {
  await openQuote(page);
  const help = helpFor(page, "Waste (%)");
  await expect(help).toBeVisible();
  // The hint text is in the DOM for assistive tech but not painted.
  const tip = page.locator("[role=tooltip]", { hasText: WASTE_HINT });
  await expect(tip).toHaveCount(1);
  await expect(tip).not.toBeInViewport();
  await expect(page.getByLabel(/^Waste/)).toHaveAccessibleDescription(WASTE_HINT);
  // The control's name is still only its label.
  await expect(page.getByRole("spinbutton", { name: "Waste (%)" })).toBeVisible();
});

test("hover opens the tooltip, it stays open under the pointer, and Escape closes it", async ({ page }) => {
  await openQuote(page);
  const help = helpFor(page, "Waste (%)");
  const tip = page.locator("[role=tooltip]", { hasText: WASTE_HINT });
  await help.hover();
  await expect(tip).toBeInViewport();
  // Hoverable (WCAG 1.4.13): moving onto the tooltip itself keeps it open.
  const box = (await tip.boundingBox())!;
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2, { steps: 5 });
  await page.waitForTimeout(300);
  await expect(tip).toBeInViewport();
  // Dismissible: Escape closes it while the pointer is still there.
  await page.keyboard.press("Escape");
  await expect(tip).not.toBeInViewport();
  // Moving away and back reopens it.
  await page.mouse.move(5, 5);
  await help.hover();
  await expect(tip).toBeInViewport();
  await page.mouse.move(5, 5);
  await expect(tip).not.toBeInViewport();
});

test("keyboard focus opens the tooltip and Escape closes it", async ({ page }) => {
  await openQuote(page);
  const tip = page.locator("[role=tooltip]", { hasText: WASTE_HINT });
  await page.getByLabel(/^Filament price/).focus();
  await page.keyboard.press("Tab");
  await expect(helpFor(page, "Waste (%)")).toBeFocused();
  await expect(tip).toBeInViewport();
  await page.keyboard.press("Escape");
  await expect(tip).not.toBeInViewport();
  // Escape closed only the tooltip: focus stays on the button.
  await expect(helpFor(page, "Waste (%)")).toBeFocused();
  // Tabbing on to the field leaves it closed.
  await page.keyboard.press("Tab");
  await expect(page.getByLabel(/^Waste/)).toBeFocused();
  await expect(tip).not.toBeInViewport();
});

test("a click pins the tooltip and a click elsewhere closes it", async ({ page }) => {
  await openQuote(page);
  const tip = page.locator("[role=tooltip]", { hasText: WASTE_HINT });
  await helpFor(page, "Waste (%)").click();
  await page.mouse.move(5, 5);
  await expect(tip).toBeInViewport();
  await page.getByRole("heading", { name: "New quote" }).click();
  await expect(tip).not.toBeInViewport();
});

test.describe("on a phone", () => {
  test.use({ viewport: { width: 390, height: 844 }, hasTouch: true });

  test("a tap opens the tooltip inside the viewport, a second tap closes it, and the target is 44px", async ({ page }) => {
    await page.goto("/admin/");
    const help = helpFor(page, "Quantity (units)");
    await help.scrollIntoViewIfNeeded();
    const hit = (await help.boundingBox())!;
    expect(hit.width).toBeGreaterThanOrEqual(44);
    expect(hit.height).toBeGreaterThanOrEqual(44);
    const tip = page.locator("[role=tooltip]", { hasText: /How many copies you are printing/ });
    await help.tap();
    await expect(tip).toBeInViewport();
    const box = (await tip.boundingBox())!;
    expect(box.x).toBeGreaterThanOrEqual(0);
    expect(box.x + box.width).toBeLessThanOrEqual(390);
    expect(box.y).toBeGreaterThanOrEqual(0);
    await help.tap();
    await expect(tip).not.toBeInViewport();
  });
});

test("a tooltip near the right edge stays inside the viewport", async ({ page }) => {
  await openQuote(page, 1100);
  // The summary column's price fields are the rightmost help buttons.
  const help = helpFor(page, "Shipping charged (customer pays)");
  await page.getByLabel("Set a selling price").check();
  await openMorePricing(page);
  await help.hover();
  const tip = page.locator("[role=tooltip]", { hasText: /What you charge the customer for shipping/ });
  await expect(tip).toBeInViewport();
  const box = (await tip.boundingBox())!;
  expect(box.x).toBeGreaterThanOrEqual(0);
  expect(box.x + box.width).toBeLessThanOrEqual(1100);
});

test("errors stay visible under the field; only the hint moves into the tooltip", async ({ page }) => {
  await openQuote(page);
  await page.getByLabel(/^Waste/).fill("-5");
  await page.getByLabel(/^Waste/).blur();
  const field = page.locator(".ui-field", { has: page.getByLabel(/^Waste/) });
  await expect(field.locator(".error-text")).toBeVisible();
  await expect(page.getByLabel(/^Waste/)).toHaveAccessibleDescription(/Extra filament lost[\s\S]*waste/i);
});

test("controls in one row share a top edge and sit right under their labels", async ({ page }) => {
  for (const width of [900, 1440]) {
    await openQuote(page, width);
    const group = page.getByRole("group", { name: /Step 2: Material and printer/ });
    const rows = await group.locator(".form-grid.aligned > .ui-field").first().evaluate((first) => {
      const grid = first.parentElement!;
      return [...grid.children].map((f) => {
        const head = f.querySelector(".ui-field-head")!.getBoundingClientRect();
        const control = f.querySelector<HTMLElement>("input, select")!.getBoundingClientRect();
        return { head: Math.round(head.top), gap: control.top - head.bottom };
      });
    });
    for (const a of rows) {
      // Nothing between the label row and the control: no hint row, however long a neighbour's hint is.
      expect(a.gap, `gap under the label at width ${width}`).toBeLessThanOrEqual(8);
      for (const b of rows) if (a.head === b.head) expect(Math.abs(a.gap - b.gap)).toBeLessThanOrEqual(1);
    }
  }
});
