import { test, expect } from "@playwright/test";
import type { APIRequestContext, Page } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { navButton, seedQuoteBody, stubWindowPrint } from "./helpers";

// v1.1.0 money-requests lane: invoices (totals rows, money validation, conflicts, draft payments, overpayment),
// Requests (invoiced state, filter, reply, bulk finalize, More menu), the Dashboard's Needs attention list, and
// the Quotes list (client filter, chips, stacked cards, menu keys). Shares the suite's one server, so every
// record it creates is deleted again (requests.spec.ts expects an empty Requests list later in the run).

const TAGS = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"];

const created = { invoices: [] as string[], requests: [] as string[], quotes: [] as string[] };
let xffCounter = 0;

/** Each public submission uses its own forwarded address, so the per-IP submission budget the other specs use
 * up doesn't apply. The main port (baseURL) serves the shop too and, with ADMIN_TRUST_PROXY=1, honors the
 * header; the separate public port doesn't. */
function nextXff(): string {
  xffCounter += 1;
  return `203.0.113.${200 + xffCounter}`; // a11y.spec.ts and front-door.spec.ts use other addresses in this block
}

async function seedInvoice(request: APIRequestContext, body: Record<string, unknown>): Promise<{ id: string; number: string }> {
  const res = await request.post("/admin/api/invoices", { data: body });
  expect(res.status(), await res.text()).toBe(201);
  const inv = (await res.json()) as { id: string; number: string };
  created.invoices.push(inv.id);
  return inv;
}

/** Submits a public estimate request (a pasted g-code with weight and time), returns its id. */
async function seedRequest(request: APIRequestContext, name: string): Promise<string> {
  const res = await request.post("/api/public/quote-request", {
    headers: { "X-Forwarded-For": nextXff() },
    multipart: {
      name,
      email: `${name.toLowerCase().replace(/[^a-z]+/g, ".")}@example.com`,
      note: "",
      material: "PLA",
      quantity: "1",
      weightG: "12",
      timeSeconds: "3600",
    },
  });
  expect(res.status(), await res.text()).toBe(201);
  const id = ((await res.json()) as { id: string }).id;
  created.requests.push(id);
  return id;
}

// A public estimate needs a public material, so PLA is opted in with a full-document PUT /api/settings (the way
// public-quote-flow.spec.ts does it) and reverted afterward, so later specs see the original config.
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
  // Invoices that were sent can't be deleted; cancel them first (a no-op error for drafts is ignored).
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

async function openInvoice(page: Page, number: string): Promise<void> {
  await page.goto("/admin/");
  await navButton(page, "Invoices").click();
  await page.getByRole("button", { name: number, exact: true }).click();
  await expect(page.getByRole("heading", { name: `Invoice ${number}` })).toBeVisible();
}

const invoiceBody = (client: string, extra: Record<string, unknown> = {}) => ({
  clientName: client,
  lines: [{ description: "Widget", quantity: 1, unitPriceCents: 1009 }],
  ...extra,
});

test.describe("invoices", () => {
  test("totals show Shipping and Discount rows that add up; money input is validated; Ctrl+S saves", async ({ page, request }) => {
    const inv = await seedInvoice(request, {
      clientName: "Totals Customer",
      taxBp: 825,
      discountCents: 100,
      lines: [
        { description: "Widget", quantity: 1, unitPriceCents: 1009 },
        { description: "Shipping", kind: "shipping", quantity: 1, unitPriceCents: 400 },
      ],
    });
    await stubWindowPrint(page);
    await openInvoice(page, inv.number);

    const totals = page.locator(".totals").first();
    await expect(totals.locator("div", { hasText: /^Subtotal/ })).toContainText("$10.09");
    await expect(totals.locator("div", { hasText: /^Shipping/ })).toContainText("$4.00");
    await expect(totals.locator("div", { hasText: /^Discount/ })).toContainText("-$1.00");
    await expect(totals.locator("div", { hasText: /^Tax/ })).toContainText("$0.75");
    await expect(totals.locator("div", { hasText: /^Total/ })).toContainText("$13.84");

    // The printed invoice lists the same rows.
    await page.getByRole("button", { name: "Print", exact: true }).click();
    const printed = page.locator("#print-view .totals");
    await expect(printed).toContainText("Shipping");
    await expect(printed).toContainText("Invoice discount");
    await expect(printed).toContainText("-$1.00");

    // A sub-cent price and a negative quantity are refused before anything is sent.
    await page.getByLabel("Line 1 unit price").fill("0.333");
    await page.getByRole("button", { name: "Save", exact: true }).click();
    await expect(page.getByText("Line 1 unit price: use at most two decimal places.")).toBeVisible();
    await page.getByLabel("Line 1 unit price").fill("10.09");
    await page.getByLabel("Line 1 quantity").fill("-1");
    await page.getByRole("button", { name: "Save", exact: true }).click();
    await expect(page.getByText("Line 1 quantity: must be greater than zero.")).toBeVisible();
    await page.getByLabel("Line 1 quantity").fill("1");

    // Ctrl+S from inside a field saves, and the edit survives a reload.
    await page.getByLabel("Notes").fill("saved with the keyboard");
    await page.getByLabel("Notes").press("Control+s");
    await expect(page.getByText(`Invoice ${inv.number} saved.`)).toBeAttached();
    await page.reload();
    await expect(page.getByLabel("Notes")).toHaveValue("saved with the keyboard");
  });

  test("a draft takes a payment: the button states it, Enter submits, and an overpayment asks first", async ({ page, request }) => {
    const inv = await seedInvoice(request, { clientName: "Counter Customer", lines: [{ description: "Gift", quantity: 1, unitPriceCents: 1484 }] });
    await openInvoice(page, inv.number);

    await expect(page.getByRole("heading", { name: "Payments" })).toBeVisible();
    await expect(page.getByText(/Recording a payment marks it sent/)).toBeVisible();
    await expect(page.getByRole("button", { name: /^Record \$14\.84 .*today$/ })).toBeVisible();

    // Enter in the amount field records it; the draft becomes sent with a balance.
    await page.locator(".payments-card").getByLabel(/^Amount/).fill("5.01");
    await expect(page.getByRole("button", { name: /^Record \$5\.01 .*today$/ })).toBeVisible();
    await page.locator(".payments-card").getByLabel(/^Amount/).press("Enter");
    await expect(page.getByText(/Paid \$5\.01 of \$14\.84, balance \$9\.83/)).toBeVisible();
    await expect(page.getByText("sent", { exact: true })).toBeVisible();

    // $20.00 is $10.17 over the $9.83 balance.
    await page.locator(".payments-card").getByLabel(/^Amount/).fill("20.00");
    await page.getByRole("button", { name: /^Record \$20\.00 / }).click();
    await expect(page.getByText("This is $10.17 more than the balance. Record anyway?")).toBeVisible();
    await page.getByRole("button", { name: "Cancel", exact: true }).click();
    await expect(page.getByText(/Paid \$5\.01 of/)).toBeVisible();

    await page.getByRole("button", { name: /^Record \$20\.00 / }).click();
    await page.getByRole("button", { name: "Yes, record $20.00" }).click();
    await expect(page.getByText("Overpaid $10.17")).toBeVisible();
    await expect(page.getByText("paid", { exact: true })).toBeVisible();
  });

  test("a stale save from a second tab shows Reload and Overwrite", async ({ page, request, context }) => {
    const inv = await seedInvoice(request, invoiceBody("Two Tabs Customer"));
    await openInvoice(page, inv.number);
    const other = await context.newPage();
    await openInvoice(other, inv.number);

    await page.getByLabel("Notes").fill("first tab");
    await page.getByRole("button", { name: "Save", exact: true }).click();
    await expect(page.getByText(`Invoice ${inv.number} saved.`)).toBeAttached();

    await other.getByLabel("Notes").fill("second tab");
    await other.getByRole("button", { name: "Save", exact: true }).click();
    await expect(other.locator(".conflict")).toContainText("This was changed in another tab or by another user.");
    await other.getByRole("button", { name: "Reload" }).click();
    await expect(other.getByLabel("Notes")).toHaveValue("first tab");

    await other.getByLabel("Notes").fill("second tab again");
    await page.getByLabel("Notes").fill("third save from the first tab");
    await page.getByRole("button", { name: "Save", exact: true }).click(); // bumps the version past `other`'s
    await expect(page.getByText(`Invoice ${inv.number} saved.`)).toBeAttached();
    await other.getByRole("button", { name: "Save", exact: true }).click();
    await other.getByRole("button", { name: "Overwrite" }).click();
    await expect(other.getByText(`Invoice ${inv.number} saved.`)).toBeAttached();
    await other.reload();
    await expect(other.getByLabel("Notes")).toHaveValue("second tab again");
    await other.close();
  });

  test("a reload with unsaved edits is guarded", async ({ page, request }) => {
    const inv = await seedInvoice(request, invoiceBody("Guard Customer"));
    await openInvoice(page, inv.number);
    await page.getByLabel("Notes").fill("unsaved");
    // Closing with runBeforeUnload raises the same dialog a reload or a closed tab would.
    const dialog = page.waitForEvent("dialog");
    await page.close({ runBeforeUnload: true });
    const d = await dialog;
    expect(d.type()).toBe("beforeunload");
    await d.dismiss();
    await expect(page.getByLabel("Notes")).toHaveValue("unsaved"); // still open
  });

  test("the invoices list stacks into cards at 390 px with the amount and status visible", async ({ page, request }) => {
    const inv = await seedInvoice(request, invoiceBody("Phone Customer With A Rather Long Name Indeed", { dueDate: "2030-01-01" }));
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto("/admin/#/invoices");
    const row = page.locator("tr.clickable-row", { hasText: inv.number });
    await expect(row).toBeVisible();
    const box = await row.boundingBox();
    expect(box).not.toBeNull();
    for (const cell of [row.locator(".c-amount"), row.locator(".c-status")]) {
      const b = await cell.boundingBox();
      expect(b!.x).toBeGreaterThanOrEqual(0);
      expect(b!.x + b!.width).toBeLessThanOrEqual(390);
    }
    await expect(row.locator(".c-amount")).toContainText("$10.09");
  });
});

test.describe("requests and the dashboard", () => {
  test("converting a request marks it Invoiced with links both ways; reply, filter, and Ctrl+K work", async ({ page, request }) => {
    const name = `Convert Buyer ${Date.now() % 100000}`;
    const id = await seedRequest(request, name);
    await page.goto("/admin/");
    await navButton(page, "Requests").click();

    // Ctrl+K focuses the filter; the filter narrows by name. The filter renders once the list has loaded.
    await expect(page.getByLabel("Filter requests by name, item, or file")).toBeVisible();
    await page.keyboard.press("Control+k");
    await expect(page.getByLabel("Filter requests by name, item, or file")).toBeFocused();
    await page.keyboard.type(name);
    const row = page.locator("tr.req-row", { hasText: name });
    await expect(row).toHaveCount(1);
    await page.getByLabel("Filter requests by name, item, or file").fill("no such requester");
    await expect(page.getByText("No requests match the filter.")).toBeVisible();
    await page.getByLabel("Filter requests by name, item, or file").fill(name);

    await row.getByRole("button", { name: new RegExp(name) }).click();
    await expect(page.getByRole("heading", { name: `Estimate request from ${name}` })).toBeVisible();

    // The help text keeps its sentences apart and says Finalize doesn't email the customer.
    await expect(page.getByText(/Top items\. Convert to invoice creates the client/)).toBeVisible();
    await expect(page.getByText(/doesn't email the customer/)).toBeVisible();
    const reply = page.getByRole("link", { name: /^Reply by email/ });
    const href = decodeURIComponent((await reply.getAttribute("href")) ?? "");
    // The public form's short reference (not the hex id prefix) rides in the subject.
    const stored = (await (await request.get(`/admin/api/requests/${id}`)).json()) as { reference: string };
    expect(stored.reference).toMatch(/^[A-Z0-9]{1,10}-\d{4,}$/);
    expect(href).toContain(`Re: your request ${stored.reference}`);
    await expect(page.getByText(stored.reference, { exact: true }).first()).toBeVisible();

    await page.getByRole("button", { name: "Convert to invoice" }).click();
    await expect(page.getByRole("heading", { name: /^Invoice INV-/ })).toBeVisible();
    const number = ((await page.getByRole("heading", { name: /^Invoice INV-/ }).textContent()) ?? "").replace("Invoice ", "").trim();
    // Dates default on conversion.
    await expect(page.getByText("Issue date").locator("..").getByText("-", { exact: true })).toHaveCount(0);
    const created2 = await request.get("/admin/api/invoices");
    const invoices = ((await created2.json()) as { invoices: { id: string; number: string }[] }).invoices;
    const invoiceId = invoices.find((i) => i.number === number)!.id;
    created.invoices.push(invoiceId);

    // Back in Requests the row and the detail say Invoiced, and Convert is gone.
    await navButton(page, "Requests").click();
    const again = page.locator("tr.req-row", { hasText: name });
    await expect(again).toContainText("invoiced");
    await expect(again).toContainText(number);
    await again.getByRole("button", { name: new RegExp(name) }).click();
    await expect(page.getByText(`Invoiced as ${number} (request`)).toBeVisible();
    await expect(page.getByRole("button", { name: "Convert to invoice" })).toHaveCount(0);
    await page.getByRole("button", { name: "Open invoice" }).click();
    await expect(page.getByRole("heading", { name: `Invoice ${number}` })).toBeVisible();
  });

  test("a public estimate without a print time is flagged, takes a print time, is priced, and converts", async ({ page, request }) => {
    // The same multipart the shop posts for an unsliced STL: a weight and no timeSeconds.
    const name = `NoTime Buyer ${Date.now() % 100000}`;
    const res = await request.post("/api/public/quote-request", {
      headers: { "X-Forwarded-For": nextXff() },
      multipart: { name, email: `${name.toLowerCase().replace(/[^a-z]+/g, ".")}@example.com`, note: "", material: "PLA", quantity: "1", weightG: "12" },
    });
    expect(res.status(), await res.text()).toBe(201);
    const { id } = (await res.json()) as { id: string };
    created.requests.push(id);
    const before = (await (await request.get(`/admin/api/requests/${id}`)).json()) as { needsPrintTime: boolean; priceCents: number; reference: string };
    expect(before.needsPrintTime).toBe(true);
    expect(before.priceCents).toBe(0);

    await page.goto("/admin/");
    await navButton(page, "Requests").click();
    const row = page.locator("tr.req-row", { hasText: name });
    await expect(row).toContainText("Needs print time");
    await expect(row).toContainText(before.reference);
    await row.getByRole("button", { name: new RegExp(name) }).click();
    await expect(page.getByRole("heading", { name: `Estimate request from ${name}` })).toBeVisible();

    // Convert is blocked until the time is set.
    await expect(page.getByRole("button", { name: "Convert to invoice" })).toHaveAttribute("aria-disabled", "true");
    await expect(page.getByRole("button", { name: "Convert to invoice" })).toHaveAccessibleDescription(/there's no price yet/);

    await page.getByLabel("Hours").fill("1");
    await page.getByLabel("Minutes").fill("30");
    await page.getByRole("button", { name: "Save print time" }).click();
    await expect(page.getByRole("heading", { name: "This estimate needs a print time" })).toHaveCount(0);
    await expect(page.getByText("Not provided")).toHaveCount(0);
    const after = (await (await request.get(`/admin/api/requests/${id}`)).json()) as { needsPrintTime: boolean; priceCents: number; timeSeconds: number };
    expect(after.needsPrintTime).toBe(false);
    expect(after.timeSeconds).toBe(5400);
    expect(after.priceCents).toBeGreaterThan(0);

    await page.getByRole("button", { name: "Convert to invoice" }).click();
    await expect(page.getByRole("heading", { name: /^Invoice INV-/ })).toBeVisible();
    const number = ((await page.getByRole("heading", { name: /^Invoice INV-/ }).textContent()) ?? "").replace("Invoice ", "").trim();
    const list = (await (await request.get("/admin/api/invoices")).json()) as { invoices: { id: string; number: string }[] };
    created.invoices.push(list.invoices.find((i) => i.number === number)!.id);
  });

  test("Finalize selected finalizes the checked viewed requests", async ({ page, request }) => {
    const nameA = `Bulk A ${Date.now() % 100000}`;
    const nameB = `Bulk B ${Date.now() % 100000}`;
    await seedRequest(request, nameA);
    await seedRequest(request, nameB);
    await page.goto("/admin/");
    await navButton(page, "Requests").click();
    // Opening a request marks it viewed; only viewed rows have a checkbox.
    for (const n of [nameA, nameB]) {
      await page.locator("tr.req-row", { hasText: n }).getByRole("button", { name: new RegExp(n) }).click();
      await expect(page.getByRole("heading", { name: `Estimate request from ${n}` })).toBeVisible();
      await page.getByRole("button", { name: /Back to list/ }).click();
    }
    await page.getByLabel(`Select request from ${nameA}`).check();
    await page.getByLabel(`Select request from ${nameB}`).check();
    await expect(page.getByText("2 selected")).toBeVisible();
    await page.getByRole("button", { name: "Finalize selected" }).click();
    await page.getByRole("button", { name: "Yes, finalize 2 requests" }).click();
    await expect(page.getByText("Finalized 2 requests.")).toBeVisible();
    await expect(page.locator("tr.req-row", { hasText: nameA })).toContainText("finalized");
    await expect(page.locator("tr.req-row", { hasText: nameB })).toContainText("finalized");
  });

  test("Delete all new lives in the More menu and confirms the count and permanence", async ({ page, request }) => {
    const name = `Flood ${Date.now() % 100000}`;
    await seedRequest(request, name);
    await page.goto("/admin/");
    await navButton(page, "Requests").click();

    // Delete all new is not a Tab stop of its own beside the heading.
    await expect(page.getByRole("button", { name: /^Delete all new/ })).toHaveCount(0);
    const more = page.getByRole("button", { name: "More actions for requests" });
    await more.click();
    await page.keyboard.press("ArrowDown");
    await expect(page.getByRole("button", { name: "Delete all new…" })).toBeFocused();
    await page.keyboard.press("Home");
    await page.keyboard.press("End");
    await page.keyboard.press("Enter");

    const group = page.getByRole("group", { name: "Confirm deleting all new requests" });
    await expect(group).toContainText(/Delete \d+ new requests?\?/);
    await expect(group).toContainText("deleted permanently");
    await expect(group.getByRole("button", { name: "Cancel" })).toBeFocused(); // a double Enter can't delete
    await expect(group.getByRole("button", { name: /^Yes, delete \d+ requests?$/ })).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(group).toHaveCount(0);
    await expect(page.getByRole("button", { name: "More actions for requests" })).toBeFocused();
    await expect(page.locator("tr.req-row", { hasText: name })).toHaveCount(1); // nothing was deleted
  });

  test("request detail: facts are a list, header Back keeps the filter and row focus, a confirm closes on Escape", async ({ page, request }) => {
    const name = `Detail Buyer ${Date.now() % 100000}`;
    await seedRequest(request, name);
    await page.goto("/admin/");
    await navButton(page, "Requests").click();
    const filter = page.getByLabel("Filter requests by name, item, or file");
    await filter.fill(name);
    const open = page.getByRole("button", { name: new RegExp(name) });
    await open.click();

    const heading = page.getByRole("heading", { level: 1, name: `Estimate request from ${name}` });
    await expect(heading).toBeFocused();
    await expect(page).toHaveTitle(new RegExp(name));
    // The facts are one description list; the email is a link and the price is the emphasized value.
    const facts = page.getByRole("list", { name: "Request details" }).or(page.locator("dl[aria-label='Request details']"));
    await expect(facts.getByText("Material", { exact: true })).toBeVisible();
    await expect(facts.getByRole("link", { name: /@example\.com$/ })).toBeVisible();
    // The table fills its card, labels share one column, and the values start at one x beside them.
    const card = facts.locator("xpath=..");
    const cardBox = (await card.boundingBox())!;
    const factsBox = (await facts.boundingBox())!;
    expect(factsBox.width / cardBox.width, "facts span the full card width").toBeGreaterThan(0.98);
    const valueXs = await facts.locator("dd").evaluateAll((els) => els.map((e) => Math.round(e.getBoundingClientRect().left)));
    expect(new Set(valueXs).size, "values share one column").toBe(1);
    const label = (await facts.locator("dt").first().boundingBox())!;
    const value = (await facts.locator("dd").first().boundingBox())!;
    expect(value.x, "the value sits beside its label").toBeGreaterThan(label.x + label.width * 0.5);
    // Reading order: Back, then the single h1, then the facts.
    const order = await page.evaluate(() => {
      const back = [...document.querySelectorAll("button")].find((b) => /Back to list/.test(b.textContent ?? ""));
      const h1 = document.querySelector("h1");
      const dl = document.querySelector("dl[aria-label='Request details']");
      const before = (a: Node | null | undefined, b: Node | null | undefined) => !!a && !!b && !!(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING);
      return { backBeforeTitle: before(back, h1), titleBeforeFacts: before(h1, dl), h1Count: document.querySelectorAll("h1").length };
    });
    expect(order).toEqual({ backBeforeTitle: true, titleBeforeFacts: true, h1Count: 1 });

    // Escape closes only a confirmation, and focus returns to the control that opened it.
    await page.getByRole("button", { name: "Delete this request" }).click();
    await expect(page.getByRole("button", { name: "Yes, delete this request" })).toBeFocused();
    await expect(page.getByText("This can't be undone.")).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(page.getByRole("button", { name: "Yes, delete this request" })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Delete this request" })).toBeFocused();
    await expect(heading).toBeVisible();

    // The header Back returns to the same filtered list with focus on the row that opened the record.
    await page.getByRole("button", { name: /Back to list/ }).click();
    await expect(filter).toHaveValue(name);
    await expect(page.getByRole("button", { name: new RegExp(name) })).toBeFocused();
  });

  test("the Dashboard lists what needs attention and links to it", async ({ page, request }) => {
    const name = `Attention ${Date.now() % 100000}`;
    await seedRequest(request, name);
    const draft = await seedInvoice(request, invoiceBody("Draft Attention Client"));
    const overdue = await seedInvoice(request, invoiceBody("Overdue Attention Client", { issueDate: "2020-01-01", dueDate: "2020-01-31" }));
    const sent = await request.post(`/admin/api/invoices/${overdue.id}/status`, { data: { status: "sent" } });
    expect(sent.ok()).toBeTruthy();

    await page.goto("/admin/");
    await navButton(page, "Dashboard").click();
    const list = page.getByRole("region", { name: "Needs attention" });
    await expect(list).toContainText(name);
    await expect(list).toContainText("New request");
    await expect(list).toContainText("Draft Attention Client");
    await expect(list).toContainText(draft.number);
    await expect(list).toContainText("Overdue invoice");
    await expect(list).toContainText("Overdue Attention Client");

    // Recent invoice amounts stay inside their card.
    const card = page.locator(".card", { has: page.getByRole("heading", { name: "Recent invoices" }) });
    const cardBox = (await card.boundingBox())!;
    for (const total of await card.locator(".inv-total").all()) {
      const b = (await total.boundingBox())!;
      expect(b.x + b.width).toBeLessThanOrEqual(cardBox.x + cardBox.width + 0.5);
    }

    await list.getByRole("link", { name: new RegExp(draft.number) }).click();
    await expect(page.getByRole("heading", { name: `Invoice ${draft.number}` })).toBeVisible();
    await navButton(page, "Dashboard").click();
    await list.getByRole("link", { name: new RegExp(name) }).click();
    await expect(page.getByRole("heading", { name: `Estimate request from ${name}` })).toBeVisible();
  });

  test("the request views pass axe in both schemes", async ({ page, request }) => {
    const name = `Axe Buyer ${Date.now() % 100000}`;
    await seedRequest(request, name);
    await page.goto("/admin/");
    await navButton(page, "Requests").click();
    for (const scheme of ["light", "dark"] as const) {
      await page.emulateMedia({ colorScheme: scheme });
      await page.getByRole("button", { name: "More actions for requests" }).click();
      await page.waitForLoadState("networkidle");
      const results = await new AxeBuilder({ page }).withTags(TAGS).analyze();
      expect(results.violations.map((v) => `${v.id}: ${v.nodes[0]?.target.join(" ")}`), `requests ${scheme}`).toEqual([]);
      await page.keyboard.press("Escape");
    }
    await page.emulateMedia({ colorScheme: "light" });
  });
});

test.describe("landing", () => {
  // A fresh browser: no remembered tab, so the default landing rule applies.
  test.use({ storageState: { cookies: [], origins: [] } });

  test("the default landing is the Dashboard once the shop has data", async ({ page, request }) => {
    await seedInvoice(request, invoiceBody("Landing Client"));
    await page.goto("/admin/");
    await expect(page.getByRole("heading", { name: "Dashboard", level: 1 })).toBeVisible();
    await expect(page).toHaveURL(/#\/dashboard$/);
  });
});

test.describe("quotes list", () => {
  const stamp = `${Date.now() % 100000}`;
  const labels = Array.from({ length: 6 }, (_, i) => `QL ${stamp} Quote ${i + 1}`);
  const clientFor = (i: number) => (i === 0 ? `Casey Filter ${stamp}` : `Other Client ${i}`);

  test.beforeAll(async ({ request }) => {
    for (const [i, label] of labels.entries()) {
      const body = { ...seedQuoteBody(label), clientName: clientFor(i) };
      const res = await request.post("/admin/api/quotes", { data: body });
      expect(res.status(), await res.text()).toBe(201);
      created.quotes.push(((await res.json()) as { id: string }).id);
    }
  });

  test("the filter matches the client name, and status chips narrow the list", async ({ page }) => {
    await page.goto("/admin/");
    await navButton(page, "Quotes").click();
    const filter = page.getByLabel("Filter by label or client");
    await filter.fill(`Casey Filter ${stamp}`);
    await expect(page.locator("tbody tr.quote-row")).toHaveCount(1);
    await expect(page.locator("tbody tr.quote-row")).toContainText(labels[0]!);
    await filter.fill("");

    const chips = page.getByRole("group", { name: "Filter by status" });
    await chips.getByRole("button", { name: "Accepted" }).click();
    await expect(page.locator("tbody tr.quote-row", { hasText: labels[1]! })).toHaveCount(0);
    await chips.getByRole("button", { name: "Draft" }).click();
    await expect(page.locator("tbody tr.quote-row", { hasText: labels[1]! })).toHaveCount(1);
    await chips.getByRole("button", { name: "All" }).click();
  });

  test("row menus take arrow keys and Tab keeps focus on the page", async ({ page }) => {
    await page.goto("/admin/");
    await navButton(page, "Quotes").click();
    const toggle = page.getByRole("button", { name: `More actions for "${labels[1]}"` });
    await toggle.click();
    const items = page.locator(".menu-panel .menu-item:not([disabled])");
    await expect(items.first()).toBeFocused();
    await page.keyboard.press("ArrowDown");
    await expect(items.nth(1)).toBeFocused();
    await page.keyboard.press("End");
    await expect(items.last()).toBeFocused();
    await page.keyboard.press("ArrowDown");
    await expect(items.last()).toBeFocused(); // stops at the end: wrapping onto the first action is a mis-press waiting to happen
    await page.keyboard.press("Home");
    await expect(items.first()).toBeFocused();
    await page.keyboard.press("ArrowUp");
    await expect(items.first()).toBeFocused(); // and at the start

    await page.keyboard.press("Tab");
    await expect(page.locator(".menu-panel")).toHaveCount(0);
    expect(await page.evaluate(() => document.activeElement === document.body)).toBe(false);
  });

  test("rows stack as cards at 390 px with the price and status in view", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto("/admin/#/quotes");
    const row = page.locator("tbody tr.quote-row", { hasText: labels[2]! });
    await expect(row).toBeVisible();
    for (const sel of [".c-price", ".c-status", ".c-actions"]) {
      const b = (await row.locator(sel).boundingBox())!;
      expect(b.x, sel).toBeGreaterThanOrEqual(0);
      expect(b.x + b.width, sel).toBeLessThanOrEqual(390);
    }
    const scroll = await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth);
    expect(scroll).toBe(true);
  });
});

test("Ctrl+S saves the client form", async ({ page, request }) => {
  const name = `CtrlS Client ${Date.now() % 100000}`;
  const res = await request.post("/admin/api/clients", {
    data: { name, email: "", phone: "", billingAddress: "", shippingAddress: "", notes: "" },
  });
  expect(res.status()).toBe(201);
  const id = ((await res.json()) as { id: string }).id;
  try {
    await page.goto("/admin/");
    await navButton(page, "Clients").click();
    await page.getByRole("button", { name }).click();
    await page.getByLabel("Notes").fill("saved with Ctrl+S");
    await page.getByLabel("Notes").press("Control+s");
    await expect(page.getByText(`${name} saved.`)).toBeAttached();
    const check = await request.get("/admin/api/clients");
    const saved = ((await check.json()) as { clients: { id: string; notes: string }[] }).clients.find((c) => c.id === id);
    expect(saved?.notes).toBe("saved with Ctrl+S");
  } finally {
    await request.delete(`/admin/api/clients/${id}`);
  }
});
