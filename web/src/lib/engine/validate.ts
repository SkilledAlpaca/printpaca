type Severity = "warning" | "error";
type FieldCode = "required" | "nan" | "below-min" | "above-max" | "not-integer" | "invalid-enum";

export interface FieldWarning {
  field: string;
  code: FieldCode;
  message: string;
  severity: Severity;
  /** The threshold behind a below-min/above-max warning, so a UI can build its own copy. Absent for other
   * codes and for settingsRules.ts's own warnings. */
  bound?: number;
}

export interface FieldRule {
  min?: number;
  max?: number;
  required?: boolean;
  integer?: boolean;
  severity?: Severity;
}

export function validateField(
  field: string,
  value: number | null | undefined,
  rule: FieldRule,
): FieldWarning[] {
  if (value === null || value === undefined) {
    if (rule.required) {
      return [{ field, code: "required", message: `${field} is required`, severity: "error" }];
    }
    return [];
  }
  if (!Number.isFinite(value)) {
    return [{ field, code: "nan", message: `${field} is not a finite number`, severity: "error" }];
  }
  const sev = rule.severity ?? "error";
  const warnings: FieldWarning[] = [];
  if (rule.min !== undefined && value < rule.min) {
    warnings.push({ field, code: "below-min", message: `${field} must be ≥ ${rule.min}`, severity: sev, bound: rule.min });
  }
  if (rule.max !== undefined && value > rule.max) {
    warnings.push({ field, code: "above-max", message: `${field} must be ≤ ${rule.max}`, severity: sev, bound: rule.max });
  }
  if (rule.integer && !Number.isInteger(value)) {
    warnings.push({ field, code: "not-integer", message: `${field} must be a whole number`, severity: sev });
  }
  return warnings;
}

export function hasErrors(warnings: FieldWarning[]): boolean {
  return warnings.some((w) => w.severity === "error");
}
