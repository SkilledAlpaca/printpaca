// Accent color check for shop.accentColor. The same algorithm lives in server/accent.go; both read
// testdata/accent-vectors.json (accent.test.ts here), so they can't drift. Pure functions: no DOM, no I/O.
//
// Which pairs must pass (WCAG 2.2 AA, 4.5:1 for text):
//   - link: the accent used as text color on every shop background of the scheme (page, card, tinted card);
//     the lowest ratio counts.
//   - button: the accent as a button background with its label color (white or near-black, whichever
//     contrasts more).
//
// The light scheme is what the owner picks from. A color that fails there is rejected with the measured ratios
// and the nearest passing color. The dark-mode variant is derived: same hue and saturation, lightness moved the
// least that passes the same two pairs on the dark backgrounds.

// The shop backgrounds the accent is checked against: the --bg, --surface and --surface-2 tokens of each
// scheme in theme.css. Update server/accent.go together with these.
export const ACCENT_LIGHT_BACKGROUNDS = ["#fff5dc", "#fffbef", "#f6e8c4"] as const;
export const ACCENT_DARK_BACKGROUNDS = ["#141414", "#1e1e1e", "#292929"] as const;
/** The label colors a button on the accent can use. */
export const ACCENT_TEXT_CANDIDATES = ["#ffffff", "#141414"] as const;

export const ACCENT_MIN_RATIO = 4.5;
const STEP = 0.005; // lightness step of the nearest-color search
const MAX_STEPS = 200;
const HEX_RE = /^#[0-9a-fA-F]{6}$/;

export interface AccentScheme {
  accent: string;
  buttonText: string;
  linkRatio: number;
  buttonRatio: number;
  pass: boolean;
}

export interface AccentCheck {
  ok: boolean;
  accent: string;
  light: AccentScheme;
  dark: AccentScheme;
  /** The nearest passing light color when the input fails, "" otherwise. */
  suggestion: string;
}

const EMPTY_SCHEME: AccentScheme = { accent: "", buttonText: "", linkRatio: 0, buttonRatio: 0, pass: false };

export function isAccentHex(v: string): boolean {
  return HEX_RE.test(v);
}

function parseHex(hex: string): [number, number, number] {
  const n = parseInt(hex.slice(1), 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
}

function toHex(r: number, g: number, b: number): string {
  return `#${[r, g, b].map((v) => v.toString(16).padStart(2, "0")).join("")}`;
}

function linear(c255: number): number {
  const c = c255 / 255;
  return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4);
}

function luminance(hex: string): number {
  const [r, g, b] = parseHex(hex);
  return 0.2126 * linear(r) + 0.7152 * linear(g) + 0.0722 * linear(b);
}

/** The WCAG contrast ratio of two #rrggbb colors. */
export function accentContrast(a: string, b: string): number {
  let l1 = luminance(a);
  let l2 = luminance(b);
  if (l1 < l2) [l1, l2] = [l2, l1];
  return (l1 + 0.05) / (l2 + 0.05);
}

const round4 = (v: number): number => Math.round(v * 1e4) / 1e4;

function evalScheme(hex: string, backgrounds: readonly string[]): AccentScheme {
  let link = Infinity;
  for (const bg of backgrounds) link = Math.min(link, accentContrast(hex, bg));
  let text: string = ACCENT_TEXT_CANDIDATES[0];
  let button = 0;
  for (const cand of ACCENT_TEXT_CANDIDATES) {
    const r = accentContrast(hex, cand);
    if (r > button) {
      text = cand;
      button = r;
    }
  }
  return {
    accent: hex,
    buttonText: text,
    linkRatio: round4(link),
    buttonRatio: round4(button),
    pass: link >= ACCENT_MIN_RATIO && button >= ACCENT_MIN_RATIO,
  };
}

function passes(hex: string, backgrounds: readonly string[]): boolean {
  for (const bg of backgrounds) if (accentContrast(hex, bg) < ACCENT_MIN_RATIO) return false;
  let best = 0;
  for (const cand of ACCENT_TEXT_CANDIDATES) best = Math.max(best, accentContrast(hex, cand));
  return best >= ACCENT_MIN_RATIO;
}

function rgbToHsl(hex: string): [number, number, number] {
  const [r8, g8, b8] = parseHex(hex);
  const r = r8 / 255;
  const g = g8 / 255;
  const b = b8 / 255;
  const max = Math.max(r, g, b);
  const min = Math.min(r, g, b);
  const l = (max + min) / 2;
  if (max === min) return [0, 0, l];
  const d = max - min;
  const s = l > 0.5 ? d / (2 - max - min) : d / (max + min);
  let h: number;
  if (max === r) h = (g - b) / d + (g < b ? 6 : 0);
  else if (max === g) h = (b - r) / d + 2;
  else h = (r - g) / d + 4;
  return [h / 6, s, l];
}

function hue2rgb(p: number, q: number, t0: number): number {
  let t = t0;
  if (t < 0) t += 1;
  if (t > 1) t -= 1;
  if (t < 1 / 6) return p + (q - p) * 6 * t;
  if (t < 1 / 2) return q;
  if (t < 2 / 3) return p + (q - p) * (2 / 3 - t) * 6;
  return p;
}

function hslToHex(h: number, s: number, l: number): string {
  let r: number;
  let g: number;
  let b: number;
  if (s === 0) {
    r = g = b = l;
  } else {
    const q = l < 0.5 ? l * (1 + s) : l + s - l * s;
    const p = 2 * l - q;
    r = hue2rgb(p, q, h + 1 / 3);
    g = hue2rgb(p, q, h);
    b = hue2rgb(p, q, h - 1 / 3);
  }
  return toHex(Math.round(r * 255), Math.round(g * 255), Math.round(b * 255));
}

/** Walks lightness away from hex in STEP increments, first in the preferFirst direction (-1 darker, +1
 * lighter), and returns the first color that passes; "" when none does. */
function nearestPassing(hex: string, backgrounds: readonly string[], preferFirst: 1 | -1): string {
  const [h, s, l] = rgbToHsl(hex);
  for (let k = 1; k <= MAX_STEPS; k++) {
    for (const dir of [preferFirst, -preferFirst]) {
      const nl = l + dir * k * STEP;
      if (nl < 0 || nl > 1) continue;
      const cand = hslToHex(h, s, nl);
      if (passes(cand, backgrounds)) return cand;
    }
  }
  return "";
}

/** Validates a #rrggbb accent for the shop. Anything else returns ok false with empty schemes (report the
 * format problem first). */
export function checkAccentColor(input: string): AccentCheck {
  if (!HEX_RE.test(input)) return { ok: false, accent: "", light: EMPTY_SCHEME, dark: EMPTY_SCHEME, suggestion: "" };
  const hex = input.toLowerCase();
  const light = evalScheme(hex, ACCENT_LIGHT_BACKGROUNDS);
  if (!light.pass) {
    return { ok: false, accent: hex, light, dark: EMPTY_SCHEME, suggestion: nearestPassing(hex, ACCENT_LIGHT_BACKGROUNDS, -1) };
  }
  const darkAccent = passes(hex, ACCENT_DARK_BACKGROUNDS) ? hex : nearestPassing(hex, ACCENT_DARK_BACKGROUNDS, 1);
  if (darkAccent === "") {
    return { ok: false, accent: hex, light, dark: evalScheme(hex, ACCENT_DARK_BACKGROUNDS), suggestion: "" };
  }
  const dark = evalScheme(darkAccent, ACCENT_DARK_BACKGROUNDS);
  return { ok: dark.pass, accent: hex, light, dark, suggestion: "" };
}

const floor2 = (v: number): number => Math.floor(v * 100) / 100;

/** The one-line message server config validation reports for a rejected accent, or "" when the color is valid
 * or empty. Ratios are floored to two decimals so "4.50" never means 4.496. */
export function accentProblem(input: string): string {
  if (input === "") return "";
  if (!HEX_RE.test(input)) {
    return "Accent color: must be a color like #9c4212 (six hex digits), or empty for the theme accent";
  }
  const res = checkAccentColor(input);
  if (res.ok) return "";
  if (!res.light.pass) {
    // Name only the pairs that fail: listing a passing ratio as if it failed misleads (A7).
    const failing: string[] = [];
    if (res.light.linkRatio < ACCENT_MIN_RATIO) failing.push(`links measure ${floor2(res.light.linkRatio).toFixed(2)}:1`);
    if (res.light.buttonRatio < ACCENT_MIN_RATIO) failing.push(`button text measures ${floor2(res.light.buttonRatio).toFixed(2)}:1`);
    const need = failing.length === 2 ? "but both need 4.5:1" : "but need 4.5:1";
    let msg = `Accent color: ${res.accent} doesn't have enough contrast on the shop background: ${failing.join(" and ")}, ${need}`;
    if (res.suggestion) msg += `; try ${res.suggestion}`;
    return msg;
  }
  return `Accent color: no dark-mode variant of ${res.accent} reaches 4.5:1; pick a different color`;
}
