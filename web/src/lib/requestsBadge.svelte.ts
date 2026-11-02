import { loadRequests } from "./requestsApi";

/** Boxed because an exported `let` can't be reassigned from an importing module. App.svelte reads this for
 * the nav pill; RequestsTab calls refreshUnread() after any mutation. */
export const unreadRequests: { count: number } = $state({ count: 0 });

/** Refetches the list and recounts status "new". A failure (loadRequests() null) is "nothing to report":
 * the pill just doesn't update. No polling; callers decide when to recount. */
export async function refreshUnread(): Promise<void> {
  const requests = await loadRequests();
  if (requests === null) return;
  unreadRequests.count = requests.filter((r) => r.status === "new").length;
}
