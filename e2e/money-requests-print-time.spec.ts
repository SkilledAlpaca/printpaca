import { test, expect } from "@playwright/test";
import type { APIRequestContext } from "@playwright/test";
import { navButton } from "./helpers";

// D36: a request that needs a print time shows one compact notice.

// A public estimate needs a public material, so PLA is opted in with a full-document PUT /api/settings (as
// money-requests.spec.ts does) and the original config is put back afterward.
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
  if (originalConfig) await request.put("/admin/api/settings", { data: { config: originalConfig } });
});

let xff = 0;
async function seedNoTimeRequest(request: APIRequestContext, name: string): Promise<string> {
  xff += 1;
  const res = await request.post("/api/public/quote-request", {
    headers: { "X-Forwarded-For": `198.51.100.${40 + xff}` },
    multipart: { name, email: `${name.toLowerCase().replace(/[^a-z]+/g, ".")}@example.com`, note: "", material: "PLA", quantity: "1", weightG: "12" },
  });
  expect(res.status(), await res.text()).toBe(201);
  return ((await res.json()) as { id: string }).id;
}

test.describe("request that needs a print time (D36)", () => {
  let id = "";
  const name = `Pat Estimate ${Date.now() % 100000}`;
  test.beforeAll(async ({ request }) => {
    id = await seedNoTimeRequest(request, name);
  });
  test.afterAll(async ({ request }) => {
    await request.delete(`/admin/api/requests/${id}`);
  });

  test("the notice is one compact block: a heading, one sentence, and the actions in one row", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto("/admin/");
    await navButton(page, "Requests").click();
    await page.locator("tr.req-row", { hasText: name }).getByRole("button", { name: new RegExp(name) }).click();
    const notice = page.getByRole("group", { name: "This estimate needs a print time" });
    await expect(notice).toBeVisible();

    // Exactly one heading and one sentence.
    await expect(notice.getByRole("heading")).toHaveCount(1);
    await expect(notice.locator("p")).toHaveCount(1);
    // The old extra lines and the nested "Print time" box are gone.
    await expect(page.getByText("Enter the print time before finalizing.")).toHaveCount(0);
    await expect(page.getByText("Enter the print time above before converting")).toHaveCount(0);
    await expect(notice.getByRole("group", { name: "Print time" })).toHaveCount(0);

    // Hours, Minutes and Save share one row.
    const boxes = await Promise.all([notice.getByLabel("Hours"), notice.getByLabel("Minutes"), notice.getByRole("button", { name: "Save print time" })].map((l) => l.boundingBox()));
    const mid = boxes.map((b) => b!.y + b!.height / 2);
    expect(Math.max(...mid) - Math.min(...mid)).toBeLessThanOrEqual(2);

    // The disabled actions are explained by the notice itself.
    for (const action of ["Finalize", "Convert to invoice"]) {
      const b = page.getByRole("button", { name: action });
      await expect(b).toHaveAttribute("aria-disabled", "true");
      await expect(b).toHaveAccessibleDescription(/there's no price yet/);
    }

    // Saving a time removes the whole notice.
    await notice.getByLabel("Hours").fill("1");
    await notice.getByLabel("Minutes").fill("30");
    await notice.getByRole("button", { name: "Save print time" }).click();
    await expect(page.getByRole("heading", { name: "This estimate needs a print time" })).toHaveCount(0);
  });

  test("on a phone the row wraps instead of overflowing", async ({ page, request }) => {
    const phoneName = `Pat Phone ${Date.now() % 100000}`;
    const phoneId = await seedNoTimeRequest(request, phoneName);
    try {
      await page.setViewportSize({ width: 390, height: 844 });
      await page.goto("/admin/#/requests");
      await page.getByRole("button", { name: new RegExp(phoneName) }).first().click();
      const notice = page.getByRole("group", { name: "This estimate needs a print time" });
      await expect(notice).toBeVisible();
      const n = (await notice.boundingBox())!;
      for (const l of [notice.getByLabel("Hours"), notice.getByLabel("Minutes"), notice.getByRole("button", { name: "Save print time" })]) {
        const b = (await l.boundingBox())!;
        expect(b.x).toBeGreaterThanOrEqual(n.x - 1);
        expect(b.x + b.width).toBeLessThanOrEqual(n.x + n.width + 1);
      }
    } finally {
      await request.delete(`/admin/api/requests/${phoneId}`);
    }
  });
});
