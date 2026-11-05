import { describe, expect, it } from "vitest";
import { existsSync, readFileSync } from "node:fs";
import path from "node:path";
import { ENV_ONLY_ROWS, OTHER_PAGE_ROWS, RECORD_FLOW, SAVE_BRANCHES, SAVE_MAIN, SETTINGS_PAGE_ROWS } from "./guideDiagrams";

const ROOT = path.resolve(__dirname, "../../..");
// The Docker build's test stage copies only web/, so the checks against server/ and docs/ run everywhere else.
const haveRepo = existsSync(path.join(ROOT, "server", "config.go")) && existsSync(path.join(ROOT, "docs", "configuration.md"));
const read = (...p: string[]) => (existsSync(path.join(ROOT, ...p)) ? readFileSync(path.join(ROOT, ...p), "utf8") : "");
const configGo = read("server", "config.go");
const configDocs = read("docs", "configuration.md");
const settingsTab = readFileSync(path.join(ROOT, "web", "src", "components", "SettingsTab.svelte"), "utf8");

// The top-level keys of the Config struct, as written in config.yaml.
const topLevel = new Set([...configGo.slice(Math.max(0, configGo.indexOf("type Config struct"))).split("\n}\n")[0]!.matchAll(/yaml:"([A-Za-z]+)"/g)].map((m) => m[1]!));

describe("Where settings live", () => {
  it.skipIf(!haveRepo)("maps only to real top-level config.yaml keys", () => {
    for (const row of [...SETTINGS_PAGE_ROWS, ...OTHER_PAGE_ROWS]) for (const k of row.keys) expect(topLevel.has(k), `${row.where}: ${k}`).toBe(true);
  });
  it("lists the same sections and keys as the Settings page's section list", () => {
    for (const row of SETTINGS_PAGE_ROWS) {
      expect(settingsTab, row.where).toContain(`label: "${row.where}"`);
      for (const k of row.keys) expect(settingsTab, `${row.where}: ${k}`).toContain(`"${k}"`);
    }
  });
  it.skipIf(!haveRepo)("names only environment variables that docs/configuration.md documents", () => {
    for (const env of ENV_ONLY_ROWS) expect(configDocs, env.name).toContain(`\`${env.name}\``);
  });
  it("does not repeat a config.yaml key", () => {
    const all = [...SETTINGS_PAGE_ROWS, ...OTHER_PAGE_ROWS].flatMap((r) => r.keys);
    expect(new Set(all).size).toBe(all.length);
  });
});

describe("What happens when you save", () => {
  it("has unique box ids, and each branch hangs off a real step", () => {
    const ids = [...SAVE_MAIN, ...SAVE_BRANCHES.map((b) => b.box), ...RECORD_FLOW].map((b) => b.id);
    expect(new Set(ids).size).toBe(ids.length);
    for (const b of SAVE_BRANCHES) expect(SAVE_MAIN.map((m) => m.id)).toContain(b.from);
  });
  it.skipIf(!haveRepo)("quotes the messages the app shows", () => {
    const editor = readFileSync(path.join(ROOT, "web", "src", "components", "settings", "SettingsEditor.svelte"), "utf8");
    const settingsLib = readFileSync(path.join(ROOT, "web", "src", "lib", "settings.ts"), "utf8");
    expect(editor).toContain("Settings saved.");
    expect(editor).toContain("Discard changes");
    expect(editor).toContain("Save changes");
    expect(settingsLib).toContain("Couldn't reach the server.");
    expect(readFileSync(path.join(ROOT, "server", "invoices.go"), "utf8")).toContain("This was changed in another tab or by another user.");
  });
});
