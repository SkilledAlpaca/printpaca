// Name and email the visitor is typing on this page. Shared by the custom-estimate form and the item order panel
// so switching between them before submitting does not ask again (WCAG 3.3.7 Redundant Entry). In memory only:
// a reload starts clean, nothing here is persisted or sent anywhere, and clearVisitor() empties it after a
// successful submit.
export const visitor = $state({ name: "", email: "" });

/** Forgets the visitor's name and email once a request or order is sent, so the next person on a shared device
 * starts with an empty form. Until then the values carry between the estimate form and an item order. */
export function clearVisitor(): void {
  visitor.name = "";
  visitor.email = "";
}
