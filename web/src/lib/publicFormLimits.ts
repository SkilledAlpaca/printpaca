// Client-side length checks mirroring server/public.go's handlePublicQuoteRequest caps exactly: name/email <=
// 200 bytes, note <= 2000 bytes. Go's len() counts bytes, not UTF-16 units, so multi-byte text (CJK, emoji,
// ...) could pass a .length check and 422 server-side; utf8ByteLength uses TextEncoder, matching the
// multipart body and Go's []byte(string).

export const NAME_MAX_BYTES = 200;
export const EMAIL_MAX_BYTES = 200;
export const NOTE_MAX_BYTES = 2000;

// ItemOrderPanel's email cap, mirroring handlePublicItemOrder: 320 bytes (the RFC 5321 maximum) instead of
// the quote handler's 200, so it has its own constant.
export const ITEM_ORDER_EMAIL_MAX_BYTES = 320;

export function utf8ByteLength(s: string): number {
  return new TextEncoder().encode(s).length;
}

/** Byte budget left for the visitor's note once modelFileNote (appended at submit time, not editable in the
 * textarea) counts against NOTE_MAX_BYTES. */
export function noteByteBudget(modelFileNote: string): number {
  if (modelFileNote.length === 0) return NOTE_MAX_BYTES;
  return Math.max(0, NOTE_MAX_BYTES - utf8ByteLength(modelFileNote) - 2); // "\n\n" join separator, 2 ASCII bytes
}

/** The one email-format message on the public forms; server/public.go's publicEmailFormatProblem says the same. */
export const EMAIL_FORMAT_ERROR = "Enter an email address like name@example.com";

/** Whether `email` looks like name@example.com: exactly one @, a non-empty local part, a domain with a dot that
 * is neither first nor last, and no whitespace. Mirrors server/public.go's plausiblePublicEmail. */
export function isPlausibleEmail(email: string): boolean {
  const at = email.indexOf("@");
  if (at <= 0 || email.indexOf("@", at + 1) !== -1 || /\s/.test(email)) return false;
  const domain = email.slice(at + 1);
  return domain.indexOf(".") > 0 && !domain.endsWith(".");
}
