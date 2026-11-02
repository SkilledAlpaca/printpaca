/** Preloads `cfg.quote.logoUrl` out of band so print never blocks on it. window.print() hands straight to
 * the browser dialog, which blocks until every `<img>` loads, so a slow or unreachable logo URL would
 * stall it. App.svelte preloads on config resolve; the print views render the `<img>` only once `readyUrl`
 * matches the configured URL. A logo that is slow or broken is simply omitted, with no timeout needed.
 * Boxed because an exported `let` can't be reassigned from an importing module. */
export const printLogo: { readyUrl: string | null } = $state({ readyUrl: null });

/** The most recently requested logo URL; not reactive. It guards onload so a preload resolving after the
 * logo changed again can't publish a stale URL. */
let requestedUrl: string | null = null;

/** Starts or clears the preload for `url`; call when the effective logoUrl changes. Safe to repeat. Clears
 * `readyUrl` first for any unconfirmed URL so a previous logo is never left on the printout. */
export function preloadPrintLogo(url: string | undefined | null): void {
  const trimmed = (url ?? "").trim() || null;
  requestedUrl = trimmed;
  if (trimmed === null) {
    printLogo.readyUrl = null;
    return;
  }
  if (printLogo.readyUrl === trimmed) return; // already cached and current

  printLogo.readyUrl = null;
  const img = new Image();
  img.onload = () => {
    if (requestedUrl === trimmed) printLogo.readyUrl = trimmed;
  };
  img.onerror = () => {
    // A logo that can't load is omitted; print never waits on it.
  };
  img.src = trimmed;
}
