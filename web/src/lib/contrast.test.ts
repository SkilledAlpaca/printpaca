import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { contrastRatio, mixSrgb } from "./contrast";

// Reads the real stylesheet rather than a hand-copied token list, so the gate can't drift from theme.css.
const cssPath = resolve(__dirname, "../theme.css");
const rawCss = readFileSync(cssPath, "utf-8");
// Strip comments first so a commented-out custom property can't be picked up by the token regex.
const css = rawCss.replace(/\/\*[\s\S]*?\*\//g, "");

  /** Returns the text inside the brace-depth-aware block opening after the first `marker`. */
function extractBlockAfter(source: string, marker: string): string {
  const markerIdx = source.indexOf(marker);
  if (markerIdx === -1) {
    throw new Error(`theme.css: marker not found: "${marker}"`);
  }
  const braceStart = source.indexOf("{", markerIdx);
  if (braceStart === -1) {
    throw new Error(`theme.css: no "{" after marker: "${marker}"`);
  }
  let depth = 0;
  for (let i = braceStart; i < source.length; i++) {
    if (source[i] === "{") depth++;
    else if (source[i] === "}") {
      depth--;
      if (depth === 0) return source.slice(braceStart, i + 1);
    }
  }
  throw new Error(`theme.css: unbalanced braces after marker: "${marker}"`);
}

  /** Extracts every `--custom-prop: #hex;` declaration in a block of CSS. */
function extractHexTokens(block: string): Record<string, string> {
  const tokens: Record<string, string> = {};
  const re = /--([a-z0-9-]+)\s*:\s*(#[0-9a-fA-F]{3,8})\s*;/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(block))) {
    const [, name, hex] = m;
    if (name && hex) tokens[name] = hex;
  }
  return tokens;
}

  /** Looks up a required token, failing loudly if theme.css stopped defining it (narrows to `string`). */
function token(tokens: Record<string, string>, name: string, schemeName: string): string {
  const value = tokens[name];
  if (value === undefined) {
    throw new Error(`${schemeName}: --${name} not found in theme.css`);
  }
  return value;
}

const DARK_MARKER = "@media (prefers-color-scheme: dark)";

// Block extraction assumes the light `:root {` is the first occurrence. Guard that, and that the two
// schemes differ, so a reordered file can't make "light" assertions silently re-check dark.
const lightMarkerIdx = css.indexOf(":root {");
const darkMarkerIdx = css.indexOf(DARK_MARKER);
if (lightMarkerIdx === -1) {
  throw new Error('theme.css: light ":root {" marker not found');
}
if (darkMarkerIdx === -1) {
  throw new Error(`theme.css: dark scheme marker not found: "${DARK_MARKER}"`);
}
if (!(lightMarkerIdx < darkMarkerIdx)) {
  throw new Error(
    'theme.css: light ":root {" must appear before the dark scheme marker; block extraction assumes source order and would otherwise silently read the wrong scheme',
  );
}

const lightBlock = extractBlockAfter(css, ":root {");
const darkBlock = extractBlockAfter(css, DARK_MARKER);
const light = extractHexTokens(lightBlock);
const dark = extractHexTokens(darkBlock);

if (light["bg"] !== undefined && light["bg"] === dark["bg"]) {
  throw new Error(
    "theme.css: light and dark --bg resolved to the same value; light/dark block extraction likely collapsed onto the same block",
  );
}

const TEXT_TOKENS = ["text", "muted", "danger", "warn", "accent-text", "ok"] as const;

type BadgeVariant = { fileLabel: string; className: string; textToken: string; mixPercent: number };

  /** Parses every `.badge-xxx` rule in a component's <style> block into its `color` and
   * `color-mix(... var(--surface))` pair, so the gate checks the component's real formula. */
function extractBadgeVariants(componentPath: string, fileLabel: string): BadgeVariant[] {
  const source = readFileSync(componentPath, "utf-8");
  const styleMatch = source.match(/<style[^>]*>([\s\S]*?)<\/style>/);
  if (!styleMatch || styleMatch[1] === undefined) {
    throw new Error(`${fileLabel}: no <style> block found`);
  }
  const styleBlock = styleMatch[1].replace(/\/\*[\s\S]*?\*\//g, "");

  const variants: BadgeVariant[] = [];
  const ruleRe = /([^{}]+)\{([^{}]*)\}/g;
  let m: RegExpExecArray | null;
  while ((m = ruleRe.exec(styleBlock))) {
    const [, selectorPart, body] = m;
    if (!selectorPart || body === undefined) continue;
      // Substring check: a reshaped selector like ".badge.badge-viewed" must still meet the parseable shape.
    const badgeSelectors = selectorPart
      .split(",")
      .map((s) => s.trim())
      .filter((s) => s.includes(".badge-"));
    if (badgeSelectors.length === 0) continue;

    let textToken: string | undefined;
    let mixToken: string | undefined;
    let mixPercent: number | undefined;
    for (const rawDecl of body.split(";")) {
      const decl = rawDecl.trim();
      if (decl.startsWith("color:")) {
        const cm = decl.match(/^color:\s*var\(--([a-z0-9-]+)\)$/);
        if (cm && cm[1]) textToken = cm[1];
      } else if (decl.startsWith("background:")) {
        const bm = decl.match(/^background:\s*color-mix\(in srgb,\s*var\(--([a-z0-9-]+)\)\s*(\d+)%,\s*var\(--surface\)\)$/);
        if (bm && bm[1] && bm[2]) {
          mixToken = bm[1];
          mixPercent = Number(bm[2]);
        }
      }
    }
    // Any deviation from the parseable shape is a loud collection-time failure, not a silent skip: an
    // unreadable badge rule is one the contrast gate isn't checking.
    if (textToken === undefined || mixToken === undefined || mixPercent === undefined) {
      throw new Error(
        `${fileLabel}: selector "${badgeSelectors.join(", ")}" looks like a badge variant (matches ".badge-") but its ` +
          "declarations don't match the parseable \"color: var(--X); background: color-mix(in srgb, var(--X) N%, " +
          `var(--surface));\" shape (parsed color token: ${textToken ?? "none"}, background mix token: ${mixToken ?? "none"}, ` +
          `mix percent: ${mixPercent ?? "none"}). Rule body: ${body.trim()}`,
      );
    }
    if (textToken !== mixToken) {
      throw new Error(
        `${fileLabel}: ${badgeSelectors.join(", ")} text color token "--${textToken}" doesn't match its background mix token "--${mixToken}"`,
      );
    }
    for (const className of badgeSelectors) {
      variants.push({ fileLabel, className, textToken, mixPercent });
    }
  }
  return variants;
}

// StatusBadge is the single home for badge CSS; the assertion below fails loudly if extraction breaks.
const BADGE_VARIANTS: BadgeVariant[] = [
  ...extractBadgeVariants(resolve(__dirname, "../components/StatusBadge.svelte"), "StatusBadge.svelte"),
];

describe("theme.css block extraction sanity", () => {
  it("parsed distinct light and dark token sets", () => {
    expect(lightMarkerIdx).toBeLessThan(darkMarkerIdx);
    expect(light["bg"]).toBeDefined();
    expect(dark["bg"]).toBeDefined();
    expect(light["bg"]).not.toEqual(dark["bg"]);
  });

  it("found at least one badge variant", () => {
    expect(BADGE_VARIANTS.some((v) => v.fileLabel === "StatusBadge.svelte")).toBe(true);
  });
});

describe.each([
  { schemeName: "light", tokens: light },
  { schemeName: "dark", tokens: dark },
])("theme.css $schemeName scheme contrast", ({ schemeName, tokens }) => {
  it("parsed the core tokens this suite depends on", () => {
    for (const name of ["bg", "surface", "surface-2", "accent", "accent-contrast", ...TEXT_TOKENS]) {
      expect(tokens[name], `${schemeName}: --${name} not found in theme.css`).toBeDefined();
    }
  });

  for (const t of TEXT_TOKENS) {
    it(`--${t} clears 4.5:1 against --surface`, () => {
      expect(contrastRatio(token(tokens, t, schemeName), token(tokens, "surface", schemeName))).toBeGreaterThanOrEqual(4.5);
    });
    it(`--${t} clears 4.5:1 against --bg`, () => {
      expect(contrastRatio(token(tokens, t, schemeName), token(tokens, "bg", schemeName))).toBeGreaterThanOrEqual(4.5);
    });
  }

  // A link inside a --surface-2 panel gets the global `a` color, so it needs its own --accent-text vs
  // --surface-2 assertion.
  it("--accent-text clears 4.5:1 against --surface-2 (links inside inset panels)", () => {
    expect(
      contrastRatio(token(tokens, "accent-text", schemeName), token(tokens, "surface-2", schemeName)),
    ).toBeGreaterThanOrEqual(4.5);
  });

  // button.quiet:hover swaps its background to --surface-2 and forces color: var(--text), so that is
  // the pairing actually rendered.
  it("--text clears 4.5:1 against --surface-2 (button.quiet hover background)", () => {
    expect(
      contrastRatio(token(tokens, "text", schemeName), token(tokens, "surface-2", schemeName)),
    ).toBeGreaterThanOrEqual(4.5);
  });

  // button.quiet.danger:hover keeps --danger text over a --surface-2 background.
  it("--danger clears 4.5:1 against --surface-2 (quiet danger button hover)", () => {
    expect(
      contrastRatio(token(tokens, "danger", schemeName), token(tokens, "surface-2", schemeName)),
    ).toBeGreaterThanOrEqual(4.5);
  });

  it("--accent-contrast clears 4.5:1 against --accent (button.primary text)", () => {
    expect(
      contrastRatio(token(tokens, "accent-contrast", schemeName), token(tokens, "accent", schemeName)),
    ).toBeGreaterThanOrEqual(4.5);
  });

  it("--accent clears 3:1 against --surface (non-text UI: borders, focus ring)", () => {
    expect(
      contrastRatio(token(tokens, "accent", schemeName), token(tokens, "surface", schemeName)),
    ).toBeGreaterThanOrEqual(3.0);
  });

  // Muted text lands on --surface-2 in active rows and hovered table rows (WCAG 1.4.3).
  it("--muted clears 4.5:1 against --surface-2 (active and hovered rows)", () => {
    expect(contrastRatio(token(tokens, "muted", schemeName), token(tokens, "surface-2", schemeName))).toBeGreaterThanOrEqual(4.5);
  });

  // Data-entry control borders and the focus ring are non-text UI (WCAG 1.4.11): 3:1 on every surface.
  for (const nonText of ["control-border", "focus-ring"]) {
    for (const surfaceName of ["bg", "surface", "surface-2"]) {
      it(`--${nonText} clears 3:1 against --${surfaceName}`, () => {
        expect(
          contrastRatio(token(tokens, nonText, schemeName), token(tokens, surfaceName, schemeName)),
        ).toBeGreaterThanOrEqual(3.0);
      });
    }
  }

  for (const { fileLabel, className, textToken, mixPercent } of BADGE_VARIANTS) {
    it(`${fileLabel} ${className} text clears 4.5:1 on its color-mix background`, () => {
      // Reproduces the component's `color-mix(in srgb, ...)`, which interpolates gamma-encoded sRGB
      // components directly (as mixSrgb does); only the two final colors are linearized.
      const text = token(tokens, textToken, schemeName);
      const surface = token(tokens, "surface", schemeName);
      const bg = mixSrgb(text, mixPercent, surface);
      expect(contrastRatio(text, bg)).toBeGreaterThanOrEqual(4.5);
    });
  }
});
