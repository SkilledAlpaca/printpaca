import { describe, expect, it } from "vitest";
import { publicViewFor } from "./publicRoute";

describe("publicViewFor", () => {
  it("shows the shop only at /order", () => {
    expect(publicViewFor("/order")).toBe("order");
    expect(publicViewFor("/")).toBe("front-door");
    expect(publicViewFor("/anything-else")).toBe("front-door");
    expect(publicViewFor("/order/")).toBe("front-door");
  });
});
