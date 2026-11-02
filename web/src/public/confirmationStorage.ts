// sessionStorage-backed persistence for the public page's post-submit confirmations, so a reload or back-tap
// doesn't lose the only record of what was submitted. The custom-quote form and the gallery item-order form
// each use their own `key`. sessionStorage, not localStorage: it should survive a reload of this tab, not
// linger across restarts or leak into a new tab.
const PREFIX = "printpaca:public-confirmation:";

/** Best-effort: sessionStorage can throw (private browsing, disabled, full, absent); every function
 * swallows that. Losing it only means a reload won't restore the confirmation. */
export function saveConfirmation<T>(key: string, value: T): void {
  try {
    sessionStorage.setItem(PREFIX + key, JSON.stringify(value));
  } catch {
    // see file doc comment
  }
}

export function loadConfirmation<T>(key: string): T | null {
  try {
    const raw = sessionStorage.getItem(PREFIX + key);
    return raw === null ? null : (JSON.parse(raw) as T);
  } catch {
    return null;
  }
}

export function clearConfirmation(key: string): void {
  try {
    sessionStorage.removeItem(PREFIX + key);
  } catch {
    // see file doc comment
  }
}
