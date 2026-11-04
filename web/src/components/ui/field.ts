// Pure helpers for ui/Field and friends.

/** Ids of the elements that describe a control, in reading order, joined for aria-describedby. Falsy entries are
 * skipped, so hint, caller-supplied and error ids combine instead of replacing each other. Undefined when none. */
export function describedBy(...ids: (string | false | null | undefined)[]): string | undefined {
  const present = ids.filter((id): id is string => typeof id === "string" && id.trim() !== "").flatMap((id) => id.trim().split(/\s+/));
  const unique = [...new Set(present)];
  return unique.length > 0 ? unique.join(" ") : undefined;
}

export type FieldSize = "xs" | "s" | "m" | "l" | "full";

/** The ids a Field gives its hint and error, derived from the control's id so tests and callers can predict them. */
export function fieldIds(id: string): { hint: string; error: string } {
  return { hint: `${id}-hint`, error: `${id}-err` };
}
