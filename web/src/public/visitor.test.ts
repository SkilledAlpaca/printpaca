import { describe, expect, it } from "vitest";
import { clearVisitor, visitor } from "./visitor.svelte";

describe("visitor store", () => {
  it("keeps name and email until a submit succeeds, then forgets them", () => {
    visitor.name = "Casey";
    visitor.email = "casey@example.com";
    expect(visitor.name).toBe("Casey");
    clearVisitor();
    expect(visitor.name).toBe("");
    expect(visitor.email).toBe("");
  });
});
