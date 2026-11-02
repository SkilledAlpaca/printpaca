#!/usr/bin/env bash
# Build the SPA, compile it into a real Go binary, and run it with a throwaway
# config dir. Used only by the Playwright webServer (e2e/playwright.config.ts).
#
# Runs on PORT 8099, not 8080: e2e/history.spec.ts PUTs a full store overwrite,
# which would wipe a developer's real docker instance if they shared a port.
# The public listener runs on PUBLIC_PORT 8098 for e2e/public.spec.ts.
#
# go:embed bakes the dist into the binary at build time, so the script copies
# the build into server/embedded/dist, compiles, and restores the tracked
# placeholder (see cleanup() below).
set -euo pipefail
cd "$(dirname "$0")/.."

# Playwright starts two instances of this script concurrently (8099, and 9099
# with ADMIN_PASSWORD set for e2e/auth.spec.ts), both building the same
# web/dist and server/embedded/dist. flock serializes that build on a fixed
# lock file under the git common dir (a bare ".git" is a file in a linked
# worktree). The lock is released with `-u` before the final `exec`, because
# exec keeps file descriptors open and would otherwise block the other
# instance for the whole life of the server.
lock_file="$(git rev-parse --git-common-dir)/e2e-build.lock"
exec 200>"$lock_file"
flock 200

npm run build -w web

# A per-port scratch root instead of mktemp -d: the final `exec` skips the EXIT
# trap, so mktemp dirs (20-40 MB each) would accumulate. The rm -rf below
# reclaims the previous run's space.
scratch="${TMPDIR:-/tmp}/printpaca-e2e-${PORT:-8099}"
rm -rf "$scratch"
bin_dir="$scratch/bin"
config_dir="$scratch/config"
mkdir -p "$bin_dir" "$config_dir"
bin="$bin_dir/printpaca-e2e"

# Restore the tracked placeholder on any exit, so a failed `go build` doesn't
# leave the real build in the working tree. `|| true` keeps it harmless
# outside a git worktree.
cleanup() {
  git checkout -- server/embedded/dist >/dev/null 2>&1 || true
  git clean -fdx server/embedded/dist >/dev/null 2>&1 || true
}
trap cleanup EXIT

rm -rf server/embedded/dist
cp -r web/dist server/embedded/dist

(cd server && go build -o "$bin" .)

# Success path: restore now, because `exec` below skips the EXIT trap.
git checkout -- server/embedded/dist >/dev/null 2>&1 || true
git clean -fdx server/embedded/dist >/dev/null 2>&1 || true

# Release the build lock; the two servers then run concurrently.
flock -u 200
exec 200>&-

PORT="${PORT:-8099}" PUBLIC_PORT="${PUBLIC_PORT:-8098}" CONFIG_DIR="$config_dir" exec "$bin"
