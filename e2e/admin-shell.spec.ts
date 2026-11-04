import { test, expect } from "@playwright/test";
import { deflateSync, crc32 } from "node:zlib";
import { navButton } from "./helpers";

// Admin shell: sidebar fit, item editor wording and concurrency, page shortcuts, materials, electricity
// schedule, logo upload, and the Guide. Runs on the shared :8099 server and removes what it creates.

test.describe.configure({ mode: "serial" });

const createdItemIds: string[] = [];

test.afterAll(async ({ request }) => {
  for (const id of createdItemIds) await request.delete(`/admin/api/items/${id}`);
});

test("the sidebar fits at 1366x768 with Settings above the footer, and groups read Sell and Costs", async ({ page }) => {
  await page.setViewportSize({ width: 1366, height: 768 });
  await page.goto("/admin/");
  const nav = page.getByRole("navigation", { name: "Sections" });
  await expect(nav.getByText("Costs", { exact: true })).toBeVisible();
  await expect(nav.getByText("Shop", { exact: true })).toHaveCount(0);
  const settings = navButton(page, "Settings");
  await expect(settings).toBeVisible();
  const settingsBox = await settings.boundingBox();
  const footerBox = await page.locator(".sidebar-footer").boundingBox();
  expect(settingsBox).not.toBeNull();
  expect(footerBox).not.toBeNull();
  expect(settingsBox!.y + settingsBox!.height).toBeLessThanOrEqual(footerBox!.y + 1);
  // Shop items sit under Sell, with the other things you sell.
  const sell = nav.getByRole("group", { name: "Sell" });
  await expect(sell.getByRole("button", { name: /^Shop items/ })).toBeVisible();
});

test("the hamburger shows a visible Menu label on a phone", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/admin/");
  const burger = page.getByRole("button", { name: /^Open menu/ });
  await expect(burger).toContainText("Menu");
});

test("item editor: group wording, hidden-on-save notice with Show in shop, and a visible Saved status", async ({ page, request }) => {
  await page.goto("/admin/#/items");
  await expect(page.getByRole("heading", { name: "Shop items", level: 1 })).toBeVisible();
  await page.getByRole("button", { name: "Add shop item" }).click();
  await page.getByLabel("Name", { exact: true }).fill("Shell Test Item");
  await page.getByRole("button", { name: "Add group" }).click();
  await expect(page.getByLabel("Group name")).toBeVisible();
  const required = page.getByLabel("Customer must choose one");
  await expect(required).toBeChecked();
  await expect(page.getByLabel("Group 1 choice 1", { exact: true })).toBeVisible();
  await page.getByLabel("Group name").fill("Color");
  await page.getByLabel("Group 1 choice 1", { exact: true }).fill("Red");

  await page.getByRole("button", { name: "Save", exact: true }).click();
  const status = page.getByTestId("item-saved");
  await expect(status).toContainText("Saved. Hidden from the shop.");
  const list = await (await request.get("/admin/api/items")).json();
  const saved = (list.items as { id: string; name: string; public: boolean }[]).find((i) => i.name === "Shell Test Item");
  expect(saved).toBeTruthy();
  createdItemIds.push(saved!.id);
  expect(saved!.public).toBe(false);

  await status.getByRole("button", { name: "Show in shop" }).click();
  await expect(status).toContainText("Saved.");
  await expect(status).not.toContainText("Hidden");
  const after = await (await request.get("/admin/api/items")).json();
  expect((after.items as { id: string; public: boolean }[]).find((i) => i.id === saved!.id)?.public).toBe(true);
});

test("item editor: Ctrl+S saves and the list filter takes Ctrl+K", async ({ page, request }) => {
  await page.goto("/admin/#/items");
  await expect(page.locator("main .shortcuts-line")).toContainText("Ctrl+K filter");
  await page.keyboard.press("Control+k");
  await expect(page.locator("#items-filter")).toBeFocused();
  await page.locator("#items-filter").fill("shell test");
  await expect(page.locator("button.row-open", { hasText: "Shell Test Item" })).toBeVisible();
  await page.locator("#items-filter").fill("zzzz-no-match");
  await expect(page.getByText("No shop items match that filter.")).toBeVisible();
  await page.locator("#items-filter").fill("shell test");

  await page.locator("button.row-open", { hasText: "Shell Test Item" }).click();
  await expect(page.locator("main .shortcuts-line")).toContainText("Ctrl+S save");
  await page.getByLabel("Description").fill("Saved by shortcut");
  await page.getByLabel("Description").press("Control+s");
  await expect(page.getByTestId("item-saved")).toContainText("Saved.");
  const list = await (await request.get("/admin/api/items")).json();
  expect((list.items as { name: string; description: string }[]).find((i) => i.name === "Shell Test Item")?.description).toBe("Saved by shortcut");
});

test("item editor: a stale save in a second tab offers Reload or Overwrite", async ({ browser }) => {
  const ctx = await browser.newContext();
  const a = await ctx.newPage();
  const b = await ctx.newPage();
  for (const p of [a, b]) {
    await p.goto("/admin/#/items");
    await p.locator("button.row-open", { hasText: "Shell Test Item" }).click();
  }
  await a.getByLabel("Description").fill("From tab A");
  await a.getByRole("button", { name: "Save", exact: true }).click();
  await expect(a.getByTestId("item-saved")).toContainText("Saved.");

  await b.getByLabel("Description").fill("From tab B");
  await b.getByRole("button", { name: "Save", exact: true }).click();
  const conflict = b.getByRole("alert").filter({ hasText: "changed in another tab or by another user" });
  await expect(conflict).toBeVisible();
  // Reload discards B's edit and shows A's.
  await conflict.getByRole("button", { name: "Reload" }).click();
  await expect(b.getByLabel("Description")).toHaveValue("From tab A");

  // Make B stale again, then Overwrite keeps B's text.
  await a.getByLabel("Description").fill("From tab A again");
  await a.getByRole("button", { name: "Save", exact: true }).click();
  await expect(a.getByTestId("item-saved")).toContainText("Saved.");
  await b.getByLabel("Description").fill("From tab B again");
  await b.getByRole("button", { name: "Save", exact: true }).click();
  await expect(conflict).toBeVisible();
  await conflict.getByRole("button", { name: "Overwrite" }).click();
  await expect(b.getByTestId("item-saved")).toContainText("Saved.");
  await ctx.close();
});

test("the list's Public toggle states what happened and offers Undo", async ({ page, request }) => {
  await page.goto("/admin/#/items");
  const row = page.locator("tr.item-row", { hasText: "Shell Test Item" });
  await row.getByRole("checkbox", { name: /^Public/ }).uncheck();
  await expect(page.locator(".notice", { hasText: "is now hidden from the shop" })).toBeVisible();
  await page.getByRole("button", { name: "Undo" }).click();
  await expect(row.getByRole("checkbox", { name: /^Public/ })).toBeChecked();
  const list = await (await request.get("/admin/api/items")).json();
  expect((list.items as { name: string; public: boolean }[]).find((i) => i.name === "Shell Test Item")?.public).toBe(true);
});

test("Ctrl+S and Ctrl+K are offered only where they work", async ({ page }) => {
  const hint = page.locator("main .shortcuts-line");
  await page.goto("/admin/#/dashboard");
  await expect(page.getByRole("heading", { level: 1 }).first()).toBeVisible();
  await expect(hint).toHaveCount(0);
  await page.goto("/admin/#/electricity");
  await expect(hint).toContainText("Ctrl+S save");
  await expect(hint).not.toContainText("Ctrl+K");
  await page.goto("/admin/#/settings");
  await expect(hint).toContainText("Ctrl+S save");
});

test("Settings saves with Ctrl+S", async ({ page, request }) => {
  const original = (await (await request.get("/admin/api/settings")).json()).config;
  try {
    await page.goto("/admin/#/settings");
    const phone = page.getByLabel("Phone", { exact: true });
    await phone.fill("555-0100");
    await phone.press("Control+s");
    await expect(page.getByText("Settings saved.")).toBeVisible();
  } finally {
    await request.put("/admin/api/settings", { data: { config: original } });
  }
});

test("filament materials are read-only until Edit, and ID and density sit behind the advanced toggle", async ({ page }) => {
  await page.goto("/admin/#/filament");
  await expect(page.getByRole("heading", { name: "Materials" })).toBeVisible();
  await expect(page.getByLabel("Material 1 price per kg")).toHaveCount(0);
  await expect(page.getByLabel("Material 1 id")).toHaveCount(0);
  await expect(page.getByRole("columnheader", { name: "ID" })).toHaveCount(0);
  await page.getByRole("button", { name: /^Edit / }).first().click();
  await expect(page.getByLabel("Material 1 price per kg")).toBeVisible();
  await expect(page.getByLabel("Material 1 density")).toHaveCount(0);
  await page.getByRole("button", { name: /^Done editing/ }).first().click();
  await expect(page.getByLabel("Material 1 price per kg")).toHaveCount(0);
  await page.getByLabel("Show ID and density (advanced)").check();
  await expect(page.getByRole("columnheader", { name: "ID" })).toBeVisible();
  await expect(page.getByRole("columnheader", { name: /^Density/ })).toBeVisible();
});

test.describe("electricity schedule", () => {
  let original: unknown;
  test.beforeAll(async ({ request }) => {
    original = (await (await request.get("/admin/api/settings")).json()).config;
  });
  test.afterAll(async ({ request }) => {
    await request.put("/admin/api/settings", { data: { config: original } });
  });

  test("problems are written in UI terms, fields are marked, and Save is blocked until fixed", async ({ page }) => {
    await page.goto("/admin/#/electricity");
    await page.getByLabel("Mode").selectOption("schedule");
    await page.getByRole("button", { name: "Load SCE TOU-D-4-9PM example" }).click();
    const start = page.getByLabel("Season 1 Weekday window 1 start");
    // A time input can't hold "4pm"; an emptied one is the invalid state the editor can reach.
    await start.fill("");
    const problems = page.getByRole("list", { name: "Schedule problems" });
    await expect(problems).toContainText("Season 1 › Weekday › Window 1");
    await expect(problems).toContainText("enter a time like 16:00");
    await expect(problems).not.toContainText("seasons[0]");
    await expect(start).toHaveAttribute("aria-invalid", "true");
    const save = page.getByRole("button", { name: "Save changes" });
    await expect(save).toHaveAttribute("aria-disabled", "true");
    // aria-disabled buttons fail Playwright's enabled check, so force the click: it must still do nothing.
    await save.click({ force: true });
    await expect(page.getByText("Settings saved.")).toHaveCount(0);
    await start.fill("16:00");
    await expect(problems).toHaveCount(0);
    await expect(save).not.toHaveAttribute("aria-disabled", "true");
  });

  test("an overnight window is accepted, explained, and split at midnight on save", async ({ page, request }) => {
    await page.goto("/admin/#/electricity");
    await page.getByLabel("Mode").selectOption("schedule");
    await page.getByRole("button", { name: "Load SCE TOU-D-4-9PM example" }).click();
    await page.getByLabel("Season 1 Weekday window 1 start").fill("22:00");
    await page.getByLabel("Season 1 Weekday window 1 end").fill("06:00");
    await expect(page.getByRole("list", { name: "Schedule problems" })).toHaveCount(0);
    await expect(page.locator("#sched-overnight")).toContainText("split into two windows");
    await page.getByRole("button", { name: "Save changes" }).click();
    await expect(page.getByText(/Settings saved\. A window that ran past midnight was split at midnight\./)).toBeVisible();
    const cfg = (await (await request.get("/admin/api/settings")).json()).config as {
      energy: { schedule: { seasons: { weekday: { windows: { start: string; end: string }[] } }[] } };
    };
    const wins = cfg.energy.schedule.seasons[0]!.weekday.windows.map((w) => `${w.start}-${w.end}`);
    expect(wins).toEqual(expect.arrayContaining(["22:00-24:00", "00:00-06:00"]));
  });

  test("at 390 px a schedule window's fields and its Remove button stack inside the card", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto("/admin/#/electricity");
    const rate = page.getByLabel("Season 1 Weekday window 1 rate");
    await rate.scrollIntoViewIfNeeded();
    const box = await rate.boundingBox();
    expect(box).not.toBeNull();
    expect(box!.x + box!.width).toBeLessThanOrEqual(390);
    const remove = page.getByRole("button", { name: /^Remove Season 1 Weekday window 1/ });
    const rb = await remove.boundingBox();
    expect(rb!.x + rb!.width).toBeLessThanOrEqual(390);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  });
});

function tinyPng(): Buffer {
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
    chunk("IDAT", deflateSync(Buffer.from([0, 10, 120, 200]))),
    chunk("IEND", Buffer.alloc(0)),
  ]);
}

test("business identity: upload a logo, see the preview, save, and remove it", async ({ page, request }) => {
  const original = (await (await request.get("/admin/api/settings")).json()).config;
  try {
    await page.goto("/admin/#/settings");
    await expect(page.locator("#section-quote .hint").first()).toContainText("admin header");
    await page.getByLabel("Upload logo").setInputFiles({ name: "logo.png", mimeType: "image/png", buffer: tinyPng() });
    await expect(page.getByAltText("Your logo")).toBeVisible();
    await expect(page.getByText(/^Uploaded image, \d+ KB$/)).toBeVisible();
    await expect(page.locator("#quote-logo-url")).toHaveCount(0);
    await page.getByRole("button", { name: "Save changes" }).click();
    await expect(page.getByText("Settings saved.")).toBeVisible();
    const cfg = (await (await request.get("/admin/api/settings")).json()).config as { quote: { logoUrl: string } };
    expect(cfg.quote.logoUrl).toMatch(/^data:image\/png;base64,/);
    await page.getByRole("button", { name: "Remove logo" }).click();
    await expect(page.locator("#quote-logo-url")).toBeVisible();
    await expect(page.locator("#quote-logo-url")).toHaveValue("");
  } finally {
    await request.put("/admin/api/settings", { data: { config: original } });
  }
});

test("the Guide covers requests, shop items, Spoolman, users, and shortcuts", async ({ page }) => {
  await page.goto("/admin/#/guide");
  for (const name of ["Requests: Finalize or Convert", "Shop items and options", "Spoolman", "Users and permissions", "Keyboard shortcuts"]) {
    await expect(page.getByRole("heading", { name })).toBeVisible();
  }
  await expect(page.getByRole("table")).toContainText("Ctrl+S");
});

test("Explain this page scrolls the first note into view", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 500 });
  await page.goto("/admin/#/quote");
  await page.getByRole("button", { name: "Explain this page" }).click();
  const note = page.locator(".help-note:not([hidden])").first();
  await expect(note).toBeVisible();
  await expect.poll(async () => note.evaluate((el) => {
    const r = el.getBoundingClientRect();
    return r.top >= 0 && r.bottom <= window.innerHeight;
  })).toBe(true);
});

test("a dirty item form asks before the page unloads", async ({ page }) => {
  await page.goto("/admin/#/items");
  await page.locator("button.row-open", { hasText: "Shell Test Item" }).click();
  await page.getByLabel("Description").fill("unsaved edit");
  // The handler cancels the event; assert through the same path the browser uses.
  const prevented = await page.evaluate(() => {
    const e = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(e);
    return e.defaultPrevented;
  });
  expect(prevented).toBe(true);
});

test("#/quote/<id> resolves to the New quote tab, and an unknown id falls back to a blank quote", async ({ page }) => {
  await page.goto("/admin/#/quote/abc123");
  await expect(page.getByRole("heading", { name: "New quote", level: 1 })).toBeVisible();
  // No saved quote has that id, so the editor stays blank and the address drops the stale id (quote-editor lane).
  await expect(page).toHaveURL(/#\/quote$/);
});

test("Integrations Test works for a URL set by an environment variable", async ({ page }) => {
  // :9199 sets SPOOLMAN_URL and MOONRAKER_URL (and serves stubs for both).
  await page.goto("http://127.0.0.1:9199/admin/#/settings");
  const card = page.locator("#section-integrationsUrls");
  await expect(card.getByText("SPOOLMAN_URL is set on the container")).toBeVisible();
  const test = card.locator(".card", { hasText: "Spoolman" }).getByRole("button", { name: "Test" });
  await expect(test).not.toBeDisabled();
  await expect(test).toHaveAttribute("aria-disabled", "false");
  await test.click();
  await expect(card.getByText(/^Connected\./).first()).toBeVisible();
});
