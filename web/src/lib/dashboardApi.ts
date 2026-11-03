import { DEFAULT_FETCH_TIMEOUT_MS } from "./fetchTimeouts";
import type { InvoiceSummary } from "./invoicesApi";

/** Bucket of Dashboard.revenueByMonth (server/dashboard.go): always 12 entries, oldest first, zero-filled */
interface RevenueMonth {
  month: string; // "YYYY-MM"
  cents: number;
}

/** The GET /api/dashboard shape (server/dashboard.go's dashboardResponse); computed server-side, only
 * rendered here. */
export interface Dashboard {
  revenueMonthCents: number;
  revenueYearCents: number;
  outstandingCents: number;
  overdueCount: number;
  recentInvoices: InvoiceSummary[];
  revenueByMonth: RevenueMonth[];
  quotesSavedThisMonth: number;
  /** Share (0-100) of this month's quotes with invoicedAs set; null when none were saved. */
  quotesInvoicedSharePct: number | null;
  /** Mean achieved margin percent over every invoiced quote, any month; null when there are none. */
  quotesAvgMarginPct: number | null;
  /** The "Needs attention" rows (server/dashboard.go): up to 10 per kind, requests only for callers who may
   * open Requests and invoices only for those who may open Invoices. */
  needsAttention: AttentionItem[];
  attentionCounts: AttentionCounts;
}

export type AttentionKind = "new-request" | "print-time-request" | "overdue-invoice" | "draft-invoice";

/** One "Needs attention" row. name is the requester or client; number is the invoice number (invoices only);
 * amountCents is the request price or the invoice balance (overdue) or total (draft); date is the due date
 * (overdue), the creation date (draft) or the submission time (requests). */
export interface AttentionItem {
  kind: AttentionKind;
  id: string;
  name: string;
  number: string;
  amountCents: number;
  date: string;
}

/** The full count behind each kind, for "and N more". */
export interface AttentionCounts {
  newRequests: number;
  printTimeRequests: number;
  overdueInvoices: number;
  draftInvoices: number;
}

/** A network failure returns null so the page can show an offline note. */
export async function fetchDashboard(): Promise<Dashboard | null> {
  try {
    const res = await fetch("api/dashboard", { signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS) });
    if (!res.ok) return null;
    return (await res.json()) as Dashboard;
  } catch {
    return null;
  }
}

/** One entry of TopItem.options: units sold of a single option. */
export interface TopItemOption {
  name: string;
  units: number;
}

/** One ranked gallery item of GET /api/dashboard/top-items (server/dashboard.go's topItem). */
export interface TopItem {
  itemId: string;
  name: string;
  /** True when the item no longer exists; name is then the snapshot from its latest sale in the window. */
  removed: boolean;
  units: number;
  /** units split by source: finalized gallery orders vs. sent or paid invoice lines linked to the item. */
  galleryUnits: number;
  inPersonUnits: number;
  orders: number;
  revenueCents: number;
  /** Units per non-empty option name, most units first. */
  options: TopItemOption[];
}

/** The GET /api/dashboard/top-items shape (server/dashboard.go's topItemsResponse). The totals cover every
 * sale in the window, not just the (at most 10) items listed. */
export interface TopItems {
  days: number;
  totalUnits: number;
  galleryUnits: number;
  inPersonUnits: number;
  totalRevenueCents: number;
  items: TopItem[];
}

/** The windows the server accepts; anything else is a 400. */
export const TOP_ITEMS_WINDOWS = [30, 90, 365] as const;
export type TopItemsWindow = (typeof TOP_ITEMS_WINDOWS)[number];

/** Like fetchDashboard, a failure of any kind returns null. */
export async function fetchTopItems(days: TopItemsWindow): Promise<TopItems | null> {
  try {
    const res = await fetch(`api/dashboard/top-items?days=${days}`, {
      signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS),
    });
    if (!res.ok) return null;
    return (await res.json()) as TopItems;
  } catch {
    return null;
  }
}

/** The muted line under an item's name: "Red 4 · Blue 2". Empty when the item sold without options. */
export function optionsLine(options: TopItemOption[]): string {
  return options.map((o) => `${o.name} ${o.units}`).join(" · ");
}

/** The muted shop-order/in-person split: "9 shop orders · 5 in person". */
export function sourceSplit(galleryUnits: number, inPersonUnits: number): string {
  return `${galleryUnits} shop orders · ${inPersonUnits} in person`;
}
