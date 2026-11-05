import { describe, expect, it } from "vitest";
import { parseInline, plainInline } from "./guideInline";

describe("parseInline", () => {
  it("splits bold, code, and topic links from plain text", () => {
    expect(parseInline("Press **Save** and see [Quote a print](#/guide#quote-a-print), or enter `**x**`.")).toEqual([
      { kind: "text", text: "Press " },
      { kind: "bold", text: "Save" },
      { kind: "text", text: " and see " },
      { kind: "link", text: "Quote a print", href: "#/guide#quote-a-print" },
      { kind: "text", text: ", or enter " },
      { kind: "code", text: "**x**" },
      { kind: "text", text: "." },
    ]);
  });
  it("leaves text without markup alone, and never produces markup from it", () => {
    expect(parseInline("a < b & <b>c</b>")).toEqual([{ kind: "text", text: "a < b & <b>c</b>" }]);
  });
  it("only links to guide topics", () => {
    expect(parseInline("[x](https://example.com)")).toEqual([{ kind: "text", text: "[x](https://example.com)" }]);
  });
  it("plainInline drops the markup", () => {
    expect(plainInline("Press **Save** in `code`.")).toBe("Press Save in code.");
  });
});
