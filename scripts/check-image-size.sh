#!/usr/bin/env bash
# Fail if the image's real uncompressed size exceeds the 20 MiB gate.
#
# `docker image inspect` .Size is unreliable on containerd-backed daemons: it
# reports compressed layer blobs, not the rootfs size the gate bounds.
# `docker history` has no raw byte field. Instead, `docker create` (which
# never runs the entrypoint) plus `docker export` streams the real
# decompressed rootfs as one tar, correct for any storage driver. Tar headers
# and padding add a few hundred bytes, the right direction to round for a gate.
set -euo pipefail
image="${1:-printpaca}"

if ! command -v docker >/dev/null 2>&1; then
  echo "need docker on PATH" >&2
  exit 1
fi
cid=$(docker create "$image")
trap 'docker rm -f "$cid" >/dev/null 2>&1 || true' EXIT
size=$(docker export "$cid" | wc -c)

echo "image size (uncompressed rootfs): $(( size / 1024 / 1024 )) MiB (${size} bytes), gate: 20 MiB"
if [ "$size" -gt $(( 20 * 1024 * 1024 )) ]; then
  echo "FAIL: image exceeds the 20 MiB gate" >&2
  exit 1
fi
