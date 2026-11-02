import { test, expect, type APIRequestContext, type Page } from "@playwright/test";
import { fillCanonicalQuote, historyRow, seedQuoteBody } from "./helpers";

// Shares the server-backed database with every spec and runs after history.spec.ts (filename order,
// one worker). Tests that depend on the saved-quote count seed what they need and delete it in a
// `finally`; one quote lingers from history.spec.ts, well under every threshold checked here.

/** Arms a one-shot window keydown listener that records event.defaultPrevented. Added after
 * navigation, so it observes the decision the app's own handler already made. */
async function armDefaultPreventedProbe(page: Page): Promise<() => Promise<boolean | undefined>> {
  await page.evaluate(() => {
    window.addEventListener(
      "keydown",
      (e) => {
        (window as unknown as { __kbProbe?: boolean }).__kbProbe = e.defaultPrevented;
      },
      { once: true },
    );
  });
  return () => page.evaluate(() => (window as unknown as { __kbProbe?: boolean }).__kbProbe);
}

/** Seeds `count` minimal one-part draft quotes via the API and returns their ids for deleteQuotes. The server
 * assigns savedAt (the input rejects unknown fields), so creation order is oldest-to-newest. */
async function seedQuotes(request: APIRequestContext, baseURL: string | undefined, count: number): Promise<string[]> {
  const pad = (n: number) => String(n).padStart(3, "0");
  const ids: string[] = [];
  for (let i = 0; i < count; i++) {
    const res = await request.post(`${baseURL}/admin/api/quotes`, {
      data: seedQuoteBody(`kb-seed-${pad(i + 1)}`),
    });
    expect(res.ok()).toBeTruthy();
    const body = (await res.json()) as { id: string };
    ids.push(body.id);
  }
  return ids;
}

async function deleteQuotes(request: APIRequestContext, baseURL: string | undefined, ids: string[]): Promise<void> {
  for (const id of ids) {
    await request.delete(`${baseURL}/admin/api/quotes/${id}`);
  }
}

/** Deletes the saved quote with this label via the API, for tests that saved through the UI. */
async function deleteQuoteByLabel(request: APIRequestContext, baseURL: string | undefined, label: string): Promise<void> {
  const res = await request.get(`${baseURL}/admin/api/quotes`);
  const body = (await res.json()) as { quotes: { id: string; label: string }[] };
  const match = body.quotes.find((q) => q.label === label);
  if (match) await request.delete(`${baseURL}/admin/api/quotes/${match.id}`);
}

/** Creates `count` clients via the API and returns their ids. The table is empty on entry
 * (clients.spec.ts runs first), but each test still deletes what it creates. */
async function seedClients(request: APIRequestContext, baseURL: string | undefined, count: number): Promise<string[]> {
  const ids: string[] = [];
  for (let i = 0; i < count; i++) {
    const res = await request.post(`${baseURL}/admin/api/clients`, {
      data: {
        name: `kb-client-${String(i + 1).padStart(2, "0")}`,
        email: "",
        phone: "",
        billingAddress: "",
        shippingAddress: "",
        notes: "",
      },
    });
    expect(res.ok()).toBeTruthy();
    const body = (await res.json()) as { id: string };
    ids.push(body.id);
  }
  return ids;
}

async function deleteClients(request: APIRequestContext, baseURL: string | undefined, ids: string[]): Promise<void> {
  for (const id of ids) {
    await request.delete(`${baseURL}/admin/api/clients/${id}`);
  }
}

test('"/" focuses the Quotes ledger filter when it is present', async ({ page, request, baseURL }) => {
  // The filter input only renders with more than 5 saved quotes; seed 6 explicitly.
  const ids = await seedQuotes(request, baseURL, 6);
  try {
    await page.goto("/admin/#/quotes");
    await expect(page.getByRole("heading", { name: "Quotes" })).toBeVisible();
    const filterInput = page.locator("#quotes-filter");
    await expect(filterInput).toBeVisible();
    await expect(filterInput).not.toBeFocused();

    // Nothing is focused after a fresh load, so the keypress reaches the window handler.
    await page.keyboard.press("/");
    await expect(filterInput).toBeFocused();
  } finally {
    await deleteQuotes(request, baseURL, ids);
  }
});

test('"/" is a silent no-op when the filter is absent: no preventDefault, focus unchanged', async ({ page, request, baseURL }) => {
  // At or under the threshold, so #quotes-filter never renders.
  const ids = await seedQuotes(request, baseURL, 2);
  try {
    await page.goto("/admin/#/quotes");
    await expect(page.getByRole("heading", { name: "Quotes" })).toBeVisible();
    await expect(page.locator("#quotes-filter")).toHaveCount(0);

    // Probe defaultPrevented directly: the handler's early return must leave the browser's own "/"
    // handling untouched.
    const readProbe = await armDefaultPreventedProbe(page);
    await page.keyboard.press("/");
    expect(await readProbe()).toBe(false);

    // Focus never moved, so document.body is still active.
    await expect(page.locator("body")).toBeFocused();
  } finally {
    await deleteQuotes(request, baseURL, ids);
  }
});

test('"/" does not focus a filter from the Quote (editor) tab: the shortcut is ledger-only', async ({
  page,
  request,
  baseURL,
}) => {
  // The editor's recent-saves strip has nothing to filter; seed past the ledger's threshold.
  const ids = await seedQuotes(request, baseURL, 6);
  try {
    await page.goto("/admin/");
    await expect(page.getByRole("heading", { name: "New quote" })).toBeVisible();
    await expect(page.locator("#history-filter")).toHaveCount(0);

    const readProbe = await armDefaultPreventedProbe(page);
    await page.keyboard.press("/");
    expect(await readProbe()).toBe(false);
    await expect(page.locator("body")).toBeFocused();
  } finally {
    await deleteQuotes(request, baseURL, ids);
  }
});

test("Ctrl+S saves the current quote, even while focus is still in the label field", async ({ page, request, baseURL }) => {
  const label = `kb-ctrlsave-${Date.now()}`;

  await page.goto("/admin/");
  await fillCanonicalQuote(page);
  const labelInput = page.getByLabel("Label for this quote");
  // Type a label, then Ctrl+S without tabbing out of the field.
  await labelInput.fill(label);
  await labelInput.press("Control+S");

  // The save went through the REST endpoint and the row shows in the recent-saves strip.
  await expect(historyRow(page, label)).toBeVisible();
  await deleteQuoteByLabel(request, baseURL, label);
});

test("Ctrl+S is not intercepted outside the quote tab (e.g. Invoices)", async ({ page }) => {
  await page.goto("/admin/#/invoices");
  await expect(page.getByRole("heading", { name: "Invoices" })).toBeVisible();

  const readProbe = await armDefaultPreventedProbe(page);
  await page.keyboard.press("Control+S");
  expect(await readProbe()).toBe(false);
});

test('"/" typed in a text field is entered literally, not treated as a shortcut', async ({ page }) => {
  await page.goto("/admin/");
  const labelInput = page.getByLabel("Label for this quote");
  await labelInput.click();
  await labelInput.pressSequentially("a/b");

  // The character lands in the field and focus never moves to the filter input.
  await expect(labelInput).toHaveValue("a/b");
  await expect(labelInput).toBeFocused();
});

test('"/" focuses the Clients list filter when it is present', async ({ page, request, baseURL }) => {
  // ClientsTab only renders the filter input with more than 5 clients; seed 6 explicitly.
  const ids = await seedClients(request, baseURL, 6);
  try {
    await page.goto("/admin/#/clients");
    await expect(page.getByRole("heading", { name: "Clients" })).toBeVisible();
    const filterInput = page.locator("#clients-filter");
    await expect(filterInput).toBeVisible();
    await expect(filterInput).not.toBeFocused();

    // Nothing is focused after a fresh load.
    await page.keyboard.press("/");
    await expect(filterInput).toBeFocused();
  } finally {
    await deleteClients(request, baseURL, ids);
  }
});

test("Escape closes an open client detail view, returning to the list", async ({ page, request, baseURL }) => {
  const ids = await seedClients(request, baseURL, 1);
  try {
    // Deep link straight into the detail view, as a bookmark or back/forward hop would.
    await page.goto(`/admin/#/clients#${ids[0]}`);
    await expect(page.getByRole("heading", { name: "kb-client-01" })).toBeVisible();
    await expect(page).toHaveURL(/#\/clients#.+/);

    await page.keyboard.press("Escape");

    // Same destination as "Back to list": the bare tab hash with the list visible.
    await expect(page).toHaveURL(/#\/clients$/);
    await expect(page.getByRole("heading", { name: "Clients" })).toBeVisible();
  } finally {
    await deleteClients(request, baseURL, ids);
  }
});
