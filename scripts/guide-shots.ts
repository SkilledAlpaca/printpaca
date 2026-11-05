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
// context, and saves WebP (quality 80). Every image the Guide references (web/src/lib/guideContent.ts) must be
// captured, and every captured key must be referenced: the script fails otherwise.
//
// The overlay is drawn with DOM nodes plus page.screenshot({ style }) because the app's CSP rejects injected
// <style> tags. Fonts come from the app (Figtree, bundled) and the e2e image's Noto fallbacks. Text that carries a
// date (the seed is relative to the day you run it) is kept out of the crops where possible.
import { spawn, type ChildProcess } from "node:child_process";
import { mkdirSync, mkdtempSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { chromium } from "@playwright/test";
import { GUIDE_IMAGE_KEYS } from "../web/src/lib/guideContent.ts";
import { Shooter, VIEWPORT } from "./lib/guide-shooter.ts";
import { REPO_ROOT, buildServer, restoreEmbeddedDist, startServer, stopServer } from "./lib/demo-server.ts";
import { captureAll } from "./lib/guide-captures.ts";

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
    await stopServer(server);
    server = await startServer(bin, { ...env, ADMIN_PASSWORD: SEED_PASSWORD }, ADMIN_PORT);
    stubs.push(spawn("node", [path.join(REPO_ROOT, "scripts", "e2e-spoolman-stub.mjs")], { env: { ...process.env, PORT: String(SPOOLMAN_PORT) }, stdio: "inherit" }));
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
      const sorted = Object.fromEntries(Object.entries(shooter.dims).sort(([a], [b]) => a.localeCompare(b)));
      writeFileSync(path.join(outDir, "dimensions.json"), JSON.stringify(sorted, null, 2) + "\n");
    }
    console.log(`[guide-shots] done: ${shooter.written.length} images`);
  } finally {
    await cleanup();
  }
}

await main();
