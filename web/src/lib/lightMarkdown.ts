// Light markdown for the shop's intro and announcement. One small parser, no dependency, and no HTML:
// it returns a token tree that RichText.svelte renders as Svelte markup, so owner text is never parsed as HTML.
// server/lightmarkdown.go is a line-for-line port that produces the plain text for <meta name="description">;
// testdata/light-markdown-vectors.json keeps the two in step.
//
// Supported: blank-line paragraphs, single line breaks, "- " bullet lists, **bold**, *italic* and
// [text](https://…) / [text](mailto:…) links. Everything else (underscores, headings, HTML, other URL
// schemes) stays literal text.

export type Inline =
  | { t: "text"; v: string }
  | { t: "strong"; c: Inline[] }
  | { t: "em"; c: Inline[] }
  | { t: "a"; href: string; c: Inline[] };

export type Block =
  | { t: "p"; lines: Inline[][] } // one entry per source line; the renderer puts a line break between them
  | { t: "ul"; items: Inline[][] };

/** The deepest strong/em/link nesting that is parsed; deeper markers stay literal. */
const MAX_DEPTH = 4;

// https with a host, or mailto with an address. No whitespace, controls, quotes, angle brackets, backticks or backslashes.
const HTTPS_URL = /^https:\/\/[^\x00-\x20\x7f<>"'\x60\\/?#][^\x00-\x20\x7f<>"'\x60\\]*$/i;
const MAILTO_URL = /^mailto:[^\x00-\x20\x7f<>"'\x60\\]+$/i;

/** True for a URL the parser turns into a link. Exported for the renderer and the tests. */
export function isSafeUrl(url: string): boolean {
  return HTTPS_URL.test(url) || MAILTO_URL.test(url);
}

// ASCII-only trim, so the Go port (strings.Trim) behaves the same on exotic Unicode spaces.
const trimWs = (s: string): string => s.replace(/^[ \t\n\r\f\v]+|[ \t\n\r\f\v]+$/g, "");

const isSpace = (ch: string | undefined): boolean => ch === " " || ch === "\t";

function pushText(out: Inline[], v: string): void {
  if (v === "") return;
  const last = out[out.length - 1];
  if (last && last.t === "text") last.v += v;
  else out.push({ t: "text", v });
}

/** Parses one line of inline markup. */
export function parseInline(s: string, depth = 0, allowLink = true): Inline[] {
  const out: Inline[] = [];
  let i = 0;
  while (i < s.length) {
    const ch = s[i] as string;
    if (ch === "*" && depth < MAX_DEPTH) {
      if (s[i + 1] === "*") {
        const j = closeStrong(s, i + 2);
        if (j >= 0) {
          out.push({ t: "strong", c: parseInline(s.slice(i + 2, j), depth + 1, allowLink) });
          i = j + 2;
          continue;
        }
        pushText(out, "**");
        i += 2;
        continue;
      }
      if (i + 1 < s.length && !isSpace(s[i + 1])) {
        const j = closeEm(s, i + 1);
        if (j >= 0) {
          out.push({ t: "em", c: parseInline(s.slice(i + 1, j), depth + 1, allowLink) });
          i = j + 1;
          continue;
        }
      }
    } else if (ch === "[" && allowLink && depth < MAX_DEPTH) {
      const m = s.indexOf("]", i + 1);
      if (m > i + 1 && s[m + 1] === "(") {
        const n = s.indexOf(")", m + 2);
        if (n >= 0) {
          const url = s.slice(m + 2, n);
          if (isSafeUrl(url)) {
            out.push({ t: "a", href: url, c: parseInline(s.slice(i + 1, m), depth + 1, false) });
            i = n + 1;
            continue;
          }
        }
      }
    }
    pushText(out, ch);
    i++;
  }
  return out;
}

/** Index of the "**" that closes a bold run starting at from, or -1. The run is non-empty and has no edge spaces. */
function closeStrong(s: string, from: number): number {
  if (from >= s.length || isSpace(s[from])) return -1;
  for (let k = from; k + 1 < s.length; k++) {
    if (s[k] === "*" && s[k + 1] === "*" && k > from && !isSpace(s[k - 1])) return k;
  }
  return -1;
}

/** Index of the "*" that closes an italic run starting at from, or -1. "**" pairs inside the run are skipped. */
function closeEm(s: string, from: number): number {
  for (let k = from; k < s.length; k++) {
    if (s[k] !== "*") continue;
    if (s[k + 1] === "*") {
      k++;
      continue;
    }
    if (k > from && !isSpace(s[k - 1])) return k;
  }
  return -1;
}

/** Parses owner text into blocks. */
export function parseLightMarkdown(src: string): Block[] {
  const blocks: Block[] = [];
  let para: Inline[][] | null = null;
  let list: Inline[][] | null = null;
  const flush = () => {
    if (para) blocks.push({ t: "p", lines: para });
    if (list) blocks.push({ t: "ul", items: list });
    para = null;
    list = null;
  };
  for (const raw of src.replace(/\r\n?/g, "\n").split("\n")) {
    const line = trimWs(raw);
    if (line === "") {
      flush();
    } else if (line.startsWith("- ") && trimWs(line.slice(2)) !== "") {
      if (para) flush();
      list ??= [];
      list.push(parseInline(trimWs(line.slice(2))));
    } else {
      if (list) flush();
      para ??= [];
      para.push(parseInline(line));
    }
  }
  flush();
  return blocks;
}

function inlinePlain(nodes: Inline[]): string {
  let s = "";
  for (const n of nodes) s += n.t === "text" ? n.v : inlinePlain(n.c);
  return s;
}

/** The text with all markup removed, on one line: paragraphs, line breaks and list items join with a space. */
export function lightMarkdownToPlain(src: string): string {
  const parts: string[] = [];
  for (const b of parseLightMarkdown(src)) {
    if (b.t === "p") for (const l of b.lines) parts.push(inlinePlain(l));
    else for (const it of b.items) parts.push(inlinePlain(it));
  }
  return trimWs(parts.join(" ").replace(/[ \t\n\r\f\v]+/g, " "));
}
