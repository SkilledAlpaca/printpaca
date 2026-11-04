import { beforeEach, describe, expect, it } from "vitest";
import type { FieldWarning, OrderBreakdown } from "./engine/index";
import { quote, orderStamp, type PartForm } from "./state.svelte";
import { currentBlockers } from "./currentBlockers";

function w(field: string): FieldWarning {
  return { field, code: "required", message: "x", severity: "error" };
}
function order(partWarnings: FieldWarning[][]): OrderBreakdown {
  return {
    parts: partWarnings.map((warnings) => ({ label: "", breakdown: { warnings, valid: warnings.length === 0 } })),
    pricing: { warnings: [], valid: false },
    warnings: [],
    valid: false,
  } as unknown as OrderBreakdown;
}
function setParts(...ps: Array<[string, string]>) {
  quote.parts = ps.map(([id, label]) => ({ id, label }) as PartForm);
}

describe("currentBlockers", () => {
  beforeEach(() => {
    orderStamp.partIds = [];
  });

  it("follows the part by id when a part was inserted since the answer", () => {
    // The answer was computed for [bracket, lid, spacers]; the editor now has a copy at index 1.
    orderStamp.partIds = ["b", "l", "s"];
    setParts(["b", "bracket"], ["b2", "bracket (copy)"], ["l", "lid"], ["s", "spacers"]);
    const [blocker] = currentBlockers(order([[], [w("weightG")], []]));
    expect(blocker).toMatchObject({ partId: "l", partIndex: 1 });
    expect(blocker?.label).toBe("Model weight (lid)");
  });

  it("drops a blocker whose part was removed", () => {
    orderStamp.partIds = ["b", "l"];
    setParts(["b", "bracket"]);
    expect(currentBlockers(order([[], [w("weightG")]]))).toEqual([]);
  });

  it("falls back to the editor's parts before any answer is stamped", () => {
    setParts(["b", "bracket"], ["l", "lid"]);
    expect(currentBlockers(order([[], [w("weightG")]]))[0]?.partId).toBe("l");
  });
});
