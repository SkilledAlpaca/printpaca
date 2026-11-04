/** Page-level keyboard shortcuts for editors that have a Save button (App.svelte's Ctrl/Cmd+S handler).
 *
 * The quote tab keeps its own mechanism (`quoteActions.save` in state.svelte.ts). Every other editor calls
 * `registerPageSave(fn)` while it is mounted, for example from an `$effect`:
 *
 *   $effect(() => registerPageSave(() => void save()));
 *
 * The returned function unregisters. Registrations form a stack, so when two editors are mounted at once (an
 * embedded SettingsEditor under an invoice editor) the one registered last receives the shortcut. The handler
 * owns `preventDefault`, so `fn` must tolerate being called when there is nothing to save and do nothing.
 *
 * Ctrl/Cmd+K is declared in App.svelte's FILTER_IDS (tab to the id of its filter input); the footer hint
 * lists a shortcut only while its target exists. */

interface Entry {
  fn: () => void;
}

const stack: Entry[] = [];

/** Reactive view of the stack for the footer hint: true while any page save is registered. */
export const pageSave: { active: boolean } = $state({ active: false });

/** Registers `fn` as the Ctrl/Cmd+S action for the current page and returns the unregister function. */
export function registerPageSave(fn: () => void): () => void {
  const entry: Entry = { fn };
  stack.push(entry);
  pageSave.active = true;
  return () => {
    const i = stack.indexOf(entry);
    if (i >= 0) stack.splice(i, 1);
    pageSave.active = stack.length > 0;
  };
}

/** Runs the newest registered page save. Returns false when none is registered. */
export function runPageSave(): boolean {
  const top = stack[stack.length - 1];
  if (!top) return false;
  top.fn();
  return true;
}
