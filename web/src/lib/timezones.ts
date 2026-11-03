/** Suggestions for the Settings time zone field, UTC first. Firefox already lists "UTC" in
 * Intl.supportedValuesOf("timeZone") and Chromium doesn't, so the list is deduplicated: the Settings
 * tab renders it in a keyed each block, and a duplicate key crashes the tab. */
export function timezoneOptions(supported: () => string[] = () => Intl.supportedValuesOf("timeZone")): string[] {
  try {
    return [...new Set(["UTC", ...supported()])];
  } catch {
    return ["UTC"];
  }
}
