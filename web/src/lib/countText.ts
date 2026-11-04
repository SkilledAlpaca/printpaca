import { utf8ByteLength } from "./publicFormLimits";

/** The counter under a length-capped text box: "812 / 2000 characters". The server's cap counts UTF-8 bytes, so a
 * plain-ASCII text reads as exactly its character count; accented letters and emoji use more than one, and the
 * note says so only when that is the case. */
export function characterCountText(text: string, max: number): string {
  const bytes = utf8ByteLength(text);
  const chars = Array.from(text).length;
  const base = `${bytes} / ${max} characters`;
  return bytes > chars ? `${base} (accented letters and emoji count as more than one)` : base;
}
