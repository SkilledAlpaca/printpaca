import { test, expect } from "@playwright/test";

// The only spec that sets MOONRAKER_URL/FLUIDD_URL. It targets a third server on 9199
// (playwright.config.ts), pointed at scripts/e2e-moonraker-stub.mjs on 9125, by absolute URL.
const MOONRAKER_URL = "http://127.0.0.1:9199/admin/";

const FLUIDD_HREF = "http://127.0.0.1:9126";

const RECENT_PRINTS_LABEL = "Recent print (from Moonraker)";

function fluiddLink(page: import("@playwright/test").Page) {
  // Scoped to the "Sections" landmark, which renders this link once.
  return page.getByRole("navigation", { name: "Sections" }).getByRole("link", { name: /Fluidd/ });
}

test("Recent Prints picker prefills weight/time from Moonraker history, and a later pick supersedes the first", async ({ page }) => {
  await page.goto(MOONRAKER_URL);

  const picker = page.getByLabel(RECENT_PRINTS_LABEL);
  await expect(picker).toBeVisible();

  // job-benchy: filament_weight_total (42.5g) is used directly, and print_duration (5400s) wins over
  // estimated_time, so the source is "actual".
  await picker.selectOption("job-benchy");
  await expect(page.getByLabel(/Model weight/)).toHaveValue("42.5");
  await expect(page.getByLabel(/^Print time \(h/)).toHaveValue("1");
  await expect(page.getByLabel(/Print time \(minutes\)/)).toHaveValue("30");
  await expect(page.getByText(/from Moonraker history, actual print time/)).toBeVisible();

  // job-cube: no weight totals, so weight derives from filament_used (2500mm of 1.75mm PLA at 1.24
  // g/cm3 -> 7.46g). print_duration is 0, so time falls back to estimated_time (1800s).
  await picker.selectOption("job-cube");
  await expect(page.getByLabel(/Model weight/)).toHaveValue("7.46");
  await expect(page.getByLabel(/^Print time \(h/)).toHaveValue("0");
  await expect(page.getByLabel(/Print time \(minutes\)/)).toHaveValue("30");
  await expect(page.getByText(/from Moonraker history, slicer-estimated time/)).toBeVisible();
});

test("Fluidd link is present in the sidebar, pointing at FLUIDD_URL and opening in a new tab", async ({ page }) => {
  await page.goto(MOONRAKER_URL);
  const link = fluiddLink(page);
  await expect(link).toBeVisible();
  await expect(link).toHaveAttribute("href", FLUIDD_HREF);
  await expect(link).toHaveAttribute("target", "_blank");
});

test("the default instance (no MOONRAKER_URL/FLUIDD_URL) has neither the picker nor the Fluidd link", async ({ page }) => {
  await page.goto("/admin/");
  await expect(page.getByLabel(RECENT_PRINTS_LABEL)).not.toBeVisible();
  await expect(fluiddLink(page)).not.toBeVisible();
});
