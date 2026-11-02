import type { CurrencyConfig } from "./engine/index";

/** Subset of an invoice the pay link depends on. */
export interface PayLinkInvoice {
  status: string;
  balanceCents: number;
}

/** Builds the PayPal.me link, or "" when no pay block should show (no handle, canceled invoice, nothing
 * left to pay). With a currency code the open balance is appended as /<amount><CODE> (currency.decimals
 * digits, dot separator, no grouping, half-up rounding); without one the document tells the payer what to
 * send. Mirrors the server's link builder; both run testdata/paypal-link.json. */
export function paypalPayLink(
  handle: string | undefined,
  currency: CurrencyConfig | undefined,
  invoice: PayLinkInvoice,
): string {
  if (!handle || invoice.status === "canceled" || invoice.balanceCents <= 0) return "";
  const link = `https://paypal.me/${handle}`;
  const code = currency?.code ?? "";
  if (code === "") return link;
  const decimals = Math.max(0, currency?.decimals ?? 2);
  const scale = 10 ** decimals;
  const amount = Math.floor((invoice.balanceCents / 100) * scale + 0.5) / scale;
  return `${link}/${amount.toFixed(decimals)}${code}`;
}
