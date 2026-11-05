import type { PublicConfig } from "./api";

/** The id of the server-rendered `<script type="application/json">` block holding the public config. */
export const SHOP_CONFIG_ELEMENT_ID = "shop-config";

/** The tab title of last resort, when neither shop.pageTitle nor the business name is set. */
export const FALLBACK_SHOP_TITLE = "Shop";

/**
 * Reads the config the server embedded in the HTML, synchronously, so the first paint is the owner's shop and
 * never a flash of built-in defaults. Returns null when the block is missing or isn't a usable config (an older
 * server, a cached page, a dev server): the caller then shows a neutral loading state until the fetch returns.
 */
export function readEmbeddedConfig(doc: Pick<Document, "getElementById"> = document): PublicConfig | null {
  const el = doc.getElementById(SHOP_CONFIG_ELEMENT_ID);
  const text = el?.textContent;
  if (!text) return null;
  try {
    return parsePublicConfig(JSON.parse(text));
  } catch {
    return null;
  }
}

/** Accepts a parsed value only if it has the parts the page cannot render without; anything else is null. */
export function parsePublicConfig(value: unknown): PublicConfig | null {
  if (typeof value !== "object" || value === null) return null;
  const v = value as Partial<PublicConfig>;
  if (typeof v.branding !== "object" || v.branding === null) return null;
  if (typeof v.branding.businessName !== "string") return null;
  if (!Array.isArray(v.materials)) return null;
  if (typeof v.currency !== "object" || v.currency === null) return null;
  return value as PublicConfig;
}

/** The shop title: shop.pageTitle (the server serves it already resolved), else the business name, else "Shop". */
export function shopTitleOf(cfg: PublicConfig): string {
  return cfg.shop?.pageTitle?.trim() || cfg.branding.businessName.trim() || FALLBACK_SHOP_TITLE;
}

/** The tab title (WCAG 2.4.2): the shop title alone on the gallery, "<page> · <shop title>" elsewhere. */
export function composeTitle(shopTitle: string, page: string | null): string {
  return page ? `${page} · ${shopTitle}` : shopTitle;
}
