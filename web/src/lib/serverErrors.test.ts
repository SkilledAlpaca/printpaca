import { describe, expect, it } from "vitest";
import { GENERIC_SERVER_ERROR, formatServerErrors, mapServerError, setErrorCurrencySymbol } from "./serverErrors";

describe("mapServerError", () => {
  it("maps a known indexed path and converts cents to dollars", () => {
    expect(mapServerError("lines[0].unitPriceCents: must be 0-100000000").message).toBe(
      "Unit price: must be from $0 to $1,000,000.",
    );
  });
  it("handles the 'between' wording", () => {
    expect(mapServerError("priceCents: must be between 0 and 100000000").message).toBe("Price: must be from $0 to $1,000,000.");
  });
  it("labels password fields", () => {
    expect(mapServerError("new: must be at least 8 characters").message).toBe("New password: must be at least 8 characters.");
  });
  it("passes a path-less sentence through capitalized", () => {
    expect(mapServerError("only draft invoices can be deleted").message).toBe("Only draft invoices can be deleted.");
  });
  it("falls back to a generic sentence with the raw text for unknown paths", () => {
    const m = mapServerError("frobnicate: exploded");
    expect(m.message).toBe(GENERIC_SERVER_ERROR);
    expect(m.details).toBe("frobnicate: exploded");
  });
});

describe("formatServerErrors", () => {
  it("joins mapped messages and appends a details line only for unknown errors", () => {
    expect(formatServerErrors(["discountCents: must be 0-100000000", "bogus: nope"])).toBe(
      `Discount: must be from $0 to $1,000,000. ${GENERIC_SERVER_ERROR} Details: bogus: nope`,
    );
  });
});

describe("currency symbol", () => {
  it("uses the configured symbol in cent ranges", () => {
    setErrorCurrencySymbol("€");
    try {
      expect(mapServerError("priceCents: must be between 0 and 100000000").message).toBe("Price: must be from €0 to €1,000,000.");
      expect(mapServerError("lines[0].unitPriceCents: must be 0-100000000").message).toBe("Unit price: must be from €0 to €1,000,000.");
    } finally {
      setErrorCurrencySymbol("$");
    }
  });
});
