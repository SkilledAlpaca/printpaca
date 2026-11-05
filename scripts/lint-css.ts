#!/usr/bin/env -S node --experimental-strip-types
// CSS spacing and width lint. Flags raw spacing and width literals (margin, padding, gap, width,
// min-width, max-width written as 12px, 0.75rem, 20ch ...) so layout code reaches for the tokens in
// web/src/theme.css (--space-*, --gap, --field-*, --content-max, ...) instead of inventing values.
//
//   Blocking files (scripts/css-lint.config.json "blocking"): a violation fails the run (exit 1).
//   Every other scanned file: violations are warnings, counted per file, so unmigrated screens never block CI.
//   Exceptions: scripts/css-lint.config.json "exceptions", a reviewed list. Each entry names a file, a property,
//   the exact value and a reason. A stale entry (nothing matches it any more) fails the run, so the list can't rot.
//
// Allowed everywhere without an entry: 0, %, auto, fit-content/min-content/max-content, any value built only from
// var(...) tokens, custom property definitions (--x: ...; theme.css owns the tokens), @media conditions, @media
// print blocks, @font-face, @keyframes, and accessibility sizing (min-width: 24px or 44px).
//
// Usage: node --experimental-strip-types scripts/lint-css.ts [--verbose]   (npm run lint:css)
import { readFileSync, readdirSync, statSync } from "node:fs";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

export interface Violation {
  file: string;
  line: number;
  selector: string;
  property: string;
  value: string;
}

export interface ExceptionEntry {
  file: string;
  property: string;
  value: string;
  /** Optional substring of the rule's selector, to tell two identical declarations in one file apart. */
  selector?: string;
  reason: string;
}

export interface LintConfig {
  blocking: string[];
  scan: string[];
  exceptions: ExceptionEntry[];
}

const CHECKED_PROPERTY =
  /^(?:(?:margin|padding)(?:-(?:top|right|bottom|left|inline|block)(?:-(?:start|end))?)?|(?:row-|column-)?gap|(?:min-|max-)?(?:width|inline-size))$/;
const LITERAL_LENGTH = /(?<![\w.#-])-?(?:\d*\.)?\d+(?:px|rem|em|ch|ex|vw|vh|vmin|vmax|cm|mm|in|pt)\b/gi;
const SKIPPED_AT_RULES = /^@(?:media\s+print|font-face|keyframes|-webkit-keyframes|page)\b/i;

/** Removes var(...) calls (balanced), so a fallback inside one never counts as a literal. */
function stripVars(value: string): string {
  let out = "";
  let i = 0;
  while (i < value.length) {
    if (value.startsWith("var(", i)) {
      let depth = 0;
      let j = i + 3;
      for (; j < value.length; j++) {
        if (value[j] === "(") depth++;
        else if (value[j] === ")" && --depth === 0) break;
      }
      out += " ";
      i = j + 1;
    } else {
      out += value[i];
      i++;
    }
  }
  return out;
}

/** True when the declaration writes a raw spacing or width literal that the rules don't allow. */
export function isRawLiteral(property: string, rawValue: string): boolean {
  if (!CHECKED_PROPERTY.test(property)) return false;
  const value = rawValue.replace(/!important/i, "").trim();
  if (property === "min-width" && /^(?:24|44)px$/.test(value)) return false; // accessibility sizing (WCAG 2.5.8, 2.5.5)
  const remaining = stripVars(value);
  for (const match of remaining.matchAll(LITERAL_LENGTH)) {
    if (/^-?0*\.?0+[a-z]*$/i.test(match[0])) continue; // 0px, 0rem
    return true;
  }
  return false;
}

/** Extracts the CSS to check: the <style> blocks of a .svelte file, or the whole file for .css. Returns text with
 * every non-CSS character blanked to a space, so offsets (and therefore line numbers) stay those of the file. */
function cssRegions(file: string, source: string): string {
  if (!file.endsWith(".svelte")) return source;
  let out = source.replace(/[^\n]/g, " ");
  const chars = out.split("");
  for (const m of source.matchAll(/<style\b[^>]*>([\s\S]*?)<\/style>/gi)) {
    const start = (m.index ?? 0) + m[0].indexOf(m[1] ?? "");
    const body = m[1] ?? "";
    for (let k = 0; k < body.length; k++) chars[start + k] = body[k] as string;
  }
  out = chars.join("");
  return out;
}

/** Lints one file's text. Pure: no exceptions applied, no file access. */
export function lintSource(file: string, source: string): Violation[] {
  const text = cssRegions(file, source).replace(/\/\*[\s\S]*?\*\//g, (c) => c.replace(/[^\n]/g, " "));
  const violations: Violation[] = [];
  const stack: { prelude: string; skip: boolean }[] = [];
  let buffer = "";
  let bufferStart = 0;
  const lineAt = (offset: number) => text.slice(0, offset).split("\n").length;

  const declaration = (offset: number) => {
    const decl = buffer.trim();
    buffer = "";
    if (stack.length === 0 || stack.some((s) => s.skip)) return;
    const colon = decl.indexOf(":");
    if (colon < 1) return;
    const property = decl.slice(0, colon).trim().toLowerCase();
    if (property.startsWith("--")) return;
    const value = decl.slice(colon + 1).trim().replace(/\s+/g, " ");
    if (!isRawLiteral(property, value)) return;
    const rule = [...stack].reverse().find((s) => !s.prelude.startsWith("@"));
    violations.push({ file, line: lineAt(offset), selector: rule?.prelude.replace(/\s+/g, " ") ?? "", property, value });
  };

  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (c === "{") {
      const prelude = buffer.trim().replace(/\s+/g, " ");
      stack.push({ prelude, skip: SKIPPED_AT_RULES.test(prelude) });
      buffer = "";
    } else if (c === "}") {
      if (buffer.trim() !== "") declaration(bufferStart);
      stack.pop();
      buffer = "";
    } else if (c === ";") {
      declaration(bufferStart);
    } else {
      if (buffer.trim() === "" && !/\s/.test(c as string)) bufferStart = i;
      buffer += c;
    }
  }
  return violations;
}

function matchesGlob(rel: string, pattern: string): boolean {
  if (pattern.endsWith("/**")) return rel.startsWith(pattern.slice(0, -2));
  return rel === pattern;
}

function exceptionMatches(e: ExceptionEntry, v: Violation): boolean {
  return e.file === v.file && e.property === v.property && e.value === v.value && (e.selector === undefined || v.selector.includes(e.selector));
}

export interface LintResult {
  errors: Violation[];
  warnings: Violation[];
  staleExceptions: ExceptionEntry[];
}

/** Applies the config to a set of violations: exceptions drop out, the rest split into errors (blocking files) and warnings. */
export function classify(violations: Violation[], config: Pick<LintConfig, "blocking" | "exceptions">): LintResult {
  const used = new Set<ExceptionEntry>();
  const errors: Violation[] = [];
  const warnings: Violation[] = [];
  for (const v of violations) {
    const exception = config.exceptions.find((e) => exceptionMatches(e, v));
    if (exception) {
      used.add(exception);
      continue;
    }
    (config.blocking.some((p) => matchesGlob(v.file, p)) ? errors : warnings).push(v);
  }
  return { errors, warnings, staleExceptions: config.exceptions.filter((e) => !used.has(e)) };
}

function walk(dir: string, out: string[]): void {
  for (const name of readdirSync(dir)) {
    if (name === "node_modules" || name === "dist") continue;
    const full = path.join(dir, name);
    if (statSync(full).isDirectory()) walk(full, out);
    else out.push(full);
  }
}

function main(): number {
  const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
  const config = JSON.parse(readFileSync(path.join(root, "scripts", "css-lint.config.json"), "utf8")) as LintConfig;
  const verbose = process.argv.includes("--verbose");
  const files: string[] = [];
  walk(path.join(root, "web", "src"), files);
  const violations: Violation[] = [];
  for (const abs of files) {
    const rel = path.relative(root, abs).split(path.sep).join("/");
    if (!config.scan.some((ext) => rel.endsWith(ext))) continue;
    violations.push(...lintSource(rel, readFileSync(abs, "utf8")));
  }
  const result = classify(violations, config);

  for (const v of result.errors) console.error(`error  ${v.file}:${v.line}  ${v.property}: ${v.value}  (${v.selector})`);
  for (const e of result.staleExceptions) console.error(`error  stale exception: ${e.file} ${e.property}: ${e.value} (${e.reason})`);

  const perFile = new Map<string, Violation[]>();
  for (const w of result.warnings) perFile.set(w.file, [...(perFile.get(w.file) ?? []), w]);
  for (const [file, list] of [...perFile].sort((a, b) => b[1].length - a[1].length)) {
    console.log(`warn   ${file}: ${list.length} raw spacing or width literal${list.length === 1 ? "" : "s"} (not migrated yet)`);
    if (verbose) for (const v of list) console.log(`         line ${v.line}  ${v.property}: ${v.value}`);
  }
  console.log(
    `css-lint: ${result.errors.length} error(s), ${result.staleExceptions.length} stale exception(s), ${result.warnings.length} warning(s) in ${perFile.size} unmigrated file(s)`,
  );
  return result.errors.length + result.staleExceptions.length > 0 ? 1 : 0;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) process.exit(main());
