import { test, expect } from "@playwright/test";
import type { Locator, Page } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { seedQuoteBody } from "./helpers";

// Release candidate tidy: D57 sidebar footer line (the integration badges are gone), D49 the Quotes email
// notice never overlaps content, D50 fixed-slot Quotes row actions.
// 8099 has no SMTP, so the email notice shows.

const TAGS = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"];
const INTEGRATIONS_URL = "http://127.0.0.1:9199/admin/";
const sidebar = (page: Page) => page.getByRole("complementary");

async function axeClean(page: Page, label: string): Promise<void> {
  await page.waitForLoadState("networkidle");
  await page.evaluate(() => document.fonts.ready);
  const results = await new AxeBuilder({ page }).withTags(TAGS).analyze();
  expect(results.violations.map((v) => `${label}: ${v.id} (${v.impact}) ${v.nodes[0]?.target.join(" ")}`)).toEqual([]);
}

async function pickTheme(page: Page, theme: "light" | "dark"): Promise<void> {
  const btn = sidebar(page).getByRole("button", { name: /^(Dark|Light) mode$/ });
  const isDark = (await btn.getAttribute("aria-label")) === "Light mode";
  if (isDark !== (theme === "dark")) await btn.click();
  await expect(btn).toHaveAccessibleName(theme === "dark" ? "Light mode" : "Dark mode");
}

/** The micro token (--type-micro) resolved to px on this page. */
async function microPx(page: Page): Promise<number> {
  return page.evaluate(() => {
    const probe = document.createElement("span");
    probe.style.fontSize = "var(--type-micro)";
    document.body.appendChild(probe);
    const px = parseFloat(getComputedStyle(probe).fontSize);
    probe.remove();
    return px;
  });
}

test.describe("D57 sidebar footer text", () => {
  test("the footer line uses the micro size, muted, on one line, expanded and in the drawer", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto("/admin/");
    const px = await microPx(page);
    expect(px).toBeCloseTo(11, 0);
    const texts = sidebar(page).locator(".about");
    await expect(texts).toHaveCount(1);
    for (const el of await texts.all()) {
      expect(await el.evaluate((n) => parseFloat(getComputedStyle(n).fontSize))).toBe(px);
    }
    // Smaller than the secondary size the nav buttons use, and muted rather than full text color.
    const secondary = await page.evaluate(() => {
      const probe = document.createElement("span");
      probe.style.fontSize = "var(--type-secondary)";
      document.body.appendChild(probe);
      const v = parseFloat(getComputedStyle(probe).fontSize);
      probe.remove();
      return v;
    });
    expect(px).toBeLessThan(secondary);
    expect(await texts.first().evaluate((n) => getComputedStyle(n).color)).toBe(
      await page.evaluate(() => {
        const probe = document.createElement("span");
        probe.style.color = "var(--muted)";
        document.body.appendChild(probe);
        const v = getComputedStyle(probe).color;
        probe.remove();
        return v;
      }),
    );
    await page.setViewportSize({ width: 390, height: 844 });
    await page.getByRole("button", { name: /^Open menu/ }).click();
    for (const el of await texts.all()) {
      expect(await el.evaluate((n) => parseFloat(getComputedStyle(n).fontSize))).toBe(px);
    }
  });
});

type Status = "draft" | "sent" | "accepted" | "declined";
const STATUSES: Status[] = ["draft", "sent", "accepted", "declined"];

async function seedStatuses(request: import("@playwright/test").APIRequestContext, tag: string, base = "http://127.0.0.1:8099"): Promise<Record<Status, string>> {
  const out = {} as Record<Status, string>;
  for (const status of STATUSES) {
    const label = `${tag}-${status}`;
    const res = await request.post(`${base}/admin/api/quotes`, { data: { ...seedQuoteBody(label), status } });
    expect(res.ok(), await res.text()).toBe(true);
    out[status] = label;
  }
  return out;
}

const quoteRow = (page: Page, label: string): Locator => page.locator("tr.quote-row", { hasText: label });

test.describe("D49 Quotes email notice", () => {
  for (const [width, density] of [[1440, "comfortable"], [1440, "compact"], [390, "comfortable"], [390, "compact"]] as const) {
    test(`with email unconfigured the notice overlaps no sibling at ${width} px, ${density}`, async ({ page, request }) => {
      await seedStatuses(request, `d49-${width}-${density}`);
      await page.setViewportSize({ width, height: 900 });
      await page.addInitScript((d) => localStorage.setItem("printpaca-density", d), density);
      await page.goto("/admin/#/quotes");
      const notice = page.getByText("Email isn't configured, so quotes can't be emailed from here.");
      await expect(notice).toBeVisible();
      await expect(page.locator("tr.quote-row").first()).toBeVisible();
      const res = await notice.evaluate((el) => {
        const box = el.closest(".banner-muted") ?? el;
        const r = box.getBoundingClientRect();
        const hits: string[] = [];
        for (const sib of Array.from(box.parentElement!.children)) {
          if (sib === box) continue;
          const s = sib.getBoundingClientRect();
          if (s.width === 0 || s.height === 0) continue;
          const w = Math.min(r.right, s.right) - Math.max(r.left, s.left);
          const h = Math.min(r.bottom, s.bottom) - Math.max(r.top, s.top);
          if (w > 0.5 && h > 0.5) hits.push(`${sib.tagName}.${sib.className}`);
        }
        // Nothing but the notice itself paints at its center, and its box stays inside the viewport width.
        const top = document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2);
        return { hits, topIsNotice: top !== null && box.contains(top), inside: r.left >= 0 && r.right <= document.documentElement.clientWidth };
      });
      expect(res.hits).toEqual([]);
      expect(res.topIsNotice).toBe(true);
      expect(res.inside).toBe(true);
    });
  }
});

test.describe("D50 Quotes row actions", () => {
  test("every row has the same action slots, equal widths, equal x positions", async ({ page, request }) => {
    const labels = await seedStatuses(request, "d50");
    for (const density of ["comfortable", "compact"] as const) {
      await page.setViewportSize({ width: 1440, height: 900 });
      await page.addInitScript((d) => localStorage.setItem("printpaca-density", d), density);
      await page.goto("/admin/#/quotes");
      await page.reload();
      await expect(quoteRow(page, labels.draft)).toBeVisible();
      const geo: { w: number; x: number; trunc: boolean; names: string[] }[] = [];
      for (const status of STATUSES) {
        const row = quoteRow(page, labels[status]);
        const slots = row.locator(".row-actions > .slot");
        await expect(slots, `${density} ${status} slot count`).toHaveCount(2);
        const items = await slots.evaluateAll((els) =>
          els.map((el) => {
            const r = el.getBoundingClientRect();
            return { w: Math.round(r.width * 10) / 10, x: Math.round(r.x * 10) / 10, trunc: el.scrollWidth > el.clientWidth, name: el.getAttribute("aria-label") ?? el.textContent ?? "" };
          }),
        );
        for (const it of items) geo.push({ w: it.w, x: it.x, trunc: it.trunc, names: [it.name] });
      }
      expect(geo.every((g) => !g.trunc), `${density}: no label is truncated`).toBe(true);
      expect(new Set(geo.map((g) => g.w)).size, `${density}: one width`).toBe(1);
      // Two x positions in total: slot 1 and slot 2, the same on every row.
      expect(new Set(geo.map((g) => g.x)).size, `${density}: two x positions`).toBe(2);
      await page.evaluate(() => localStorage.removeItem("printpaca-density"));
    }
  });

  test("an action that does not apply is a disabled N/A button with a clear name", async ({ page, request }) => {
    const labels = await seedStatuses(request, "d50na");
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto("/admin/#/quotes");
    for (const status of ["accepted", "declined"] as const) {
      const row = quoteRow(page, labels[status]);
      const na = row.getByRole("button", { name: "Change status: not available" });
      await expect(na).toBeVisible();
      await expect(na).toBeDisabled();
      await expect(na).toHaveText("N/A");
      await expect(na).toHaveAccessibleDescription(/can't change status/);
    }
    for (const status of ["draft", "sent"] as const) {
      await expect(quoteRow(page, labels[status]).getByRole("button", { name: "N/A" })).toHaveCount(0);
    }
    // Draft offers Mark sent, sent offers Mark accepted, both in slot 1.
    await expect(quoteRow(page, labels.draft).locator(".row-actions > .slot").first()).toHaveText("Mark sent");
    await expect(quoteRow(page, labels.sent).locator(".row-actions > .slot").first()).toHaveText("Mark accepted");
  });

  test("nothing shifts after a status change", async ({ page, request }) => {
    const labels = await seedStatuses(request, "d50shift");
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto("/admin/#/quotes");
    const row = quoteRow(page, labels.draft);
    await expect(row.locator(".row-actions > button")).toHaveCount(2);
    const before = await row.locator(".row-actions > button").evaluateAll((els) => els.map((el) => { const r = el.getBoundingClientRect(); return [r.x, r.width]; }));
    await row.getByRole("button", { name: "Mark sent" }).click();
    await page.getByRole("button", { name: /^Yes, mark as sent/ }).first().click().catch(() => {});
    await expect(row.locator(".row-actions > .slot").first()).toHaveText("Mark accepted");
    const mid = await row.locator(".row-actions > button").evaluateAll((els) => els.map((el) => { const r = el.getBoundingClientRect(); return [r.x, r.width]; }));
    expect(mid).toEqual(before);
    await row.getByRole("button", { name: "Mark accepted" }).click();
    await page.getByRole("button", { name: /^Yes, mark as accepted/ }).first().click().catch(() => {});
    await expect(row.getByRole("button", { name: "Change status: not available" })).toBeVisible();
    const after = await row.locator(".row-actions > button").evaluateAll((els) => els.map((el) => { const r = el.getBoundingClientRect(); return [r.x, r.width]; }));
    expect(after).toEqual(before);
  });

  test("on a phone the slots keep equal widths and positions across rows", async ({ page, request }) => {
    const labels = await seedStatuses(request, "d50ph");
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto("/admin/#/quotes");
    const geo: number[][] = [];
    for (const status of STATUSES) {
      const row = quoteRow(page, labels[status]);
      await expect(row).toBeVisible();
      const items = await row.locator(".row-actions > .slot").evaluateAll((els) => els.map((el) => { const r = el.getBoundingClientRect(); return [Math.round(r.width), Math.round(r.x), Math.round(r.height)]; }));
      expect(items).toHaveLength(2);
      geo.push(...items);
    }
    expect(new Set(geo.map((g) => g[0])).size).toBe(1);
    expect(new Set(geo.map((g) => g[1])).size).toBeLessThanOrEqual(2);
    for (const g of geo) expect(g[2]).toBeGreaterThanOrEqual(44);
  });
});

test.describe("axe on the sidebar and Quotes", () => {
  test("pass in both themes", async ({ page, request }) => {
    await seedStatuses(page.request, "axe", "http://127.0.0.1:9199");
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto(INTEGRATIONS_URL + "#/quotes");
    await expect(page.locator("tr.quote-row").first()).toBeVisible();
    for (const theme of ["light", "dark"] as const) {
      await pickTheme(page, theme);
      await axeClean(page, `sidebar and quotes ${theme}`);
    }
    await page.setViewportSize({ width: 390, height: 844 });
    for (const theme of ["light", "dark"] as const) {
      await page.getByRole("button", { name: /^Open menu/ }).click();
      await pickTheme(page, theme);
      await axeClean(page, `drawer ${theme}`);
      await page.keyboard.press("Escape");
      await axeClean(page, `quotes phone ${theme}`);
    }
    await page.evaluate(() => localStorage.removeItem("printpaca-theme"));
  });
});

test.describe("D51 time zone picker", () => {
  const field = (page: Page) => page.getByRole("combobox", { name: "Time zone" });
  const openSettings = async (page: Page) => {
    await page.goto("/admin/#/settings");
    await expect(field(page)).toBeVisible();
  };
  const saved = async (page: Page) => (await (await page.request.get("/admin/api/config")).json()).timezone as string;
  const reset = async (page: Page) => {
    await field(page).fill("UTC");
    await page.getByRole("button", { name: "Save changes" }).click();
    await expect(page.getByText("Settings saved.")).toBeVisible();
  };

  test("type a partial name, pick with the keyboard, save, and it persists after a reload", async ({ page }) => {
    await openSettings(page);
    await field(page).click();
    await field(page).fill("");
    await field(page).pressSequentially("los");
    const list = page.getByRole("listbox", { name: "Zone suggestions" });
    await expect(list).toBeVisible();
    await expect(field(page)).toHaveAttribute("aria-expanded", "true");
    await expect(list.getByRole("option", { name: "America/Los_Angeles" })).toBeVisible();
    // Narrowing the list: every option contains the typed text.
    for (const o of await list.getByRole("option").allTextContents()) expect(o.toLowerCase()).toContain("los");
    await page.keyboard.press("ArrowDown");
    const active = await field(page).getAttribute("aria-activedescendant");
    expect(active).toBeTruthy();
    await expect(page.locator(`#${active}`)).toHaveAttribute("aria-selected", "true");
    // Move to America/Los_Angeles whatever its rank, then Enter.
    for (let i = 0; i < 20 && (await page.locator(`#${await field(page).getAttribute("aria-activedescendant")}`).textContent()) !== "America/Los_Angeles"; i++) {
      await page.keyboard.press("ArrowDown");
    }
    await page.keyboard.press("Enter");
    await expect(field(page)).toHaveValue("America/Los_Angeles");
    await expect(list).toBeHidden();
    await page.getByRole("button", { name: "Save changes" }).click();
    await expect(page.getByText("Settings saved.")).toBeVisible();
    expect(await saved(page)).toBe("America/Los_Angeles");
    await page.reload();
    await expect(field(page)).toHaveValue("America/Los_Angeles");
    await reset(page);
  });

  test("Escape closes the list and a click on an option picks it", async ({ page }) => {
    await openSettings(page);
    await field(page).fill("");
    await field(page).pressSequentially("tok");
    await expect(page.getByRole("listbox", { name: "Zone suggestions" })).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(page.getByRole("listbox", { name: "Zone suggestions" })).toBeHidden();
    await field(page).pressSequentially("y");
    await page.getByRole("option", { name: "Asia/Tokyo" }).click();
    await expect(field(page)).toHaveValue("Asia/Tokyo");
  });

  test.describe("device button", () => {
    test.use({ timezoneId: "Pacific/Auckland" });
    test("fills the browser's time zone", async ({ page }) => {
      await openSettings(page);
      await page.getByRole("button", { name: "Use this device's time zone" }).click();
      await expect(field(page)).toHaveValue("Pacific/Auckland");
    });
  });

  test("an invalid typed value is rejected with a clear message and nothing is saved", async ({ page }) => {
    await openSettings(page);
    await field(page).fill("Mars/Phobos");
    await page.getByRole("button", { name: "Save changes" }).click();
    await expect(page.getByRole("alert").filter({ hasText: "timezone" })).toBeVisible();
    await expect(page.getByRole("alert").filter({ hasText: "timezone" })).toContainText(/Mars\/Phobos|not a valid|unknown/i);
    expect(await saved(page)).toBe("UTC");
  });

  test("the picker passes axe in both themes, open and closed", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await openSettings(page);
    for (const theme of ["light", "dark"] as const) {
      await pickTheme(page, theme);
      await axeClean(page, `time zone closed ${theme}`);
      await field(page).fill("");
      await field(page).pressSequentially("am");
      await expect(page.getByRole("listbox", { name: "Zone suggestions" })).toBeVisible();
      await page.keyboard.press("ArrowDown");
      await axeClean(page, `time zone open ${theme}`);
      await field(page).fill("");
      await field(page).pressSequentially("zzz");
      await expect(page.getByRole("status").filter({ hasText: "No matching option" })).toBeVisible();
      await axeClean(page, `time zone no match ${theme}`);
      await page.keyboard.press("Escape");
      await field(page).fill("UTC");
    }
    await page.evaluate(() => localStorage.removeItem("printpaca-theme"));
  });
});
