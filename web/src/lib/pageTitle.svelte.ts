// Record name for the document title (WCAG 2.4.2). A detail view (an open invoice, client, item, or request)
// sets `pageTitle.record` to the record's name while it is open and back to "" when it closes; App.svelte
// composes "<record> · <tab> · printpaca" from it. Tabs without a detail view never touch it.
export const pageTitle = $state({ record: "" });
