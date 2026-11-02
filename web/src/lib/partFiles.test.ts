import { describe, expect, it } from "vitest";
import { partLabelFromFile } from "./partFiles";

describe("partLabelFromFile", () => {
  it("strips the model extension", () => {
    expect(partLabelFromFile("bracket.gcode")).toBe("bracket");
    expect(partLabelFromFile("Lid v2.STL")).toBe("Lid v2");
    expect(partLabelFromFile("box.3mf")).toBe("box");
  });
  it("keeps the name when nothing would be left", () => {
    expect(partLabelFromFile(".gcode")).toBe(".gcode");
  });
  it("caps the label at the server limit", () => {
    expect(partLabelFromFile(`${"x".repeat(200)}.gcode`)).toHaveLength(120);
  });
});
