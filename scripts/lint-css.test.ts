import { describe, expect, it } from "vitest";
import { classify, isRawLiteral, lintSource } from "./lint-css.ts";

const style = (css: string) => `<div></div>\n<style>\n${css}\n</style>\n`;

describe("isRawLiteral", () => {
  it("flags raw lengths on spacing and width properties", () => {
    expect(isRawLiteral("margin", "12px")).toBe(true);
    expect(isRawLiteral("padding", "0 0.75rem")).toBe(true);
    expect(isRawLiteral("gap", "1rem 2rem")).toBe(true);
    expect(isRawLiteral("max-width", "44rem")).toBe(true);
    expect(isRawLiteral("width", "calc(100% - 20px)")).toBe(true);
    expect(isRawLiteral("margin-inline-start", "-4px")).toBe(true);
  });
  it("allows tokens, zero, percentages, keywords and intrinsic sizing", () => {
    expect(isRawLiteral("margin", "var(--space-2) 0")).toBe(false);
    expect(isRawLiteral("padding", "0")).toBe(false);
    expect(isRawLiteral("padding", "0px")).toBe(false);
    expect(isRawLiteral("width", "100%")).toBe(false);
    expect(isRawLiteral("width", "auto")).toBe(false);
    expect(isRawLiteral("min-width", "0")).toBe(false);
    expect(isRawLiteral("max-width", "fit-content")).toBe(false);
    expect(isRawLiteral("max-width", "min(100%, var(--field-m))")).toBe(false);
    expect(isRawLiteral("gap", "calc(var(--gap) * 2)")).toBe(false);
  });
  it("ignores a literal fallback inside var()", () => {
    expect(isRawLiteral("margin", "var(--space-2, 8px)")).toBe(false);
  });
  it("allows accessibility target sizes on min-width", () => {
    expect(isRawLiteral("min-width", "44px")).toBe(false);
    expect(isRawLiteral("min-width", "24px")).toBe(false);
    expect(isRawLiteral("min-width", "30px")).toBe(true);
  });
  it("ignores properties that are not spacing or width", () => {
    expect(isRawLiteral("font-size", "12px")).toBe(false);
    expect(isRawLiteral("border-radius", "8px")).toBe(false);
    expect(isRawLiteral("min-height", "44px")).toBe(false);
    expect(isRawLiteral("height", "12rem")).toBe(false);
  });
});

describe("lintSource", () => {
  it("reports line, selector, property and value from a svelte style block", () => {
    const found = lintSource("a.svelte", style(".card {\n  margin: 8px;\n  color: red;\n}\n.x { padding: var(--space-1) 0.3rem }"));
    expect(found).toEqual([
      { file: "a.svelte", line: 4, selector: ".card", property: "margin", value: "8px" },
      { file: "a.svelte", line: 7, selector: ".x", property: "padding", value: "var(--space-1) 0.3rem" },
    ]);
  });
  it("skips custom property definitions, media preludes, print blocks, font-face and markup", () => {
    const css = [
      ":root { --gap: 16px; --field-m: 20rem; }",
      "@media (max-width: 600px) { .a { margin: var(--space-2); } }",
      "@media print { .a { margin: 12mm; width: 190mm; } }",
      '@font-face { font-family: "x"; src: url(x.woff2); }',
      "@keyframes k { 50% { margin-left: 3px; } }",
    ].join("\n");
    expect(lintSource("t.css", css)).toEqual([]);
    expect(lintSource("a.svelte", '<div style="margin: 3px">x</div>\n' + style(".ok { margin: 0; }"))).toEqual([]);
  });
  it("still checks rules nested in a non-print @media", () => {
    expect(lintSource("t.css", "@media (min-width: 900px) { .a { max-width: 30rem; } }").map((v) => v.property)).toEqual(["max-width"]);
  });
  it("ignores comments", () => {
    expect(lintSource("t.css", "/* margin: 3px; */\n.a { margin: var(--space-1); }")).toEqual([]);
  });
});

describe("classify", () => {
  const v = { file: "web/src/components/ui/Card.svelte", line: 3, selector: ".card", property: "margin", value: "3px" };
  const other = { ...v, file: "web/src/components/OldTab.svelte" };
  it("blocks violations in blocking files and warns elsewhere", () => {
    const r = classify([v, other], { blocking: ["web/src/components/ui/**"], exceptions: [] });
    expect(r.errors).toEqual([v]);
    expect(r.warnings).toEqual([other]);
  });
  it("drops a violation covered by an exception and flags a stale one", () => {
    const covered = { file: v.file, property: "margin", value: "3px", reason: "test" };
    const stale = { file: v.file, property: "padding", value: "9px", reason: "gone" };
    const r = classify([v], { blocking: ["web/src/components/ui/**"], exceptions: [covered, stale] });
    expect(r.errors).toEqual([]);
    expect(r.staleExceptions).toEqual([stale]);
  });
  it("narrows an exception by selector substring", () => {
    const e = { file: v.file, selector: ".other", property: "margin", value: "3px", reason: "test" };
    expect(classify([v], { blocking: ["web/src/components/ui/**"], exceptions: [e] }).errors).toEqual([v]);
  });
});
