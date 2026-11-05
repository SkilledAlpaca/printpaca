import { pageTitle } from "./pageTitle.svelte";

/**
 * Svelte action for a record view's h1: mirrors its text into pageTitle.record, which App.svelte appends to
 * document.title (WCAG 2.4.2). The text can change while the view is open (a client name that loads after
 * "Client"), so it observes the node, and it clears the record segment when the view closes.
 * Used by ui/PageHeader so every record view gets its title from one place.
 */
export function recordTitle(node: HTMLElement) {
  const sync = () => {
    pageTitle.record = (node.textContent ?? "").trim();
  };
  sync();
  const observer = new MutationObserver(sync);
  observer.observe(node, { childList: true, characterData: true, subtree: true });
  return {
    destroy() {
      observer.disconnect();
      pageTitle.record = "";
    },
  };
}
