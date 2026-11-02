import { test, expect } from "@playwright/test";

// The public listener runs on its own port (PUBLIC_PORT, 8098), so this spec uses an absolute URL.
const PUBLIC_URL = "http://127.0.0.1:8098/";

test("public page renders branding and the no-materials state on default config", async ({ page }) => {
  await page.goto(`${PUBLIC_URL}order`);
  await expect(page.getByRole("heading", { name: "printpaca" })).toBeVisible();
  // The fresh default config has no public materials, so the request form must not render, only the
  // explanatory copy.
  await expect(page.getByText("No materials are currently offered")).toBeVisible();
});

test("public config endpoint serves branding and an empty materials list", async ({ request }) => {
  const res = await request.get(`${PUBLIC_URL}api/public/config`, { timeout: 5000 });
  expect(res.ok()).toBe(true);
  const body = await res.json();
  expect(body.branding).toBeDefined();
  expect(Array.isArray(body.materials)).toBe(true);
  expect(body.materials).toEqual([]);
});
