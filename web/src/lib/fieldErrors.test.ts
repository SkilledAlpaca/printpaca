import { describe, expect, it } from "vitest";
import { errorLabel } from "./fieldErrors";

// A "Label: message" error is linked to the control with that label (WCAG 3.3.1); the label is the text before the colon.
describe("errorLabel", () => {
  it("returns the text before the first colon", () => {
    expect(errorLabel("Price: enter a valid amount.")).toBe("Price");
    expect(errorLabel("Quantity in stock: enter a valid amount.")).toBe("Quantity in stock");
  });

  it("returns null for a message with no label", () => {
    expect(errorLabel("Couldn't save item.")).toBeNull();
    expect(errorLabel(": no label")).toBeNull();
  });
});
