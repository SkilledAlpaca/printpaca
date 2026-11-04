import type { CurrencyConfig } from "./money";
import type { ConfigUser } from "../permissions";
import type { EnergySchedule } from "./schedule";

interface MaterialConfig {
  id: string;
  name: string;
  density: number; // g/cm³
  pricePerKg: number;
  wastePct: number; // 0-100; server always serializes it
  public: boolean; // opts this material into the public quote page
}

export interface PrinterConfig {
  id: string;
  name: string;
  watts: number;
  purchaseCost: number;
  lifespanHours: number;
  filamentDiameterMm: number;
}

// One reusable add-on component (magnet, insert, ...) attached to a quote as an extra per-unit cost line;
// mirrors server/config.go's Hardware. name is its only identity.
export interface HardwareConfig {
  name: string;
  unitCost: number;
  lostOnFailure: boolean;
}

export interface EnergyConfig {
  mode: "flat" | "tou" | "schedule";
  flatRate: number;
  tou: { peakRate: number; offPeakRate: number; defaultOffPeakFrac: number };
  /** Used when mode is "schedule"; server/energy_schedule.go validates it. */
  schedule: EnergySchedule;
}

export interface FeeSchedule {
  source?: string;
  fixed: { amount: number; label: string }[];
  percent: { pct: number; base: "revenue"; label: string }[];
}

interface PricingConfig {
  strategy: "margin" | "markup";
  marginPct: number;
  markupMultiplier: number;
  taxRatePct: number;
  /** What the public page bills the visitor for shipping, and a new admin quote's shipping charge. */
  defaultShippingCharge: number;
  /** The shipping you pay per order (an expense), separate from the charge. */
  defaultShippingCost: number;
  defaultFeeSchedule: string; // "none" or a key of feeSchedules
  laborPerUnit: boolean;
}

export interface PresetConfig {
  failureRatePct: number;
  prepMinutes: number;
  laborRate: number;
  monthlyFixed: number;
  monthlyHours: number;
}

interface InvoicingConfig {
  numberPrefix: string;
  salesTaxPct: number;
  dueDays: number;
  paymentMethods: string[];
  paypalMe: string;
}

// Mirrors server/config.go's PublicQuotes: the owner-fixed printer/preset every visitor estimate uses, plus
// optional intro text.
interface PublicQuotesConfig {
  printer: string;
  preset: string;
  introText: string;
}

// Mirrors server/config.go's Site: what the main port serves at its root and where the shop footer's Staff
// sign-in link points. Applies without a restart.
interface SiteConfig {
  shopOnMainPort: boolean;
  staffLink: boolean;
  adminUrl: string;
}

// Mirrors server/config.go's Shop: the customer-facing storefront settings. Every default keeps v1.1
// behavior (lib/shop.ts's DEFAULT_SHOP_CONFIG). Applies without a restart. Text fields are plain text.
export interface ShopConfig {
  /** The largest quantity one public estimate request or gallery order may carry (1 to 10000). */
  maxOrderQty: number;
  /** Off: the shop hides the custom estimate flow and the server rejects estimate previews and submissions. */
  customEstimates: boolean;
  /** Off: the shop shows only the custom estimate and the server rejects item orders. Not both off. */
  gallery: boolean;
  /** Off (vacation mode): the shop stays visible, pausedMessage replaces the submit buttons, and the server
   * rejects new submissions. */
  acceptingOrders: boolean;
  pausedMessage: string;
  defaultView: "gallery" | "estimate";
  heading: string;
  announcement: string;
  /** Browser tab title, up to 70 characters (D30). Optional until the server always sends it; absent is "". */
  pageTitle?: string;
  /** Set only by the hero image upload endpoint; a settings save keeps the stored file and takes only alt. */
  heroImage: { file: string; alt: string };
  /** "" for the theme accent, else #rrggbb passing lib/accent.ts's check. */
  accentColor: string;
  showStock: boolean;
  showFromPrice: boolean;
  itemSort: "manual" | "name" | "price";
  footerLinks: { label: string; url: string }[];
  showContact: boolean;
  shippingNote: string;
  turnaroundNote: string;
}

// Mirrors server/config.go's Smtp. The password never travels here; it comes only from SMTP_PASSWORD.
interface SmtpConfig {
  host: string;
  port: number;
  username: string;
  from: string;
  to: string;
}

// Mirrors server/config.go's IntegrationsUrls: the editable fallback for
// SPOOLMAN_URL/MOONRAKER_URL/FLUIDD_URL; the env var always wins (see integrationSources). Changes apply only
// after a container restart.
interface IntegrationsUrlsConfig {
  spoolmanUrl: string;
  moonrakerUrl: string;
  fluiddUrl: string;
}

/** Which of env/config/unset resolved an integration URL (server/proxy.go's resolveIntegrationURL). "env"
 * locks the field in the UI. */
type IntegrationSource = "env" | "config" | "unset";

interface IntegrationSourcesConfig {
  spoolman: IntegrationSource;
  moonraker: IntegrationSource;
  fluidd: IntegrationSource;
}

export interface EffectiveConfig {
  currency: CurrencyConfig;
  pricing: PricingConfig;
  feeSchedules: Record<string, FeeSchedule>;
  materials: MaterialConfig[];
  printers: PrinterConfig[];
  hardware: HardwareConfig[];
  energy: EnergyConfig;
  presets: Record<string, PresetConfig>;
  defaults: {
    preset: string;
    printer: string;
    material: string;
    failureRatePct: number;
    quantity: number;
  };
  quote: {
    businessName: string;
    logoUrl: string;
    address: string;
    email: string;
    phone: string;
    taxId: string;
    paymentInstructions: string;
    termsText: string;
    validDays: number;
    /** Prefix of the customer-facing order reference; "" derives it from the business name. */
    referencePrefix: string;
  };
  invoicing: InvoicingConfig;
  publicQuotes: PublicQuotesConfig;
  site: SiteConfig;
  shop: ShopConfig;
  // Operator-authored CSS served verbatim and unsanitized at GET /custom.css on both listeners
  // (server/config.go's Config.CustomCss). The admin app loads it with media="print" only, so it styles
  // printed quotes and invoices, never the on-screen UI.
  customCss: string;
  /** IANA time zone name (server/config.go's Config.Timezone): the dashboard's months, overdue checks and
   * auto-dated payments use it. */
  timezone: string;
  smtp: SmtpConfig;
  integrationsUrls: IntegrationsUrlsConfig;
  /** Which nav tabs are hidden (ui.disabledTabs); decluttering only, never access control. */
  ui: { disabledTabs: string[] };
  integrations: { spoolman: boolean };
  version: string;
  /** Whether ADMIN_PASSWORD is set (server/auth.go): every /api/* route except /api/login and /healthz
   * requires a session. */
  authEnabled: boolean;
  /** Whether MOONRAKER_URL is configured (server/main.go), so /api/moonraker/* is proxied. */
  moonraker: boolean;
  /** FLUIDD_URL when configured, link-only (never proxied); "" when unset. */
  fluiddUrl: string;
  /** Whether DEMO_MODE=1 (server/main.go): a read-only demo. The server rejects admin writes regardless;
   * this only sets expectations in the UI. */
  demoMode: boolean;
  /** Which of env/config/unset resolved each integration URL (server/main.go's deriveIntegrationSources),
   * so Settings can lock env-sourced rows. */
  integrationSources: IntegrationSourcesConfig;
  /** The signed-in identity, resolved per request (server/users.go's meResponse). */
  user: ConfigUser;
}
