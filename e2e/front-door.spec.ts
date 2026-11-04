import { test, expect } from "@playwright/test";
import { ADMIN_URL, navButton, requestIdByReference } from "./helpers";

// The shop is the landing page: "/" and "/order" both render it, next to the admin app (/admin/), all on one
// hostname: the main port. Runs against the :8099 server (authentication off) and, for the Staff sign-in link,
// the :9099 server (password set). playwright.config.ts sets ADMIN_TRUST_PROXY on :8099 so this spec can
// order from its own address (X-Forwarded-For) without spending the per-IP submission budget the
// public-listener specs share.

const CUSTOMER_NAME = "Front Door Visitor";
const CUSTOMER_EMAIL = "front-door@example.com";
const ITEM_NAME = "Front Door Planter";
const VISITOR_IP = "203.0.113.77";

test.describe.configure({ mode: "serial" });

test("/ renders the shop with the business name and a Staff sign-in footer link", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByRole("heading", { level: 1, name: "printpaca" })).toBeVisible();
  // The shop itself: its estimate flow, or its empty state on a fresh config. No separate landing page.
  await expect(page.getByRole("link", { name: "Order or get an estimate" })).toHaveCount(0);
  const footer = page.locator("footer.shop-footer");
  await expect(footer.getByRole("link", { name: "Staff sign-in" })).toHaveAttribute("href", "/admin/");
  await expect(footer).toContainText("Powered by printpaca");
  await expect(footer.getByRole("link", { name: "printpaca", exact: true })).toHaveAttribute("href", "https://printpaca.com");
  // One page, so no Home or Shop links.
  await expect(footer.getByRole("link", { name: "Home" })).toHaveCount(0);
  await expect(footer.getByRole("link", { name: "Shop" })).toHaveCount(0);
});

test("/order renders the same shop, so old links keep working", async ({ page }) => {
  await page.goto("/order");
  await expect(page).toHaveURL(/\/order$/);
  await expect(page.getByRole("heading", { level: 1, name: "printpaca" })).toBeVisible();
  await expect(page.locator("footer.shop-footer").getByRole("link", { name: "Staff sign-in" })).toBeVisible();
});

test("Staff sign-in reaches the admin login", async ({ page }) => {
  // The :9099 server has a password, so /admin/ shows the sign-in form.
  const origin = new URL(ADMIN_URL).origin;
  await page.goto(`${origin}/`);
  await page.getByRole("link", { name: "Staff sign-in" }).click();
  await expect(page).toHaveURL(new RegExp(`^${origin}/admin/(#.*)?$`));
  await expect(page.getByLabel("Password")).toBeVisible();
  await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();
  // The sign-in page says who it is for and leads back to the shop.
  await expect(page.getByText(/^For .+ staff$/)).toBeVisible();
  await page.getByRole("link", { name: "Back to shop" }).click();
  await expect(page).toHaveURL(new RegExp(`^${origin}/(#.*)?$`));
  await expect(page.locator("footer.shop-footer")).toBeVisible();
});

test("/api/* on the main port is a 404 and /admin redirects", async ({ request }) => {
  const missing = await request.get("/api/quotes");
  expect(missing.status()).toBe(404);
  expect(missing.headers()["content-type"]).toContain("application/json");
  expect((await request.get("/admin/api/quotes")).ok()).toBe(true);

  const redirect = await request.get("/admin", { maxRedirects: 0 });
  expect(redirect.status()).toBe(301);
  expect(redirect.headers()["location"]).toBe("/admin/");
});

test("the root has no service worker and /admin/sw.js is the real one", async ({ request }) => {
  expect((await request.get("/sw.js")).status()).toBe(404);

  const real = await request.get("/admin/sw.js");
  expect(real.status()).toBe(200);
  const realBody = await real.text();
  expect(realBody).not.toContain("registration.unregister()");
  expect(realBody).toContain("caches");
});

test("the admin bundle is not served from the root", async ({ request }) => {
  const res = await request.get("/index.html");
  expect(res.status()).toBe(404);
});

test.describe("an item order placed through the shop at / on the main port", () => {
  let itemId: string | null = null;
  let requestId: string | null = null;

  test.afterAll(async ({ request }) => {
    if (requestId) await request.delete(`/admin/api/requests/${requestId}`);
    if (itemId) await request.delete(`/admin/api/items/${itemId}`);
  });

  test("lands in the admin Requests list", async ({ page, request }) => {
    const created = await request.post("/admin/api/items", {
      data: {
        name: ITEM_NAME,
        description: "A small printed planter.",
        priceCents: 1200,
        public: true,
        stockMode: "in_stock",
        stockQty: 3,
        sortOrder: 0,
      },
    });
    expect(created.ok()).toBeTruthy();
    itemId = (await created.json()).id;

    // handlePublicItems caches for 5s with no write invalidation: poll until the new item is served.
    await expect
      .poll(
        async () => {
          const r = await request.get("/api/public/items");
          return ((await r.json()).items as { name: string }[]).some((i) => i.name === ITEM_NAME);
        },
        { timeout: 15_000, intervals: [500, 500, 1000] },
      )
      .toBe(true);

    await page.setExtraHTTPHeaders({ "X-Forwarded-For": VISITOR_IP });
    await page.goto("/");
    await page.getByRole("button", { name: new RegExp(ITEM_NAME) }).click();
    await expect(page.getByRole("heading", { name: ITEM_NAME })).toBeVisible();
    await page.getByLabel("Name").fill(CUSTOMER_NAME);
    await page.getByLabel("Email").fill(CUSTOMER_EMAIL);
    await page.getByRole("button", { name: "Place order" }).click();

    await expect(page.getByText("Order received")).toBeVisible();
    const refText = (await page.locator(".confirmation .ref").textContent())?.trim() ?? "";
    const refMatch = refText.match(/^Reference: (.+)$/);
    expect(refMatch).not.toBeNull();
    requestId = await requestIdByReference(request, refMatch![1]!);

    await page.setExtraHTTPHeaders({});
    await page.goto("/admin/");
    await navButton(page, "Requests").click();
    await expect(page).toHaveURL(/#\/requests$/);
    const row = page.locator("tr.req-row", { hasText: CUSTOMER_NAME });
    await expect(row).toBeVisible();
    await expect(row.getByText("item order")).toBeVisible();
  });
});
