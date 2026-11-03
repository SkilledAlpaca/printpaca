#!/usr/bin/env node
// Dependency-free, stateful Spoolman stub for e2e/spoolman-inventory.spec.ts and e2e/integrations.spec.ts,
// started as its own webServer in e2e/playwright.config.ts. It serves the paths in server/spoolman_guard.go's
// route table (GET /api/v1/spool, /api/v1/spool/{id}, /api/v1/filament, /api/v1/vendor; POST /api/v1/spool;
// PATCH /api/v1/spool/{id}); anything else 404s. Like Spoolman itself it hides archived spools from the list
// unless allow_archived=true, and it reports the list length in X-Total-Count.
//
// State lives in memory for the life of the process, so specs that write must not assume a fresh stub.

import { createServer } from "node:http";

const PORT = Number(process.env.PORT ?? process.argv[2] ?? 9127);

const VENDORS = [
  { id: 1, name: "Prusament" },
  { id: 2, name: "Overture" },
  { id: 3, name: "Polymaker" },
];

const FILAMENTS = [
  { id: 1, name: "Galaxy Black", material: "PLA", density: 1.24, diameter: 1.75, weight: 1000, price: 29.99, color_hex: "1A1A1A", vendor: VENDORS[0] },
  { id: 2, name: "Blue", material: "PETG", density: 1.27, diameter: 1.75, weight: 1000, price: 22.5, color_hex: "1E50DC", vendor: VENDORS[1] },
  { id: 3, name: "Lava Red", material: "PLA", density: 1.24, diameter: 1.75, weight: 750, price: 19.99, color_hex: "DC1E1E", vendor: VENDORS[2] },
];

const spools = [
  { id: 1, filament: FILAMENTS[0], initial_weight: 1000, remaining_weight: 850, price: 24.99, location: "Shelf A", archived: false },
  { id: 2, filament: FILAMENTS[1], initial_weight: 1000, remaining_weight: 400, price: 22.5, location: "Dry box", archived: false },
  { id: 3, filament: FILAMENTS[2], initial_weight: 750, remaining_weight: 0, price: 19.99, location: "", archived: true },
];
let nextId = 4;

function sendJson(res, status, body, headers = {}) {
  const payload = JSON.stringify(body);
  res.writeHead(status, { "Content-Type": "application/json", "Content-Length": Buffer.byteLength(payload), ...headers });
  res.end(payload);
}

function readJson(req) {
  return new Promise((resolve, reject) => {
    let raw = "";
    req.on("data", (c) => (raw += c));
    req.on("end", () => {
      try {
        resolve(JSON.parse(raw));
      } catch (e) {
        reject(e);
      }
    });
  });
}

const server = createServer(async (req, res) => {
  const url = new URL(req.url ?? "/", `http://127.0.0.1:${PORT}`);
  const path = url.pathname;

  if (path === "/healthz") {
    res.writeHead(200);
    res.end("ok");
    return;
  }
  if (path === "/api/v1/filament" && req.method === "GET") return sendJson(res, 200, FILAMENTS);
  if (path === "/api/v1/vendor" && req.method === "GET") return sendJson(res, 200, VENDORS);

  if (path === "/api/v1/spool" && req.method === "GET") {
    const all = url.searchParams.get("allow_archived") === "true";
    const list = spools.filter((s) => all || !s.archived);
    return sendJson(res, 200, list, { "X-Total-Count": String(list.length) });
  }

  if (path === "/api/v1/spool" && req.method === "POST") {
    try {
      const body = await readJson(req);
      const filament = FILAMENTS.find((f) => f.id === body.filament_id);
      if (!filament) return sendJson(res, 404, { message: "No filament with ID " + body.filament_id });
      const initial = body.initial_weight ?? filament.weight;
      const spool = {
        id: nextId++,
        filament,
        initial_weight: initial,
        remaining_weight: body.remaining_weight ?? initial,
        price: body.price ?? filament.price,
        location: body.location ?? "",
        archived: false,
      };
      spools.push(spool);
      return sendJson(res, 200, spool);
    } catch {
      return sendJson(res, 400, { message: "bad body" });
    }
  }

  const m = /^\/api\/v1\/spool\/(\d+)$/.exec(path);
  if (m) {
    const spool = spools.find((s) => s.id === Number(m[1]));
    if (!spool) return sendJson(res, 404, { message: "No spool with ID " + m[1] });
    if (req.method === "GET") return sendJson(res, 200, spool);
    if (req.method === "PATCH") {
      try {
        Object.assign(spool, await readJson(req));
        return sendJson(res, 200, spool);
      } catch {
        return sendJson(res, 400, { message: "bad body" });
      }
    }
  }

  res.writeHead(404);
  res.end();
});

server.listen(PORT, "127.0.0.1", () => {
  console.log(`e2e-spoolman-stub listening on 127.0.0.1:${PORT}`);
});
