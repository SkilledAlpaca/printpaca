import { describe, expect, it } from "vitest";
import { GUIDE_TOPICS, SHORTCUTS, TILE_HELP, TILE_IDS } from "./guideContent";

// The em dash is written as an escape so this file doesn't trip the repo-wide em-dash sweep.
const EM_DASH = "\u2014";

describe("guideContent", () => {
  it("has a non-empty help string for every tile id, and no em dashes", () => {
    for (const id of TILE_IDS) {
      const help = TILE_HELP[id];
      expect(help).toBeTruthy();
      expect(help.length).toBeGreaterThan(0);
      expect(help).not.toContain(EM_DASH);
    }
  });

  it("has no extra keys beyond TILE_IDS", () => {
    expect(Object.keys(TILE_HELP).sort()).toEqual([...TILE_IDS].sort());
  });
});

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
  it("lists the save and filter shortcuts without em dashes", () => {
    expect(SHORTCUTS.map((s) => s.action)).toEqual(expect.arrayContaining(["Save", "Jump to the page's filter box"]));
    for (const sc of SHORTCUTS) expect(`${sc.keys}${sc.action}${sc.where}`).not.toContain(EM_DASH);
  });
});
