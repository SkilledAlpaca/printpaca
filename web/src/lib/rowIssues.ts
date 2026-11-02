/** Shared by MaterialsSettings/PrintersSettings/HardwareSettings.svelte, which render a wholesale array as
 * a table plus a flat list of per-row `.warn-text` lines (one per problem). They differ only in field-name
 * prefix and suffixes scanned. */

import type { FieldWarning } from "./engine/index";
import { warnFor } from "./format";

export interface RowIssue {
  row: number;
  field: string;
  message: string;
}

/** One entry per non-empty warning across `rowCount` rows named `${prefix}[i].${suffix}`, in
 * row-then-suffix order. */
export function rowProblems(
  prefix: string,
  rowCount: number,
  suffixes: string[],
  warnings: FieldWarning[],
): RowIssue[] {
  const out: RowIssue[] = [];
  for (let i = 0; i < rowCount; i++) {
    for (const suffix of suffixes) {
      const field = `${prefix}[${i}].${suffix}`;
      const msg = warnFor(warnings, field);
      if (msg !== "") out.push({ row: i + 1, field, message: msg });
    }
  }
  return out;
}

/** The id an offending cell's `aria-describedby` points at, and the id of the matching `.warn-text` line. */
export function warnId(field: string): string {
  return `warn-${field.replace(/[^a-zA-Z0-9]+/g, "-")}`;
}
