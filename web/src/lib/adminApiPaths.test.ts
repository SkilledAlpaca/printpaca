import { describe, expect, it } from "vitest";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative } from "node:path";

// The admin app is served from /admin/ and must call its API with relative URLs ("api/quotes", resolved
// against /admin/), so one Cloudflare Access application scoped to /admin covers the whole admin surface. An
// absolute "/api/..." in the admin code would hit the main port's /api 404 instead. Only the public
// page's "/api/public/..." calls stay absolute.

const SRC_DIR = join(import.meta.dirname, "..");

function sourceFiles(dir: string): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      out.push(...sourceFiles(full));
    } else if (/\.(ts|svelte|js)$/.test(entry) && !/\.(test|spec)\.(ts|js)$/.test(entry)) {
      out.push(full);
    }
  }
  return out;
}

// A string or template literal that starts with /api/ and is not /api/public/.
const ABSOLUTE_ADMIN_API = /(["'`])\/api\/(?!public\/)/;

describe("admin API paths", () => {
  it("never uses an absolute /api/ string or template literal outside /api/public/", () => {
    const offenders: string[] = [];
    for (const file of sourceFiles(SRC_DIR)) {
      readFileSync(file, "utf8")
        .split("\n")
        .forEach((line, i) => {
          const trimmed = line.trim();
          if (trimmed.startsWith("//") || trimmed.startsWith("*") || trimmed.startsWith("/*")) return;
          if (ABSOLUTE_ADMIN_API.test(line)) {
            offenders.push(`${relative(SRC_DIR, file)}:${i + 1}: ${trimmed}`);
          }
        });
    }
    expect(
      offenders,
      'Admin calls must be relative ("api/..."), not "/api/...": the admin app is served from /admin/.\n' +
        offenders.join("\n"),
    ).toEqual([]);
  });
});
