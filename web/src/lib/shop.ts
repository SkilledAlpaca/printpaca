import type { ShopConfig } from "./engine/types";

/** server/defaults.yaml's shop block: v1.1 behavior. Test fixtures and the settings form's "reset" use it. */
export const DEFAULT_SHOP_CONFIG: ShopConfig = {
  maxOrderQty: 100,
  customEstimates: true,
  gallery: true,
  acceptingOrders: true,
  pausedMessage: "",
  defaultView: "gallery",
  heading: "",
  announcement: "",
  heroImage: { file: "", alt: "" },
  accentColor: "",
  showStock: true,
  showFromPrice: true,
  itemSort: "manual",
  footerLinks: [],
  showContact: true,
  shippingNote: "",
  turnaroundNote: "",
};
