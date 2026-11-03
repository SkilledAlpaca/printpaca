import type { CurrencyConfig } from "./money";
import type { ConfigUser } from "../permissions";

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
  mode: "flat" | "tou";
  flatRate: number;
  tou: { peakRate: number; offPeakRate: number; defaultOffPeakFrac: number };
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
  };
  invoicing: InvoicingConfig;
  publicQuotes: PublicQuotesConfig;
  site: SiteConfig;
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
