import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: ".",
  // Absolute path, so `npm run e2e` from the root and `playwright test` from e2e/ share one output dir.
  outputDir: new URL("test-results", import.meta.url).pathname,
  timeout: 30_000,
  // All specs share one server and database and some write records directly, so run serially.
  fullyParallel: false,
  workers: 1,
  retries: 0,
  // Port 8099, not the default 8080, so reuseExistingServer can only reuse an e2e-launched instance,
  // never a developer's real deployment.
  use: { baseURL: "http://127.0.0.1:8099" },
  // The main port serves the shop (/ and /order) and the admin app (/admin/); the specs reach
  // the admin app under /admin/.
  // Three servers, each with its own throwaway CONFIG_DIR: 8099 (baseURL), 9099 with ADMIN_PASSWORD set
  // (auth.spec.ts), and 9199 with MOONRAKER_URL/FLUIDD_URL (moonraker.spec.ts). PUBLIC_PORT is set on
  // all so the default 8098 never collides. The 8099 and 9199 servers set ADMIN_PASSWORD="" explicitly,
  // because an unset password makes the server generate and enforce a random one.
  webServer: [
    {
      command: "bash ../scripts/e2e-server.sh",
      url: "http://127.0.0.1:8099/healthz",
      // The public listener, alongside the main port on 8099, for public.spec.ts. ADMIN_TRUST_PROXY lets
      // front-door.spec.ts order from its own X-Forwarded-For address, outside the per-IP submission budget
      // that the public-listener specs (same server, same budget) already use up.
      env: { PORT: "8099", PUBLIC_PORT: "8098", ADMIN_PASSWORD: "", ADMIN_TRUST_PROXY: "1" },
      timeout: 180_000,
      reuseExistingServer: !process.env.CI,
    },
    {
      command: "bash ../scripts/e2e-server.sh",
      url: "http://127.0.0.1:9099/healthz",
      env: { PORT: "9099", PUBLIC_PORT: "9098", ADMIN_PASSWORD: "e2e-admin-secret" },
      timeout: 180_000,
      reuseExistingServer: !process.env.CI,
    },
    // The stub fakes Moonraker on 9125. FLUIDD_URL (9126) is link-only (scheme/host shape is checked,
    // not reachability), so nothing listens there.
    {
      command: "bash ../scripts/e2e-server.sh",
      url: "http://127.0.0.1:9199/healthz",
      env: {
        PORT: "9199",
        PUBLIC_PORT: "9198",
        ADMIN_PASSWORD: "",
        MOONRAKER_URL: "http://127.0.0.1:9125",
        FLUIDD_URL: "http://127.0.0.1:9126",
      },
      timeout: 180_000,
      reuseExistingServer: !process.env.CI,
    },
    // 9125 is distinct from every other port and from Spoolman's usual 7912.
    {
      command: "node ../scripts/e2e-moonraker-stub.mjs",
      url: "http://127.0.0.1:9125/healthz",
      env: { PORT: "9125" },
      timeout: 30_000,
      reuseExistingServer: !process.env.CI,
    },
  ],
});
