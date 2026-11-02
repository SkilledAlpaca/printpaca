import { test, expect } from "@playwright/test";
import type { APIRequestContext, Page } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { navButton, seedQuoteBody, ADMIN_URL } from "./helpers";

// Regression gate for WCAG 2.2 AA (docs: obsidian-ai/43 - 3D Printing/printpaca/wcag-fix-spec.md). Runs axe on
// the login screen, front door, shop, every admin tab, and one open detail view per record type, in light and
// dark, at desktop and 390 px, and asserts no horizontal page scroll at 320 px. Zero violations are required.
// Focus checks cover the mobile drawer, a delete confirm, and the public submit confirmations.

const TAGS = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"];
const SCHEMES = ["light", "dark"] as const;
const VIEWPORTS = [
  { name: "desktop", width: 1280, height: 900 },
  { name: "390", width: 390, height: 844 },
] as const;

const ITEM_NAME = "A11y Gate Planter";
const CLIENT_NAME = "A11y Gate Client";

test.describe.configure({ mode: "serial" });

/** Waits for the page to settle: network idle plus fonts, so contrast is computed on final styles. */
async function settle(page: Page): Promise<void> {
  await page.waitForLoadState("networkidle");
  await page.evaluate(() => document.fonts.ready);
}

async function runAxe(page: Page, label: string): Promise<void> {
  const results = await new AxeBuilder({ page }).withTags(TAGS).analyze();
  const summary = results.violations.map(
    (v) => `${v.id} (${v.impact}): ${v.nodes.length} node(s), e.g. ${v.nodes[0]?.target.join(" ")} :: ${v.nodes[0]?.failureSummary?.split("\n")[1] ?? ""}`,
  );
  expect(summary, `axe violations on ${label}`).toEqual([]);
}

/** Scans the page already on screen in all four scheme and viewport combinations, then checks reflow at 320 px.
 * `prepare` re-establishes any transient state (an open menu, typed input) that a resize would reset. */
async function scanAll(page: Page, name: string): Promise<void> {
  for (const vp of VIEWPORTS) {
    await page.setViewportSize({ width: vp.width, height: vp.height });
    for (const scheme of SCHEMES) {
      await page.emulateMedia({ colorScheme: scheme });
      await settle(page);
      await runAxe(page, `${name} [${scheme}, ${vp.name}]`);
    }
  }
  await page.emulateMedia({ colorScheme: "light" });
  await page.setViewportSize({ width: 320, height: 640 });
  await settle(page);
  const overflow = await page.evaluate(() => ({
    scroll: document.documentElement.scrollWidth,
    client: document.documentElement.clientWidth,
  }));
  expect(overflow.scroll, `${name}: horizontal page scroll at 320 px (scrollWidth ${overflow.scroll})`).toBeLessThanOrEqual(overflow.client);
  await page.setViewportSize({ width: 1280, height: 900 });
}

let originalConfig: Record<string, unknown> & { materials: Array<{ name: string; public?: boolean }> };
const created: { clients: string[]; items: string[]; invoices: string[]; requests: string[] } = {
  clients: [],
  items: [],
  invoices: [],
  requests: [],
};
let clientId = "";
let invoiceId = "";
let requestId = "";

test.beforeAll(async ({ request }) => {
  // The shop form needs a public material.
  const getRes = await request.get("/admin/api/settings");
  expect(getRes.ok()).toBeTruthy();
  originalConfig = (await getRes.json()).config;
  const updated = structuredClone(originalConfig);
  updated.materials = updated.materials.map((m) => (m.name === "PLA" ? { ...m, public: true } : m));
  expect((await request.put("/admin/api/settings", { data: { config: updated } })).ok()).toBeTruthy();

  const client = await request.post("/admin/api/clients", {
    data: { name: CLIENT_NAME, email: "gate@example.com", phone: "", billingAddress: "", shippingAddress: "", notes: "" },
  });
  expect(client.ok()).toBeTruthy();
  clientId = (await client.json()).id;
  created.clients.push(clientId);

  const item = await request.post("/admin/api/items", {
    data: {
      name: ITEM_NAME,
      description: "A small printed planter.",
      priceCents: 1200,
      optionLabel: "",
      options: [],
      public: true,
      stockMode: "in_stock",
      stockQty: 3,
      sortOrder: 0,
    },
  });
  expect(item.ok()).toBeTruthy();
  const itemId = (await item.json()).id as string;
  created.items.push(itemId);

  const invoice = await request.post("/admin/api/invoices", {
    data: { clientName: CLIENT_NAME, lines: [{ description: "Bracket", quantity: 2, unitPriceCents: 1250 }] },
  });
  expect(invoice.ok()).toBeTruthy();
  invoiceId = (await invoice.json()).id;
  created.invoices.push(invoiceId);

  await request.post("/admin/api/quotes", { data: seedQuoteBody("a11y-gate-quote") });

  // The public items list is cached for 5 s with no write invalidation: wait until the new item is served.
  await expect
    .poll(async () => ((await (await request.get("/api/public/items")).json()).items as { name: string }[]).some((i) => i.name === ITEM_NAME), {
      timeout: 15_000,
      intervals: [500, 500, 1000],
    })
    .toBe(true);

  const order = await request.post("/api/public/item-order", {
    headers: { "X-Forwarded-For": "203.0.113.10" },
    data: { itemId, optionName: "", quantity: 1, name: "Gate Visitor", email: "visitor@example.com", note: "" },
  });
  expect(order.ok()).toBeTruthy();
  requestId = (await order.json()).id;
  created.requests.push(requestId);
});

test.afterAll(async ({ request }) => {
  await cleanup(request);
});

async function cleanup(request: APIRequestContext): Promise<void> {
  for (const id of created.requests) await request.delete(`/admin/api/requests/${id}`);
  for (const id of created.invoices) await request.delete(`/admin/api/invoices/${id}`);
  for (const id of created.items) await request.delete(`/admin/api/items/${id}`);
  for (const id of created.clients) await request.delete(`/admin/api/clients/${id}`);
  const quotes = await request.get("/admin/api/quotes");
  if (quotes.ok()) {
    for (const q of ((await quotes.json()) as { quotes: { id: string; label: string }[] }).quotes) {
      if (q.label === "a11y-gate-quote") await request.delete(`/admin/api/quotes/${q.id}`);
    }
  }
  if (originalConfig) await request.put("/admin/api/settings", { data: { config: originalConfig } });
}

test.describe("login", () => {
  test("login screen and its error state", async ({ page }) => {
    test.setTimeout(120_000);
    await page.goto(ADMIN_URL);
    await expect(page.getByLabel("Password")).toBeVisible();
    await expect(page).toHaveTitle("Sign in · printpaca");
    await scanAll(page, "login");
    await page.getByRole("button", { name: "Sign in" }).click();
    await expect(page.getByRole("alert").filter({ hasText: "Enter your password." })).toBeVisible();
    await expect(page.getByLabel("Password")).toBeFocused();
    await scanAll(page, "login error");
  });
});

test.describe("public pages", () => {
  test("front door", async ({ page }) => {
    test.setTimeout(120_000);
    await page.goto("/");
    await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
    await scanAll(page, "front door");
  });

  test("shop: gallery, item panel, empty form, and form with an estimate", async ({ page }) => {
    test.setTimeout(240_000);
    await page.goto("/order");
    await expect(page.getByRole("button", { name: new RegExp(ITEM_NAME) })).toBeVisible();
    await scanAll(page, "shop gallery");

    await page.getByRole("button", { name: new RegExp(ITEM_NAME) }).click();
    await expect(page.getByRole("heading", { name: ITEM_NAME })).toBeFocused();
    await scanAll(page, "shop item panel");

    await page.getByRole("button", { name: /Back to gallery/ }).click();
    // Focus returns to the card that opened the panel.
    await expect(page.getByRole("button", { name: new RegExp(ITEM_NAME) })).toBeFocused();

    await page.getByRole("button", { name: "Custom estimate" }).click();
    await expect(page.getByLabel("Material")).toBeVisible();
    await scanAll(page, "shop estimate form (empty)");

    await page.getByLabel("Material").selectOption("PLA");
    await page.getByLabel("Quantity").fill("1");
    await page.getByLabel(/Model weight/).fill("50");
    await page.getByLabel(/^Print time \(h/).fill("1");
    await page.getByLabel(/Print time \(minutes\)/).fill("20");
    await expect(page.locator(".estimate .price")).toBeVisible();
    await scanAll(page, "shop estimate form (with estimate)");

    // Validation surfaces even though the price has resolved: name and email are tied to their messages.
    await page.getByRole("button", { name: "Request an estimate" }).click();
    await expect(page.getByLabel("Name")).toBeFocused();
    await expect(page.getByLabel("Name")).toHaveAttribute("aria-invalid", "true");
    await expect(page.getByLabel("Name")).toHaveAccessibleDescription("Name is required.");
    await scanAll(page, "shop estimate form (validation)");
  });

  test("a submitted item order moves focus to the confirmation heading", async ({ page }) => {
    await page.setExtraHTTPHeaders({ "X-Forwarded-For": "203.0.113.20" });
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto("/order");
    await page.getByRole("button", { name: new RegExp(ITEM_NAME) }).click();
    await page.getByLabel("Name").fill("Focus Visitor");
    await page.getByLabel("Email").fill("focus@example.com");
    await page.getByRole("button", { name: "Place order" }).click();
    const heading = page.getByRole("heading", { name: "Order received" });
    await expect(heading).toBeFocused();
    const ref = (await page.locator(".confirmation .ref").textContent())?.replace("Reference: ", "").trim() ?? "";
    if (ref) created.requests.push(ref);
    await scanAll(page, "shop order confirmation");
  });

  test("a submitted estimate request moves focus to the confirmation heading", async ({ page }) => {
    await page.setExtraHTTPHeaders({ "X-Forwarded-For": "203.0.113.30" });
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto("/order");
    await page.getByRole("button", { name: "Custom estimate" }).click();
    await page.getByLabel("Material").selectOption("PLA");
    await page.getByLabel(/Model weight/).fill("50");
    await page.getByLabel(/^Print time \(h/).fill("1");
    await page.getByLabel(/Print time \(minutes\)/).fill("20");
    await page.getByLabel("Name").fill("Focus Visitor");
    await page.getByLabel("Email").fill("focus@example.com");
    await expect(page.locator(".estimate .price")).toBeVisible();
    await page.getByRole("button", { name: "Request an estimate" }).click();
    await expect(page.getByRole("heading", { name: "Request received" })).toBeFocused();
    const ref = (await page.locator(".confirmation .ref").textContent())?.replace("Reference: ", "").trim() ?? "";
    if (ref) created.requests.push(ref);
    await scanAll(page, "shop estimate confirmation");
  });
});

const TABS: { name: string; hash: string }[] = [
  { name: "New quote", hash: "#/quote" },
  { name: "Quotes", hash: "#/quotes" },
  { name: "Requests", hash: "#/requests" },
  { name: "Invoices", hash: "#/invoices" },
  { name: "Items", hash: "#/items" },
  { name: "Clients", hash: "#/clients" },
  { name: "Dashboard", hash: "#/dashboard" },
  { name: "Filament", hash: "#/filament" },
  { name: "Electricity", hash: "#/electricity" },
  { name: "Printers", hash: "#/printers" },
  { name: "Settings", hash: "#/settings" },
  { name: "Guide", hash: "#/guide" },
];

test.describe("admin tabs", () => {
  for (const tab of TABS) {
    test(`tab: ${tab.name}`, async ({ page }) => {
      test.setTimeout(120_000);
      await page.goto(`/admin/${tab.hash}`);
      await expect(page.locator("main h1").first()).toBeVisible();
      await expect(page).toHaveTitle(/ · printpaca$/);
      await scanAll(page, `admin ${tab.name}`);
    });
  }
});

test.describe("admin detail views", () => {
  test("invoice detail", async ({ page }) => {
    test.setTimeout(120_000);
    await page.goto(`/admin/#/invoices#${invoiceId}`);
    await expect(page.locator("main h1")).toHaveCount(1);
    await scanAll(page, "admin invoice detail");
  });

  test("client detail", async ({ page }) => {
    test.setTimeout(120_000);
    await page.goto(`/admin/#/clients#${clientId}`);
    await expect(page.locator("main h1")).toHaveCount(1);
    await scanAll(page, "admin client detail");
  });

  test("request detail", async ({ page }) => {
    test.setTimeout(120_000);
    await page.goto(`/admin/#/requests#${requestId}`);
    await expect(page.locator("main h1")).toHaveCount(1);
    await scanAll(page, "admin request detail");
  });

  test("item detail", async ({ page }) => {
    test.setTimeout(120_000);
    await page.goto("/admin/#/items");
    await page.locator("main").getByRole("button", { name: new RegExp(ITEM_NAME) }).first().click();
    await expect(page.locator("main h1")).toHaveCount(1);
    await scanAll(page, "admin item detail");
  });

  test("a delete confirm keeps focus on a control inside the page", async ({ page }) => {
    await page.goto(`/admin/#/clients#${clientId}`);
    await expect(page.locator("main h1")).toHaveCount(1);
    await page.getByRole("button", { name: /^Delete client/ }).click();
    // The trigger is replaced by the confirm and cancel buttons; focus must land on one of them, not <body>.
    await expect(page.locator("main :focus")).toHaveCount(1);
    await expect(page.locator("main :focus")).toHaveText(/Delete client\?|Cancel/);
  });
});

test.describe("mobile drawer focus", () => {
  test.use({ viewport: { width: 390, height: 844 } });

  test("focus moves into the drawer, back to the menu button, and to main after a pick", async ({ page }) => {
    await page.goto("/admin/");
    const menu = page.getByRole("button", { name: /^Open menu/ });
    await menu.focus();
    await page.keyboard.press("Enter");
    const drawer = page.locator("#app-drawer");
    await expect(drawer).toHaveClass(/open/);
    await expect(drawer.locator("button, a[href]").first()).toBeFocused();

    await page.keyboard.press("Escape");
    await expect(page.getByRole("button", { name: /^Open menu/ })).toBeFocused();

    await page.getByRole("button", { name: /^Open menu/ }).click();
    await expect(drawer.locator("button, a[href]").first()).toBeFocused();
    await navButton(page, "Clients").click();
    await expect(page.locator("#main")).toBeFocused();
    await expect(page).toHaveTitle("Clients · printpaca");
  });
});

test.describe("keyboard shortcuts", () => {
  test("a bare slash is not a shortcut", async ({ page }) => {
    await page.goto("/admin/#/quotes");
    await expect(page.locator("main h1").first()).toBeVisible();
    await page.keyboard.press("/");
    await expect(page.locator("body")).toBeFocused();
  });
});
