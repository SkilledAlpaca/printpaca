import { test, expect } from "@playwright/test";
import { fillCanonicalQuote, stubWindowPrint } from "./helpers";

// Covers lib/printLogo.svelte.ts: the print views render the logo <img> only from a preloaded cached URL,
// so a slow or unreachable logo can't stall the print dialog. The only e2e that drives the print <img>.

const TINY_PNG_DATA_URL =
  "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII=";
const UNREACHABLE_LOGO_URL = "http://10.255.255.1/logo.png";

async function setLogoUrl(request: import("@playwright/test").APIRequestContext, logoUrl: string) {
  const getRes = await request.get("/admin/api/settings");
  expect(getRes.ok()).toBeTruthy();
  const body = await getRes.json();
  const updated = structuredClone(body.config);
  updated.quote.logoUrl = logoUrl;
  const putRes = await request.put("/admin/api/settings", { data: { config: updated } });
  expect(putRes.ok()).toBeTruthy();
  return body.config;
}

let originalConfig: unknown;

test.afterEach(async ({ request }) => {
  if (originalConfig) {
    await request.put("/admin/api/settings", { data: { config: originalConfig } });
  }
});

test("unreachable logo: print view renders with NO logo img (readyUrl stayed null)", async ({ page, request }) => {
  originalConfig = await setLogoUrl(request, UNREACHABLE_LOGO_URL);

  await stubWindowPrint(page);
  await page.goto("/admin/");
  await fillCanonicalQuote(page);
  // Selling must be on: canPrint needs a valid price, not just a valid cost breakdown.
  await page.getByLabel("Set a selling price").check();

  const printBtn = page.getByRole("button", { name: "Print client quote" });
  await expect(printBtn).toBeEnabled();
  await printBtn.click();

  const printView = page.locator("#print-view");
  await expect(printView).toBeVisible();
  // No readiness signal exists for a preload that never settles; wait a beat so a late-appearing logo would fail the assertion below.
  await page.waitForTimeout(1000);
  await expect(printView.locator("img.logo")).toHaveCount(0);
});

test("valid data: URL logo: print view renders the cached logo img", async ({ page, request }) => {
  originalConfig = await setLogoUrl(request, TINY_PNG_DATA_URL);

  await stubWindowPrint(page);
  await page.goto("/admin/");
  await fillCanonicalQuote(page);
  // Selling must be on: canPrint needs a valid price, not just a valid cost breakdown.
  await page.getByLabel("Set a selling price").check();

  const printBtn = page.getByRole("button", { name: "Print client quote" });
  await expect(printBtn).toBeEnabled();
  await printBtn.click();

  const printView = page.locator("#print-view");
  await expect(printView).toBeVisible();
  const logo = printView.locator("img.logo");
  await expect(logo).toHaveCount(1);
  await expect(logo).toHaveAttribute("src", TINY_PNG_DATA_URL);
});
