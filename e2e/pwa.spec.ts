import { test, expect } from "@playwright/test";
import { seedQuoteBody } from "./helpers";

// Admin-SPA PWA surface: manifest, sw.js, icons, and service-worker registration. The admin app lives
// under /admin/, so its manifest, worker and scope do too. baseURL is a secure context, which exercises the
// registration path that plain-HTTP LAN deployments skip.

test("manifest.webmanifest is served correctly and its icons resolve", async ({ request, baseURL }) => {
  const res = await request.get("/admin/manifest.webmanifest");
  expect(res.status()).toBe(200);
  expect(res.headers()["content-type"]).toContain("application/manifest+json");

  const manifest = (await res.json()) as { start_url: string; scope: string; icons: { src: string; type: string }[] };
  expect(manifest.icons).toHaveLength(3);
  // Relative to the manifest's own URL, so the installed app starts and stays inside /admin/.
  expect(new URL(manifest.start_url, `${baseURL}/admin/manifest.webmanifest`).pathname).toBe("/admin/");
  expect(new URL(manifest.scope, `${baseURL}/admin/manifest.webmanifest`).pathname).toBe("/admin/");

  for (const icon of manifest.icons) {
    expect(icon.type).toBe("image/png");
    const iconRes = await request.get(new URL(icon.src, `${baseURL}/admin/manifest.webmanifest`).toString());
    expect(iconRes.status(), `${icon.src} should be served`).toBe(200);
    expect(iconRes.headers()["content-type"]).toBe("image/png");
  }
});

test("the admin page links the manifest in <head>", async ({ page }) => {
  await page.goto("/admin/");
  const href = await page.locator('head link[rel="manifest"]').getAttribute("href");
  expect(href).toBe("./manifest.webmanifest");
});

test("sw.js is served as JS with no-cache, so updates propagate", async ({ request }) => {
  const res = await request.get("/admin/sw.js");
  expect(res.status()).toBe(200);
  expect(res.headers()["content-type"]).toContain("javascript");
  expect(res.headers()["cache-control"]).toContain("no-cache");
});

test("the service worker registers and takes control of the page", async ({ page }) => {
  await page.goto("/admin/");

  // Generous timeout: install, activate, and clients.claim() must finish in a cold browser context.
  await page.waitForFunction(() => navigator.serviceWorker.controller !== null, undefined, { timeout: 15_000 });

  const registration = await page.evaluate(async () => {
    const reg = await navigator.serviceWorker.getRegistration();
    return reg ? { scope: reg.scope, active: reg.active?.state } : null;
  });
  expect(registration).not.toBeNull();
  expect(registration?.scope).toBe(`${new URL(page.url()).origin}/admin/`);
  expect(registration?.active).toBe("activated");
});

test("the service worker never controls the front door, the shop, or the public API", async ({ page, baseURL }) => {
  await page.goto("/admin/");
  await page.waitForFunction(() => navigator.serviceWorker.controller !== null, undefined, { timeout: 15_000 });

  // The same browser context, so the admin worker is installed; pages outside /admin/ must not be controlled.
  for (const path of ["/", "/order"]) {
    await page.goto(`${baseURL}${path}`);
    expect(await page.evaluate(() => navigator.serviceWorker.controller), `controller at ${path}`).toBeNull();
    expect(await page.evaluate(async () => (await navigator.serviceWorker.getRegistration())?.scope ?? null), `registration at ${path}`).toBeNull();
  }
});

test("API responses stay live (server-authoritative) after the service worker is active", async ({ page, request, baseURL }) => {
  await page.goto("/admin/");
  await page.waitForFunction(() => navigator.serviceWorker.controller !== null, undefined, { timeout: 15_000 });

  // A write then read must reflect the write, proving the SW's api/ bypass holds while it controls the page.
  const created = await request.post(`${baseURL}/admin/api/quotes`, {
    data: seedQuoteBody("pwa-sw-bypass-check"),
  });
  expect(created.ok()).toBeTruthy();
  const { id } = (await created.json()) as { id: string };

  try {
    // Issued from the controlled page so it passes through the service worker's fetch handler.
    const seenLabel = await page.evaluate(async (quoteId) => {
      const res = await fetch(`api/quotes/${quoteId}`);
      const body = await res.json();
      return body.label as string;
    }, id);
    expect(seenLabel).toBe("pwa-sw-bypass-check");
  } finally {
    await request.delete(`${baseURL}/admin/api/quotes/${id}`);
  }
});
