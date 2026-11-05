// Inline markup for the Guide's text (guideContent.ts): **bold** for a UI name, `code` for a literal, and
// [label](#/guide#topic) for a link to another topic. Anything else is plain text. Rendered as segments, never as
// HTML, so the copy can't inject markup.

export type InlineSegment =
  | { kind: "text"; text: string }
  | { kind: "bold"; text: string }
  | { kind: "code"; text: string }
  | { kind: "link"; text: string; href: string };

const TOKEN = /`([^`]+)`|\*\*([^*]+)\*\*|\[([^\]]+)\]\((#\/guide#[a-z0-9-]+)\)/g;

export function parseInline(text: string): InlineSegment[] {
  const out: InlineSegment[] = [];
  let last = 0;
  for (const m of text.matchAll(TOKEN)) {
    const at = m.index ?? 0;
    if (at > last) out.push({ kind: "text", text: text.slice(last, at) });
    if (m[1] !== undefined) out.push({ kind: "code", text: m[1] });
    else if (m[2] !== undefined) out.push({ kind: "bold", text: m[2] });
    else out.push({ kind: "link", text: m[3]!, href: m[4]! });
    last = at + m[0].length;
  }
  if (last < text.length) out.push({ kind: "text", text: text.slice(last) });
  return out;
}

/** The text with the markup removed, as a reader sees it. Tests and the e2e deep-link check use it. */
export function plainInline(text: string): string {
  return parseInline(text)
    .map((s) => s.text)
    .join("");
}
