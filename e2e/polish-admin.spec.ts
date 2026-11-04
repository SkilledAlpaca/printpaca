import { test, expect } from "@playwright/test";
import type { APIRequestContext } from "@playwright/test";
import { ADMIN_URL, navButton, resetServerState, seedQuoteBody } from "./helpers";

// v1.1.0 round 3 polish, admin items 9 to 26. Shares the suite's one server (8099), so every record this spec
// creates is deleted again; the staff test (item 25) uses the :9099 server with ADMIN_PASSWORD.

const created = { invoices: [] as string[], requests: [] as string[], quotes: [] as string[] };
let xff = 0;
function nextXff(): string {
  xff += 1;
  return `198.51.100.${100 + xff}`; // a block no other spec uses
}

async function seedInvoice(request: APIRequestContext, body: Record<string, unknown>): Promise<{ id: string; number: string }> {
  const res = await request.post("/admin/api/invoices", { data: body });
  expect(res.status(), await res.text()).toBe(201);
  const inv = (await res.json()) as { id: string; number: string };
  created.invoices.push(inv.id);
  return inv;
}

async function seedRequest(request: APIRequestContext, name: string): Promise<string> {
  const res = await request.post("/api/public/quote-request", {
    headers: { "X-Forwarded-For": nextXff() },
    multipart: { name, email: `${name.toLowerCase().replace(/[^a-z]+/g, ".")}@example.com`, note: "", material: "PLA", quantity: "1", weightG: "12", timeSeconds: "3600" },
  });
  expect(res.status(), await res.text()).toBe(201);
  const id = ((await res.json()) as { id: string }).id;
  created.requests.push(id);
  return id;
}

let originalConfig: (Record<string, unknown> & { materials: Array<{ name: string; public?: boolean }> }) | null = null;

test.beforeAll(async ({ request }) => {
  const getRes = await request.get("/admin/api/settings");
  expect(getRes.ok()).toBeTruthy();
  originalConfig = (await getRes.json()).config;
  const updated = structuredClone(originalConfig!);
  updated.materials = updated.materials.map((m) => (m.name === "PLA" ? { ...m, public: true } : m));
  const putRes = await request.put("/admin/api/settings", { data: { config: updated } });
  expect(putRes.ok(), await putRes.text()).toBeTruthy();
});

test.afterAll(async ({ request }) => {
  for (const id of created.invoices) {
    const res = await request.delete(`/admin/api/invoices/${id}`);
    if (!res.ok()) {
      const inv = await request.get(`/admin/api/invoices/${id}`);
      if (inv.ok()) {
        const body = (await inv.json()) as { payments: { id: string }[] };
        for (const p of body.payments) await request.delete(`/admin/api/invoices/${id}/payments/${p.id}`);
        await request.post(`/admin/api/invoices/${id}/status`, { data: { status: "canceled" } });
      }
    }
  }
  for (const id of created.requests) await request.delete(`/admin/api/requests/${id}`);
  for (const id of created.quotes) await request.delete(`/admin/api/quotes/${id}`);
  if (originalConfig) await request.put("/admin/api/settings", { data: { config: originalConfig } });
});

test.describe("invoices", () => {
  test("Record payment follows the balance after a save, the amount field is named by its visible label, and the URL is #/invoices/<id>", async ({ page }) => {
    await page.goto("/admin/");
    await navButton(page, "Invoices").click();
    await page.getByRole("button", { name: "New invoice" }).click();
    await page.getByLabel("Client name").fill("Polish Payment Client");
    await page.getByRole("button", { name: "Add line" }).click();
    await page.getByLabel("Line 1 description").fill("Desk hook");
    await page.getByLabel("Line 1 quantity").fill("1");
    await page.getByLabel("Line 1 unit price").fill("6");
    await page.getByRole("button", { name: "Save" }).click();
    await expect(page.getByRole("heading", { name: /^Invoice INV-/ })).toBeVisible();
    const number = ((await page.getByRole("heading", { name: /^Invoice INV-/ }).textContent()) ?? "").replace("Invoice ", "").trim();

    // Item 9: not "Record $0.00".
    await expect(page.getByRole("button", { name: /^Record \$6\.00 .*today$/ })).toBeVisible();
    await expect(page.getByRole("button", { name: /^Record \$0\.00/ })).toHaveCount(0);

    // Item 15: no aria-label overriding the visible "Amount ($)" text.
    const amount = page.locator(".payments-card").getByLabel(/^Amount/);
    await expect(amount).not.toHaveAttribute("aria-label", /.+/);
    await expect(amount).toHaveValue("6");

    // Item 20: the address carries the id, and reload opens the same invoice; the older form still works.
    await expect(page).toHaveURL(/#\/invoices\/[0-9a-f-]+$/);
    const url = page.url();
    const id = url.split("/").pop()!;
    created.invoices.push(id);
    await page.reload();
    await expect(page.getByRole("heading", { name: `Invoice ${number}` })).toBeVisible();
    await page.goto(`/admin/#/invoices#${id}`);
    await page.reload();
    await expect(page.getByRole("heading", { name: `Invoice ${number}` })).toBeVisible();
  });

  test("lines show an inline error as you type and leave the line amount blank", async ({ page }) => {
    await page.goto("/admin/");
    await navButton(page, "Invoices").click();
    await page.getByRole("button", { name: "New invoice" }).click();
    await page.getByRole("button", { name: "Add line" }).click();
    await page.getByLabel("Line 1 quantity").fill("-2");
    await expect(page.getByText("Must be greater than zero.")).toBeVisible();
    await page.getByLabel("Line 1 unit price").fill("0.333");
    await expect(page.getByText("Use at most two decimal places.")).toBeVisible();
    await expect(page.getByLabel("Line 1 quantity")).toHaveAttribute("aria-invalid", "true");
    await expect(page.locator("tbody tr").first().locator("td.num")).toHaveText("-");
    await page.getByLabel("Line 1 quantity").fill("2");
    await page.getByLabel("Line 1 unit price").fill("0.33");
    await expect(page.getByText("Use at most two decimal places.")).toHaveCount(0);
    await expect(page.locator("tbody tr").first().locator("td.num")).toHaveText("$0.66");
  });

  test("a partially paid invoice's chip stays on one line at 390 px", async ({ page, request }) => {
    const inv = await seedInvoice(request, { clientName: "Chip Client", lines: [{ description: "Widget", quantity: 1, unitPriceCents: 2000 }] });
    await request.post(`/admin/api/invoices/${inv.id}/status`, { data: { status: "sent" } });
    const pay = await request.post(`/admin/api/invoices/${inv.id}/payments`, { data: { paidDate: "2026-01-02", amountCents: 500, method: "cash", note: "", reference: "" } });
    expect(pay.ok(), await pay.text()).toBeTruthy();
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto("/admin/#/invoices");
    const row = page.locator("tbody tr", { hasText: inv.number });
    const pill = row.getByText("Partially paid");
    await expect(pill).toBeVisible();
    const box = (await pill.boundingBox())!;
    expect(box.height).toBeLessThan(28); // one line of text-xs plus padding
    const badge = (await row.locator(".badge").boundingBox())!;
    expect(box.y).toBeGreaterThanOrEqual(badge.y + badge.height - 1); // under the badge, not beside it
  });

  test("an invoice created from a request says so and links back; the request hides the Convert help", async ({ page, request }) => {
    const name = `Polish Req ${Date.now() % 100000}`;
    const id = await seedRequest(request, name);
    await page.goto("/admin/");
    await navButton(page, "Requests").click();
    await page.locator(".plain-list li", { hasText: name }).getByRole("button", { name: new RegExp(name) }).click();
    await expect(page.getByText(/Convert to invoice creates the client/)).toBeVisible();
    await page.getByRole("button", { name: "Convert to invoice" }).click();
    await expect(page.getByRole("heading", { name: /^Invoice INV-/ })).toBeVisible();
    const stored = (await (await request.get(`/admin/api/requests/${id}`)).json()) as { reference: string; invoiceId: string };
    created.invoices.push(stored.invoiceId);
    await expect(page.getByText(/Created from request/)).toBeVisible();
    await page.getByRole("button", { name: stored.reference, exact: true }).click();
    await expect(page.getByRole("heading", { name: `Estimate request from ${name}` })).toBeVisible();
    await expect(page.getByText(/Convert to invoice creates the client/)).toHaveCount(0);
  });
});

test.describe("dashboard", () => {
  test("Needs attention lists money first, caps each kind at 3 with View all, adds sent quotes; stat cards link", async ({ page, request }) => {
    for (let i = 1; i <= 4; i++) {
      await seedInvoice(request, { clientName: `Draft Client ${i}`, lines: [{ description: "Widget", quantity: 1, unitPriceCents: 500 }] });
    }
    await seedRequest(request, `Polish Dash ${Date.now() % 100000}`);
    const q = await request.post("/admin/api/quotes", { data: { ...seedQuoteBody(`Polish Sent ${Date.now() % 100000}`), clientName: "Sent Client", status: "sent" } });
    expect(q.status(), await q.text()).toBe(201);
    created.quotes.push(((await q.json()) as { id: string }).id);

    await page.goto("/admin/#/dashboard");
    const section = page.getByRole("region", { name: "Needs attention" });
    await expect(section).toBeVisible();
    const kinds = await section.locator(".attention-kind").allTextContents();
    const rank = ["Overdue invoice", "Draft invoice", "Needs a print time", "New request", "Quote awaiting reply"];
    const ranks = kinds.map((k) => rank.indexOf(k));
    expect(ranks.every((r) => r >= 0), kinds.join(" | ")).toBe(true);
    expect([...ranks].sort((a, b) => a - b)).toEqual(ranks);
    for (const k of rank) expect(kinds.filter((x) => x === k).length).toBeLessThanOrEqual(3);
    expect(kinds).toContain("Quote awaiting reply");
    await expect(section.getByRole("link", { name: /^View all \d+ draft invoices$/ })).toHaveAttribute("href", "#/invoices");

    const outstanding = page.getByRole("link", { name: /Outstanding/ });
    await expect(outstanding).toHaveAttribute("href", "#/invoices");
    await page.getByRole("link", { name: /Quotes this month/ }).click();
    await expect(page).toHaveURL(/#\/quotes$/);
  });
});

test.describe("quotes list", () => {
  const stamp = Date.now() % 100000;
  const labels = ["A", "B"].map((s) => `Polish Row ${stamp} ${s} with a longer label`);

  test.beforeAll(async ({ request }) => {
    for (const [i, label] of labels.entries()) {
      const res = await request.post("/admin/api/quotes", { data: { ...seedQuoteBody(label), clientName: `Client ${i}`, status: i === 0 ? "draft" : "sent" } });
      expect(res.status(), await res.text()).toBe(201);
      created.quotes.push(((await res.json()) as { id: string }).id);
    }
  });

  test("at 1280 px no ⋯ is clipped and labels stay within three lines", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.goto("/admin/#/quotes");
    const rows = page.locator("tbody tr.quote-row");
    await expect(rows.first()).toBeVisible();
    const wrapper = (await page.locator(".table-scroll").first().boundingBox())!;
    const count = await rows.count();
    for (let i = 0; i < count; i++) {
      const row = rows.nth(i);
      const toggle = (await row.locator(".menu-toggle").boundingBox())!;
      expect(toggle.x + toggle.width, `row ${i} ⋯ right edge`).toBeLessThanOrEqual(wrapper.x + wrapper.width + 0.5);
      // Count the label's wrapped lines, not pixels: CI fonts differ from local ones, and the cell also
      // holds the parts badge and attribution line.
      const lines = await row.locator(".c-label [data-row-id]").evaluate((el) => {
        const lh = parseFloat(getComputedStyle(el).lineHeight) || parseFloat(getComputedStyle(el).fontSize) * 1.5;
        const rects = Array.from(el.getClientRects());
        const tops = new Set(rects.map((r) => Math.round(r.top)));
        return Math.max(tops.size, Math.round(el.getBoundingClientRect().height / lh));
      });
      expect(lines, `row ${i} label lines`).toBeLessThanOrEqual(3);
    }
  });

  test("Tab from the last menu item closes the menu and leaves focus on the page", async ({ page }) => {
    await page.goto("/admin/#/quotes");
    await page.getByRole("button", { name: `More actions for "${labels[1]}"` }).click();
    const items = page.locator(".menu-panel .menu-item:not([disabled])");
    await expect(items.first()).toBeFocused();
    await page.keyboard.press("End");
    await expect(items.last()).toBeFocused();
    await page.keyboard.press("Tab");
    await expect(page.locator(".menu-panel")).toHaveCount(0);
    expect(await page.evaluate(() => document.activeElement === document.body)).toBe(false);
    // Focus moved on to the next control after the toggle, not back onto it.
    expect(await page.evaluate(() => document.activeElement?.getAttribute("aria-controls"))).toBeNull();
  });
});

test.describe("requests", () => {
  test("NEW rows get checkboxes and a Select all new button, and Finalize selected finalizes them", async ({ page, request }) => {
    const name = `Polish Bulk ${Date.now() % 100000}`;
    const id = await seedRequest(request, name);
    await page.goto("/admin/#/requests");
    const row = page.locator(".plain-list li", { hasText: name });
    await expect(row).toBeVisible();
    await expect(row.getByRole("checkbox", { name: `Select request from ${name}` })).toBeVisible();
    await page.getByLabel("Filter requests by name, item, or file").fill(name);
    await page.getByRole("button", { name: /^Select all new \(1\)$/ }).click();
    await expect(page.getByText("1 selected")).toBeVisible();
    await page.getByRole("button", { name: "Finalize selected" }).click();
    await page.getByRole("button", { name: "Yes, finalize 1 request" }).click();
    await expect(page.getByText("Finalized 1 request.")).toBeVisible();
    const stored = (await (await request.get(`/admin/api/requests/${id}`)).json()) as { status: string };
    expect(stored.status).toBe("finalized");
  });
});

test.describe("sidebar and quote page", () => {
  test("Settings is reachable at 1366x860 and the shortcut hint lives in the page footer", async ({ page }) => {
    await page.setViewportSize({ width: 1366, height: 860 });
    await page.goto("/admin/#/quote");
    const settings = navButton(page, "Settings");
    await expect(settings).toBeVisible();
    const hit = await settings.evaluate((el) => {
      const r = el.getBoundingClientRect();
      const top = document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2);
      return el === top || el.contains(top);
    });
    expect(hit).toBe(true);
    const nav = await page.locator(".side-nav").evaluate((el) => ({ sh: el.scrollHeight, ch: el.clientHeight }));
    expect(nav.sh).toBeLessThanOrEqual(nav.ch + 1);
    await expect(page.locator(".sidebar .shortcuts-line")).toHaveCount(0);
  });

  test("the cost bar names every missing field and stays in view while scrolling", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.goto("/admin/#/quote");
    const bar = page.getByRole("group", { name: "Quote summary" });
    const need = bar.locator(".cost-need a");
    await expect(need).toContainText(/^Price needs a print time and /);
    await expect(need).not.toContainText("more");
    await page.locator("main").evaluate((el) => {
      el.scrollTop = 700;
    });
    await expect.poll(async () => (await bar.boundingBox())!.y).toBeLessThan(40);
  });

  test("jargon: fee schedules, prep labor, infill, tax and shipping behind More pricing options", async ({ page }) => {
    await page.goto("/admin/#/quote");
    await page.getByLabel(/Model weight/).fill("50");
    await page.getByLabel(/^Print time \(h/).fill("1");
    await expect(page.getByText("Prep labor (per batch)", { exact: true })).toBeAttached();
    await expect(page.getByText(/amortized/)).toHaveCount(0);

    await page.getByLabel("Set a selling price").check();
    await expect(page.getByRole("option", { name: "Etsy (US)" })).toBeAttached();
    const more = page.locator("details.more-pricing");
    await expect(more).not.toHaveAttribute("open", /.*/);
    await more.locator("summary").click();
    await page.getByLabel(/^Tax \(included in price\)/).fill("7");
    await expect(more).toHaveAttribute("open", "");
  });

  test("an empty quote's breakdown shows a dash, not NaN", async ({ page }) => {
    await page.goto("/admin/#/quote");
    await expect(page.getByRole("group", { name: "Quote summary" })).toBeVisible();
    // The breakdown comes back from the server preview, so wait for the rows.
    await expect.poll(() => page.locator("table.measure-cap code").count(), { timeout: 10_000 }).toBeGreaterThan(0);
    const formulas = (await page.locator("table.measure-cap code").allTextContents()).join(" ");
    expect(formulas).not.toContain("NaN");
    expect(formulas).toContain("– g");
  });

  test("the solidity field is named Infill (solidity)", async ({ page }) => {
    // The label lives on GcodeDrop's estimate result; unit coverage is in the Svelte label itself. Smoke only:
    // the quote page loads without the old bare "Solidity" label.
    await page.goto("/admin/#/quote");
    await expect(page.getByLabel("Solidity", { exact: true })).toHaveCount(0);
  });
});

test.describe("electricity and settings", () => {
  test("rates read as $/kWh with an explanation of kWh and the 24-hour clock", async ({ page }) => {
    await page.goto("/admin/#/electricity");
    await expect(page.getByText(/A kilowatt-hour \(kWh\) is the unit on your power bill/)).toBeVisible();
    await expect(page.getByText(/Times use the 24-hour clock/)).toBeVisible();
    await page.getByLabel(/Rate for this calculation/).fill("0.2");
    await expect(page.getByRole("button", { name: /^Use configured rate \(\$0\.\d\d\/kWh\)$/ })).toBeVisible();
  });

  test("Send test email says why it is disabled and aria-disabled matches", async ({ page }) => {
    await page.goto("/admin/#/settings");
    const host = page.locator("#section-smtp").getByLabel("Host");
    await host.fill("");
    const button = page.getByRole("button", { name: "Send test email" });
    await expect(button).toHaveAttribute("aria-disabled", "true");
    await expect(button).not.toHaveAttribute("disabled", /.*/);
    await expect(page.getByText("Set an SMTP host first.")).toBeVisible();
    await host.fill("smtp.example.test");
    await expect(button).toHaveAttribute("aria-disabled", "false");
  });

  test("the logo preview sits beside the upload control and counters say characters", async ({ page }) => {
    await page.goto("/admin/#/settings");
    const png = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==", "base64");
    const upload = page.locator("#quote-logo-file");
    await upload.setInputFiles({ name: "logo.png", mimeType: "image/png", buffer: png });
    const preview = page.locator("#section-quote img.logo-preview");
    await expect(preview).toBeVisible();
    const a = (await upload.boundingBox())!;
    const b = (await preview.boundingBox())!;
    expect(Math.abs(b.y - a.y)).toBeLessThan(80);
    await expect(page.locator("#quote-payment-count")).toContainText(/ characters/);
    await expect(page.locator("#quote-payment-count")).not.toContainText("bytes");
  });
});

test.describe("staff", () => {
  test.describe.configure({ mode: "serial" });
  test.beforeAll(resetServerState);
  test.afterAll(resetServerState);

  test("opening an invoice without the Shop items grant never calls /api/items", async ({ browser }) => {
    const admin = await browser.newContext({ baseURL: ADMIN_URL });
    const api = admin.request;
    const login = await api.post("api/login", { data: { username: "", password: "e2e-admin-secret" } });
    expect(login.ok()).toBe(true);
    const cfgRes = await api.get("api/config");
    const cfg = (await cfgRes.json()) as Record<string, unknown> & { users: Record<string, unknown> };
    for (const k of ["integrations", "version", "authEnabled", "moonraker", "fluiddUrl", "demoMode", "integrationSources", "user"]) delete cfg[k];
    cfg.users = { ...cfg.users, multiUser: true };
    expect((await api.put("api/settings", { data: { config: cfg } })).ok()).toBe(true);
    const user = await api.post("api/users", { data: { username: "polish_staff", password: "polish-staff-secret", permissions: ["invoices"] } });
    expect(user.ok(), await user.text()).toBe(true);
    const inv = await api.post("api/invoices", { data: { clientName: "Staff Client", lines: [{ description: "Widget", quantity: 1, unitPriceCents: 700 }] } });
    expect(inv.status(), await inv.text()).toBe(201);
    const number = ((await inv.json()) as { number: string }).number;

    const staff = await browser.newContext();
    const page = await staff.newPage();
    const itemCalls: string[] = [];
    page.on("response", (r) => {
      if (/\/api\/items(\?|$)/.test(r.url())) itemCalls.push(`${r.status()} ${r.url()}`);
    });
    await page.goto(ADMIN_URL);
    await page.getByLabel("Username").fill("polish_staff");
    await page.getByLabel("Password").fill("polish-staff-secret");
    await page.getByRole("button", { name: "Sign in" }).click();
    await navButton(page, "Invoices").click();
    await page.getByRole("button", { name: number, exact: true }).click();
    await expect(page.getByRole("heading", { name: `Invoice ${number}` })).toBeVisible();
    expect(itemCalls).toEqual([]);
    await staff.close();
    await admin.close();
  });
});
