import { test, expect } from "@playwright/test";
import type { APIRequestContext } from "@playwright/test";
import { gotoSettings, getConfig } from "./helpers";
import type { Json } from "./helpers";

// The shop intro and announcement accept light markdown. The Settings preview and the shop render the same
// token tree as markup (never as HTML), and the server-rendered <meta name="description"> carries plain text.

const INTRO = [
  "Welcome to **our shop** with *careful* prints, see [our policy](https://example.com/policy) or [mail us](mailto:hi@example.com).",
  "Second line of the same paragraph.",
  "",
  "- first bullet",
  "- second **bold** bullet",
  "",
  "Hostile: <script>window.__pwned = 1</script> and [click me](javascript:window.__pwned=2) and [data](data:text/html,x) &amp; snake_case_name",
].join("\n");
const ANNOUNCEMENT = "Closed **Dec 24** to *Jan 2*. [Details](https://example.com/hours)";

let original: Json;

test.describe.configure({ mode: "serial" });

test.beforeAll(async ({ request }) => {
  original = await getConfig(request);
});

test.afterAll(async ({ request }) => {
  const config = await getConfig(request);
  config.shop = structuredClone(original.shop);
  config.publicQuotes = structuredClone(original.publicQuotes);
  const res = await request.put("/admin/api/settings", { data: { config } });
  expect(res.ok(), await res.text()).toBeTruthy();
});

async function expectRendered(root: import("@playwright/test").Locator, kind: "intro" | "announcement"): Promise<void> {
  await expect(root.locator("strong").first()).toHaveText(kind === "intro" ? "our shop" : "Dec 24");
  await expect(root.locator("em").first()).toHaveText(kind === "intro" ? "careful" : "Jan 2");
  const https = root.locator('a[href^="https://example.com/"]').first();
  await expect(https).toHaveAttribute("target", "_blank");
  await expect(https).toHaveAttribute("rel", "noopener noreferrer");
  if (kind === "intro") {
    await expect(root.locator('a[href="mailto:hi@example.com"]')).not.toHaveAttribute("target", /.+/);
    await expect(root.locator(".rt-p")).toHaveCount(2);
    await expect(root.locator(".rt-p").first().locator("br")).toHaveCount(1);
    await expect(root.locator("ul > li")).toHaveCount(2);
    // Hostile text shows as literal text: no script element, and no link for javascript: or data:.
    await expect(root.locator("script")).toHaveCount(0);
    await expect(root.locator('a[href^="javascript"], a[href^="data"]')).toHaveCount(0);
    await expect(root).toContainText("<script>window.__pwned = 1</script>");
    await expect(root).toContainText("[click me](javascript:window.__pwned=2)");
    await expect(root).toContainText("&amp; snake_case_name");
  }
}

test("Settings previews the markup, and the shop and its meta description follow after a save", async ({ page, request }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await gotoSettings(page);
  const card = page.locator("#section-storefront");
  await card.getByLabel("Intro text").fill(INTRO);
  await card.getByLabel("Announcement").fill(ANNOUNCEMENT);
  await expectRendered(card.getByTestId("intro-preview"), "intro");
  await expectRendered(card.getByTestId("announcement-preview"), "announcement");
  await page.keyboard.press("Control+s");
  await expect(page.getByText("Settings saved.")).toBeVisible();

  // The shop renders the same markup.
  await page.goto("/");
  await expectRendered(page.locator(".intro"), "intro");
  await expectRendered(page.locator(".shop-announcement"), "announcement");
  expect(await page.evaluate(() => (window as any).__pwned)).toBeUndefined();
  expect(errors).toEqual([]);

  // The first response carries the plain-text description: markup gone, text escaped.
  const raw = await (await request.get("/")).text();
  const meta = /<meta name="description" content="([^"]*)">/.exec(raw);
  expect(meta).not.toBeNull();
  expect(meta![1]).toBe(
    "Welcome to our shop with careful prints, see our policy or mail us. Second line of the same paragraph. first bullet second bold bullet " +
      "Hostile: &lt;script&gt;window.__pwned = 1&lt;/script&gt; and [click me](javascript:window.__pwned=2) and [data](data:text/html,x) &amp;amp; snake_case_name",
  );
  expect(meta![1]).not.toContain("**");
});
