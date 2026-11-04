import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";
import { requestIdByReference } from "./helpers";

// v1.1 customer-shop usability fixes: an unsliced model without a print time, hash routes, self-clearing
// errors, quantity handling, and a submit that waits for the price. Runs against the main port (8099), where
// ADMIN_TRUST_PROXY lets each test order from its own X-Forwarded-For address.

const PREFIX = "UsabilityE2E";
const ITEM = `${PREFIX} Shop Item`;
let itemId = "";
let originalConfig: Record<string, unknown> | null = null;
const createdRefs: string[] = [];

/** A cube of `size` model units on a side, as ASCII STL. */
function cubeStl(size: number): string {
  const s = size;
  const tris = [
    [[0, 0, 0], [0, s, 0], [s, s, 0]],
    [[0, 0, 0], [s, s, 0], [s, 0, 0]],
    [[0, 0, s], [s, 0, s], [s, s, s]],
    [[0, 0, s], [s, s, s], [0, s, s]],
    [[0, 0, 0], [s, 0, 0], [s, 0, s]],
    [[0, 0, 0], [s, 0, s], [0, 0, s]],
    [[0, s, 0], [0, s, s], [s, s, s]],
    [[0, s, 0], [s, s, s], [s, s, 0]],
    [[0, 0, 0], [0, 0, s], [0, s, s]],
    [[0, 0, 0], [0, s, s], [0, s, 0]],
    [[s, 0, 0], [s, s, 0], [s, s, s]],
    [[s, 0, 0], [s, s, s], [s, 0, s]],
  ];
  const facets = tris
    .map((t) => `  facet normal 0 0 0\n    outer loop\n${t.map((v) => `      vertex ${v[0]} ${v[1]} ${v[2]}`).join("\n")}\n    endloop\n  endfacet`)
    .join("\n");
  return `solid cube\n${facets}\nendsolid cube\n`;
}

async function dropStl(page: Page, name: string, size: number): Promise<void> {
  await page.setInputFiles("#public-gcode-file", { name, mimeType: "model/stl", buffer: Buffer.from(cubeStl(size), "utf-8") });
}

test.describe.configure({ mode: "serial" });

test.beforeAll(async ({ request }) => {
  const getRes = await request.get("/admin/api/settings");
  expect(getRes.ok()).toBe(true);
  originalConfig = (await getRes.json()).config;
  const updated = structuredClone(originalConfig) as { materials: { name: string }[] };
  updated.materials = updated.materials.map((m) => (m.name === "PLA" ? { ...m, public: true } : m));
  expect((await request.put("/admin/api/settings", { data: { config: updated } })).ok()).toBe(true);

  const item = await request.post("/admin/api/items", {
    data: {
      name: ITEM,
      description: "A small printed planter.",
      priceCents: 700,
      public: true,
      stockMode: "in_stock",
      stockQty: 5,
      optionGroups: [{ label: "Color", required: true, options: [{ name: "Sage", deltaCents: 0 }] }],
      sortOrder: 0,
    },
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
  for (const ref of createdRefs) {
    const res = await request.get("/admin/api/requests");
    const body = (await res.json()) as { requests?: { id: string; reference: string }[] };
    const row = (body.requests ?? []).find((r) => r.reference === ref);
    if (row) await request.delete(`/admin/api/requests/${row.id}`);
  }
  await request.delete(`/admin/api/items/${itemId}`);
  if (originalConfig) await request.put("/admin/api/settings", { data: { config: originalConfig } });
});

test("an unsliced STL can be sent without a print time and the shop sees it needs one", async ({ page, request }) => {
  await page.setExtraHTTPHeaders({ "X-Forwarded-For": "203.0.113.61" });
  await page.goto("/order#/estimate");
  await page.getByLabel("Material").selectOption("PLA");
  await dropStl(page, "cube.stl", 20);

  // No print time: the panel promises a confirmed price instead of asking for something the visitor can't know.
  await expect(page.getByText("We'll confirm the price after we prepare your file for printing.")).toBeVisible();
  await expect(page.locator(".estimate .price")).toHaveCount(0);
  await expect(page.locator(".model-size")).toContainText("20 × 20 × 20 mm");

  await page.getByLabel("Name").fill(`${PREFIX} Visitor`);
  await page.getByLabel("Email").fill("usability@example.com");
  await page.getByRole("button", { name: "Request an estimate" }).click();

  await expect(page).toHaveURL(/#\/estimate\/received$/);
  await expect(page.getByRole("region", { name: "Request received" })).toBeFocused();
  await expect(page.locator(".confirmation .price")).toHaveText("Price to be confirmed");
  await expect(page.locator(".confirmation")).toContainText("File: cube.stl");
  const refText = (await page.locator(".confirmation .ref").textContent())?.trim() ?? "";
  const ref = refText.replace("Reference: ", "");
  // A short, per-shop reference, not the 32-character id.
  expect(ref).toMatch(/^[A-Z0-9]{1,10}-\d{4,}$/);
  createdRefs.push(ref);

  // The confirmation survives a reload.
  await page.reload();
  await expect(page.locator(".confirmation .ref")).toHaveText(`Reference: ${ref}`);

  const id = await requestIdByReference(request, ref);
  const stored = await (await request.get(`/admin/api/requests/${id}`)).json();
  expect(stored.needsPrintTime).toBe(true);
  expect(stored.reference).toBe(ref);
  expect(stored.timeSeconds).toBe(0);
  expect(stored.priceCents).toBe(0);
});

test("an entered print time still gives an estimated price, and the confirmation says so", async ({ page, request }) => {
  await page.setExtraHTTPHeaders({ "X-Forwarded-For": "203.0.113.62" });
  await page.goto("/order#/estimate");
  await page.getByLabel("Material").selectOption("PLA");
  await dropStl(page, "cube.stl", 20);
  await page.getByLabel(/^Print time \(h/).fill("1");
  await page.getByLabel(/Print time \(minutes\)/).fill("30");
  await expect(page.locator(".estimate .price")).toContainText("$");

  // Pressing submit while the preview is still updating waits for it instead of asking to try again.
  await page.getByLabel("Name").fill(`${PREFIX} Timed`);
  await page.getByLabel("Email").fill("usability@example.com");
  await page.getByLabel(/Print time \(minutes\)/).fill("45");
  await page.getByRole("button", { name: "Request an estimate" }).click();

  await expect(page.getByRole("heading", { name: "Request received" })).toBeVisible();
  await expect(page.getByText("The estimate is still updating")).toHaveCount(0);
  await expect(page.locator(".confirmation")).toContainText("Estimated price");
  await expect(page.locator(".confirmation")).toContainText("Print time used: 1 h 45 min");
  await expect(page.locator(".confirmation")).toContainText("File: cube.stl");
  const ref = ((await page.locator(".confirmation .ref").textContent()) ?? "").replace("Reference: ", "").trim();
  createdRefs.push(ref);
  const stored = await (await request.get(`/admin/api/requests/${await requestIdByReference(request, ref)}`)).json();
  expect(stored.needsPrintTime).toBe(false);
  expect(stored.priceCents).toBeGreaterThan(0);
});

test("swapping the model file keeps the typed print time and Solidity", async ({ page }) => {
  await page.setExtraHTTPHeaders({ "X-Forwarded-For": "203.0.113.63" });
  await page.goto("/order#/estimate");
  await page.getByLabel("Material").selectOption("PLA");
  await dropStl(page, "a.stl", 20);
  await page.getByText("How we estimated this").click();
  await expect(page.getByLabel("Solidity (%)")).toHaveValue("20");
  await page.getByLabel("Solidity (%)").fill("40");
  await page.getByLabel(/^Print time \(h/).fill("1");
  await page.getByLabel(/Print time \(minutes\)/).fill("30");
  await expect(page.locator(".estimate .price")).toContainText("$");

  await dropStl(page, "b.stl", 30);
  await expect(page.locator(".model-size")).toContainText("30 × 30 × 30 mm");
  await expect(page.getByLabel("Solidity (%)")).toHaveValue("40");
  await expect(page.getByLabel(/^Print time \(h/)).toHaveValue("1");
  await expect(page.getByLabel(/Print time \(minutes\)/)).toHaveValue("30");
  // The price comes back without retyping a digit.
  await expect(page.locator(".estimate .price")).toContainText("$");
});

test("a model that looks exported in inches is flagged, and the weight can be overridden", async ({ page }) => {
  await page.setExtraHTTPHeaders({ "X-Forwarded-For": "203.0.113.64" });
  await page.goto("/order#/estimate");
  await page.getByLabel("Material").selectOption("PLA");
  await dropStl(page, "inch.stl", 0.787);
  await expect(page.getByText(/exported it in inches/)).toBeVisible();

  await dropStl(page, "cube.stl", 20);
  const weight = page.getByLabel("Model weight (g)");
  await expect(weight).not.toHaveValue("");
  await weight.fill("12.5");
  await expect(page.getByText("Using your weight.")).toBeVisible();
  await page.getByLabel(/^Print time \(h/).fill("1");
  await expect(page.locator(".model-size")).toContainText("20 × 20 × 20 mm");
});

test("estimate errors name the blocker and clear as soon as it is fixed", async ({ page }) => {
  await page.setExtraHTTPHeaders({ "X-Forwarded-For": "203.0.113.65" });
  await page.goto("/order#/estimate");
  // Before anything: the panel names the first blocker.
  await expect(page.locator(".estimate")).toContainText("Choose a material.");

  await page.getByRole("button", { name: "Request an estimate" }).click();
  // The message sits under the field, once; the line above the button links to it.
  const alert = page.locator("#quote-material-err");
  await expect(alert).toHaveText("Choose a material.");
  await expect(page.getByText("Choose a material.")).toHaveCount(1);
  await expect(page.getByLabel("Material")).toBeFocused();
  await expect(page.getByLabel("Material")).toHaveAttribute("aria-invalid", "true");

  await page.getByLabel("Material").selectOption("PLA");
  await expect(alert).toHaveCount(0);
  await expect(page.getByLabel("Material")).not.toHaveAttribute("aria-invalid", "true");

  // A bad value is named, not hidden behind a generic prompt.
  await page.getByLabel("Model weight (g)").fill("200000");
  await expect(page.locator("#quote-weight-err")).toHaveText("Weight must be from 0 to 100,000 g.");
  await page.getByLabel("Model weight (g)").fill("20");
  await page.getByLabel(/^Print time \(h/).fill("1");
  await page.getByLabel(/Print time \(minutes\)/).fill("90");
  await page.getByRole("button", { name: "Request an estimate" }).click();
  await expect(page.getByLabel(/Print time \(minutes\)/)).toBeFocused();
  await expect(page.locator("#quote-time-err")).toHaveText("Minutes must be from 0 to 59.");
  await expect(page.getByText("Minutes must be from 0 to 59.")).toHaveCount(1);
});

test("items have their own URL: Back, reload and a shared link work", async ({ page }) => {
  await page.setExtraHTTPHeaders({ "X-Forwarded-For": "203.0.113.66" });
  await page.goto("/order");
  await page.getByRole("button", { name: new RegExp(ITEM) }).click();
  await expect(page).toHaveURL(/#\/item\/[0-9a-f]+$/);
  await expect(page.getByRole("heading", { name: ITEM })).toBeFocused();
  const itemUrl = page.url();

  // Stock and shipping are on the item page.
  await expect(page.getByText("5 in stock.")).toBeVisible();
  await expect(page.getByText("Shipping, if any, is confirmed by email.")).toBeVisible();

  await page.goBack();
  await expect(page).not.toHaveURL(/#\/item\//);
  await expect(page.getByRole("button", { name: new RegExp(ITEM) })).toBeFocused();

  // A shared link opens the item directly, and so does a reload.
  await page.goto("about:blank");
  await page.goto(itemUrl);
  await expect(page.getByRole("heading", { name: ITEM })).toBeVisible();
  await page.reload();
  await expect(page.getByRole("heading", { name: ITEM })).toBeVisible();

  await page.getByRole("button", { name: "Custom estimate" }).click();
  await expect(page).toHaveURL(/#\/estimate$/);
  await page.goBack();
  await expect(page.getByRole("heading", { name: ITEM })).toBeVisible();

  // An item that doesn't exist returns to the gallery with a note.
  await page.goto("/order#/item/ffffffffffffffff");
  await expect(page.getByText("That item is no longer available.")).toBeVisible();
  await expect(page).toHaveURL(/#\/$/);
  // A confirmation route with nothing behind it does the same.
  await page.goto("/order#/order/received");
  await expect(page.getByText("That order confirmation is no longer available.")).toBeVisible();
});

test("Escape goes back from an item and the page says so", async ({ page }) => {
  await page.setExtraHTTPHeaders({ "X-Forwarded-For": "203.0.113.67" });
  await page.goto("/order");
  await page.getByRole("button", { name: new RegExp(ITEM) }).click();
  await expect(page.getByText("Press Esc to go back.")).toBeAttached();
  // Back to gallery follows the heading in the tab order.
  await expect(page.getByRole("heading", { name: ITEM })).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(page.getByRole("button", { name: /Back to gallery/ })).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("button", { name: new RegExp(ITEM) })).toBeFocused();
});

test("quantity: no total for an invalid value, and the clamp names the limit", async ({ page }) => {
  await page.setExtraHTTPHeaders({ "X-Forwarded-For": "203.0.113.68" });
  await page.goto("/order");
  await page.getByRole("button", { name: new RegExp(ITEM) }).click();
  await page.getByLabel("Color").selectOption({ label: "Sage" });
  const total = page.getByRole("group", { name: "Total" }).locator(".price");
  await expect(total).toContainText("$7.00");

  await page.getByLabel("Quantity").fill("-3");
  await expect(total).toHaveCount(0);
  await expect(page.getByText(/Enter a whole number from 1 to 5 to see a total/)).toBeVisible();
  await page.getByLabel("Quantity").fill("2.5");
  await expect(total).toHaveCount(0);

  // Stock caps at 5 here, so the message says stock, not a per-order maximum. The value stays as typed and
  // no total shows while it is over the cap.
  await page.getByLabel("Quantity").fill("50");
  await expect(page.getByText("Only 5 in stock.")).toBeVisible();
  await expect(page.getByLabel("Quantity")).toHaveValue("50");
  await expect(total).toHaveCount(0);
  await page.getByLabel("Quantity").fill("5");
  await expect(total).toContainText("$35.00");
});

test("an order shows a short reference and what happens next, and another can be placed", async ({ page, request }) => {
  await page.setExtraHTTPHeaders({ "X-Forwarded-For": "203.0.113.69" });
  await page.goto("/order");
  await page.getByRole("button", { name: new RegExp(ITEM) }).click();
  await page.getByLabel("Color").selectOption({ label: "Sage" });
  await page.getByLabel("Name").fill(`${PREFIX} Buyer`);
  await page.getByLabel("Email").fill("buyer@example.com");
  await page.getByRole("button", { name: "Place order" }).click();

  await expect(page).toHaveURL(/#\/order\/received$/);
  await expect(page.getByRole("region", { name: "Order received" })).toBeFocused();
  const ref = ((await page.locator(".confirmation .ref").textContent()) ?? "").replace("Reference: ", "").trim();
  expect(ref).toMatch(/^[A-Z0-9]{1,10}-\d{4,}$/);
  createdRefs.push(ref);
  // The summary reads "1 × Item · Color: Sage" with the space before the bullet.
  await expect(page.locator(".confirmation .order-summary")).toHaveText(`1 × ${ITEM} · Color: Sage`);
  await expect(page.locator(".confirmation")).toContainText("You won't be charged until then.");
  expect(await requestIdByReference(request, ref)).toMatch(/^[0-9a-f]{32}$/);

  // Taps in the first half second after a confirmation appears are ignored (a double tap on Place order).
  await page.waitForTimeout(600);
  await page.getByRole("button", { name: "Back to gallery" }).click();
  await expect(page.getByRole("button", { name: new RegExp(ITEM) })).toBeVisible();
});

// Round 3 polish batch: public items 1-8.

async function setMaxOrderQty(request: import("@playwright/test").APIRequestContext, qty: number): Promise<void> {
  // Start from the live config: it already has PLA public, which originalConfig (the pre-test state) does not.
  const cfg = (await (await request.get("/admin/api/settings")).json()).config as { shop?: { maxOrderQty: number } };
  cfg.shop = { maxOrderQty: qty };
  const res = await request.put("/admin/api/settings", { data: { config: cfg } });
  expect(res.ok()).toBe(true);
}

test("the price heading says Price until a price can be computed, and a slicer file is explained", async ({ page }) => {
  await page.setExtraHTTPHeaders({ "X-Forwarded-For": "203.0.113.71" });
  await page.goto("/order#/estimate");
  await expect(page.locator(".intro")).not.toContainText(/instant/i);
  await expect(page.getByText(/the\s+program that prepares a model for printing/)).toBeVisible();
  await page.getByLabel("Material").selectOption("PLA");
  await dropStl(page, "cube.stl", 20);
  await expect(page.getByText("We'll confirm the price after we prepare your file for printing.")).toBeVisible();
  await expect(page.locator("#estimate-label")).toHaveText("Price");
  await page.getByLabel(/^Print time \(h/).fill("1");
  await expect(page.locator(".estimate .price")).toContainText("$");
  await expect(page.locator("#estimate-label")).toHaveText("Estimated price");
});

test("a blocked submit opens the hidden Solidity field, focuses it, and shows its error once", async ({ page }) => {
  await page.setExtraHTTPHeaders({ "X-Forwarded-For": "203.0.113.72" });
  await page.goto("/order#/estimate");
  await page.getByLabel("Material").selectOption("PLA");
  await dropStl(page, "cube.stl", 20);
  await page.getByLabel(/^Print time \(h/).fill("1");
  await page.getByText("How we estimated this").click();
  await page.getByLabel("Solidity (%)").fill("150");
  // No price while Solidity is invalid.
  await expect(page.locator(".estimate .price")).toHaveCount(0);
  // Collapse the section, then submit: it reopens with focus on the field.
  await page.getByText("How we estimated this").click();
  await expect(page.getByLabel("Solidity (%)")).toBeHidden();
  await page.getByLabel("Name").fill(`${PREFIX} Solidity`);
  await page.getByLabel("Email").fill("usability@example.com");
  await page.getByRole("button", { name: "Request an estimate" }).click();
  await expect(page.getByLabel("Solidity (%)")).toBeFocused();
  await expect(page.locator("#quote-solidity-err")).toHaveText("Solidity must be from 5 to 100%.");
  await expect(page.getByText("Solidity must be from 5 to 100%.")).toHaveCount(1);
  await expect(page.locator(".error-summary")).toContainText("1 field needs attention");
  await expect(page.locator(".error-summary a")).toHaveText("Solidity");
  // No price for an invalid print time either.
  await page.getByLabel("Solidity (%)").fill("20");
  await page.getByLabel(/Print time \(minutes\)/).fill("90");
  await expect(page.locator(".estimate .price")).toHaveCount(0);
  await expect(page.locator(".error-summary")).toContainText("Print time");
  await page.locator(".error-summary a").click();
  await expect(page.getByLabel(/Print time \(minutes\)/)).toBeFocused();
  expect(page.url()).toMatch(/#\/estimate$/);
});

test("quantity: no total above the cap, scientific notation is normalised, and the cap uses a separator", async ({ page, request }) => {
  await page.setExtraHTTPHeaders({ "X-Forwarded-For": "203.0.113.73" });
  try {
    await setMaxOrderQty(request, 1500);
    await page.goto("/order#/estimate");
    await page.getByLabel("Material").selectOption("PLA");
    await page.getByLabel("Model weight (g)").fill("20");
    await page.getByLabel(/^Print time \(h/).fill("1");
    await expect(page.locator(".estimate .price")).toContainText("$");

    await page.getByLabel("Quantity").fill("1501");
    await expect(page.locator("#quote-qty-notice")).toHaveText("Max 1,500 per order.");
    await expect(page.locator(".estimate .price")).toHaveCount(0);
    await expect(page.getByLabel("Quantity")).toHaveValue("1501");

    await page.getByLabel("Quantity").fill("1e3");
    await page.getByLabel("Name").focus();
    await expect(page.getByLabel("Quantity")).toHaveValue("1000");
    await expect(page.locator("#quote-qty-notice")).toHaveText("");
    await expect(page.locator(".estimate .price")).toContainText("$");

    // The setting is the cap: lowering it makes the same quantity too large.
    await setMaxOrderQty(request, 100);
    await page.reload();
    await page.getByLabel("Quantity").fill("101");
    await expect(page.locator("#quote-qty-notice")).toHaveText("Max 100 per order.");
  } finally {
    await setMaxOrderQty(request, 100);
  }
});

test("name and email carry over to another order, can be cleared, and are gone after a reload", async ({ page }) => {
  await page.setExtraHTTPHeaders({ "X-Forwarded-For": "203.0.113.74" });
  await page.goto("/order");
  await page.getByRole("button", { name: new RegExp(ITEM) }).click();
  await page.getByLabel("Color").selectOption({ label: "Sage" });
  await page.getByLabel("Name").fill(`${PREFIX} Repeat`);
  await page.getByLabel("Email").fill("repeat@example.com");
  await page.getByRole("button", { name: "Place order" }).click();
  await expect(page.getByRole("heading", { name: "Order received" })).toBeVisible();
  createdRefs.push(((await page.locator(".confirmation .ref").textContent()) ?? "").replace("Reference: ", "").trim());
  // The reference is prominent and comes with a save hint.
  await expect(page.locator(".confirmation .ref strong")).toBeVisible();
  await expect(page.locator(".confirmation .ref-hint")).toContainText("Save this reference");

  // Taps in the first half second after a confirmation appears are ignored (a double tap on Place order).
  await page.waitForTimeout(600);
  await page.getByRole("button", { name: "Back to gallery" }).click();
  await page.getByRole("button", { name: new RegExp(ITEM) }).click();
  await expect(page.getByLabel("Name")).toHaveValue(`${PREFIX} Repeat`);
  await expect(page.getByLabel("Email")).toHaveValue("repeat@example.com");

  await page.getByRole("button", { name: "Not you? Clear name and email" }).click();
  await expect(page.getByLabel("Name")).toHaveValue("");
  await expect(page.getByLabel("Email")).toHaveValue("");
  await expect(page.getByRole("button", { name: "Not you? Clear name and email" })).toHaveCount(0);

  await page.getByLabel("Name").fill("Someone");
  await page.reload();
  await expect(page.getByLabel("Name")).toHaveValue("");
});

test("an estimate's name and email carry over to Request another estimate", async ({ page }) => {
  await page.setExtraHTTPHeaders({ "X-Forwarded-For": "203.0.113.75" });
  await page.goto("/order#/estimate");
  await page.getByLabel("Material").selectOption("PLA");
  await page.getByLabel("Model weight (g)").fill("20");
  await page.getByLabel(/^Print time \(h/).fill("1");
  await page.getByLabel("Name").fill(`${PREFIX} Again`);
  await page.getByLabel("Email").fill("again@example.com");
  await page.getByRole("button", { name: "Request an estimate" }).click();
  await expect(page.getByRole("heading", { name: "Request received" })).toBeVisible();
  createdRefs.push(((await page.locator(".confirmation .ref").textContent()) ?? "").replace("Reference: ", "").trim());
  await page.waitForTimeout(600); // taps right after a confirmation appears are ignored
  await page.getByRole("button", { name: "Request another estimate" }).click();
  await expect(page.getByLabel("Name")).toHaveValue(`${PREFIX} Again`);
  await expect(page.getByLabel("Email")).toHaveValue("again@example.com");
});

test.describe("phone", () => {
  test("tap targets are 44 px, the Esc hint is hidden, and rate-limit contacts are links", async ({ browser }) => {
    const context = await browser.newContext({
      viewport: { width: 390, height: 844 },
      hasTouch: true,
      isMobile: true,
      extraHTTPHeaders: { "X-Forwarded-For": "203.0.113.76" },
    });
    const page = await context.newPage();
    try {
      // Give the shop contact details so the footer and the rate-limit message have something to link.
      await page.route("**/api/public/config", async (route) => {
        const res = await route.fetch();
        const body = await res.json();
        body.branding.email = "shop@example.com";
        body.branding.phone = "555-010-0142";
        await route.fulfill({ response: res, json: body });
      });
      await page.route("**/api/public/quote-request", (route) =>
        route.fulfill({ status: 429, contentType: "application/json", body: JSON.stringify({ errors: ["slow down"] }) }),
      );
      await page.goto("/order#/estimate");
      await page.getByLabel("Material").selectOption("PLA");
      await dropStl(page, "cube.stl", 20);

      for (const name of ["Rotate left", "Rotate right", "Reset view"]) {
        const box = await page.getByRole("button", { name }).boundingBox();
        expect(box?.height ?? 0, name).toBeGreaterThanOrEqual(44);
      }
      const links = page.locator(".shop-footer a");
      expect(await links.count()).toBeGreaterThan(1);
      for (let i = 0; i < (await links.count()); i++) {
        const box = await links.nth(i).boundingBox();
        expect(box?.height ?? 0).toBeGreaterThanOrEqual(44);
      }

      await page.getByLabel("Name").fill(`${PREFIX} Limited`);
      await page.getByLabel("Email").fill("limited@example.com");
      await page.getByRole("button", { name: "Request an estimate" }).click();
      const notice = page.locator(".rate-limited");
      await expect(notice.getByRole("link", { name: "shop@example.com" })).toHaveAttribute("href", "mailto:shop@example.com");
      const phone = notice.getByRole("link", { name: "555-010-0142" });
      await expect(phone).toHaveAttribute("href", "tel:5550100142");
      const lineHeight = await phone.evaluate((el) => parseFloat(getComputedStyle(el).lineHeight) || 20);
      expect((await phone.boundingBox())?.height ?? 0).toBeLessThan(lineHeight * 1.6);

      // The Esc hint is for keyboards.
      await page.route("**/api/public/quote-request", (route) => route.continue());
      await page.goto("/order");
      await page.getByRole("button", { name: new RegExp(ITEM) }).click();
      await expect(page.getByText("Press Esc to go back.")).toBeHidden();
    } finally {
      await context.close();
    }
  });
});
