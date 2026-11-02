import { test, expect } from "@playwright/test";

// A length-only g-code (Cura style) must convert to weight with the selected material's density, and a
// slicer-reported weight must not move when the material changes. PETG and ABS are opted into public for
// this spec only; afterAll restores the original config.

const PUBLIC_URL = "http://127.0.0.1:8098/";
const DIAMETER_MM = 1.75;
const LENGTH_M = 10;
const DENSITIES: Record<string, number> = { PLA: 1.24, PETG: 1.27, ABS: 1.04 };

let originalConfig: Record<string, unknown> & { materials: Array<{ name: string; density: number; public?: boolean }> };

function expectedWeight(density: number): string {
  const grams = ((LENGTH_M * 1000 * Math.PI * (DIAMETER_MM / 2) ** 2) / 1000) * density;
  return String(Math.round(grams * 100) / 100);
}

const LENGTH_ONLY_GCODE = `;FLAVOR:Marlin\n;TIME:3600\n;Filament used: ${LENGTH_M}m\nG1 X0 Y0\n`;
const REPORTED_WEIGHT_GCODE = `;FLAVOR:Marlin\n;TIME:3600\n;Filament used: ${LENGTH_M}m\n; total filament used [g] = 42.5\nG1 X0 Y0\n`;

test.describe.configure({ mode: "serial" });

test.beforeAll(async ({ request }) => {
  const getRes = await request.get("/admin/api/settings");
  expect(getRes.ok()).toBeTruthy();
  originalConfig = (await getRes.json()).config;
  for (const name of Object.keys(DENSITIES)) {
    const m = originalConfig.materials.find((x) => x.name === name);
    expect(m?.density, `${name} default density`).toBe(DENSITIES[name]);
  }
  const updated = structuredClone(originalConfig);
  updated.materials = updated.materials.map((m) => (m.name in DENSITIES ? { ...m, public: true } : m));
  const putRes = await request.put("/admin/api/settings", { data: { config: updated } });
  expect(putRes.ok()).toBeTruthy();
});

test.afterAll(async ({ request }) => {
  if (originalConfig) {
    const res = await request.put("/admin/api/settings", { data: { config: originalConfig } });
    expect(res.ok()).toBeTruthy();
  }
});

test("a length-only g-code derives weight from the selected material's density and follows material changes", async ({ page }) => {
  await page.goto(`${PUBLIC_URL}order`);
  await expect(page.getByRole("heading", { name: "printpaca" })).toBeVisible();

  await page.getByLabel("Material").selectOption("PETG");
  await page.setInputFiles("#public-gcode-file", {
    name: "length-only.gcode",
    mimeType: "text/plain",
    buffer: Buffer.from(LENGTH_ONLY_GCODE, "utf-8"),
  });
  const meta = page.locator(".parsed-meta");
  await expect(meta).toContainText(`Weight: ${expectedWeight(1.27)} g`);

  await page.getByLabel("Material").selectOption("ABS");
  await expect(meta).toContainText(`Weight: ${expectedWeight(1.04)} g`);

  await page.getByLabel("Material").selectOption("PLA");
  await expect(meta).toContainText(`Weight: ${expectedWeight(1.24)} g`);
});

test("a slicer-reported weight does not change when the material changes", async ({ page }) => {
  await page.goto(`${PUBLIC_URL}order`);
  await expect(page.getByRole("heading", { name: "printpaca" })).toBeVisible();

  await page.getByLabel("Material").selectOption("PETG");
  await page.setInputFiles("#public-gcode-file", {
    name: "reported.gcode",
    mimeType: "text/plain",
    buffer: Buffer.from(REPORTED_WEIGHT_GCODE, "utf-8"),
  });
  const meta = page.locator(".parsed-meta");
  await expect(meta).toContainText("Weight: 42.5 g");

  await page.getByLabel("Material").selectOption("ABS");
  await expect(meta).toContainText("Weight: 42.5 g");
});
