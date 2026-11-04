import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { fillCanonicalQuote, navButton, openBreakdown } from "./helpers";

// Time-of-use schedule energy mode. It switches the shared server to schedule mode and restores the original
// settings afterwards. The example rates are the app's SCE TOU-D-4-9PM example (summer weekday on-peak 0.55,
// off-peak 0.33, no holidays), and the e2e instance time zone is UTC.

const TAGS = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"];

let originalConfig: Record<string, unknown>;

test.beforeAll(async ({ request }) => {
  const res = await request.get("/admin/api/settings");
  expect(res.ok()).toBeTruthy();
  originalConfig = (await res.json()).config;
});

test.afterAll(async ({ request }) => {
  const res = await request.put("/admin/api/settings", { data: { config: originalConfig } });
  expect(res.ok()).toBeTruthy();
});

async function electricityRow(page: Page) {
  return page.locator(".breakdown table tbody tr", { hasText: "Electricity" });
}

test.describe.serial("time-of-use schedule", () => {
  test("choose the mode, load the labelled SCE example, and save", async ({ page }) => {
    await page.goto("/admin/");
    await navButton(page, "Electricity").click();
    await page.getByLabel("Mode", { exact: true }).selectOption("schedule");

    // The empty schedule is flagged until a season exists.
    await expect(page.getByRole("list", { name: "Schedule problems" })).toContainText(/season/i);

    await expect(page.getByText(/rates are examples, not SCE's published rates/)).toBeVisible();
    await page.getByRole("button", { name: "Load SCE TOU-D-4-9PM example" }).click();
    await expect(page.getByRole("group", { name: "Season 1 Weekday", exact: true })).toBeVisible();
    await expect(page.getByLabel("Season 1 Weekday window 1 name")).toHaveValue("On-peak");
    await expect(page.getByRole("list", { name: "Schedule problems" })).toHaveCount(0);

    // A second load replaces a schedule, so it asks first; Cancel keeps the edit.
    await page.getByLabel("Season 1 Weekday window 1 rate").fill("0.6");
    await page.getByRole("button", { name: "Load SCE TOU-D-4-9PM example" }).click();
    const replace = page.getByRole("button", { name: "Yes, replace with example" });
    await expect(replace).toBeFocused();
    await page.keyboard.press("Escape");
    await expect(page.getByRole("button", { name: "Load SCE TOU-D-4-9PM example" })).toBeFocused();
    await expect(page.getByLabel("Season 1 Weekday window 1 rate")).toHaveValue("0.6");
    await page.getByLabel("Season 1 Weekday window 1 rate").fill("0.55");

    await page.getByRole("button", { name: "Save changes" }).click();
    await expect(page.getByText("Settings saved.")).toBeVisible();
  });

  test("the Electricity calculator splits a print across windows", async ({ page }) => {
    await page.goto("/admin/");
    await navButton(page, "Electricity").click();
    await page.getByLabel("Printer power (W)").fill("1000");
    await page.getByLabel("Print time (h)").fill("8");
    await page.getByLabel("Print start", { exact: true }).fill("2026-07-03T14:00");
    // Friday in July: 3 h off-peak at 0.33 and 5 h on-peak at 0.55 on a 1 kW printer = 3.74.
    await expect(page.locator('dl[aria-label="Electricity results"]')).toContainText("3.740");
    const split = page.getByRole("table", { name: "Hours and cost by tariff period" });
    await expect(split.getByRole("row", { name: /Off-peak/ })).toContainText("3");
    await expect(split.getByRole("row", { name: /On-peak/ })).toContainText("2.750");

    // A Saturday has no on-peak: the same hours cost less.
    await page.getByLabel("Print start", { exact: true }).fill("2026-07-04T14:00");
    await expect(split.getByRole("row", { name: /Mid-peak/ })).toBeVisible();
    await expect(split.getByRole("row", { name: /On-peak/ })).toHaveCount(0);
  });

  test("a quote prices from its print start, with a per-part override", async ({ page }) => {
    await page.goto("/admin/");
    await navButton(page, "New quote").click();
    await openBreakdown(page);
    await fillCanonicalQuote(page);

    const order = page.getByLabel("Print start (order)");
    const part = page.getByLabel("Print start (this part)");
    await order.fill("2026-07-03T14:00");
    // 1 h 20 min at 250 W, all off-peak (0.33): $0.11.
    await expect(await electricityRow(page)).toContainText("$0.11");

    // On-peak (0.55): $0.18, and the breakdown names the period.
    await order.fill("2026-07-03T16:00");
    await expect(await electricityRow(page)).toContainText("$0.18");
    await (await electricityRow(page)).locator("summary").click();
    await expect(page.getByRole("list", { name: "Electricity by tariff period" })).toContainText("On-peak");

    // A part-level start wins over the order's.
    await part.fill("2026-07-03T10:00");
    await expect(await electricityRow(page)).toContainText("$0.11");
    await page.getByRole("button", { name: "Use the order's start" }).click();
    await expect(part).toBeFocused();
    await expect(part).toHaveValue("");
    await expect(await electricityRow(page)).toContainText("$0.18");

    // The default is restorable, and a missing start is called out.
    await page.getByRole("button", { name: /^Use the next 22:00/ }).click();
    await expect(order).toHaveValue(/T22:00$/);
    await order.fill("");
    await order.blur();
    await expect(page.getByText("Enter the print start.")).toBeVisible();
  });

  test("Use the next 22:00 sets the order's print start, says what it did, and re-prices the breakdown", async ({ page }) => {
    await page.goto("/admin/");
    await navButton(page, "New quote").click();
    await openBreakdown(page);
    await fillCanonicalQuote(page);
    const order = page.getByLabel("Print start (order)");
    const use = page.getByRole("button", { name: /^Use the next 22:00/ });
    const note = page.getByTestId("print-start-note");

    // The field starts at the next 22:00, so the first click changes nothing on screen. It has to say so.
    const seeded = await order.inputValue();
    expect(seeded).toMatch(/T22:00$/);
    await use.click();
    await expect(order).toHaveValue(seeded);
    await expect(note).toContainText("already the next 22:00");

    // After an edit it restores the seeded value, the note names the zone, the breakdown re-prices, and editing hides the note.
    await order.fill("2026-07-03T16:00");
    await expect(note).toHaveCount(0);
    const onPeak = await (await electricityRow(page)).innerText();
    await use.click();
    await expect(order).toHaveValue(seeded);
    await expect(note).toContainText(`Print start set to ${seeded.replace("T", " ")} (UTC)`);
    await expect(async () => expect(await (await electricityRow(page)).innerText()).not.toBe(onPeak)).toPass();
    await order.fill("2026-07-03T16:00");
    await expect(note).toHaveCount(0);

    // A half-typed field (the browser reports "") is replaced too.
    await order.focus();
    await page.keyboard.press("Backspace");
    await use.click();
    await expect(order).toHaveValue(seeded);

    // A part with its own start keeps pricing from it, and the note says so.
    await page.getByLabel("Print start (this part)").fill("2026-07-03T10:00");
    await use.click();
    await expect(note).toContainText("This part has its own start");
  });

  for (const scheme of ["light", "dark"] as const) {
    for (const vp of [
      { name: "desktop", width: 1280, height: 900 },
      { name: "390", width: 390, height: 844 },
    ]) {
      test(`axe: schedule UI has no WCAG 2.2 AA violations (${scheme}, ${vp.name})`, async ({ page }) => {
        await page.setViewportSize({ width: vp.width, height: vp.height });
        await page.emulateMedia({ colorScheme: scheme });
        test.setTimeout(120_000);
        await page.goto("/admin/#/electricity");
        for (const tab of ["Electricity", "New quote"]) {
          // The hash, not the nav button: at 390 px the nav is a drawer.
          await page.evaluate((h) => (location.hash = h), tab === "Electricity" ? "#/electricity" : "#/quote");
          if (tab === "New quote") {
            await fillCanonicalQuote(page);
            await page.getByLabel("Print start (order)").fill("2026-07-03T16:00");
          } else {
            await expect(page.getByLabel("Season 1 Weekday window 1 name")).toBeVisible();
          }
          await page.waitForLoadState("networkidle");
          await page.evaluate(() => document.fonts.ready);
          const results = await new AxeBuilder({ page }).withTags(TAGS).analyze();
          expect(
            results.violations.map((v) => `${tab}: ${v.id} (${v.impact}) e.g. ${v.nodes[0]?.target.join(" ")}`),
          ).toEqual([]);
        }
      });
    }
  }

  test("a saved quote keeps its price after the shop schedule changes", async ({ page, request }) => {
    // The server prices from the quote's own schedule snapshot and start, so editing Settings afterwards
    // can't move a saved quote's price.
    const part = {
      label: "Part 1", weightG: 50, pricePerKg: 20, wastePct: 0, timeHours: 8, timeMinutes: 0, watts: 1000,
      energy: {
        mode: "schedule", timezone: "UTC", start: "2026-07-03T14:00",
        schedule: (await (await request.get("/admin/api/config")).json()).energy.schedule,
      },
      failureRatePct: 0, prepMinutes: 0, laborRate: 0, machineCost: 0, machineLifeHours: 0, monthlyFixed: 0,
      monthlyHours: 0, quantity: 1,
    };
    const compute = async () => {
      const res = await request.post("/admin/api/quote/compute", { data: { inputs: { parts: [part] }, options: { laborPerUnit: false } } });
      expect(res.ok()).toBeTruthy();
      return (await res.json()).parts[0].breakdown.perUnit.find((l: { id: string }) => l.id === "electricity");
    };
    const before = await compute();
    expect(before.amount).toBe(374);
    expect(before.periods.map((p: { name: string }) => p.name)).toEqual(["Off-peak", "On-peak"]);

    // Change the shop's live schedule; the snapshot in the inputs keeps the old price.
    await page.goto("/admin/");
    await navButton(page, "Electricity").click();
    await page.getByLabel("Season 1 Weekday window 1 rate").fill("9");
    await page.getByRole("button", { name: "Save changes" }).click();
    await expect(page.getByText("Settings saved.")).toBeVisible();
    expect((await compute()).amount).toBe(374);
  });

  test("Same schedule all year: asks before overwriting, shares the windows, keeps the rates, and saves the flag", async ({ page, request }) => {
    await page.goto("/admin/");
    await navButton(page, "Electricity").click();
    await page.getByRole("button", { name: "Load SCE TOU-D-4-9PM example" }).click();
    await page.getByRole("button", { name: "Yes, replace with example" }).click();
    const sameSwitch = page.getByRole("checkbox", { name: "Same schedule all year" });
    await expect(sameSwitch).not.toBeChecked();
    // The example's seasons have different windows (Winter has a second weekday window), so turning it on asks first.
    await sameSwitch.click();
    await expect(page.getByText("Use Summer's windows for every season?")).toBeVisible();
    await expect(sameSwitch).not.toBeChecked();
    await page.keyboard.press("Escape");
    await expect(sameSwitch).toBeFocused();
    await expect(page.getByLabel("Season 2 Weekday window 2 name")).toHaveValue("Mid-peak");
    await expect(page.getByRole("region", { name: "Windows for every season" })).toHaveCount(0);

    await sameSwitch.click();
    await page.getByRole("button", { name: "Yes, use Summer's windows" }).click();
    await expect(sameSwitch).toBeChecked();
    await expect(sameSwitch).toBeFocused();
    const shared = page.getByRole("region", { name: "Windows for every season" });
    await expect(shared.getByLabel("Weekday window 1 name")).toHaveValue("On-peak");
    // Winter now has Summer's single weekday window, and kept its own rate for window 1 (0.28) and base rate (0.34).
    await expect(page.getByLabel("Season 2 Weekday window 1 rate")).toHaveValue("0.28");
    await expect(page.getByLabel("Season 2 Weekday base rate")).toHaveValue("0.34");
    await expect(page.getByLabel("Season 2 Weekday window 2 rate")).toHaveCount(0);
    // Per-season window names and times are no longer editable.
    await expect(page.getByLabel("Season 2 Weekday window 1 name")).toHaveCount(0);

    // Edit a shared time: every season gets it. Window times are real time inputs on a 15-minute step.
    const start = shared.getByLabel("Weekday window 1 start");
    await expect(start).toHaveAttribute("type", "time");
    await expect(start).toHaveAttribute("step", "900");
    await start.fill("17:00");
    await shared.getByLabel("Weekday window 1 end").fill("22:00");
    await page.getByLabel("Season 1 Weekday window 1 rate").fill("0.56");
    await page.getByRole("button", { name: "Save changes" }).click();
    await expect(page.getByText("Settings saved.")).toBeVisible();

    const cfg = (await (await request.get("/admin/api/settings")).json()).config as {
      energy: { schedule: { sameWindowsAllYear?: boolean; seasons: { weekday: { windows: { name: string; start: string; end: string; rate: number }[] } }[] } };
    };
    expect(cfg.energy.schedule.sameWindowsAllYear).toBe(true);
    expect(cfg.energy.schedule.seasons.map((s) => s.weekday.windows.map((w) => `${w.name} ${w.start}-${w.end} ${w.rate}`))).toEqual([
      ["On-peak 17:00-22:00 0.56"],
      ["On-peak 17:00-22:00 0.28"],
    ]);

    // It survives a reload, and turning it off keeps each season's own copy.
    await page.reload();
    await expect(sameSwitch).toBeChecked();
    await sameSwitch.uncheck();
    await expect(page.getByLabel("Season 2 Weekday window 1 name")).toHaveValue("On-peak");
    await expect(page.getByLabel("Season 2 Weekday window 1 rate")).toHaveValue("0.28");
  });

  test("a stored end of 24:00 shows as 00:00 and stays 24:00 until edited; the server rejects a flagged schedule whose windows differ", async ({ page, request }) => {
    await page.goto("/admin/");
    await navButton(page, "Electricity").click();
    await page.getByRole("button", { name: "Load SCE TOU-D-4-9PM example" }).click();
    await page.getByRole("button", { name: "Yes, replace with example" }).click();
    await page.getByLabel("Season 1 Weekday window 1 start").fill("22:00");
    await page.getByLabel("Season 1 Weekday window 1 end").fill("00:00");
    await expect(page.getByRole("list", { name: "Schedule problems" })).toHaveCount(0);
    await page.getByRole("button", { name: "Save changes" }).click();
    await expect(page.getByText("Settings saved.")).toBeVisible();
    const get = async () => (await (await request.get("/admin/api/settings")).json()).config;
    const cfg = await get();
    expect(cfg.energy.schedule.seasons[0].weekday.windows[0]).toMatchObject({ start: "22:00", end: "24:00" });
    await page.reload();
    await expect(page.getByLabel("Season 1 Weekday window 1 end")).toHaveValue("00:00");

    const bad = JSON.parse(JSON.stringify(cfg));
    bad.energy.schedule.sameWindowsAllYear = true;
    const res = await request.put("/admin/api/settings", { data: { config: bad } });
    expect(res.status()).toBe(422);
    expect(await res.text()).toContain("sameWindowsAllYear");
    bad.energy.schedule.sameWindowsAllYear = false;
    expect((await request.put("/admin/api/settings", { data: { config: bad } })).ok()).toBeTruthy();
  });

  for (const scheme of ["light", "dark"] as const) {
    test(`axe: Same schedule all year has no WCAG 2.2 AA violations (${scheme})`, async ({ page }) => {
      await page.emulateMedia({ colorScheme: scheme });
      test.setTimeout(120_000);
      await page.goto("/admin/#/electricity");
      await page.getByRole("button", { name: "Load SCE TOU-D-4-9PM example" }).click();
      await page.getByRole("button", { name: "Yes, replace with example" }).click();
      await page.getByRole("checkbox", { name: "Same schedule all year" }).click();
      await page.getByRole("button", { name: "Yes, use Summer's windows" }).click();
      await expect(page.getByRole("region", { name: "Windows for every season" })).toBeVisible();
      await page.waitForLoadState("networkidle");
      await page.evaluate(() => document.fonts.ready);
      const results = await new AxeBuilder({ page }).withTags(TAGS).analyze();
      expect(results.violations.map((v) => `${v.id} (${v.impact}) e.g. ${v.nodes[0]?.target.join(" ")}`)).toEqual([]);
    });
  }
});
