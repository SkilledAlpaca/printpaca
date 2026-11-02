/** Boxed because an exported `let` can't be reassigned from an importing module. SettingsEditor.svelte owns
 * the write side: an $effect mirrors its `dirty` here while mounted, and onDestroy clears it (an unmount
 * discards the edit). Only one SettingsEditor is mounted at a time, so there is no cross-instance state.
 * App.svelte's activate() reads it to confirm before a tab switch would discard an in-progress edit. */
export const settingsDirty: { current: boolean } = $state({ current: false });
