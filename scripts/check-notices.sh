#!/usr/bin/env bash
# Fails when a Go module linked into the server binary is missing from
# THIRD_PARTY_NOTICES.md or is listed at a different version (CONTRIBUTING.md rule 3).
# Mirrors the regeneration command in the notices file: go list -deps from server/.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
notices="${NOTICES_FILE:-$root/THIRD_PARTY_NOTICES.md}"
fail=0
while IFS='@' read -r path version; do
  [ -n "$path" ] && [ -n "$version" ] || continue
  # Pseudo-versions (v0.0.0-<timestamp>-<hash>) are listed as "pseudo-version <timestamp>-<hash>".
  want="$version"
  case "$version" in v0.0.0-*) want="${version#v0.0.0-}" ;; esac
  if ! grep -E '^### ' "$notices" | grep -F -- "$path" | grep -qF -- "$want"; then
    echo "check-notices: $path@$version is linked but not listed at that version in THIRD_PARTY_NOTICES.md" >&2
    fail=1
  fi
done < <(cd "$root/server" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go list -deps \
  -f '{{if .Module}}{{if not .Module.Main}}{{.Module.Path}}@{{.Module.Version}}{{end}}{{end}}' . | sort -u)
[ "$fail" -eq 0 ] && echo "check-notices: THIRD_PARTY_NOTICES.md matches the linked Go modules"
exit "$fail"
