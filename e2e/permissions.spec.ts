import { test, expect } from "@playwright/test";
import type { Browser, Page } from "@playwright/test";
import { ADMIN_URL, ADMIN_PASSWORD, navButton, resetServerState, seedQuoteBody } from "./helpers";

// A13 and A16: what a limited user sees. riley holds items, requests and filament; sam holds quotes, invoices,
// clients and dashboard; quinn holds only quotes. Every action a user can't complete is hidden or disabled with
// a reason, and a forbidden address answers "No access". The server-side checks are untouched. Runs on the :9099
// server (multi-user mode needs ADMIN_PASSWORD).

const MAIN = "http://127.0.0.1:9099";
const USERS = {
  riley: { password: "riley-secret-pass", permissions: ["items", "requests", "filament"] },
  sam: { password: "sam-secret-pass", permissions: ["quotes", "invoices", "clients", "dashboard"] },
  quinn: { password: "quinn-secret-pass", permissions: ["quotes"] },
} as const;
type Who = keyof typeof USERS;

const STRIP = ["integrations", "version", "authEnabled", "moonraker", "fluiddUrl", "smtpConfigured", "demoMode", "integrationSources", "user"];
const ids = { plainRequest: "", invoicedRequest: "", invoice: "", invoiceNumber: "", quote: "" };
let invoicedName = "";
let plainName = "";

test.describe.configure({ mode: "serial" });

test.beforeAll(async ({ browser }) => {
  await resetServerState();
  const admin = await browser.newContext({ baseURL: ADMIN_URL });
  const api = admin.request;
  expect((await api.post("api/login", { data: { username: "", password: ADMIN_PASSWORD } })).ok()).toBe(true);
  const cfg = (await (await api.get("api/config")).json()) as Record<string, any>;
  for (const k of STRIP) delete cfg[k];
  cfg.users = { ...cfg.users, multiUser: true };
  for (const m of cfg.materials) if (m.name === "PLA") m.public = true;
  const put = await api.put("api/settings", { data: { config: cfg } });
  expect(put.ok(), await put.text()).toBe(true);
  for (const [username, u] of Object.entries(USERS)) {
    const res = await api.post("api/users", { data: { username, password: u.password, permissions: u.permissions } });
    expect(res.ok(), await res.text()).toBe(true);
  }

  const submit = async (name: string, xff: string): Promise<string> => {
    const res = await admin.request.post(`${MAIN}/api/public/quote-request`, {
      headers: { "X-Forwarded-For": xff },
      multipart: { name, email: `${name.toLowerCase().replace(/[^a-z]+/g, ".")}@example.com`, note: "", material: "PLA", quantity: "1", weightG: "12", timeSeconds: "3600" },
    });
    expect(res.status(), await res.text()).toBe(201);
    return ((await res.json()) as { id: string }).id;
  };
  plainName = "Perm Plain Buyer";
  invoicedName = "Perm Invoiced Buyer";
  ids.plainRequest = await submit(plainName, "198.51.100.71");
  ids.invoicedRequest = await submit(invoicedName, "198.51.100.72");
  const client = await api.post("api/clients", { data: { name: invoicedName, email: "perm.invoiced@example.com", phone: "", billingAddress: "", shippingAddress: "", notes: "" } });
  expect(client.ok(), await client.text()).toBe(true);
  const clientId = ((await client.json()) as { id: string }).id;
  const inv = await api.post("api/invoices", { data: { clientId, taxBp: 0, discountCents: 0, notes: "", requestId: ids.invoicedRequest, lines: [{ description: "3D print: PLA", quantity: 1, unitPriceCents: 1000, discountCents: 0, quoteId: "" }] } });
  expect(inv.status(), await inv.text()).toBe(201);
  const invBody = (await inv.json()) as { id: string; number: string };
  ids.invoice = invBody.id;
  ids.invoiceNumber = invBody.number;
  const q = await api.post("api/quotes", { data: seedQuoteBody("Perm quote") });
  expect(q.ok(), await q.text()).toBe(true);
  ids.quote = ((await q.json()) as { id: string }).id;
  await admin.close();
});

test.afterAll(async ({ browser }) => {
  const admin = await browser.newContext({ baseURL: ADMIN_URL });
  const api = admin.request;
  await api.post("api/login", { data: { username: "", password: ADMIN_PASSWORD } });
  const cfg = (await (await api.get("api/config")).json()) as Record<string, any>;
  for (const k of STRIP) delete cfg[k];
  for (const m of cfg.materials) if (m.name === "PLA") m.public = false;
  await api.put("api/settings", { data: { config: cfg } });
  await resetServerState();
  await admin.close();
});

async function signIn(browser: Browser, who: Who, hash = ""): Promise<Page> {
  const page = await (await browser.newContext()).newPage();
  await page.goto(ADMIN_URL);
  await page.getByLabel("Username").fill(who);
  await page.getByLabel("Password").fill(USERS[who].password);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("navigation", { name: "Sections" })).toBeVisible();
  if (hash) await page.evaluate((h) => (location.hash = h), hash);
  return page;
}

test.describe("riley: requests, items, filament", () => {
  test("Public estimates are read-only", async ({ browser }) => {
    const page = await signIn(browser, "riley");
    await navButton(page, "Requests").click();
    await expect(page.getByRole("region", { name: "Requests" })).toBeVisible();
    const card = page.getByRole("heading", { name: "Public estimates" });
    await expect(card).toBeVisible();
    await expect(page.getByText("Only an admin can change this.")).toBeVisible();
    await expect(page.locator("#public-estimate-printer")).toHaveCount(0);
    await expect(page.locator("#public-estimate-preset")).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Save changes" })).toHaveCount(0);
  });

  test("Convert to invoice and Open in new quote are disabled with a reason, and do nothing", async ({ browser }) => {
    const page = await signIn(browser, "riley");
    await navButton(page, "Requests").click();
    await page.getByRole("button", { name: new RegExp(plainName) }).first().click();
    await expect(page.getByRole("heading", { level: 1, name: `Estimate request from ${plainName}` })).toBeVisible();

    const convert = page.getByRole("button", { name: "Convert to invoice" });
    await expect(convert).toHaveAttribute("aria-disabled", "true");
    await expect(convert).toHaveAccessibleDescription(/needs access to Clients and Invoices\. Ask an admin\./);
    const quote = page.getByRole("button", { name: "Open in new quote" });
    await expect(quote).toHaveAttribute("aria-disabled", "true");
    await expect(quote).toHaveAccessibleDescription("You don't have access to New quote. Ask an admin.");

    // Pressing them changes nothing: no request goes out and the page stays put.
    const calls: string[] = [];
    page.on("request", (r) => {
      if (/\/api\/(clients|invoices)/.test(r.url())) calls.push(r.method() + " " + r.url());
    });
    // aria-disabled controls are not "enabled" to Playwright, so press them the way a user would: force the click.
    await convert.click({ force: true });
    await quote.click({ force: true });
    await page.waitForTimeout(300);
    expect(calls).toEqual([]);
    await expect(page.getByRole("heading", { level: 1, name: `Estimate request from ${plainName}` })).toBeVisible();
    await expect(page.getByText("Couldn't")).toHaveCount(0);
  });

  test("an invoiced request names its invoice but offers no link to Invoices", async ({ browser }) => {
    const page = await signIn(browser, "riley");
    await navButton(page, "Requests").click();
    await page.getByRole("button", { name: new RegExp(invoicedName) }).first().click();
    await expect(page.getByText(`Invoiced as ${ids.invoiceNumber}`)).toBeVisible();
    await expect(page.getByRole("button", { name: "Open invoice" })).toHaveCount(0);
    await expect(page.getByText("You don't have access to Invoices")).toBeVisible();
  });
});

test.describe("sam: quotes, invoices, clients, dashboard", () => {
  test("an invoice made from a request names it without linking to Requests", async ({ browser }) => {
    const page = await signIn(browser, "sam");
    await navButton(page, "Invoices").click();
    await page.getByRole("button", { name: ids.invoiceNumber, exact: true }).click();
    await expect(page.getByRole("heading", { name: `Invoice ${ids.invoiceNumber}` })).toBeVisible();
    await expect(page.getByText(/Created from request/)).toBeVisible();
    await expect(page.locator("button.linkish")).toHaveCount(0);
  });

  test("#/new answers No access instead of redirecting", async ({ browser }) => {
    const page = await signIn(browser, "sam", "#/new");
    const alert = page.getByRole("alert").filter({ hasText: "No access" });
    await expect(alert).toBeVisible();
    await expect(alert).toContainText("New quote");
  });

  test("#/settings/storefront answers No access, with the Account card still there", async ({ browser }) => {
    const page = await signIn(browser, "sam", "#/settings/storefront");
    const alert = page.getByRole("alert").filter({ hasText: "No access" });
    await expect(alert).toBeVisible();
    await expect(alert).toContainText("storefront");
    await expect(page.getByRole("heading", { name: /Account/ }).first()).toBeVisible();
  });
});

test.describe("quinn: quotes only", () => {
  test("Convert to invoice is disabled with a reason and the invoiced badge is not a link", async ({ browser }) => {
    const page = await signIn(browser, "quinn");
    await navButton(page, "Quotes").click();
    const convert = page.getByRole("button", { name: /^Convert to invoice: "Perm quote"/ });
    await expect(convert).toBeVisible();
    await expect(convert).toHaveAttribute("aria-disabled", "true");
    await expect(convert).toHaveAccessibleDescription(/You don't have access to Invoices/);
    const calls: string[] = [];
    page.on("request", (r) => {
      if (/\/api\/(clients|invoices)/.test(r.url())) calls.push(r.url());
    });
    await convert.click({ force: true });
    await page.waitForTimeout(300);
    expect(calls).toEqual([]);
    await expect(page.getByRole("heading", { name: /Convert/ })).toHaveCount(0);
  });
});
