import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { ACCENT_DARK_BACKGROUNDS, ACCENT_LIGHT_BACKGROUNDS } from "./accent";

// Drift guard: the accent contrast check hardcodes the shop backgrounds, so a theme.css change to --bg,
// --surface or --surface-2 must be mirrored in accent.ts and server/accent.go.
const css = readFileSync(resolve(__dirname, "../theme.css"), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");

/** The --bg, --surface and --surface-2 values of the first rule whose selector starts with `selector`. */
function surfaces(selector: string): string[] {
  const start = css.indexOf(selector + " {");
  expect(start, `selector ${selector} in theme.css`).toBeGreaterThanOrEqual(0);
  const body = css.slice(start, css.indexOf("}", start));
  return ["--bg", "--surface", "--surface-2"].map((name) => {
    const m = body.match(new RegExp(`${name}:\\s*(#[0-9a-fA-F]{6})\\s*;`));
    if (!m) throw new Error(`${name} not found in ${selector}`);
    return (m[1] ?? "").toLowerCase();
  });
}

describe("accent backgrounds match theme.css", () => {
  it("light", () => {
    expect(surfaces(":root")).toEqual([...ACCENT_LIGHT_BACKGROUNDS]);
  });
  it("dark, prefers-color-scheme block", () => {
    expect(surfaces(':root:not([data-theme="light"])')).toEqual([...ACCENT_DARK_BACKGROUNDS]);
  });
  it("dark, data-theme block", () => {
    expect(surfaces(':root[data-theme="dark"]')).toEqual([...ACCENT_DARK_BACKGROUNDS]);
  });
});
