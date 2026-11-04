/** Decision 7: the Dashboard is the landing tab once the shop has any quote, request, or invoice; New quote
 * (with its onboarding) stays the landing tab only on a fresh install. */

/** The row counts behind the three tabs. A tab the user can't open reports 0 (its list endpoint refuses). */
export interface ShopActivity {
  quotes: number;
  requests: number;
  invoices: number;
}

/** True when the shop has any quote, request, or invoice. */
export function hasShopActivity(a: ShopActivity): boolean {
  return a.quotes > 0 || a.requests > 0 || a.invoices > 0;
}
