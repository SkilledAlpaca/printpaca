export type Cents = number;

export interface CurrencyConfig {
  symbol: string;
  decimals: number;
  /** ISO 4217 code ("" when unset); display formatting ignores it. Used for the PayPal.me payment link. */
  code: string;
}

/** Round half away from zero, the single tie rule for money math. The 1e-9 epsilon corrects IEEE754
 * tie-underflow (1.005*100 is stored as 100.49999999999999) and is inert above |v| ~ 4.5e15. Non-finite
 * input rounds to 0. */
export function roundCents(v: number): number {
  if (!Number.isFinite(v)) return 0;
  const sign = v < 0 ? -1 : 1;
  const r = sign * Math.floor(Math.abs(v) + 0.5 + 1e-9);
  return r === 0 ? 0 : r; // never -0
}

export function toCents(amount: number): Cents {
  return roundCents(amount * 100);
}

/** Negative sign precedes the symbol; integer digits are grouped with commas (en-US only). */
export function formatMoney(cents: Cents, currency: CurrencyConfig): string {
  const sign = cents < 0 ? "-" : "";
  const formatted = (Math.abs(cents) / 100).toFixed(currency.decimals);
  const dot = formatted.indexOf(".");
  const whole = dot === -1 ? formatted : formatted.slice(0, dot);
  const frac = dot === -1 ? "" : formatted.slice(dot + 1);
  const groupedWhole = whole.replace(/\B(?=(\d{3})+(?!\d))/g, ",");
  const value = frac === "" ? groupedWhole : `${groupedWhole}.${frac}`;
  return `${sign}${currency.symbol}${value}`;
}
