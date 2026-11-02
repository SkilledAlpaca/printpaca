import { describe, expect, it } from "vitest";
import { describeStorageBanner } from "./storageBanner";

describe("describeStorageBanner", () => {
  it("well under the warn threshold: no banner", () => {
    expect(describeStorageBanner(0, 2 * 1024 ** 3)).toBeNull();
    expect(describeStorageBanner(1024 ** 3, 2 * 1024 ** 3)).toBeNull(); // 50%
  });

  it("just under 80%: still no banner", () => {
    expect(describeStorageBanner(0.799 * 2 * 1024 ** 3, 2 * 1024 ** 3)).toBeNull();
  });

  it("at or over 80%, under 100%: warn level, names the percentage, says what to do", () => {
    const b = describeStorageBanner(0.8 * 2 * 1024 ** 3, 2 * 1024 ** 3);
    expect(b?.level).toBe("warn");
    expect(b?.text).toContain("80%");
    expect(b?.text).toContain("delete old requests");
  });

  it("95% floors, not rounds, so it never claims a threshold it hasn't reached", () => {
    const b = describeStorageBanner(0.959 * 2 * 1024 ** 3, 2 * 1024 ** 3);
    expect(b?.text).toContain("95%");
  });

  it("at exactly the cap: full level, distinct wording, no percentage math implied", () => {
    const b = describeStorageBanner(2 * 1024 ** 3, 2 * 1024 ** 3);
    expect(b?.level).toBe("full");
    expect(b?.text).toContain("full");
    expect(b?.text).toContain("dropped");
  });

  it("over the cap (shouldn't happen, but never divides into something over 100%'s worth of alarm): still full", () => {
    const b = describeStorageBanner(3 * 1024 ** 3, 2 * 1024 ** 3);
    expect(b?.level).toBe("full");
  });

  it("a non-positive cap never produces a banner (nothing meaningful to divide by)", () => {
    expect(describeStorageBanner(100, 0)).toBeNull();
    expect(describeStorageBanner(100, -1)).toBeNull();
  });
});
