import { describe, expect, it } from "vitest";
import {
  missingRequired,
  moveItem,
  optionSummary,
  parseOptionSummary,
  pickDisplayImage,
  selectedDeltaCents,
  selectionsPayload,
  type ChoiceGroup,
} from "./itemOptions";

const groups: ChoiceGroup[] = [
  {
    id: "g1",
    label: "Color",
    required: true,
    options: [
      { id: "o1", name: "Black", deltaCents: 0, imageUrl: "/item-images/black.png" },
      { id: "o2", name: "White", deltaCents: 50 },
    ],
  },
  {
    id: "g2",
    label: "Mount",
    required: false,
    options: [{ id: "o3", name: "Strip", deltaCents: 100, imageUrl: "/item-images/strip.png" }],
  },
];

describe("selection math", () => {
  it("sums the selected deltas and ignores unknown or empty choices", () => {
    expect(selectedDeltaCents(groups, {})).toBe(0);
    expect(selectedDeltaCents(groups, { g1: "o2", g2: "o3" })).toBe(150);
    expect(selectedDeltaCents(groups, { g1: "o2", g2: "" })).toBe(50);
    expect(selectedDeltaCents(groups, { g1: "nope" })).toBe(0);
  });

  it("reports unanswered required groups only", () => {
    expect(missingRequired(groups, {}).map((g) => g.label)).toEqual(["Color"]);
    expect(missingRequired(groups, { g1: "o1" })).toEqual([]);
    expect(missingRequired(groups, { g2: "o3" }).map((g) => g.id)).toEqual(["g1"]);
  });

  it("builds selections and the summary in display order, skipping None", () => {
    const sel = { g2: "o3", g1: "o2" };
    expect(selectionsPayload(groups, sel)).toEqual([
      { groupId: "g1", optionId: "o2" },
      { groupId: "g2", optionId: "o3" },
    ]);
    expect(optionSummary(groups, sel)).toBe("Color: White · Mount: Strip");
    expect(optionSummary(groups, { g1: "o1", g2: "" })).toBe("Color: Black");
    expect(optionSummary(groups, {})).toBe("");
  });
});

describe("photo choice", () => {
  it("shows the first group's photo in display order, not the most recently changed one", () => {
    expect(pickDisplayImage(groups, { g1: "o1" })).toEqual({ url: "/item-images/black.png", alt: "Color: Black" });
    // Choosing a later group's photo does not replace the earlier group's.
    expect(pickDisplayImage(groups, { g1: "o1", g2: "o3" })?.url).toBe("/item-images/black.png");
    // The later group's photo shows when no earlier selection has one.
    expect(pickDisplayImage(groups, { g1: "o2", g2: "o3" })?.url).toBe("/item-images/strip.png");
    expect(pickDisplayImage(groups, { g1: "o1", g2: "" })?.url).toBe("/item-images/black.png");
  });

  it("returns null when nothing selected has a photo", () => {
    expect(pickDisplayImage(groups, { g1: "o2" })).toBeNull();
    expect(pickDisplayImage(groups, {})).toBeNull();
  });
});

describe("moveItem", () => {
  it("moves one step and clamps at the ends", () => {
    expect(moveItem(["a", "b", "c"], 1, -1)).toEqual(["b", "a", "c"]);
    expect(moveItem(["a", "b", "c"], 1, 1)).toEqual(["a", "c", "b"]);
    const list = ["a", "b"];
    expect(moveItem(list, 0, -1)).toBe(list);
    expect(moveItem(list, 1, 1)).toBe(list);
  });
});

describe("parseOptionSummary", () => {
  it("round-trips summaries in any group order", () => {
    expect(parseOptionSummary(groups, "")).toEqual({});
    expect(parseOptionSummary(groups, "Color: White")).toEqual({ g1: "o2" });
    expect(parseOptionSummary(groups, "Mount: Strip · Color: Black")).toEqual({ g1: "o1", g2: "o3" });
  });

  it("rejects unknown, repeated, and malformed summaries", () => {
    for (const s of ["White", "Color: Green", "Color: Black · Color: White", "Color: Black · ", "Mount: Strip ·"]) {
      expect(parseOptionSummary(groups, s)).toBeNull();
    }
  });

  it("handles labels and names that contain the separators", () => {
    const tricky: ChoiceGroup[] = [
      { id: "a", label: "A: B", required: false, options: [{ id: "x", name: "C · D", deltaCents: 0 }] },
      { id: "b", label: "E", required: false, options: [{ id: "y", name: "F", deltaCents: 0 }] },
    ];
    expect(parseOptionSummary(tricky, "A: B: C · D · E: F")).toEqual({ a: "x", b: "y" });
  });
});
