// Draws the numbered highlight, crops, and encodes one Guide image. Used by scripts/guide-shots.ts.
import { writeFileSync } from "node:fs";
import path from "node:path";
import type { Locator, Page } from "@playwright/test";
import { GUIDE_IMAGE_KEYS } from "../../web/src/lib/guideContent.ts";

export const VIEWPORT = { width: 1440, height: 1500 };
const PAD = 24;

export type Target = { loc: Locator; n: number };
export type ShotSpec = {
  key: string;
  targets: Target[];
  /** The crop region. Defaults to the union of the targets. Give a card or fieldset so the heading shows. */
  region?: Locator | Locator[];
  /** Capture another page (the public shop) instead of the admin app. */
  page?: Page;
};

// Light-theme ring and badge from the app's tokens: --focus-ring (#9c4212) is 5.5:1 on the surfaces, and white on
// --accent-text is 6.9:1. Styles go through the CSSOM (el.style): the app's CSP rejects injected <style> tags and
// page.screenshot({ style }).
const RING_STYLE: Record<string, string> = {
  position: "fixed", "z-index": "2147483646", "box-sizing": "border-box", border: "3px solid var(--focus-ring, #9c4212)",
  "border-radius": "9px", "pointer-events": "none",
};
const BADGE_STYLE: Record<string, string> = {
  position: "fixed", "z-index": "2147483647", width: "26px", height: "26px", "border-radius": "50%",
  background: "var(--accent-text, #9c4212)", color: "#fff", font: "700 15px/26px Figtree, sans-serif", "text-align": "center",
  "box-shadow": "0 0 0 2px #fff", "pointer-events": "none",
};

/** Replaces every "Mon D, YYYY[, h:mm AM]" in the page with the same offset from a fixed "today" (Nov 4, 2026), so
 * images don't change with the day you run the script. A time on today's date becomes 9:30 AM. */
export async function freezeDates(page: Page): Promise<void> {
  await page.evaluate(() => {
    const FIXED_TODAY = Date.UTC(2026, 10, 4);
    const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
    const re = /\b(Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec) (\d{1,2}), (\d{4})(, (\d{1,2}):(\d{2}) ([AP]M))?/g;
    const now = new Date();
    const realToday = Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate());
    // A node already rewritten (and not changed by the app since) keeps its fixed text: running twice must not shift it again.
    const w = window as unknown as { __guideFrozen?: WeakMap<Text, string> };
    const done = (w.__guideFrozen ??= new WeakMap());
    // Date inputs show a fixed day the same way. Only the displayed value changes (no input event), so the app still
    // believes it is the real day: "Record $2.00 bank transfer, today" stays true.
    const inputs = (w as unknown as { __guideInputs?: WeakMap<HTMLInputElement, string> });
    const seen = (inputs.__guideInputs ??= new WeakMap());
    for (const el of Array.from(document.querySelectorAll<HTMLInputElement>('input[type="date"]'))) {
      if (!el.value || seen.get(el) === el.value) continue;
      const [y, m, d] = el.value.split("-").map(Number);
      const diffDays = Math.round((realToday - Date.UTC(y!, m! - 1, d!)) / 86_400_000);
      const f = new Date(FIXED_TODAY - diffDays * 86_400_000);
      el.value = `${f.getUTCFullYear()}-${String(f.getUTCMonth() + 1).padStart(2, "0")}-${String(f.getUTCDate()).padStart(2, "0")}`;
      seen.set(el, el.value);
    }
    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
    const nodes: Text[] = [];
    while (walker.nextNode()) nodes.push(walker.currentNode as Text);
    for (const n of nodes) {
      const v = n.nodeValue ?? "";
      if (done.get(n) === v) continue;
      re.lastIndex = 0;
      if (!re.test(v)) continue;
      re.lastIndex = 0;
      const out = v.replace(re, (_m, mon, day, year, _t, hh, mm, ap) => {
        const d = Date.UTC(Number(year), MONTHS.indexOf(mon), Number(day));
        const diffDays = Math.round((realToday - d) / 86_400_000);
        const f = new Date(FIXED_TODAY - diffDays * 86_400_000);
        const date = `${MONTHS[f.getUTCMonth()]} ${f.getUTCDate()}, ${f.getUTCFullYear()}`;
        if (!hh) return date;
        return diffDays === 0 ? `${date}, 9:30 AM` : `${date}, ${hh}:${mm} ${ap}`;
      });
      n.nodeValue = out;
      done.set(n, out);
    }
  });
}

export class Shooter {
  readonly written: string[] = [];
  readonly dims: Record<string, { width: number; height: number }> = {};
  readonly page: Page;
  private readonly encoder: Page;
  private readonly outDir: string;
  private readonly only: string | undefined;
  constructor(page: Page, encoder: Page, outDir: string, only: string | undefined) {
    this.page = page;
    this.encoder = encoder;
    this.outDir = outDir;
    this.only = only;
  }

  wanted(key: string): boolean {
    return !this.only || key.startsWith(this.only);
  }

  async shot(spec: ShotSpec): Promise<void> {
    const page = spec.page ?? this.page;
    if (!GUIDE_IMAGE_KEYS.includes(spec.key)) throw new Error(`image key "${spec.key}" is not referenced by guideContent.ts`);
    if (!this.wanted(spec.key)) return;
    await page.getByText("Updating price").first().waitFor({ state: "hidden", timeout: 15_000 }).catch(() => {});
    await freezeDates(this.page);
    const first = spec.targets[0]!.loc.first();
    await first.waitFor({ state: "visible", timeout: 15_000 });
    // <main> scrolls, not the window; bring the target into view, then read viewport coordinates.
    await first.scrollIntoViewIfNeeded();
    await page.evaluate(() => document.fonts.ready);
    await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    // Park the pointer: a hovered field shows its number spinner or hover fill.
    await page.mouse.move(0, 0);
    await page.waitForTimeout(600);
    const rect = async (l: Locator) => {
      const b = await l.first().boundingBox();
      if (!b) throw new Error(`no box for a target of ${spec.key}`);
      return b;
    };
    const boxes = [];
    // Whole pixels, so a fractional scroll offset can't move a ring by a sub-pixel between runs.
    const whole = (b: { x: number; y: number; width: number; height: number }) => ({ x: Math.round(b.x), y: Math.round(b.y), width: Math.round(b.width), height: Math.round(b.height) });
    for (const t of spec.targets) boxes.push({ n: t.n, b: whole(await rect(t.loc)) });
    const regions = spec.region ? (Array.isArray(spec.region) ? spec.region : [spec.region]) : [];
    const regionBoxes = [];
    for (const r of regions) regionBoxes.push(whole(await rect(r)));
    await page.evaluate(({ items, ringStyle, badgeStyle }) => {
      for (const { n, b } of items) {
        const ring = document.createElement("div");
        ring.dataset.guide = "1";
        for (const [k, v] of Object.entries(ringStyle)) ring.style.setProperty(k, v);
        ring.style.left = `${b.x - 6}px`;
        ring.style.top = `${b.y - 6}px`;
        ring.style.width = `${b.width + 12}px`;
        ring.style.height = `${b.height + 12}px`;
        const badge = document.createElement("div");
        badge.dataset.guide = "1";
        for (const [k, v] of Object.entries(badgeStyle)) badge.style.setProperty(k, v);
        badge.textContent = String(n);
        badge.style.left = `${b.x - 6 - 13}px`;
        badge.style.top = `${b.y - 6 - 13}px`;
        document.body.append(ring, badge);
      }
    }, { items: boxes, ringStyle: RING_STYLE, badgeStyle: BADGE_STYLE });
    // The region (when given) is the crop; otherwise the union of the targets. Rings and badges overhang by 19 px.
    const base = regionBoxes.length ? regionBoxes : boxes.map((x) => x.b);
    const x0 = Math.min(...base.map((b) => b.x), ...boxes.map((x) => x.b.x - 19));
    const y0 = Math.min(...base.map((b) => b.y), ...boxes.map((x) => x.b.y - 19));
    const x1 = Math.max(...base.map((b) => b.x + b.width), ...boxes.map((x) => x.b.x + x.b.width + 6));
    const y1 = Math.max(...base.map((b) => b.y + b.height), ...boxes.map((x) => x.b.y + x.b.height + 6));
    const clip = {
      x: Math.max(0, Math.floor(x0 - PAD)),
      y: Math.max(0, Math.floor(y0 - PAD)),
      width: 0,
      height: 0,
    };
    clip.width = Math.min(VIEWPORT.width, Math.ceil(x1 + PAD)) - clip.x;
    clip.height = Math.min(VIEWPORT.height, Math.ceil(y1 + PAD)) - clip.y;
    // Settled when two captures in a row are identical: a count still loading or a field mid-validation differs.
    let png = await page.screenshot({ clip, animations: "disabled", caret: "hide", type: "png" });
    for (let tries = 0; tries < 8; tries++) {
      await page.waitForTimeout(300);
      const again = await page.screenshot({ clip, animations: "disabled", caret: "hide", type: "png" });
      if (again.equals(png)) break;
      png = again;
    }
    await page.evaluate(() => document.querySelectorAll("[data-guide]").forEach((e) => e.remove()));
    const b64 = png.toString("base64");
    const webp = await this.encoder.evaluate(async (data) => {
      const img = new Image();
      img.src = `data:image/png;base64,${data}`;
      await img.decode();
      const c = document.createElement("canvas");
      c.width = img.width;
      c.height = img.height;
      c.getContext("2d")!.drawImage(img, 0, 0);
      return c.toDataURL("image/webp", 0.8).split(",")[1]!;
    }, b64);
    const buf = Buffer.from(webp, "base64");
    const file = path.join(this.outDir, `${spec.key}.webp`);
    writeFileSync(file, buf);
    this.written.push(spec.key);
    this.dims[spec.key] = { width: clip.width, height: clip.height };
    console.log(`[guide-shots] ${spec.key}.webp ${clip.width}x${clip.height} ${buf.length} bytes`);
  }
}

