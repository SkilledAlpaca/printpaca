import { describe, expect, it } from "vitest";
import { materialDescription } from "./materialInfo";

describe("materialDescription", () => {
  it("matches common materials by name, case-insensitively", () => {
    expect(materialDescription("PLA")).toMatch(/everyday/);
    expect(materialDescription("pla+ matte")).toMatch(/everyday/);
    expect(materialDescription("PETG")).toMatch(/Tough/);
    expect(materialDescription("TPU 95A")).toMatch(/Rubber/);
  });
  it("returns an empty string for an unknown material", () => {
    expect(materialDescription("Unobtainium")).toBe("");
  });
});
