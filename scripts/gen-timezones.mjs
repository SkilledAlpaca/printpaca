// Regenerates testdata/timezones.json, the zone list behind the Settings time zone picker (D51):
// "UTC" first, then every name Node's Intl reports, sorted. server/timezones_test.go checks that each entry
// loads against the embedded tzdata.
//   podman run --rm -v "$PWD":/w -w /w docker.io/library/node:26 node scripts/gen-timezones.mjs
import { writeFileSync } from "node:fs";

const zones = [...new Set(["UTC", ...Intl.supportedValuesOf("timeZone")])];
zones.sort((a, b) => (a === "UTC" ? -1 : b === "UTC" ? 1 : a < b ? -1 : a > b ? 1 : 0));
const out = new URL("../testdata/timezones.json", import.meta.url);
writeFileSync(out, JSON.stringify(zones, null, 0).replace(/","/g, '",\n  "').replace(/^\[/, "[\n  ").replace(/\]$/, "\n]") + "\n");
console.log(`${zones.length} zones`);
