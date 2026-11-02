import { describe, expect, it } from "vitest";
import { EMAIL_MAX_BYTES, NAME_MAX_BYTES, NOTE_MAX_BYTES, noteByteBudget, utf8ByteLength } from "./publicFormLimits";

describe("utf8ByteLength", () => {
  it("ASCII: byte length equals code-unit length", () => {
    expect(utf8ByteLength("hello")).toBe(5);
    expect("hello".length).toBe(5);
  });

  it("a multi-byte character counts for more bytes than JS's own .length reports (the bug this fixes)", () => {
    // U+4E2D: one UTF-16 code unit, three UTF-8 bytes.
    const s = "中";
    expect(s.length).toBe(1);
    expect(utf8ByteLength(s)).toBe(3);
  });

  it("a string past the UTF-16 astral-plane boundary (surrogate pair) still measures its true byte count", () => {
    // U+1F600: two UTF-16 code units, four UTF-8 bytes.
    const s = "😀";
    expect(s.length).toBe(2);
    expect(utf8ByteLength(s)).toBe(4);
  });

  it("a string that passes a naive .length <= 200 check can still exceed the real byte cap", () => {
    const s = "中".repeat(150); // .length === 150, well under 200; 450 bytes, well over
    expect(s.length).toBeLessThanOrEqual(NAME_MAX_BYTES);
    expect(utf8ByteLength(s)).toBeGreaterThan(NAME_MAX_BYTES);
  });

  it("empty string is zero bytes", () => {
    expect(utf8ByteLength("")).toBe(0);
  });
});

describe("noteByteBudget", () => {
  it("no model-file note pending: the full cap", () => {
    expect(noteByteBudget("")).toBe(NOTE_MAX_BYTES);
  });

  it("a pending model-file note reserves its own byte length plus the 2-byte join separator", () => {
    const note = "Model file: part.stl (weight estimated from geometry, attached).";
    expect(noteByteBudget(note)).toBe(NOTE_MAX_BYTES - utf8ByteLength(note) - 2);
  });

  it("never goes negative even if the model-file note alone exceeds the cap", () => {
    const huge = "x".repeat(NOTE_MAX_BYTES + 500);
    expect(noteByteBudget(huge)).toBe(0);
  });

  it("EMAIL_MAX_BYTES matches server/public.go's handlePublicQuoteRequest cap", () => {
    expect(EMAIL_MAX_BYTES).toBe(200);
  });
});
