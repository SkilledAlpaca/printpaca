// Name and email the visitor already typed on this page. Shared by the custom-estimate form and the item
// order panel so a second submit in the same visit doesn't ask again (WCAG 3.3.7 Redundant Entry). In memory
// only: a reload starts clean, and nothing here is persisted or sent anywhere.
export const visitor = $state({ name: "", email: "" });
