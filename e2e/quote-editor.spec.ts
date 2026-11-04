import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";
import { fillCanonicalQuote, historyRow, navButton } from "./helpers";
import AxeBuilder from "@axe-core/playwright";
import { geometryOnly3mf } from "./geometry3mf";

const TAGS = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"];

// The admin quote editor's state rules: a new quote is blank, a saved quote stays bound after a save, a
// reopened quote keeps its saved price, and a stale save is refused.

const SPOOLMAN_URL = "http://127.0.0.1:9199/admin/";
const MOONRAKER_URL = "http://127.0.0.1:9199/admin/";

function uniqueLabel(tag: string): string {
  return `qe-${tag}-${Date.now()}-${Math.floor(Math.random() * 1000)}`;
}

async function saveCanonical(page: Page, label: string): Promise<string> {
  await fillCanonicalQuote(page);
  await page.getByLabel("Label for this quote").fill(label);
  await page.getByRole("button", { name: "Save quote" }).click();
  await expect(page.locator(".action-status")).toContainText(`"${label}" saved.`);
  await expect(page).toHaveURL(/#\/quote\/[0-9a-f]+$/);
  return new URL(page.url()).hash.replace("#/quote/", "");
}

test("New quote opens blank after a save and a hand-typed print time doesn't survive a dropped model", async ({ page }) => {
  await page.goto("/admin/");
  await saveCanonical(page, uniqueLabel("blank"));

  await navButton(page, "New quote").click();
  await expect(page.getByRole("heading", { level: 1, name: "New quote" })).toBeVisible();
  await expect(page).toHaveURL(/#\/quote$/);
  await expect(page.getByLabel(/Model weight/)).toHaveValue("");
  await expect(page.getByLabel(/^Print time \(h/)).toHaveValue("");
  await expect(page.getByLabel("Label for this quote")).toHaveValue("");

  // A hand-typed time belongs to some other model; a geometry-only file has no time of its own.
  await page.getByLabel(/^Print time \(h/).fill("2");
  await page.locator('input[type="file"]').first().setInputFiles({ name: "cube.3mf", mimeType: "model/3mf", buffer: geometryOnly3mf() });
  await expect(page.getByLabel("Solidity")).toBeVisible();
  await expect(page.getByLabel(/^Print time \(h/)).toHaveValue("");

  // The cost bar says why the cost is missing, links to the field, and explains "slicer" on demand.
  const need = page.getByRole("group", { name: "Quote summary" }).getByRole("link", { name: /Price needs a print time/ });
  await expect(need).toBeVisible();
  await need.click();
  await expect(page.getByLabel(/^Print time \(h/)).toBeFocused();
  const help = page.getByRole("group", { name: "Quote summary" }).getByText("What is a slicer?");
  await help.click();
  await expect(page.getByRole("group", { name: "Quote summary" }).getByText(/turns a 3D model into instructions/)).toBeVisible();
});

test("a saved quote is edited under its own heading, stays bound after a save, and loads from its URL", async ({ page }) => {
  const label = uniqueLabel("bound");
  await page.goto("/admin/");
  const id = await saveCanonical(page, label);

  // Saving bound the form: heading, label and sidebar follow the saved quote.
  await expect(page.getByRole("heading", { level: 1, name: `Edit quote: ${label}` })).toBeVisible();
  await expect(navButton(page, "Quotes")).toHaveAttribute("aria-current", "page");
  await expect(navButton(page, "New quote")).not.toHaveAttribute("aria-current", "page");

  // A second Ctrl+S updates the same quote; it must not create another row.
  await page.getByLabel(/Model weight/).fill("55");
  await page.keyboard.press("Control+s");
  await expect(page.locator(".action-status")).toContainText(`"${label}" updated.`);
  await page.keyboard.press("Control+s");
  await expect(page.locator(".action-status")).toContainText(`"${label}" updated.`);
  await expect(historyRow(page, label)).toHaveCount(1);

  // The address names the quote, so a new load shows it with the label prefilled.
  await page.goto(`/admin/#/quote/${id}`);
  await page.reload();
  await expect(page.getByRole("heading", { level: 1, name: `Edit quote: ${label}` })).toBeVisible();
  await expect(page.getByLabel(/Model weight/)).toHaveValue("55");
  await expect(page.getByLabel("Label for this quote")).toHaveValue(label);

  // The editing state (top bar, rates note area, disclosure) passes the same axe gate as the rest of the app.
  const results = await new AxeBuilder({ page }).withTags(TAGS).analyze();
  expect(results.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(" ")).join(", ")}`)).toEqual([]);

  // Update and Save as new sit at the top, beside the heading.
  const top = page.getByRole("group", { name: "Save changes to this quote" });
  await expect(top.getByRole("button", { name: "Update quote" })).toBeVisible();
  await top.getByRole("button", { name: "Save as new" }).click();
  await expect(page.locator(".action-status")).toContainText(`"${label} (copy)" saved.`);
  await expect(page.getByRole("heading", { level: 1, name: `Edit quote: ${label} (copy)` })).toBeVisible();
});

test("leaving or replacing a draft with unsaved edits asks first", async ({ page }) => {
  await page.goto("/admin/");
  await page.getByLabel(/Model weight/).fill("12");

  // Declining stays on the editor with the edit intact.
  page.once("dialog", (d) => void d.dismiss());
  await navButton(page, "Quotes").click();
  await expect(page.getByLabel(/Model weight/)).toHaveValue("12");

  page.once("dialog", (d) => void d.dismiss());
  await navButton(page, "New quote").click();
  await expect(page.getByLabel(/Model weight/)).toHaveValue("12");

  // Accepting starts the blank quote.
  page.once("dialog", (d) => void d.accept());
  await navButton(page, "New quote").click();
  await expect(page.getByLabel(/Model weight/)).toHaveValue("");
});

test("a save made from a stale copy is refused, and Reload or Overwrite resolves it", async ({ page, request }) => {
  const label = uniqueLabel("conflict");
  await page.goto("/admin/");
  const id = await saveCanonical(page, label);

  // Another tab (here: the API) saves first.
  const current = await (await request.get(`/admin/api/quotes/${id}`)).json();
  const body = { ...current, label: `${label} elsewhere` };
  for (const k of ["id", "savedAt", "quantity", "perUnitTotalCents", "batchTotalCents", "recommendedPriceCents", "resolvedPriceCents", "netProfitCents", "achievedMarginPct", "valid", "partCount", "createdBy", "updatedBy"]) {
    delete body[k];
  }
  const put = await request.put(`/admin/api/quotes/${id}`, { data: body });
  expect(put.ok()).toBe(true);

  await page.getByLabel(/Model weight/).fill("61");
  await page.getByRole("group", { name: "Save changes to this quote" }).getByRole("button", { name: "Update quote" }).click();
  const alert = page.getByRole("alert").filter({ hasText: "This was changed in another tab or by another user." });
  await expect(alert.first()).toBeVisible();

  // Reload discards the edit and shows the other tab's version.
  await alert.getByRole("button", { name: "Reload" }).first().click();
  await expect(page.getByLabel(/Model weight/)).toHaveValue("50");
  await expect(page.getByLabel("Label for this quote")).toHaveValue(`${label} elsewhere`);

  // Overwrite keeps the edit.
  await page.getByLabel(/Model weight/).fill("62");
  const apiTouch = await request.put(`/admin/api/quotes/${id}`, { data: body });
  expect(apiTouch.ok()).toBe(true);
  await page.getByRole("group", { name: "Save changes to this quote" }).getByRole("button", { name: "Update quote" }).click();
  await page.getByRole("button", { name: "Overwrite" }).click();
  await expect(page.locator(".action-status")).toContainText("updated.");
  const after = await (await request.get(`/admin/api/quotes/${id}`)).json();
  expect(after.formState.parts[0].weightG).toBe(62);
});

test.describe("a reopened quote keeps its saved price", () => {
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

  test("opens at the list price after the shop's rates change, and says so", async ({ page, request }) => {
    const label = uniqueLabel("price");
    await page.goto("/admin/");
    const id = await saveCanonical(page, label);
    const saved = await (await request.get(`/admin/api/quotes/${id}`)).json();
    const savedTotal = `$${(saved.batchTotalCents / 100).toFixed(2)}`;

    // The shop switches to time-of-use pricing with different rates: the same form would now price higher.
    const cfg = structuredClone(originalConfig) as { energy: { mode: string; tou: { peakRate: number; offPeakRate: number; defaultOffPeakFrac: number } } };
    cfg.energy.mode = "tou";
    cfg.energy.tou = { peakRate: 5, offPeakRate: 4, defaultOffPeakFrac: 0 };
    const put = await request.put("/admin/api/settings", { data: { config: cfg } });
    expect(put.ok(), await put.text()).toBe(true);

    await page.goto("/admin/");
    await page.goto(`/admin/#/quote/${id}`);
    await page.reload();
    await expect(page.getByRole("heading", { level: 1, name: `Edit quote: ${label}` })).toBeVisible();
    await expect(page.locator(".cost-stats .stat.cost .stat-value")).toHaveText(savedTotal);
    const note = page.getByText("Shop electricity rates changed since this quote was saved.");
    await expect(note).toBeVisible();

    await page.getByRole("button", { name: "Use current rates" }).click();
    await expect(note).toHaveCount(0);
    await expect(page.locator(".cost-stats .stat.cost .stat-value")).not.toHaveText(savedTotal);
  });
});

test("the print fields are one skip link away, and every Costs input has a name", async ({ page }) => {
  await page.goto("/admin/");
  await page.getByRole("link", { name: "Skip to model weight and print time" }).focus();
  await page.keyboard.press("Enter");
  await expect(page.getByLabel(/Model weight/)).toBeFocused();

  await page.locator("details.rates > summary").click();
  const unnamed = await page.locator("#main input:not([type=hidden]):not([type=file]), #main select").evaluateAll((els) =>
    els
      .filter((el) => {
        const e = el as HTMLInputElement;
        return !(e.labels && e.labels.length > 0) && !e.getAttribute("aria-label") && !e.getAttribute("aria-labelledby");
      })
      .map((el) => `${el.tagName}#${el.id}.${el.className}`),
  );
  expect(unnamed).toEqual([]);
});

test("the cost lines explain themselves and a batch total says how it relates to the unit cost", async ({ page }) => {
  await page.goto("/admin/");
  await fillCanonicalQuote(page);
  await page.getByLabel(/Quantity/).fill("3");
  await expect(page.getByText("Printer wear: its price spread over its lifespan")).toBeVisible();
  await expect(page.getByText(/differ from the cost per unit/)).toBeVisible();
  // Turning on a selling price with no shipping must not show "-$0.00".
  await page.getByLabel("Set a selling price").check();
  await expect(page.getByText("-$0.00")).toHaveCount(0);
});

test("one client field feeds the quote and the print toolbar", async ({ page }) => {
  await page.goto("/admin/");
  await fillCanonicalQuote(page);
  await page.getByLabel("Set a selling price").check();
  await expect(page.getByPlaceholder("Client name")).toHaveCount(0);
  await page.getByLabel("Client for this quote").fill("One Field Customer");
  await expect(page.locator(".linked-customer")).toHaveText("One Field Customer");
});

test("a Spoolman spool sets the material to match", async ({ page }) => {
  await page.goto(SPOOLMAN_URL);
  await page.getByLabel(/Model weight/).fill("20");
  const material = page.getByRole("combobox", { name: "Material" });
  await expect(material).toHaveValue("pla");
  const spool = page.getByLabel("Spool (from Spoolman)");
  await expect(spool).toBeVisible();
  // "Blue" is a PETG spool in the stub, and PETG is in the material catalog.
  await spool.selectOption((await spool.locator("option", { hasText: "Blue" }).first().getAttribute("value")) ?? "");
  await expect(material).toHaveValue("petg");
  await expect(page.getByText("Material changed to PETG to match this spool.")).toBeVisible();

  // Picking a PLA spool moves the material back.
  await spool.selectOption((await spool.locator("option", { hasText: "Galaxy Black" }).first().getAttribute("value")) ?? "");
  await expect(material).toHaveValue("pla");
});

test("Recent prints labels where its filament weight comes from", async ({ page }) => {
  await page.goto(MOONRAKER_URL);
  const picker = page.getByLabel("Recent print (from Moonraker)");
  await expect(picker).toBeVisible();
  await expect(picker.locator("option", { hasText: "calibration_cube.gcode" })).toContainText("from length used");
  await expect(page.getByText(/rough guess from how much filament was fed/)).toBeVisible();
});

test("a blank print start is named in the Electricity formula", async ({ request }) => {
  const res = await request.post("/admin/api/quote/compute", {
    data: {
      inputs: {
        parts: [
          {
            label: "Part 1", weightG: 10, pricePerKg: 20, wastePct: 0, timeHours: 1, timeMinutes: 0, watts: 100,
            energy: { mode: "schedule", timezone: "UTC", schedule: { defaultStartTime: "22:00", holidays: [], seasons: [{ name: "All", months: [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12], weekday: { baseName: "Base", baseRate: 0.2, windows: [] }, weekend: { baseName: "Base", baseRate: 0.2, windows: [] } }] } },
            failureRatePct: 0, prepMinutes: 0, laborRate: 0, machineCost: 0, machineLifeHours: 0, monthlyFixed: 0, monthlyHours: 0, quantity: 1,
          },
        ],
      },
      options: { laborPerUnit: false },
    },
  });
  expect(res.ok()).toBe(true);
  const body = await res.json();
  const electricity = body.parts[0].breakdown.perUnit.find((l: { id: string }) => l.id === "electricity");
  expect(electricity.formula).toBe("needs a print start");
});
