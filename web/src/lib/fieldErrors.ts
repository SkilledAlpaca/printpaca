// Ties a "Label: message" error to the form control that owns it (WCAG 3.3.1, 3.3.3; A-23). The server and
// client validators both phrase field errors as "<field label>: <problem>" (serverErrors.ts), so the label text
// is enough to find the control without threading ids through every form.

export interface ErrorTarget {
  el: HTMLElement;
  /** True when `el` is the control the error names, so it can carry aria-invalid; false for a fallback such as the
   * first control of the card that holds the problem. */
  field: boolean;
}

/** The text before the first colon, e.g. "Price" for "Price: enter a valid amount.", or null. */
export function errorLabel(message: string): string | null {
  const i = message.indexOf(":");
  return i > 0 ? message.slice(0, i).trim() : null;
}

/** The form control labelled `name` inside <main>, matching the label's own text (a trailing "(unit)" suffix is
 * ignored), or null. */
export function findFieldByLabel(name: string, scope: ParentNode = document): HTMLElement | null {
  const want = name.toLowerCase();
  for (const label of scope.querySelectorAll<HTMLLabelElement>("#main label")) {
    const own = (label.querySelector(":scope > span")?.textContent ?? label.firstChild?.textContent ?? label.textContent ?? "").trim();
    const text = own.replace(/\s*\([^)]*\)\s*$/, "").toLowerCase();
    if (text !== want) continue;
    const control = label.control;
    if (control instanceof HTMLElement) return control;
  }
  return null;
}

/** Default resolver: the control a "Label: message" error names. */
export function resolveByLabel(message: string): ErrorTarget | null {
  const name = errorLabel(message);
  const el = name ? findFieldByLabel(name) : null;
  return el ? { el, field: true } : null;
}
