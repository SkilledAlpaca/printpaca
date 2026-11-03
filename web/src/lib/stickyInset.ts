// Sticky bars cover the focused field unless the scroll container reserves their height (WCAG 2.4.11 Focus Not
// Obscured). A bar that is sticky to the top or bottom of the scroll container uses this action to publish its
// measured height as `--sticky-top-h` or `--sticky-bottom-h` on <html>; App.svelte's `scroll-padding-*` rules read
// them, so the padding tracks the real bar (it grows when the save bar shows an error list or wraps on a phone).
export type StickyEdge = "top" | "bottom";

/** Svelte action. Pass `{ edge, active }`; `active: false` (a bar that is only sticky in some states) reserves nothing. */
export function stickyInset(node: HTMLElement, opts: { edge: StickyEdge; active?: boolean }) {
  let current = opts;
  const prop = (edge: StickyEdge) => `--sticky-${edge}-h`;
  const root = document.documentElement;
  const apply = () => {
    if (current.active === false) root.style.removeProperty(prop(current.edge));
    else root.style.setProperty(prop(current.edge), `${node.offsetHeight}px`);
  };
  apply();
  const observer = new ResizeObserver(apply);
  observer.observe(node);
  return {
    update(next: { edge: StickyEdge; active?: boolean }) {
      root.style.removeProperty(prop(current.edge));
      current = next;
      apply();
    },
    destroy() {
      observer.disconnect();
      root.style.removeProperty(prop(current.edge));
    },
  };
}
