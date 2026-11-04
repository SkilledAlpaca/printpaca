import { test, expect } from "@playwright/test";
import { ADMIN_PASSWORD, ADMIN_URL } from "./helpers";

// The only spec that runs with ADMIN_PASSWORD set; it targets the second server on 9099
// (playwright.config.ts) by absolute URL. Every other spec runs against the 8099 server with auth disabled.

test("an unauthenticated visit shows the login screen, not the app shell", async ({ page }) => {
  await page.goto(ADMIN_URL);
  await expect(page.getByRole("heading", { name: "printpaca" })).toBeVisible();
  await expect(page.getByLabel("Password")).toBeVisible();
  await expect(page.getByRole("navigation", { name: "Sections" })).not.toBeVisible();
});

test("a wrong password shows an error and leaves the login screen up", async ({ page }) => {
  await page.goto(ADMIN_URL);
  await page.getByLabel("Password").fill("not-the-password");
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByText("Incorrect password.")).toBeVisible();
  await expect(page.getByLabel("Password")).toBeVisible();
});

test("the correct password logs in, survives reload, and Sign out returns to the login screen", async ({ page }) => {
  await page.goto(ADMIN_URL);
  await page.getByLabel("Password").fill(ADMIN_PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();

  const navButton = page.getByRole("navigation", { name: "Sections" }).getByRole("button", { name: "New quote" });
  await expect(navButton).toBeVisible();

  await page.reload();
  await expect(navButton).toBeVisible();
  await expect(page.getByLabel("Password")).not.toBeVisible();

  // Sign out clears the session and returns to the login screen.
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await expect(page.getByLabel("Password")).toBeVisible();
  await expect(navButton).not.toBeVisible();
});

test("the username hint tells staff what to enter instead of offering the admin shortcut", async ({ page }) => {
  await page.route("**/api/demo", (route) => route.fulfill({ json: { multiUser: true } }));
  await page.goto(ADMIN_URL);
  const hint = page.locator("#username-hint");
  await expect(hint).toBeVisible();
  await expect(hint).not.toContainText("Leave empty to sign in as the admin");
  await expect(hint).toContainText("Enter the username you were given");
});
