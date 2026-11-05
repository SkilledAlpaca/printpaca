import { describe, expect, it } from "vitest";
import { existsSync, readdirSync, statSync } from "node:fs";
import path from "node:path";
import { GUIDE_IMAGE_KEYS, GUIDE_NAV, GUIDE_TASKS, GUIDE_TOPICS, SHORTCUTS, resolveGuideTopic } from "./guideContent";
import { parseInline } from "./guideInline";
import dimensions from "../assets/guide/dimensions.json";

const IMAGE_DIR = path.resolve(__dirname, "../assets/guide");

// The em dash is written as an escape so this file doesn't trip the repo-wide em-dash sweep.
const EM_DASH = "\u2014";

describe("GUIDE_TOPICS", () => {
  it("has a non-empty title and body for every topic, and no em dashes", () => {
    for (const topic of GUIDE_TOPICS) {
      expect(topic.title.length).toBeGreaterThan(0);
      expect(topic.body.length).toBeGreaterThan(0);
      expect(topic.title).not.toContain(EM_DASH);
      expect(topic.body).not.toContain(EM_DASH);
    }
  });

  it("has unique ids", () => {
    const ids = GUIDE_TOPICS.map((t) => t.id);
    expect(new Set(ids).size).toBe(ids.length);
  });
});

describe("guide coverage", () => {
  it("documents requests, shop items, Spoolman and users", () => {
    const ids = GUIDE_TOPICS.map((t) => t.id);
    for (const id of ["requests-finalize-vs-convert", "shop-items-and-options", "spoolman", "users-and-permissions"]) {
      expect(ids).toContain(id);
    }
  });
  it("keeps the topics the removed card help explained (D33)", () => {
    const ids = GUIDE_TOPICS.map((t) => t.id);
    for (const id of ["model-files", "material-and-printer", "cost-breakdown", "margin-vs-markup", "marketplace-fees", "saving-quotes", "waste-and-failure-buffers"]) {
      expect(ids).toContain(id);
    }
  });
  it("lists the save and filter shortcuts without em dashes", () => {
    expect(SHORTCUTS.map((s) => s.action)).toEqual(expect.arrayContaining(["Save", "Jump to the page's filter box"]));
    for (const sc of SHORTCUTS) expect(`${sc.keys}${sc.action}${sc.where}`).not.toContain(EM_DASH);
  });
});

describe("GUIDE_TASKS", () => {
  const allIds = GUIDE_NAV.flatMap((g) => g.items.map((i) => i.id));

  it("has unique ids across tasks, concepts, and the shortcuts reference", () => {
    expect(new Set(allIds).size).toBe(allIds.length);
  });

  it("covers every D56 topic", () => {
    const ids = GUIDE_TASKS.map((t) => t.id);
    for (const id of [
      "first-time-setup", "quote-a-print", "quote-several-parts", "save-and-reopen-quote", "handle-a-shop-request",
      "invoice-and-payment", "set-up-storefront", "add-a-shop-item", "electricity-schedules", "filament-and-spoolman",
      "printers-and-moonraker", "add-staff-accounts", "read-the-dashboard",
    ]) expect(ids).toContain(id);
  });

  it("gives every task a title, a one-sentence summary, steps, and at least one image", () => {
    for (const t of GUIDE_TASKS) {
      expect(t.title.length, t.id).toBeGreaterThan(0);
      expect(t.summary.length, t.id).toBeGreaterThan(0);
      expect(t.summary, t.id).toMatch(/\.$/);
      expect(t.steps.length, t.id).toBeGreaterThan(1);
      expect(t.steps.filter((s) => s.image).length, t.id).toBeGreaterThan(0);
    }
  });

  it("writes every step as one non-empty text, with alt text on every image, and no em dashes", () => {
    for (const t of GUIDE_TASKS) {
      const text = [t.title, t.summary, t.next ?? "", ...t.steps.flatMap((s) => [s.text, s.image?.alt ?? ""])].join(" ");
      expect(text, t.id).not.toContain(EM_DASH);
      for (const s of t.steps) {
        expect(s.text.trim().length, t.id).toBeGreaterThan(10);
        if (s.image) expect(s.image.alt.length, s.image.key).toBeGreaterThan(20);
      }
    }
  });

  it("links only to topics that exist", () => {
    for (const t of GUIDE_TASKS) {
      for (const text of [t.next ?? "", ...t.steps.map((s) => s.text)]) {
        for (const seg of parseInline(text)) {
          if (seg.kind === "link") expect(allIds, `${t.id} -> ${seg.href}`).toContain(seg.href.replace("#/guide#", ""));
        }
      }
    }
  });

  it("uses each image key once", () => {
    expect(new Set(GUIDE_IMAGE_KEYS).size).toBe(GUIDE_IMAGE_KEYS.length);
  });
});

describe("guide images", () => {
  const files = readdirSync(IMAGE_DIR).filter((f) => f.endsWith(".webp"));

  it("has a file for every referenced image, and every file is referenced", () => {
    for (const key of GUIDE_IMAGE_KEYS) expect(existsSync(path.join(IMAGE_DIR, `${key}.webp`)), key).toBe(true);
    expect(files.map((f) => f.slice(0, -5)).sort()).toEqual([...GUIDE_IMAGE_KEYS].sort());
  });

  it("records the size of every image, for width and height attributes", () => {
    const dims = dimensions as Record<string, { width: number; height: number }>;
    expect(Object.keys(dims).sort()).toEqual([...GUIDE_IMAGE_KEYS].sort());
    for (const key of GUIDE_IMAGE_KEYS) {
      expect(dims[key]!.width, key).toBeGreaterThan(50);
      expect(dims[key]!.height, key).toBeGreaterThan(50);
    }
  });

  it("stays under the 1.5 MB budget", () => {
    const total = files.reduce((sum, f) => sum + statSync(path.join(IMAGE_DIR, f)).size, 0);
    expect(total).toBeLessThan(1.5 * 1024 * 1024);
  });
});

describe("resolveGuideTopic", () => {
  it("opens the named topic, and the first task for an unknown or missing fragment", () => {
    expect(resolveGuideTopic("margin-vs-markup")).toBe("margin-vs-markup");
    expect(resolveGuideTopic("keyboard-shortcuts")).toBe("keyboard-shortcuts");
    expect(resolveGuideTopic("nope")).toBe(GUIDE_TASKS[0]!.id);
    expect(resolveGuideTopic(null)).toBe(GUIDE_TASKS[0]!.id);
  });
  it("keeps every v1.1 deep-link anchor", () => {
    for (const t of GUIDE_TOPICS) expect(resolveGuideTopic(t.id)).toBe(t.id);
  });
});
