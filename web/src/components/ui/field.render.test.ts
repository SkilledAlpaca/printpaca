import { describe, expect, it } from "vitest";
import { createRawSnippet } from "svelte";
import { render } from "svelte/server";
import Field from "./Field.svelte";
import { HINT_MODE_KEY } from "./hintMode";

// Server-render Field to check which of the two hint presentations it picks.
const control = createRawSnippet(() => ({ render: () => "<input />" }));

function html(props: Record<string, unknown>, mode?: "visible" | "tooltip"): string {
  const context = mode ? new Map<unknown, unknown>([[HINT_MODE_KEY, mode]]) : undefined;
  return render(Field, { props: { id: "f", label: "Waste (%)", children: control, ...props }, context }).body;
}

describe("Field hint presentation", () => {
  it("puts the hint in a tooltip behind a Help button by default, and keeps it in the DOM for aria-describedby", () => {
    const out = html({ hint: "Extra filament." });
    expect(out).toContain('Help: Waste (%)');
    expect(out).toMatch(/role="tooltip"[^>]*id="f-hint"|id="f-hint"[^>]*role="tooltip"/);
    expect(out).toContain("Extra filament.");
    expect(out).not.toMatch(/class="hint-text[ "]/);
  });
  it("keeps the hint visible with hintVisible", () => {
    const out = html({ hint: "Env override.", hintVisible: true });
    expect(out).toMatch(/class="hint-text[ "]/);
    expect(out).toContain('id="f-hint"');
    expect(out).not.toContain("Help:");
  });
  it("keeps every hint visible under a visible hint mode (the customer shop)", () => {
    const out = html({ hint: "Optional." }, "visible");
    expect(out).toMatch(/class="hint-text[ "]/);
    expect(out).not.toContain("Help:");
  });
  it("renders no Help button when there is no hint, and always renders the error", () => {
    const out = html({ error: "Too small." });
    expect(out).not.toContain("Help:");
    expect(out).toMatch(/class="error-text[ "]/);
    expect(out).toContain("Too small.");
  });
});
