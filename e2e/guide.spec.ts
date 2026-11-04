import { test, expect } from "@playwright/test";

test("guide is reachable as its own page via the sidebar footer link", async ({ page }) => {
  await page.goto("/admin/");
  await expect(page.getByRole("heading", { name: "Guide", level: 1 })).toHaveCount(0);

  // Guide isn't a nav destination; it's a sidebar-footer button that calls activate("guide").
  await page.getByRole("button", { name: "Guide", exact: true }).click();
  await expect(page).toHaveURL(/#\/guide$/);
  await expect(page.getByRole("heading", { name: "Guide", level: 1 })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Getting started" })).toBeVisible();
});

test("card help renders in flow on click, never on hover", async ({ page }) => {
  await page.goto("/admin/");

  const costsNote = page.locator("#help-costs");
  await expect(costsNote).toBeHidden();

  // Hovering the card must never reveal its help note.
  await page.locator(".tile").filter({ has: page.getByRole("button", { name: "About Costs" }) }).hover();
  await expect(costsNote).toBeHidden();

  await page.getByRole("button", { name: "About Costs" }).click();
  await expect(costsNote).toBeVisible();
  await expect(costsNote).toContainText("shop rates");

  // The note must sit in normal flow, never as an absolute or fixed overlay.
  const position = await costsNote.evaluate((el) => getComputedStyle(el).position);
  expect(position).toBe("static");
});

test("card help toggles via keyboard alone", async ({ page }) => {
  await page.goto("/admin/");

  const costsToggle = page.getByRole("button", { name: "About Costs" });
  const costsNote = page.locator("#help-costs");
  await expect(costsNote).toBeHidden();

  await costsToggle.focus();
  await page.keyboard.press("Enter");
  await expect(costsNote).toBeVisible();
});

test("a card help note's anchored guide link lands on the Guide tab and scrolls to the topic", async ({ page }) => {
  await page.goto("/admin/");

  // Pricing has a real guide anchor ("#/guide#margin-vs-markup"). tabFromHash must strip the second
  // fragment, and GettingStarted's mount-time scroll must win the race against the scroll reset.
  await page.getByRole("button", { name: "About Price and profit" }).click();
  await page.locator("#help-pricing").getByRole("link", { name: "More in the guide" }).click();

  await expect(page).toHaveURL(/#\/guide/);
  await expect(page.getByRole("heading", { name: "Getting started" })).toBeVisible();
  // toBeInViewport is hollow here (the anchor is visible at scroll 0); assert scrollTop directly.
  await expect
    .poll(() => page.locator("#main").evaluate((el) => el.scrollTop))
    .toBeGreaterThan(0);
});

test("a cold-load deep link to a guide anchor scrolls to that topic", async ({ page }) => {
  // On a fresh navigation, App's replaceState normalization must not strip the fragment before
  // GettingStarted's onMount reads location.hash.
  await page.goto("/admin/#/guide#margin-vs-markup");

  await expect(page.getByRole("heading", { name: "Guide", level: 1 })).toBeVisible();
  await expect
    .poll(() => page.locator("#main").evaluate((el) => el.scrollTop))
    .toBeGreaterThan(0);
});

test("Explain this page opens every card's help at once", async ({ page }) => {
  await page.goto("/admin/");

  const explainToggle = page.getByRole("button", { name: "Explain this page" });
  await expect(explainToggle).toHaveAttribute("aria-pressed", "false");

  await explainToggle.click();
  await expect(explainToggle).toHaveAttribute("aria-pressed", "true");
  for (const id of ["gcode", "print", "costs", "breakdown", "pricing", "history"]) {
    await expect(page.locator(`#help-${id}`)).toBeVisible();
  }

  await explainToggle.click();
  await expect(explainToggle).toHaveAttribute("aria-pressed", "false");
  await expect(page.locator("#help-costs")).toBeHidden();
});
