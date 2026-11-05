/**
 * Scroll `container` just enough that `el` is fully visible inside it (the same result as
 * scrollIntoView({ block: "nearest" }) but limited to this one container, so the page never jumps). Does nothing
 * when the container does not scroll or the element is already visible.
 */
export function revealInNav(container: HTMLElement | null | undefined, el: HTMLElement | null | undefined): void {
  if (!container || !el) return;
  if (container.scrollHeight <= container.clientHeight) return;
  const c = container.getBoundingClientRect();
  const e = el.getBoundingClientRect();
  if (e.top < c.top) container.scrollTop -= c.top - e.top;
  else if (e.bottom > c.bottom) container.scrollTop += e.bottom - c.bottom;
}
