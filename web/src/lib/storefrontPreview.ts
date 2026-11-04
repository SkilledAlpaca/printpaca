// Pure helpers for Settings > Storefront: what the Closed page preview shows, and how the accent preview picks
// its scheme. The preview renders public/ClosedPage.svelte with the unsaved form values.
import type { ServerConfig } from "./settings";

/** The branding the Closed page shows. The server serves contact details only while shop.showContact is on
 * (server/public.go), so the preview omits them when it is off, as customers would see. */
export function closedPreviewBranding(form: Pick<ServerConfig, "quote" | "shop">): { email: string; phone: string; address: string } {
  if (!form.shop.showContact) return { email: "", phone: "", address: "" };
  return { email: form.quote.email.trim(), phone: form.quote.phone.trim(), address: form.quote.address.trim() };
}
