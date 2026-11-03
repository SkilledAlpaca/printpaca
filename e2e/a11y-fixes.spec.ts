import { test, expect } from "@playwright/test";
import type { APIRequestContext, Page } from "@playwright/test";
import { seedQuoteBody } from "./helpers";

// Regression checks for the WCAG 2.2 review fixes: in-flight guards on aria-disabled controls, repeat
// activation on two-step confirms, the dashboard chart's narrow-width labels, and the public forms' email
// check and post-submit privacy reset. Shares the suite's 8099 server and removes what it creates.

const PREFIX = "a11y-fix";

async function deleteQuotesLabeled(request: APIRequestContext, label: string): Promise<void> {
  const res = await request.get("/admin/api/quotes");
  if (!res.ok()) return;
  for (const q of ((await res.json()) as { quotes: { id: string; label: string }[] }).quotes) {
    if (q.label === label) await request.delete(`/admin/api/quotes/${q.id}`);
  }
}

async function deleteClientsNamed(request: APIRequestContext, name: string): Promise<void> {
  const res = await request.get("/admin/api/clients");
  if (!res.ok()) return;
  const body = (await res.json()) as { clients?: { id: string; name: string }[] } | { id: string; name: string }[];
  const list = Array.isArray(body) ? body : (body.clients ?? []);
  for (const c of list) if (c.name === name) await request.delete(`/admin/api/clients/${c.id}`);
}

test.describe("quote email double submit", () => {
  const LABEL = `${PREFIX}-email-quote`;
  const CLIENT = `${PREFIX} Email Client`;

  test.afterEach(async ({ request }) => {
    await deleteQuotesLabeled(request, LABEL);
    await deleteClientsNamed(request, CLIENT);
  });

  async function openEmailEntry(page: Page): Promise<{ sends: () => number }> {
    let sendCount = 0;
    // Email only works with SMTP configured: report a host in the config the app loads.
    await page.route(/\/api\/config(\?.*)?$/, async (route) => {
      const res = await route.fetch();
      const cfg = await res.json();
      cfg.smtp = { ...cfg.smtp, host: "smtp.example.test" };
      await route.fulfill({ response: res, json: cfg });
    });
    // The seeded quote has no server-computed price; give it one so "Email quote" is offered.
    await page.route(/\/api\/quotes(\?.*)?$/, async (route) => {
      if (route.request().method() !== "GET") return route.fallback();
      const res = await route.fetch();
      const body = await res.json();
      body.quotes = body.quotes.map((q: { label: string }) => (q.label === LABEL ? { ...q, resolvedPriceCents: 2500 } : q));
      await route.fulfill({ response: res, json: body });
    });
    await page.route("**/api/quotes/send", async (route) => {
      sendCount += 1;
      await new Promise((r) => setTimeout(r, 600)); // long enough for a second Enter or click to land mid-flight
      await route.fulfill({ json: { sentAt: new Date().toISOString() } });
    });
    await page.goto("/admin/#/quotes");
    const row = page.locator("table tbody tr", { hasText: LABEL });
    await expect(row).toBeVisible();
    await row.getByRole("button", { name: /^More actions for/ }).click();
    await page.getByRole("menuitem", { name: /Email quote/ }).or(page.getByRole("button", { name: /Email quote/ })).first().click();
    await page.getByLabel("Client email for this quote").fill("double@example.com");
    return { sends: () => sendCount };
  }

  async function seed(request: APIRequestContext): Promise<void> {
    const body = seedQuoteBody(LABEL) as Record<string, unknown> & { formState: { order: Record<string, unknown> } };
    body.clientName = CLIENT;
    body.formState.order.sellingEnabled = true;
    const res = await request.post("/admin/api/quotes", { data: body });
    expect(res.ok(), await res.text()).toBe(true);
  }

  test("a double Enter sends exactly one email", async ({ page, request }) => {
    await seed(request);
    const { sends } = await openEmailEntry(page);
    const input = page.getByLabel("Client email for this quote");
    await input.press("Enter");
    await input.press("Enter");
    await expect(page.getByText(/Emailed .* to double@example.com/)).toBeVisible();
    expect(sends()).toBe(1);
  });

  test("a double click on Send sends exactly one email", async ({ page, request }) => {
    await seed(request);
    const { sends } = await openEmailEntry(page);
    await page.getByRole("button", { name: "Send", exact: true }).dblclick();
    await expect(page.getByText(/Emailed .* to double@example.com/)).toBeVisible();
    expect(sends()).toBe(1);
  });
});

test("a held Enter on a Delete button does not confirm the delete", async ({ page, request }) => {
  const name = `${PREFIX} Hold Item`;
  const created = await request.post("/admin/api/items", {
    data: { name, description: "", priceCents: 500, public: false, stockMode: "in_stock", stockQty: 1, sortOrder: 0 },
  });
  expect(created.ok()).toBe(true);
  const id = (await created.json()).id as string;
  try {
    await page.goto("/admin/#/items");
    await page.locator("button.row-open", { hasText: name }).click();
    const trigger = page.getByRole("button", { name: "Delete shop item", exact: true });
    await trigger.focus();
    await page.keyboard.down("Enter"); // activates the trigger; the confirm button takes focus
    const confirm = page.getByRole("button", { name: /^Yes, delete / });
    await expect(confirm).toBeFocused();
    for (let i = 0; i < 6; i++) await page.keyboard.down("Enter"); // auto-repeat while the key stays down
    await page.keyboard.up("Enter");
    await page.waitForTimeout(300);
    await expect(confirm).toBeVisible();
    expect((await request.get("/admin/api/items")).ok()).toBe(true);
    const list = (await (await request.get("/admin/api/items")).json()) as { items?: { id: string }[] } | { id: string }[];
    const items = Array.isArray(list) ? list : (list.items ?? []);
    expect(items.some((x) => x.id === id)).toBe(true);
  } finally {
    await request.delete(`/admin/api/items/${id}`);
  }
});

test.describe("revenue chart labels", () => {
  async function mockRevenue(page: Page): Promise<void> {
    await page.route("**/api/dashboard", async (route) => {
      const res = await route.fetch();
      const d = await res.json();
      const now = new Date();
      d.revenueByMonth = Array.from({ length: 12 }, (_, i) => {
        const dt = new Date(now.getFullYear(), now.getMonth() - 11 + i, 1);
        return { month: `${dt.getFullYear()}-${String(dt.getMonth() + 1).padStart(2, "0")}`, cents: 10000 + i * 2500 };
      });
      await route.fulfill({ response: res, json: d });
    });
  }

  for (const [width, visible] of [[390, 6], [1280, 12]] as const) {
    test(`at ${width}px the axis shows ${visible} labels without overlap and the table keeps 12`, async ({ page }) => {
      await page.setViewportSize({ width, height: 900 });
      await mockRevenue(page);
      await page.goto("/admin/#/dashboard");
      const labels = page.locator(".revenue-chart .axis-label");
      await expect(labels.first()).toBeVisible();
      const boxes: { x: number; w: number }[] = [];
      for (const el of await labels.all()) {
        if (!(await el.isVisible())) continue;
        const b = await el.boundingBox();
        if (b && b.width > 0) boxes.push({ x: b.x, w: b.width });
      }
      expect(boxes).toHaveLength(visible);
      boxes.sort((a, b) => a.x - b.x);
      for (let i = 1; i < boxes.length; i++) {
        const prev = boxes[i - 1]!;
        expect(boxes[i]!.x, `label ${i} overlaps label ${i - 1}`).toBeGreaterThanOrEqual(prev.x + prev.w);
      }
      await expect(page.locator(".chart-data tbody tr")).toHaveCount(12);
    });
  }
});

test.describe("public forms", () => {
  const ITEM = `${PREFIX} Shop Item`;
  let itemId = "";
  let originalConfig: Record<string, unknown> | null = null;

  test.beforeAll(async ({ request }) => {
    // The custom-estimate toggle needs a public material.
    const getRes = await request.get("/admin/api/settings");
    expect(getRes.ok()).toBe(true);
    originalConfig = (await getRes.json()).config;
    const updated = structuredClone(originalConfig) as { materials: { name: string }[] };
    updated.materials = updated.materials.map((m) => (m.name === "PLA" ? { ...m, public: true } : m));
    expect((await request.put("/admin/api/settings", { data: { config: updated } })).ok()).toBe(true);

    const item = await request.post("/admin/api/items", {
      data: { name: ITEM, description: "", priceCents: 700, public: true, stockMode: "in_stock", stockQty: 5, sortOrder: 0 },
    });
    expect(item.ok()).toBe(true);
    itemId = (await item.json()).id as string;
    await expect
      .poll(async () => ((await (await request.get("/api/public/items")).json()).items as { name: string }[]).some((i) => i.name === ITEM), {
        timeout: 15_000,
        intervals: [500, 500, 1000],
      })
      .toBe(true);
  });

  test.afterAll(async ({ request }) => {
    const res = await request.get("/admin/api/requests");
    if (res.ok()) {
      const body = (await res.json()) as { requests?: { id: string; name: string }[] } | { id: string; name: string }[];
      const list = Array.isArray(body) ? body : (body.requests ?? []);
      for (const r of list) if (r.name.startsWith(PREFIX)) await request.delete(`/admin/api/requests/${r.id}`);
    }
    await request.delete(`/admin/api/items/${itemId}`);
    if (originalConfig) await request.put("/admin/api/settings", { data: { config: originalConfig } });
  });

  test("an address without a domain dot is rejected with one clear message", async ({ page }) => {
    await page.setExtraHTTPHeaders({ "X-Forwarded-For": "203.0.113.41" });
    await page.goto("/order");
    await page.getByRole("button", { name: new RegExp(ITEM) }).click();
    await page.getByLabel("Name").fill(`${PREFIX} Visitor`);
    for (const bad of ["jo@", "@gmail.com", "a@b", "a b@c.d"]) {
      await page.getByLabel("Email").fill(bad);
      await page.getByRole("button", { name: "Place order" }).click();
      await expect(page.getByText("Enter an email address like name@example.com")).toBeVisible();
      await expect(page.getByLabel("Email")).toBeFocused();
    }
  });

  test("name and email carry between forms while unsent, and are cleared after an order", async ({ page }) => {
    await page.setExtraHTTPHeaders({ "X-Forwarded-For": "203.0.113.42" });
    await page.goto("/order");
    await page.getByRole("button", { name: new RegExp(ITEM) }).click();
    await page.getByLabel("Name").fill(`${PREFIX} Carry`);
    await page.getByLabel("Email").fill("carry@example.com");
    await page.getByRole("button", { name: /Back to gallery/ }).click();
    await page.getByRole("button", { name: "Custom estimate" }).click();
    await expect(page.getByLabel("Name")).toHaveValue(`${PREFIX} Carry`);
    await expect(page.getByLabel("Email")).toHaveValue("carry@example.com");
    await page.getByRole("button", { name: "Gallery", exact: true }).click();
    await page.getByRole("button", { name: new RegExp(ITEM) }).click();
    await page.getByRole("button", { name: "Place order" }).click();
    await expect(page.getByRole("heading", { name: "Order received" })).toBeFocused();
    await page.getByRole("button", { name: /Back to gallery/ }).click();
    await page.getByRole("button", { name: "Custom estimate" }).click();
    await expect(page.getByLabel("Name")).toHaveValue("");
    await expect(page.getByLabel("Email")).toHaveValue("");
  });
});

test.describe("confirm before irreversible actions (3.3.4)", () => {
  test("a quote status change asks first, Cancel returns focus, and confirming applies it", async ({ page, request }) => {
    const label = `${PREFIX}-confirm-quote`;
    const res = await request.post("/admin/api/quotes", { data: seedQuoteBody(label) });
    expect(res.ok(), await res.text()).toBe(true);
    try {
      await page.goto("/admin/#/quotes");
      const row = page.locator("table tbody tr", { hasText: label });
      const trigger = row.getByRole("button", { name: "Mark sent", exact: true });
      await trigger.click();
      const confirm = page.getByRole("button", { name: `Mark "${label}" as sent?` });
      await expect(confirm).toBeFocused();
      await page.getByRole("button", { name: "Cancel", exact: true }).click();
      await expect(trigger).toBeFocused();
      await expect(confirm).toHaveCount(0);
      await trigger.click();
      await page.getByRole("button", { name: `Mark "${label}" as sent?` }).click();
      await expect(page.getByText(`"${label}" marked sent.`)).toBeVisible();
    } finally {
      await deleteQuotesLabeled(request, label);
    }
  });

  test("Email to client on an invoice asks first and sends one request after confirming", async ({ page, request }) => {
    const client = await request.post("/admin/api/clients", {
      data: { name: `${PREFIX} Mail Client`, email: "mail@example.com", phone: "", billingAddress: "", shippingAddress: "", notes: "" },
    });
    expect(client.ok(), await client.text()).toBe(true);
    const clientId = (await client.json()).id as string;
    const created = await request.post("/admin/api/invoices", {
      data: { clientId, clientName: `${PREFIX} Mail Client`, lines: [{ description: "Bracket", quantity: 1, unitPriceCents: 1000 }] },
    });
    expect(created.ok(), await created.text()).toBe(true);
    const inv = (await created.json()) as { id: string; number: string };
    let sends = 0;
    await page.route(/\/api\/config(\?.*)?$/, async (route) => {
      const res = await route.fetch();
      const cfg = await res.json();
      cfg.smtp = { ...cfg.smtp, host: "smtp.example.test" };
      await route.fulfill({ response: res, json: cfg });
    });
    await page.route(/\/api\/invoices\/[^/]+\/send$/, async (route) => {
      sends += 1;
      await route.fulfill({ status: 502, json: { errors: ["stub: not sent"] } });
    });
    try {
      await page.goto("/admin/#/invoices");
      await page.locator(".plain-list li, table tbody tr", { hasText: inv.number }).first().getByRole("button").first().click();
      const trigger = page.getByRole("button", { name: "Email to client", exact: true });
      await trigger.click();
      const confirm = page.getByRole("button", { name: new RegExp(`^Send ${inv.number} to `) });
      await expect(confirm).toBeFocused();
      await page.getByRole("button", { name: "Cancel", exact: true }).click();
      await expect(trigger).toBeFocused();
      expect(sends).toBe(0);
      await trigger.click();
      await confirm.click();
      await expect(page.getByText("stub: not sent")).toBeVisible();
      expect(sends).toBe(1);
    } finally {
      await request.delete(`/admin/api/invoices/${inv.id}`);
      await request.delete(`/admin/api/clients/${clientId}`);
    }
  });
});
