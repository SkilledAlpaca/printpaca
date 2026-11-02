export const UNSAVED_PROMPT = "You have unsaved changes. Leave without saving?";

/** True when the Escape key should do nothing for this event target: text entry (input, textarea, select, or
 * contenteditable) uses Escape for its own purposes such as dismissing autocomplete. */
export function escapeBelongsToField(target: EventTarget | null): boolean {
  if (!target || typeof (target as HTMLElement).tagName !== "string") return false;
  const el = target as HTMLElement;
  const tag = el.tagName;
  return tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || el.isContentEditable === true;
}

/** Returns true when it is fine to leave the view: nothing is unsaved, or the user confirms. */
export function canLeave(dirty: boolean, confirmFn: (message: string) => boolean = (m) => confirm(m)): boolean {
  return !dirty || confirmFn(UNSAVED_PROMPT);
}
