import { test, expect } from "@playwright/test";
import type { Browser, Page } from "@playwright/test";
import { ADMIN_URL, ADMIN_PASSWORD, navButton, resetServerState } from "./helpers";

// D31: employees' config reads omit the SMTP block and the integration URLs, and their screens still load.
// Runs on the :9099 server (multi-user mode needs ADMIN_PASSWORD).

const SMTP_HOST = "smtp.d31-secret.example";
const FLUIDD_URL = "http://d31-secret-printer.local";

type Role = { username: string; password: string; permissions: string[]; tabs: string[] };
const SAM: Role = { username: "sam", password: "sam-secret-pass", permissions: ["quotes", "invoices", "clients", "dashboard"], tabs: ["Dashboard", "Quotes", "Invoices", "Clients"] };
const RILEY: Role = { username: "riley", password: "riley-secret-pass", permissions: ["items", "requests", "filament"], tabs: ["Requests", "Shop items", "Filament"] };

const STRIP = ["integrations", "version", "authEnabled", "moonraker", "fluiddUrl", "smtpConfigured", "demoMode", "integrationSources", "user"];

test.describe.configure({ mode: "serial" });
test.beforeAll(async ({ browser }) => {
  await resetServerState();
  const admin = await browser.newContext({ baseURL: ADMIN_URL });
  const api = admin.request;
  expect((await api.post("api/login", { data: { username: "", password: ADMIN_PASSWORD } })).ok()).toBe(true);
  const cfg = (await (await api.get("api/config")).json()) as Record<string, any>;
  for (const k of STRIP) delete cfg[k];
  cfg.users = { ...cfg.users, multiUser: true };
  cfg.smtp = { ...cfg.smtp, host: SMTP_HOST, port: 587, from: "shop@example.com", to: "owner@example.com" };
  cfg.integrationsUrls = { ...cfg.integrationsUrls, fluiddUrl: FLUIDD_URL };
  const put = await api.put("api/settings", { data: { config: cfg } });
  expect(put.ok(), await put.text()).toBe(true);
  for (const r of [SAM, RILEY]) {
    const res = await api.post("api/users", { data: { username: r.username, password: r.password, permissions: r.permissions } });
    expect(res.ok(), await res.text()).toBe(true);
  }
  await admin.close();
});
test.afterAll(async ({ browser }) => {
  // Put the shared server back: SMTP and Fluidd cleared, single-user mode, no employees.
  const admin = await browser.newContext({ baseURL: ADMIN_URL });
  const api = admin.request;
  await api.post("api/login", { data: { username: "", password: ADMIN_PASSWORD } });
  const cfg = (await (await api.get("api/config")).json()) as Record<string, any>;
  for (const k of STRIP) delete cfg[k];
  cfg.smtp = { ...cfg.smtp, host: "", from: "", to: "" };
  cfg.integrationsUrls = { ...cfg.integrationsUrls, fluiddUrl: "" };
  await api.put("api/settings", { data: { config: cfg } });
  await admin.close();
  await resetServerState();
});

async function signIn(browser: Browser, role: Role): Promise<{ page: Page; problems: string[]; configBodies: string[] }> {
  const context = await browser.newContext();
  const page = await context.newPage();
  const problems: string[] = [];
  const configBodies: string[] = [];
  page.on("pageerror", (e) => problems.push(`pageerror: ${e.message}`));
  page.on("console", (m) => {
    if (m.type() === "error" && !/status of 401/.test(m.text())) problems.push(`console: ${m.text()}`);
  });
  page.on("response", async (r) => {
    if (!r.url().includes("/admin/api/")) return;
    if (/\/api\/(config|settings)(\?|$)/.test(r.url())) configBodies.push(await r.text().catch(() => ""));
    // The login screen's probe of /api/config is a 401 by design and can land after sign-in.
    if (r.status() === 401 && /\/api\/config$/.test(r.url())) return;
    if (r.status() >= 400) problems.push(`${r.status()} ${r.url()}`);
  });
  await page.goto(ADMIN_URL);
  await page.getByLabel("Username").fill(role.username);
  await page.getByLabel("Password").fill(role.password);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("navigation", { name: "Sections" })).toBeVisible();
  // The login screen's own probe of /api/config is a 401 by design; judge only what happens once signed in.
  problems.length = 0;
  return { page, problems, configBodies };
}

for (const role of [SAM, RILEY]) {
  test(`${role.username}: screens load and the config omits SMTP and integration URLs`, async ({ browser }) => {
    const { page, problems, configBodies } = await signIn(browser, role);
    for (const tab of role.tabs) {
      await navButton(page, tab).click();
      await expect(page.getByRole("heading", { level: 1 }).first()).toBeVisible();
      // Let the tab's own requests settle before the next one.
      await page.waitForLoadState("networkidle");
    }
    await navButton(page, "Settings").click();
    await expect(page.getByRole("heading", { level: 1 }).first()).toBeVisible();
    await page.waitForLoadState("networkidle");

    expect(configBodies.length).toBeGreaterThan(0);
    for (const body of configBodies) {
      expect(body).not.toContain(SMTP_HOST);
      expect(body).not.toContain(FLUIDD_URL);
      expect(body).not.toContain("owner@example.com");
    }
    // Mail stays "configured" for the screens that offer Email, and no Fluidd link leaks into the nav.
    await expect(page.getByText("Email isn't configured")).toHaveCount(0);
    await expect(page.locator(`a[href="${FLUIDD_URL}"]`)).toHaveCount(0);
    expect(problems).toEqual([]);
  });
}

test("sam: the Quotes screen does not claim email is unconfigured", async ({ browser }) => {
  const { page } = await signIn(browser, SAM);
  await navButton(page, "Quotes").click();
  await expect(page.getByRole("heading", { name: "Quotes", level: 1 })).toBeVisible();
  await expect(page.getByText("Email isn't configured, so quotes can't be emailed from here.")).toHaveCount(0);
});
