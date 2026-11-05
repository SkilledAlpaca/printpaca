// Draws the numbered highlight, crops, and encodes one Guide image. Used by scripts/guide-shots.ts.
import { writeFileSync } from "node:fs";
import path from "node:path";
import type { Locator, Page } from "@playwright/test";
import { GUIDE_IMAGE_KEYS, GUIDE_IMAGE_PLAN } from "../../web/src/lib/guideContent.ts";

export const VIEWPORT = { width: 1440, height: 1500 };
const PAD = 16;
/** WebP quality and the widest crop. With the total budget in guide-shots.ts and the vitest budget test. */
export const WEBP_QUALITY = 0.45;
export const MAX_CROP_WIDTH = 1200;

export type ShotSpec = {
  key: string;
  /**
   * The controls to highlight, in reading order: one per badge. Each must be the control itself (an input, a select,
   * a button, a checkbox, a tab, a link) or, for something to read, a heading or a small text element. Never a row,
   * a form grid, a fieldset, a table, a card, or the save bar: the shooter rejects those and fails the run. The badge
   * labels come from the Guide content ("2", or "2.1", "2.2", ... for a step with sub-steps).
   */
  targets: Locator[];
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
  position: "fixed", "z-index": "2147483647", "box-sizing": "border-box", "min-width": "26px", height: "26px", padding: "0 6px", "border-radius": "13px",
  background: "var(--accent-text, #9c4212)", color: "#fff", font: "700 14px/26px Figtree, sans-serif", "text-align": "center", "white-space": "nowrap",
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

export type Box = { x: number; y: number; width: number; height: number };
export type Measured = { ok: true; box: Box } | { ok: false; reason: string };

/**
 * Runs in the page. Returns the box to highlight for a target element and checks it is a specific control:
 * a control with its label, a button or link, a tab, a heading or a small piece of text. Rejects a row, grid,
 * table, fieldset, form, list, card, the save bar, and anything that holds more than one control. The highlight
 * may not be larger than the control plus its own label, give or take a small margin.
 */
export function measureTarget(el: Element): Measured {
  const MARGIN = 8;
  const MAX_W = 1140;
  const MAX_H = 200;
  const tag = el.tagName;
  const role = el.getAttribute("role") ?? "";
  const cls = typeof (el as HTMLElement).className === "string" ? (el as HTMLElement).className : "";
  const desc = `<${tag.toLowerCase()}${role ? ` role=${role}` : ""}${cls ? ` class="${cls}"` : ""}>`;
  const DENY_TAGS = ["TR", "TD", "TH", "TABLE", "THEAD", "TBODY", "FIELDSET", "FORM", "SECTION", "ARTICLE", "UL", "OL", "DL", "NAV", "HEADER", "FOOTER", "ASIDE", "MAIN", "DIALOG"];
  const DENY_ROLES = ["row", "grid", "table", "rowgroup", "group", "toolbar", "region", "form", "tablist", "list", "listitem", "article", "navigation", "dialog", "cell", "gridcell"];
  const DENY_CLASSES = ["card", "ui-card", "row-editor", "form-grid", "ui-form-grid", "save-actions", "sticky", "summary-stats", "ui-field", "ui-fieldset", "req-row", "quote-row", "client-row", "data-table", "ui-data-table", "settings-cards", "topic-body", "page", "row-actions", "cost-stats", "stats"];
  if (DENY_TAGS.includes(tag)) return { ok: false, reason: `${desc} is a container (row, grid, table, fieldset, list, card), not a control` };
  if (DENY_ROLES.includes(role)) return { ok: false, reason: `${desc} has the container role "${role}", not a control` };
  if (cls.split(/\s+/).some((c) => DENY_CLASSES.includes(c))) return { ok: false, reason: `${desc} is a layout container (row, grid, card, or the save bar), not a control` };
  const visible = (e: Element) => {
    const r = e.getBoundingClientRect();
    const cs = getComputedStyle(e);
    return r.width > 0 && r.height > 0 && cs.visibility !== "hidden" && cs.display !== "none";
  };
  const union = (rs: DOMRect[]): Box => {
    const x0 = Math.min(...rs.map((r) => r.left));
    const y0 = Math.min(...rs.map((r) => r.top));
    const x1 = Math.max(...rs.map((r) => r.right));
    const y1 = Math.max(...rs.map((r) => r.bottom));
    return { x: x0, y: y0, width: x1 - x0, height: y1 - y0 };
  };
  const textRect = (n: Node): DOMRect => {
    const range = document.createRange();
    range.selectNodeContents(n);
    return range.getBoundingClientRect();
  };
  if (!visible(el)) return { ok: false, reason: `${desc} is not visible` };
  const isField = ["INPUT", "SELECT", "TEXTAREA"].includes(tag);
  const controlsInside = Array.from(el.querySelectorAll("input:not([type=hidden]), select, textarea, button, a[href], [role=tab], [role=checkbox], [role=button]")).filter(visible);
  let box: Box;
  let allowed: Box;
  if (isField) {
    const own = el.getBoundingClientRect();
    const labels = Array.from((el as HTMLInputElement).labels ?? []).filter(visible).map(textRect).filter((r) => r.width > 0);
    box = union([own, ...labels]);
    allowed = box;
  } else if (["BUTTON", "A"].includes(tag) || ["tab", "button", "link", "checkbox"].includes(role)) {
    if (controlsInside.length > 0 && tag !== "BUTTON" && tag !== "A") return { ok: false, reason: `${desc} holds other controls` };
    box = el.getBoundingClientRect();
    allowed = box;
  } else {
    // Text to read (a heading, a stat, a line): the text itself, never something that holds controls.
    if (controlsInside.length > 0) return { ok: false, reason: `${desc} holds ${controlsInside.length} control(s); target one control instead` };
    const own = el.getBoundingClientRect();
    // The text's own bounds, clamped to the element (a legend's number badge can sit outside its box).
    const t = textRect(el);
    const x0 = Math.max(t.left, own.left);
    const y0 = Math.max(t.top, own.top);
    const x1 = Math.min(t.right, own.right);
    const y1 = Math.min(t.bottom, own.bottom);
    box = x1 > x0 && y1 > y0 ? { x: x0, y: y0, width: x1 - x0, height: y1 - y0 } : { x: own.left, y: own.top, width: own.width, height: own.height };
    allowed = { x: own.left, y: own.top, width: own.width, height: own.height };
  }
  if (box.width <= 0 || box.height <= 0) return { ok: false, reason: `${desc} has no size` };
  if (box.width > MAX_W || box.height > MAX_H) return { ok: false, reason: `${desc} is ${Math.round(box.width)}x${Math.round(box.height)}, too large for one control (limit ${MAX_W}x${MAX_H})` };
  if (box.x < allowed.x - MARGIN || box.y < allowed.y - MARGIN || box.x + box.width > allowed.x + allowed.width + MARGIN || box.y + box.height > allowed.y + allowed.height + MARGIN) {
    return { ok: false, reason: `${desc} highlight is larger than the control plus its label` };
  }
  return { ok: true, box };
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
    try {
      await this.shotInner(spec);
    } catch (e) {
      // Keep the page that failed, to see which element a locator should have found.
      if (process.env.GUIDE_DEBUG_DIR) {
        const html = await (spec.page ?? this.page).evaluate(() => document.body.innerHTML).catch(() => "");
        writeFileSync(path.join(process.env.GUIDE_DEBUG_DIR, `fail-${spec.key}.html`), html);
      }
      throw e;
    }
  }

  private async shotInner(spec: ShotSpec): Promise<void> {
    const page = spec.page ?? this.page;
    if (!GUIDE_IMAGE_KEYS.includes(spec.key)) throw new Error(`image key "${spec.key}" is not referenced by guideContent.ts`);
    if (!this.wanted(spec.key)) return;
    await page.getByText("Updating price").first().waitFor({ state: "hidden", timeout: 15_000 }).catch(() => {});
    await freezeDates(this.page);
    const first = spec.targets[0]!.first();
    await first.waitFor({ state: "visible", timeout: 15_000 });
    // <main> scrolls, not the window; bring the target into view, then read viewport coordinates.
    await first.scrollIntoViewIfNeeded();
    await page.evaluate(() => document.fonts.ready);
    await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    // Park the pointer: a hovered field shows its number spinner or hover fill.
    await page.mouse.move(0, 0);
    await page.waitForTimeout(600);
    const plan = GUIDE_IMAGE_PLAN[spec.key]!;
    if (plan.labels.length !== spec.targets.length) {
      throw new Error(`${spec.key}: the Guide step has ${plan.labels.length} badge(s) (${plan.labels.join(", ")}) but the capture highlights ${spec.targets.length}`);
    }
    // Whole pixels, so a fractional scroll offset can't move a ring by a sub-pixel between runs.
    const whole = (b: { x: number; y: number; width: number; height: number }) => ({ x: Math.round(b.x), y: Math.round(b.y), width: Math.round(b.width), height: Math.round(b.height) });
    const rect = async (l: Locator) => {
      const b = await l.first().boundingBox();
      if (!b) throw new Error(`no box for a region of ${spec.key}`);
      return b;
    };
    const boxes: { n: string; b: { x: number; y: number; width: number; height: number } }[] = [];
    for (const [i, t] of spec.targets.entries()) {
      const label = plan.labels[i]!;
      const el = t.first();
      const r = await el.evaluate(measureTarget);
      if (!r.ok) throw new Error(`${spec.key} highlight ${label}: ${r.reason}`);
      boxes.push({ n: label, b: whole(r.box) });
    }
    // Reading order: top to bottom, and left to right within a line.
    for (let i = 1; i < boxes.length; i++) {
      const a = boxes[i - 1]!.b;
      const c = boxes[i]!.b;
      const dy = c.y + c.height / 2 - (a.y + a.height / 2);
      if (dy < -14 || (Math.abs(dy) <= 14 && c.x < a.x)) {
        throw new Error(`${spec.key}: highlight ${boxes[i]!.n} comes before ${boxes[i - 1]!.n} in reading order; reorder the step's sub-steps`);
      }
    }
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
        badge.textContent = n;
        badge.dataset.guideBadge = n;
        document.body.append(ring, badge);
        // Sit on the ring's top-left corner, above the label so the first letter stays readable.
        badge.style.left = `${b.x - 6 - badge.offsetWidth + 14}px`;
        badge.style.top = `${b.y - 6 - 24}px`;
      }
    }, { items: boxes, ringStyle: RING_STYLE, badgeStyle: BADGE_STYLE });
    const drawn = await page.evaluate(() => Array.from(document.querySelectorAll<HTMLElement>("[data-guide-badge]")).map((e) => e.textContent ?? ""));
    if (drawn.join("|") !== plan.labels.join("|")) throw new Error(`${spec.key}: drew badges ${drawn.join(", ")} but the step's labels are ${plan.labels.join(", ")}`);
    // The region (when given) is the crop; otherwise the union of the targets. Rings and badges overhang.
    const base = regionBoxes.length ? regionBoxes : boxes.map((x) => x.b);
    const x0 = Math.min(...base.map((b) => b.x), ...boxes.map((x) => x.b.x - 32));
    const y0 = Math.min(...base.map((b) => b.y), ...boxes.map((x) => x.b.y - 30));
    const x1 = Math.max(...base.map((b) => b.x + b.width), ...boxes.map((x) => x.b.x + x.b.width + 6));
    const y1 = Math.max(...base.map((b) => b.y + b.height), ...boxes.map((x) => x.b.y + x.b.height + 6));
    const clip = {
      x: Math.max(0, Math.floor(x0 - PAD)),
      y: Math.max(0, Math.floor(y0 - PAD)),
      width: 0,
      height: 0,
    };
    clip.width = Math.min(VIEWPORT.width, MAX_CROP_WIDTH + clip.x, Math.ceil(x1 + PAD)) - clip.x;
    for (const bx of boxes) {
      if (bx.b.x + bx.b.width + 6 > clip.x + clip.width) throw new Error(`${spec.key}: highlight ${bx.n} falls outside the ${MAX_CROP_WIDTH}px crop`);
    }
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
    const webp = await this.encoder.evaluate(async ({ data, q }) => {
      const img = new Image();
      img.src = `data:image/png;base64,${data}`;
      await img.decode();
      const c = document.createElement("canvas");
      c.width = img.width;
      c.height = img.height;
      c.getContext("2d")!.drawImage(img, 0, 0);
      return c.toDataURL("image/webp", q).split(",")[1]!;
    }, { data: b64, q: WEBP_QUALITY });
    const buf = Buffer.from(webp, "base64");
    const file = path.join(this.outDir, `${spec.key}.webp`);
    writeFileSync(file, buf);
    this.written.push(spec.key);
    this.dims[spec.key] = { width: clip.width, height: clip.height };
    console.log(`[guide-shots] ${spec.key}.webp ${clip.width}x${clip.height} ${buf.length} bytes`);
  }
}

