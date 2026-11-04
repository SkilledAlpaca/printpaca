import { test, expect } from "@playwright/test";
import type { APIRequestContext } from "@playwright/test";
import { deflateSync, crc32 } from "node:zlib";
import { requestIdByReference } from "./helpers";

// v1.2 public-shop usability fixes (P1-P10 of the simulated review). Each test fails without its fix.
// Orders use their own X-Forwarded-For address (ADMIN_TRUST_PROXY is on for this server), outside the per-IP
// submission budget the other specs use.

type Json = Record<string, any>;

/** A valid 1x1 PNG of one color. */
function tinyPng(r: number, g: number, b: number): Buffer {
  const chunk = (type: string, data: Buffer): Buffer => {
    const len = Buffer.alloc(4);
    len.writeUInt32BE(data.length);
    const body = Buffer.concat([Buffer.from(type, "ascii"), data]);
    const crc = Buffer.alloc(4);
    crc.writeUInt32BE(crc32(body));
    return Buffer.concat([len, body, crc]);
  };
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(1, 0);
  ihdr.writeUInt32BE(1, 4);
  ihdr[8] = 8;
  ihdr[9] = 2;
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk("IHDR", ihdr),
    chunk("IDAT", deflateSync(Buffer.from([0, r, g, b]))),
    chunk("IEND", Buffer.alloc(0)),
  ]);
}

/** A cube of `size` model units on a side, as ASCII STL. */
function cubeStl(size: number): string {
  const s = size;
  const tris = [
    [[0, 0, 0], [0, s, 0], [s, s, 0]], [[0, 0, 0], [s, s, 0], [s, 0, 0]],
    [[0, 0, s], [s, 0, s], [s, s, s]], [[0, 0, s], [s, s, s], [0, s, s]],
    [[0, 0, 0], [s, 0, 0], [s, 0, s]], [[0, 0, 0], [s, 0, s], [0, 0, s]],
    [[0, s, 0], [0, s, s], [s, s, s]], [[0, s, 0], [s, s, s], [s, s, 0]],
    [[0, 0, 0], [0, 0, s], [0, s, s]], [[0, 0, 0], [0, s, s], [0, s, 0]],
    [[s, 0, 0], [s, s, 0], [s, s, s]], [[s, 0, 0], [s, s, s], [s, 0, s]],
  ];
  const facets = tris
    .map((t) => `  facet normal 0 0 0\n    outer loop\n${t.map((v) => `      vertex ${v[0]} ${v[1]} ${v[2]}`).join("\n")}\n    endloop\n  endfacet`)
    .join("\n");
  return `solid cube\n${facets}\nendsolid cube\n`;
}
const PREFIX = "PubFix";
const WIDGET = `${PREFIX} Widget`;
const TRAY = `${PREFIX} Tray`;
let originalConfig: Json;
const createdItems: string[] = [];
const createdRequests: string[] = [];
let xffCounter = 0;
const xff = () => ({ "X-Forwarded-For": `192.0.2.${100 + (xffCounter++ % 100)}` });

async function getConfig(request: APIRequestContext): Promise<Json> {
  const res = await request.get("/admin/api/settings");
  expect(res.ok()).toBeTruthy();
  return (await res.json()).config as Json;
}
async function putConfig(request: APIRequestContext, config: Json): Promise<void> {
  const res = await request.put("/admin/api/settings", { data: { config } });
  expect(res.ok(), await res.text()).toBeTruthy();
}
async function setShop(request: APIRequestContext, patch: Json, top: Json = {}): Promise<void> {
  const config = await getConfig(request);
  Object.assign(config.shop, patch);
  Object.assign(config, top);
  await putConfig(request, config);
}
async function resetShop(request: APIRequestContext): Promise<void> {
  const config = await getConfig(request);
  config.shop = structuredClone(originalConfig.shop);
  config.quote = structuredClone(originalConfig.quote);
  config.publicQuotes = structuredClone(originalConfig.publicQuotes);
  // The settings save keeps the stored hero file; the delete endpoint is what removes it.
  await request.delete("/admin/api/shop/hero-image");
  config.shop.heroImage = { file: "", alt: "" };
  await putConfig(request, config);
}
async function mkItem(request: APIRequestContext, body: Json): Promise<string> {
  const res = await request.post("/admin/api/items", { data: { public: true, ...body } });
  expect(res.ok(), await res.text()).toBeTruthy();
  const id = (await res.json()).id as string;
  createdItems.push(id);
  return id;
}
async function pollItems(request: APIRequestContext, names: string[]): Promise<void> {
  await expect
    .poll(
      async () => {
        const got = ((await (await request.get("/api/public/items")).json()).items as { name: string }[]).map((i) => i.name);
        return names.every((n) => got.includes(n));
      },
      { timeout: 15_000, intervals: [500, 500, 1000] },
    )
    .toBe(true);
}

test.describe.configure({ mode: "serial" });

test.beforeAll(async ({ request }) => {
  originalConfig = await getConfig(request);
  const withMaterial = structuredClone(originalConfig);
  withMaterial.materials = withMaterial.materials.map((m: Json) => (m.name === "PLA" ? { ...m, public: true } : m));
  await putConfig(request, withMaterial);
  await mkItem(request, { name: WIDGET, description: "A widget.", priceCents: 900, sortOrder: 80 });
  await mkItem(request, {
    name: TRAY,
    description: "A tray.",
    priceCents: 2200,
    sortOrder: 81,
    featured: true,
    optionGroups: [
      {
        label: "Size",
        required: true,
        options: [
          { name: "Small", deltaCents: 0 },
          { name: "Large", deltaCents: 400 },
        ],
      },
    ],
  });
  await pollItems(request, [WIDGET, TRAY]);
});

test.afterAll(async ({ request }) => {
  for (const id of createdRequests) await request.delete(`/admin/api/requests/${id}`);
  for (const id of createdItems) await request.delete(`/admin/api/items/${id}`);
  await putConfig(request, originalConfig);
});

const openItem = async (page: import("@playwright/test").Page, name: string) => {
  await page.goto("/");
  await page.getByRole("button", { name: new RegExp(name) }).click();
};

test.describe("P1: Not you? never appears while typing", () => {
  test("item form: Name, Email, Tab, then typing reaches the Note and keeps both fields", async ({ page }) => {
    await page.setExtraHTTPHeaders(xff());
    await openItem(page, WIDGET);
    await page.getByLabel("Name", { exact: true }).fill("Pat Typist");
    await page.getByLabel("Email").fill("pat@example.com");
    await page.getByLabel("Email").press("Tab");
    await expect(page.getByLabel("Note (optional)")).toBeFocused();
    await page.keyboard.type(" hello");
    await expect(page.getByLabel("Name", { exact: true })).toHaveValue("Pat Typist");
    await expect(page.getByLabel("Email")).toHaveValue("pat@example.com");
    await expect(page.getByLabel("Note (optional)")).toHaveValue(" hello");
    await expect(page.getByRole("button", { name: /Not you/ })).toHaveCount(0);
  });

  test("estimate form: Name, Email, Tab, then typing reaches the Note and keeps both fields", async ({ page }) => {
    await page.setExtraHTTPHeaders(xff());
    await page.goto("/#/estimate");
    await page.getByLabel("Name", { exact: true }).fill("Pat Typist");
    await page.getByLabel("Email").fill("pat@example.com");
    await page.getByLabel("Email").press("Tab");
    await expect(page.getByLabel("Note (optional)")).toBeFocused();
    await page.keyboard.type(" hello");
    await expect(page.getByLabel("Name", { exact: true })).toHaveValue("Pat Typist");
    await expect(page.getByLabel("Email")).toHaveValue("pat@example.com");
    await expect(page.getByLabel("Note (optional)")).toHaveValue(" hello");
    await expect(page.getByRole("button", { name: /Not you/ })).toHaveCount(0);
  });

  test("prefilled details show Not you above Name, at least 24 px tall; clearing focuses Name and Undo restores", async ({ page }) => {
    await page.setExtraHTTPHeaders(xff());
    await page.goto("/#/estimate");
    await page.getByLabel("Name", { exact: true }).fill("Pat Typist");
    await page.getByLabel("Email").fill("pat@example.com");
    // Carry the details to an item form: they arrive prefilled, so the control shows.
    await page.getByRole("button", { name: "Gallery", exact: true }).click();
    await page.getByRole("button", { name: new RegExp(WIDGET) }).click();
    const notYou = page.getByRole("button", { name: "Not you? Clear name and email" });
    await expect(notYou).toBeVisible();
    expect((await notYou.boundingBox())!.height).toBeGreaterThanOrEqual(24);
    // Before Name in the page order.
    const [by, ny] = [(await notYou.boundingBox())!.y, (await page.getByLabel("Name", { exact: true }).boundingBox())!.y];
    expect(by).toBeLessThan(ny);

    await notYou.click();
    await expect(page.getByLabel("Name", { exact: true })).toHaveValue("");
    await expect(page.getByLabel("Email")).toHaveValue("");
    await expect(page.getByLabel("Name", { exact: true })).toBeFocused();
    await page.getByRole("button", { name: "Undo" }).click();
    await expect(page.getByLabel("Name", { exact: true })).toHaveValue("Pat Typist");
    await expect(page.getByLabel("Email")).toHaveValue("pat@example.com");
  });

  test("the estimate form also shows Not you for prefilled details", async ({ page }) => {
    await page.setExtraHTTPHeaders(xff());
    await openItem(page, WIDGET);
    await page.getByLabel("Name", { exact: true }).fill("Pat Typist");
    await page.getByLabel("Email").fill("pat@example.com");
    await expect(page.getByRole("button", { name: /Not you/ })).toHaveCount(0);
    await page.getByRole("button", { name: /Back to gallery/ }).click();
    await page.getByRole("button", { name: "Custom estimate" }).click();
    await expect(page.getByRole("button", { name: "Not you? Clear name and email" })).toBeVisible();
    await expect(page.getByLabel("Name", { exact: true })).toHaveValue("Pat Typist");
  });
});

test.describe("P2: Escape never discards a dirty order form", () => {
  test("a note, a quantity or an option keeps the form open on Escape; a clean form still goes back", async ({ page }) => {
    await page.setExtraHTTPHeaders(xff());
    await openItem(page, TRAY);
    const heading = page.getByRole("heading", { name: TRAY, level: 2 });
    await expect(heading).toBeVisible();
    // Clean: Escape from a button goes back to the gallery.
    await page.getByRole("button", { name: "Place order" }).focus();
    await page.keyboard.press("Escape");
    await expect(page.getByRole("heading", { name: "Items" })).toBeVisible();

    await page.getByRole("button", { name: new RegExp(TRAY) }).click();
    await page.getByLabel("Size (required)").selectOption({ label: "Large (+$4.00)" });
    await page.getByLabel("Quantity").fill("3");
    await page.getByLabel("Note (optional)").fill("keep me");
    await page.getByRole("button", { name: "Place order" }).focus();
    await page.keyboard.press("Escape");
    await expect(heading).toBeVisible();
    await expect(page.getByLabel("Size (required)")).toHaveValue(/.+/);
    await expect(page.getByLabel("Quantity")).toHaveValue("3");
    await expect(page.getByLabel("Note (optional)")).toHaveValue("keep me");
    // The Back button still leaves on purpose.
    await page.getByRole("button", { name: /Back to gallery/ }).click();
    await expect(page.getByRole("heading", { name: "Items" })).toBeVisible();
  });

  test("a mouse-and-keyboard shop shows the Escape hint; a phone does not", async ({ page, browser }) => {
    await page.setExtraHTTPHeaders(xff());
    await openItem(page, WIDGET);
    await expect(page.getByText("Press Esc to go back.")).toBeVisible();
    const ctx = await browser.newContext({ hasTouch: true, isMobile: true, viewport: { width: 390, height: 844 }, extraHTTPHeaders: xff() });
    const phone = await ctx.newPage();
    await phone.goto("http://127.0.0.1:8099/");
    await phone.getByRole("button", { name: new RegExp(WIDGET) }).click();
    await expect(phone.getByRole("heading", { name: WIDGET, level: 2 })).toBeVisible();
    await expect(phone.getByText("Press Esc to go back.")).toBeHidden();
    await ctx.close();
  });
});

async function placeWidgetOrder(page: import("@playwright/test").Page, request: APIRequestContext, who: string): Promise<string> {
  await openItem(page, WIDGET);
  await page.getByLabel("Name", { exact: true }).fill(`${PREFIX} ${who}`);
  await page.getByLabel("Email").fill("pubfix@example.com");
  await page.getByRole("button", { name: "Place order" }).click();
  const region = page.getByRole("region", { name: "Order received" });
  await expect(region).toBeFocused();
  const ref = ((await region.locator(".ref strong").textContent()) ?? "").trim();
  createdRequests.push(await requestIdByReference(request, ref));
  return ref;
}

test.describe("P3/P5: the order confirmation", () => {
  test("a double tap where Place order was keeps the confirmation; Copy reference copies it", async ({ page, request, context }) => {
    await context.grantPermissions(["clipboard-read", "clipboard-write"]);
    await page.setExtraHTTPHeaders(xff());
    await openItem(page, WIDGET);
    await page.getByLabel("Name", { exact: true }).fill(`${PREFIX} Tap`);
    await page.getByLabel("Email").fill("pubfix@example.com");
    const submit = page.getByRole("button", { name: "Place order" });
    const box = (await submit.boundingBox())!;
    const x = box.x + box.width / 2;
    const y = box.y + box.height / 2;
    await submit.click();
    const region = page.getByRole("region", { name: "Order received" });
    await expect(region).toBeVisible();
    // The second tap of a double tap, at the same screen position, right after the confirmation shows.
    await page.mouse.click(x, y);
    await page.mouse.click(x, y);
    await expect(region).toBeVisible();
    await expect(page).toHaveURL(/#\/order\/received$/);
    const ref = ((await region.locator(".ref strong").textContent()) ?? "").trim();
    createdRequests.push(await requestIdByReference(request, ref));
    // Even a click straight on the navigation links is ignored in the first moments.
    await page.getByRole("button", { name: "Back to gallery" }).dispatchEvent("click");
    await expect(region).toBeVisible();
    // No primary "Order another item" button remains.
    await expect(page.getByRole("button", { name: "Order another item" })).toHaveCount(0);

    await page.getByRole("button", { name: "Copy reference" }).click();
    expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(ref);
    // After the moment passes the confirmation reload still works and the links act.
    await page.reload();
    await expect(region).toBeVisible();
    await expect(page.locator(".confirmation .ref strong")).toHaveText(ref);
  });

  test("Back to <item> focuses that item's card; Back to gallery follows the heading in DOM order", async ({ page, request }) => {
    await page.setExtraHTTPHeaders(xff());
    await placeWidgetOrder(page, request, "Back");
    const order = await page.evaluate(() => {
      const pos = (el: Element | null) => (el ? Array.from(document.querySelectorAll("*")).indexOf(el) : -1);
      return {
        heading: pos(document.getElementById("item-conf-heading")),
        gallery: pos(Array.from(document.querySelectorAll("button")).find((b) => b.textContent?.includes("Back to gallery")) ?? null),
        summary: pos(document.querySelector(".order-summary")),
      };
    });
    expect(order.heading).toBeLessThan(order.gallery);
    expect(order.gallery).toBeLessThan(order.summary);
    await page.waitForTimeout(600);
    await page.getByRole("button", { name: `Back to ${WIDGET}` }).click();
    await expect(page.getByRole("button", { name: new RegExp(WIDGET) })).toBeFocused();
    await expect(page.getByRole("heading", { name: "Items" })).toBeVisible();
  });

  test("at 200% zoom the amount and reference are in view and the card is at the top", async ({ browser, request }) => {
    const ctx = await browser.newContext({ viewport: { width: 683, height: 384 }, deviceScaleFactor: 2, extraHTTPHeaders: xff() });
    const page = await ctx.newPage();
    await page.goto("http://127.0.0.1:8099/");
    await page.getByRole("button", { name: new RegExp(WIDGET) }).click();
    await page.getByLabel("Name", { exact: true }).fill(`${PREFIX} Zoom`);
    await page.getByLabel("Email").fill("pubfix@example.com");
    await page.getByRole("button", { name: "Place order" }).click();
    const region = page.getByRole("region", { name: "Order received" });
    await expect(region).toBeFocused();
    createdRequests.push(await requestIdByReference(request, ((await region.locator(".ref strong").textContent()) ?? "").trim()));
    await page.waitForTimeout(300);
    const vh = 384;
    for (const sel of [".confirmation .price", ".confirmation .ref strong"]) {
      const b = (await page.locator(sel).boundingBox())!;
      expect(b.y, sel).toBeGreaterThanOrEqual(0);
      expect(b.y + b.height, sel).toBeLessThanOrEqual(vh);
    }
    const card = (await page.locator(".confirmation-card").boundingBox())!;
    expect(card.y).toBeLessThan(40);
    await ctx.close();
  });
});

test.describe("P4: a submit refused because the shop closed", () => {
  test("the Closed page says the order wasn't placed, focuses the notice, and the details return after reopening", async ({ page, request }) => {
    await page.setExtraHTTPHeaders(xff());
    try {
      await openItem(page, TRAY);
      await page.getByLabel("Size (required)").selectOption({ label: "Large (+$4.00)" });
      await page.getByLabel("Quantity").fill("2");
      await page.getByLabel("Name", { exact: true }).fill(`${PREFIX} Closing`);
      await page.getByLabel("Email").fill("closing@example.com");
      await page.getByLabel("Note (optional)").fill("please hurry");
      await setShop(request, { acceptingOrders: false, pausedMessage: "Back Monday" });
      await page.getByRole("button", { name: "Place order" }).click();
      await expect(page.getByRole("heading", { level: 1, name: "We're closed right now" })).toBeVisible();
      const notice = page.locator("#closed-notice");
      await expect(notice).toContainText("Your order wasn't placed. The shop closed while you were ordering.");
      await expect(notice).toBeFocused();
      await expect(page.getByText("Back Monday")).toBeVisible();

      // The shop reopens; a reload brings the form back with everything typed.
      await resetShop(request);
      await page.reload();
      await expect(page.getByRole("heading", { name: TRAY, level: 2 })).toBeVisible();
      await expect(page.getByLabel("Size (required)")).toHaveValue(/.+/);
      await expect(page.getByLabel("Quantity")).toHaveValue("2");
      await expect(page.getByLabel("Name", { exact: true })).toHaveValue(`${PREFIX} Closing`);
      await expect(page.getByLabel("Email")).toHaveValue("closing@example.com");
      await expect(page.getByLabel("Note (optional)")).toHaveValue("please hurry");
      await expect(page.locator("#closed-notice")).toHaveCount(0);
    } finally {
      await resetShop(request);
    }
  });

  test("a refused estimate request says so and keeps its details", async ({ page, request }) => {
    await page.setExtraHTTPHeaders(xff());
    try {
      await page.goto("/#/estimate");
      await page.getByLabel("Material").selectOption("PLA");
      await page.getByLabel(/Model weight/).fill("40");
      await page.getByLabel(/^Print time \(h/).fill("1");
      await page.getByLabel(/Print time \(minutes\)/).fill("10");
      await page.getByLabel("Name", { exact: true }).fill(`${PREFIX} Est`);
      await page.getByLabel("Email").fill("est@example.com");
      await page.getByLabel("Note (optional)").fill("est note");
      await expect(page.locator(".estimate .price")).toContainText("$");
      await setShop(request, { acceptingOrders: false, pausedMessage: "Back Monday" });
      await page.getByRole("button", { name: "Request an estimate" }).click();
      await expect(page.locator("#closed-notice")).toContainText("Your estimate request wasn't sent");
      await expect(page.locator("#closed-notice")).toBeFocused();
      await resetShop(request);
      await page.reload();
      await expect(page.getByLabel("Name", { exact: true })).toHaveValue(`${PREFIX} Est`);
      await expect(page.getByLabel("Note (optional)")).toHaveValue("est note");
      await expect(page.getByLabel("Material")).toHaveValue("PLA");
    } finally {
      await resetShop(request);
    }
  });
});

test.describe("P6: the total itemizes option surcharges", () => {
  test("from price while a required option is unchosen, then the base plus the surcharge", async ({ page }) => {
    await page.setExtraHTTPHeaders(xff());
    await openItem(page, TRAY);
    const total = page.locator("#item-total-label + div");
    await expect(total).toContainText("from $22.00");
    await expect(page.getByText("Choose Size to see the exact total.")).toBeVisible();
    await page.getByLabel("Size (required)").selectOption({ label: "Large (+$4.00)" });
    await expect(total).toContainText("$26.00");
    await expect(total).not.toContainText("from");
    await expect(page.getByText("$22.00 + Large $4.00")).toBeVisible();
    await page.getByLabel("Quantity").fill("2");
    await expect(total).toContainText("$52.00");
    await expect(total).toContainText("$26.00 each");
    await page.getByLabel("Size (required)").selectOption({ label: "Small" });
    await expect(total).toContainText("$44.00");
    // No surcharge, nothing to itemize.
    await expect(page.locator(".breakdown")).toHaveCount(0);
  });
});

test.describe("P7: a phone's first screen shows an item", () => {
  test("with a hero, announcement, heading and long intro, the first card starts in the upper part of the screen; an item page drops the pitch", async ({ browser, request }) => {
    const intro = "Everything here is printed to order on our own machines, finished by hand, and packed with care. Ask us about custom colors, sizes, and anything you can imagine.";
    const config = await getConfig(request);
    config.publicQuotes = { ...config.publicQuotes, introText: intro };
    config.shop = { ...config.shop, announcement: "Spring sale: free shipping on orders over $40 until the end of the month.", heading: "Handmade 3D prints" };
    await putConfig(request, config);
    expect((await request.post("/admin/api/shop/hero-image?alt=Shelves%20of%20prints", { data: tinyPng(12, 34, 56), headers: { "Content-Type": "image/png" } })).ok()).toBeTruthy();
    const ctx = await browser.newContext({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true, extraHTTPHeaders: xff() });
    const page = await ctx.newPage();
    try {
      await page.goto("http://127.0.0.1:8099/");
      await expect(page.getByRole("img", { name: "Shelves of prints" })).toBeVisible();
      const card = page.locator("[data-item-id]").first();
      await expect(card).toBeVisible();
      const top = (await card.boundingBox())!.y;
      // The review measured 565 px with this kind of header; now a card shows well within the first screen (about 460 px).
      expect(top).toBeLessThan(844 * 0.6);

      await page.getByRole("button", { name: new RegExp(TRAY) }).click();
      await expect(page.getByRole("heading", { name: TRAY, level: 2 })).toBeVisible();
      await expect(page.locator(".intro")).toHaveCount(0);
      await expect(page.locator(".shop-hero")).toHaveCount(0);
      await expect(page.locator(".shop-heading")).toHaveCount(0);
      // The announcement and the Gallery / Custom estimate switch stay; the options start on the first screen.
      await expect(page.locator(".shop-announcement")).toBeVisible();
      expect((await page.getByLabel("Size (required)").boundingBox())!.y).toBeLessThan(844);
    } finally {
      await ctx.close();
      await resetShop(request);
    }
  });
});

test.describe("P8: the estimate form tells a buyer without a slicer what is optional", () => {
  test("the upload sits above Material and the weight and print time say they are optional with a file", async ({ page }) => {
    await page.setExtraHTTPHeaders(xff());
    await page.goto("/#/estimate");
    const upload = (await page.getByRole("button", { name: "Choose file" }).boundingBox())!.y;
    const material = (await page.getByLabel("Material").boundingBox())!.y;
    expect(upload).toBeLessThan(material);
    await expect(page.getByLabel(/Model weight/)).toHaveAccessibleDescription(/Optional if you upload a 3D file above/);
    await expect(page.locator("#quote-time-hint")).toContainText("Optional if you upload a 3D file above");
    await expect(page.getByLabel(/^Print time \(h/)).toHaveAccessibleDescription(/Optional if you upload/);
  });
});

test.describe("P9: small polish", () => {
  test("featured gallery cards say Featured", async ({ page }) => {
    await page.setExtraHTTPHeaders(xff());
    await page.goto("/");
    await expect(page.locator(`[data-item-id]:has-text("${TRAY}") .featured-badge`)).toHaveText("Featured");
    await expect(page.locator(`[data-item-id]:has-text("${WIDGET}") .featured-badge`)).toHaveCount(0);
  });

  test("the estimate heading takes focus when opened from the custom card", async ({ page }) => {
    await page.setExtraHTTPHeaders(xff());
    await page.goto("/");
    await page.getByRole("button", { name: /Need something custom/ }).click();
    await expect(page.getByRole("heading", { name: "Custom estimate", level: 2 })).toBeFocused();
  });

  test("Solidity has a label and the file input a short name", async ({ page }) => {
    await page.setExtraHTTPHeaders(xff());
    await page.goto("/#/estimate");
    await page.getByLabel("Material").selectOption("PLA");
    await expect(page.locator("#public-gcode-file")).toHaveAccessibleName("Upload your 3D file (STL, 3MF, or G-code)");
    await page.setInputFiles("#public-gcode-file", { name: "cube.stl", mimeType: "model/stl", buffer: Buffer.from(cubeStl(20), "utf-8") });
    await page.getByText("How we estimated this").click();
    await expect(page.getByLabel("Solidity (%)")).toBeVisible();
  });

  test("focused cards and fields keep a scroll margin from the viewport edge", async ({ page }) => {
    await page.setExtraHTTPHeaders(xff());
    await page.setViewportSize({ width: 1366, height: 768 });
    await page.goto("/");
    const margin = async (sel: string) => page.locator(sel).first().evaluate((el) => parseFloat(getComputedStyle(el).scrollMarginBottom));
    expect(await margin("[data-item-id]")).toBeGreaterThanOrEqual(8);
    await page.getByRole("button", { name: new RegExp(WIDGET) }).click();
    expect(await margin("#item-note")).toBeGreaterThanOrEqual(8);
    await page.goto("/#/estimate");
    expect(await margin("#quote-note")).toBeGreaterThanOrEqual(8);
  });
});

test.describe("P10: the default intro matches what the shop offers", () => {
  test("with custom estimates off, the built-in intro does not mention uploading a model; an owner's intro is untouched", async ({ page, request }) => {
    await page.setExtraHTTPHeaders(xff());
    try {
      await page.goto("/");
      await expect(page.locator(".intro")).toContainText("upload your own 3D model");
      await setShop(request, { customEstimates: false });
      await page.reload();
      await expect(page.locator(".intro")).toHaveText("Order a ready-made 3D print.");
      const config = await getConfig(request);
      config.publicQuotes = { ...config.publicQuotes, introText: "Upload your STL and we will print it." };
      await putConfig(request, config);
      await page.reload();
      await expect(page.locator(".intro")).toHaveText("Upload your STL and we will print it.");
    } finally {
      await resetShop(request);
    }
  });
});
