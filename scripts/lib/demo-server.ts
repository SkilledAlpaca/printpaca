// Shared helpers for the documentation capture scripts (scripts/guide-shots.ts): build the web app, compile a
// throwaway server binary with it embedded, and run that binary against a scratch CONFIG_DIR. Mirrors the build
// dance in scripts/e2e-server.sh and scripts/screenshot-docs.ts (go:embed needs web/dist under
// server/embedded/dist at build time; the tracked placeholder is restored afterwards).
import { execFileSync, spawn, type ChildProcess } from "node:child_process";
import { cpSync, readFileSync, rmSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

export const REPO_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "..");
const EMBEDDED_DIST = path.join(REPO_ROOT, "server", "embedded", "dist");

export function run(cmd: string, args: string[], cwd: string): void {
  execFileSync(cmd, args, { cwd, stdio: "inherit" });
}

export function restoreEmbeddedDist(): void {
  for (const args of [["checkout", "--", "server/embedded/dist"], ["clean", "-fdx", "server/embedded/dist"]]) {
    try {
      execFileSync("git", args, { cwd: REPO_ROOT, stdio: "ignore" });
    } catch {
      /* best-effort */
    }
  }
}

/** Builds web/dist and compiles the server binary at `bin`. */
export function buildServer(bin: string): void {
  run("npm", ["run", "build", "-w", "web"], REPO_ROOT);
  rmSync(EMBEDDED_DIST, { recursive: true, force: true });
  cpSync(path.join(REPO_ROOT, "web", "dist"), EMBEDDED_DIST, { recursive: true });
  try {
    const { version } = JSON.parse(readFileSync(path.join(REPO_ROOT, "web", "package.json"), "utf8")) as { version: string };
    run("go", ["build", "-ldflags", `-X main.version=v${version}`, "-o", bin, "."], path.join(REPO_ROOT, "server"));
  } finally {
    restoreEmbeddedDist();
  }
}

export async function waitForHealthz(url: string, timeoutMs = 60_000): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  let lastErr: unknown;
  while (Date.now() < deadline) {
    try {
      const res = await fetch(url);
      if (res.ok) return;
      lastErr = new Error(`${url} -> ${res.status}`);
    } catch (err) {
      lastErr = err;
    }
    await new Promise((r) => setTimeout(r, 250));
  }
  throw new Error(`server never became healthy at ${url}: ${String(lastErr)}`);
}

/** Starts the binary and resolves once /healthz answers. */
export async function startServer(bin: string, env: Record<string, string>, port: number): Promise<ChildProcess> {
  const proc = spawn(bin, [], { cwd: REPO_ROOT, env: { ...process.env, ...env }, stdio: ["ignore", "inherit", "inherit"] });
  const failed = new Promise<never>((_, reject) => proc.on("error", (e) => reject(new Error(`server failed to start: ${String(e)}`))));
  await Promise.race([waitForHealthz(`http://127.0.0.1:${port}/healthz`), failed]);
  failed.catch(() => {});
  return proc;
}

export async function stopServer(proc: ChildProcess | undefined): Promise<void> {
  if (!proc || proc.exitCode !== null) return;
  proc.kill();
  await new Promise((r) => proc.once("exit", r));
}
