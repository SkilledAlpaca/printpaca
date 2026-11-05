// Client-side search over the Guide's content. Pure functions: no DOM, no server. The Guide page renders the
// snippet segments as DOM nodes (a <mark> for each match), never as HTML.
import { GUIDE_TASKS, GUIDE_TOPICS, SHORTCUTS } from "./guideContent";
import type { GuideTopic } from "./guideContent";
import { ENV_ONLY_ROWS, OTHER_PAGE_ROWS, RECORD_FLOW, SAVE_BRANCHES, SAVE_MAIN, SETTINGS_PAGE_ROWS } from "./guideDiagrams";
import { plainInline } from "./guideInline";

export interface SearchEntry {
  id: string;
  title: string;
  /** Searchable passages in reading order: the summary, then each step, and so on. */
  passages: string[];
}

export interface SnippetPart {
  text: string;
  mark: boolean;
}

export interface SearchResult {
  id: string;
  title: string;
  snippet: SnippetPart[];
}

const SNIPPET_CHARS = 120;

/** The distinct lower-case words of a query, split on whitespace. Empty for a blank query. */
export function queryWords(query: string): string[] {
  return [...new Set(query.toLowerCase().split(/\s+/).filter(Boolean))];
}

const escapeRe = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

/** Splits `text` into parts, marking every occurrence of any word (case-insensitive). */
export function markWords(text: string, words: string[]): SnippetPart[] {
  if (!words.length || !text) return text ? [{ text, mark: false }] : [];
  const re = new RegExp(`(${[...words].sort((a, b) => b.length - a.length).map(escapeRe).join("|")})`, "gi");
  return text
    .split(re)
    .filter((t) => t !== "")
    .map((t, i) => ({ text: t, mark: i % 2 === 1 }));
}

/** A short window of `passage` around its first match, cut at word boundaries and marked with ellipses. */
function windowAround(passage: string, words: string[]): string {
  if (passage.length <= SNIPPET_CHARS) return passage;
  const lower = passage.toLowerCase();
  const at = Math.min(...words.map((w) => lower.indexOf(w)).filter((i) => i >= 0));
  let start = Math.max(0, at - Math.floor(SNIPPET_CHARS / 3));
  let end = Math.min(passage.length, start + SNIPPET_CHARS);
  start = Math.max(0, end - SNIPPET_CHARS);
  if (start > 0) {
    const sp = passage.indexOf(" ", start);
    if (sp >= 0 && sp < at) start = sp + 1;
  }
  if (end < passage.length) {
    const sp = passage.lastIndexOf(" ", end);
    if (sp > at) end = sp;
  }
  return `${start > 0 ? "…" : ""}${passage.slice(start, end)}${end < passage.length ? "…" : ""}`;
}

/** The entries whose title and passages together contain every word, title matches first, then in the given order. */
export function searchGuide(entries: SearchEntry[], query: string): SearchResult[] | null {
  const words = queryWords(query);
  if (!words.length) return null;
  const hits: { r: SearchResult; titleHit: boolean; order: number }[] = [];
  entries.forEach((e, order) => {
    const lowerTitle = e.title.toLowerCase();
    const haystack = `${lowerTitle}\n${e.passages.join("\n").toLowerCase()}`;
    if (!words.every((w) => haystack.includes(w))) return;
    // The passage that holds the most of the words (the first one on a tie) is the snippet.
    let best = "";
    let bestCount = 0;
    for (const p of e.passages) {
      const lower = p.toLowerCase();
      const count = words.filter((w) => lower.includes(w)).length;
      if (count > bestCount) {
        best = p;
        bestCount = count;
      }
    }
    const text = bestCount ? windowAround(best, words) : "";
    hits.push({ r: { id: e.id, title: e.title, snippet: markWords(text, words) }, titleHit: words.every((w) => lowerTitle.includes(w)), order });
  });
  hits.sort((a, b) => Number(b.titleHit) - Number(a.titleHit) || a.order - b.order);
  return hits.map((h) => h.r);
}

/** "4 topics match", "1 topic matches", "No topics match": the polite announcement for a result count. */
export function resultCountText(count: number): string {
  if (count === 0) return "No topics match";
  return count === 1 ? "1 topic matches" : `${count} topics match`;
}

/** The searchable passages of a reference topic: its intro, section headings, and every paragraph, bullet, and table cell. */
export function topicPassages(t: GuideTopic): string[] {
  const out = [plainInline(t.intro)];
  for (const sec of t.sections) {
    out.push(sec.heading);
    for (const b of sec.blocks) {
      if (b.kind === "p") out.push(plainInline(b.text));
      else if (b.kind === "list") out.push(...b.items.map(plainInline));
      else if (b.kind === "table") out.push(...b.rows.map((r) => r.map(plainInline).join(". ")));
      else if (b.id === "settings-map") {
        out.push(...[...SETTINGS_PAGE_ROWS, ...OTHER_PAGE_ROWS].map((r) => `${r.where}: ${r.keys.join(", ")}`), ...ENV_ONLY_ROWS.map((r) => `${r.name}: ${r.what}`));
      } else out.push(...[...SAVE_MAIN, ...SAVE_BRANCHES.map((x) => x.box), ...RECORD_FLOW].map((x) => `${x.title}. ${x.detail}`));
    }
  }
  return out;
}

/** Search entries for every topic in the Guide, built from the content (tasks, concepts, shortcuts). */
export const GUIDE_SEARCH_ENTRIES: SearchEntry[] = [
  ...GUIDE_TASKS.map((t) => ({
    id: t.id,
    title: t.title,
    passages: [
      t.summary,
      ...t.steps.flatMap((s) => [plainInline(s.text), ...(s.subs ?? []).map(plainInline), s.after ? plainInline(s.after) : ""]),
      t.next ? plainInline(t.next) : "",
    ].filter(Boolean),
  })),
  ...GUIDE_TOPICS.map((t) => ({ id: t.id, title: t.title, passages: topicPassages(t) })),
  {
    id: "keyboard-shortcuts",
    title: "Keyboard shortcuts",
    passages: SHORTCUTS.map((sc) => `${sc.keys}: ${sc.action} (${sc.where})`),
  },
];
