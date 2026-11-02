/** Boxed like settingsDirty: a detail form (client, item, invoice) mirrors its dirty state here while it is
 * open and clears it on unmount. App.svelte's tab-switch guards read it to confirm before the form is
 * discarded. */
export const unsavedForm: { current: boolean } = $state({ current: false });
