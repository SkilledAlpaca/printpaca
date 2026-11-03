import { test, expect, request as pwRequest } from "@playwright/test";
import type { Page } from "@playwright/test";
import { navButton, stubWindowPrint } from "./helpers";

// Turns the PayPal.me handle and currency on, checks the printed invoice, then turns both off. The
// original values are snapshotted in beforeAll and restored in afterAll, even when a test fails.

const HANDLE = "yourshop";

// The save button reads "Saving…" in flight, so wait for the whole bar button to go away: otherwise the
// next nav click hits the unsaved-changes confirm(), which Playwright auto-dismisses.
async function saveSettings(page: Page): Promise<void> {
  const saveBar = page.getByRole("button", { name: /^(Save changes|Saving…)$/ });
  await saveBar.click();
  await expect(saveBar).toHaveCount(0);
}

async function configField(page: Page): Promise<{ paypalMe: string; code: string }> {
  const res = await page.request.get("/admin/api/config");
  const body = (await res.json()) as { invoicing: { paypalMe: string }; currency: { code: string } };
  return { paypalMe: body.invoicing.paypalMe, code: body.currency.code };
}

// Sets or clears the PayPal.me handle and currency via the API. PUT /api/settings takes the full
// config, so GET /api/config's read-only wrapper fields are dropped.
async function setPaypalConfig(values: { paypalMe: string; code: string }): Promise<void> {
  const api = await pwRequest.newContext({ baseURL: "http://127.0.0.1:8099/admin/" });
  try {
    const cfg = (await (await api.get("api/config")).json()) as Record<string, any>;
    for (const k of ["integrations", "version", "authEnabled", "moonraker", "fluiddUrl", "demoMode", "integrationSources", "user"]) {
      delete cfg[k];
    }
    cfg.invoicing = { ...cfg.invoicing, paypalMe: values.paypalMe };
    cfg.currency = { ...cfg.currency, code: values.code };
    const put = await api.put("api/settings", { data: { config: cfg } });
    expect(put.ok(), await put.text()).toBe(true);
  } finally {
    await api.dispose();
  }
}

let original = { paypalMe: "", code: "" };

test.beforeAll(async () => {
  const api = await pwRequest.newContext({ baseURL: "http://127.0.0.1:8099/admin/" });
  try {
    const body = (await (await api.get("api/config")).json()) as { invoicing: { paypalMe: string }; currency: { code: string } };
    original = { paypalMe: body.invoicing.paypalMe, code: body.currency.code };
  } finally {
    await api.dispose();
  }
});

test.afterAll(async () => {
  await setPaypalConfig(original);
});

async function printInvoice(page: Page) {
  await page.getByRole("button", { name: "Print", exact: true }).click();
  await page.emulateMedia({ media: "print" });
  const view = page.locator("#print-view");
  await expect(view).toBeVisible();
  return view;
}

test("a PayPal.me handle and currency code put a pay link and QR on a sent invoice's print view", async ({ page }) => {
  await setPaypalConfig({ paypalMe: "", code: "" });
  await stubWindowPrint(page);
  await page.goto("/admin/");

  await navButton(page, "Settings").click();
  await page.getByLabel("Currency code").fill("USD");
  await saveSettings(page);

  await navButton(page, "Invoices").click();
  await page.getByLabel("PayPal.me handle").fill(HANDLE);
  await saveSettings(page);
  await expect.poll(async () => await configField(page)).toEqual({ paypalMe: HANDLE, code: "USD" });

  await page.reload();
  await navButton(page, "Invoices").click();
  await page.getByRole("button", { name: "New invoice" }).click();
  await page.getByLabel("Client name").fill("PayPal Test Customer");
  await page.getByRole("button", { name: "Add line" }).click();
  await page.getByLabel("Line 1 description").fill("Custom bracket");
  await page.getByLabel("Line 1 quantity").fill("1");
  await page.getByLabel("Line 1 unit price").fill("40.00");
  await page.getByRole("button", { name: "Save" }).click();
  await expect(page.getByRole("heading", { name: /^Invoice INV-/ })).toBeVisible();
  await page.getByRole("button", { name: "Mark sent" }).click();
  await expect(page.getByText("sent", { exact: true })).toBeVisible();

  const view = await printInvoice(page);
  await expect(view).toContainText("Pay with PayPal");
  await expect(view).toContainText(`https://paypal.me/${HANDLE}/40.00USD`);
  await expect(view.locator("svg.qr")).toBeVisible();
});

test("a fully paid invoice's print view has no pay block", async ({ page }) => {
  await setPaypalConfig({ paypalMe: HANDLE, code: "USD" });
  await stubWindowPrint(page);
  await page.goto("/admin/");
  await navButton(page, "Invoices").click();
  await page.getByRole("button", { name: "New invoice" }).click();
  await page.getByLabel("Client name").fill("PayPal Paid Customer");
  await page.getByRole("button", { name: "Add line" }).click();
  await page.getByLabel("Line 1 description").fill("Custom bracket");
  await page.getByLabel("Line 1 quantity").fill("1");
  await page.getByLabel("Line 1 unit price").fill("15.00");
  await page.getByRole("button", { name: "Save" }).click();
  await expect(page.getByRole("heading", { name: /^Invoice INV-/ })).toBeVisible();
  await page.getByRole("button", { name: "Mark sent" }).click();
  await expect(page.getByText("sent", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.getByText("paid", { exact: true })).toBeVisible();

  const view = await printInvoice(page);
  await expect(view).not.toContainText("Pay with PayPal");
  await expect(view).not.toContainText("paypal.me");
  await expect(view.locator("svg.qr")).toHaveCount(0);
});

test("an invalid handle is rejected with a path-scoped error", async ({ page }) => {
  // Start from a known-blank handle.
  await setPaypalConfig({ paypalMe: "", code: "" });
  await page.goto("/admin/");
  await navButton(page, "Invoices").click();
  await page.getByLabel("PayPal.me handle").fill("not/a/handle");
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByRole("alert").filter({ hasText: "invoicing.paypalMe" })).toBeVisible();
  expect((await configField(page)).paypalMe).toBe("");
});
