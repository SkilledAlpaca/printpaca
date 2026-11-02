// WCAG contrast helpers for contrast.test.ts (the theme's accessibility regression gate); dependency-free so
// runtime code can reuse them.

type Rgb = { r: number; g: number; b: number };

function hexToRgb(hex: string): Rgb {
  const h = hex.trim().replace(/^#/, "");
  const full = h.length === 3 ? h.split("").map((c) => c + c).join("") : h;
  if (!/^[0-9a-fA-F]{6}$/.test(full)) {
    throw new Error(`hexToRgb: not a 3- or 6-digit hex color: "${hex}"`);
  }
  const num = parseInt(full, 16);
  return { r: (num >> 16) & 255, g: (num >> 8) & 255, b: num & 255 };
}

function toHex2(n: number): string {
  return Math.max(0, Math.min(255, Math.round(n))).toString(16).padStart(2, "0");
}

function rgbToHex({ r, g, b }: Rgb): string {
  return `#${toHex2(r)}${toHex2(g)}${toHex2(b)}`;
}

function srgbChannelToLinear(c255: number): number {
  const c = c255 / 255;
  return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4);
}

function relativeLuminance(hex: string): number {
  const { r, g, b } = hexToRgb(hex);
  const R = srgbChannelToLinear(r);
  const G = srgbChannelToLinear(g);
  const B = srgbChannelToLinear(b);
  return 0.2126 * R + 0.7152 * G + 0.0722 * B;
}

export function contrastRatio(hexA: string, hexB: string): number {
  const L1 = relativeLuminance(hexA);
  const L2 = relativeLuminance(hexB);
  const lighter = Math.max(L1, L2);
  const darker = Math.min(L1, L2);
  return (lighter + 0.05) / (darker + 0.05);
}

/** Resolves CSS `color-mix(in srgb, A p%, B)` to hex: a per-channel lerp of gamma-encoded 0-255 components
 * (no linearization). Alpha is not handled; every mixed token is opaque. */
export function mixSrgb(hexA: string, percentA: number, hexB: string): string {
  const a = hexToRgb(hexA);
  const b = hexToRgb(hexB);
  const t = percentA / 100;
  return rgbToHex({
    r: a.r * t + b.r * (1 - t),
    g: a.g * t + b.g * (1 - t),
    b: a.b * t + b.b * (1 - t),
  });
}
