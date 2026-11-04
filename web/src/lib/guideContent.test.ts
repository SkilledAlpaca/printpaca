import { describe, expect, it } from "vitest";
import { GUIDE_TOPICS, SHORTCUTS } from "./guideContent";

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
