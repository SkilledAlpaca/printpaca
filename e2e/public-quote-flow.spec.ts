import { test, expect } from "@playwright/test";
import { navButton } from "./helpers";

// Drives a visitor's manual-entry submission on the public listener (:8098, absolute URL) through to the
// admin Requests tab on :8099. beforeAll opts PLA into public via a full-document PUT /api/settings;
// afterAll deletes the request and reverts the config so requests.spec.ts sees an empty list.

const PUBLIC_URL = "http://127.0.0.1:8098/";
const VISITOR_NAME = "Ada Visitor";
const VISITOR_EMAIL = "ada@example.com";

// The top-level customCss key, set in beforeAll and reverted in afterAll. A bare custom property on :root
// can't collide with theme.css, so its presence after a fresh load proves the stylesheet applied.
const CUSTOM_CSS_PROBE_PROPERTY = "--e2e-custom-css-probe";
const CUSTOM_CSS_PROBE_VALUE = "42px";
const CUSTOM_CSS_PROBE = `:root { ${CUSTOM_CSS_PROBE_PROPERTY}: ${CUSTOM_CSS_PROBE_VALUE}; }`;

// Mirrors the server's Config shape; this spec only reads it back and flips one boolean.
let originalConfig: Record<string, unknown> & { materials: Array<{ name: string; public?: boolean }> };
let materialIndex = -1;
let createdRequestId: string | null = null;
let createdStlRequestId: string | null = null;

// A 10mm axis-aligned cube (1 cm3) as ASCII STL, spelled out here because e2e doesn't import from web/src.
// It only needs to parse to a nonzero volume; the exact figure is never asserted.
const CUBE_STL = `solid cube
${[
  [[0, 0, 0], [0, 10, 0], [10, 10, 0]],
  [[0, 0, 0], [10, 10, 0], [10, 0, 0]],
  [[0, 0, 10], [10, 0, 10], [10, 10, 10]],
  [[0, 0, 10], [10, 10, 10], [0, 10, 10]],
  [[0, 0, 0], [10, 0, 0], [10, 0, 10]],
  [[0, 0, 0], [10, 0, 10], [0, 0, 10]],
  [[0, 10, 0], [0, 10, 10], [10, 10, 10]],
  [[0, 10, 0], [10, 10, 10], [10, 10, 0]],
  [[0, 0, 0], [0, 0, 10], [0, 10, 10]],
  [[0, 0, 0], [0, 10, 10], [0, 10, 0]],
  [[10, 0, 0], [10, 10, 0], [10, 10, 10]],
  [[10, 0, 0], [10, 10, 10], [10, 0, 10]],
]
  .map(
    (tri) =>
      `  facet normal 0 0 0\n    outer loop\n${tri.map((v) => `      vertex ${v[0]} ${v[1]} ${v[2]}`).join("\n")}\n    endloop\n  endfacet`,
  )
  .join("\n")}
endsolid cube
`;

test.describe.configure({ mode: "serial" });

test.beforeAll(async ({ request }) => {
  const getRes = await request.get("/admin/api/settings");
  expect(getRes.ok()).toBeTruthy();
  const body = await getRes.json();
  originalConfig = body.config;

  materialIndex = originalConfig.materials.findIndex((m: { name: string }) => m.name === "PLA");
  expect(materialIndex).toBeGreaterThanOrEqual(0);

  const updated = structuredClone(originalConfig);
  // Only PLA (the first default material) is opted into the public list; "public quotes enabled" is
  // derived from any material carrying public: true. .map keeps this valid under noUncheckedIndexedAccess.
  updated.materials = updated.materials.map((m, i) => (i === materialIndex ? { ...m, public: true } : m));
  // Cast instead of widening originalConfig's type: customCss is only written as an opaque bag.
  (updated as Record<string, unknown>).customCss = CUSTOM_CSS_PROBE;

  const putRes = await request.put("/admin/api/settings", { data: { config: updated } });
  expect(putRes.ok()).toBeTruthy();
});

test.afterAll(async ({ request }) => {
  if (createdRequestId) {
    await request.delete(`/admin/api/requests/${createdRequestId}`);
  }
  if (createdStlRequestId) {
    await request.delete(`/admin/api/requests/${createdStlRequestId}`);
  }
  if (originalConfig) {
    const revertRes = await request.put("/admin/api/settings", { data: { config: originalConfig } });
    expect(revertRes.ok()).toBeTruthy();
  }
});

test("a visitor submits a manual quote request and it reaches the owner", async ({ page, request }) => {
  // 1. Visitor side (:8098): the form renders, manual entry, a live price, submit, confirmation.
  await page.goto(`${PUBLIC_URL}order`);
  await expect(page.getByRole("heading", { name: "printpaca" })).toBeVisible();
  await expect(page.getByText("No materials are currently offered")).not.toBeVisible();

  await page.getByLabel("Material").selectOption("PLA");
  await page.getByLabel("Quantity").fill("2");
  // Manual entry (no file): weightG + hours/minutes, as fillCanonicalQuote does on the admin tab.
  await page.getByLabel(/Model weight/).fill("50");
  await page.getByLabel(/^Print time \(h/).fill("1");
  await page.getByLabel(/Print time \(minutes\)/).fill("20");

  // The debounced preview round trip resolves this; expect's auto-retry covers the ~350ms debounce.
  const priceLocator = page.locator(".estimate .price");
  await expect(priceLocator).toBeVisible();
  const priceText = (await priceLocator.textContent())?.trim();
  expect(priceText).toMatch(/^\$\d/);
  // With quantity 2 the estimate appends a " · $X.XX each" suffix that the confirmation card and the
  // Requests row never show, so compare against the total-only prefix.
  const priceTotalText = priceText!.match(/^\$[\d,.]+/)![0];

  await page.getByLabel("Name").fill(VISITOR_NAME);
  await page.getByLabel("Email").fill(VISITOR_EMAIL);
  await page.getByLabel("Note (optional)").fill("Please rush this if possible.");

  await page.getByRole("button", { name: "Request an estimate" }).click();

  // The confirmation card: "Request received", the same total, and "Reference: <id>".
  await expect(page.getByText("Request received")).toBeVisible();
  await expect(page.locator(".confirmation .price")).toHaveText(priceTotalText);
  const refText = (await page.locator(".confirmation .ref").textContent())?.trim() ?? "";
  const refMatch = refText.match(/^Reference: (.+)$/);
  expect(refMatch).not.toBeNull();
  createdRequestId = refMatch![1]!;

  // 2. Owner side (:8099): the Requests tab lists the request with the submitted name and price.
  await page.goto("/admin/");
  await navButton(page, "Requests").click();
  await expect(page).toHaveURL(/#\/requests$/);

  const row = page.locator(".plain-list li", { hasText: VISITOR_NAME });
  await expect(row).toBeVisible();
  await expect(row.locator(".r-price")).toHaveText(priceTotalText);

  // 3. The stored price_cents is server-computed (the browser never sends one) and equals the price the
  // visitor's page displayed.
  const apiRes = await request.get(`/admin/api/requests/${createdRequestId}`);
  expect(apiRes.ok()).toBeTruthy();
  const stored = await apiRes.json();
  expect(stored.name).toBe(VISITOR_NAME);
  expect(stored.email).toBe(VISITOR_EMAIL);
  expect(stored.priceMismatch).toBeUndefined();
  expect(stored.serverPriceCents).toBeUndefined();
  expect(typeof stored.priceCents).toBe("number");
  expect(stored.priceCents).toBeGreaterThan(0);
});

// A parsed STL drop retains its File and uploads it in the "gcode" multipart field. An STL yields only a
// weight estimate, so print time is still entered manually.
const STL_VISITOR_NAME = "Ivy Sculptor";
const STL_VISITOR_EMAIL = "ivy@example.com";
const STL_FILENAME = "part.stl";

test("a visitor drops an STL and the admin Requests row exposes the uploaded file", async ({ page, request }) => {
  await page.goto(`${PUBLIC_URL}order`);
  await expect(page.getByRole("heading", { name: "printpaca" })).toBeVisible();

  await page.getByLabel("Material").selectOption("PLA");
  await page.getByLabel("Quantity").fill("1");

  await page.setInputFiles("#public-gcode-file", {
    name: STL_FILENAME,
    mimeType: "model/stl",
    buffer: Buffer.from(CUBE_STL, "utf-8"),
  });

  // The STL parses to a weight-only estimate, so a manual print time is still needed.
  await expect(page.locator(".parsed-meta")).toContainText("Estimated weight");

  // A parsed STL also feeds ModelPreview's canvas. role="img" sits on the wrapper div, not the <canvas>
  // (Svelte's a11y check objects), and getByRole matches by computed role.
  await expect(page.getByRole("img", { name: `3D preview of ${STL_FILENAME}` })).toBeVisible();

  await page.getByLabel(/^Print time \(h/).fill("2");
  await page.getByLabel(/Print time \(minutes\)/).fill("0");

  const priceLocator = page.locator(".estimate .price");
  await expect(priceLocator).toBeVisible();

  await page.getByLabel("Name").fill(STL_VISITOR_NAME);
  await page.getByLabel("Email").fill(STL_VISITOR_EMAIL);

  await page.getByRole("button", { name: "Request an estimate" }).click();

  await expect(page.getByText("Request received")).toBeVisible();
  const refText = (await page.locator(".confirmation .ref").textContent())?.trim() ?? "";
  const refMatch = refText.match(/^Reference: (.+)$/);
  expect(refMatch).not.toBeNull();
  createdStlRequestId = refMatch![1]!;

  // Owner side (:8099): the Requests detail view shows the uploaded filename and a working download link.
  await page.goto("/admin/");
  await navButton(page, "Requests").click();
  await expect(page).toHaveURL(/#\/requests$/);

  const row = page.locator(".plain-list li", { hasText: STL_VISITOR_NAME });
  await expect(row).toBeVisible();
  await row.getByRole("button").click();

  await expect(page.getByRole("heading", { name: `Estimate request from ${STL_VISITOR_NAME}` })).toBeVisible();
  const gcodeLine = page.locator(".gcode-line");
  await expect(gcodeLine).toContainText(STL_FILENAME);
  await expect(gcodeLine.getByRole("link", { name: "Download" })).toHaveAttribute(
    "href",
    `api/requests/${createdStlRequestId}/gcode`,
  );

  // A stored .stl is classified "model", so RequestsTab skips the Verification subcard and shows the
  // neutral explanatory line instead, with no recompute alert.
  await expect(page.getByRole("alert").filter({ hasText: "Couldn't recompute weight and print time" })).toHaveCount(0);
  await expect(
    page.getByText("Weight came from the uploaded model file itself"),
  ).toBeVisible();
  await expect(page.getByRole("heading", { name: "Verification" })).toHaveCount(0);

  // The stored bytes round-trip exactly (as the Go download tests pin), here via a real browser upload.
  const apiRes = await request.get(`/admin/api/requests/${createdStlRequestId}`);
  expect(apiRes.ok()).toBeTruthy();
  const stlStored = await apiRes.json();
  expect(stlStored.gcodeFilename).toBe(STL_FILENAME);
  expect(stlStored.gcodeBytes).toBe(Buffer.byteLength(CUBE_STL, "utf-8"));

  const dlRes = await request.get(`/admin/api/requests/${createdStlRequestId}/gcode`);
  expect(dlRes.ok()).toBeTruthy();
  expect(await dlRes.text()).toBe(CUBE_STL);
  expect(dlRes.headers()["content-disposition"] ?? "").toContain(STL_FILENAME);
});

// docs/formulas.md's "Hardware scope" says hardware is admin-quote-only; only the Go layer pinned that.
// This drives manual entry twice, against an empty catalog and after configuring a hardware entry, and
// asserts the public quote's price is byte-identical.
test("a configured hardware entry never changes a public quote's price", async ({ page, request }) => {
  async function fillManualQuote(): Promise<void> {
    await page.getByLabel("Material").selectOption("PLA");
    await page.getByLabel("Quantity").fill("2");
    await page.getByLabel(/Model weight/).fill("50");
    await page.getByLabel(/^Print time \(h/).fill("1");
    await page.getByLabel(/Print time \(minutes\)/).fill("20");
  }

  await page.goto(`${PUBLIC_URL}order`);
  await expect(page.getByRole("heading", { name: "printpaca" })).toBeVisible();
  await fillManualQuote();
  const priceLocator = page.locator(".estimate .price");
  await expect(priceLocator).toBeVisible();
  const baselinePrice = (await priceLocator.textContent())?.trim();
  expect(baselinePrice).toMatch(/^\$\d/);

  const getRes = await request.get("/admin/api/settings");
  expect(getRes.ok()).toBeTruthy();
  const current = (await getRes.json()).config;
  const withHardware = structuredClone(current);
  // A huge unit cost, so any leak into the public path would move the price visibly.
  withHardware.hardware = [{ name: "e2e wo31 hardware", unitCost: 999999, lostOnFailure: false }];
  const putRes = await request.put("/admin/api/settings", { data: { config: withHardware } });
  expect(putRes.ok()).toBeTruthy();

  // No manual revert: afterAll restores originalConfig, which covers hardware.
  await page.reload();
  await expect(page.getByRole("heading", { name: "printpaca" })).toBeVisible();
  await fillManualQuote();
  await expect(priceLocator).toHaveText(baselinePrice!);
});

// The top-level customCss key (GET /custom.css) was set in beforeAll, so a fresh load of the public page
// should have it applied. This proves the stylesheet loads and its rule takes effect; cascade order is a
// build-time property of web/public.html.
test("the public page applies the operator's custom CSS", async ({ page }) => {
  await page.goto(`${PUBLIC_URL}order`);
  await expect(page.getByRole("heading", { name: "printpaca" })).toBeVisible();

  const probeValue = await page.evaluate(
    (prop) => getComputedStyle(document.documentElement).getPropertyValue(prop).trim(),
    CUSTOM_CSS_PROBE_PROPERTY,
  );
  expect(probeValue).toBe(CUSTOM_CSS_PROBE_VALUE);
});
