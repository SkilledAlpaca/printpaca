import { validateField, type FieldRule, type FieldWarning } from "./engine/index";
import type { ServerConfig } from "./settings";
import { utf8ByteLength } from "./publicFormLimits";
import { accentProblem } from "./accent";

// Byte caps mirroring server/config.go's validate() exactly. Go's len() counts bytes, so they are measured
// with utf8ByteLength (TextEncoder), not .length (see publicFormLimits.ts). Keep in sync with
// publicIntroTextMaxBytes, quotePaymentInstructionsMaxBytes, quoteTermsTextMaxBytes and
// paymentMethodMaxBytes.
export const PUBLIC_INTRO_TEXT_MAX_BYTES = 500;
export const QUOTE_PAYMENT_INSTRUCTIONS_MAX_BYTES = 2000;
export const QUOTE_TERMS_TEXT_MAX_BYTES = 2000;
const PAYMENT_METHOD_MAX_BYTES = 50;
// Mirrors server/config.go's customCssMaxBytes (32 KiB) for the top-level customCss field.
export const CUSTOM_CSS_MAX_BYTES = 32 * 1024;

/** Svelte's bind:value on <input type="number"> yields `null` when emptied though the fields are typed
 * `number`; this widens the local type honestly instead of an `as number` cast that hides the empty case. */
function num(value: number): number | null {
  return value;
}

/** required:true on every rule-carrying numeric field, so an emptied input (null) becomes the engine's
 * "required" FieldWarning rather than silently coercing to 0. */
function field(f: string, value: number, rule: Omit<FieldRule, "required">): FieldWarning[] {
  return validateField(f, num(value), { ...rule, required: true });
}

/** Strictly-greater-than-0 check where the engine's inclusive `min: 0` can't express the bound (density,
 * filament diameter), with the same required/nan handling as validateField. */
function positive(f: string, value: number): FieldWarning[] {
  const v = num(value);
  if (v === null || v === undefined) {
    return [{ field: f, code: "required", message: `${f} is required`, severity: "error" }];
  }
  if (!Number.isFinite(v)) {
    return [{ field: f, code: "nan", message: `${f} is not a finite number`, severity: "error" }];
  }
  if (v <= 0) {
    return [{ field: f, code: "below-min", message: "must be greater than 0", severity: "error" }];
  }
  return [];
}

/** The server loads the name with time.LoadLocation, which is case-sensitive and rejects "Local".
 * Intl.DateTimeFormat accepts the same IANA names but also case variants ("america/new_york"), so a name that
 * differs from its canonical form only by case is rejected here. Aliases the browser doesn't know pass this
 * check and are validated by the server's response on Save. */
function timezoneWarnings(name: string): FieldWarning[] {
  const msg = "must be an IANA time zone name such as UTC or America/New_York (case-sensitive)";
  const bad: FieldWarning[] = [{ field: "timezone", code: "invalid-enum", message: msg, severity: "error" }];
  const trimmed = name.trim();
  if (trimmed === "") return []; // the server treats empty as UTC
  if (trimmed === "Local") return bad;
  try {
    const resolved = new Intl.DateTimeFormat("en-US", { timeZone: trimmed }).resolvedOptions().timeZone;
    if (resolved !== trimmed && resolved.toLowerCase() === trimmed.toLowerCase()) return bad;
    return [];
  } catch {
    return bad;
  }
}

/** Shared by materials[i].id / printers[i].id and hardware[i].name: required plus uniqueness across the
 * array. A duplicate flags every sharing row, matching the per-section danger border. Empty values report
 * "required" only, so "duplicate" never means "duplicate empty string". `fieldFor` builds each row's field
 * path. */
function uniqueRequiredWarnings(values: string[], fieldFor: (i: number) => string): FieldWarning[] {
  const warnings: FieldWarning[] = [];
  const positions = new Map<string, number[]>();
  values.forEach((value, i) => {
    if (value === "") return;
    const list = positions.get(value);
    if (list) list.push(i);
    else positions.set(value, [i]);
  });
  values.forEach((value, i) => {
    const f = fieldFor(i);
    if (value === "") {
      warnings.push({ field: f, code: "required", message: `${f} is required`, severity: "error" });
    } else if ((positions.get(value)?.length ?? 0) > 1) {
      warnings.push({ field: f, code: "above-max", message: `must be unique ("${value}" is used twice)`, severity: "error" });
    }
  });
  return warnings;
}

/** materials[i].id / printers[i].id; see uniqueRequiredWarnings. */
function idWarnings(prefix: "materials" | "printers", ids: string[]): FieldWarning[] {
  return uniqueRequiredWarnings(ids, (i) => `${prefix}[${i}].id`);
}

/** hardware[i].name: hardware has no id, so name is what server/config.go's validate() dedupes on. */
function hardwareNameWarnings(names: string[]): FieldWarning[] {
  return uniqueRequiredWarnings(names, (i) => `hardware[${i}].name`);
}

/** invoicing.numberPrefix: 1-10 ASCII letters/digits, mirroring the server's isAlnumPrefix (no engine
 * FieldRule covers a charset check). */
function numberPrefixWarnings(prefix: string): FieldWarning[] {
  const f = "invoicing.numberPrefix";
  if (prefix.length === 0) {
    return [{ field: f, code: "required", message: `${f} is required`, severity: "error" }];
  }
  if (!/^[A-Za-z0-9]{1,10}$/.test(prefix)) {
    return [{ field: f, code: "above-max", message: "must be 1-10 letters or digits", severity: "error" }];
  }
  return [];
}

/** invoicing.paypalMe: empty, or 1-20 ASCII letters/digits (the bare handle, not the URL). Mirrors
 * server/config.go's paypalHandleRe. */
const PAYPAL_HANDLE_RE = /^[A-Za-z0-9]{1,20}$/;
function paypalMeWarnings(handle: string): FieldWarning[] {
  if (handle === "" || PAYPAL_HANDLE_RE.test(handle)) return [];
  return [{
    field: "invoicing.paypalMe",
    code: "above-max",
    message: "must be 1-20 letters or digits (the handle, not the URL)",
    severity: "error",
  }];
}

/** currency.code: empty, or 3 uppercase ASCII letters (ISO 4217). Mirrors server/config.go currencyCodeRe. */
const CURRENCY_CODE_RE = /^[A-Z]{3}$/;
function currencyCodeWarnings(code: string): FieldWarning[] {
  if (code === "" || CURRENCY_CODE_RE.test(code)) return [];
  return [{
    field: "currency.code",
    code: "above-max",
    message: "must be 3 uppercase letters, such as USD",
    severity: "error",
  }];
}

/** invoicing.paymentMethods: array length 1-20 on the array field, each trimmed entry non-empty and <=
 * PAYMENT_METHOD_MAX_BYTES bytes on its own indexed field. Mirrors validatePaymentMethods, trimming first
 * and measuring bytes. */
function paymentMethodsWarnings(methods: string[]): FieldWarning[] {
  const warnings: FieldWarning[] = [];
  const f = "invoicing.paymentMethods";
  const n = methods.length;
  if (n < 1 || n > 20) {
    warnings.push({
      field: f,
      code: n < 1 ? "below-min" : "above-max",
      message: `must have 1-20 entries (found ${n})`,
      severity: "error",
    });
  }
  methods.forEach((m, i) => {
    const entryField = `${f}[${i}]`;
    const trimmed = m.trim();
    if (trimmed === "") {
      warnings.push({ field: entryField, code: "required", message: `${entryField} is required`, severity: "error" });
    } else if (utf8ByteLength(trimmed) > PAYMENT_METHOD_MAX_BYTES) {
      warnings.push({
        field: entryField,
        code: "above-max",
        message: `must be ${PAYMENT_METHOD_MAX_BYTES} bytes or fewer`,
        severity: "error",
      });
    }
  });
  return warnings;
}

/** Byte-length cap for free-text settings written into config.yaml and rendered server-side; mirrors
 * server/config.go's len() check via utf8ByteLength. */
function maxBytesWarning(f: string, value: string, maxBytes: number): FieldWarning[] {
  if (utf8ByteLength(value) > maxBytes) {
    return [{ field: f, code: "above-max", message: `must be ${maxBytes} bytes or fewer`, severity: "error" }];
  }
  return [];
}

// Mirrors server/config.go's shop* length constants (characters, not bytes: validateShop counts runes).
const SHOP_HEADING_MAX = 120;
const SHOP_LONG_TEXT_MAX = 300; // announcement, pausedMessage, shippingNote, turnaroundNote
const SHOP_HERO_ALT_MAX = 200;
const SHOP_PAGE_TITLE_MAX = 70; // D30
const SHOP_FOOTER_LINKS_MAX = 6;
const SHOP_FOOTER_LABEL_MAX = 40;
const SHOP_FOOTER_URL_MAX = 500;
// eslint-disable-next-line no-control-regex
const CONTROL_CHARS = /[\u0000-\u001f\u007f-\u009f]/;

function plainTextWarning(f: string, value: string, maxChars: number): FieldWarning[] {
  if ([...value].length > maxChars) {
    return [{ field: f, code: "above-max", message: `must be ${maxChars} characters or fewer`, severity: "error", bound: maxChars }];
  }
  if (CONTROL_CHARS.test(value)) {
    return [{ field: f, code: "invalid-enum", message: "must be plain text on one line", severity: "error" }];
  }
  return [];
}

function httpsUrlWarning(f: string, raw: string): FieldWarning[] {
  if (raw === "") return [{ field: f, code: "required", message: `${f} is required`, severity: "error" }];
  if ([...raw].length > SHOP_FOOTER_URL_MAX) {
    return [{ field: f, code: "above-max", message: `must be ${SHOP_FOOTER_URL_MAX} characters or fewer`, severity: "error", bound: SHOP_FOOTER_URL_MAX }];
  }
  if (/\s/.test(raw) || CONTROL_CHARS.test(raw)) {
    return [{ field: f, code: "invalid-enum", message: "must not contain spaces", severity: "error" }];
  }
  let u: URL | null = null;
  try {
    u = new URL(raw);
  } catch {
    u = null;
  }
  if (!u || u.protocol !== "https:" || u.hostname === "" || u.username !== "" || u.password !== "") {
    return [{ field: f, code: "invalid-enum", message: "must be an https:// address", severity: "error" }];
  }
  return [];
}

/** Mirrors server/config.go's validateShop. The accent message is lib/accent.ts's, the same text the server
 * returns (the shared accent-vectors fixture pins both). */
export function shopWarnings(shop: ServerConfig["shop"]): FieldWarning[] {
  const warnings: FieldWarning[] = [
    ...field("shop.maxOrderQty", shop.maxOrderQty, { min: 1, max: 10000, integer: true }),
    ...plainTextWarning("shop.heading", shop.heading, SHOP_HEADING_MAX),
    ...plainTextWarning("shop.pageTitle", shop.pageTitle ?? "", SHOP_PAGE_TITLE_MAX),
    ...plainTextWarning("shop.announcement", shop.announcement, SHOP_LONG_TEXT_MAX),
    ...plainTextWarning("shop.pausedMessage", shop.pausedMessage, SHOP_LONG_TEXT_MAX),
    ...plainTextWarning("shop.shippingNote", shop.shippingNote, SHOP_LONG_TEXT_MAX),
    ...plainTextWarning("shop.turnaroundNote", shop.turnaroundNote, SHOP_LONG_TEXT_MAX),
    ...plainTextWarning("shop.heroImage.alt", shop.heroImage.alt, SHOP_HERO_ALT_MAX),
  ];
  if (!shop.gallery && !shop.customEstimates) {
    warnings.push({
      field: "shop.gallery",
      code: "invalid-enum",
      message: "the item gallery and custom estimates can't both be off; to stop taking orders, turn off accepting orders instead",
      severity: "error",
    });
  }
  if (shop.heroImage.file !== "" && shop.heroImage.alt.trim() === "") {
    warnings.push({
      field: "shop.heroImage.alt",
      code: "required",
      message: "required when a hero image is set; describe the image for people who can't see it",
      severity: "error",
    });
  }
  const accent = accentProblem(shop.accentColor.trim());
  if (accent !== "") {
    warnings.push({ field: "shop.accentColor", code: "invalid-enum", message: accent.replace(/^Accent color: /, ""), severity: "error" });
  }
  if (shop.footerLinks.length > SHOP_FOOTER_LINKS_MAX) {
    warnings.push({ field: "shop.footerLinks", code: "above-max", message: `at most ${SHOP_FOOTER_LINKS_MAX} links`, severity: "error", bound: SHOP_FOOTER_LINKS_MAX });
  }
  shop.footerLinks.slice(0, SHOP_FOOTER_LINKS_MAX).forEach((l, i) => {
    const f = `shop.footerLinks[${i}]`;
    if (l.label.trim() === "") {
      warnings.push({ field: `${f}.label`, code: "required", message: `${f}.label is required`, severity: "error" });
    } else {
      warnings.push(...plainTextWarning(`${f}.label`, l.label.trim(), SHOP_FOOTER_LABEL_MAX));
    }
    warnings.push(...httpsUrlWarning(`${f}.url`, l.url.trim()));
  });
  return warnings;
}

/** smtp.port/from/to are validated only once smtp.host is non-empty, matching server/config.go (the host
 * check is un-trimmed there too). */
function smtpWarnings(smtp: ServerConfig["smtp"]): FieldWarning[] {
  if (smtp.host === "") return [];
  const warnings: FieldWarning[] = [
    ...field("smtp.port", smtp.port, { min: 1, max: 65535, integer: true }),
  ];
  if (!smtp.from.includes("@")) {
    warnings.push({ field: "smtp.from", code: "below-min", message: "must contain @", severity: "error" });
  }
  if (!smtp.to.includes("@")) {
    warnings.push({ field: "smtp.to", code: "below-min", message: "must contain @", severity: "error" });
  }
  return warnings;
}

/** Every mirrored client-side rule for the settings form, from the current (possibly mid-edit) state.
 * Mirrors only what server/config.go's validate() enforces; the server's 422 stays authoritative. `field`
 * strings use the server's dotted/indexed paths so warnFor-style lookup works against a server error's
 * leading token too. */
export function settingsWarnings(form: ServerConfig): FieldWarning[] {
  const warnings: FieldWarning[] = [
    // currency.decimals: server/config.go rejects anything outside 0-4.
    ...field("currency.decimals", form.currency.decimals, { min: 0, max: 4, integer: true }),
    // pricing.marginPct: 99.9 is the single ceiling shared with server/config.go's validate() and
    // server/pricing.go's maxMarginPct. FieldRule.max is inclusive, so no bespoke check is needed, and the
    // message ("must be <= 99.9") names the number.
    ...field("pricing.marginPct", form.pricing.marginPct, { min: 0, max: 99.9 }),
    // pricing.markupMultiplier: 1000 is the single ceiling shared with server/config.go's validate() and
    // server/pricing.go's maxMarkupMultiplier. Without a maximum, 1e6 priced a $14.51 part at $14.5 M and
    // 1e15 wrapped revenue negative on a "valid" quote.
    ...field("pricing.markupMultiplier", form.pricing.markupMultiplier, { min: 0, max: 10000 }),
    // pricing.taxRatePct: [0,100], mirroring server/config.go's validate() and server/pricing.go's
    // maxPercentAmount. The tax term is price-inclusive, so a negative rate would silently shave every
    // quote's price.
    ...field("pricing.taxRatePct", form.pricing.taxRatePct, { min: 0, max: 100 }),
    // failureRatePct: 90 is the single ceiling shared with server/config.go's validate() and
    // server/pricing.go's maxFailureRatePct (the buffer is base x p / (1 - p)).
    ...field("defaults.failureRatePct", form.defaults.failureRatePct, { min: 0, max: 90 }),
    ...Object.entries(form.presets).flatMap(([key, preset]) =>
      field(`presets.${key}.failureRatePct`, preset.failureRatePct, { min: 0, max: 90 }),
    ),
    ...field("defaults.quantity", form.defaults.quantity, { min: 1, integer: true }),
    ...numberPrefixWarnings(form.invoicing.numberPrefix),
    // invoicing.salesTaxPct: server/config.go rejects anything outside 0-100 (maxSalesTaxPct).
    ...field("pricing.defaultShippingCharge", form.pricing.defaultShippingCharge, { min: 0, max: 1e9 }),
    ...timezoneWarnings(form.timezone),
    ...field("invoicing.salesTaxPct", form.invoicing.salesTaxPct, { min: 0, max: 100 }),
    // invoicing.dueDays: server/config.go rejects anything outside 0-10000 (maxDueDays).
    ...field("invoicing.dueDays", form.invoicing.dueDays, { min: 0, max: 10000, integer: true }),
    ...paymentMethodsWarnings(form.invoicing.paymentMethods),
    ...paypalMeWarnings(form.invoicing.paypalMe ?? ""),
    ...currencyCodeWarnings(form.currency.code),
    ...maxBytesWarning("publicQuotes.introText", form.publicQuotes.introText, PUBLIC_INTRO_TEXT_MAX_BYTES),
    ...maxBytesWarning(
      "quote.paymentInstructions",
      form.quote.paymentInstructions,
      QUOTE_PAYMENT_INSTRUCTIONS_MAX_BYTES,
    ),
    ...maxBytesWarning("quote.termsText", form.quote.termsText, QUOTE_TERMS_TEXT_MAX_BYTES),
    ...maxBytesWarning("customCss", form.customCss, CUSTOM_CSS_MAX_BYTES),
    ...idWarnings("materials", form.materials.map((m) => m.id)),
    ...idWarnings("printers", form.printers.map((p) => p.id)),
    ...hardwareNameWarnings(form.hardware.map((h) => h.name)),
    ...smtpWarnings(form.smtp),
    ...shopWarnings(form.shop),
  ];

  form.materials.forEach((m, i) => {
    warnings.push(...positive(`materials[${i}].density`, m.density));
    warnings.push(...field(`materials[${i}].wastePct`, m.wastePct, { min: 0, max: 100 }));
  });
  form.printers.forEach((p, i) => {
    warnings.push(...positive(`printers[${i}].filamentDiameterMm`, p.filamentDiameterMm));
  });
  form.hardware.forEach((h, i) => {
    warnings.push(...field(`hardware[${i}].unitCost`, h.unitCost, { min: 0 }));
  });

  return warnings;
}
