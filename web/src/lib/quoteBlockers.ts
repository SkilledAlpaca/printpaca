import type { FieldWarning, OrderBreakdown } from "./engine/index";
import { fieldLabel } from "./format";

/** One field that keeps an order from being priced: what to call it, and where to find its input. */
export interface QuoteBlocker {
  /** Stable key for {#each}: "<partIndex|order>:<label>". */
  key: string;
  /** User-facing name: "Print time", with " (Part 2)" appended when the order has several parts. */
  label: string;
  /** The engine field id the warning named (the first one, for a merged pair such as print time). */
  field: string;
  /** Index into the order's parts, or null for an order-level field (margin, tax, shipping). It indexes the
   * (possibly stale) answer, not the live editor: resolve the part through `partId`. */
  partIndex: number | null;
  /** The id of the editor part the warning belongs to (null for an order-level field, or when unknown). Stable
   * across inserts, removals and edits made while the price answer was still in flight. */
  partId: string | null;
  /** True when the field is blank ("add"), false when it holds a value the server rejected ("fix"). */
  missing: boolean;
  /** The visible label text of the input, for locating it in the page. */
  inputLabel: string;
}

/** Pricing-block fields that have an input on the Price and profit card, with that input's label text. */
const ORDER_FIELD_LABELS: Record<string, string> = {
  marginPct: "Margin",
  markupMultiplier: "Markup",
  taxRatePct: "Tax (included in price)",
  shippingCharge: "Shipping charged",
  shippingCost: "Shipping cost",
};

/** The print time is two inputs but one thing to the user, so the pair merges into one blocker. */
const TIME_FIELDS = new Set(["timeHours", "timeMinutes"]);

function isMissing(w: FieldWarning): boolean {
  return w.code === "required" || w.code === "nan";
}

/**
 * The fields that stop an order from being priced, read from each part's cost warnings plus the pricing
 * block's own input warnings. Empty when nothing specific is known (the caller then falls back to a generic
 * message). Warnings below "error" severity never block, so they are skipped.
 */
export function quoteBlockers(order: OrderBreakdown, partLabels: readonly string[], partIds: readonly string[] = []): QuoteBlocker[] {
  const multi = order.parts.length > 1;
  const out: QuoteBlocker[] = [];
  const seen = new Set<string>();

  order.parts.forEach((p, i) => {
    for (const w of p.breakdown.warnings) {
      if (w.severity !== "error") continue;
      const isTime = TIME_FIELDS.has(w.field);
      const base = isTime ? "Print time" : fieldLabel(w.field);
      const key = `${i}:${isTime ? "time" : w.field}`;
      if (seen.has(key)) continue;
      seen.add(key);
      out.push({
        key,
        label: multi ? `${base} (${partLabels[i] ?? `Part ${i + 1}`})` : base,
        field: w.field,
        partIndex: i,
        partId: partIds[i] ?? null,
        missing: isMissing(w),
        inputLabel: isTime ? "Print time" : base,
      });
    }
  });

  for (const w of order.pricing?.warnings ?? []) {
    const inputLabel = ORDER_FIELD_LABELS[w.field];
    if (w.severity !== "error" || inputLabel === undefined) continue;
    const key = `order:${w.field}`;
    if (seen.has(key)) continue;
    seen.add(key);
    out.push({ key, label: inputLabel, field: w.field, partIndex: null, partId: null, missing: isMissing(w), inputLabel });
  }
  return out;
}

/**
 * The cost bar's sentence naming every blank field: "Price needs a print time and a material". Print time
 * gets its article and leads the list (a model file never supplies it); every other label is lower-cased. A
 * multi-part label such as "Print time (Part 2)" keeps its part name. Empty when nothing is missing.
 */
export function needSentence(missing: readonly Pick<QuoteBlocker, "label" | "field">[]): string {
  if (missing.length === 0) return "";
  const isTime = (b: Pick<QuoteBlocker, "field">) => b.field === "timeHours" || b.field === "timeMinutes";
  const ordered = [...missing.filter(isTime), ...missing.filter((b) => !isTime(b))];
  const names = ordered.map((b) => (isTime(b) ? b.label.replace(/^Print time/, "a print time") : b.label.toLowerCase()));
  const list =
    names.length === 1
      ? names[0]!
      : names.length === 2
        ? `${names[0]} and ${names[1]}`
        : `${names.slice(0, -1).join(", ")}, and ${names[names.length - 1]}`;
  return `Price needs ${list}`;
}
