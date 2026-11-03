// One-line, plain-language descriptions for the material picker, matched by the material's name. The public
// config carries only a name and a density, so a name this list doesn't recognise simply has no description.

const DESCRIPTIONS: ReadonlyArray<[RegExp, string]> = [
  [/\bpetg\b/i, "Tough and slightly flexible; good for functional parts and outdoor use."],
  [/\bpla\b/i, "The everyday choice: easy to print, crisp detail, many colors."],
  [/\babs\b/i, "Strong and heat resistant; used for durable parts."],
  [/\basa\b/i, "Like ABS but made for sun and weather."],
  [/\btpu\b|\bflex/i, "Rubber-like and bendy; for grips, gaskets and cases."],
  [/nylon|\bpa\d*\b/i, "Very tough and wear resistant; for gears and hinges."],
  [/\bpc\b|polycarbonate/i, "Very strong and heat resistant."],
  [/resin/i, "Smooth, finely detailed surface; for small figures and display pieces."],
];

/** The one-line description for a material name, or "" when none is known. */
export function materialDescription(name: string): string {
  for (const [pattern, text] of DESCRIPTIONS) if (pattern.test(name)) return text;
  return "";
}
