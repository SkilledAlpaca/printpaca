import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { isSafeUrl, lightMarkdownToPlain, parseInline, parseLightMarkdown, type Block, type Inline } from "./lightMarkdown";

// testdata/light-markdown-vectors.json is shared with server/lightmarkdown_test.go; both parsers must agree.
const fx = JSON.parse(readFileSync(resolve(__dirname, "../../../testdata/light-markdown-vectors.json"), "utf8")) as {
  vectors: { name: string; input: string; plain: string }[];
};

describe("shared light-markdown vectors (Go parity)", () => {
  it("has vectors", () => expect(fx.vectors.length).toBeGreaterThan(30));
  for (const v of fx.vectors) it(v.name, () => expect(lightMarkdownToPlain(v.input)).toBe(v.plain));
});

// A compact s-expression of the tree, so the table reads like markup.
function show(nodes: Inline[]): string {
  return nodes
    .map((n) => (n.t === "text" ? n.v : n.t === "a" ? `<a ${n.href}>${show(n.c)}</a>` : `<${n.t}>${show(n.c)}</${n.t}>`))
    .join("");
}
function showBlocks(bs: Block[]): string {
  return bs
    .map((b) => (b.t === "p" ? `P[${b.lines.map(show).join(" | ")}]` : `UL[${b.items.map(show).join(" | ")}]`))
    .join(" ");
}

describe("parseLightMarkdown tree", () => {
  const cases: [string, string, string][] = [
    ["bold", "a **b** c", "P[a <strong>b</strong> c]"],
    ["italic", "a *b* c", "P[a <em>b</em> c]"],
    ["nested", "**a *b* c**", "P[<strong>a <em>b</em> c</strong>]"],
    ["https link", "[x](https://e.com/a?b=1)", "P[<a https://e.com/a?b=1>x</a>]"],
    ["mailto link", "[x](mailto:a@b.co)", "P[<a mailto:a@b.co>x</a>]"],
    ["two paragraphs", "a\n\nb", "P[a] P[b]"],
    ["line break", "a\nb", "P[a | b]"],
    ["list", "- a\n- *b*", "UL[a | <em>b</em>]"],
    ["paragraph then list then paragraph", "x\n- a\ny", "P[x] UL[a] P[y]"],
    ["the first closing bracket ends the link text", "[a [b](https://e.com) c](https://f.com)", "P[<a https://e.com>a [b</a> c](https://f.com)]"],
    ["underscore inside a word", "snake_case_name", "P[snake_case_name]"],
    ["underscores are not emphasis", "_a_ __b__", "P[_a_ __b__]"],
    ["script is text", "<script>alert(1)</script>", "P[<script>alert(1)</script>]"],
    ["javascript link is text", "[x](javascript:alert(1))", "P[[x](javascript:alert(1))]"],
    ["data url is text", "[x](data:text/html,hi)", "P[[x](data:text/html,hi)]"],
    ["entities are text", "&lt;b&gt; &amp;", "P[&lt;b&gt; &amp;]"],
    ["unbalanced", "**a *b", "P[**a *b]"],
  ];
  for (const [name, src, want] of cases) it(name, () => expect(showBlocks(parseLightMarkdown(src))).toBe(want));

  it("never produces a node that can carry markup or an unsafe href", () => {
    const hostile = [
      "[x](javascript:alert(1))",
      "[x]( javascript:alert(1))",
      "[x](\tjavascript:alert(1))",
      "[x](java\nscript:alert(1))",
      "[x](vbscript:msgbox(1))",
      "[x](data:text/html;base64,AAAA)",
      "[x](https://e.com\"onmouseover=\"alert(1))",
      "[x](https://e.com/<script>)",
      "**[x](javascript:alert(1))**",
      "[**x**](JAVASCRIPT:alert(1))",
      "<img src=x onerror=alert(1)>",
      "[x](https://e.com/\\x)",
    ];
    const walk = (nodes: Inline[]) => {
      for (const n of nodes) {
        if (n.t === "a") expect(isSafeUrl(n.href)).toBe(true);
        if (n.t !== "text") walk(n.c);
      }
    };
    for (const h of hostile) {
      for (const b of parseLightMarkdown(h)) {
        if (b.t === "p") b.lines.forEach(walk);
        else b.items.forEach(walk);
      }
    }
  });

  it("accepts https and mailto only", () => {
    for (const ok of ["https://e.com", "HTTPS://E.COM/x?y=1#z", "mailto:a@b.co"]) expect(isSafeUrl(ok)).toBe(true);
    for (const bad of ["http://e.com", "javascript:alert(1)", "data:text/html,x", "//e.com", "/x", "https://", "https:// e.com", "ftp://e.com", ""])
      expect(isSafeUrl(bad)).toBe(false);
  });

  it("handles a long text without spaces in bounded time", () => {
    const long = "A".repeat(5000) + "[".repeat(2000) + "*".repeat(2000);
    const t0 = performance.now();
    expect(lightMarkdownToPlain(long).startsWith("A".repeat(5000) + "[".repeat(2000))).toBe(true);
    expect(performance.now() - t0).toBeLessThan(2000);
    expect(parseInline("A".repeat(5000))).toEqual([{ t: "text", v: "A".repeat(5000) }]);
  });
});
