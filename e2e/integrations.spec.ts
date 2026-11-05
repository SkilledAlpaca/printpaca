import { test, expect } from "@playwright/test";
import type { Locator, Page } from "@playwright/test";
import { gotoSettings, navButton } from "./helpers";

// Settings > Integrations (v1.1 items 9 and 10), on the 9299 server that starts with no integration URLs.
// It points at the Spoolman stub (9127) and the Moonraker stub (9125). The tests run in order: the last ones
// rely on the URLs saved by the earlier ones.

test.use({ baseURL: "http://127.0.0.1:9299" });
test.describe.configure({ mode: "serial" });

const SPOOLMAN = "http://127.0.0.1:9127";
const MOONRAKER = "http://127.0.0.1:9125";

function integration(page: Page, name: string): Locator {
  return page.locator("#section-integrationsUrls .card").filter({ has: page.getByRole("heading", { name, exact: true }) });
}

test("Test reports what Spoolman and Moonraker found, in plain words", async ({ page }) => {
  await gotoSettings(page);

  const spoolman = integration(page, "Spoolman");
  await expect(spoolman.getByText("Not configured")).toBeVisible();
  await spoolman.getByLabel("Spoolman URL").fill(SPOOLMAN);
  await spoolman.getByRole("button", { name: "Test" }).click();
  await expect(spoolman.getByText("Connected. 2 active spools.")).toBeVisible();

  const moonraker = integration(page, "Moonraker");
  await moonraker.getByLabel("Moonraker URL").fill(MOONRAKER);
  await moonraker.getByRole("button", { name: "Test" }).click();
  await expect(moonraker.getByText("Connected to voron-e2e (Klipper ready). 20 recent jobs.")).toBeVisible();
});

test("a failed Test says what to check", async ({ page }) => {
  await gotoSettings(page);
  const spoolman = integration(page, "Spoolman");
  await spoolman.getByLabel("Spoolman URL").fill("http://127.0.0.1:9");
  await spoolman.getByRole("button", { name: "Test" }).click();
  const alert = spoolman.getByRole("alert");
  await expect(alert).toContainText("refused the connection");
  await expect(alert).toContainText("Check the port");
});

test("each integration says where it is used, with working links", async ({ page }) => {
  await gotoSettings(page);
  const spoolman = integration(page, "Spoolman");
  await expect(spoolman.getByRole("link", { name: "Spool picker on New quote" })).toHaveAttribute("href", "#/quote");
  await expect(spoolman.getByRole("link", { name: "Spool inventory on the Filament tab" })).toHaveAttribute("href", "#/filament");
  await expect(integration(page, "Moonraker").getByRole("link", { name: "Recent prints on New quote" })).toHaveAttribute("href", "#/quote");
  await expect(page.getByText(/Restart the container/)).toHaveCount(0);
});

test("Moonraker offers to add its printer; Not now hides the prompt until the URL changes", async ({ page }) => {
  await gotoSettings(page);
  const moonraker = integration(page, "Moonraker");
  await moonraker.getByLabel("Moonraker URL").fill(MOONRAKER);
  await moonraker.getByRole("button", { name: "Test" }).click();
  const prompt = moonraker.getByRole("group", { name: "Add printer from Moonraker" });
  await expect(prompt).toContainText("Add printer 'voron-e2e' to your printers?");

  await prompt.getByRole("button", { name: "Not now" }).click();
  await expect(prompt).toHaveCount(0);
  await expect(moonraker.getByRole("button", { name: "Test" })).toBeFocused();

  // Same URL: still hidden after another test.
  await moonraker.getByRole("button", { name: "Test" }).click();
  await expect(moonraker.getByText("Connected to voron-e2e")).toBeVisible();
  await expect(prompt).toHaveCount(0);

  // A different URL for the same server: the prompt returns.
  await moonraker.getByLabel("Moonraker URL").fill(`${MOONRAKER}/`);
  await moonraker.getByRole("button", { name: "Test" }).click();
  await expect(prompt).toBeVisible();
});

test("Add printer saves it, announces it, and links to the Printers tab", async ({ page }) => {
  await gotoSettings(page);
  const moonraker = integration(page, "Moonraker");
  await moonraker.getByLabel("Moonraker URL").fill(MOONRAKER);
  await moonraker.getByRole("button", { name: "Test" }).click();
  const prompt = moonraker.getByRole("group", { name: "Add printer from Moonraker" });
  await prompt.getByRole("button", { name: "Add printer" }).click();

  const added = moonraker.locator("[data-printer-added]");
  await expect(added).toContainText("Added printer 'voron-e2e'");
  await expect(added).toContainText("wattage, cost, and lifespan");
  await expect(added).toBeFocused();
  await expect(prompt).toHaveCount(0);

  // Save the URLs typed above too, so leaving for the Printers tab doesn't ask about unsaved changes.
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByText("Settings saved.")).toBeVisible();

  // The printer is saved on the server already.
  const cfg = (await (await page.request.get("/admin/api/config")).json()) as { printers: { name: string; watts: number }[] };
  expect(cfg.printers.filter((p) => p.name === "voron-e2e")).toHaveLength(1);

  await added.getByRole("link", { name: "Printers tab" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Printers" })).toBeVisible();
  await expect(page.locator('input[aria-label^="Printer "][aria-label$=" name"]').last()).toHaveValue("voron-e2e");

  // The prompt does not come back for a printer that exists.
  await gotoSettings(page, { load: false });
  await moonraker.getByRole("button", { name: "Test" }).click();
  await expect(moonraker.getByText("Connected to voron-e2e")).toBeVisible();
  await expect(moonraker.getByRole("group", { name: "Add printer from Moonraker" })).toHaveCount(0);
});

test("saved URLs apply without a restart", async ({ page }) => {
  await gotoSettings(page);
  await integration(page, "Spoolman").getByLabel("Spoolman URL").fill(SPOOLMAN);
  await integration(page, "Moonraker").getByLabel("Moonraker URL").fill(MOONRAKER);
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByText("Settings saved.")).toBeVisible();

  const spoolman = integration(page, "Spoolman");
  await expect(spoolman.getByText("Configured in settings")).toBeVisible();

  // The proxy follows the saved URL on the next request.
  await expect
    .poll(async () => (await page.request.get("/admin/api/spoolman/api/v1/spool")).status())
    .toBe(200);

  await navButton(page, "Filament").click();
  await expect(page.locator("#spoolman-inventory tr[data-spool-id='1']")).toBeVisible();
  await navButton(page, "New quote").click();
  await expect(page.getByLabel("Recent print (from Moonraker)")).toBeVisible();
  await expect(page.getByLabel("Spool (from Spoolman)").first()).toBeVisible();

  // Clearing a URL turns the integration off at once.
  await gotoSettings(page, { load: false });
  await integration(page, "Spoolman").getByLabel("Spoolman URL").fill("");
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByText("Settings saved.")).toBeVisible();
  await expect.poll(async () => (await page.request.get("/admin/api/spoolman/api/v1/spool")).status()).toBe(404);
  await navButton(page, "Filament").click();
  await expect(page.locator("#spoolman-inventory")).toContainText("Connect Spoolman");
});

// A save applies live (v1.1): the config refetch that follows "Settings saved." can land after the user has
// already opened the next page, so the integration-backed screens must follow it without a reload. The delay
// below makes that order deterministic.
async function delayConfig(page: Page, ms = 1500): Promise<void> {
  await page.route("**/api/config", async (route) => {
    await new Promise((resolve) => setTimeout(resolve, ms));
    await route.continue();
  });
}

async function saveUrls(page: Page, urls: { spoolman?: string; moonraker?: string }): Promise<void> {
  await gotoSettings(page, { load: false });
  if (urls.spoolman !== undefined) await integration(page, "Spoolman").getByLabel("Spoolman URL").fill(urls.spoolman);
  if (urls.moonraker !== undefined) await integration(page, "Moonraker").getByLabel("Moonraker URL").fill(urls.moonraker);
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByText("Settings saved.")).toBeVisible();
}

test("Filament inventory follows a Spoolman URL saved in the same page session", async ({ page }) => {
  // The previous test left Spoolman cleared.
  await page.goto("/admin/");
  await navButton(page, "Filament").click();
  await expect(page.locator("#spoolman-inventory")).toContainText("Connect Spoolman");

  await delayConfig(page);
  await saveUrls(page, { spoolman: SPOOLMAN });
  await navButton(page, "Filament").click();
  await expect(page.locator("#spoolman-inventory tr[data-spool-id='1']")).toBeVisible({ timeout: 8_000 });
  await expect(page.locator("#spoolman-inventory")).not.toContainText("Loading spools");
});

test("Filament inventory follows a changed Spoolman URL while it is loaded", async ({ page }) => {
  await page.goto("/admin/");
  await navButton(page, "Filament").click();
  await expect(page.locator("#spoolman-inventory tr[data-spool-id='1']")).toBeVisible();

  await delayConfig(page);
  await saveUrls(page, { spoolman: "http://127.0.0.1:9" });
  await navButton(page, "Filament").click();
  await expect(page.locator("#spoolman-inventory")).toContainText("Couldn't load the Spoolman inventory", { timeout: 8_000 });
  await expect(page.locator("#spoolman-inventory tr[data-spool-id='1']")).toHaveCount(0);

  await saveUrls(page, { spoolman: SPOOLMAN });
  await navButton(page, "Filament").click();
  await expect(page.locator("#spoolman-inventory tr[data-spool-id='1']")).toBeVisible({ timeout: 8_000 });
});

test("Recent prints and the spool picker follow URLs saved in the same page session", async ({ page }) => {
  await page.goto("/admin/");
  await saveUrls(page, { spoolman: "", moonraker: "" });
  await navButton(page, "New quote").click();
  await expect(page.getByLabel("Recent print (from Moonraker)")).toHaveCount(0);

  await delayConfig(page);
  await saveUrls(page, { spoolman: SPOOLMAN, moonraker: MOONRAKER });
  await navButton(page, "New quote").click();
  await expect(page.getByLabel("Recent print (from Moonraker)")).toBeVisible({ timeout: 8_000 });
  await expect(page.getByLabel("Recent print (from Moonraker)").locator("option")).not.toHaveCount(1);
  await expect(page.getByLabel("Spool (from Spoolman)").first().locator("option")).not.toHaveCount(1);
});
