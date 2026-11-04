import { describe, expect, it } from "vitest";
import { describeLogoDataUrl, logoFileProblem, LOGO_MAX_BYTES } from "./logoUpload";

describe("logoUpload", () => {
  it("rejects non-images and oversized SVGs but lets big rasters through to the downscaler", () => {
    expect(logoFileProblem("application/pdf", 10)).not.toBe("");
    expect(logoFileProblem("image/svg+xml", LOGO_MAX_BYTES + 1)).toContain("SVG");
    expect(logoFileProblem("image/svg+xml", 100)).toBe("");
    expect(logoFileProblem("image/png", LOGO_MAX_BYTES * 10)).toBe("");
  });
  it("describes a stored data URL and ignores web URLs", () => {
    const url = "data:image/png;base64," + "A".repeat(4096);
    expect(describeLogoDataUrl(url)).toBe("Uploaded image, 3 KB");
    expect(describeLogoDataUrl("https://example.com/logo.png")).toBe("");
  });
});
