import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";
import { gotoSettings } from "./helpers";

// All specs share one server, and a settings save hot-applies to every later spec via /api/config.
// These tests restore the default symbol before the suite moves on, and the second depends on the
// symbol the first saved.

async function configSymbol(page: Page): Promise<string> {
  const res = await page.request.get("/admin/api/config");
  const body = (await res.json()) as { currency: { symbol: string } };
  return body.currency.symbol;
}

test("saving Settings hot-applies: /api/config serves the new value with no restart", async ({ page }) => {
  await gotoSettings(page);
  expect(await configSymbol(page)).toBe("$");

  await page.getByLabel("Symbol").fill("€");
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByRole("button", { name: "Save changes" })).toHaveCount(0);

  // Poll: "Save changes" reaching count 0 marks the save starting, and /api/config settles to the new
  // value a beat later.
  await expect.poll(async () => await configSymbol(page)).toBe("€");
  await expect(page.getByText(/Restart printpaca/)).toHaveCount(0);
});

test("the saved symbol persists across reload", async ({ page }) => {
  await gotoSettings(page);
  await expect(page.getByLabel("Symbol")).toHaveValue("€");

  await page.reload();
  await gotoSettings(page);
  await expect(page.getByLabel("Symbol")).toHaveValue("€");
});

test("an invalid marginPct is rejected with a path-scoped error; a valid one saves; symbol restored", async ({ page }) => {
  await gotoSettings(page);

  const marginError = page.getByRole("alert").filter({ hasText: "pricing.marginPct" });

  await page.getByLabel("Margin (%)").fill("150");
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(marginError).toBeVisible();
  expect(await configSymbol(page)).toBe("€");

  await page.getByLabel("Margin (%)").fill("30");
  // Restore the default symbol in the same save.
  await page.getByLabel("Symbol").fill("$");
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(marginError).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Save changes" })).toHaveCount(0);
  // Poll: the button relabel marks the save starting, not finishing.
  await expect.poll(async () => await configSymbol(page)).toBe("$");
});

// lostOnFailure must round-trip through a real save and reload; a form that dropped the checkbox on
// save would pass every other test.
test("a checked lostOnFailure hardware entry persists across reload and via GET /api/config", async ({ page }) => {
  await gotoSettings(page);
  await page.getByRole("button", { name: "Add hardware" }).click();
  await page.getByLabel("Hardware 1 name").fill("e2e persist magnet");
  await page.getByLabel("Hardware 1 unit cost").fill("1.00");
  await page.getByLabel("Hardware 1 lost on failed prints").check();
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByRole("button", { name: "Save changes" })).toHaveCount(0);

  await page.reload();
  await gotoSettings(page);
  await expect(page.getByLabel("Hardware 1 lost on failed prints")).toBeChecked();

  const res = await page.request.get("/admin/api/config");
  const body = (await res.json()) as { hardware: Array<{ name: string; lostOnFailure: boolean }> };
  const entry = body.hardware.find((h) => h.name === "e2e persist magnet");
  expect(entry?.lostOnFailure).toBe(true);

  // Cleanup: remove the hardware catalog entry.
  await page.getByRole("button", { name: "Remove hardware e2e persist magnet" }).click();
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByRole("button", { name: "Save changes" })).toHaveCount(0);
});

// timezone and the customer shipping charge are new top-level and pricing keys: both must survive a real save
// and show up in /api/config. The shipping charge is restored to 0 so later specs see the stock default.
test("time zone and default shipping charge persist through a save and are restored", async ({ page }) => {
  await gotoSettings(page);
  await page.getByLabel("Time zone").fill("America/New_York");
  await page.getByLabel("Default shipping charge").fill("6.5");
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByRole("button", { name: "Save changes" })).toHaveCount(0);

  const read = async () => {
    const body = (await (await page.request.get("/admin/api/config")).json()) as {
      timezone: string;
      pricing: { defaultShippingCharge: number };
    };
    return [body.timezone, body.pricing.defaultShippingCharge];
  };
  await expect.poll(read).toEqual(["America/New_York", 6.5]);

  await page.getByLabel("Time zone").fill("UTC");
  await page.getByLabel("Default shipping charge").fill("0");
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByRole("button", { name: "Save changes" })).toHaveCount(0);
  await expect.poll(read).toEqual(["UTC", 0]);
});

test("an unknown time zone is rejected with a timezone-scoped error", async ({ page }) => {
  await gotoSettings(page);
  await page.getByLabel("Time zone").fill("Mars/Phobos");
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByRole("alert").filter({ hasText: "timezone" })).toBeVisible();
  expect((await (await page.request.get("/admin/api/config")).json()).timezone).toBe("UTC");
});
