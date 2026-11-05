#!/usr/bin/env -S node --experimental-strip-types
// Regenerates the Guide's images (web/src/assets/guide/*.webp plus dimensions.json) against a freshly built
// server, so the in-app Guide and docs/getting-started.md show the current UI.
//
// Usage:
//   node --experimental-strip-types scripts/guide-shots.ts              build, seed, capture every image
//   node --experimental-strip-types scripts/guide-shots.ts --only quote  capture only keys starting with "quote"
//   node --experimental-strip-types scripts/guide-shots.ts --bin <path>  reuse a built server binary (skips the build)
//   node --experimental-strip-types scripts/guide-shots.ts --out <dir>   write somewhere else (check determinism)
//
// What it does: builds web/dist and a throwaway server (scripts/lib/demo-server.ts), seeds fictional data (the
// DEMO_MODE seed, then a restart with DEMO_MODE off so the script can also write), starts the Spoolman and
// Moonraker stubs, drives the admin app in light theme at a fixed 1280x1500 viewport with reduced motion, and for
// each image draws a 3 px accent ring and a numbered badge around the target, crops to the region plus 24 px of
// context, and saves WebP (quality 45, crops at most 1200 px wide). Every image the Guide references (web/src/lib/guideContent.ts) must be
// captured, and every captured key must be referenced: the script fails otherwise.
//
// The overlay is drawn with DOM nodes plus page.screenshot({ style }) because the app's CSP rejects injected
// <style> tags. Fonts come from the app (Figtree, bundled) and the e2e image's Noto fallbacks. Text that carries a
// date (the seed is relative to the day you run it) is kept out of the crops where possible.
import { spawn, type ChildProcess } from "node:child_process";
import { mkdirSync, mkdtempSync, readdirSync, rmSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { chromium } from "@playwright/test";
import { GUIDE_IMAGE_KEYS } from "../web/src/lib/guideContent.ts";
import { Shooter, VIEWPORT } from "./lib/guide-shooter.ts";
import { REPO_ROOT, buildServer, restoreEmbeddedDist, startServer, stopServer } from "./lib/demo-server.ts";
import { captureAll } from "./lib/guide-captures.ts";

const GUIDE_IMAGE_BUDGET_BYTES = 700_000;
const ADMIN_PORT = 8197;
const PUBLIC_PORT = 8196;
const SPOOLMAN_PORT = 9327;
const MOONRAKER_PORT = 9325;
const ADMIN_URL = `http://127.0.0.1:${ADMIN_PORT}`;
const SEED_PASSWORD = "demo-password-1";

function arg(name: string): string | undefined {
  const i = process.argv.indexOf(`--${name}`);
  return i >= 0 ? process.argv[i + 1] : undefined;
}

interface DemoSpool {
  id: number;
  filament: { name: string; material: string; density: number; weight: number; price: number; vendor: { name: string } };
  [k: string]: unknown;
}

/** Reads the DEMO_MODE server's fake Spoolman spools and shapes them as the stub's fixture: vendors and
 * filaments get ids (the demo data has none), and each spool links its filament. */
async function demoSpoolmanFixture(adminUrl: string): Promise<{ vendors: unknown[]; filaments: unknown[]; spools: unknown[] }> {
  const login = await fetch(`${adminUrl}/admin/api/login`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ username: "", password: "demo" }) });
  if (!login.ok) throw new Error(`demo login failed: ${login.status}`);
  const cookie = (login.headers.getSetCookie?.() ?? []).map((c) => c.split(";")[0]).join("; ");
  const res = await fetch(`${adminUrl}/admin/api/spoolman/api/v1/spool`, { headers: { Cookie: cookie } });
  if (!res.ok) throw new Error(`demo spools: ${res.status}`);
  const demo = (await res.json()) as DemoSpool[];
  const vendors: { id: number; name: string }[] = [];
  const filaments: Record<string, unknown>[] = [];
  const spools = demo.map((s) => {
    let vendor = vendors.find((v) => v.name === s.filament.vendor.name);
    if (!vendor) vendors.push((vendor = { id: vendors.length + 1, name: s.filament.vendor.name }));
    const filament = { ...s.filament, id: filaments.length + 1, diameter: 1.75, vendor };
    filaments.push(filament);
    return { ...s, filament, location: "", archived: false };
  });
  return { vendors, filaments, spools };
}

async function main(): Promise<void> {
  const outDir = path.resolve(arg("out") ?? path.join(REPO_ROOT, "web", "src", "assets", "guide"));
  const only = arg("only");
  mkdirSync(outDir, { recursive: true });
  const scratch = mkdtempSync(path.join(tmpdir(), "printpaca-guide-shots-"));
  const configDir = path.join(scratch, "config");
  mkdirSync(configDir, { recursive: true });
  const stubs: ChildProcess[] = [];
  let server: ChildProcess | undefined;
  const cleanup = async () => {
    await stopServer(server);
    for (const s of stubs) s.kill();
    restoreEmbeddedDist();
    rmSync(scratch, { recursive: true, force: true });
  };
  process.on("SIGINT", () => void cleanup().then(() => process.exit(130)));
  try {
    let bin = arg("bin");
    if (!bin) {
      bin = path.join(scratch, "printpaca-guide-shots");
      console.log("[guide-shots] building...");
      buildServer(bin);
    }
    const env = { PORT: String(ADMIN_PORT), PUBLIC_PORT: String(PUBLIC_PORT), CONFIG_DIR: configDir, ADMIN_PASSWORD: "demo" };
    // First start seeds the fictional data (DEMO_MODE), the second runs with writes enabled on the same data.
    server = await startServer(bin, { ...env, DEMO_MODE: "1" }, ADMIN_PORT);
    // The Spoolman stub serves the demo's own fake spools, read from the DEMO_MODE server before it stops, so
    // the Filament list, the quotes and the Spoolman images all show one set of filament and prices.
    const spoolFixture = path.join(scratch, "spoolman-fixture.json");
    writeFileSync(spoolFixture, JSON.stringify(await demoSpoolmanFixture(ADMIN_URL)));
    await stopServer(server);
    server = await startServer(bin, { ...env, ADMIN_PASSWORD: SEED_PASSWORD }, ADMIN_PORT);
    stubs.push(spawn("node", [path.join(REPO_ROOT, "scripts", "e2e-spoolman-stub.mjs")], { env: { ...process.env, PORT: String(SPOOLMAN_PORT), SPOOLMAN_FIXTURE: spoolFixture }, stdio: "inherit" }));
    stubs.push(spawn("node", [path.join(REPO_ROOT, "scripts", "e2e-moonraker-stub.mjs")], { env: { ...process.env, PORT: String(MOONRAKER_PORT) }, stdio: "inherit" }));

    const browser = await chromium.launch();
    const context = await browser.newContext({ viewport: VIEWPORT, deviceScaleFactor: 1, reducedMotion: "reduce", colorScheme: "light" });
    const page = await context.newPage();
    page.on("dialog", (d) => void d.accept());
    const encoder = await context.newPage();
    await encoder.goto("about:blank");
    await page.goto(`${ADMIN_URL}/admin/`);
    await page.getByLabel("Password").fill(SEED_PASSWORD);
    await page.getByRole("button", { name: "Sign in" }).click();
    await page.locator("nav.side-nav").waitFor({ state: "visible" });

    const shooter = new Shooter(page, encoder, outDir, only);
    await captureAll(shooter, { adminUrl: ADMIN_URL, publicUrl: `http://127.0.0.1:${PUBLIC_PORT}`, spoolmanUrl: `http://127.0.0.1:${SPOOLMAN_PORT}`, moonrakerUrl: `http://127.0.0.1:${MOONRAKER_PORT}`, browser });
    await browser.close();

    if (!only) {
      const missing = GUIDE_IMAGE_KEYS.filter((k) => !shooter.written.includes(k));
      if (missing.length) throw new Error(`guideContent.ts references images the script did not capture: ${missing.join(", ")}`);
      for (const f of readdirSync(outDir)) {
        if (f.endsWith(".webp") && !shooter.written.includes(f.slice(0, -5))) rmSync(path.join(outDir, f));
      }
      // The folder stays under 700 KB. Fail rather than commit images that blow the image-size budget.
      const total = readdirSync(outDir).filter((f) => f.endsWith(".webp")).reduce((n, f) => n + statSync(path.join(outDir, f)).size, 0);
      console.log(`[guide-shots] total ${total} bytes (budget ${GUIDE_IMAGE_BUDGET_BYTES})`);
      if (total > GUIDE_IMAGE_BUDGET_BYTES) throw new Error(`guide images total ${total} bytes, over the ${GUIDE_IMAGE_BUDGET_BYTES} byte budget`);
      const sorted = Object.fromEntries(Object.entries(shooter.dims).sort(([a], [b]) => a.localeCompare(b)));
      writeFileSync(path.join(outDir, "dimensions.json"), JSON.stringify(sorted, null, 2) + "\n");
    }
    console.log(`[guide-shots] done: ${shooter.written.length} images`);
  } finally {
    await cleanup();
  }
}

await main();
