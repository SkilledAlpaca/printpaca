import { test, expect } from "@playwright/test";
import { ADMIN_URL, gotoSettings, loginAsAdmin, resetServerState } from "./helpers";

// The Users card's Cloudflare Access email fields (per-employee Email and the admin's own email). Runs
// on the :9099 server like users.spec.ts, in order, and restores single-user mode with no employees.

const USER_A = "e2e_email_a";
const USER_B = "e2e_email_b";
const ADMIN_EMAIL = "Owner@Example.com";

test.beforeAll(resetServerState);
test.afterAll(resetServerState);

test.describe.configure({ mode: "serial" });

test("an employee's Access email and the admin's Access email survive a round trip", async ({ page }) => {
  await loginAsAdmin(page);
  await gotoSettings(page, { load: false });

  await page.getByLabel("Multi-user mode").check();
  await page.getByLabel("Your Cloudflare Access email", { exact: true }).fill(ADMIN_EMAIL);
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByText("Settings saved.")).toBeVisible();
  await expect(page.getByRole("heading", { name: "Add employee" })).toBeVisible();

  const usersCard = page.locator("#section-users");
  await usersCard.getByLabel("Username").fill(USER_A);
  await usersCard.getByLabel("Password", { exact: true }).fill("e2e-email-secret");
  await usersCard.getByLabel("Cloudflare Access email (optional)", { exact: true }).fill("  Alpha@Example.com ");
  await usersCard.getByLabel("Quotes").check();
  await usersCard.getByRole("button", { name: "Add employee" }).click();

  const emailA = usersCard.getByLabel(`Cloudflare Access email for ${USER_A}`, { exact: true });
  await expect(emailA).toHaveValue("alpha@example.com");

  // A second employee given the first one's address: the server answers 409 and the row shows it.
  await usersCard.getByLabel("Username").fill(USER_B);
  await usersCard.getByLabel("Password", { exact: true }).fill("e2e-email-secret");
  await usersCard.getByRole("button", { name: "Add employee" }).click();
  const emailB = usersCard.getByLabel(`Cloudflare Access email for ${USER_B}`, { exact: true });
  await expect(emailB).toHaveValue("");
  await emailB.fill("ALPHA@example.com");
  await emailB.blur();
  await expect(usersCard.getByRole("alert")).toContainText("already used");

  await emailB.fill("Beta@Example.com");
  await emailB.blur();
  await expect(emailB).toHaveValue("beta@example.com");

  await page.reload(); // stays on #/settings, with the session cookie
  await expect(usersCard.getByRole("heading", { name: "Add employee" })).toBeVisible();
  await expect(usersCard.getByLabel(`Cloudflare Access email for ${USER_A}`, { exact: true })).toHaveValue("alpha@example.com");
  await expect(usersCard.getByLabel(`Cloudflare Access email for ${USER_B}`, { exact: true })).toHaveValue("beta@example.com");
  await expect(page.getByLabel("Your Cloudflare Access email", { exact: true })).toHaveValue("owner@example.com");

  const cleared = usersCard.getByLabel(`Cloudflare Access email for ${USER_A}`, { exact: true });
  await cleared.fill("");
  await cleared.blur();
  await expect(cleared).toHaveValue("");
  const list = await page.request.get(`${ADMIN_URL}api/users`);
  const body = (await list.json()) as { users: { username: string; email: string }[] };
  expect(body.users.find((u) => u.username === USER_A)?.email).toBe("");
  expect(body.users.find((u) => u.username === USER_B)?.email).toBe("beta@example.com");
});
