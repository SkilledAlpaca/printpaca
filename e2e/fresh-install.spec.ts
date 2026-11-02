import { test, expect } from "@playwright/test";
import { navButton } from "./helpers";

// The :8099 server boots from a throwaway CONFIG_DIR, so this asserts the first-run experience: seeded
// defaults, no uncaught page errors, and each data tab's empty state. Specs run in filename order on one
// worker, after clients.spec.ts and before the specs that leave quotes/invoices/requests behind.

test("GET /api/config serves the seeded defaults with no setup required", async ({ request }) => {
  const res = await request.get("/admin/api/config");
  expect(res.ok()).toBeTruthy();
  const body = await res.json();

  expect(body.currency.symbol).toBe("$");
  expect(Array.isArray(body.materials)).toBe(true);
  expect(body.materials.map((m: { name: string }) => m.name)).toEqual(
    expect.arrayContaining(["PLA", "PETG", "ABS", "TPU", "Resin"]),
  );
  // A valid printer/preset pair is seeded even though no material is public yet.
  expect(body.publicQuotes.printer).toBe("generic");
  expect(body.publicQuotes.preset).toBe("side-hustle");
  // ADMIN_PASSWORD is empty on :8099, so auth is disabled.
  expect(body.authEnabled).toBe(false);
});

test("the app loads with no uncaught page errors and shows first-run empty states", async ({ page }) => {
  const pageErrors: Error[] = [];
  page.on("pageerror", (err) => pageErrors.push(err));

  await page.goto("/admin/");
  await expect(page.getByRole("navigation", { name: "Sections" })).toBeVisible();

  await navButton(page, "Dashboard").click();
  await expect(page).toHaveURL(/#\/dashboard$/);
  await expect(
    page.getByText("No revenue yet. Convert a quote to an invoice and record a payment to see revenue here."),
  ).toBeVisible();
  await expect(
    page.getByText("No invoices yet. Convert a saved quote on the Quotes page, or create one on the Invoices page."),
  ).toBeVisible();

  await navButton(page, "Quotes").click();
  await expect(page).toHaveURL(/#\/quotes$/);
  await expect(page.getByText("No saved quotes yet. Save one from the New quote page.")).toBeVisible();

  await navButton(page, "Clients").click();
  await expect(page).toHaveURL(/#\/clients$/);
  await expect(page.getByText("No clients yet.")).toBeVisible();

  await navButton(page, "Invoices").click();
  await expect(page).toHaveURL(/#\/invoices$/);
  await expect(
    page.getByText("No invoices yet. Convert a saved quote from the Quotes page, or press New invoice.").first(),
  ).toBeVisible();

  await navButton(page, "Requests").click();
  await expect(page).toHaveURL(/#\/requests$/);
  await expect(
    page.getByText("No estimate requests yet. They arrive here when a visitor submits your public estimate page. Set that page up below."),
  ).toBeVisible();

  expect(pageErrors, `uncaught page errors: ${pageErrors.map((e) => e.message).join(", ")}`).toEqual([]);
});
