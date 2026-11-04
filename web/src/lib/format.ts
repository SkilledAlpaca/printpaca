import { formatMoney, roundCents, toCents, type FieldWarning, type EffectiveConfig } from "./engine/index";

export function money(cents: number, cfg: EffectiveConfig | null): string {
  return formatMoney(cents, cfg?.currency ?? { symbol: "$", decimals: 2, code: "" });
}

/** Like money(), but for plain dollar amounts (electricity, filament), not cents; converts via toCents. */
export function moneyAmount(amount: number, cfg: EffectiveConfig | null): string {
  return money(toCents(amount), cfg);
}

/** Sub-cent unit prices need more precision than toCents' whole-cent rounding: formats against the
 * configured symbol at the requested precision, using roundCents at a caller-chosen scale. */
export function moneyPrecise(amount: number, cfg: EffectiveConfig | null, decimals: number): string {
  const symbol = cfg?.currency.symbol ?? "$";
  const scale = 10 ** decimals;
  const roundedScaled = roundCents(amount * scale);
  // Base the sign on the rounded result so a sub-half-step negative never renders "-$0.000" (roundCents
  // normalizes -0).
  const sign = roundedScaled < 0 ? "-" : "";
  return `${sign}${symbol}${Math.abs(roundedScaled / scale).toFixed(decimals)}`;
}

/** Human-readable labels for the Quote tab's fields, keyed by engine field id, so warnings never show a
 * bare "weightG". */
const FIELD_LABELS: Record<string, string> = {
  weightG: "Model weight",
  pricePerKg: "Filament price",
  wastePct: "Waste",
  timeHours: "Print time (hours)",
  timeMinutes: "Print time (minutes)",
  watts: "Printer power",
  flatRate: "Electricity rate",
  peakRate: "Peak rate",
  offPeakRate: "Off-peak rate",
  offPeakFrac: "Off-peak share",
  printStart: "Print start",
  schedule: "Electricity schedule",
  failureRatePct: "Failure rate",
  netProfit: "Net profit",
  prepMinutes: "Prep time",
  laborRate: "Labor rate",
  machineCost: "Printer cost",
  machineLifeHours: "Printer lifespan",
  monthlyFixed: "Monthly fixed costs",
  monthlyHours: "Monthly print hours",
  quantity: "Quantity",
  marginPct: "Margin",
  markupMultiplier: "Markup multiplier",
  taxRatePct: "Tax rate",
  shippingCharge: "Shipping charged",
  shippingCost: "Shipping cost",
  salePriceOverride: "Sale price override",
  feeSchedule: "Fee schedule",
  perUnitTotal: "Cost per unit",
  hours: "Print time",
  rate: "Rate",
  spoolPrice: "Spool price",
  spoolWeightKg: "Spool weight",
  grams: "Remaining filament",
  diameterMm: "Filament diameter",
  density: "Material density",
  unitPriceCents: "Unit price",
  discountCents: "Discount",
  amountCents: "Amount",
  priceCents: "Price",
  clientName: "Client name",
  description: "Description",
  issueDate: "Issue date",
  dueDate: "Due date",
  paidDate: "Payment date",
  current: "Current password",
  new: "New password",
  password: "Password",
  price: "Price",
  smtp: "Email",
  label: "Label",
  email: "Email",
  username: "Username",
  status: "Status",
  note: "Note",
  notes: "Notes",
  reference: "Reference",
  clientId: "Client",
  method: "Payment method",
  material: "Material",
  item: "Item",
  options: "Options",
  optionName: "Option name",
  stockQty: "Stock quantity",
  stockMode: "Stock mode",
  taxBp: "Sales tax",
  shippingCents: "Shipping",
  billingAddress: "Billing address",
  shippingAddress: "Shipping address",
  phone: "Phone",
  // settingsRules.ts fields use the server's dotted/indexed paths; fieldLabel() reduces them to the trailing
  // key before lookup. Some keys above (marginPct, quantity, ...) are intentionally reused.
  id: "ID",
  name: "Name",
  slug: "Address name",
  categoryId: "Category",
  unitCost: "Unit cost",
  customCss: "Custom CSS",
  timezone: "Time zone",
  defaultShippingCharge: "Default shipping charge",
  decimals: "Decimals",
  numberPrefix: "Number prefix",
  salesTaxPct: "Sales tax",
  dueDays: "Due days",
  paymentMethods: "Payment methods",
  paypalMe: "PayPal.me handle",
  code: "Currency code",
  port: "SMTP port",
  from: "From address",
  to: "To address",
  filamentDiameterMm: "Filament diameter",
  introText: "Intro text",
  paymentInstructions: "Payment instructions",
  termsText: "Terms text",
  // Fee-schedule entries arrive indexed ("feeSchedule.fixed[1].amount") so the operator can find the bad one;
  // the trailing keys are unique to fee schedules.
  amount: "Fee amount",
  pct: "Fee percent",
  base: "Fee base",
};

/** Field ids are bare engine keys ("weightG") or settingsRules.ts's dotted/indexed paths; stripping index
 * brackets and taking the trailing segment reduces both to one lookup key. */
export function fieldLabel(field: string): string {
  const key = field.replace(/\[\d+\]/g, "").split(".").pop() ?? field;
  return FIELD_LABELS[key] ?? field;
}

/** True when fieldLabel() has a readable label for this field path. */
export function hasFieldLabel(field: string): boolean {
  const key = field.replace(/\[\d+\]/g, "").split(".").pop() ?? field;
  return key in FIELD_LABELS;
}

/** Rebuilds user-facing copy from the structured code and field label ("Enter a valid weight.") instead of
 * validate.ts's technical wording. below-min/above-max thresholds come from FieldWarning.bound; hand-built
 * warnings (settingsRules.ts, quote.ts) leave it unset and fall to the label swap. */
export function friendlyMessage(w: FieldWarning): string {
  const label = fieldLabel(w.field);
  const lower = label.charAt(0).toLowerCase() + label.slice(1);
  switch (w.code) {
    case "required":
      // A blank print time is one thing to the user, whichever half the warning names.
      if (w.field === "timeHours" || w.field === "timeMinutes") return "Enter the print time.";
      return `${label} is required.`;
    case "nan":
      return `Enter a valid ${lower}.`;
    case "not-integer":
      return `${label} must be a whole number.`;
    case "below-min":
    case "above-max":
      if (w.bound !== undefined) {
        return `${label} must be ${w.code === "below-min" ? "at least" : "at most"} ${w.bound}.`;
      }
      break;
  }
  if (w.message.startsWith("must ")) return `${label} ${w.message}.`;
  return w.message.startsWith(w.field) ? label + w.message.slice(w.field.length) : `${label}: ${w.message}`;
}

export function warnFor(warnings: FieldWarning[], field: string): string {
  const w = warnings.find((w) => w.field === field);
  return w ? friendlyMessage(w) : "";
}

const DATE_ONLY = /^\d{4}-\d{2}-\d{2}$/;

/** A calendar date such as "Nov 2, 2026" in en-US. Parses YYYY-MM-DD as a local date (not UTC) so it never
 * shifts a day; also accepts RFC 3339 timestamps. Returns the input unchanged when it doesn't parse. */
export function formatDate(isoOrDate: string): string {
  const d = DATE_ONLY.test(isoOrDate) ? new Date(`${isoOrDate}T00:00:00`) : new Date(isoOrDate);
  return Number.isNaN(d.getTime())
    ? isoOrDate
    : d.toLocaleDateString("en-US", { year: "numeric", month: "short", day: "numeric" });
}

/** A timestamp such as "Nov 2, 2026, 2:03 PM" in en-US. Returns the input unchanged when it doesn't parse. */
export function formatDateTime(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString("en-US", { dateStyle: "medium", timeStyle: "short" });
}

/** Display name for a preset key: "side-hustle" becomes "Side hustle". The key itself stays the stored id. */
export function presetDisplayName(key: string): string {
  const spaced = key.replace(/[-_]+/g, " ").trim();
  return spaced.charAt(0).toUpperCase() + spaced.slice(1);
}

const FEE_BRANDS: Record<string, string> = { ebay: "eBay", paypal: "PayPal", etsy: "Etsy", square: "Square", stripe: "Stripe", shopify: "Shopify", amazon: "Amazon", none: "None" };

/** Display name for a fee-schedule key: "etsy-us" becomes "Etsy (US)", "ebay-uk" becomes "eBay (UK)". A trailing
 * two-letter segment reads as a region; known marketplace names get their usual spelling; anything else falls
 * back to presetDisplayName. The key itself stays the stored id. */
export function feeScheduleDisplayName(key: string): string {
  const parts = key.split(/[-_]+/).filter(Boolean);
  if (parts.length === 0) return key;
  let region = "";
  if (parts.length > 1 && /^[a-z]{2}$/i.test(parts[parts.length - 1]!)) region = parts.pop()!.toUpperCase();
  const name = parts.map((p, i) => FEE_BRANDS[p.toLowerCase()] ?? (i === 0 ? p.charAt(0).toUpperCase() + p.slice(1) : p)).join(" ");
  return region ? `${name} (${region})` : name;
}

/** An electricity rate as dollars per kWh: two decimals ("$0.30"), or more only when the rate needs them
 * ("$0.325"), never trailing zeros past the second place. */
export function rateText(rate: number, cfg: EffectiveConfig | null): string {
  const cents = rate * 100;
  const decimals = Math.abs(cents - Math.round(cents)) < 1e-9 ? 2 : Math.abs(rate * 1000 - Math.round(rate * 1000)) < 1e-9 ? 3 : 4;
  return `${moneyPrecise(rate, cfg, decimals)}/kWh`;
}

/** A material shown by name: a stored id such as "petg" becomes the configured name ("PETG"); a value that is
 * already a name, or matches nothing, is returned as it is. */
export function materialDisplayName(value: string, materials: readonly { id: string; name: string }[]): string {
  const lower = value.trim().toLowerCase();
  if (lower === "") return value;
  const byName = materials.find((m) => m.name.toLowerCase() === lower);
  if (byName) return byName.name;
  const byId = materials.find((m) => m.id.toLowerCase() === lower);
  return byId ? byId.name : value;
}
