#!/usr/bin/env -S node --experimental-strip-types
// Regenerates docs/screenshots/*.png against a freshly built server, so the
// documentation screenshots show the current UI.
//
// Usage: node --experimental-strip-types scripts/screenshot-docs.ts
// (Node >=23.6 also runs it without the flag; the shebang passes it for the
// pinned Node 26.)
//
// Like scripts/e2e-server.sh: build web/dist, swap it into server/embedded/dist
// (go:embed needs it at build time), go build a throwaway binary, then restore
// the tracked placeholder (see restoreEmbeddedDist()).
//
// DEMO_MODE=1 seeds about six months of clients, quotes, invoices, payments,
// requests, and items into an empty CONFIG_DIR (server/demoseed.go), plus a
// demo config.yaml with PLA and PETG marked Public. The script leaves
// CONFIG_DIR empty so DEMO_MODE is the only source of fake data.
// ADMIN_PASSWORD=demo is the login (DEMO_MODE waives the length floor).
//
// Trade-off: DEMO_MODE renders a read-only demo banner (class="demo-banner")
// that a real deployment never shows. hideDemoBanner() removes it before every
// capture; nothing else is altered.
//
// Captures are 1680px-wide viewport shots into docs/screenshots/, 1100px tall
// unless CAPTURE_HEIGHTS says otherwise. Ports 8199 and 8198 avoid the ports scripts/e2e-server.sh and e2e/playwright.config.ts use,
// so this can run alongside `npm run e2e`.
import { execFileSync, spawn, type ChildProcess } from "node:child_process";
import { cpSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { chromium, type Browser, type Page } from "@playwright/test";

const REPO_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const OUT_DIR = path.join(REPO_ROOT, "docs", "screenshots");
const EMBEDDED_DIST = path.join(REPO_ROOT, "server", "embedded", "dist");

const ADMIN_PORT = 8199;
const PUBLIC_PORT = 8198;
const ADMIN_URL = `http://127.0.0.1:${ADMIN_PORT}`;
const PUBLIC_URL = `http://127.0.0.1:${PUBLIC_PORT}`;
const ADMIN_PASSWORD = "demo";

const VIEWPORT = { width: 1680, height: 1100 };

// Per-capture viewport heights, for pages whose content doesn't fit 1100px or
// that end in an unrelated card. Each height ends at a card boundary, so no
// capture cuts a table or card in half. Captures not listed use VIEWPORT.height.
const CAPTURE_HEIGHTS: Record<string, number> = {
  dashboard: 1340, // KPI tiles, revenue chart, and the whole Top items table
  invoices: 1115, // all seeded invoices
  requests: 860, // the requests list, without the settings card below it
  filament: 900, // Materials and Spool price cards
  printers: 1060, // Printer list and Presets cards
  settings: 1040, // Account, Currency, and Pricing cards
  "public-quote": 1300, // the form through the estimate card and submit button
};
const heightFor = (name: string): number => CAPTURE_HEIGHTS[name] ?? VIEWPORT.height;

// The README hero (docs/screenshot.png) uses a smaller viewport so its text
// stays legible at full README width.
const HERO_FILE = path.join(REPO_ROOT, "docs", "screenshot.png");
const HERO_VIEWPORT = { width: 1280, height: 900 };

// Removes the demo banner (App.svelte and PublicQuotePage.svelte share the
// class). It removes the element rather than styling it: the app's CSP
// (default-src 'self', no style-src) rejects injected styles, while
// page.evaluate() runs via CDP and is exempt.
async function hideDemoBanner(page: Page): Promise<void> {
  await page.evaluate(() => {
    document.querySelectorAll(".demo-banner").forEach((el) => el.remove());
  });
}

function run(cmd: string, args: string[], cwd: string): void {
  execFileSync(cmd, args, { cwd, stdio: "inherit" });
}

function restoreEmbeddedDist(): void {
  try {
    execFileSync("git", ["checkout", "--", "server/embedded/dist"], { cwd: REPO_ROOT, stdio: "ignore" });
  } catch {
    /* best-effort, mirrors e2e-server.sh's `|| true` */
  }
  try {
    execFileSync("git", ["clean", "-fdx", "server/embedded/dist"], { cwd: REPO_ROOT, stdio: "ignore" });
  } catch {
    /* best-effort, mirrors e2e-server.sh's `|| true` */
  }
}

async function waitForHealthz(url: string, timeoutMs = 60_000): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  let lastErr: unknown;
  while (Date.now() < deadline) {
    try {
      const res = await fetch(url);
      if (res.ok) return;
      lastErr = new Error(`${url} -> ${res.status}`);
    } catch (err) {
      lastErr = err;
    }
    await new Promise((r) => setTimeout(r, 250));
  }
  throw new Error(`server never became healthy at ${url}: ${String(lastErr)}`);
}

/** Fails loudly (throws) rather than silently taking a screenshot of the
 * wrong -- or blank -- page. Every capture below calls this first. */
async function requireVisible(page: Page, selector: string, label: string, timeoutMs = 15_000): Promise<void> {
  try {
    await page.locator(selector).first().waitFor({ state: "visible", timeout: timeoutMs });
  } catch (err) {
    throw new Error(`signature element missing for "${label}" (selector: ${selector}): ${String(err)}`);
  }
}

type CaptureResult = { name: string; file: string; route: string; width: number; height: number; bytes: number };

function pngDims(file: string): { width: number; height: number; bytes: number } {
  const buf = readFileSync(file);
  return { width: buf.readUInt32BE(16), height: buf.readUInt32BE(20), bytes: buf.length };
}

async function main(): Promise<void> {
  const scratch = mkdtempSync(path.join(tmpdir(), "printpaca-screenshot-docs-"));
  const configDir = path.join(scratch, "config");
  const binDir = path.join(scratch, "bin");
  mkdirSync(configDir, { recursive: true });
  mkdirSync(binDir, { recursive: true });

  let serverProc: ChildProcess | undefined;
  let browser: Browser | undefined;
  let embeddedDistRestored = false;
  const results: CaptureResult[] = [];

  const cleanup = () => {
    if (serverProc && serverProc.exitCode === null && !serverProc.killed) {
      serverProc.kill();
    }
    if (!embeddedDistRestored) {
      restoreEmbeddedDist();
      embeddedDistRestored = true;
    }
    rmSync(scratch, { recursive: true, force: true });
  };
  process.on("SIGINT", () => {
    cleanup();
    process.exit(130);
  });
  process.on("SIGTERM", () => {
    cleanup();
    process.exit(143);
  });

  try {
    console.log("[screenshot-docs] building web app...");
    run("npm", ["run", "build", "-w", "web"], REPO_ROOT);

    rmSync(EMBEDDED_DIST, { recursive: true, force: true });
    cpSync(path.join(REPO_ROOT, "web", "dist"), EMBEDDED_DIST, { recursive: true });

    const bin = path.join(binDir, "printpaca-screenshot-docs");
    console.log("[screenshot-docs] building server binary...");
    // Stamp the release version the way a tag build does (v + web/package.json version) instead of "dev".
    const { version } = JSON.parse(readFileSync(path.join(REPO_ROOT, "web", "package.json"), "utf8")) as { version: string };
    run("go", ["build", "-ldflags", `-X main.version=v${version}`, "-o", bin, "."], path.join(REPO_ROOT, "server"));

    // Success path: restore now; go:embed already baked the build into the binary.
    restoreEmbeddedDist();
    embeddedDistRestored = true;

    console.log(`[screenshot-docs] starting server on :${ADMIN_PORT} (public :${PUBLIC_PORT})...`);
    serverProc = spawn(bin, [], {
      cwd: REPO_ROOT,
      env: {
        ...process.env,
        PORT: String(ADMIN_PORT),
        PUBLIC_PORT: String(PUBLIC_PORT),
        CONFIG_DIR: configDir,
        ADMIN_PASSWORD,
        DEMO_MODE: "1",
      },
      stdio: ["ignore", "inherit", "inherit"],
    });
    // Don't throw here: an "error" event fires on a later tick, so a throw would
    // bypass main()'s try/finally and leak the scratch dir. Reject a promise and
    // race it against waitForHealthz instead.
    const spawnFailure = new Promise<never>((_, reject) => {
      serverProc!.on("error", (err) => {
        reject(new Error(`server process failed to start: ${String(err)}`));
      });
    });

    await Promise.race([waitForHealthz(`${ADMIN_URL}/healthz`), spawnFailure]);
    // After healthz wins the race, a late "error" event would reject spawnFailure
    // with no awaiter and Node would terminate on the unhandled rejection.
    spawnFailure.catch(() => {});

    browser = await chromium.launch();
    const context = await browser.newContext({ viewport: VIEWPORT, deviceScaleFactor: 1 });

    // ---------------------------------------------------------------
    // Admin tabs
    // ---------------------------------------------------------------
    const admin = await context.newPage();
    await admin.goto(`${ADMIN_URL}/admin/`);
    await admin.getByLabel("Password").fill(ADMIN_PASSWORD);
    await admin.getByRole("button", { name: "Sign in" }).click();
    const nav = admin.getByRole("navigation", { name: "Sections" });
    // Use nav.side-nav: at 1680px the header hides its own copy of this nav, and
    // both share one aria-label, so a bare selector hits the hidden copy first.
    await requireVisible(admin, "nav.side-nav", "admin sidebar nav after login");
    // Removed once at the shell level; the SPA keeps it gone across tab switches.
    await hideDemoBanner(admin);

    async function captureTab(
      name: string,
      tabLabel: string,
      verify: () => Promise<void>,
    ): Promise<void> {
      // Not exact: the Requests button's name gains an "N unread requests" suffix
      // when seeded requests are unreviewed.
      await admin.setViewportSize({ width: VIEWPORT.width, height: heightFor(name) });
      await nav.getByRole("button", { name: tabLabel }).click();
      await verify();
      const file = path.join(OUT_DIR, `${name}.png`);
      await admin.screenshot({ path: file });
      await admin.setViewportSize(VIEWPORT);
      const dims = pngDims(file);
      results.push({ name, file, route: `#/${name}`, ...dims });
      console.log(`[screenshot-docs] captured ${name}.png (${dims.width}x${dims.height}, ${dims.bytes} bytes)`);
    }

    type TabSpec = { name: string; tabLabel: string; verify: () => Promise<void> };

    const tabSpecs: TabSpec[] = [
      {
        name: "dashboard",
        tabLabel: "Dashboard",
        verify: async () => {
          await requireVisible(admin, 'h1:has-text("Dashboard")', "Dashboard heading");
          await requireVisible(admin, 'h2:has-text("Recent invoices")', "Recent invoices heading");
        },
      },
      {
        name: "quote",
        tabLabel: "New quote",
        verify: async () => {
          await requireVisible(admin, 'h1:has-text("New quote")', "New quote heading");
          // Fill weight and time so the breakdown renders real numbers.
          await admin.getByLabel("Model weight (g)").fill("180");
          await admin.getByLabel("Print time (hours)").fill("4");
          await admin.getByLabel("Print time (minutes)").fill("0");
          await requireVisible(admin, 'h2:has-text("Cost breakdown")', "Cost breakdown heading");
          // Show the client price and profit, not the "Set a price" button.
          await admin.getByRole("button", { name: "Set a price" }).click();
          await admin.locator(".card.summary .group.sell .stat-value").filter({ hasText: /\d/ }).first().waitFor({ state: "visible", timeout: 15_000 });
          // Pricing is server-side and debounced: wait for an amount, not the
          // "-" placeholder, then scroll <main> back to the top.
          await admin.locator(".card.summary .stat.cost .stat-value").filter({ hasText: /\d/ }).first().waitFor({ state: "visible", timeout: 15_000 });
          await admin.evaluate(() => document.querySelector("main")?.scrollTo(0, 0));
        },
      },
      {
        name: "electricity",
        tabLabel: "Electricity",
        verify: async () => {
          await requireVisible(admin, 'h1:has-text("Electricity")', "Electricity heading");
          await requireVisible(admin, 'h2:has-text("Electricity calculator")', "Electricity calculator heading");
          // The saved-settings editor loads after the calculator; wait for it
          // so the capture does not show its "Loading…" placeholder.
          await requireVisible(admin, 'h2:has-text("Saved energy settings")', "energy settings editor");
        },
      },
      {
        name: "filament",
        tabLabel: "Filament",
        verify: async () => {
          await requireVisible(admin, 'h1:has-text("Filament")', "Filament heading");
          await requireVisible(admin, 'input[aria-label="Material 1 name"]', "material row");
          const materialName = await admin.getByLabel("Material 1 name").inputValue();
          if (materialName !== "PLA") throw new Error(`expected material row 1 to be PLA, got ${JSON.stringify(materialName)}`);
          // Confirms PLA is public; the public-quote capture depends on it.
          const publicChecked = await admin.getByLabel("Material 1 public").isChecked();
          if (!publicChecked) throw new Error("expected demo-seeded config.yaml to mark Material 1 (PLA) public");
        },
      },
      {
        name: "printers",
        tabLabel: "Printers",
        verify: async () => {
          await requireVisible(admin, 'h1:has-text("Printers")', "Printers heading");
          await requireVisible(admin, 'input[aria-label="Printer 1 name"]', "printer row");
          const printerName = await admin.getByLabel("Printer 1 name").inputValue();
          if (printerName !== "Generic FDM") throw new Error(`expected printer row 1 to be "Generic FDM", got ${JSON.stringify(printerName)}`);
        },
      },
      {
        name: "clients",
        tabLabel: "Clients",
        verify: async () => {
          await requireVisible(admin, 'h1:has-text("Clients")', "Clients heading");
          await requireVisible(admin, 'text=Alex Merriweather', "seeded client row");
        },
      },
      {
        name: "invoices",
        tabLabel: "Invoices",
        verify: async () => {
          await requireVisible(admin, 'h1:has-text("Invoices")', "Invoices heading");
          await requireVisible(admin, "button.open-btn", "seeded invoice row");
        },
      },
      {
        name: "items",
        tabLabel: "Items",
        verify: async () => {
          await requireVisible(admin, 'h1:has-text("Items")', "Items heading");
          await requireVisible(admin, 'text=Cable Clip 8-Pack', "seeded item row");
        },
      },
      {
        name: "requests",
        tabLabel: "Requests",
        verify: async () => {
          await requireVisible(admin, 'h1:has-text("Requests")', "Requests heading");
          await requireVisible(admin, "span.r-name", "seeded request row");
        },
      },
      {
        name: "settings",
        tabLabel: "Settings",
        verify: async () => {
          await requireVisible(admin, 'h1:has-text("Settings")', "Settings heading");
          await requireVisible(admin, 'h2:has-text("Currency")', "Currency section heading");
        },
      },
    ];

    for (const spec of tabSpecs) {
      await captureTab(spec.name, spec.tabLabel, spec.verify);
    }

    // README hero (docs/screenshot.png): New quote with a filled-in calculation.
    // A separate context, because viewport is a context-level Playwright setting.
    const heroContext = await browser.newContext({ viewport: HERO_VIEWPORT, deviceScaleFactor: 1 });
    const hero = await heroContext.newPage();
    await hero.goto(`${ADMIN_URL}/admin/`);
    await hero.getByLabel("Password").fill(ADMIN_PASSWORD);
    await hero.getByRole("button", { name: "Sign in" }).click();
    await requireVisible(hero, "nav.side-nav", "hero sidebar nav after login");
    await hideDemoBanner(hero);
    await hero.getByRole("navigation", { name: "Sections" }).getByRole("button", { name: "New quote" }).click();
    await requireVisible(hero, 'h1:has-text("New quote")', "hero New quote heading");
    await hero.getByLabel("Model weight (g)").fill("127");
    await hero.getByLabel("Print time (hours)").fill("7");
    await hero.getByLabel("Print time (minutes)").fill("30");
    await hero.getByLabel("Quantity (units)").fill("3");
    await requireVisible(hero, 'h2:has-text("Cost breakdown")', "hero cost breakdown");
    // Show the client price and profit, not the "Set a price" button.
    await hero.getByRole("button", { name: "Set a price" }).click();
    await hero.locator(".card.summary .group.sell .stat-value").filter({ hasText: /\d/ }).first().waitFor({ state: "visible", timeout: 15_000 });
    // Pricing is server-side and debounced: wait for an amount, not the "-"
    // placeholder.
    const heroPrice = hero.locator(".card.summary .stat.cost .stat-value");
    await heroPrice.filter({ hasText: /\d/ }).first().waitFor({ state: "visible", timeout: 15_000 });
    // Filling fields scrolled <main>; scroll back to the top before capture.
    await hero.evaluate(() => document.querySelector("main")?.scrollTo(0, 0));
    await hero.screenshot({ path: HERO_FILE });
    const heroDims = pngDims(HERO_FILE);
    results.push({ name: "hero", file: HERO_FILE, route: "#/ (New quote)", ...heroDims });
    console.log(`[screenshot-docs] captured screenshot.png (${heroDims.width}x${heroDims.height}, ${heroDims.bytes} bytes)`);

    await heroContext.close();

    // ---------------------------------------------------------------
    // Public quote page (separate listener/port, unauthenticated)
    // ---------------------------------------------------------------
    const pub = await context.newPage();
    await pub.goto(`${PUBLIC_URL}/order`);
    await requireVisible(pub, "h1", "public page heading");
    // Hide the banner only after the heading renders: config loads asynchronously
    // and the banner reappears if removed earlier.
    await hideDemoBanner(pub);
    // Seeded items make the page open on the Gallery; switch to the quote form.
    await pub.getByRole("button", { name: "Custom estimate" }).click();
    await pub.getByLabel("Material").selectOption({ label: "PLA" });
    await pub.getByLabel("Model weight (g)").fill("120");
    await pub.getByLabel("Print time (hours)").fill("3");
    await pub.getByLabel("Print time (minutes)").fill("30");
    const priceLocator = pub.locator(".card.estimate .price");
    await requireVisible(pub, ".card.estimate .price", "server-priced estimate");
    const priceText = await priceLocator.first().innerText();
    if (!/[$0-9]/.test(priceText)) {
      throw new Error(`expected a formatted price in the estimate card, got: ${JSON.stringify(priceText)}`);
    }
    await pub.setViewportSize({ width: VIEWPORT.width, height: heightFor("public-quote") });
    const publicQuoteFile = path.join(OUT_DIR, "public-quote.png");
    await pub.screenshot({ path: publicQuoteFile });
    const pqDims = pngDims(publicQuoteFile);
    results.push({ name: "public-quote", file: publicQuoteFile, route: "/ (Custom estimate view)", ...pqDims });
    console.log(`[screenshot-docs] captured public-quote.png (${pqDims.width}x${pqDims.height}, ${pqDims.bytes} bytes)`);

    await context.close();
  } finally {
    if (browser) await browser.close().catch(() => {});
    if (serverProc && serverProc.exitCode === null) serverProc.kill();
    cleanup();
  }

  // ---------------------------------------------------------------
  // Verify every capture: correct dimensions, non-trivial size.
  // ---------------------------------------------------------------
  const expectedNames = [
    "clients",
    "dashboard",
    "electricity",
    "filament",
    "invoices",
    "items",
    "printers",
    "public-quote",
    "quote",
    "requests",
    "settings",
  ];
  for (const name of expectedNames) {
    const r = results.find((x) => x.name === name);
    if (!r) throw new Error(`missing capture for ${name}.png`);
    const wantHeight = heightFor(name);
    if (r.width !== VIEWPORT.width || r.height !== wantHeight) {
      throw new Error(`${name}.png is ${r.width}x${r.height}, expected ${VIEWPORT.width}x${wantHeight}`);
    }
    if (r.bytes < 20_000) {
      throw new Error(`${name}.png is suspiciously small (${r.bytes} bytes)`);
    }
  }

  // The hero has its own viewport, so it is verified apart from the loop above.
  const heroResult = results.find((x) => x.name === "hero");
  if (!heroResult) throw new Error("missing capture for docs/screenshot.png");
  if (heroResult.width !== HERO_VIEWPORT.width || heroResult.height !== HERO_VIEWPORT.height) {
    throw new Error(`screenshot.png is ${heroResult.width}x${heroResult.height}, expected ${HERO_VIEWPORT.width}x${HERO_VIEWPORT.height}`);
  }
  if (heroResult.bytes < 20_000) {
    throw new Error(`screenshot.png is suspiciously small (${heroResult.bytes} bytes)`);
  }

  const remaining = readdirSync(OUT_DIR).filter((f) => f.endsWith(".png") && !expectedNames.includes(f.replace(/\.png$/, "")));
  if (remaining.length > 0) {
    console.warn(`[screenshot-docs] unexpected leftover PNGs in ${OUT_DIR}: ${remaining.join(", ")}`);
  }

  console.log("\n[screenshot-docs] done. Captures:");
  for (const r of results) {
    console.log(`  ${r.name}.png  ${r.width}x${r.height}  ${r.bytes} bytes  route: ${r.route}`);
  }
}

main().catch((err) => {
  console.error("[screenshot-docs] FAILED:", err);
  process.exitCode = 1;
});
