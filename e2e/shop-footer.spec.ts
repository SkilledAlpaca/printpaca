import { test, expect } from "@playwright/test";
import type { APIRequestContext, Page } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { requestIdByReference } from "./helpers";

// D44 (the shop footer) and D45 (the header logo and name link home). The shop's branding, footer links and
// pause state are set through the settings API; the original config is restored afterwards.

type Json = Record<string, any>;
const ITEM = "FooterSpec Planter";
const NAME = "Footer Spec Shop";
const PNG = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAEAAAABACAYAAACqaXHeAAAA3ElEQVR4nO3QwQ2DQAwFUUpJaZROJ+GKkBDJwu6s7fmS7563LM653tvWz7f16N+b9yQ6LEaP6DAYI+OnQiDCp4GgwzEEOhaFoANRBDoMRaCDUAQ6BEegI1AAOgBFoB/HEeinUQD6YRSBfhRHoJ8UgASgH8QR6OcEIAHox3AE+ikBBBBAAAEEEEAAAQQQQAABCIAqCJfxAgiQH+E2XgAB8iL8HJ8R4e94ARIhNMdnQHgcL0BghNfiIyK8Hh8JoVt8BITu8bNCDA2fDQGLP65s+HGl488rGX21ktEuyHYEagXuNXObIQAAAABJRU5ErkJggg==";
const SHOTS = process.env.FOOTER_SHOTS ?? "";
const TAGS = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"];
let original: Json;
let itemId = "";
const requests: string[] = [];
let xffN = 0;
const xff = () => ({ "X-Forwarded-For": `198.51.100.${10 + (xffN++ % 200)}` });

async function getConfig(request: APIRequestContext): Promise<Json> {
  const res = await request.get("/admin/api/settings");
  expect(res.ok()).toBeTruthy();
  return (await res.json()).config as Json;
}
async function apply(request: APIRequestContext, quote: Json, shop: Json = {}): Promise<void> {
  const config = structuredClone(original);
  config.materials = config.materials.map((m: Json) => (m.name === "PLA" ? { ...m, public: true } : m));
  Object.assign(config.quote, { businessName: NAME, logoUrl: "", email: "", phone: "", address: "" }, quote);
  Object.assign(config.shop, { footerLinks: [], gallery: true, customEstimates: true, acceptingOrders: true, pausedMessage: "" }, shop);
  const res = await request.put("/admin/api/settings", { data: { config } });
  expect(res.ok(), await res.text()).toBeTruthy();
}
const FULL = { logoUrl: PNG, address: "12 Alpaca Lane\nSpringfield", email: "hello@footerspec.example", phone: "555-010-0199" };
const LINKS = [
  { label: "Etsy shop", url: "https://etsy.example.com/footerspec" },
  { label: "Instagram", url: "https://instagram.example.com/footerspec" },
];

test.describe.configure({ mode: "serial" });

test.beforeAll(async ({ request }) => {
  original = await getConfig(request);
  const res = await request.post("/admin/api/items", { data: { name: ITEM, description: "A planter.", priceCents: 1500, public: true, sortOrder: 61 } });
  expect(res.ok()).toBeTruthy();
  itemId = (await res.json()).id;
  await expect
    .poll(async () => ((await (await request.get("/api/public/items")).json()).items as Json[]).some((i) => i.id === itemId), { timeout: 15_000, intervals: [500, 500, 1000] })
    .toBe(true);
});
test.afterAll(async ({ request }) => {
  for (const id of requests) await request.delete(`/admin/api/requests/${id}`);
  await request.delete(`/admin/api/items/${itemId}`);
  await request.put("/admin/api/settings", { data: { config: original } });
});

/** The footer plus some page above it. */
async function shoot(page: Page, name: string): Promise<void> {
  if (!SHOTS) return;
  await page.evaluate(() => document.fonts.ready);
  const box = (await page.locator("footer.shop-footer").boundingBox())!;
  const top = Math.max(0, box.y - 220);
  await page.screenshot({ path: `${SHOTS}/${name}.png`, fullPage: true, clip: { x: 0, y: top, width: page.viewportSize()!.width, height: box.y + box.height - top } });
}
async function axe(page: Page): Promise<string[]> {
  await page.waitForLoadState("networkidle");
  const results = await new AxeBuilder({ page }).withTags(TAGS).analyze();
  return results.violations.map((v) => `${v.id}: ${v.nodes[0]?.target.join(" ")}`);
}
const VIEWS = [
  { w: 390, h: 844 },
  { w: 1280, h: 800 },
  { w: 1440, h: 900 },
];

test.describe("D44 footer", () => {
  test("full shop: three columns, bottom row with copyright, Staff sign-in and the credit on the right", async ({ page, request }) => {
    await apply(request, FULL, { footerLinks: LINKS });
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.goto("/");
    const footer = page.locator("footer.shop-footer");
    await expect(footer.locator(".columns > *")).toHaveCount(3);
    await expect(footer.locator(".business")).toContainText(NAME);
    await expect(footer.locator(".business img")).toHaveCount(1);
    await expect(footer.locator(".business")).toContainText("12 Alpaca Lane");
    await expect(footer.getByRole("heading", { name: "Contact" })).toBeVisible();
    await expect(footer.getByRole("link", { name: "hello@footerspec.example" })).toHaveAttribute("href", "mailto:hello@footerspec.example");
    await expect(footer.getByRole("link", { name: "555-010-0199" })).toHaveAttribute("href", "tel:5550100199");
    await expect(footer.getByRole("navigation", { name: "Links" })).toBeVisible();
    await expect(footer.getByRole("link", { name: "Etsy shop" })).toHaveAttribute("href", "https://etsy.example.com/footerspec");
    await expect(footer.locator(".legal")).toContainText(`© ${new Date().getFullYear()} ${NAME}`);
    await expect(footer.getByRole("link", { name: "Staff sign-in" })).toHaveAttribute("href", "/admin/");
    // Three columns sit side by side.
    const xs = await footer.locator(".columns > *").evaluateAll((els) => els.map((e) => Math.round(e.getBoundingClientRect().x)));
    expect(new Set(xs).size).toBe(3);
    // The credit: right-aligned on the bottom row, its link goes to printpaca.com and opens a new tab.
    const credit = footer.locator(".powered-by");
    await expect(credit).toContainText("Powered by printpaca");
    const link = credit.getByRole("link", { name: "printpaca" });
    await expect(link).toHaveAttribute("href", "https://printpaca.com");
    await expect(link).toHaveAttribute("target", "_blank");
    await expect(link).toHaveAttribute("rel", /noopener/);
    const cb = (await credit.boundingBox())!;
    const fb = (await footer.locator(".bottom").boundingBox())!;
    expect(cb.x + cb.width).toBeGreaterThan(fb.x + fb.width - 4);
    expect(cb.x).toBeGreaterThan(fb.x + fb.width / 2);
    // The credit is set in the smallest type size in the footer.
    const sizes = await footer.evaluate((el) => {
      const out: number[] = [];
      const w = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
      for (let n = w.nextNode(); n; n = w.nextNode()) {
        if ((n.textContent ?? "").trim() && n.parentElement) out.push(parseFloat(getComputedStyle(n.parentElement).fontSize));
      }
      return out;
    });
    const creditSize = await credit.evaluate((el) => parseFloat(getComputedStyle(el).fontSize));
    expect(creditSize).toBe(Math.min(...sizes));
    await expect(credit).toHaveCSS("color", await footer.locator(".col-title").first().evaluate((el) => getComputedStyle(el).color));
  });

  test("a minimal shop collapses the empty columns", async ({ page, request }) => {
    await apply(request, {});
    await page.goto("/");
    const footer = page.locator("footer.shop-footer");
    await expect(footer.locator(".columns > *")).toHaveCount(1);
    await expect(footer.locator(".business")).toContainText(NAME);
    await expect(footer.locator(".business img")).toHaveCount(0);
    await expect(footer.getByRole("heading", { name: "Contact" })).toHaveCount(0);
    await expect(footer.getByRole("heading", { name: "Links" })).toHaveCount(0);
    await expect(footer.getByRole("link", { name: "Staff sign-in" })).toBeVisible();
    await expect(footer.getByRole("link", { name: "printpaca", exact: true })).toHaveAttribute("href", "https://printpaca.com");
    // Only contact details: two columns. Only links: two columns.
    await apply(request, { email: "only@footerspec.example" });
    await page.reload();
    await expect(footer.locator(".columns > *")).toHaveCount(2);
    await expect(footer.getByRole("heading", { name: "Links" })).toHaveCount(0);
    await apply(request, {}, { footerLinks: LINKS });
    await page.reload();
    await expect(footer.locator(".columns > *")).toHaveCount(2);
    await expect(footer.getByRole("heading", { name: "Contact" })).toHaveCount(0);
  });

  test("the Closed page hides the Contact column and keeps its own contact block", async ({ page, request }) => {
    await apply(request, FULL, { acceptingOrders: false, pausedMessage: "Back Monday", footerLinks: LINKS });
    await page.goto("/");
    await expect(page.getByRole("heading", { name: "We're closed right now" })).toBeVisible();
    const footer = page.locator("footer.shop-footer");
    await expect(footer.getByRole("heading", { name: "Contact" })).toHaveCount(0);
    await expect(footer.getByRole("link", { name: "hello@footerspec.example" })).toHaveCount(0);
    await expect(footer.getByRole("navigation", { name: "Links" })).toBeVisible();
    await expect(page.getByRole("list", { name: "Contact details" }).getByRole("link", { name: "hello@footerspec.example" })).toBeVisible();
    await expect(footer.getByRole("link", { name: "Staff sign-in" })).toBeVisible();
    await expect(footer.getByRole("link", { name: "printpaca", exact: true })).toHaveAttribute("href", "https://printpaca.com");
  });

  test("phone: every footer link is a 44 px tap target", async ({ page, request }) => {
    await apply(request, FULL, { footerLinks: LINKS });
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto("/");
    const links = page.locator("footer.shop-footer a");
    expect(await links.count()).toBe(2 + 2 + 2); // email, phone, two links, Staff sign-in, credit
    for (let i = 0; i < (await links.count()); i++) {
      const b = (await links.nth(i).boundingBox())!;
      expect(b.height, await links.nth(i).innerText()).toBeGreaterThanOrEqual(44);
    }
    // The columns stack.
    const xs = await page.locator("footer.shop-footer .columns > *").evaluateAll((els) => els.map((e) => Math.round(e.getBoundingClientRect().x)));
    expect(new Set(xs).size).toBe(1);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  });

  for (const scheme of ["light", "dark"] as const) {
    test(`axe and screenshots, ${scheme} theme`, async ({ page, request }) => {
      const cases: { name: string; quote: Json; shop: Json }[] = [
        { name: "full", quote: FULL, shop: { footerLinks: LINKS } },
        { name: "minimal", quote: {}, shop: {} },
        { name: "closed", quote: FULL, shop: { acceptingOrders: false, pausedMessage: "Back Monday", footerLinks: LINKS } },
      ];
      await page.emulateMedia({ colorScheme: scheme });
      const problems: string[] = [];
      for (const c of cases) {
        await apply(request, c.quote, c.shop);
        for (const v of VIEWS) {
          await page.setViewportSize({ width: v.w, height: v.h });
          await page.goto("/");
          await expect(page.locator("footer.shop-footer")).toBeVisible();
          problems.push(...(await axe(page)).map((p) => `${c.name} ${scheme} ${v.w}: ${p}`));
          await shoot(page, `footer-${c.name}-${scheme}-${v.w}`);
        }
      }
      expect(problems).toEqual([]);
    });
  }
});

test.describe("D45 header link", () => {
  const homeLink = (page: Page) => page.getByRole("link", { name: `${NAME}, shop home` });

  test("from an item the link goes home with focus on the gallery heading", async ({ page, request }) => {
    await apply(request, FULL, { footerLinks: LINKS });
    await page.setExtraHTTPHeaders(xff());
    await page.goto("/");
    await expect(homeLink(page)).toHaveAttribute("href", "#/");
    await page.getByRole("button", { name: new RegExp(ITEM) }).click();
    await expect(page.getByRole("heading", { name: ITEM, level: 2 })).toBeVisible();
    await homeLink(page).click();
    await expect(page.getByRole("heading", { name: "Items" })).toBeFocused();
    await expect(page).toHaveURL(/#\/$/);
  });

  test("from a confirmation", async ({ page, request }) => {
    await apply(request, FULL);
    await page.setExtraHTTPHeaders(xff());
    await page.goto("/");
    await page.getByRole("button", { name: new RegExp(ITEM) }).click();
    await page.getByLabel("Name", { exact: true }).fill("Footer Tester");
    await page.getByLabel("Email").fill("footer-spec@example.com");
    await page.getByRole("button", { name: "Place order" }).click();
    const region = page.getByRole("region", { name: "Order received" });
    await expect(region).toBeFocused();
    const ref = ((await region.locator(".ref strong").textContent()) ?? "").trim();
    requests.push(await requestIdByReference(request, ref));
    await page.waitForTimeout(600); // taps right after a confirmation appears are ignored
    await homeLink(page).click();
    await expect(page.getByRole("heading", { name: "Items" })).toBeFocused();
    await expect(region).toHaveCount(0);
  });

  test("a dirty order form asks first; declining keeps it, accepting leaves", async ({ page, request }) => {
    await apply(request, FULL);
    await page.setExtraHTTPHeaders(xff());
    await page.goto("/");
    await page.getByRole("button", { name: new RegExp(ITEM) }).click();
    // A clean form leaves without asking.
    let asked = 0;
    page.on("dialog", (d) => {
      asked++;
      void (asked === 1 ? d.dismiss() : d.accept());
    });
    await homeLink(page).click();
    await expect(page.getByRole("heading", { name: "Items" })).toBeFocused();
    expect(asked).toBe(0);

    await page.getByRole("button", { name: new RegExp(ITEM) }).click();
    await page.getByLabel("Note (optional)").fill("keep me");
    await homeLink(page).click();
    await expect.poll(() => asked).toBe(1);
    await expect(page.getByLabel("Note (optional)")).toHaveValue("keep me");
    await homeLink(page).click();
    await expect.poll(() => asked).toBe(2);
    await expect(page.getByRole("heading", { name: "Items" })).toBeFocused();
  });

  test("on the Closed page it reloads the Closed page", async ({ page, request }) => {
    await apply(request, FULL, { acceptingOrders: false, pausedMessage: "Back Monday" });
    await page.goto("/");
    await expect(page.getByRole("heading", { name: "We're closed right now" })).toBeVisible();
    await page.evaluate(() => ((window as any).__marker = 1));
    await homeLink(page).click();
    await expect(page.getByRole("heading", { name: "We're closed right now" })).toBeVisible();
    expect(await page.evaluate(() => (window as any).__marker)).toBeUndefined();
  });

  test("with the gallery off it goes to the estimate form", async ({ page, request }) => {
    await apply(request, FULL, { gallery: false });
    await page.goto("/#/estimate");
    await homeLink(page).click();
    await expect(page.getByRole("heading", { name: "Custom estimate" })).toBeFocused();
  });

  for (const scheme of ["light", "dark"] as const) {
    test(`hover and focus states, ${scheme}`, async ({ page, request }) => {
      await apply(request, FULL, { footerLinks: LINKS });
      await page.emulateMedia({ colorScheme: scheme });
      await page.setViewportSize({ width: 1280, height: 800 });
      await page.goto("/");
      const link = homeLink(page);
      await link.hover();
      await expect(link.locator(".brand-name")).toHaveCSS("text-decoration-line", "underline");
      if (SHOTS) await page.screenshot({ path: `${SHOTS}/header-hover-${scheme}.png`, clip: { x: 0, y: 0, width: 1280, height: 160 } });
      await page.mouse.move(0, 700);
      await page.keyboard.press("Tab");
      await page.keyboard.press("Tab");
      await link.focus();
      await page.keyboard.press("Shift+Tab");
      await page.keyboard.press("Tab");
      await expect(link).toBeFocused();
      await expect(link).toHaveCSS("outline-style", "solid");
      if (SHOTS) await page.screenshot({ path: `${SHOTS}/header-focus-${scheme}.png`, clip: { x: 0, y: 0, width: 1280, height: 160 } });
      expect(await axe(page)).toEqual([]);
    });
  }
});
