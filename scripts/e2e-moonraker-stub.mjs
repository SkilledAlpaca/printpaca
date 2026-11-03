#!/usr/bin/env node
// Dependency-free Moonraker stub for e2e/moonraker.spec.ts, started as its own
// webServer in e2e/playwright.config.ts. It serves only the two paths in
// server/proxy.go's moonrakerAllowedPaths (/server/history/list and
// /server/files/metadata) plus /printer/info; anything else 404s.
//
// Three completed jobs with distinct figures, following the priority rules in
// web/src/lib/moonraker.ts:
//   - job-benchy: metadata.filament_weight_total is reported directly, and
//     job.print_duration wins over metadata.estimated_time.
//   - job-vase: metadata.filament_total (mm) wins over the job's filament_used
//     and derives weight from length; print_duration wins on time.
//   - job-cube: no metadata filament figures, so weight falls back to the job's
//     filament_used; print_duration is 0 (unusable), so time falls back to
//     metadata.estimated_time.
// Fifty-seven older "bulk_NN.gcode" jobs follow, so the history has 60 entries and the picker's second page
// ("Load more", 50 per page) is reachable. /server/history/list honours limit and start, newest first.
// Expected prefilled numbers are worked out in e2e/moonraker.spec.ts.

import { createServer } from "node:http";

const PORT = Number(process.env.PORT ?? process.argv[2] ?? 9125);

const JOBS = [
  {
    job_id: "job-benchy",
    filename: "benchy.gcode",
    status: "completed",
    filament_used: 3000, // mm; unused, metadata.filament_weight_total wins
    print_duration: 5400, // s = 1h30m
    total_duration: 5700,
    end_time: 1751000000,
  },
  {
    job_id: "job-vase",
    filename: "vase_tall.gcode",
    status: "completed",
    filament_used: 5000, // mm; unused, metadata.filament_total wins
    print_duration: 9000, // s = 2h30m
    total_duration: 9300,
    end_time: 1751100000,
  },
  {
    job_id: "job-cube",
    filename: "calibration_cube.gcode",
    status: "completed",
    filament_used: 2500, // mm; used, metadata has no length or weight
    print_duration: 0, // unusable (must be > 0): forces the estimated_time
    // fallback below
    total_duration: 1900,
    end_time: 1751200000,
  },
];

for (let i = 1; i <= 57; i++) {
  JOBS.push({
    job_id: `job-bulk-${String(i).padStart(2, "0")}`,
    filename: `bulk_${String(i).padStart(2, "0")}.gcode`,
    status: "completed",
    filament_used: 1000,
    print_duration: 600,
    total_duration: 650,
    end_time: 1750000000 - i * 100,
  });
}
JOBS.sort((a, b) => b.end_time - a.end_time);

// Keyed by filename, the same way GET /server/files/metadata?filename=...
// looks a job's metadata up.
const METADATA = {
  "benchy.gcode": {
    filament_weight_total: 42.5, // g, reported directly
    estimated_time: 5000, // s; unused, job.print_duration wins
  },
  "vase_tall.gcode": {
    filament_total: 4000, // mm; derived into weight
    estimated_time: 8000, // s; unused, job.print_duration wins
  },
  "calibration_cube.gcode": {
    estimated_time: 1800, // s = 0h30m; the only usable time for this job
  },
};

function sendJson(res, status, body) {
  const payload = JSON.stringify(body);
  res.writeHead(status, {
    "Content-Type": "application/json",
    "Content-Length": Buffer.byteLength(payload),
  });
  res.end(payload);
}

const server = createServer((req, res) => {
  const url = new URL(req.url ?? "/", `http://127.0.0.1:${PORT}`);

  if (url.pathname === "/healthz") {
    res.writeHead(200);
    res.end("ok");
    return;
  }

  if (url.pathname === "/server/history/list") {
    const limit = Number(url.searchParams.get("limit") ?? JOBS.length);
    const start = Number(url.searchParams.get("start") ?? 0);
    sendJson(res, 200, { result: { count: JOBS.length, jobs: JOBS.slice(start, start + limit) } });
    return;
  }

  if (url.pathname === "/printer/info") {
    sendJson(res, 200, {
      result: { hostname: "voron-e2e", state: "ready", state_message: "Printer is ready", software_version: "v0.12.0" },
    });
    return;
  }

  if (url.pathname === "/server/files/metadata") {
    const filename = url.searchParams.get("filename") ?? "";
    sendJson(res, 200, { result: METADATA[filename] ?? {} });
    return;
  }

  res.writeHead(404);
  res.end();
});

server.listen(PORT, "127.0.0.1", () => {
  console.log(`e2e-moonraker-stub listening on 127.0.0.1:${PORT}`);
});
