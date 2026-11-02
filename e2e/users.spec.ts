import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";
import { ADMIN_URL, gotoSettings, loginAsAdmin, navButton, resetServerState } from "./helpers";

// Multi-user mode needs ADMIN_PASSWORD, which the shared :8099 server never has, so this spec uses the
// :9099 server from auth.spec.ts. Tests run in file order on one server, and the last restores
// single-user mode with no employees.

const EMPLOYEE_USERNAME = "e2e_employee";
const EMPLOYEE_PASSWORD = "e2e-employee-secret";
const EMPLOYEE_NEW_PASSWORD = "e2e-employee-secret-2";

// Logout awaits POST /api/logout and then reloads; wait for the reloaded login screen so a following
// page.goto doesn't race it and die with net::ERR_ABORTED.
async function logOut(page: Page): Promise<void> {
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();
}

async function loginAsEmployee(page: Page, password: string): Promise<void> {
  await page.goto(ADMIN_URL);
  await page.getByLabel("Username").fill(EMPLOYEE_USERNAME);
  await page.getByLabel("Password").fill(password);
  await page.getByRole("button", { name: "Sign in" }).click();
  // Wait for the logged-in shell, or a following page.request races Set-Cookie and goes out unauthenticated.
  await expect(page.getByRole("navigation", { name: "Sections" })).toBeVisible();
}

test.beforeAll(resetServerState);
test.afterAll(resetServerState);

// Serial: the tests depend on each other, and the group stops at the first failure.
test.describe.configure({ mode: "serial" });

test("admin turns on multi-user mode and creates an employee with distinct grants", async ({ page }) => {
  await loginAsAdmin(page);
  await gotoSettings(page, { load: false });

  // Single-user mode shows the mode toggle plus a hint, nothing else.
  await expect(page.getByRole("heading", { name: "Add employee" })).not.toBeVisible();

  await page.getByLabel("Multi-user mode").check();
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByRole("button", { name: "Save changes" })).toHaveCount(0);

  // The Users card unlocks once the persisted multiUser flip lands, not on the draft checkbox.
  await expect(page.getByRole("heading", { name: "Add employee" })).toBeVisible();

  // Scoped to the Users card: the SMTP card also has a "Username" field, so a page-wide label is ambiguous.
  const usersCard = page.locator("#section-users");
  await usersCard.getByLabel("Username").fill(EMPLOYEE_USERNAME);
  await usersCard.getByLabel("Password").fill(EMPLOYEE_PASSWORD);
  // Quotes/invoices only, which the next test's nav and API checks rely on.
  await usersCard.getByLabel("Quotes").check();
  await usersCard.getByLabel("Invoices").check();
  await usersCard.getByRole("button", { name: "Add employee" }).click();

  // exact: the disable/reset cells' aria-labels contain the username as a substring.
  await expect(usersCard.getByRole("cell", { name: EMPLOYEE_USERNAME, exact: true })).toBeVisible();
});

test("employee login sees only granted tabs; the login screen asks for a username", async ({ page }) => {
  await page.goto(ADMIN_URL);
  // Only multi-user mode's login screen shows this field.
  await expect(page.getByLabel("Username")).toBeVisible();

  await page.getByLabel("Username").fill(EMPLOYEE_USERNAME);
  await page.getByLabel("Password").fill(EMPLOYEE_PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();

  await expect(navButton(page, "Quotes")).toBeVisible();
  await expect(navButton(page, "Invoices")).toBeVisible();
  // Every other grantable tab is hidden, including "New quote" (the "quote" permission key).
  await expect(navButton(page, "New quote")).not.toBeVisible();
  await expect(navButton(page, "Requests")).not.toBeVisible();
  await expect(navButton(page, "Items")).not.toBeVisible();
  await expect(navButton(page, "Clients")).not.toBeVisible();
  await expect(navButton(page, "Dashboard")).not.toBeVisible();
  await expect(navButton(page, "Filament")).not.toBeVisible();
  await expect(navButton(page, "Electricity")).not.toBeVisible();
  await expect(navButton(page, "Printers")).not.toBeVisible();
  await expect(navButton(page, "Settings")).toBeVisible();
});

test("a direct API call to an ungranted route family 403s regardless of the hidden nav", async ({ page }) => {
  await loginAsEmployee(page, EMPLOYEE_PASSWORD);

  // UI hiding is not the security boundary: Clients is hidden from this nav and refused server-side.
  const res = await page.request.get(`${ADMIN_URL}api/clients`);
  expect(res.status()).toBe(403);

  const ok = await page.request.get(`${ADMIN_URL}api/invoices`);
  expect(ok.status()).toBe(200);
});

test("changing the employee's own password ends their other sessions, not the one that changed it", async ({
  page,
  browser,
}) => {
  await loginAsEmployee(page, EMPLOYEE_PASSWORD);

  // A second, independent session for the same employee; logging in again mints a new token.
  const otherContext = await browser.newContext();
  const otherPage = await otherContext.newPage();
  await loginAsEmployee(otherPage, EMPLOYEE_PASSWORD);
  await expect(navButton(otherPage, "Invoices")).toBeVisible();

  await gotoSettings(page, { load: false });
  await page.getByLabel("Current password").fill(EMPLOYEE_PASSWORD);
  await page.getByLabel("New password").fill(EMPLOYEE_NEW_PASSWORD);
  await page.getByRole("button", { name: "Change password" }).click();
  await expect(page.getByText("Password changed.")).toBeVisible();

  // The session that changed the password stays alive; only the other sessions are killed.
  const stillAlive = await page.request.get(`${ADMIN_URL}api/me`);
  expect(stillAlive.status()).toBe(200);

  const killed = await otherPage.request.get(`${ADMIN_URL}api/me`);
  expect(killed.status()).toBe(401);

  await otherContext.close();
});

test("an invoice created by the employee shows its attribution stamp to admin", async ({ page }) => {
  await loginAsEmployee(page, EMPLOYEE_NEW_PASSWORD);

  const res = await page.request.post(`${ADMIN_URL}api/invoices`, {
    data: {
      clientName: "E2E attribution walk-in",
      lines: [{ description: "E2E attribution line", quantity: 1, unitPriceCents: 500 }],
    },
  });
  expect(res.ok()).toBe(true);
  const invoice = (await res.json()) as { id: string; number: string };

  await logOut(page);
  await loginAsAdmin(page);
  await navButton(page, "Invoices").click();
  await page.getByRole("button", { name: invoice.number }).click();

  await expect(page.getByText(`Created by ${EMPLOYEE_USERNAME}`)).toBeVisible();
});

test("cleanup: admin deletes the employee and turns multi-user mode back off", async ({ page }) => {
  await loginAsAdmin(page);
  await gotoSettings(page, { load: false });

  await page.getByRole("button", { name: `Delete ${EMPLOYEE_USERNAME}` }).click();
  await page.getByRole("button", { name: `Confirm delete ${EMPLOYEE_USERNAME}` }).click();
  await expect(page.getByRole("cell", { name: EMPLOYEE_USERNAME })).toHaveCount(0);

  await page.getByLabel("Multi-user mode").uncheck();
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByRole("button", { name: "Save changes" })).toHaveCount(0);

  // Single-user mode's login screen has no username field (as auth.spec.ts assumes).
  await logOut(page);
  await expect(page.getByLabel("Username")).not.toBeVisible();
});
