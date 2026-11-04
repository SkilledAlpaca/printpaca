import { describe, expect, it } from "vitest";
import { validateItemForm, type ItemFormShape } from "./itemFormValidation";

function baseForm(overrides: Partial<ItemFormShape> = {}): ItemFormShape {
  return {
    price: 10,
    stockMode: "ready_to_print",
    stockQty: null,
    groups: [],
    ...overrides,
  };
}

describe("validateItemForm", () => {
  it("accepts a valid ready-to-print form", () => {
    expect(validateItemForm(baseForm())).toEqual([]);
  });

  it("accepts price at the exact min/max boundary", () => {
    expect(validateItemForm(baseForm({ price: 0 }))).toEqual([]);
    expect(validateItemForm(baseForm({ price: 1000000 }))).toEqual([]);
  });

  it("rejects a null price", () => {
    expect(validateItemForm(baseForm({ price: null }))).toEqual(["Price: enter a valid amount."]);
  });

  it("rejects non-finite prices instead of letting them through to roundCents (which maps them to 0)", () => {
    expect(validateItemForm(baseForm({ price: 1e308 }))).toEqual(["Price: enter a valid amount."]);
    expect(validateItemForm(baseForm({ price: Infinity }))).toEqual(["Price: enter a valid amount."]);
    expect(validateItemForm(baseForm({ price: -Infinity }))).toEqual(["Price: enter a valid amount."]);
    expect(validateItemForm(baseForm({ price: NaN }))).toEqual(["Price: enter a valid amount."]);
  });

  it("rejects a price outside [0, 1000000]", () => {
    expect(validateItemForm(baseForm({ price: -1 }))).toEqual(["Price: enter a valid amount."]);
    expect(validateItemForm(baseForm({ price: 1000000.01 }))).toEqual(["Price: enter a valid amount."]);
  });

  it("only checks stockQty when stockMode is in_stock", () => {
    expect(validateItemForm(baseForm({ stockMode: "ready_to_print", stockQty: null }))).toEqual([]);
    expect(validateItemForm(baseForm({ stockMode: "ready_to_print", stockQty: 1e308 }))).toEqual([]);
  });

  it("rejects a null, non-finite, or out-of-range stockQty when in_stock", () => {
    expect(validateItemForm(baseForm({ stockMode: "in_stock", stockQty: null }))).toEqual([
      "Quantity in stock: enter a valid amount.",
    ]);
    expect(validateItemForm(baseForm({ stockMode: "in_stock", stockQty: 1e308 }))).toEqual([
      "Quantity in stock: enter a valid amount.",
    ]);
    expect(validateItemForm(baseForm({ stockMode: "in_stock", stockQty: -1 }))).toEqual([
      "Quantity in stock: enter a valid amount.",
    ]);
    expect(validateItemForm(baseForm({ stockMode: "in_stock", stockQty: 1000000.01 }))).toEqual([
      "Quantity in stock: enter a valid amount.",
    ]);
  });

  it("accepts a valid in_stock quantity", () => {
    expect(validateItemForm(baseForm({ stockMode: "in_stock", stockQty: 5 }))).toEqual([]);
  });

  const grp = (options: { name: string; delta: number | null }[], label = "Color") => baseForm({ groups: [{ label, options }] });

  it("rejects a null, non-finite, or out-of-range option delta", () => {
    expect(validateItemForm(grp([{ name: "Red", delta: null }]))).toEqual([
      "Option group 1 (Color), choice 1 (Red): enter a valid price adjustment.",
    ]);
    for (const delta of [1e308, -1000000.01, 1000000.01]) {
      expect(validateItemForm(grp([{ name: "Red", delta }]))).toEqual([
        "Option group 1 (Color), choice 1 (Red): enter a valid price adjustment.",
      ]);
    }
  });

  it("accepts option deltas at the exact min/max boundary", () => {
    expect(validateItemForm(grp([{ name: "A", delta: -1000000 }]))).toEqual([]);
    expect(validateItemForm(grp([{ name: "A", delta: 1000000 }]))).toEqual([]);
  });

  it("requires a group label, at least one option, and an option name", () => {
    expect(validateItemForm(grp([{ name: "A", delta: 0 }], " "))).toEqual(["Option group 1: enter a label."]);
    expect(validateItemForm(grp([]))).toEqual(["Option group 1 (Color): add at least one choice."]);
    expect(validateItemForm(grp([{ name: " ", delta: 0 }]))).toEqual(["Option group 1 (Color), choice 1: enter a name."]);
  });

  it("rejects duplicate option names in a group and duplicate group labels, ignoring case", () => {
    expect(
      validateItemForm(
        grp([
          { name: "Red", delta: 0 },
          { name: "red", delta: 0 },
        ]),
      ),
    ).toEqual(["Option group 1 (Color), choice 2 (red): another choice in this group has the same name."]);
    const two = baseForm({
      groups: [
        { label: "Color", options: [{ name: "A", delta: 0 }] },
        { label: "color", options: [{ name: "A", delta: 0 }] },
      ],
    });
    expect(validateItemForm(two)).toEqual(["Option group 2 (color): another group already uses this label."]);
    // The same option name in different groups is fine.
    expect(validateItemForm(baseForm({ groups: [
      { label: "Color", options: [{ name: "A", delta: 0 }] },
      { label: "Size", options: [{ name: "A", delta: 0 }] },
    ] }))).toEqual([]);
  });

  it("collects multiple errors across fields and options", () => {
    expect(
      validateItemForm(
        baseForm({
          price: null,
          stockMode: "in_stock",
          stockQty: null,
          groups: [{ label: "Color", options: [{ name: "A", delta: null }, { name: "", delta: 1e308 }] }],
        }),
      ),
    ).toEqual([
      "Price: enter a valid amount.",
      "Quantity in stock: enter a valid amount.",
      "Option group 1 (Color), choice 1 (A): enter a valid price adjustment.",
      "Option group 1 (Color), choice 2: enter a name.",
      "Option group 1 (Color), choice 2: enter a valid price adjustment.",
    ]);
  });
});
