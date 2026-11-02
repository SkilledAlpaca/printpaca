/** Today's date (YYYY-MM-DD) in the shop's configured IANA time zone (config `timezone`), the same zone the
 * server uses for the dashboard and Mark paid, so overdue flags and default dates agree with it. An empty or
 * unrecognized name falls back to the browser's zone. */
export function todayInTimezone(timeZone?: string): string {
  if (timeZone) {
    try {
      return new Intl.DateTimeFormat("en-CA", { timeZone }).format(new Date());
    } catch {
      // fall through to the browser zone
    }
  }
  return new Date().toLocaleDateString("en-CA");
}

/** "Overdue" is always derived, never stored: status is sent and dueDate < today. Shared so every UI place
 * agrees. `today` is a parameter so callers and tests can pin it. */
export function isInvoiceOverdue(inv: { status: string; dueDate: string }, today = todayInTimezone()): boolean {
  return inv.status === "sent" && inv.dueDate !== "" && inv.dueDate < today;
}

/** The badge/filter variant: the status, except an overdue sent invoice reports "overdue". */
export function invoiceStatusVariant(inv: { status: string; dueDate: string }, today?: string): string {
  return isInvoiceOverdue(inv, today) ? "overdue" : inv.status;
}
