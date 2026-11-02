import { expect, request as pwRequest } from "@playwright/test";
import type { Locator, Page } from "@playwright/test";

/** A minimal valid POST /api/quotes body: one part in the {parts} inputs bag and the matching
 * formState, written field for field as the editor's toFormState does (every PartFormState and OrderForm key,
 * at the web state defaults except where the engine part below overrides them), so the saved quote lists,
 * opens in the editor, and recomputes like one the editor saved. */
export function seedQuoteBody(label: string): Record<string, unknown> {
  const part = {
    label: "Part 1",
    weightG: 10,
    pricePerKg: 20,
    wastePct: 0,
    timeHours: 1,
    timeMinutes: 0,
    watts: 100,
    energy: { mode: "flat", flatRate: 0.1 },
    failureRatePct: 0,
    prepMinutes: 0,
    laborRate: 0,
    machineCost: 0,
    machineLifeHours: 0,
    monthlyFixed: 0,
    monthlyHours: 0,
    quantity: 1,
  };
  return {
    label,
    clientName: "",
    project: "",
    reference: "",
    status: "draft",
    statusLog: [],
    inputs: { parts: [part] },
    options: { laborPerUnit: false },
    formState: {
      version: 1,
      parts: [
        {
          label: "Part 1",
          weightG: 10,
          timeHours: 1,
          timeMinutes: 0,
          pricePerKg: 20,
          wastePct: 0,
          printerId: "",
          materialId: "",
          watts: 100,
          machineCost: 0,
          machineLifeHours: 0,
          flatRate: 0.1,
          offPeakFrac: 0.7,
          failureRatePct: 0,
          prepMinutes: 0,
          laborRate: 0,
          monthlyFixed: 0,
          monthlyHours: 0,
          quantity: 1,
          hardware: [],
          presetName: "",
          presetModified: false,
          salePriceOverride: null,
          spoolOverride: null,
        },
      ],
      order: {
        sellingEnabled: false,
        strategy: "margin",
        marginPct: 30,
        markupMultiplier: 2.5,
        taxRatePct: 0,
        shippingCharge: 0,
        shippingCost: 0,
        feeScheduleName: "none",
      },
    },
  };
}

/** Fills the canonical quote case (50 g, 1 h 20 m, config defaults), which prices to $2.24. */
export async function fillCanonicalQuote(page: Page): Promise<void> {
  await page.getByLabel(/Model weight/).fill("50");
  // The hours and minutes inputs carry their own aria-labels; an unanchored /Print time/ also matches
  // the wrapping role=group, so anchor the hours lookup.
  await page.getByLabel(/^Print time \(h/).fill("1");
  await page.getByLabel(/Print time \(minutes\)/).fill("20");
}

/** The "Recent saves" strip on the Quote page, scoped by heading so a bare `li` can't match wrongly. */
function historyList(page: Page): Locator {
  return page.locator(".card", { has: page.getByRole("heading", { name: /Saved quotes/ }) });
}

/** A saved-quote row in the recent-saves strip, matched by label text. */
export function historyRow(page: Page, label: string): Locator {
  return historyList(page).locator("li", { hasText: label });
}

/** A row in the Quotes ledger (#/quotes) table, matched by label text. */
export function quotesRow(page: Page, label: string): Locator {
  return page.locator("table tbody tr", { hasText: label });
}

/** Approximate PDF page count from the cleartext `/Type /Page` markers; an upper bound, not a parser. */
export function countPdfPages(pdf: Buffer): number {
  const text = pdf.toString("latin1");
  const matches = text.match(/\/Type\s*\/Page(?!s)\b/g);
  return matches ? matches.length : 0;
}

/** A button in the "Sections" nav landmark, matched from the start of its name: a suffix such as an unread
 * count still matches, but the "More settings" pill doesn't match "Settings". */
export function navButton(page: Page, name: string): Locator {
  const escaped = name.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  return page
    .getByRole("navigation", { name: "Sections" })
    .getByRole("button", { name: new RegExp(`^${escaped}\\b`, "i") });
}

/** Opens Settings through the nav; `load: false` skips the page.goto("/admin/") for an already-loaded app. */
export async function gotoSettings(page: Page, { load = true }: { load?: boolean } = {}): Promise<void> {
  if (load) await page.goto("/admin/");
  await navButton(page, "Settings").click();
  await expect(page).toHaveURL(/#\/settings$/);
}

/** Stubs window.print(): headless Chromium fires afterprint at once and App then clears #print-view. */
export async function stubWindowPrint(page: Page): Promise<void> {
  await page.addInitScript(() => {
    window.print = () => {};
  });
}

/** Extracts the first "$1,234.56"-shaped substring of a locator's text as a number of dollars. */
export async function readMoney(locator: Locator): Promise<number> {
  const text = (await locator.textContent()) ?? "";
  const match = text.match(/-?\$[\d,]+(?:\.\d+)?/);
  if (!match) throw new Error(`no money amount found in ${JSON.stringify(text)}`);
  return parseFloat(match[0].replace(/[$,]/g, ""));
}

/** The second server (ADMIN_PASSWORD set) used by auth, users and users-email. */
export const ADMIN_URL = "http://127.0.0.1:9099/admin/";
export const ADMIN_PASSWORD = "e2e-admin-secret"; // must match playwright.config.ts's webServer env

export async function loginAsAdmin(page: Page): Promise<void> {
  await page.goto(ADMIN_URL);
  await page.getByLabel("Password").fill(ADMIN_PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(navButton(page, "New quote")).toBeVisible();
}

/** Resets the :9099 server over the API: delete every employee, clear the admin Access email, and turn
 * multi-user mode off. Runs even if an earlier failure skipped the UI cleanup. */
export async function resetServerState(): Promise<void> {
  const api = await pwRequest.newContext({ baseURL: ADMIN_URL });
  try {
    const login = await api.post("api/login", { data: { username: "", password: ADMIN_PASSWORD } });
    expect(login.ok()).toBe(true);
    const users = await api.get("api/users");
    if (users.ok()) {
      const body = (await users.json()) as { users?: { id: string }[] } | { id: string }[];
      const list = Array.isArray(body) ? body : (body.users ?? []);
      for (const u of list) {
        expect((await api.delete(`api/users/${u.id}`)).ok()).toBe(true);
      }
    }
    const cfgRes = await api.get("api/config");
    const cfg = (await cfgRes.json()) as Record<string, unknown> & { users: { multiUser: boolean; accessAdminEmail: string } };
    if (cfg.users.multiUser || cfg.users.accessAdminEmail !== "") {
      for (const k of ["integrations", "version", "authEnabled", "moonraker", "fluiddUrl", "demoMode", "integrationSources", "user"]) {
        delete cfg[k];
      }
      cfg.users = { ...cfg.users, multiUser: false, accessAdminEmail: "" };
      const put = await api.put("api/settings", { data: { config: cfg } });
      expect(put.ok(), await put.text()).toBe(true);
    }
  } finally {
    await api.dispose();
  }
}
