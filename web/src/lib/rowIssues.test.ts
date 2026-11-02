import { describe, expect, it } from "vitest";
import type { FieldWarning } from "./engine/index";
import { warnFor } from "./format";
import { rowProblems, warnId } from "./rowIssues";

// "below-min" without a `bound` gives a deterministic "must "-prefixed message to assert against, without
// coupling to format.ts's FIELD_LABELS.
function warning(field: string, message: string): FieldWarning {
  return { field, code: "below-min", message, severity: "warning" };
}

describe("rowProblems", () => {
  it("returns nothing when there are no matching warnings", () => {
    expect(rowProblems("materials", 2, ["id", "density"], [])).toEqual([]);
  });

  it("emits one entry per non-empty warning, in row-then-suffix order", () => {
    const warnings = [
      warning("materials[1].density", "must be greater than 0"),
      warning("materials[0].id", "must be unique"),
    ];
    expect(rowProblems("materials", 2, ["id", "density", "wastePct"], warnings)).toEqual([
      { row: 1, field: "materials[0].id", message: warnFor(warnings, "materials[0].id") },
      { row: 2, field: "materials[1].density", message: warnFor(warnings, "materials[1].density") },
    ]);
  });

  it("can produce two entries for the same row when it has multiple problems", () => {
    const warnings = [warning("hardware[0].name", "must be set"), warning("hardware[0].unitCost", "must be >= 0")];
    expect(rowProblems("hardware", 1, ["name", "unitCost"], warnings)).toEqual([
      { row: 1, field: "hardware[0].name", message: warnFor(warnings, "hardware[0].name") },
      { row: 1, field: "hardware[0].unitCost", message: warnFor(warnings, "hardware[0].unitCost") },
    ]);
  });

  it("only scans the given prefix and suffixes, ignoring warnings for other arrays/fields", () => {
    const warnings = [warning("printers[0].id", "must be unique"), warning("materials[0].id", "must be unique")];
    expect(rowProblems("materials", 1, ["id"], warnings)).toEqual([
      { row: 1, field: "materials[0].id", message: warnFor(warnings, "materials[0].id") },
    ]);
  });

  it("skips rows/suffixes with no warning, leaving no empty-message entries", () => {
    const warnings = [warning("printers[1].id", "must be unique")];
    const result = rowProblems("printers", 3, ["id", "filamentDiameterMm"], warnings);
    expect(result).toHaveLength(1);
    expect(result[0]).toEqual({ row: 2, field: "printers[1].id", message: warnFor(warnings, "printers[1].id") });
  });
});

describe("warnId", () => {
  it("turns a field path into a DOM-safe id", () => {
    expect(warnId("materials[0].id")).toBe("warn-materials-0-id");
  });

  it("collapses runs of non-alphanumeric characters into one hyphen", () => {
    expect(warnId("hardware[12].unitCost")).toBe("warn-hardware-12-unitCost");
  });
});
