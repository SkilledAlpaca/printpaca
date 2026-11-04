import { describe, expect, it } from "vitest";
import { closedPreviewBranding } from "./storefrontPreview";
import { DEFAULT_SHOP_CONFIG } from "./shop";

const quote = { email: " shop@example.com ", phone: "555-0100", address: "1 Main St" };

describe("closedPreviewBranding", () => {
  it("shows the contact details while showContact is on", () => {
    const form = { quote, shop: { ...DEFAULT_SHOP_CONFIG, showContact: true } } as never;
    expect(closedPreviewBranding(form)).toEqual({ email: "shop@example.com", phone: "555-0100", address: "1 Main St" });
  });
  it("omits them while showContact is off", () => {
    const form = { quote, shop: { ...DEFAULT_SHOP_CONFIG, showContact: false } } as never;
    expect(closedPreviewBranding(form)).toEqual({ email: "", phone: "", address: "" });
  });
});
